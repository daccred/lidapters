package blend

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestUnknownWasmHashIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{
		V2WasmHashes: map[string]struct{}{
			"known-v2": {},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 1,
		CloseTime: time.Unix(0, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{
				{
					ContractID: "CPOOLUNKNOWN",
					WasmHash:   "unknown",
					Reserves:   []contractsv1.ReserveState{},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Quarantine) == 0 {
		t.Fatalf("expected unknown wasm hash quarantine event")
	}
	if out.Quarantine[0].Reason != "unknown_wasm_hash" {
		t.Fatalf("unexpected quarantine reason: %s", out.Quarantine[0].Reason)
	}
}

func TestUnknownEventShapeIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type": "unmapped_shape",
	})
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 123,
		CloseTime: time.Unix(10, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{
			{
				LedgerSeq:  123,
				TxHash:     "tx-hash",
				EventIndex: 0,
				ContractID: "CDEMOPOOL",
				Topic:      "blend pool unknown",
				RawEvent:   raw,
				CloseTime:  time.Unix(10, 0).UTC(),
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Quarantine) != 1 {
		t.Fatalf("expected 1 quarantine event, got %d", len(out.Quarantine))
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activities, got %d", len(out.Activities))
	}
}

func TestMissingActivityAddressIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type":   "supply",
		"asset":  validContractString(t, 42),
		"amount": "1000",
	})
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 123,
		CloseTime: time.Unix(10, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  123,
			TxHash:     "tx-missing-wallet",
			EventIndex: 0,
			ContractID: validContractString(t, 41),
			Topic:      "blend supply",
			RawEvent:   raw,
			CloseTime:  time.Unix(10, 0).UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activity with missing wallet, got %d", len(out.Activities))
	}
	if len(out.Quarantine) != 1 || out.Quarantine[0].Reason != "missing_activity_address" {
		t.Fatalf("expected missing_activity_address quarantine, got %+v", out.Quarantine)
	}
}

func TestInvalidActivityAddressIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type":   "borrow",
		"wallet": "GUSER",
		"asset":  validContractString(t, 44),
		"amount": "1000",
	})
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 123,
		CloseTime: time.Unix(10, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  123,
			TxHash:     "tx-invalid-wallet",
			EventIndex: 0,
			ContractID: validContractString(t, 43),
			Topic:      "blend borrow",
			RawEvent:   raw,
			CloseTime:  time.Unix(10, 0).UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activity with invalid wallet, got %d", len(out.Activities))
	}
	if len(out.Quarantine) != 1 || out.Quarantine[0].Reason != "invalid_activity_address" {
		t.Fatalf("expected invalid_activity_address quarantine, got %+v", out.Quarantine)
	}
}

func TestInvalidActivityAssetIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type":   "deposit",
		"wallet": validAccountString(t, 46),
		"asset":  "CASSET",
		"amount": "1000",
	})
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 123,
		CloseTime: time.Unix(10, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  123,
			TxHash:     "tx-invalid-asset",
			EventIndex: 0,
			ContractID: validContractString(t, 45),
			Topic:      "blend deposit",
			RawEvent:   raw,
			CloseTime:  time.Unix(10, 0).UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activity with invalid asset, got %d", len(out.Activities))
	}
	if len(out.Quarantine) != 1 || out.Quarantine[0].Reason != "invalid_activity_asset" {
		t.Fatalf("expected invalid_activity_asset quarantine, got %+v", out.Quarantine)
	}
}

func TestInvalidActivityContractIsQuarantined(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type":   "deposit",
		"wallet": validAccountString(t, 54),
		"asset":  validContractString(t, 55),
		"amount": "1000",
	})
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 123,
		CloseTime: time.Unix(10, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  123,
			TxHash:     "tx-invalid-contract",
			EventIndex: 0,
			ContractID: "CPOOL",
			Topic:      "blend deposit",
			RawEvent:   raw,
			CloseTime:  time.Unix(10, 0).UTC(),
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activity with invalid contract, got %d", len(out.Activities))
	}
	if len(out.Quarantine) != 1 || out.Quarantine[0].Reason != "invalid_activity_contract" {
		t.Fatalf("expected invalid_activity_contract quarantine, got %+v", out.Quarantine)
	}
}

func TestTopicDataXDRFragmentDoesNotBecomeAddress(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 62898493,
		CloseTime: time.Unix(11, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  62898493,
			TxHash:     "tx-data-xdr-fragment",
			EventIndex: 0,
			ContractID: validContractString(t, 47),
			Topic:      `{"data":"[1048700000 933537308]","data_xdr":"AAAAEAAAAAEAAAACAAAACgAAAAAAAAAAAAAAAD6B5GAAAAAKAAAAAAAAAAAAAAAAN6SmHA==","topics":["withdraw","CCW67TSZV3SSS2HXMBQ5JFGCKJNXKZM7UQUWUZPUTHXSTZLEO7SJMI75","CBUKYTAUPRPWZ76AENDIGCAYF2FUFKV37YZEPYJSYICMBPBPJBPKKZCR"],"topics_xdr":["AAAADwAAAAh3aXRoZHJhdw=="],"type":"ContractEventTypeContract"}`,
			CloseTime:  time.Unix(11, 0).UTC(),
			Metadata:   map[string]string{"protocol_id": "blend"},
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Activities) != 0 {
		t.Fatalf("expected no activity from partial data_xdr address, got %+v", out.Activities)
	}
	if len(out.Quarantine) != 1 || out.Quarantine[0].Reason != "missing_activity_address" {
		t.Fatalf("expected missing_activity_address quarantine, got %+v", out.Quarantine)
	}
}

func TestLifecycleStatusEventPreservesContractIdentity(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	contractID := validContractString(t, 48)
	raw := contractEventRaw(t, []xdr.ScVal{symbolVal(t, "reserve_config")}, i128Val(1))

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 126,
		CloseTime: time.Unix(12, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  126,
			TxHash:     "tx-status",
			EventIndex: 0,
			ContractID: contractID,
			Topic:      `{"topics":["reserve_config"]}`,
			RawEvent:   raw,
			CloseTime:  time.Unix(12, 0).UTC(),
			Metadata:   map[string]string{"protocol_id": "blend"},
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Quarantine) != 0 {
		t.Fatalf("expected no quarantine, got %+v", out.Quarantine)
	}
	if len(out.Activities) != 1 {
		t.Fatalf("expected one lifecycle activity, got %d", len(out.Activities))
	}
	if out.Activities[0].Address != contractID {
		t.Fatalf("expected lifecycle address to be contract, got %s", out.Activities[0].Address)
	}
}

func TestContractEventDataAddressProducesActivity(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}
	wallet := accountAddressVal(t, 7)
	asset := contractAddressVal(t, 8)
	raw := contractEventRaw(t, []xdr.ScVal{symbolVal(t, "supply")}, mapVal(t, map[string]xdr.ScVal{
		"user":   wallet,
		"asset":  asset,
		"amount": i128Val(12345),
	}))

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 124,
		CloseTime: time.Unix(11, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{{
			LedgerSeq:  124,
			TxHash:     "tx-data-wallet",
			EventIndex: 0,
			ContractID: validContractString(t, 49),
			Topic:      `{"topics":["supply"]}`,
			RawEvent:   raw,
			CloseTime:  time.Unix(11, 0).UTC(),
			Metadata:   map[string]string{"protocol_id": "blend"},
		}},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Quarantine) != 0 {
		t.Fatalf("expected no quarantine, got %+v", out.Quarantine)
	}
	if len(out.Activities) != 1 {
		t.Fatalf("expected one activity, got %d", len(out.Activities))
	}
	if out.Activities[0].Address == "" {
		t.Fatalf("expected decoded wallet address")
	}
	if out.Activities[0].AssetID == "" || out.Activities[0].AmountRaw != "12345" {
		t.Fatalf("expected decoded asset and amount, got %+v", out.Activities[0])
	}
}

func TestConfiguredSingleV2HashEnrichesPoolShapedState(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{V2WasmHashes: map[string]struct{}{"known-v2": {}}})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 125,
		CloseTime: time.Unix(12, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{{
				ContractID:       "CPOOL",
				BackstopTakeRate: "0",
				Reserves: []contractsv1.ReserveState{{
					AssetID:         "CASSET",
					AssetDecimals:   7,
					BRateRaw:        "1000000000000",
					DRateRaw:        "1000000000000",
					BSupplyRaw:      "1000",
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
				}},
			}},
			Users: []contractsv1.UserReservePosition{{
				Address:        "GUSER",
				PoolContractID: "CPOOL",
				AssetID:        "CASSET",
				PositionType:   contractsv1.PositionTypeSupply,
				BTokensRaw:     "1000",
			}},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Quarantine) != 0 {
		t.Fatalf("expected no quarantine, got %+v", out.Quarantine)
	}
	if len(out.Contracts) != 1 || out.Contracts[0].WasmHash != "known-v2" {
		t.Fatalf("expected enriched contract wasm hash, got %+v", out.Contracts)
	}
	if len(out.Reserves) != 1 || len(out.Positions) != 1 {
		t.Fatalf("expected normalized reserve and position, got reserves=%d positions=%d", len(out.Reserves), len(out.Positions))
	}
}

func TestDeterministicOutputForSameInput(t *testing.T) {
	t.Parallel()

	adapter, err := New(DefaultConfig())
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	raw, _ := json.Marshal(map[string]any{
		"type":   "borrow",
		"amount": "1000",
		"wallet": validAccountString(t, 50),
		"asset":  validContractString(t, 51),
	})

	input := contractsv1.TransformInput{
		LedgerSeq: 100,
		CloseTime: time.Unix(1000, 0).UTC(),
		Events: []contractsv1.RawEventEnvelope{
			{
				LedgerSeq:  100,
				TxHash:     "tx-1",
				EventIndex: 0,
				ContractID: validContractString(t, 52),
				Topic:      "blend borrow",
				RawEvent:   raw,
				CloseTime:  time.Unix(1000, 0).UTC(),
			},
		},
	}

	out1, err := adapter.Transform(input)
	if err != nil {
		t.Fatalf("transform 1: %v", err)
	}
	out2, err := adapter.Transform(input)
	if err != nil {
		t.Fatalf("transform 2: %v", err)
	}
	b1, _ := json.Marshal(out1)
	b2, _ := json.Marshal(out2)
	if string(b1) != string(b2) {
		t.Fatalf("expected deterministic output for same input")
	}
}

func TestStateMetadataUsesCanonicalOracleKey(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{
		V2WasmHashes: map[string]struct{}{
			"known-v2": {},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 100,
		CloseTime: time.Unix(1000, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{
				{
					ContractID: "CPOOL",
					WasmHash:   "known-v2",
					Reserves: []contractsv1.ReserveState{
						{
							AssetID:         "CASSET",
							AssetDecimals:   7,
							BRateRaw:        "10000000",
							DRateRaw:        "10000000",
							BSupplyRaw:      "10000000",
							DSupplyRaw:      "0",
							CFactorRaw:      "8000000",
							LFactorRaw:      "8500000",
							UtilTargetRaw:   "8000000",
							MaxUtilRaw:      "9500000",
							RBaseRaw:        "0",
							ROneRaw:         "0",
							RTwoRaw:         "0",
							RThreeRaw:       "0",
							RateModifierRaw: "1000000000",
							SupplyCapRaw:    "100000000",
							OraclePriceRaw:  "1250000000",
							OracleDecimals:  7,
						},
					},
				},
			},
			Users: []contractsv1.UserReservePosition{
				{
					Address:        "GUSER",
					PoolContractID: "CPOOL",
					AssetID:        "CASSET",
					PositionType:   contractsv1.PositionTypeCollateral,
					BTokensRaw:     "10000000",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if len(out.Positions) == 0 {
		t.Fatalf("expected positions from state")
	}
	pos := out.Positions[0]
	if pos.Metadata["oracle_price_usd"] == "" {
		t.Fatalf("expected canonical oracle_price_usd metadata")
	}
	if pos.Metadata["oracle_price"] == "" {
		t.Fatalf("expected legacy oracle_price metadata during transition")
	}
	if len(out.Reserves) == 0 {
		t.Fatalf("expected reserves from state")
	}
	reserve := out.Reserves[0]
	if reserve.Metadata["oracle_price_usd"] == "" {
		t.Fatalf("expected reserve canonical oracle_price_usd metadata")
	}
}

func TestBackstopMetadataCarriesQueueShape(t *testing.T) {
	t.Parallel()

	adapter, err := New(Config{
		V2WasmHashes: map[string]struct{}{
			"known-v2": {},
		},
	})
	if err != nil {
		t.Fatalf("new adapter: %v", err)
	}

	unlockAt := time.Unix(2000, 0).UTC()
	out, err := adapter.Transform(contractsv1.TransformInput{
		LedgerSeq: 101,
		CloseTime: time.Unix(1001, 0).UTC(),
		State: &contractsv1.LedgerState{
			Pools: []contractsv1.PoolState{
				{
					ContractID: "CPOOL",
					WasmHash:   "known-v2",
					Reserves:   []contractsv1.ReserveState{},
				},
			},
			Backstops: []contractsv1.BackstopPosition{
				{
					Address:               "GBACKSTOP",
					PoolContractID:        "CPOOL",
					UserSharesRaw:         "10000000",
					PoolSharesRaw:         "100000000",
					PoolTokensRaw:         "100000000",
					Q4W:                   []contractsv1.Q4WEntry{{SharesRaw: "1000000", UnlockAt: unlockAt}},
					UnclaimedEmissionsRaw: "1000",
					LPTokenSupplyRaw:      "100000000",
					LPBLNDReserveRaw:      "20000000",
					LPUSDCReserveRaw:      "30000000",
					BLNDDecimals:          7,
					USDCDecimals:          7,
					BLNDPriceUSD:          "2",
					USDCPriceUSD:          "1",
					BackstopInterestAPY:   "0.1",
					BackstopEmissionsAPY:  "0.2",
				},
			},
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
	if backstop.Metadata["q4w_unlock_at"] == "" {
		t.Fatalf("expected q4w_unlock_at metadata")
	}
	if backstop.Metadata["blnd_component"] == "" || backstop.Metadata["usdc_component"] == "" {
		t.Fatalf("expected backstop component metadata")
	}
	if len(backstop.Q4WEntries) != 1 {
		t.Fatalf("expected q4w entries, got %d", len(backstop.Q4WEntries))
	}
	if !backstop.Q4WEntries[0].UnlockAt.Equal(unlockAt) {
		t.Fatalf("unexpected q4w unlock timestamp: %s", backstop.Q4WEntries[0].UnlockAt)
	}
}

func contractEventRaw(t *testing.T, topics []xdr.ScVal, data xdr.ScVal) []byte {
	t.Helper()
	body, err := xdr.NewContractEventBody(0, xdr.ContractEventV0{
		Topics: topics,
		Data:   data,
	})
	if err != nil {
		t.Fatalf("contract event body: %v", err)
	}
	evt := xdr.ContractEvent{
		Type: xdr.ContractEventTypeContract,
		Body: body,
	}
	var raw bytes.Buffer
	if _, err := xdr.Marshal(&raw, evt); err != nil {
		t.Fatalf("marshal contract event: %v", err)
	}
	return raw.Bytes()
}

func validAccountString(t *testing.T, seed byte) string {
	t.Helper()
	address := scValAddress(accountAddressVal(t, seed))
	if address == "" {
		t.Fatalf("empty account address for seed %d", seed)
	}
	return address
}

func validContractString(t *testing.T, seed byte) string {
	t.Helper()
	address := scValAddress(contractAddressVal(t, seed))
	if address == "" {
		t.Fatalf("empty contract address for seed %d", seed)
	}
	return address
}

func accountAddressVal(t *testing.T, seed byte) xdr.ScVal {
	t.Helper()
	var raw xdr.Uint256
	raw[31] = seed
	account, err := xdr.NewAccountId(xdr.PublicKeyTypePublicKeyTypeEd25519, raw)
	if err != nil {
		t.Fatalf("account id: %v", err)
	}
	address, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeAccount, account)
	if err != nil {
		t.Fatalf("account address: %v", err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}

func contractAddressVal(t *testing.T, seed byte) xdr.ScVal {
	t.Helper()
	var hash xdr.Hash
	hash[31] = seed
	contractID := xdr.ContractId(hash)
	address, err := xdr.NewScAddress(xdr.ScAddressTypeScAddressTypeContract, contractID)
	if err != nil {
		t.Fatalf("contract address: %v", err)
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &address}
}

func symbolVal(t *testing.T, raw string) xdr.ScVal {
	t.Helper()
	sym := xdr.ScSymbol(raw)
	value, err := xdr.NewScVal(xdr.ScValTypeScvSymbol, sym)
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	return value
}

func mapVal(t *testing.T, fields map[string]xdr.ScVal) xdr.ScVal {
	t.Helper()
	entries := make(xdr.ScMap, 0, len(fields))
	for key, value := range fields {
		entries = append(entries, xdr.ScMapEntry{
			Key: symbolVal(t, key),
			Val: value,
		})
	}
	ptr := &entries
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &ptr}
}

func i128Val(value int64) xdr.ScVal {
	raw := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(value)}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &raw}
}
