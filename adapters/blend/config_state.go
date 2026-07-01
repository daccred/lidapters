package blend

// Blend's side of the config-persistence inversion of control. The adapter owns
// its low-frequency config: it declares the storage schema (ConfigSchema), emits
// this ledger's config changes as opaque records (ConfigRecords), and rebuilds the
// seed LedgerState from persisted records on cold start (HydrateConfig). The relay
// is a generic host that stores and returns records without decoding them.
//
// Config vs data split (persist ONLY the low-frequency half):
//   - oracle: decimals + asset->index map (NOT the per-index prices).
//   - pool:   oracle/backstop refs, status, take rate, wasm hash (NOT reserves).
//   - reserve: ResConfig — index, decimals, factors, rate-curve, caps (NOT ResData:
//     the b/d rate accumulators and supplies, which re-fold from bronze).
//
// All three methods are pure: ConfigRecords/HydrateConfig read only their inputs,
// so the fold stays run-twice byte-identical.

import (
	"encoding/json"
	"sort"

	contractsv1 "github.com/daccred/lidapters/contracts/v1"
	"github.com/stellar/go-stellar-sdk/xdr"
)

const (
	kindOracle  = "blend.oracle"
	kindPool    = "blend.pool"
	kindReserve = "blend.reserve"

	tableOracle  = "blend_oracle_config"
	tablePool    = "blend_pool_config"
	tableReserve = "blend_reserve_config"

	// reserveKeySep joins the pool id and asset id into the reserve's opaque
	// entity_key. The host treats the whole string as opaque; hydration recovers the
	// pool binding from the payload's pool_id, not by splitting this key.
	reserveKeySep = "|"
)

// factorScaleExpr is the 7-decimal fixed-point divisor Blend stores its factors and
// rate-curve params at. Analytics generated columns divide the raw payload value by
// it so a query reads 0.95 rather than 9500000. It is approximate-for-analytics; the
// exact raw string stays in payload.
const factorScaleExpr = "1e7"

// --- schema manifest --------------------------------------------------------

// ConfigSchema declares one table per config kind plus the STORED generated
// columns that expose the jsonb payload to SQL analytics. The host renders the DDL
// from this and imports none of these Blend field names — they live here.
func (a *Adapter) ConfigSchema() []contractsv1.ConfigTableSchema {
	factor := func(name, jsonKey string) contractsv1.ConfigGeneratedColumn {
		return contractsv1.ConfigGeneratedColumn{
			Name: name, SQLType: "numeric",
			Expr: "NULLIF(payload->>'" + jsonKey + "','')::numeric / " + factorScaleExpr,
		}
	}
	return []contractsv1.ConfigTableSchema{
		{
			Kind:  kindOracle,
			Table: tableOracle,
			Generated: []contractsv1.ConfigGeneratedColumn{
				{Name: "decimals", SQLType: "int", Expr: "NULLIF(payload->>'decimals','')::int"},
				{Name: "asset_count", SQLType: "int", Expr: "CASE WHEN jsonb_typeof(payload->'assets') = 'array' THEN jsonb_array_length(payload->'assets') ELSE 0 END"},
			},
			Indexes: []contractsv1.ConfigIndex{
				{Name: "idx_blend_oracle_config_decimals", Columns: []string{"decimals"}},
			},
		},
		{
			Kind:  kindPool,
			Table: tablePool,
			Generated: []contractsv1.ConfigGeneratedColumn{
				{Name: "status", SQLType: "text", Expr: "payload->>'status'"},
				{Name: "backstop_take_rate", SQLType: "numeric", Expr: "NULLIF(payload->>'take_rate','')::numeric / " + factorScaleExpr},
				{Name: "oracle_ref", SQLType: "text", Expr: "payload->>'oracle'"},
				{Name: "backstop_ref", SQLType: "text", Expr: "payload->>'backstop'"},
			},
			Indexes: []contractsv1.ConfigIndex{
				{Name: "idx_blend_pool_config_status", Columns: []string{"status"}},
				{Name: "idx_blend_pool_config_oracle_ref", Columns: []string{"oracle_ref"}},
			},
		},
		{
			Kind:  kindReserve,
			Table: tableReserve,
			Generated: []contractsv1.ConfigGeneratedColumn{
				{Name: "pool_id", SQLType: "text", Expr: "payload->>'pool_id'"},
				{Name: "asset_id", SQLType: "text", Expr: "payload->>'asset_id'"},
				{Name: "reserve_index", SQLType: "int", Expr: "NULLIF(payload->>'index','')::int"},
				factor("c_factor", "c_factor"),
				factor("l_factor", "l_factor"),
				factor("util_target", "util_target"),
				factor("max_util", "max_util"),
				factor("r_base", "r_base"),
				factor("r_one", "r_one"),
				factor("r_two", "r_two"),
				factor("r_three", "r_three"),
				{Name: "supply_cap", SQLType: "numeric", Expr: "NULLIF(payload->>'supply_cap','')::numeric"},
			},
			Indexes: []contractsv1.ConfigIndex{
				{Name: "idx_blend_reserve_config_pool", Columns: []string{"pool_id"}},
				{Name: "idx_blend_reserve_config_asset", Columns: []string{"asset_id"}},
			},
		},
	}
}

// --- payload bodies (canonical JSON; the adapter owns the shape) ------------

type oracleConfigBody struct {
	Decimals int32             `json:"decimals"`
	Assets   []oracleAssetBody `json:"assets"`
}

type oracleAssetBody struct {
	AssetID string `json:"asset_id"`
	Index   int64  `json:"index"`
}

type poolConfigBody struct {
	Oracle   string `json:"oracle"`
	Backstop string `json:"backstop"`
	Status   string `json:"status"`
	TakeRate string `json:"take_rate"`
	WasmHash string `json:"wasm_hash"`
}

type reserveConfigBody struct {
	PoolID     string `json:"pool_id"`
	AssetID    string `json:"asset_id"`
	Index      int32  `json:"index"`
	Decimals   int32  `json:"decimals"`
	CFactor    string `json:"c_factor"`
	LFactor    string `json:"l_factor"`
	UtilTarget string `json:"util_target"`
	MaxUtil    string `json:"max_util"`
	RBase      string `json:"r_base"`
	ROne       string `json:"r_one"`
	RTwo       string `json:"r_two"`
	RThree     string `json:"r_three"`
	SupplyCap  string `json:"supply_cap"`
}

// --- record emission (chain-signal, pure) -----------------------------------

// ConfigRecords emits this ledger's config changes. It classifies the owned
// contract-data keys to find which config entities changed (pool Config/ResList,
// reserve ResConfig, oracle instance), then reads each dirty entity's current
// config from the freshly folded next state and serializes it. A removed config
// key yields a tombstone. Prices, ResData, positions and backstop balances are
// data, not config, and are never emitted here.
func (a *Adapter) ConfigRecords(next *contractsv1.LedgerState, changes []contractsv1.ContractDataChange, ledgerSeq int64) []contractsv1.ConfigRecord {
	if next == nil {
		next = &contractsv1.LedgerState{}
	}
	oracleByID := make(map[string]contractsv1.OracleState, len(next.Oracles))
	for _, o := range next.Oracles {
		oracleByID[o.ContractID] = o
	}
	poolByID := make(map[string]contractsv1.PoolState, len(next.Pools))
	for _, p := range next.Pools {
		poolByID[p.ContractID] = p
	}

	// dirty sets: value is true when the config key was removed this ledger.
	dirtyOracle := map[string]bool{}
	dirtyPool := map[string]bool{}
	type reserveRef struct{ pool, asset string }
	dirtyReserve := map[reserveRef]bool{}

	for _, ch := range changes {
		key, ok := decodeScValBase64(ch.KeyXDR)
		if !ok {
			continue
		}
		removed := !configChangeLive(ch, ledgerSeq)
		if key.Type == xdr.ScValTypeScvLedgerKeyContractInstance {
			// A contract instance is config for whichever entity it belongs to. Use
			// the folded next state to tell an oracle instance (asset map) from a
			// pool instance (wasm hash); a removed instance that is gone from next is
			// left to the Config/ResList removal path (real decommission signal).
			if _, isOracle := oracleByID[ch.ContractID]; isOracle {
				dirtyOracle[ch.ContractID] = removed
			} else if _, isPool := poolByID[ch.ContractID]; isPool {
				dirtyPool[ch.ContractID] = removed
			}
			continue
		}
		if sym, ok := scSymbol(key); ok {
			switch sym {
			case "Config", "ResList":
				dirtyPool[ch.ContractID] = removed
			}
			continue
		}
		if variant, args, ok := scVariant(key); ok && variant == "ResConfig" {
			if asset, ok := variantAddress(args); ok {
				dirtyReserve[reserveRef{ch.ContractID, asset}] = removed
			}
		}
	}

	records := make([]contractsv1.ConfigRecord, 0, len(dirtyOracle)+len(dirtyPool)+len(dirtyReserve))
	seq := uint32(ledgerSeq)

	for id, removed := range dirtyOracle {
		if removed {
			records = append(records, tombstone(kindOracle, id, seq))
			continue
		}
		o, ok := oracleByID[id]
		if !ok {
			continue
		}
		records = append(records, contractsv1.ConfigRecord{Kind: kindOracle, EntityKey: id, Ledger: seq, Payload: marshalOracleBody(o)})
	}
	for id, removed := range dirtyPool {
		if removed {
			records = append(records, tombstone(kindPool, id, seq))
			continue
		}
		p, ok := poolByID[id]
		if !ok {
			continue
		}
		records = append(records, contractsv1.ConfigRecord{Kind: kindPool, EntityKey: id, Ledger: seq, Payload: marshalPoolBody(p)})
	}
	for ref, removed := range dirtyReserve {
		entityKey := ref.pool + reserveKeySep + ref.asset
		if removed {
			records = append(records, tombstone(kindReserve, entityKey, seq))
			continue
		}
		pool, ok := poolByID[ref.pool]
		if !ok {
			continue
		}
		reserve, ok := reserveByAsset(pool, ref.asset)
		if !ok {
			continue
		}
		records = append(records, contractsv1.ConfigRecord{Kind: kindReserve, EntityKey: entityKey, Ledger: seq, Payload: marshalReserveBody(ref.pool, reserve)})
	}

	// Deterministic order so a run-twice comparison of the emitted records is stable
	// (the host writes them keyed, so order does not affect storage, but pinning it
	// keeps the seam trivially testable).
	sort.Slice(records, func(i, j int) bool {
		if records[i].Kind != records[j].Kind {
			return records[i].Kind < records[j].Kind
		}
		return records[i].EntityKey < records[j].EntityKey
	})
	return records
}

// configChangeLive mirrors the live decision the state fold applies to a change:
// live requires a present value AND a TTL that has not lapsed. Eviction or TTL
// expiry of a config key is a removal (tombstone).
func configChangeLive(ch contractsv1.ContractDataChange, ledgerSeq int64) bool {
	if !ch.Live || ch.ValueXDR == nil {
		return false
	}
	if ch.LiveUntilLedgerSeq != nil && int64(*ch.LiveUntilLedgerSeq) < ledgerSeq {
		return false
	}
	return true
}

func tombstone(kind, entityKey string, ledger uint32) contractsv1.ConfigRecord {
	return contractsv1.ConfigRecord{Kind: kind, EntityKey: entityKey, Ledger: ledger, Removed: true}
}

func reserveByAsset(pool contractsv1.PoolState, assetID string) (contractsv1.ReserveState, bool) {
	for _, r := range pool.Reserves {
		if r.AssetID == assetID {
			return r, true
		}
	}
	return contractsv1.ReserveState{}, false
}

func marshalOracleBody(o contractsv1.OracleState) []byte {
	body := oracleConfigBody{Decimals: o.Decimals}
	body.Assets = make([]oracleAssetBody, 0, len(o.Assets))
	for _, a := range o.Assets {
		body.Assets = append(body.Assets, oracleAssetBody{AssetID: a.AssetID, Index: a.Index})
	}
	sort.Slice(body.Assets, func(i, j int) bool {
		if body.Assets[i].Index != body.Assets[j].Index {
			return body.Assets[i].Index < body.Assets[j].Index
		}
		return body.Assets[i].AssetID < body.Assets[j].AssetID
	})
	return mustMarshal(body)
}

func marshalPoolBody(p contractsv1.PoolState) []byte {
	return mustMarshal(poolConfigBody{
		Oracle:   p.OracleContract,
		Backstop: p.BackstopContract,
		Status:   p.PoolStatus,
		TakeRate: p.BackstopTakeRate,
		WasmHash: p.WasmHash,
	})
}

func marshalReserveBody(poolID string, r contractsv1.ReserveState) []byte {
	return mustMarshal(reserveConfigBody{
		PoolID:     poolID,
		AssetID:    r.AssetID,
		Index:      r.ReserveIndex,
		Decimals:   r.AssetDecimals,
		CFactor:    r.CFactorRaw,
		LFactor:    r.LFactorRaw,
		UtilTarget: r.UtilTargetRaw,
		MaxUtil:    r.MaxUtilRaw,
		RBase:      r.RBaseRaw,
		ROne:       r.ROneRaw,
		RTwo:       r.RTwoRaw,
		RThree:     r.RThreeRaw,
		SupplyCap:  r.SupplyCapRaw,
	})
}

// mustMarshal serializes a config body. The bodies are plain structs of scalars
// and a sorted slice, so json.Marshal is deterministic and cannot error.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// --- hydration (pure) -------------------------------------------------------

// HydrateConfig rebuilds the seed LedgerState from the latest-per-entity config
// records the host loaded (tombstones already excluded). The result carries pool
// config (with reserve config attached) and the oracle asset->index map with
// decimals; it carries NO prices, NO ResData and NO positions — those re-fold from
// bronze after the restart. Reserve records whose pool was not loaded are dropped.
func (a *Adapter) HydrateConfig(records []contractsv1.ConfigRecord) (*contractsv1.LedgerState, error) {
	pools := map[string]*contractsv1.PoolState{}
	reservesByPool := map[string][]contractsv1.ReserveState{}
	oracles := []contractsv1.OracleState{}

	for _, rec := range records {
		if rec.Removed {
			continue
		}
		switch rec.Kind {
		case kindOracle:
			var body oracleConfigBody
			if err := json.Unmarshal(rec.Payload, &body); err != nil {
				return nil, err
			}
			oracle := contractsv1.OracleState{ContractID: rec.EntityKey, Decimals: body.Decimals}
			for _, asset := range body.Assets {
				oracle.Assets = append(oracle.Assets, contractsv1.OracleAssetIndex{AssetID: asset.AssetID, Index: asset.Index})
			}
			oracles = append(oracles, oracle)
		case kindPool:
			var body poolConfigBody
			if err := json.Unmarshal(rec.Payload, &body); err != nil {
				return nil, err
			}
			pools[rec.EntityKey] = &contractsv1.PoolState{
				ContractID:       rec.EntityKey,
				OracleContract:   body.Oracle,
				BackstopContract: body.Backstop,
				PoolStatus:       body.Status,
				BackstopTakeRate: body.TakeRate,
				WasmHash:         body.WasmHash,
			}
		case kindReserve:
			var body reserveConfigBody
			if err := json.Unmarshal(rec.Payload, &body); err != nil {
				return nil, err
			}
			reservesByPool[body.PoolID] = append(reservesByPool[body.PoolID], contractsv1.ReserveState{
				ReserveIndex:  body.Index,
				AssetID:       body.AssetID,
				AssetDecimals: body.Decimals,
				CFactorRaw:    body.CFactor,
				LFactorRaw:    body.LFactor,
				UtilTargetRaw: body.UtilTarget,
				MaxUtilRaw:    body.MaxUtil,
				RBaseRaw:      body.RBase,
				ROneRaw:       body.ROne,
				RTwoRaw:       body.RTwo,
				RThreeRaw:     body.RThree,
				SupplyCapRaw:  body.SupplyCap,
			})
		}
	}

	state := &contractsv1.LedgerState{}
	poolIDs := make([]string, 0, len(pools))
	for id := range pools {
		poolIDs = append(poolIDs, id)
	}
	sort.Strings(poolIDs)
	for _, id := range poolIDs {
		pool := pools[id]
		reserves := reservesByPool[id]
		sort.Slice(reserves, func(i, j int) bool {
			if reserves[i].ReserveIndex != reserves[j].ReserveIndex {
				return reserves[i].ReserveIndex < reserves[j].ReserveIndex
			}
			return reserves[i].AssetID < reserves[j].AssetID
		})
		pool.Reserves = reserves
		state.Pools = append(state.Pools, *pool)
	}
	sort.Slice(oracles, func(i, j int) bool { return oracles[i].ContractID < oracles[j].ContractID })
	state.Oracles = oracles
	return state, nil
}
