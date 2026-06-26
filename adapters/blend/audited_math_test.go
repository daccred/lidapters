package blend

import (
	"encoding/json"
	"testing"
	"time"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
)

func TestV2RateModifierScaleAndUtilizationClamp(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 1,
		CloseTime: time.Unix(0, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				WasmHash:         "wasm-v2",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         "CASSET",
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "1000000000",
					DSupplyRaw:      "2000000000",
					CFactorRaw:      "8000000",
					LFactorRaw:      "9000000",
					UtilTargetRaw:   "5000000",
					MaxUtilRaw:      "9500000",
					RBaseRaw:        "0",
					ROneRaw:         "1000000",
					RTwoRaw:         "0",
					RThreeRaw:       "0",
					RateModifierRaw: "10000000",
					SupplyCapRaw:    "100000000000",
					OraclePriceRaw:  "100000000",
					OracleDecimals:  8,
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Reserves) != 1 {
		t.Fatalf("expected 1 reserve, got %d", len(out.Reserves))
	}
	reserve := out.Reserves[0]
	if reserve.Utilization != "1" {
		t.Fatalf("expected utilization clamp at 1, got %s", reserve.Utilization)
	}
	if reserve.BorrowAPY != "0.1" {
		t.Fatalf("expected borrow APY 0.1 with v2 rate modifier scalar, got %s", reserve.BorrowAPY)
	}
}

func TestPoolIsolatedWorstSummarySemantics(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	address := "GUSER"
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 2,
		CloseTime: time.Unix(10, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{
				{
					ContractID:       "CPOOLA",
					WasmHash:         "wasm-v2",
					BackstopTakeRate: "0",
					Reserves: []contractsv1.ReserveState{{
						AssetID:         "CASSETA",
						AssetDecimals:   7,
						BRateRaw:        "1000000000000",
						DRateRaw:        "1000000000000",
						BSupplyRaw:      "10000000000",
						DSupplyRaw:      "0",
						CFactorRaw:      "8000000",
						LFactorRaw:      "9000000",
						UtilTargetRaw:   "8000000",
						MaxUtilRaw:      "9500000",
						RBaseRaw:        "0",
						ROneRaw:         "0",
						RTwoRaw:         "0",
						RThreeRaw:       "0",
						RateModifierRaw: "10000000",
						SupplyCapRaw:    "100000000000",
						OraclePriceRaw:  "100000000",
						OracleDecimals:  8,
					}},
				},
				{
					ContractID:       "CPOOLB",
					WasmHash:         "wasm-v2",
					BackstopTakeRate: "0",
					Reserves: []contractsv1.ReserveState{{
						AssetID:         "CASSETB",
						AssetDecimals:   7,
						BRateRaw:        "1000000000000",
						DRateRaw:        "1000000000000",
						BSupplyRaw:      "10000000000",
						DSupplyRaw:      "1000000000",
						CFactorRaw:      "8000000",
						LFactorRaw:      "9000000",
						UtilTargetRaw:   "8000000",
						MaxUtilRaw:      "9500000",
						RBaseRaw:        "0",
						ROneRaw:         "0",
						RTwoRaw:         "0",
						RThreeRaw:       "0",
						RateModifierRaw: "10000000",
						SupplyCapRaw:    "100000000000",
						OraclePriceRaw:  "100000000",
						OracleDecimals:  8,
					}},
				},
			},
			Users: []contractsv1.UserReservePosition{
				{Address: address, PoolContractID: "CPOOLA", AssetID: "CASSETA", PositionType: contractsv1.PositionTypeCollateral, BTokensRaw: "10000000000"},
				{Address: address, PoolContractID: "CPOOLB", AssetID: "CASSETB", PositionType: contractsv1.PositionTypeLiability, DTokensRaw: "1000000000"},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	summary := findSummary(out, address)
	if summary == nil {
		t.Fatalf("summary missing")
	}
	if summary.HealthFactor != "0" {
		t.Fatalf("expected worst-pool health factor 0, got %s", summary.HealthFactor)
	}
	if summary.EffectiveCollateralUSD != "0" {
		t.Fatalf("expected effective_collateral_usd from worst pool, got %s", summary.EffectiveCollateralUSD)
	}
	if summary.Metadata["risk_semantics"] != "blend_pool_isolated" {
		t.Fatalf("expected blend_pool_isolated semantics")
	}
	if summary.Metadata["summary_health_factor_semantics"] != "worst_pool" {
		t.Fatalf("expected worst_pool semantics")
	}
	if summary.Metadata["pool_breakdown"] == "" {
		t.Fatalf("expected pool_breakdown metadata")
	}
	var breakdown map[string]any
	if err := json.Unmarshal([]byte(summary.Metadata["pool_breakdown"]), &breakdown); err != nil {
		t.Fatalf("pool_breakdown should be valid JSON: %v", err)
	}
	if _, ok := breakdown["CPOOLA"]; !ok {
		t.Fatalf("expected pool breakdown for CPOOLA")
	}
	if _, ok := breakdown["CPOOLB"]; !ok {
		t.Fatalf("expected pool breakdown for CPOOLB")
	}
}

func TestBackstopShareAndTokenAccounting(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	unlockAt := time.Unix(2000, 0).UTC()
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 3,
		CloseTime: time.Unix(20, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID: "CPOOL",
				WasmHash:   "wasm-v2",
			}},
			Backstops: []contractsv1.BackstopPosition{{
				Address:              "GBACKSTOP",
				PoolContractID:       "CPOOL",
				UserSharesRaw:        "300",
				PoolSharesRaw:        "8000",
				PoolTokensRaw:        "10000",
				Q4W:                  []contractsv1.Q4WEntry{{SharesRaw: "100", UnlockAt: unlockAt}},
				LPTokenSupplyRaw:     "10000",
				LPBLNDReserveRaw:     "20000",
				LPUSDCReserveRaw:     "30000",
				BLNDDecimals:         7,
				USDCDecimals:         7,
				BLNDPriceUSD:         "2",
				USDCPriceUSD:         "1",
				BackstopInterestAPY:  "0.1",
				BackstopEmissionsAPY: "",
			}},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	var backstop *contractsv1.Position
	for i := range out.Positions {
		if out.Positions[i].PositionType == contractsv1.PositionTypeBackstop {
			backstop = &out.Positions[i]
			break
		}
	}
	if backstop == nil {
		t.Fatalf("expected backstop position")
	}
	if backstop.ShareAmount != "400" {
		t.Fatalf("expected total shares 400, got %s", backstop.ShareAmount)
	}
	if backstop.AssetAmount != "500" {
		t.Fatalf("expected total LP tokens 500, got %s", backstop.AssetAmount)
	}
	if backstop.Metadata["active_lp_tokens"] != "375" {
		t.Fatalf("expected active LP tokens 375, got %s", backstop.Metadata["active_lp_tokens"])
	}
	if backstop.Metadata["queued_lp_tokens"] != "125" {
		t.Fatalf("expected queued LP tokens 125, got %s", backstop.Metadata["queued_lp_tokens"])
	}
	if backstop.APY != "" {
		t.Fatalf("expected NULL APY when emissions APR is missing, got %s", backstop.APY)
	}
	if backstop.Metadata["apr_partial"] != "true" {
		t.Fatalf("expected apr_partial metadata")
	}
	if len(backstop.Q4WEntries) != 1 || !backstop.Q4WEntries[0].UnlockAt.Equal(unlockAt) {
		t.Fatalf("expected q4w entries preserved")
	}
}

func TestReservePositionAPRMaterializesOnlyWhenEmissionsKnown(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 4,
		CloseTime: time.Unix(30, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				WasmHash:         "wasm-v2",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:            "CASSET",
					AssetDecimals:      7,
					BRateRaw:           "1000000000000",
					DRateRaw:           "1000000000000",
					BSupplyRaw:         "100000000",
					DSupplyRaw:         "10000000",
					CFactorRaw:         "8000000",
					LFactorRaw:         "10000000",
					UtilTargetRaw:      "5000000",
					MaxUtilRaw:         "9500000",
					RBaseRaw:           "0",
					ROneRaw:            "1000000",
					RTwoRaw:            "0",
					RThreeRaw:          "0",
					RateModifierRaw:    "10000000",
					SupplyCapRaw:       "100000000000",
					OraclePriceRaw:     "100000000",
					OracleDecimals:     8,
					SupplyEmissionsAPR: "0.003",
					BorrowEmissionsAPR: "0.005",
				}},
			}},
			Users: []contractsv1.UserReservePosition{
				{Address: "GAPR", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeSupply, BTokensRaw: "10000000"},
				{Address: "GAPR", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeLiability, DTokensRaw: "10000000"},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	supply := findPosition(out, contractsv1.PositionTypeSupply)
	if supply == nil {
		t.Fatalf("expected supply position")
	}
	if supply.APY != "0.005" {
		t.Fatalf("expected supply net APR 0.005, got %s", supply.APY)
	}
	if supply.Metadata["net_supply_apr"] != "0.005" {
		t.Fatalf("expected net_supply_apr metadata")
	}

	liability := findPosition(out, contractsv1.PositionTypeLiability)
	if liability == nil {
		t.Fatalf("expected liability position")
	}
	if liability.APY != "0.015" {
		t.Fatalf("expected liability net APR 0.015, got %s", liability.APY)
	}
	if liability.Metadata["net_borrow_apr"] != "0.015" {
		t.Fatalf("expected net_borrow_apr metadata")
	}
}

func TestReservePositionAPRMissingEmissionsStaysPartial(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 5,
		CloseTime: time.Unix(40, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				WasmHash:         "wasm-v2",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         "CASSET",
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "100000000",
					DSupplyRaw:      "10000000",
					CFactorRaw:      "8000000",
					LFactorRaw:      "10000000",
					UtilTargetRaw:   "5000000",
					MaxUtilRaw:      "9500000",
					RBaseRaw:        "0",
					ROneRaw:         "1000000",
					RTwoRaw:         "0",
					RThreeRaw:       "0",
					RateModifierRaw: "10000000",
					SupplyCapRaw:    "100000000000",
					OraclePriceRaw:  "100000000",
					OracleDecimals:  8,
				}},
			}},
			Users: []contractsv1.UserReservePosition{
				{Address: "GPARTIAL", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeSupply, BTokensRaw: "10000000"},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	supply := findPosition(out, contractsv1.PositionTypeSupply)
	if supply == nil {
		t.Fatalf("expected supply position")
	}
	if supply.APY != "" {
		t.Fatalf("expected NULL APY when emissions APR is missing, got %s", supply.APY)
	}
	if supply.Metadata["apr_partial"] != "true" || supply.Metadata["emissions_apr_unavailable"] != "true" {
		t.Fatalf("expected partial APR metadata")
	}
}

func TestReservePositionEmissionsSurfaceWhenBaseAPRInvalid(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	// UtilTargetRaw == 0 with non-zero utilization makes the borrow (and hence
	// supply) base APR invalid, so apr stays partial; but the raw emissions APRs
	// parse and must be surfaced independently into metadata.
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 6,
		CloseTime: time.Unix(50, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				WasmHash:         "wasm-v2",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:            "CASSET",
					AssetDecimals:      7,
					BRateRaw:           "1000000000000",
					DRateRaw:           "1000000000000",
					BSupplyRaw:         "100000000",
					DSupplyRaw:         "10000000",
					CFactorRaw:         "8000000",
					LFactorRaw:         "10000000",
					UtilTargetRaw:      "0",
					MaxUtilRaw:         "9500000",
					RBaseRaw:           "0",
					ROneRaw:            "1000000",
					RTwoRaw:            "0",
					RThreeRaw:          "0",
					RateModifierRaw:    "10000000",
					SupplyCapRaw:       "100000000000",
					OraclePriceRaw:     "100000000",
					OracleDecimals:     8,
					SupplyEmissionsAPR: "0.003",
					BorrowEmissionsAPR: "0.005",
				}},
			}},
			Users: []contractsv1.UserReservePosition{
				{Address: "GEMIT", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeSupply, BTokensRaw: "10000000"},
				{Address: "GEMIT", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeLiability, DTokensRaw: "10000000"},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}

	supply := findPosition(out, contractsv1.PositionTypeSupply)
	if supply == nil {
		t.Fatalf("expected supply position")
	}
	if supply.APY != "" {
		t.Fatalf("expected NULL APY when base APR invalid, got %s", supply.APY)
	}
	if supply.Metadata["apr_partial"] != "true" {
		t.Fatalf("expected apr_partial=true on supply, got %q", supply.Metadata["apr_partial"])
	}
	if supply.Metadata["supply_emissions_apr"] != "0.003" {
		t.Fatalf("expected supply_emissions_apr surfaced as 0.003, got %q", supply.Metadata["supply_emissions_apr"])
	}
	if supply.Metadata["net_supply_apr"] != "" {
		t.Fatalf("expected no net_supply_apr when base APR invalid, got %q", supply.Metadata["net_supply_apr"])
	}

	liability := findPosition(out, contractsv1.PositionTypeLiability)
	if liability == nil {
		t.Fatalf("expected liability position")
	}
	if liability.APY != "" {
		t.Fatalf("expected NULL APY when base APR invalid, got %s", liability.APY)
	}
	if liability.Metadata["apr_partial"] != "true" {
		t.Fatalf("expected apr_partial=true on liability, got %q", liability.Metadata["apr_partial"])
	}
	if liability.Metadata["borrow_emissions_apr"] != "0.005" {
		t.Fatalf("expected borrow_emissions_apr surfaced as 0.005, got %q", liability.Metadata["borrow_emissions_apr"])
	}
	if liability.Metadata["net_borrow_apr"] != "" {
		t.Fatalf("expected no net_borrow_apr when base APR invalid, got %q", liability.Metadata["net_borrow_apr"])
	}
}

func TestActivityUSDUsesEventLedgerPriceOnly(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	walletID := validAccountString(t, 60)
	poolID := validContractString(t, 61)
	assetID := validContractString(t, 62)
	raw, _ := json.Marshal(map[string]any{
		"type":   "supply",
		"amount": "10000000",
		"wallet": walletID,
		"asset":  assetID,
	})

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 6,
		CloseTime: time.Unix(50, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  6,
			TxHash:     "tx-activity-price",
			EventIndex: 0,
			ContractID: poolID,
			Topic:      "blend supply",
			RawEvent:   raw,
			CloseTime:  time.Unix(50, 0).UTC(),
			Metadata: map[string]string{
				"event_ledger_usd_price": "2",
				"asset_decimals":         "7",
			},
		}},
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID: poolID,
				WasmHash:   "wasm-v2",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         assetID,
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "0",
					DSupplyRaw:      "0",
					CFactorRaw:      "8000000",
					LFactorRaw:      "10000000",
					UtilTargetRaw:   "5000000",
					MaxUtilRaw:      "9500000",
					RBaseRaw:        "0",
					ROneRaw:         "0",
					RTwoRaw:         "0",
					RThreeRaw:       "0",
					RateModifierRaw: "10000000",
					OraclePriceRaw:  "500000000",
					OracleDecimals:  8,
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 1 {
		t.Fatalf("expected one activity, got %d", len(out.Activities))
	}
	if out.Activities[0].USDValue != "2" {
		t.Fatalf("expected frozen event USD value 2, got %s", out.Activities[0].USDValue)
	}
	if out.Activities[0].Metadata["usd_value_source"] != "event_ledger_price" {
		t.Fatalf("expected event ledger price source metadata")
	}
}

func TestActivityUSDMissingEventPriceStaysNull(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	walletID := validAccountString(t, 63)
	poolID := validContractString(t, 64)
	assetID := validContractString(t, 65)
	raw, _ := json.Marshal(map[string]any{
		"type":   "supply",
		"amount": "10000000",
		"wallet": walletID,
		"asset":  assetID,
	})

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 7,
		CloseTime: time.Unix(60, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  7,
			TxHash:     "tx-activity-no-price",
			EventIndex: 0,
			ContractID: poolID,
			Topic:      "blend supply",
			RawEvent:   raw,
			CloseTime:  time.Unix(60, 0).UTC(),
		}},
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID: poolID,
				WasmHash:   "wasm-v2",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         assetID,
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "0",
					DSupplyRaw:      "0",
					CFactorRaw:      "8000000",
					LFactorRaw:      "10000000",
					UtilTargetRaw:   "5000000",
					MaxUtilRaw:      "9500000",
					RBaseRaw:        "0",
					ROneRaw:         "0",
					RTwoRaw:         "0",
					RThreeRaw:       "0",
					RateModifierRaw: "10000000",
					OraclePriceRaw:  "500000000",
					OracleDecimals:  8,
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 1 {
		t.Fatalf("expected one activity, got %d", len(out.Activities))
	}
	if out.Activities[0].USDValue != "" {
		t.Fatalf("expected NULL USD value without event price, got %s", out.Activities[0].USDValue)
	}
	if out.Activities[0].Metadata["event_price_unavailable"] != "true" {
		t.Fatalf("expected missing event price metadata")
	}
}

func TestLiquidationScenariosArePoolScoped(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"wasm-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 8,
		CloseTime: time.Unix(70, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				WasmHash:         "wasm-v2",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         "CASSET",
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "1000000000",
					DSupplyRaw:      "500000000",
					CFactorRaw:      "8000000",
					LFactorRaw:      "10000000",
					UtilTargetRaw:   "5000000",
					MaxUtilRaw:      "9500000",
					RBaseRaw:        "0",
					ROneRaw:         "0",
					RTwoRaw:         "0",
					RThreeRaw:       "0",
					RateModifierRaw: "10000000",
					SupplyCapRaw:    "100000000000",
					OraclePriceRaw:  "100000000",
					OracleDecimals:  8,
				}},
			}},
			Users: []contractsv1.UserReservePosition{
				{Address: "GLIQ", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeCollateral, BTokensRaw: "1000000000"},
				{Address: "GLIQ", PoolContractID: "CPOOL", AssetID: "CASSET", PositionType: contractsv1.PositionTypeLiability, DTokensRaw: "500000000"},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	summary := findSummary(out, "GLIQ")
	if summary == nil {
		t.Fatalf("expected summary")
	}
	if summary.LiquidationPrice != "" {
		t.Fatalf("expected canonical liquidation_price to remain NULL, got %s", summary.LiquidationPrice)
	}
	breakdown, ok := summary.StructuredMetadata["pool_breakdown"].(map[string]poolBreakdownEntry)
	if !ok {
		t.Fatalf("expected structured pool breakdown")
	}
	scenario := breakdown["CPOOL"].LiquidationPriceScenarios["CASSET"]
	if scenario != "0.625" {
		t.Fatalf("expected CASSET liquidation scenario 0.625, got %s", scenario)
	}
}

func findPosition(output *contractsv1.TransformOutput, positionType contractsv1.PositionType) *contractsv1.Position {
	for i := range output.Positions {
		if output.Positions[i].PositionType == positionType {
			return &output.Positions[i]
		}
	}
	return nil
}
