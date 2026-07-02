package blend

import (
	"fmt"

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

type Adapter struct {
	cfg Config
	// contracts is the owned contract-ID set OwnsContract checks. It is
	// config-like ownership, not per-ledger scratch, so it does not affect the
	// DecodeState purity guarantee. Seeded empty; the relay projector feeds
	// discovered pools via RegisterContracts.
	contracts map[string]struct{}
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
			ShareType:    shareTypeForActivity(decoded.activityType),
			Direction:    decoded.direction,
			Timestamp:    evt.CloseTime,
			Metadata:     decoded.metadata,
		})
	}

	if err := a.computeState(input, out); err != nil {
		return nil, err
	}

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
	if !strkey.IsValidEd25519PublicKey(decoded.address) {
		return "invalid_activity_address"
	}
	return ""
}
