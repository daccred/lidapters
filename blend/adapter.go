package blend

import (
	"fmt"
	"sort"

	"github.com/daccred/lidapters/bindings"
	"github.com/daccred/lidapters/blend/contracts"
	"github.com/stellar/go-stellar-sdk/strkey"
)

// Adapter satisfies bindings.ProtocolAdapter: event decode (Transform),
// state decode (DecodeState, in state.go), and ownership (OwnsContract).
var _ bindings.ProtocolAdapter = (*Adapter)(nil)

// Adapter also owns its low-frequency config across process restarts: it declares
// the storage schema, emits config records, and rehydrates the seed state. See
// config_state.go.
var _ bindings.ConfigStateful = (*Adapter)(nil)

// Adapter decodes time-dependent oracle state (the aggregators' MaxAge
// staleness window) when the host supplies the ledger close time. See
// state_reflector.go.
var _ bindings.CloseTimeStateDecoder = (*Adapter)(nil)

type Adapter struct {
	cfg Config
	// contracts is the owned contract-ID set OwnsContract checks. It is
	// config-like ownership, not per-ledger scratch, so it does not affect the
	// DecodeState purity guarantee. Seeded empty; the relay projector feeds
	// discovered pools via RegisterContracts.
	contracts map[string]struct{}
	// assets is the registered token-contract set: reserve assets the relay edge
	// feeds via RegisterAssetContracts once a pool's reserve list reveals them.
	// It is checked ahead of the generic pool-instance branch in the reducer so a
	// registered asset's instance entry is always decoded on the SAC/SEP-41 path
	// and never mistaken for a pool — critical for a wasm-backed SEP-41 token,
	// which would otherwise pass the pool branch's wasm-hash sniff. Same
	// config-like status as contracts; does not affect DecodeState purity.
	assets map[string]struct{}
	// feeds is the registered Reflector price-feed set the relay edge fills via
	// RegisterPriceFeeds. A feed's contract_data (per-round price entries plus
	// the instance's asset list) is routed onto the Reflector decode path ahead
	// of every other branch — a feed instance carries a wasm executable and
	// would otherwise be misdecoded as a phantom pool. Same config-like status
	// as contracts; does not affect DecodeState purity.
	feeds map[string]struct{}
}

func New(cfg Config) (*Adapter, error) {
	merged := DefaultConfig()
	if cfg.AdapterID != "" {
		merged.AdapterID = cfg.AdapterID
	}
	if cfg.Protocol != "" {
		merged.Protocol = cfg.Protocol
	}
	if cfg.V2Scalar != "" {
		merged.V2Scalar = cfg.V2Scalar
	}
	merged.AllowUnknownV2 = cfg.AllowUnknownV2
	merged.V2WasmHashes = map[string]struct{}{}
	for hash := range cfg.V2WasmHashes {
		merged.V2WasmHashes[hash] = struct{}{}
	}

	if merged.AdapterID == "" {
		return nil, fmt.Errorf("adapter id is required")
	}
	if merged.Protocol == "" {
		return nil, fmt.Errorf("protocol is required")
	}
	if merged.V2Scalar == "" {
		return nil, fmt.Errorf("v2 scalar is required")
	}
	return &Adapter{cfg: merged, contracts: map[string]struct{}{}}, nil
}

func (a *Adapter) ID() string {
	return a.cfg.AdapterID
}

func (a *Adapter) Protocol() string {
	return a.cfg.Protocol
}

func (a *Adapter) Transform(input bindings.TransformInput) (*bindings.TransformOutput, error) {
	out := &bindings.TransformOutput{
		LedgerSeq:  input.LedgerSeq,
		Activities: make([]bindings.Activity, 0, len(input.Events)),
		Positions:  make([]bindings.Position, 0, 32),
		Summaries:  make([]bindings.PositionSummary, 0, 32),
		Reserves:   make([]bindings.Reserve, 0, 16),
		Contracts:  make([]bindings.Contract, 0, 8),
		Quarantine: make([]bindings.QuarantineEvent, 0, 8),
	}

	for _, evt := range input.Events {
		decoded := decodeEvent(evt)
		if !decoded.isBlend {
			continue
		}
		if decoded.activityType == "" {
			out.Quarantine = append(out.Quarantine, bindings.QuarantineEvent{
				ID:         stableID(a.cfg.AdapterID, fmt.Sprintf("%d", evt.LedgerSeq), evt.TxHash, fmt.Sprintf("%d", evt.EventIndex), "unknown"),
				AdapterID:  a.cfg.AdapterID,
				LedgerSeq:  evt.LedgerSeq,
				TxHash:     evt.TxHash,
				EventIndex: evt.EventIndex,
				ContractID: evt.ContractID,
				Reason:     decoded.reason,
				RawEvent:   evt.RawEvent,
				Metadata:   decoded.metadata,
			})
			continue
		}
		if decoded.activityType == contracts.ActivityTypeStatusChange && decoded.address == "" {
			decoded.address = evt.ContractID
		}
		if reason := activityIdentityFailure(decoded, evt); reason != "" {
			out.Quarantine = append(out.Quarantine, bindings.QuarantineEvent{
				ID:         stableID(a.cfg.AdapterID, fmt.Sprintf("%d", evt.LedgerSeq), evt.TxHash, fmt.Sprintf("%d", evt.EventIndex), reason),
				AdapterID:  a.cfg.AdapterID,
				LedgerSeq:  evt.LedgerSeq,
				TxHash:     evt.TxHash,
				EventIndex: evt.EventIndex,
				ContractID: evt.ContractID,
				Reason:     reason,
				RawEvent:   evt.RawEvent,
				Metadata:   decoded.metadata,
			})
			continue
		}
		txHash := evt.TxHash
		eventIndex := evt.EventIndex
		if decoded.activityType == contracts.ActivityTypeStatusChange {
			// Gold's lifecycle_synthetic_identity constraint keys a status change
			// as a per-ledger contract fact, not a per-event one:
			// tx_hash = status:<contract>:<ledger>, event_index = 0. The raw
			// event's tx hash and index would violate the constraint, so emit the
			// synthetic identity (and derive the stable ID from it too, so it stays
			// deterministic regardless of which raw event carried the change).
			txHash = statusChangeTxHash(evt.ContractID, evt.LedgerSeq)
			eventIndex = 0
		}
		out.Activities = append(out.Activities, bindings.Activity{
			ID:           stableID(a.cfg.Protocol, fmt.Sprintf("%d", evt.LedgerSeq), txHash, fmt.Sprintf("%d", eventIndex), string(decoded.activityType)),
			LedgerSeq:    evt.LedgerSeq,
			TxHash:       txHash,
			EventIndex:   eventIndex,
			ContractID:   evt.ContractID,
			Address:      decoded.address,
			Protocol:     a.cfg.Protocol,
			ActivityType: decoded.activityType,
			AssetID:      decoded.assetID,
			AmountRaw:    decoded.amountRaw,
			ShareAmount:  decoded.shareRaw,
			ShareType:    shareTypeForEvent(decoded.eventName, decoded.activityType),
			Direction:    decoded.direction,
			Timestamp:    evt.CloseTime,
			Metadata:     decoded.metadata,
		})
	}

	if err := a.computeState(input, out); err != nil {
		return nil, err
	}

	// Emit tombstones for positions that disappeared since the prior ledger.
	// The adapter is stateless across ledgers, so the relay passes the prior
	// ledger's gold Position output via TransformInput.PriorPositions. Any
	// position ID present in PriorPositions but absent from the current output
	// is a leg that went to zero or was evicted — emit a tombstone so the relay
	// can insert an is_deleted=TRUE row at this ledger.
	emitTombstones(input, out)

	return out, nil
}

// statusChangeTxHash builds the synthetic transaction hash gold expects for a
// contract_status_change activity. It MUST match relay migration 001's
// lifecycle_synthetic_identity CHECK exactly:
//
//	tx_hash = 'status:' || contract || ':' || ledger
//
// where ledger is the integer column rendered as text (no zero-padding).
func statusChangeTxHash(contractID string, ledgerSeq int64) string {
	return fmt.Sprintf("status:%s:%d", contractID, ledgerSeq)
}

func activityIdentityFailure(decoded decodedEvent, evt bindings.RawEventEnvelope) string {
	if decoded.address == "" {
		return "missing_activity_address"
	}
	if !strkey.IsValidContractAddress(evt.ContractID) {
		return "invalid_activity_contract"
	}
	if decoded.assetID != "" && !strkey.IsValidContractAddress(decoded.assetID) {
		return "invalid_activity_asset"
	}
	if decoded.activityType == contracts.ActivityTypeStatusChange {
		if decoded.address != evt.ContractID || !strkey.IsValidContractAddress(decoded.address) {
			return "invalid_activity_address"
		}
		return ""
	}
	// Soroban contracts can be Blend users (vaults/strategies routinely supply
	// on behalf of themselves), so both account and contract StrKeys are valid
	// activity actors.
	if !strkey.IsValidEd25519PublicKey(decoded.address) && !strkey.IsValidContractAddress(decoded.address) {
		return "invalid_activity_address"
	}
	return ""
}

// emitTombstones diffs the prior ledger's gold output against the current
// output and emits PositionTombstones and SummaryTombstones for entities that
// disappeared. A position leg disappears when it went to zero (the on-chain
// blob still exists but the leg's amount reached zero and was filtered by
// positionsFromMap) or when the entire Positions entry was evicted/removed
// (applyDelete deleted the blob so build() no longer iterates it).
//
// The relay passes PriorPositions via TransformInput; we build a set of
// current position IDs and any prior ID not in that set is a tombstone.
//
// Summary tombstones are emitted only when an address had a prior summary
// and has no positions at all in the current output (fully closed).
func emitTombstones(input bindings.TransformInput, out *bindings.TransformOutput) {
	if len(input.PriorPositions) == 0 && len(input.PriorSummaries) == 0 {
		return
	}

	// Build a set of current position identity keys: address|protocol|contract|asset|type
	currentPosKeys := make(map[string]struct{}, len(out.Positions))
	for _, p := range out.Positions {
		key := positionIdentityKey(p.Address, p.Protocol, p.ContractID, p.AssetID, string(p.PositionType))
		currentPosKeys[key] = struct{}{}
	}

	// Diff: any prior position ID not in current set → tombstone
	for _, pp := range input.PriorPositions {
		key := positionIdentityKey(pp.Address, pp.Protocol, pp.ContractID, pp.AssetID, string(pp.PositionType))
		if _, exists := currentPosKeys[key]; !exists {
			out.PositionTombstones = append(out.PositionTombstones, bindings.PositionTombstone{
				Address:      pp.Address,
				Protocol:     pp.Protocol,
				ContractID:   pp.ContractID,
				AssetID:      pp.AssetID,
				PositionType: string(pp.PositionType),
				LedgerSeq:    input.LedgerSeq,
			})
		}
	}

	// Sort tombstones for deterministic output
	sort.Slice(out.PositionTombstones, func(i, j int) bool {
		a, b := out.PositionTombstones[i], out.PositionTombstones[j]
		return a.Address < b.Address ||
			(a.Address == b.Address && a.ContractID < b.ContractID) ||
			(a.Address == b.Address && a.ContractID == b.ContractID && a.AssetID < b.AssetID) ||
			(a.Address == b.Address && a.ContractID == b.ContractID && a.AssetID == b.AssetID && a.PositionType < b.PositionType)
	})

	// Summary tombstones: an address has a prior summary but no positions now
	if len(input.PriorSummaries) > 0 {
		// Collect addresses that still have positions in the current output
		addressesWithPositions := make(map[string]bool)
		for _, p := range out.Positions {
			addressesWithPositions[p.Address] = true
		}

		// Also check which addresses are in current summaries
		currentSummaryAddrs := make(map[string]bool)
		for _, s := range out.Summaries {
			currentSummaryAddrs[s.Address] = true
		}

		for _, ps := range input.PriorSummaries {
			if !addressesWithPositions[ps.Address] && !currentSummaryAddrs[ps.Address] {
				out.SummaryTombstones = append(out.SummaryTombstones, bindings.SummaryTombstone{
					Address:   ps.Address,
					Protocol:  ps.Protocol,
					LedgerSeq: input.LedgerSeq,
				})
			}
		}

		sort.Slice(out.SummaryTombstones, func(i, j int) bool {
			a, b := out.SummaryTombstones[i], out.SummaryTombstones[j]
			return a.Address < b.Address || (a.Address == b.Address && a.Protocol < b.Protocol)
		})
	}
}

func positionIdentityKey(address, protocol, contractID, assetID, posType string) string {
	return address + "|" + protocol + "|" + contractID + "|" + assetID + "|" + posType
}
