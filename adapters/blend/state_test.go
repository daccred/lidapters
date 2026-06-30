package blend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
	"github.com/shopspring/decimal"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// representativeChanges builds a contract_data change set covering pools,
// reserves, and user positions plus backstop pool/user balances — the shape the
// run-twice and deltas-sorted determinism gates need to exercise.
func representativeChanges(t *testing.T) []contractsv1.ContractDataChange {
	t.Helper()
	poolID := validContractString(t, 1)
	backstopID := validContractString(t, 4)

	return []contractsv1.ContractDataChange{
		stateChange(t, poolID, symbolVal(t, "Config"), mapVal(t, map[string]xdr.ScVal{
			"oracle":     contractAddressVal(t, 3),
			"bstop_rate": u32Val(1_000_000),
			"status":     u32Val(1),
		})),
		stateChange(t, poolID, symbolVal(t, "Backstop"), contractAddressVal(t, 4)),
		stateChange(t, poolID, symbolVal(t, "ResList"), vecVal(contractAddressVal(t, 2))),
		stateChange(t, poolID, variantVal(t, "ResConfig", contractAddressVal(t, 2)), mapVal(t, map[string]xdr.ScVal{
			"index":    u32Val(1),
			"decimals": u32Val(7),
			"c_factor": u32Val(8_000_000),
			"l_factor": u32Val(9_000_000),
		})),
		stateChange(t, poolID, variantVal(t, "ResData", contractAddressVal(t, 2)), mapVal(t, map[string]xdr.ScVal{
			"d_rate":   i128Val(1_000_000),
			"b_rate":   i128Val(1_000_000),
			"b_supply": i128Val(100),
			"d_supply": i128Val(20),
		})),
		stateChange(t, poolID, variantVal(t, "Positions", accountAddressVal(t, 5)), mapVal(t, map[string]xdr.ScVal{
			"supply":      intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(700)}),
			"collateral":  intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(300)}),
			"liabilities": intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(250)}),
		})),
		stateChange(t, backstopID, variantVal(t, "PoolBalance", contractAddressVal(t, 1)), mapVal(t, map[string]xdr.ScVal{
			"shares": i128Val(2000),
			"tokens": i128Val(5000),
		})),
		stateChange(t, backstopID, variantVal(t, "UserBalance", mapVal(t, map[string]xdr.ScVal{
			"pool": contractAddressVal(t, 1),
			"user": accountAddressVal(t, 5),
		})), mapVal(t, map[string]xdr.ScVal{
			"shares": i128Val(400),
		})),
	}
}

// TestDecodeState_RunTwiceByteIdentical is the determinism gate: the same
// (prior, changes, ledgerSeq) folded twice must serialize byte-identically. This
// catches map-iteration-order leaks and hidden accumulators that a stateless
// pure reducer must not have.
func TestDecodeState_RunTwiceByteIdentical(t *testing.T) {
	t.Parallel()

	adapter := newTestAdapter(t)
	changes := representativeChanges(t)

	first, err := adapter.DecodeState(nil, changes, 123)
	if err != nil {
		t.Fatalf("decode first: %v", err)
	}
	second, err := adapter.DecodeState(nil, changes, 123)
	if err != nil {
		t.Fatalf("decode second: %v", err)
	}

	if len(first.Pools) == 0 || len(first.Users) == 0 || len(first.Backstops) == 0 {
		t.Fatalf("expected a non-trivial state: pools=%d users=%d backstops=%d",
			len(first.Pools), len(first.Users), len(first.Backstops))
	}

	b1, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first: %v", err)
	}
	b2, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second: %v", err)
	}
	if !bytes.Equal(b1, b2) {
		t.Fatalf("run-twice output not byte-identical:\nfirst=%s\nsecond=%s", b1, b2)
	}
}

// TestDecodeState_DeltasSorted asserts the silver-debug deltas are emitted in a
// stable total-order key, so they do not leak Go's randomized map-iteration
// order from one run to the next.
func TestDecodeState_DeltasSorted(t *testing.T) {
	t.Parallel()

	adapter := newTestAdapter(t)
	changes := representativeChanges(t)

	_, deltas := adapter.decodeBlendState(nil, changes, 123)
	if len(deltas) == 0 {
		t.Fatal("expected deltas to be emitted")
	}
	for i := 1; i < len(deltas); i++ {
		if deltaLess(deltas[i], deltas[i-1]) {
			t.Fatalf("deltas not sorted at index %d: %+v before %+v", i, deltas[i-1], deltas[i])
		}
	}
}

// deltaLess mirrors the total order DecodeState sorts deltas by.
func deltaLess(a, b typedStateDelta) bool {
	if a.EntityType != b.EntityType {
		return a.EntityType < b.EntityType
	}
	if a.EntityKey != b.EntityKey {
		return a.EntityKey < b.EntityKey
	}
	if a.LedgerSeq != b.LedgerSeq {
		return a.LedgerSeq < b.LedgerSeq
	}
	if a.Live != b.Live {
		return !a.Live && b.Live
	}
	return string(a.StateJSON) < string(b.StateJSON)
}

type evictionScenario struct {
	Scenario           string  `json:"scenario"`
	Description        string  `json:"description"`
	Prior              bool    `json:"prior"`
	ChangeType         string  `json:"change_type"`
	Live               bool    `json:"live"`
	LiveUntilLedgerSeq *uint32 `json:"live_until_ledger_seq"`
	LedgerSeq          int64   `json:"ledger_seq"`
	ExpectPresent      bool    `json:"expect_present"`
}

// TestDecodeState_EvictionTTLRestore is the eviction/TTL gate: it drives the
// three liveness fixtures (evict / TTL-expiry / restore) through DecodeState and
// asserts the pool's presence in the resulting LedgerState. Zero DB/network.
func TestDecodeState_EvictionTTLRestore(t *testing.T) {
	t.Parallel()

	scenarios := loadEvictionScenarios(t, "testdata/eviction_ttl_restore_fixtures.json")
	if len(scenarios) != 3 {
		t.Fatalf("expected 3 fixtures, got %d", len(scenarios))
	}

	adapter := newTestAdapter(t)
	poolID := validContractString(t, 7)
	configKey := symbolVal(t, "Config")
	configValue := mapVal(t, map[string]xdr.ScVal{
		"oracle":     contractAddressVal(t, 8),
		"bstop_rate": u32Val(1_000_000),
		"status":     u32Val(1),
	})

	for _, sc := range scenarios {
		t.Run(sc.Scenario, func(t *testing.T) {
			var prior *contractsv1.LedgerState
			if sc.Prior {
				baseline := []contractsv1.ContractDataChange{stateChange(t, poolID, configKey, configValue)}
				built, err := adapter.DecodeState(nil, baseline, 100)
				if err != nil {
					t.Fatalf("build prior: %v", err)
				}
				if !hasPool(built, poolID) {
					t.Fatalf("prior baseline should contain pool %s", poolID)
				}
				prior = built
			}

			opts := []changeOpt{withLive(sc.Live), withLiveUntil(sc.LiveUntilLedgerSeq)}
			if sc.ChangeType != "" {
				opts = append(opts, withChangeType(sc.ChangeType))
			}
			change := stateChange(t, poolID, configKey, configValue, opts...)

			next, err := adapter.DecodeState(prior, []contractsv1.ContractDataChange{change}, sc.LedgerSeq)
			if err != nil {
				t.Fatalf("decode %s: %v", sc.Scenario, err)
			}
			if got := hasPool(next, poolID); got != sc.ExpectPresent {
				t.Fatalf("%s: pool present = %v, want %v (%s)", sc.Scenario, got, sc.ExpectPresent, sc.Description)
			}
		})
	}
}

func hasPool(state *contractsv1.LedgerState, contractID string) bool {
	for _, pool := range state.Pools {
		if pool.ContractID == contractID {
			return true
		}
	}
	return false
}

func loadEvictionScenarios(t *testing.T, relPath string) []evictionScenario {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", relPath))
	if err != nil {
		t.Fatalf("read fixtures %s: %v", relPath, err)
	}
	var payload struct {
		Scenarios []evictionScenario `json:"scenarios"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatalf("decode fixtures %s: %v", relPath, err)
	}
	return payload.Scenarios
}

func newTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	adapter, err := New(Config{})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	return adapter
}

// --- contract_data change builders (state-decode test flavor) --------------

type changeOpt func(*contractsv1.ContractDataChange)

func withLive(live bool) changeOpt {
	return func(c *contractsv1.ContractDataChange) { c.Live = live }
}

func withLiveUntil(seq *uint32) changeOpt {
	return func(c *contractsv1.ContractDataChange) { c.LiveUntilLedgerSeq = seq }
}

func withChangeType(t string) changeOpt {
	return func(c *contractsv1.ContractDataChange) { c.ChangeType = t }
}

func stateChange(t *testing.T, contractID string, key, value xdr.ScVal, opts ...changeOpt) contractsv1.ContractDataChange {
	t.Helper()
	keyXDR, err := xdr.MarshalBase64(key)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	valueXDR, err := xdr.MarshalBase64(value)
	if err != nil {
		t.Fatalf("marshal value: %v", err)
	}
	change := contractsv1.ContractDataChange{
		ContractID: contractID,
		KeyXDR:     keyXDR,
		Durability: "persistent",
		ValueXDR:   &valueXDR,
		Live:       true,
	}
	for _, opt := range opts {
		opt(&change)
	}
	return change
}

func u32Val(value uint32) xdr.ScVal {
	raw := xdr.Uint32(value)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &raw}
}

func vecVal(items ...xdr.ScVal) xdr.ScVal {
	vec := xdr.ScVec(items)
	ptr := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ptr}
}

func variantVal(t *testing.T, name string, args ...xdr.ScVal) xdr.ScVal {
	t.Helper()
	return vecVal(append([]xdr.ScVal{symbolVal(t, name)}, args...)...)
}

func intMapVal(t *testing.T, fields map[uint32]xdr.ScVal) xdr.ScVal {
	t.Helper()
	entries := make(xdr.ScMap, 0, len(fields))
	for key, value := range fields {
		entries = append(entries, xdr.ScMapEntry{Key: u32Val(key), Val: value})
	}
	ptr := &entries
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &ptr}
}

// --- oracle price decode test ----------------------------------------------

type oracleLayout struct {
	OracleContract string              `json:"oracle_contract"`
	PoolContract   string              `json:"pool_contract"`
	LedgerSeq      int64               `json:"ledger_seq"`
	OracleDecimals int32               `json:"oracle_decimals"`
	InstanceValue  string              `json:"instance_value_xdr_base64"`
	Assets         []oracleLayoutAsset `json:"assets"`
}

type oracleLayoutAsset struct {
	Code              string      `json:"asset_code"`
	AssetID           string      `json:"asset_id"`
	Index             int         `json:"asset_index"`
	OraclePriceRaw    string      `json:"oracle_price_raw"`
	OracleDecimals    int32       `json:"oracle_decimals"`
	LastPriceExpected json.Number `json:"lastprice_expected"`
	KeyXDR            string      `json:"stored_key_xdr_base64"`
	ValueXDR          string      `json:"stored_value_xdr_base64"`
}

// TestOraclePriceDecode drives the frozen testnet oracle layout through
// DecodeState (zero DB / network) and proves the missing valuation input is now
// live: each stored price decodes onto its reserve and reproduces the oracle's
// lastprice; a non-positive or no-longer-live price is rejected; and a decoded
// price lights up the health factor and USD value the gold math already builds.
func TestOraclePriceDecode(t *testing.T) {
	t.Parallel()

	layout := loadOracleLayout(t)

	newAdapter := func() *Adapter {
		t.Helper()
		adapter, err := New(Config{AllowUnknownV2: true})
		if err != nil {
			t.Fatalf("new adapter: %v", err)
		}
		adapter.RegisterContracts(layout.OracleContract)
		return adapter
	}

	t.Run("decodes lastprice within tolerance", func(t *testing.T) {
		t.Parallel()
		adapter := newAdapter()
		changes := oracleSceneChanges(t, layout)

		state, err := adapter.DecodeState(nil, changes, layout.LedgerSeq)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		// Folding the same input twice must serialize byte-identically, so the
		// oracle-derived price does not leak map-iteration order.
		again, err := adapter.DecodeState(nil, changes, layout.LedgerSeq)
		if err != nil {
			t.Fatalf("decode again: %v", err)
		}
		b1, _ := json.Marshal(state)
		b2, _ := json.Marshal(again)
		if !bytes.Equal(b1, b2) {
			t.Fatalf("run-twice oracle decode not byte-identical")
		}

		for _, asset := range layout.Assets {
			reserve := findReserve(t, state, layout.PoolContract, asset.AssetID)
			if reserve.OraclePriceRaw != asset.OraclePriceRaw {
				t.Fatalf("%s raw price: got %q want %q", asset.Code, reserve.OraclePriceRaw, asset.OraclePriceRaw)
			}
			if reserve.OracleDecimals != asset.OracleDecimals {
				t.Fatalf("%s decimals: got %d want %d", asset.Code, reserve.OracleDecimals, asset.OracleDecimals)
			}
			decoded := decimal.RequireFromString(reserve.OraclePriceRaw).Div(decimal.New(1, reserve.OracleDecimals))
			assertWithinTolerance(t, asset.LastPriceExpected.String(), decoded.String(), "0.001", asset.Code+" lastprice")
		}
	})

	t.Run("rejects non-positive price", func(t *testing.T) {
		t.Parallel()
		adapter := newAdapter()
		changes := oracleSceneChanges(t, layout)
		setPriceValue(t, changes, layout, "wBTC", i128Val(0))

		state, err := adapter.DecodeState(nil, changes, layout.LedgerSeq)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		rejected := findReserve(t, state, layout.PoolContract, assetIDByCode(layout, "wBTC"))
		if rejected.OraclePriceRaw != "" {
			t.Fatalf("expected wBTC price rejected, got %q", rejected.OraclePriceRaw)
		}
		// A valid sibling price is unaffected.
		usdc := findReserve(t, state, layout.PoolContract, assetIDByCode(layout, "USDC"))
		if usdc.OraclePriceRaw == "" {
			t.Fatalf("expected USDC price to remain decoded")
		}
	})

	t.Run("rejects stale price", func(t *testing.T) {
		t.Parallel()
		adapter := newAdapter()
		changes := oracleSceneChanges(t, layout)
		// A stable price lives in a temporary entry; lapsing its TTL before this
		// ledger is the storage-level form of the contract's reject-stale rule.
		setPriceLiveUntil(t, changes, layout, "wBTC", uint32(layout.LedgerSeq-1))

		state, err := adapter.DecodeState(nil, changes, layout.LedgerSeq)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		stale := findReserve(t, state, layout.PoolContract, assetIDByCode(layout, "wBTC"))
		if stale.OraclePriceRaw != "" {
			t.Fatalf("expected stale wBTC price rejected, got %q", stale.OraclePriceRaw)
		}
	})

	t.Run("feeds health factor and usd value", func(t *testing.T) {
		t.Parallel()
		adapter := newAdapter()
		changes := oracleSceneChanges(t, layout)

		state, err := adapter.DecodeState(nil, changes, layout.LedgerSeq)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}

		out, err := adapter.Transform(contractsv1.TransformInput{
			LedgerSeq: layout.LedgerSeq,
			CloseTime: time.Unix(1, 0).UTC(),
			State:     state,
		})
		if err != nil {
			t.Fatalf("transform: %v", err)
		}

		if len(out.Summaries) != 1 {
			t.Fatalf("expected one summary, got %d", len(out.Summaries))
		}
		if out.Summaries[0].HealthFactor == "" {
			t.Fatalf("expected non-empty health factor once the price is decoded")
		}
		if !hasPricedPosition(out) {
			t.Fatalf("expected a position carrying a non-empty usd value")
		}
	})
}

func loadOracleLayout(t *testing.T) oracleLayout {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(".", "testdata", "oracle_stored_layout.json"))
	if err != nil {
		t.Fatalf("read oracle layout: %v", err)
	}
	var l oracleLayout
	if err := json.Unmarshal(b, &l); err != nil {
		t.Fatalf("decode oracle layout: %v", err)
	}
	if len(l.Assets) != 4 {
		t.Fatalf("expected 4 oracle assets, got %d", len(l.Assets))
	}
	return l
}

// oracleSceneChanges builds a self-contained change set: a pool wired to the
// oracle, one reserve per oracle asset (distinct indices so user positions
// resolve), a cross-asset user (wBTC collateral / USDC borrow, so health
// depends on the wBTC price), the oracle's frozen instance entry, and the four
// frozen per-asset price entries.
func oracleSceneChanges(t *testing.T, l oracleLayout) []contractsv1.ContractDataChange {
	t.Helper()
	poolID := l.PoolContract
	oracleID := l.OracleContract

	reserveConfig := func(code string, index uint32) contractsv1.ContractDataChange {
		return stateChange(t, poolID, variantVal(t, "ResConfig", addressVal(t, assetIDByCode(l, code))), mapVal(t, map[string]xdr.ScVal{
			"index":    u32Val(index),
			"decimals": u32Val(7),
			"c_factor": u32Val(9_000_000),
			"l_factor": u32Val(10_000_000),
		}))
	}

	instanceKey := xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance}

	changes := []contractsv1.ContractDataChange{
		stateChange(t, poolID, symbolVal(t, "Config"), mapVal(t, map[string]xdr.ScVal{
			"oracle":     addressVal(t, oracleID),
			"bstop_rate": u32Val(1_000_000),
			"status":     u32Val(1),
		})),
		reserveConfig("USDC", 0),
		reserveConfig("XLM", 1),
		reserveConfig("wETH", 2),
		reserveConfig("wBTC", 3),
		stateChange(t, poolID, variantVal(t, "ResData", addressVal(t, assetIDByCode(l, "wBTC"))), mapVal(t, map[string]xdr.ScVal{
			"b_rate":   i128Val(1_000_000_000_000),
			"d_rate":   i128Val(1_000_000_000_000),
			"b_supply": i128Val(10_000_000),
			"d_supply": i128Val(0),
		})),
		stateChange(t, poolID, variantVal(t, "ResData", addressVal(t, assetIDByCode(l, "USDC"))), mapVal(t, map[string]xdr.ScVal{
			"b_rate":   i128Val(1_000_000_000_000),
			"d_rate":   i128Val(1_000_000_000_000),
			"b_supply": i128Val(0),
			"d_supply": i128Val(500_000_000_000),
		})),
		stateChange(t, poolID, variantVal(t, "Positions", accountAddressVal(t, 9)), mapVal(t, map[string]xdr.ScVal{
			"supply":      intMapVal(t, map[uint32]xdr.ScVal{}),
			"collateral":  intMapVal(t, map[uint32]xdr.ScVal{3: i128Val(10_000_000)}),
			"liabilities": intMapVal(t, map[uint32]xdr.ScVal{0: i128Val(500_000_000_000)}),
		})),
		stateChange(t, oracleID, instanceKey, mustDecodeScVal(t, l.InstanceValue)),
	}

	for _, asset := range l.Assets {
		changes = append(changes, stateChange(t, oracleID,
			mustDecodeScVal(t, asset.KeyXDR),
			mustDecodeScVal(t, asset.ValueXDR)))
	}
	return changes
}

func setPriceValue(t *testing.T, changes []contractsv1.ContractDataChange, l oracleLayout, code string, value xdr.ScVal) {
	t.Helper()
	encoded, err := xdr.MarshalBase64(value)
	if err != nil {
		t.Fatalf("marshal price value: %v", err)
	}
	changes[priceChangeIndex(t, changes, l, code)].ValueXDR = &encoded
}

func setPriceLiveUntil(t *testing.T, changes []contractsv1.ContractDataChange, l oracleLayout, code string, liveUntil uint32) {
	t.Helper()
	lu := liveUntil
	changes[priceChangeIndex(t, changes, l, code)].LiveUntilLedgerSeq = &lu
}

func priceChangeIndex(t *testing.T, changes []contractsv1.ContractDataChange, l oracleLayout, code string) int {
	t.Helper()
	key := keyXDRByCode(t, l, code)
	for i := range changes {
		if changes[i].ContractID == l.OracleContract && changes[i].KeyXDR == key {
			return i
		}
	}
	t.Fatalf("price change for %s not found", code)
	return -1
}

func keyXDRByCode(t *testing.T, l oracleLayout, code string) string {
	t.Helper()
	encoded, err := xdr.MarshalBase64(mustDecodeScVal(t, codeKeyXDR(t, l, code)))
	if err != nil {
		t.Fatalf("marshal price key: %v", err)
	}
	return encoded
}

func codeKeyXDR(t *testing.T, l oracleLayout, code string) string {
	t.Helper()
	for _, a := range l.Assets {
		if a.Code == code {
			return a.KeyXDR
		}
	}
	t.Fatalf("asset %s not in layout", code)
	return ""
}

func assetIDByCode(l oracleLayout, code string) string {
	for _, a := range l.Assets {
		if a.Code == code {
			return a.AssetID
		}
	}
	return ""
}

func findReserve(t *testing.T, state *contractsv1.LedgerState, poolID, assetID string) contractsv1.ReserveState {
	t.Helper()
	for _, pool := range state.Pools {
		if pool.ContractID != poolID {
			continue
		}
		for _, reserve := range pool.Reserves {
			if reserve.AssetID == assetID {
				return reserve
			}
		}
	}
	t.Fatalf("reserve %s not found in pool %s", assetID, poolID)
	return contractsv1.ReserveState{}
}

func hasPricedPosition(out *contractsv1.TransformOutput) bool {
	for _, pos := range out.Positions {
		if pos.USDValue != "" {
			return true
		}
	}
	return false
}

func mustDecodeScVal(t *testing.T, b64 string) xdr.ScVal {
	t.Helper()
	v, ok := decodeScValBase64(b64)
	if !ok {
		t.Fatalf("decode scval %q", b64)
	}
	return v
}

func addressVal(t *testing.T, contractID string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		t.Fatalf("decode contract id %s: %v", contractID, err)
	}
	var hash xdr.Hash
	copy(hash[:], raw)
	address, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, xdr.ContractId(hash))
	if err != nil {
		t.Fatalf("contract address %s: %v", contractID, err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}
