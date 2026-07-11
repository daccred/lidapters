package aquarius

import (
	"testing"
	"time"

	"github.com/daccred/lidapters/bindings"
)

func TestTransformClassicComponentsDeterministic(t *testing.T) {
	a, err := NewWithConfig(Config{})
	if err != nil {
		t.Fatal(err)
	}
	s := &bindings.LedgerState{AMMPools: []bindings.AMMPoolState{{Protocol: "aquarius", ContractID: "pool", PoolType: "constant_product", TotalSharesRaw: "3", Tokens: []bindings.AMMTokenReserve{{AssetID: "a", ReserveRaw: "10"}, {AssetID: "b", ReserveRaw: "20"}}}}, AMMPositions: []bindings.AMMPositionState{{Address: "user", PoolContractID: "pool", SharesRaw: "2"}}}
	in := bindings.TransformInput{LedgerSeq: 7, CloseTime: time.Unix(7, 0).UTC(), State: s}
	one, err := a.Transform(in)
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.Transform(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(one.AMMComponents) != 2 || len(two.AMMComponents) != 2 {
		t.Fatalf("component counts %d %d", len(one.AMMComponents), len(two.AMMComponents))
	}
	if one.AMMComponents[0].ID != two.AMMComponents[0].ID {
		t.Fatal("non-deterministic id")
	}
	amounts := map[string]string{}
	for _, c := range one.AMMComponents {
		amounts[c.AssetID] = c.AmountRaw
		if c.USDValue != "" || c.Metadata["price_unavailable"] != "true" {
			t.Fatal("unpriced component not explicit")
		}
	}
	if amounts["a"] != "6" || amounts["b"] != "13" {
		t.Fatalf("amounts %#v", amounts)
	}
}

func TestUnknownWasmFailsClosed(t *testing.T) {
	a, err := NewWithConfig(Config{PoolWasmHashes: map[string]string{"known": "stable"}})
	if err != nil {
		t.Fatal(err)
	}
	a.RegisterContracts("pool")
	s, err := a.DecodeState(nil, nil, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.AMMPools) != 0 {
		t.Fatal("unexpected pool")
	}
}
