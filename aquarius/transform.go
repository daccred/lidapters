package aquarius

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/daccred/lidapters/bindings"
	"github.com/daccred/lidapters/blend/contracts"
	"github.com/stellar/go-stellar-sdk/xdr"
)

func stableID(parts ...any) string {
	var b strings.Builder
	for _, p := range parts {
		if b.Len() > 0 {
			b.WriteByte('|')
		}
		b.WriteString(fmt.Sprint(p))
	}
	s := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(s[:])
}

func (a *Adapter) Transform(in bindings.TransformInput) (*bindings.TransformOutput, error) {
	out := &bindings.TransformOutput{LedgerSeq: in.LedgerSeq}
	if in.State == nil {
		return out, nil
	}
	pools := map[string]bindings.AMMPoolState{}
	for _, p := range in.State.AMMPools {
		if p.Protocol == a.cfg.Protocol {
			pools[p.ContractID] = p
			out.AMMPools = append(out.AMMPools, bindings.AMMPool{Protocol: a.cfg.Protocol, RouterContract: p.RouterContract, PoolHash: p.PoolHash, ContractID: p.ContractID, PoolType: p.PoolType, WasmHash: p.WasmHash, Tokens: p.Tokens, TotalSharesRaw: p.TotalSharesRaw, FeeFractionRaw: p.FeeFractionRaw, ProtocolFeeFractionRaw: p.ProtocolFeeFractionRaw, AmplificationRaw: p.AmplificationRaw, TickSpacing: p.TickSpacing, SqrtPriceX96: p.SqrtPriceX96, CurrentTick: p.CurrentTick, ActiveLiquidityRaw: p.ActiveLiquidityRaw, LedgerSeq: in.LedgerSeq, Timestamp: in.CloseTime})
		}
	}
	for _, pos := range in.State.AMMPositions {
		pool, ok := pools[pos.PoolContractID]
		if !ok {
			// A position whose pool never folded (bounded replay without the
			// pool's instance write or seed) cannot be decomposed — but it
			// must not vanish silently. Quarantine, don't drop.
			out.Quarantine = append(out.Quarantine, bindings.QuarantineEvent{ID: stableID(a.ID(), pos.PoolContractID, pos.Address, in.LedgerSeq), AdapterID: a.ID(), LedgerSeq: in.LedgerSeq, ContractID: pos.PoolContractID, Reason: "aquarius_position_unknown_pool"})
			continue
		}
		group := stableID(a.cfg.Protocol, pos.Address, pos.PoolContractID, pos.TickLower, pos.TickUpper)
		if pool.PoolType == "concentrated" {
			appendRangeComponents(out, group, pos, pool, in)
		} else if pos.SharesRaw != "" && pos.SharesRaw != "0" {
			for _, t := range pool.Tokens {
				amount, e := proRata(pos.SharesRaw, t.ReserveRaw, pool.TotalSharesRaw)
				if e != nil {
					out.Quarantine = append(out.Quarantine, bindings.QuarantineEvent{ID: stableID(group, t.AssetID, in.LedgerSeq), AdapterID: a.ID(), LedgerSeq: in.LedgerSeq, ContractID: pool.ContractID, Reason: "invalid_lp_share_state"})
					continue
				}
				out.AMMComponents = append(out.AMMComponents, component(group, pos, a.cfg.Protocol, t.AssetID, "lp_principal", amount, pos.SharesRaw, nil, nil, in, false))
			}
		} else if pos.SharesRaw == "0" && pos.HadShares {
			// Closed classic LP position: write explicit zero tombstones so the
			// latest-per-id current view stops surfacing the pre-close rows.
			// HadShares distinguishes a real close from a never-held position.
			for _, t := range pool.Tokens {
				out.AMMComponents = append(out.AMMComponents, component(group, pos, a.cfg.Protocol, t.AssetID, "lp_principal", "0", "0", nil, nil, in, true))
			}
		}
		if pending := pendingReward(pool, pos, in.CloseTime); pending != "" && pending != "0" {
			out.AMMRewards = append(out.AMMRewards, bindings.AMMReward{ID: stableID(group, "aqua", pos.RewardTokenID), PositionGroupID: group, Address: pos.Address, Protocol: a.cfg.Protocol, PoolContractID: pos.PoolContractID, RewardTokenID: pos.RewardTokenID, RewardKind: "aqua", AmountRaw: pending, LedgerSeq: in.LedgerSeq, Timestamp: in.CloseTime, Metadata: map[string]string{"price_unavailable": "true"}})
		}
	}
	txUsers := map[string]string{}
	for _, evt := range in.Events {
		if u := eventUser(evt); u != "" {
			txUsers[evt.TxHash] = u
		}
	}
	for _, evt := range in.Events {
		acts, qs := a.decodeActivities(evt, txUsers[evt.TxHash])
		out.Activities = append(out.Activities, acts...)
		out.Quarantine = append(out.Quarantine, qs...)
	}
	sort.Slice(out.AMMPools, func(i, j int) bool { return out.AMMPools[i].ContractID < out.AMMPools[j].ContractID })
	sort.Slice(out.AMMComponents, func(i, j int) bool { return out.AMMComponents[i].ID < out.AMMComponents[j].ID })
	return out, nil
}

func component(group string, p bindings.AMMPositionState, protocol, asset, kind, amount, shares string, lower, upper *int32, in bindings.TransformInput, closed bool) bindings.AMMPositionComponent {
	metadata := map[string]string{"price_unavailable": "true", "apr_partial": "true"}
	if closed {
		metadata["closed"] = "true"
	}
	return bindings.AMMPositionComponent{ID: stableID(group, kind, asset), PositionGroupID: group, Address: p.Address, Protocol: protocol, PoolContractID: p.PoolContractID, ComponentKind: kind, AssetID: asset, AmountRaw: amount, ShareAmountRaw: shares, TickLower: lower, TickUpper: upper, LedgerSeq: in.LedgerSeq, Timestamp: in.CloseTime, Metadata: metadata}
}
func appendRangeComponents(out *bindings.TransformOutput, group string, p bindings.AMMPositionState, pool bindings.AMMPoolState, in bindings.TransformInput) {
	if len(pool.Tokens) < 2 {
		return
	}
	lo, hi := p.TickLower, p.TickUpper
	if p.LiquidityRaw == "0" && p.HadShares {
		// Closed range position (liquidity written or removed to zero):
		// explicit zero tombstones, mirroring the classic-LP close path.
		// HadShares distinguishes a real close from a never-held range;
		// absent liquidity ("") stays silent — absent is not zero.
		for i := 0; i < 2; i++ {
			out.AMMComponents = append(out.AMMComponents, component(group, p, pool.Protocol, pool.Tokens[i].AssetID, "range_principal", "0", "0", &lo, &hi, in, true))
		}
		return
	}
	if p.Principal0Raw == "" && p.Principal1Raw == "" && p.LiquidityRaw != "" && pool.SqrtPriceX96 != "" && p.SqrtPriceLowerX96 != "" && p.SqrtPriceUpperX96 != "" {
		if x, y, err := rangePrincipal(p.LiquidityRaw, pool.SqrtPriceX96, p.SqrtPriceLowerX96, p.SqrtPriceUpperX96); err == nil {
			p.Principal0Raw, p.Principal1Raw = x, y
		}
	}
	for i, x := range []string{p.Principal0Raw, p.Principal1Raw} {
		if x != "" && x != "0" {
			out.AMMComponents = append(out.AMMComponents, component(group, p, pool.Protocol, pool.Tokens[i].AssetID, "range_principal", x, p.LiquidityRaw, &lo, &hi, in, false))
		}
	}
	for i, x := range []string{p.PendingFee0Raw, p.PendingFee1Raw} {
		if x != "" && x != "0" {
			out.AMMComponents = append(out.AMMComponents, component(group, p, pool.Protocol, pool.Tokens[i].AssetID, "unclaimed_fee", x, "", &lo, &hi, in, false))
		}
	}
}

func eventUser(evt bindings.RawEventEnvelope) string {
	var ce xdr.ContractEvent
	if xdr.SafeUnmarshal(evt.RawEvent, &ce) != nil {
		return ""
	}
	v, ok := ce.Body.GetV0()
	if !ok {
		return ""
	}
	for _, t := range v.Topics {
		if x := addr(t); strings.HasPrefix(x, "G") {
			return x
		}
	}
	return ""
}

func (a *Adapter) decodeActivities(evt bindings.RawEventEnvelope, fallbackUser string) ([]bindings.Activity, []bindings.QuarantineEvent) {
	var ce xdr.ContractEvent
	if xdr.SafeUnmarshal(evt.RawEvent, &ce) != nil {
		return nil, nil
	}
	v, ok := ce.Body.GetV0()
	if !ok || len(v.Topics) == 0 {
		return nil, nil
	}
	name := strings.ToLower(symbolOrFirst(v.Topics[0]))
	typ := contracts.ActivityType("")
	switch name {
	case "deposit", "deposit_liquidity":
		typ = "add_liquidity"
	case "withdraw", "withdraw_liquidity":
		typ = "remove_liquidity"
	case "trade", "swap":
		typ = "swap"
	case "claim", "claim_reward":
		typ = contracts.ActivityTypeClaimRewards
	case "claim_fees":
		typ = "claim_fees"
	case "position_update":
		typ = "range_liquidity_change"
	default:
		return nil, nil
	}
	addresses := []string{}
	for _, t := range v.Topics {
		if x := addr(t); x != "" {
			addresses = append(addresses, x)
		}
	}
	wallet := ""
	for _, x := range addresses {
		if strings.HasPrefix(x, "G") {
			wallet = x
			break
		}
	}
	if wallet == "" {
		wallet = fallbackUser
	}
	if wallet == "" && name != "trade" {
		return nil, []bindings.QuarantineEvent{{ID: stableID(a.ID(), evt.LedgerSeq, evt.TxHash, evt.EventIndex), AdapterID: a.ID(), LedgerSeq: evt.LedgerSeq, TxHash: evt.TxHash, EventIndex: evt.EventIndex, ContractID: evt.ContractID, Reason: "aquarius_event_missing_user", RawEvent: evt.RawEvent}}
	}
	asset := ""
	for _, x := range addresses {
		if strings.HasPrefix(x, "C") && x != evt.ContractID {
			asset = x
			break
		}
	}
	return []bindings.Activity{{ID: stableID(a.cfg.Protocol, evt.LedgerSeq, evt.TxHash, evt.EventIndex, name), LedgerSeq: evt.LedgerSeq, TxHash: evt.TxHash, EventIndex: evt.EventIndex, ContractID: evt.ContractID, Address: wallet, Protocol: a.cfg.Protocol, ActivityType: typ, AssetID: asset, AmountRaw: firstUint(v.Data), Timestamp: evt.CloseTime, Metadata: map[string]string{"aquarius_event": name}}}, nil
}
