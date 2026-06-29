package blend

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
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

// TestDecodeState_RunTwiceByteIdentical is the D-03 determinism gate: the same
// (prior, changes, ledgerSeq) folded twice must serialize byte-identically. This
// catches map-iteration-order leaks and hidden accumulators that a stateless
// pure reducer (D-20) must not have.
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
// stable total-order key (the D-03 fix: the prior relay never sorted Deltas, so
// they leaked map order).
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

// TestDecodeState_EvictionTTLRestore is the D-09 decode-half gate: it drives the
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
