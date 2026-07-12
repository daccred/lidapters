package blend

// The permanent two-strategy parity gate. Every sequence here is folded
// through BOTH state-fold strategies — paranoid (the reference oracle) and
// incremental — and the serialized LedgerState plus the silver-debug delta
// stream must be byte-identical at EVERY ledger, not just at the end. Any
// change to either side of the seam (state.go's reference reducer or
// state_incremental.go's carried mirror) answers to these tests.
//
// The red-check tests prove the gate has teeth: a deliberately seeded
// divergence (a skipped sort, a dropped update) MUST be detected.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/daccred/lidapters/bindings"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// parityLedger is one ledger of fold input.
type parityLedger struct {
	seq     int64
	close   time.Time
	changes []bindings.ContractDataChange
}

// parityRun folds the same ledgers through a paranoid and an incremental
// adapter side by side, tracking each fold's own prior chain.
type parityRun struct {
	t           *testing.T
	paranoid    *Adapter
	incremental *Adapter
	pState      *bindings.LedgerState
	iState      *bindings.LedgerState
}

// newParityRun builds the two adapters. register applies identical
// registration (owned contracts, feeds, assets) to each; prior seeds both
// folds — supplied as a constructor so each adapter gets its own instance and
// the two folds share no memory.
func newParityRun(t *testing.T, register func(*Adapter), prior func() *bindings.LedgerState) *parityRun {
	t.Helper()
	run := &parityRun{t: t}
	for _, mode := range []StateMode{StateModeParanoid, StateModeIncremental} {
		adapter, err := New(Config{AllowUnknownV2: true, StateMode: mode})
		if err != nil {
			t.Fatalf("new %s adapter: %v", mode, err)
		}
		if register != nil {
			register(adapter)
		}
		if mode == StateModeParanoid {
			run.paranoid = adapter
		} else {
			run.incremental = adapter
		}
	}
	if prior != nil {
		run.pState = prior()
		run.iState = prior()
	}
	return run
}

func (r *parityRun) strategy() *incrementalStrategy {
	return r.incremental.state.(*incrementalStrategy)
}

// foldLedger advances both folds one ledger and reports whether the outputs
// (state JSON and delta stream) match, without failing the test — the
// red-check tests need the non-fatal form.
func (r *parityRun) foldLedger(ledger parityLedger) (equal bool, detail string) {
	r.t.Helper()
	pNext, pDeltas := r.paranoid.state.decodeState(r.pState, ledger.changes, ledger.seq, ledger.close)
	iNext, iDeltas := r.incremental.state.decodeState(r.iState, ledger.changes, ledger.seq, ledger.close)
	r.pState, r.iState = pNext, iNext

	pJSON := mustMarshalState(r.t, pNext)
	iJSON := mustMarshalState(r.t, iNext)
	if !bytes.Equal(pJSON, iJSON) {
		return false, "state mismatch:\nparanoid=" + string(pJSON) + "\nincremental=" + string(iJSON)
	}
	if !reflect.DeepEqual(pDeltas, iDeltas) {
		return false, "delta stream mismatch"
	}
	return true, ""
}

// fold asserts byte-parity at every ledger of the sequence.
func (r *parityRun) fold(ledgers ...parityLedger) {
	r.t.Helper()
	for _, ledger := range ledgers {
		if equal, detail := r.foldLedger(ledger); !equal {
			r.t.Fatalf("parity broken at ledger %d: %s", ledger.seq, detail)
		}
	}
}

func mustMarshalState(t *testing.T, state *bindings.LedgerState) []byte {
	t.Helper()
	b, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	return b
}

// syntheticParitySequence is a multi-ledger sequence that exercises every
// mutation class the incremental caches must track: user add / update /
// remove, a user pending before its pool exists, pool add / delete / re-add,
// reserve data updates, a reserve index remap (block invalidation), backstop
// pool + user balances and their deletes, emissions and a TTL-lapsed emission
// entry, ordering ties (one address in two pools, two addresses in one pool),
// and quiet carry-only ledgers.
func syntheticParitySequence(t *testing.T) []parityLedger {
	t.Helper()
	pool1 := validContractString(t, 1)
	pool2 := validContractString(t, 9)
	backstopID := validContractString(t, 4)
	assetA := contractAddressVal(t, 2)
	assetB := contractAddressVal(t, 10)
	user5 := accountAddressVal(t, 5)
	user6 := accountAddressVal(t, 6)

	positions := func(supply, collateral, liability int64) xdr.ScVal {
		fields := map[string]xdr.ScVal{
			"supply":      intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(supply)}),
			"collateral":  intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(collateral)}),
			"liabilities": intMapVal(t, map[uint32]xdr.ScVal{1: i128Val(liability)}),
		}
		return mapVal(t, fields)
	}
	resConfig := func(index uint32) xdr.ScVal {
		return mapVal(t, map[string]xdr.ScVal{
			"index":      u32Val(index),
			"decimals":   u32Val(7),
			"c_factor":   u32Val(8_000_000),
			"l_factor":   u32Val(9_000_000),
			"reactivity": u32Val(20_000),
			"enabled":    boolVal(true),
		})
	}
	resData := func(bSupply, dSupply int64) xdr.ScVal {
		return mapVal(t, map[string]xdr.ScVal{
			"d_rate":   i128Val(1_000_000),
			"b_rate":   i128Val(1_000_000),
			"b_supply": i128Val(bSupply),
			"d_supply": i128Val(dSupply),
		})
	}
	poolConfig := mapVal(t, map[string]xdr.ScVal{
		"oracle":     contractAddressVal(t, 3),
		"bstop_rate": u32Val(1_000_000),
		"status":     u32Val(1),
	})
	emisTTL := uint32(107)

	return []parityLedger{
		// Pool 1 bootstrap: config, backstop link, reserves, emissions.
		{seq: 100, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, symbolVal(t, "Config"), poolConfig),
			stateChange(t, pool1, symbolVal(t, "Backstop"), contractAddressVal(t, 4)),
			stateChange(t, pool1, symbolVal(t, "ResList"), vecVal(assetA, assetB)),
			stateChange(t, pool1, variantVal(t, "ResConfig", assetA), resConfig(1)),
			stateChange(t, pool1, variantVal(t, "ResData", assetA), resData(100, 20)),
			stateChange(t, pool1, variantVal(t, "EmisConfig", u32Val(3)), mapVal(t, map[string]xdr.ScVal{
				"eps":        u64Val(1_000_000),
				"expiration": u64Val(1_800_000_000),
			})),
			stateChange(t, pool1, variantVal(t, "EmisData", u32Val(3)), mapVal(t, map[string]xdr.ScVal{
				"index":     i128Val(0),
				"last_time": u64Val(1_700_000_000),
			})),
		}},
		// User add + backstop pool/user balances.
		{seq: 101, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "Positions", user5), positions(700, 300, 250)),
			stateChange(t, backstopID, variantVal(t, "PoolBalance", contractAddressVal(t, 1)), mapVal(t, map[string]xdr.ScVal{
				"shares": i128Val(2000),
				"tokens": i128Val(5000),
			})),
			stateChange(t, backstopID, variantVal(t, "UserBalance", mapVal(t, map[string]xdr.ScVal{
				"pool": contractAddressVal(t, 1),
				"user": user5,
			})), mapVal(t, map[string]xdr.ScVal{"shares": i128Val(400)})),
		}},
		// Quiet ledger: pure carry.
		{seq: 102},
		// Ordering ties: a second user in pool 1, and user 5 pending in pool 2
		// BEFORE pool 2 exists (raw blob carried, no typed positions yet).
		{seq: 103, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "Positions", user6), positions(50, 60, 0)),
			stateChange(t, pool2, variantVal(t, "Positions", user5), positions(900, 0, 10)),
		}},
		// Reserve data update (no index remap) + user update.
		{seq: 104, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "ResData", assetA), resData(140, 45)),
			stateChange(t, pool1, variantVal(t, "Positions", user5), positions(800, 350, 200)),
		}},
		// Pool 2 appears: user 5's pending blob must now resolve to positions.
		{seq: 105, changes: []bindings.ContractDataChange{
			stateChange(t, pool2, symbolVal(t, "Config"), poolConfig),
			stateChange(t, pool2, symbolVal(t, "ResList"), vecVal(assetB)),
			stateChange(t, pool2, variantVal(t, "ResConfig", assetB), resConfig(1)),
		}},
		// Reserve index remap on pool 1: every pool-1 user's positions re-resolve
		// (index 1 no longer maps to asset A).
		{seq: 106, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "ResConfig", assetA), resConfig(0)),
		}},
		// User remove + backstop user remove.
		{seq: 107, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "Positions", user6), positions(0, 0, 0), withLive(false)),
			stateChange(t, backstopID, variantVal(t, "UserBalance", mapVal(t, map[string]xdr.ScVal{
				"pool": contractAddressVal(t, 1),
				"user": user5,
			})), mapVal(t, map[string]xdr.ScVal{"shares": i128Val(0)}), withLive(false)),
		}},
		// TTL-lapsed emission entry (clears one side, keeps the reserve) +
		// backstop pool balance update.
		{seq: 108, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, variantVal(t, "EmisData", u32Val(3)), mapVal(t, map[string]xdr.ScVal{
				"index":     i128Val(5),
				"last_time": u64Val(1_700_000_500),
			}), withLiveUntil(&emisTTL)),
			stateChange(t, backstopID, variantVal(t, "PoolBalance", contractAddressVal(t, 1)), mapVal(t, map[string]xdr.ScVal{
				"shares": i128Val(2400),
				"tokens": i128Val(5600),
				"q4w":    i128Val(120),
			})),
		}},
		{seq: 109},
		// Pool 1 deleted (config eviction): its users' typed positions vanish,
		// their pending blobs survive.
		{seq: 110, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, symbolVal(t, "Config"), poolConfig, withLive(false)),
		}},
		{seq: 111},
		// Pool 1 re-added: carried pending blobs resolve again.
		{seq: 112, changes: []bindings.ContractDataChange{
			stateChange(t, pool1, symbolVal(t, "Config"), poolConfig),
			stateChange(t, pool1, symbolVal(t, "ResList"), vecVal(assetA)),
			stateChange(t, pool1, variantVal(t, "ResConfig", assetA), resConfig(1)),
		}},
	}
}

func TestStateModeSelection(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{})
	if err != nil {
		t.Fatalf("new default adapter: %v", err)
	}
	if _, ok := adapter.state.(*paranoidStrategy); !ok {
		t.Fatalf("default state mode is not paranoid: %T", adapter.state)
	}
	adapter, err = New(Config{StateMode: StateModeIncremental})
	if err != nil {
		t.Fatalf("new incremental adapter: %v", err)
	}
	if _, ok := adapter.state.(*incrementalStrategy); !ok {
		t.Fatalf("incremental state mode is not incremental: %T", adapter.state)
	}
	if _, err := New(Config{StateMode: "yolo"}); err == nil {
		t.Fatalf("unknown state mode must be rejected")
	}
}

func TestIncrementalParity_SyntheticSequence(t *testing.T) {
	t.Parallel()
	run := newParityRun(t, nil, nil)
	run.fold(syntheticParitySequence(t)...)
	if len(run.iState.PendingUserPositions) == 0 || len(run.iState.Users) == 0 {
		t.Fatalf("sequence ended trivially: pending=%d users=%d",
			len(run.iState.PendingUserPositions), len(run.iState.Users))
	}
}

// TestIncrementalParity_OracleCarry drives the frozen testnet oracle layout —
// deploy ledger, price-only ledgers, a price eviction — through both modes.
func TestIncrementalParity_OracleCarry(t *testing.T) {
	t.Parallel()
	layout := loadOracleLayout(t)
	run := newParityRun(t, func(a *Adapter) { a.RegisterContracts(layout.OracleContract) }, nil)

	priceEviction := oraclePriceOnlyChange(t, layout, "wETH", 0)
	priceEviction.Live = false
	run.fold(
		parityLedger{seq: layout.LedgerSeq, changes: oracleSceneChanges(t, layout)},
		parityLedger{seq: layout.LedgerSeq + 1, changes: []bindings.ContractDataChange{
			oraclePriceOnlyChange(t, layout, "XLM", 3_141_592),
		}},
		parityLedger{seq: layout.LedgerSeq + 2},
		parityLedger{seq: layout.LedgerSeq + 3, changes: []bindings.ContractDataChange{priceEviction}},
	)
	if len(run.iState.Oracles) == 0 {
		t.Fatalf("oracle carry ended without a carried oracle")
	}
}

// TestIncrementalParity_ReflectorGoldenFixture folds the REAL mainnet
// Reflector fixture (feeds, both storage protocols, aggregator config
// assembly, close-time staleness) through both modes at real close times.
func TestIncrementalParity_ReflectorGoldenFixture(t *testing.T) {
	t.Parallel()
	ledgers := loadReflectorFixture(t)
	run := newParityRun(t, func(a *Adapter) {
		a.RegisterContracts(fixedPoolID, fixedAggregatorID, yieldBloxPoolID)
		a.RegisterPriceFeeds(cexFeedID, dexFeedID)
	}, fixedPoolPrior)

	for _, ledger := range ledgers {
		run.fold(parityLedger{
			seq:     ledger.LedgerSeq,
			close:   time.Unix(ledger.CloseTimeUnix, 0).UTC(),
			changes: ledger.Changes,
		})
	}
	if len(run.iState.PriceFeeds) == 0 || len(run.iState.OracleAggregators) == 0 {
		t.Fatalf("reflector parity ended without carried feed/aggregator state")
	}
}

// TestIncrementalParity_EvictionTTLFixtures drives the golden liveness
// fixtures (evict / TTL-expiry / restore) through both modes.
func TestIncrementalParity_EvictionTTLFixtures(t *testing.T) {
	t.Parallel()
	scenarios := loadEvictionScenarios(t, "testdata/eviction_ttl_restore_fixtures.json")
	poolID := validContractString(t, 7)
	configKey := symbolVal(t, "Config")
	configValue := mapVal(t, map[string]xdr.ScVal{
		"oracle":     contractAddressVal(t, 8),
		"bstop_rate": u32Val(1_000_000),
		"status":     u32Val(1),
	})

	for _, sc := range scenarios {
		run := newParityRun(t, nil, nil)
		ledgers := make([]parityLedger, 0, 2)
		if sc.Prior {
			ledgers = append(ledgers, parityLedger{seq: 100, changes: []bindings.ContractDataChange{
				stateChange(t, poolID, configKey, configValue),
			}})
		}
		opts := []changeOpt{withLive(sc.Live), withLiveUntil(sc.LiveUntilLedgerSeq)}
		if sc.ChangeType != "" {
			opts = append(opts, withChangeType(sc.ChangeType))
		}
		ledgers = append(ledgers, parityLedger{seq: sc.LedgerSeq, changes: []bindings.ContractDataChange{
			stateChange(t, poolID, configKey, configValue, opts...),
		}})
		run.fold(ledgers...)
		if got := hasPool(run.iState, poolID); got != sc.ExpectPresent {
			t.Fatalf("%s: incremental pool present = %v, want %v", sc.Scenario, got, sc.ExpectPresent)
		}
	}
}

// TestIncrementalParity_CheckpointReseed is the checkpoint-restore path: the
// state is serialized and re-loaded mid-sequence (so the incremental strategy
// sees a prior that is NOT its own last output and must reseed), then the fold
// continues through both modes.
func TestIncrementalParity_CheckpointReseed(t *testing.T) {
	t.Parallel()
	sequence := syntheticParitySequence(t)
	run := newParityRun(t, nil, nil)
	run.fold(sequence[:6]...)

	// Serialize + reload both sides — same JSON round-trip the relay's fold
	// checkpoints go through.
	var restored bindings.LedgerState
	if err := json.Unmarshal(mustMarshalState(t, run.iState), &restored); err != nil {
		t.Fatalf("reload checkpoint: %v", err)
	}
	var restoredParanoid bindings.LedgerState
	if err := json.Unmarshal(mustMarshalState(t, run.pState), &restoredParanoid); err != nil {
		t.Fatalf("reload paranoid checkpoint: %v", err)
	}
	run.pState, run.iState = &restoredParanoid, &restored

	run.fold(sequence[6:]...)
}

// TestIncrementalParity_RunTwiceByteIdentical is the determinism gate for the
// incremental mode: two fresh incremental folds of the same sequence must
// serialize byte-identically (the incremental analog of
// TestDecodeState_RunTwiceByteIdentical).
func TestIncrementalParity_RunTwiceByteIdentical(t *testing.T) {
	t.Parallel()
	sequence := syntheticParitySequence(t)
	finals := make([][]byte, 0, 2)
	for range 2 {
		adapter, err := New(Config{StateMode: StateModeIncremental})
		if err != nil {
			t.Fatalf("new adapter: %v", err)
		}
		var state *bindings.LedgerState
		for _, ledger := range sequence {
			next, decodeErr := adapter.DecodeState(state, ledger.changes, ledger.seq)
			if decodeErr != nil {
				t.Fatalf("decode ledger %d: %v", ledger.seq, decodeErr)
			}
			state = next
		}
		finals = append(finals, mustMarshalState(t, state))
	}
	if !bytes.Equal(finals[0], finals[1]) {
		t.Fatalf("incremental run-twice output not byte-identical:\nfirst=%s\nsecond=%s", finals[0], finals[1])
	}
}

// TestIncrementalBisect_PinnedPlayersHoldParity runs the hybrid matrix: each
// bisect knob pins one incremental player back to its paranoid equivalent, and
// every combination must still hold byte-parity. This is the diagnostic
// affordance for divergence hunts — pin one player, rerun parity, repeat until
// the offender is isolated. Test-internal only, never public API.
func TestIncrementalBisect_PinnedPlayersHoldParity(t *testing.T) {
	t.Parallel()
	combos := []struct {
		name             string
		reloadEachLedger bool
		rebuildSnapshot  bool
	}{
		{name: "reload_pinned", reloadEachLedger: true},
		{name: "snapshot_pinned", rebuildSnapshot: true},
		{name: "both_pinned", reloadEachLedger: true, rebuildSnapshot: true},
	}
	for _, combo := range combos {
		t.Run(combo.name, func(t *testing.T) {
			t.Parallel()
			run := newParityRun(t, nil, nil)
			run.strategy().bisect.reloadEachLedger = combo.reloadEachLedger
			run.strategy().bisect.rebuildSnapshot = combo.rebuildSnapshot
			run.fold(syntheticParitySequence(t)...)
		})
	}
}

// TestIncrementalParity_RedCheck seeds deliberate divergences into the
// incremental strategy's caches mid-sequence and asserts the parity check
// CATCHES them. If this test ever passes divergence through silently, the
// parity gate has lost its teeth — that is the failure it exists to make loud.
func TestIncrementalParity_RedCheck(t *testing.T) {
	t.Parallel()

	seed := func(t *testing.T) (*parityRun, []parityLedger) {
		t.Helper()
		sequence := syntheticParitySequence(t)
		run := newParityRun(t, nil, nil)
		run.fold(sequence[:6]...)
		if len(run.strategy().keys) < 2 {
			t.Fatalf("red-check needs at least two cached user entries, got %d", len(run.strategy().keys))
		}
		return run, sequence[6:]
	}
	foldRemaining := func(run *parityRun, rest []parityLedger) bool {
		diverged := false
		for _, ledger := range rest {
			if equal, _ := run.foldLedger(ledger); !equal {
				diverged = true
			}
		}
		return diverged
	}

	t.Run("skipped_sort", func(t *testing.T) {
		t.Parallel()
		run, rest := seed(t)
		// Break the maintained global order — the "someone forgot to sort"
		// divergence.
		keys := run.strategy().keys
		keys[0], keys[1] = keys[1], keys[0]
		if !foldRemaining(run, rest) {
			t.Fatalf("red-check failed: a broken sort order sailed through the parity gate undetected")
		}
	})

	t.Run("dropped_update", func(t *testing.T) {
		t.Parallel()
		run, rest := seed(t)
		// Drop one typed position from a cached block — the "one update never
		// applied" divergence. The victim must be a pool-2 user: pool 1 is
		// legitimately remapped later in the sequence, which would recompute the
		// tampered block and repair the seeded divergence before it is observed.
		pool2 := validContractString(t, 9)
		var victim *userEntry
		for _, entry := range run.strategy().keys {
			if entry.pool == pool2 && len(entry.block) > 0 {
				victim = entry
				break
			}
		}
		if victim == nil {
			t.Fatalf("red-check needs a cached pool-2 entry with a non-empty block")
		}
		victim.block = victim.block[:len(victim.block)-1]
		if !foldRemaining(run, rest) {
			t.Fatalf("red-check failed: a dropped update sailed through the parity gate undetected")
		}
	})
}
