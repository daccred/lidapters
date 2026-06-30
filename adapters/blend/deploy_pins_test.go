package blend

import "testing"

func TestDeployPinsParse(t *testing.T) {
	// init() panics if the embedded TOML fails to parse, so reaching here with a
	// populated table proves the canonical file parsed.
	if len(DeployPins()) == 0 {
		t.Fatal("deploy_pins.toml parsed to zero rows")
	}
}

func TestDeployPinTestnetResolves(t *testing.T) {
	const (
		pool   = "CANNO5MLDIANFGZBIFUAYLY2GZLMJOVQKIGBEGWYWQRCRR3QYKEKJAXU"
		oracle = "CCSXSQDUZLCRIGRWLCYK3F547PLBGSFTUVJRQCIXRO2ZDI3VIZKN3NH2"
	)
	for _, id := range []string{pool, oracle} {
		got, ok := DeployLedger("testnet", id)
		if !ok {
			t.Fatalf("testnet %s: expected curated pin, got ok=false", id)
		}
		if got != 3294000 {
			t.Fatalf("testnet %s: got deploy ledger %d, want 3294000", id, got)
		}
	}
}

func TestDeployPinMainnetUncurated(t *testing.T) {
	// YieldBloxV2 oracle row exists but has no deploy_ledger yet -> (0, false).
	got, ok := DeployLedger("public", "CD74A3C54EKUVEGUC6WNTUPOTHB624WFKXN3IYTFJGX3EHXDXHCYMXXR")
	if ok || got != 0 {
		t.Fatalf("uncurated mainnet row: got (%d, %v), want (0, false)", got, ok)
	}
}

func TestDeployPinUnknownContract(t *testing.T) {
	if got, ok := DeployLedger("testnet", "CNOTACONTRACT"); ok || got != 0 {
		t.Fatalf("unknown contract: got (%d, %v), want (0, false)", got, ok)
	}
}
