// Blend contract_data -> typed LedgerState decode lives here, in the protocol
// adapter rather than the relay core. Keeping decode in the adapter is what
// makes the protocol self-contained: event decode, state decode, and transform
// all live in one package.
//
// DecodeState is a stateless PURE reducer — (prior, changes, ledgerSeq) -> next.
// The Adapter retains no per-ledger scratch; every carry-over threads through
// *contractsv1.LedgerState (PendingUserPositions carries the one piece of builder
// state that does not otherwise round-trip). Because it keeps no hidden state,
// folding the same input twice yields byte-identical output, and it cannot leak
// map-iteration order or wall-clock reads across ledgers.
package blend

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// DecodeState folds Blend contract_data changes into typed ledger state. It is
// a pure reducer: it rebuilds a fresh in-memory mirror from prior, applies
// changes, and returns the freshly built LedgerState. No DB / network / clock /
// random / map-order; deterministic and run-twice byte-identical.
func (a *Adapter) DecodeState(prior *contractsv1.LedgerState, changes []contractsv1.ContractDataChange, ledgerSeq int64) (*contractsv1.LedgerState, error) {
	next, _ := a.decodeBlendState(prior, changes, ledgerSeq)
	return &next, nil
}

// OwnsContract reports whether contractID belongs to Blend. Ownership is the
// runtime-discovered pool/backstop/oracle set fed in via RegisterContracts; it
// is config-like (not per-ledger scratch), so it does not break DecodeState
// purity.
func (a *Adapter) OwnsContract(contractID string) bool {
	if contractID == "" {
		return false
	}
	_, ok := a.contracts[contractID]
	return ok
}

// RegisterContracts adds discovered contract IDs to the owned set so
// OwnsContract returns true for them. Idempotent; ignores blank IDs. Called by
// the relay's projector edge as it discovers pools (it is NOT called from the
// pure DecodeState path).
func (a *Adapter) RegisterContracts(ids ...string) {
	if a.contracts == nil {
		a.contracts = map[string]struct{}{}
	}
	for _, id := range ids {
		if id != "" {
			a.contracts[id] = struct{}{}
		}
	}
}

type typedStateDelta struct {
	LedgerSeq  int64
	EntityType string
	EntityKey  string
	Live       bool
	StateJSON  json.RawMessage
}

type blendStateBuilder struct {
	pools         map[string]*poolBuilder
	pendingPos    map[string]pendingUserPositions
	backstopPools map[string]backstopPoolBalance
	backstopUsers map[string]backstopUserBalance
	deltas        []typedStateDelta
}

type poolBuilder struct {
	state          contractsv1.PoolState
	reserves       map[string]*reserveBuilder
	reserveList    []string
	reserveByIndex map[int32]string
}

type reserveBuilder struct {
	state contractsv1.ReserveState
}

type pendingUserPositions struct {
	poolContract string
	user         string
	positions    xdr.ScVal
	valueXDR     string
}

type backstopPoolBalance struct {
	poolContract string
	sharesRaw    string
	tokensRaw    string
	q4wRaw       string
}

type backstopUserBalance struct {
	poolContract string
	user         string
	sharesRaw    string
	q4w          []contractsv1.Q4WEntry
}

func newBlendStateBuilder() *blendStateBuilder {
	return &blendStateBuilder{
		pools:         map[string]*poolBuilder{},
		pendingPos:    map[string]pendingUserPositions{},
		backstopPools: map[string]backstopPoolBalance{},
		backstopUsers: map[string]backstopUserBalance{},
	}
}

// decodeBlendState is the pure-reducer core shared by DecodeState (which returns
// only the LedgerState) and the in-package tests (which assert the sorted
// Deltas). It rebuilds the mirror from prior, folds changes, and returns the
// built state plus the silver-debug deltas.
func (a *Adapter) decodeBlendState(prior *contractsv1.LedgerState, changes []contractsv1.ContractDataChange, ledgerSeq int64) (contractsv1.LedgerState, []typedStateDelta) {
	b := newBlendStateBuilder()
	if prior != nil {
		b.loadPrior(prior)
	}
	for _, change := range changes {
		b.apply(change, ledgerSeq)
	}

	// The deltas are appended from map-range iteration over the builder maps
	// (appendPoolReserves / appendPoolUsers / appendBackstopUsersForPool), and Go
	// map order is randomized, so two runs over the same ledgers would otherwise
	// emit them in different orders. Sort by a stable total-order key before emit
	// so the output is byte-identical run to run.
	sort.SliceStable(b.deltas, func(i, j int) bool {
		di, dj := b.deltas[i], b.deltas[j]
		if di.EntityType != dj.EntityType {
			return di.EntityType < dj.EntityType
		}
		if di.EntityKey != dj.EntityKey {
			return di.EntityKey < dj.EntityKey
		}
		if di.LedgerSeq != dj.LedgerSeq {
			return di.LedgerSeq < dj.LedgerSeq
		}
		if di.Live != dj.Live {
			return !di.Live && dj.Live
		}
		return string(di.StateJSON) < string(dj.StateJSON)
	})

	return b.build(), b.deltas
}

// loadPrior reconstructs the mirror from the prior LedgerState so the reducer
// keeps no state of its own between ledgers. Pools/reserves come from
// prior.Pools, backstop balances from prior.Backstops, and raw user-position
// blobs from prior.PendingUserPositions.
func (b *blendStateBuilder) loadPrior(prior *contractsv1.LedgerState) {
	for _, pool := range prior.Pools {
		pb := ensurePool(b.pools, pool.ContractID)
		pb.state = pool
		for _, reserve := range pool.Reserves {
			pb.reserves[reserve.AssetID] = &reserveBuilder{state: reserve}
		}
		finalizePoolReserves(pb)
	}
	for _, pending := range prior.PendingUserPositions {
		value, ok := decodeScValBase64(pending.PositionsXDR)
		if !ok {
			continue
		}
		b.pendingPos[typedUserEntityKey(pending.Address, pending.PoolContractID)] = pendingUserPositions{
			poolContract: pending.PoolContractID,
			user:         pending.Address,
			positions:    value,
			valueXDR:     pending.PositionsXDR,
		}
	}
	for _, backstop := range prior.Backstops {
		// Backstop pool-level balance round-trips via any of its users.
		b.backstopPools[backstop.PoolContractID] = backstopPoolBalance{
			poolContract: backstop.PoolContractID,
			sharesRaw:    backstop.PoolSharesRaw,
			tokensRaw:    backstop.PoolTokensRaw,
		}
		b.backstopUsers[typedBackstopEntityKey(backstop.Address, backstop.PoolContractID)] = backstopUserBalance{
			poolContract: backstop.PoolContractID,
			user:         backstop.Address,
			sharesRaw:    backstop.UserSharesRaw,
			q4w:          backstop.Q4W,
		}
	}
}

// build assembles the typed LedgerState from the mirror, sorting every slice so
// the output is byte-identical when the same input is folded twice.
func (b *blendStateBuilder) build() contractsv1.LedgerState {
	pools := make([]contractsv1.PoolState, 0, len(b.pools))
	users := make([]contractsv1.UserReservePosition, 0)
	pending := make([]contractsv1.PendingUserPosition, 0, len(b.pendingPos))
	backstops := make([]contractsv1.BackstopPosition, 0, len(b.backstopUsers))

	for _, pool := range b.pools {
		finalizePoolReserves(pool)
		pools = append(pools, pool.state)
	}
	for _, p := range b.pendingPos {
		// Raw blob round-trips regardless of whether the pool is known yet, so a
		// position decoded before its pool appears is not lost.
		pending = append(pending, contractsv1.PendingUserPosition{
			Address:        p.user,
			PoolContractID: p.poolContract,
			PositionsXDR:   p.valueXDR,
		})
		pool := b.pools[p.poolContract]
		if pool == nil {
			continue
		}
		finalizePoolReserves(pool)
		users = append(users, buildUserPositionsForPending(pool, p)...)
	}
	for _, userBalance := range b.backstopUsers {
		backstops = append(backstops, b.backstopPosition(userBalance))
	}

	sortLedgerState(pools, users, backstops)
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Address != pending[j].Address {
			return pending[i].Address < pending[j].Address
		}
		return pending[i].PoolContractID < pending[j].PoolContractID
	})

	return contractsv1.LedgerState{
		Pools:                pools,
		Users:                users,
		Backstops:            backstops,
		PendingUserPositions: pending,
	}
}

func (b *blendStateBuilder) apply(change contractsv1.ContractDataChange, ledgerSeq int64) {
	key, ok := decodeScValBase64(change.KeyXDR)
	if !ok {
		return
	}

	// An entry is live only if the change says so AND its TTL has not lapsed.
	// Live=false covers eviction (the relay extract sets it from the close meta's
	// evicted-key set, which is reported separately from the change stream);
	// LiveUntilLedgerSeq < ledgerSeq covers TTL expiry. Either makes the entry
	// not-live, so we apply it as a delete — otherwise evicted or expired state
	// would read as live forever.
	live := change.Live && change.ValueXDR != nil
	if change.LiveUntilLedgerSeq != nil && int64(*change.LiveUntilLedgerSeq) < ledgerSeq {
		live = false
	}
	if !live {
		b.applyDelete(change, key, ledgerSeq)
		return
	}

	value, ok := decodeScValBase64(*change.ValueXDR)
	if !ok {
		return
	}

	if wasmHash, ok := contractInstanceWasmHash(value); ok {
		pool := ensurePool(b.pools, change.ContractID)
		pool.state.WasmHash = wasmHash
		b.addDelta(ledgerSeq, "pool", change.ContractID, true, pool.state)
		return
	}

	if sym, ok := scSymbol(key); ok {
		pool := ensurePool(b.pools, change.ContractID)
		switch sym {
		case "Config":
			if !isPoolConfig(value) {
				return
			}
			applyPoolConfig(pool, value)
			b.addDelta(ledgerSeq, "pool", change.ContractID, true, pool.state)
		case "Backstop":
			if address, ok := scAddress(value); ok {
				pool.state.BackstopContract = address
				b.addDelta(ledgerSeq, "pool", change.ContractID, true, pool.state)
			}
		case "ResList":
			pool.reserveList = nil
			applyReserveList(pool, value)
			finalizePoolReserves(pool)
			b.addDelta(ledgerSeq, "pool", change.ContractID, true, pool.state)
			b.appendPoolReserves(change.ContractID, ledgerSeq)
			b.appendPoolUsers(change.ContractID, ledgerSeq)
		}
		return
	}

	variant, args, ok := scVariant(key)
	if !ok {
		return
	}
	switch variant {
	case "ResConfig":
		asset, ok := variantAddress(args)
		if !ok {
			return
		}
		pool := ensurePool(b.pools, change.ContractID)
		applyReserveConfig(ensureReserve(pool, asset), value)
		finalizePoolReserves(pool)
		b.addDelta(ledgerSeq, "reserve", typedReserveEntityKey(change.ContractID, asset), true, pool.reserves[asset].state)
		b.appendPoolUsers(change.ContractID, ledgerSeq)
	case "ResData":
		asset, ok := variantAddress(args)
		if !ok {
			return
		}
		pool := ensurePool(b.pools, change.ContractID)
		applyReserveData(ensureReserve(pool, asset), value)
		finalizePoolReserves(pool)
		b.addDelta(ledgerSeq, "reserve", typedReserveEntityKey(change.ContractID, asset), true, pool.reserves[asset].state)
	case "Positions":
		user, ok := variantAddress(args)
		if !ok {
			return
		}
		pending := pendingUserPositions{poolContract: change.ContractID, user: user, positions: value, valueXDR: *change.ValueXDR}
		b.pendingPos[typedUserEntityKey(user, change.ContractID)] = pending
		b.appendUserPositions(pending, ledgerSeq)
	case "PoolBalance":
		poolID, ok := variantAddress(args)
		if !ok {
			return
		}
		balance := decodeBackstopPoolBalance(poolID, value)
		b.backstopPools[poolID] = balance
		b.addDelta(ledgerSeq, "backstop_pool", poolID, true, typedBackstopPool(balance))
		b.appendBackstopUsersForPool(poolID, ledgerSeq)
	case "UserBalance":
		poolID, user, ok := backstopPoolUser(args)
		if !ok {
			return
		}
		balance := decodeBackstopUserBalance(poolID, user, value)
		b.backstopUsers[typedBackstopEntityKey(user, poolID)] = balance
		b.addDelta(ledgerSeq, "backstop_position", typedBackstopEntityKey(user, poolID), true, b.backstopPosition(balance))
	}
}

func (b *blendStateBuilder) applyDelete(change contractsv1.ContractDataChange, key xdr.ScVal, ledgerSeq int64) {
	if sym, ok := scSymbol(key); ok {
		if sym == "Config" || sym == "ResList" {
			delete(b.pools, change.ContractID)
			b.addDelta(ledgerSeq, "pool", change.ContractID, false, nil)
		} else if sym == "Backstop" {
			if pool := b.pools[change.ContractID]; pool != nil {
				pool.state.BackstopContract = ""
				b.addDelta(ledgerSeq, "pool", change.ContractID, true, pool.state)
			}
		}
		return
	}
	variant, args, ok := scVariant(key)
	if !ok {
		return
	}
	switch variant {
	case "ResConfig", "ResData":
		asset, ok := variantAddress(args)
		if !ok {
			return
		}
		if pool := b.pools[change.ContractID]; pool != nil {
			delete(pool.reserves, asset)
			// reserveByIndex is fully rebuilt below by finalizePoolReserves, so the
			// reserve ordering stays index-stable (the prior code deleted index 0
			// unconditionally here — a bug; dropped).
			finalizePoolReserves(pool)
		}
		b.addDelta(ledgerSeq, "reserve", typedReserveEntityKey(change.ContractID, asset), false, nil)
		b.appendPoolUsers(change.ContractID, ledgerSeq)
	case "Positions":
		user, ok := variantAddress(args)
		if !ok {
			return
		}
		delete(b.pendingPos, typedUserEntityKey(user, change.ContractID))
		b.addDelta(ledgerSeq, "user_positions", typedUserEntityKey(user, change.ContractID), false, nil)
	case "PoolBalance":
		poolID, ok := variantAddress(args)
		if !ok {
			return
		}
		delete(b.backstopPools, poolID)
		b.addDelta(ledgerSeq, "backstop_pool", poolID, false, nil)
		b.appendBackstopUsersForPool(poolID, ledgerSeq)
	case "UserBalance":
		poolID, user, ok := backstopPoolUser(args)
		if !ok {
			return
		}
		delete(b.backstopUsers, typedBackstopEntityKey(user, poolID))
		b.addDelta(ledgerSeq, "backstop_position", typedBackstopEntityKey(user, poolID), false, nil)
	}
}

func (b *blendStateBuilder) appendPoolReserves(poolID string, ledgerSeq int64) {
	pool := b.pools[poolID]
	if pool == nil {
		return
	}
	for asset, reserve := range pool.reserves {
		if reserve.state.AssetID == "" {
			reserve.state.AssetID = asset
		}
		b.addDelta(ledgerSeq, "reserve", typedReserveEntityKey(poolID, asset), true, reserve.state)
	}
}

func (b *blendStateBuilder) appendPoolUsers(poolID string, ledgerSeq int64) {
	for _, pending := range b.pendingPos {
		if pending.poolContract == poolID {
			b.appendUserPositions(pending, ledgerSeq)
		}
	}
}

func (b *blendStateBuilder) appendBackstopUsersForPool(poolID string, ledgerSeq int64) {
	for _, balance := range b.backstopUsers {
		if balance.poolContract != poolID {
			continue
		}
		b.addDelta(ledgerSeq, "backstop_position", typedBackstopEntityKey(balance.user, poolID), true, b.backstopPosition(balance))
	}
}

func (b *blendStateBuilder) appendUserPositions(pending pendingUserPositions, ledgerSeq int64) {
	pool := b.pools[pending.poolContract]
	if pool == nil {
		return
	}
	finalizePoolReserves(pool)
	positions := buildUserPositionsForPending(pool, pending)
	b.addDelta(ledgerSeq, "user_positions", typedUserEntityKey(pending.user, pending.poolContract), true, positions)
}

func buildUserPositionsForPending(pool *poolBuilder, pending pendingUserPositions) []contractsv1.UserReservePosition {
	fields := scMapFields(pending.positions)
	out := make([]contractsv1.UserReservePosition, 0)
	out = append(out, positionsFromMap(pool, pending, fields["supply"], contractsv1.PositionTypeSupply)...)
	out = append(out, positionsFromMap(pool, pending, fields["collateral"], contractsv1.PositionTypeCollateral)...)
	out = append(out, positionsFromMap(pool, pending, fields["liabilities"], contractsv1.PositionTypeLiability)...)
	return out
}

func (b *blendStateBuilder) backstopPosition(userBalance backstopUserBalance) contractsv1.BackstopPosition {
	poolBalance := b.backstopPools[userBalance.poolContract]
	return contractsv1.BackstopPosition{
		Address:              userBalance.user,
		PoolContractID:       userBalance.poolContract,
		UserSharesRaw:        userBalance.sharesRaw,
		PoolSharesRaw:        poolBalance.sharesRaw,
		PoolTokensRaw:        poolBalance.tokensRaw,
		Q4W:                  userBalance.q4w,
		BLNDDecimals:         7,
		USDCDecimals:         7,
		LPTokenSupplyRaw:     "",
		LPBLNDReserveRaw:     "",
		LPUSDCReserveRaw:     "",
		BLNDPriceUSD:         "",
		USDCPriceUSD:         "",
		BackstopInterestAPY:  "",
		BackstopEmissionsAPY: "",
	}
}

// typedBackstopPoolDelta is the silver-debug delta payload for a backstop pool
// balance (exported fields so addDelta can JSON-marshal it deterministically).
type typedBackstopPoolDelta struct {
	PoolContractID string
	SharesRaw      string
	TokensRaw      string
	Q4WRaw         string
}

func typedBackstopPool(balance backstopPoolBalance) typedBackstopPoolDelta {
	return typedBackstopPoolDelta{
		PoolContractID: balance.poolContract,
		SharesRaw:      balance.sharesRaw,
		TokensRaw:      balance.tokensRaw,
		Q4WRaw:         balance.q4wRaw,
	}
}

func (b *blendStateBuilder) addDelta(ledgerSeq int64, entityType, entityKey string, live bool, state any) {
	var raw json.RawMessage
	if state != nil {
		if encoded, err := json.Marshal(state); err == nil {
			raw = encoded
		}
	}
	b.deltas = append(b.deltas, typedStateDelta{
		LedgerSeq:  ledgerSeq,
		EntityType: entityType,
		EntityKey:  entityKey,
		Live:       live,
		StateJSON:  raw,
	})
}

func typedReserveEntityKey(poolID, assetID string) string { return poolID + "|" + assetID }
func typedUserEntityKey(address, poolID string) string    { return address + "|" + poolID }
func typedBackstopEntityKey(address, poolID string) string { return address + "|" + poolID }

func sortLedgerState(pools []contractsv1.PoolState, users []contractsv1.UserReservePosition, backstops []contractsv1.BackstopPosition) {
	sort.Slice(pools, func(i, j int) bool { return pools[i].ContractID < pools[j].ContractID })
	sort.Slice(users, func(i, j int) bool {
		if users[i].Address != users[j].Address {
			return users[i].Address < users[j].Address
		}
		if users[i].PoolContractID != users[j].PoolContractID {
			return users[i].PoolContractID < users[j].PoolContractID
		}
		if users[i].AssetID != users[j].AssetID {
			return users[i].AssetID < users[j].AssetID
		}
		return users[i].PositionType < users[j].PositionType
	})
	sort.Slice(backstops, func(i, j int) bool {
		if backstops[i].Address != backstops[j].Address {
			return backstops[i].Address < backstops[j].Address
		}
		return backstops[i].PoolContractID < backstops[j].PoolContractID
	})
}

func ensurePool(pools map[string]*poolBuilder, contractID string) *poolBuilder {
	pool, ok := pools[contractID]
	if !ok {
		pool = &poolBuilder{
			state: contractsv1.PoolState{
				ContractID: contractID,
				PoolStatus: "unknown",
			},
			reserves:       map[string]*reserveBuilder{},
			reserveByIndex: map[int32]string{},
		}
		pools[contractID] = pool
	}
	return pool
}

func ensureReserve(pool *poolBuilder, assetID string) *reserveBuilder {
	reserve, ok := pool.reserves[assetID]
	if !ok {
		reserve = &reserveBuilder{state: contractsv1.ReserveState{AssetID: assetID}}
		pool.reserves[assetID] = reserve
	}
	return reserve
}

func isPoolConfig(value xdr.ScVal) bool {
	fields := scMapFields(value)
	if fields == nil {
		return false
	}
	_, hasOracle := fieldAddress(fields, "oracle")
	_, hasBackstopTake := fieldIntString(fields, "bstop_rate")
	_, hasStatus := fieldInt32(fields, "status")
	return hasOracle && hasBackstopTake && hasStatus
}

func applyPoolConfig(pool *poolBuilder, value xdr.ScVal) {
	fields := scMapFields(value)
	if oracle, ok := fieldAddress(fields, "oracle"); ok {
		pool.state.OracleContract = oracle
	}
	if bstopRate, ok := fieldIntString(fields, "bstop_rate"); ok {
		pool.state.BackstopTakeRate = bstopRate
	}
	if statusRaw, ok := fieldInt32(fields, "status"); ok {
		pool.state.PoolStatus = blendPoolStatus(statusRaw)
	}
}

func applyReserveList(pool *poolBuilder, value xdr.ScVal) {
	items, ok := scVec(value)
	if !ok {
		return
	}
	seen := map[string]struct{}{}
	for _, item := range items {
		assetID, ok := scAddress(item)
		if !ok {
			continue
		}
		if _, exists := seen[assetID]; exists {
			continue
		}
		seen[assetID] = struct{}{}
		pool.reserveList = append(pool.reserveList, assetID)
		ensureReserve(pool, assetID)
	}
}

func applyReserveConfig(reserve *reserveBuilder, value xdr.ScVal) {
	fields := scMapFields(value)
	if index, ok := fieldInt32(fields, "index"); ok {
		reserve.state.ReserveIndex = index
	}
	if decimals, ok := fieldInt32(fields, "decimals"); ok {
		reserve.state.AssetDecimals = decimals
	}
	if cFactor, ok := fieldIntString(fields, "c_factor"); ok {
		reserve.state.CFactorRaw = cFactor
	}
	if lFactor, ok := fieldIntString(fields, "l_factor"); ok {
		reserve.state.LFactorRaw = lFactor
	}
	if util, ok := fieldIntString(fields, "util"); ok {
		reserve.state.UtilTargetRaw = util
	}
	if maxUtil, ok := fieldIntString(fields, "max_util"); ok {
		reserve.state.MaxUtilRaw = maxUtil
	}
	if rBase, ok := fieldIntString(fields, "r_base"); ok {
		reserve.state.RBaseRaw = rBase
	}
	if rOne, ok := fieldIntString(fields, "r_one"); ok {
		reserve.state.ROneRaw = rOne
	}
	if rTwo, ok := fieldIntString(fields, "r_two"); ok {
		reserve.state.RTwoRaw = rTwo
	}
	if rThree, ok := fieldIntString(fields, "r_three"); ok {
		reserve.state.RThreeRaw = rThree
	}
	if supplyCap, ok := fieldIntString(fields, "supply_cap"); ok {
		reserve.state.SupplyCapRaw = supplyCap
	}
}

func applyReserveData(reserve *reserveBuilder, value xdr.ScVal) {
	fields := scMapFields(value)
	if dRate, ok := fieldIntString(fields, "d_rate"); ok {
		reserve.state.DRateRaw = dRate
	}
	if bRate, ok := fieldIntString(fields, "b_rate"); ok {
		reserve.state.BRateRaw = bRate
	}
	if irMod, ok := fieldIntString(fields, "ir_mod"); ok {
		reserve.state.RateModifierRaw = irMod
	}
	if bSupply, ok := fieldIntString(fields, "b_supply"); ok {
		reserve.state.BSupplyRaw = bSupply
	}
	if dSupply, ok := fieldIntString(fields, "d_supply"); ok {
		reserve.state.DSupplyRaw = dSupply
	}
}

// finalizePoolReserves rebuilds reserveByIndex from scratch and sorts the pool's
// reserves by (ReserveIndex, AssetID) so reserve ordering is index-stable and
// identical run to run.
func finalizePoolReserves(pool *poolBuilder) {
	pool.reserveByIndex = map[int32]string{}
	for assetID, reserve := range pool.reserves {
		if reserve.state.AssetID == "" {
			reserve.state.AssetID = assetID
		}
		pool.reserveByIndex[reserve.state.ReserveIndex] = assetID
	}
	reserves := make([]contractsv1.ReserveState, 0, len(pool.reserves))
	for _, reserve := range pool.reserves {
		reserves = append(reserves, reserve.state)
	}
	sort.Slice(reserves, func(i, j int) bool {
		if reserves[i].ReserveIndex != reserves[j].ReserveIndex {
			return reserves[i].ReserveIndex < reserves[j].ReserveIndex
		}
		return reserves[i].AssetID < reserves[j].AssetID
	})
	pool.state.Reserves = reserves
}

func positionsFromMap(pool *poolBuilder, pending pendingUserPositions, value xdr.ScVal, kind contractsv1.PositionType) []contractsv1.UserReservePosition {
	raw, ok := value.GetMap()
	if !ok || raw == nil {
		return nil
	}
	out := make([]contractsv1.UserReservePosition, 0, len(*raw))
	for _, entry := range *raw {
		index, ok := scInt32(entry.Key)
		if !ok {
			continue
		}
		assetID := pool.reserveByIndex[index]
		if assetID == "" {
			continue
		}
		amount, ok := scIntString(entry.Val)
		if !ok || amount == "0" {
			continue
		}
		pos := contractsv1.UserReservePosition{
			Address:        pending.user,
			PoolContractID: pending.poolContract,
			AssetID:        assetID,
			PositionType:   kind,
		}
		if kind == contractsv1.PositionTypeLiability {
			pos.DTokensRaw = amount
		} else {
			pos.BTokensRaw = amount
		}
		out = append(out, pos)
	}
	return out
}

func decodeBackstopPoolBalance(poolID string, value xdr.ScVal) backstopPoolBalance {
	fields := scMapFields(value)
	balance := backstopPoolBalance{poolContract: poolID}
	if shares, ok := fieldIntString(fields, "shares"); ok {
		balance.sharesRaw = shares
	}
	if tokens, ok := fieldIntString(fields, "tokens"); ok {
		balance.tokensRaw = tokens
	}
	if q4w, ok := fieldIntString(fields, "q4w"); ok {
		balance.q4wRaw = q4w
	}
	return balance
}

func decodeBackstopUserBalance(poolID, user string, value xdr.ScVal) backstopUserBalance {
	fields := scMapFields(value)
	balance := backstopUserBalance{poolContract: poolID, user: user}
	if shares, ok := fieldIntString(fields, "shares"); ok {
		balance.sharesRaw = shares
	}
	items, ok := scVec(fields["q4w"])
	if !ok {
		return balance
	}
	for _, item := range items {
		qFields := scMapFields(item)
		amount, amountOK := fieldIntString(qFields, "amount")
		exp, expOK := fieldIntString(qFields, "exp")
		if !amountOK || !expOK {
			continue
		}
		expUnix, ok := scStringInt64(exp)
		if !ok {
			continue
		}
		balance.q4w = append(balance.q4w, contractsv1.Q4WEntry{
			SharesRaw: amount,
			UnlockAt:  time.Unix(expUnix, 0).UTC(),
		})
	}
	return balance
}

func variantAddress(args []xdr.ScVal) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	return scAddress(args[0])
}

func backstopPoolUser(args []xdr.ScVal) (string, string, bool) {
	if len(args) == 1 {
		fields := scMapFields(args[0])
		poolID, poolOK := fieldAddress(fields, "pool")
		user, userOK := fieldAddress(fields, "user")
		return poolID, user, poolOK && userOK
	}
	if len(args) >= 2 {
		poolID, poolOK := scAddress(args[0])
		user, userOK := scAddress(args[1])
		return poolID, user, poolOK && userOK
	}
	return "", "", false
}

func contractInstanceWasmHash(value xdr.ScVal) (string, bool) {
	instance, ok := value.GetInstance()
	if !ok {
		return "", false
	}
	wasmHash, ok := instance.Executable.GetWasmHash()
	if !ok {
		return "", false
	}
	return xdr.Hash(wasmHash).HexString(), true
}

func blendPoolStatus(status int32) string {
	switch status {
	case 0:
		return "admin_active"
	case 1:
		return "active"
	case 2:
		return "admin_on_ice"
	case 3:
		return "on_ice"
	case 4:
		return "admin_frozen"
	case 5:
		return "frozen"
	case 6:
		return "setup"
	default:
		return "unknown"
	}
}

func scStringInt64(raw string) (int64, bool) {
	var value int64
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
		value = value*10 + int64(ch-'0')
	}
	return value, true
}

// --- scval helpers (state-decode flavor) -----------------------------------
// These complement the event-decode helpers in decode.go (the canonical scval
// home for this adapter); the (string, bool) shapes below thinly wrap decode.go
// (scValAddress / scValSymbol / int128ToString / uint128ToString) so there is a
// single decode implementation, not the relay's duplicated copies.

func scSymbol(v xdr.ScVal) (string, bool) {
	s := scValSymbol(v)
	return s, s != ""
}

func scAddress(v xdr.ScVal) (string, bool) {
	s := scValAddress(v)
	return s, s != ""
}

func scInt64(v xdr.ScVal) (int64, bool) {
	switch v.Type {
	case xdr.ScValTypeScvU32:
		value, ok := v.GetU32()
		return int64(value), ok
	case xdr.ScValTypeScvI32:
		value, ok := v.GetI32()
		return int64(value), ok
	case xdr.ScValTypeScvU64:
		value, ok := v.GetU64()
		if !ok || uint64(value) > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(value), true
	case xdr.ScValTypeScvI64:
		value, ok := v.GetI64()
		return int64(value), ok
	case xdr.ScValTypeScvU128, xdr.ScValTypeScvI128:
		value, ok := scIntString(v)
		if !ok {
			return 0, false
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func scInt32(v xdr.ScVal) (int32, bool) {
	value, ok := scInt64(v)
	if !ok || value < -2147483648 || value > 2147483647 {
		return 0, false
	}
	return int32(value), true
}

func scIntString(v xdr.ScVal) (string, bool) {
	switch v.Type {
	case xdr.ScValTypeScvU32:
		value, ok := v.GetU32()
		if !ok {
			return "", false
		}
		return strconv.FormatUint(uint64(value), 10), true
	case xdr.ScValTypeScvI32:
		value, ok := v.GetI32()
		if !ok {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case xdr.ScValTypeScvU64, xdr.ScValTypeScvTimepoint, xdr.ScValTypeScvDuration:
		var value uint64
		var ok bool
		if v.Type == xdr.ScValTypeScvTimepoint {
			raw, found := v.GetTimepoint()
			value, ok = uint64(raw), found
		} else if v.Type == xdr.ScValTypeScvDuration {
			raw, found := v.GetDuration()
			value, ok = uint64(raw), found
		} else {
			raw, found := v.GetU64()
			value, ok = uint64(raw), found
		}
		if !ok {
			return "", false
		}
		return strconv.FormatUint(value, 10), true
	case xdr.ScValTypeScvI64:
		value, ok := v.GetI64()
		if !ok {
			return "", false
		}
		return strconv.FormatInt(int64(value), 10), true
	case xdr.ScValTypeScvU128:
		value, ok := v.GetU128()
		if !ok {
			return "", false
		}
		return uint128ToString(value), true
	case xdr.ScValTypeScvI128:
		value, ok := v.GetI128()
		if !ok {
			return "", false
		}
		return int128ToString(value), true
	default:
		return "", false
	}
}

func scMapFields(v xdr.ScVal) map[string]xdr.ScVal {
	raw, ok := v.GetMap()
	if !ok || raw == nil {
		return nil
	}
	out := map[string]xdr.ScVal{}
	for _, entry := range *raw {
		name, ok := scSymbol(entry.Key)
		if !ok {
			continue
		}
		out[name] = entry.Val
	}
	return out
}

func scVariant(v xdr.ScVal) (string, []xdr.ScVal, bool) {
	vec, ok := v.GetVec()
	if !ok || vec == nil || len(*vec) == 0 {
		return "", nil, false
	}
	name, ok := scSymbol((*vec)[0])
	if !ok {
		return "", nil, false
	}
	return name, []xdr.ScVal((*vec)[1:]), true
}

func scVec(v xdr.ScVal) ([]xdr.ScVal, bool) {
	vec, ok := v.GetVec()
	if !ok || vec == nil {
		return nil, false
	}
	return []xdr.ScVal(*vec), true
}

func fieldAddress(fields map[string]xdr.ScVal, name string) (string, bool) {
	if fields == nil {
		return "", false
	}
	return scAddress(fields[name])
}

func fieldIntString(fields map[string]xdr.ScVal, name string) (string, bool) {
	if fields == nil {
		return "", false
	}
	return scIntString(fields[name])
}

func fieldInt32(fields map[string]xdr.ScVal, name string) (int32, bool) {
	if fields == nil {
		return 0, false
	}
	return scInt32(fields[name])
}
