package lidapters

import (
	"encoding/json"
	"math/big"
	"regexp"
	"strings"

	"github.com/daccred/lidapters/contracts"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

var (
	addressRE = regexp.MustCompile(`[GC][A-Z2-7]{55}`)
)

type decodedEvent struct {
	isBlend      bool
	activityType contracts.ActivityType
	address      string
	assetID      string
	amountRaw    string
	shareRaw     string
	direction    string
	reason       string
	metadata     map[string]string
}

func decodeEvent(evt contracts.RawEventEnvelope) decodedEvent {
	out := decodedEvent{
		isBlend:  looksBlend(evt),
		metadata: map[string]string{"topic": evt.Topic},
	}
	for k, v := range evt.Metadata {
		out.metadata[k] = v
	}

	// Fixture and synthetic events may store JSON directly in raw_event.
	if parsed := decodeFixturePayload(evt.RawEvent); parsed.activityType != "" {
		mergeDecoded(&out, parsed)
		return out
	}

	// For ingest-produced events, topic is a JSON blob with human and XDR forms.
	if parsed := decodeTopicJSON(evt.Topic); parsed.activityType != "" {
		mergeDecoded(&out, parsed)
	}

	// Source of truth for chain events is raw Soroban ContractEvent XDR.
	if parsed := decodeContractEventXDR(evt.RawEvent); parsed.activityType != "" {
		mergeDecoded(&out, parsed)
	}

	if out.activityType == "" {
		if evt.Metadata["protocol_id"] == "blend" {
			out.reason = "unsupported_blend_v2_event"
		} else {
			out.reason = "unknown_blend_event_shape"
		}
	}
	if out.direction == "" {
		out.direction = directionForActivity(out.activityType)
	}
	if out.address == "" || out.assetID == "" {
		wallet, asset := extractAddresses(evt.Topic)
		if out.address == "" {
			out.address = wallet
		}
		if out.assetID == "" {
			out.assetID = asset
		}
	}
	return out
}

func decodeFixturePayload(raw []byte) decodedEvent {
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		return decodedEvent{}
	}
	evType, _ := v["type"].(string)
	if evType == "" {
		return decodedEvent{}
	}
	amount := jsonString(v["amount"])
	wallet := jsonString(v["wallet"])
	asset := jsonString(v["asset"])
	act := classifyEventName(evType)
	return decodedEvent{
		activityType: act,
		address:      wallet,
		assetID:      asset,
		amountRaw:    amount,
		direction:    directionForActivity(act),
		metadata:     map[string]string{"fixture_type": evType},
	}
}

func decodeTopicJSON(topic string) decodedEvent {
	var payload map[string]any
	if err := json.Unmarshal([]byte(topic), &payload); err != nil {
		return decodedEvent{}
	}
	rawTopics, ok := payload["topics"].([]any)
	if !ok || len(rawTopics) == 0 {
		return decodedEvent{}
	}
	eventName := strings.ToLower(jsonString(rawTopics[0]))
	act := classifyEventName(eventName)
	if act == "" {
		return decodedEvent{}
	}
	wallet := ""
	asset := ""
	for _, val := range rawTopics {
		s := jsonString(val)
		if wallet == "" && validAccountAddress(s) {
			wallet = s
		}
		if asset == "" && validContractAddress(s) {
			asset = s
		}
	}
	if wallet == "" || asset == "" {
		dataWallet, dataAsset := extractJSONAddresses(payload["data"])
		if wallet == "" {
			wallet = dataWallet
		}
		if asset == "" {
			asset = dataAsset
		}
	}
	amount := jsonString(payload["amount"])
	if amount == "" {
		amount = firstJSONNumeric(payload["data"])
	}
	if dataXDR := jsonString(payload["data_xdr"]); dataXDR != "" {
		if data, ok := decodeScValBase64(dataXDR); ok {
			dataWallet, dataAsset := collectScValAddresses(data)
			if wallet == "" {
				wallet = dataWallet
			}
			if asset == "" {
				asset = dataAsset
			}
			if amount == "" {
				amount = scValNumeric(data)
			}
		}
	}
	return decodedEvent{
		activityType: act,
		address:      wallet,
		assetID:      asset,
		amountRaw:    amount,
		direction:    directionForActivity(act),
	}
}

func decodeContractEventXDR(raw []byte) decodedEvent {
	var evt xdr.ContractEvent
	if err := xdr.SafeUnmarshal(raw, &evt); err != nil {
		return decodedEvent{}
	}
	v0, ok := evt.Body.GetV0()
	if !ok {
		return decodedEvent{}
	}
	eventName := ""
	wallet := ""
	asset := ""
	for _, topic := range v0.Topics {
		if eventName == "" {
			if symbol := scValSymbol(topic); symbol != "" {
				eventName = symbol
				continue
			}
		}
		if addr := scValAddress(topic); addr != "" {
			if wallet == "" && validAccountAddress(addr) {
				wallet = addr
				continue
			}
			if asset == "" && validContractAddress(addr) {
				asset = addr
			}
		}
	}
	dataWallet, dataAsset := collectScValAddresses(v0.Data)
	if wallet == "" {
		wallet = dataWallet
	}
	if asset == "" {
		asset = dataAsset
	}
	act := classifyEventName(eventName)
	if act == "" {
		return decodedEvent{}
	}
	return decodedEvent{
		activityType: act,
		address:      wallet,
		assetID:      asset,
		amountRaw:    scValNumeric(v0.Data),
		direction:    directionForActivity(act),
	}
}

func looksBlend(evt contracts.RawEventEnvelope) bool {
	if evt.Metadata["protocol_id"] == "blend" {
		return true
	}
	s := strings.ToLower(evt.ContractID + " " + evt.Topic)
	keys := []string{
		"blend", "pool", "backstop", "supply", "borrow", "repay", "withdraw", "liquid", "emission", "flash",
	}
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func classifyEventName(name string) contracts.ActivityType {
	s := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(s, "supply"), strings.Contains(s, "deposit"):
		return contracts.ActivityTypeDeposit
	case strings.Contains(s, "withdraw"):
		return contracts.ActivityTypeWithdraw
	case strings.Contains(s, "borrow"):
		return contracts.ActivityTypeBorrow
	case strings.Contains(s, "repay"):
		return contracts.ActivityTypeRepay
	case strings.Contains(s, "liquid"):
		return contracts.ActivityTypeLiquidation
	case strings.Contains(s, "claim"):
		return contracts.ActivityTypeClaimRewards
	case strings.Contains(s, "flash"):
		return contracts.ActivityTypeFlashLoan
	case strings.Contains(s, "bad_debt"), strings.Contains(s, "baddebt"):
		return contracts.ActivityTypeBadDebt
	case strings.Contains(s, "status"), strings.Contains(s, "reserve_config"), strings.Contains(s, "set_reserve"):
		return contracts.ActivityTypeStatusChange
	default:
		return ""
	}
}

func directionForActivity(a contracts.ActivityType) string {
	switch a {
	case contracts.ActivityTypeDeposit, contracts.ActivityTypeRepay, contracts.ActivityTypeClaimRewards:
		return "in"
	case contracts.ActivityTypeWithdraw, contracts.ActivityTypeBorrow:
		return "out"
	default:
		return "neutral"
	}
}

// shareTypeForActivity classifies the position-share an activity moves, using
// the same vocabulary as PositionType (supply / liability). Deposits and
// withdrawals move supply shares; borrows and repays move liability. Activities
// whose share semantics are not determinable from the type alone (liquidation,
// claim_rewards, status changes, etc.) return "" so the store can COALESCE.
func shareTypeForActivity(a contracts.ActivityType) string {
	switch a {
	case contracts.ActivityTypeDeposit, contracts.ActivityTypeWithdraw:
		return string(contracts.PositionTypeSupply)
	case contracts.ActivityTypeBorrow, contracts.ActivityTypeRepay:
		return string(contracts.PositionTypeLiability)
	default:
		return ""
	}
}

func extractAddresses(s string) (wallet, asset string) {
	matches := addressRE.FindAllString(s, -1)
	for _, m := range matches {
		if wallet == "" && validAccountAddress(m) {
			wallet = m
			continue
		}
		if asset == "" && validContractAddress(m) {
			asset = m
		}
	}
	return wallet, asset
}

func extractJSONAddresses(v any) (wallet, asset string) {
	switch val := v.(type) {
	case string:
		return extractAddresses(val)
	case []any:
		for _, item := range val {
			w, a := extractJSONAddresses(item)
			if wallet == "" {
				wallet = w
			}
			if asset == "" {
				asset = a
			}
		}
	case map[string]any:
		for key, item := range val {
			if strings.HasSuffix(strings.ToLower(key), "_xdr") || strings.EqualFold(key, "raw_event") {
				continue
			}
			w, a := extractJSONAddresses(item)
			if wallet == "" {
				wallet = w
			}
			if asset == "" {
				asset = a
			}
		}
	}
	return wallet, asset
}

func firstJSONNumeric(v any) string {
	switch val := v.(type) {
	case float64, json.Number:
		return jsonString(val)
	case []any:
		for _, item := range val {
			if n := firstJSONNumeric(item); n != "" {
				return n
			}
		}
	case map[string]any:
		for _, key := range []string{"amount", "tokens", "shares", "share_amount"} {
			if n := firstJSONNumeric(val[key]); n != "" {
				return n
			}
		}
	}
	return ""
}

func decodeScValBase64(raw string) (xdr.ScVal, bool) {
	if raw == "" {
		return xdr.ScVal{}, false
	}
	var out xdr.ScVal
	if err := xdr.SafeUnmarshalBase64(raw, &out); err != nil {
		return xdr.ScVal{}, false
	}
	return out, true
}

func collectScValAddresses(val xdr.ScVal) (wallet, asset string) {
	if addr := scValAddress(val); addr != "" {
		if validAccountAddress(addr) {
			return addr, ""
		}
		if validContractAddress(addr) {
			return "", addr
		}
	}
	switch val.Type {
	case xdr.ScValTypeScvVec:
		if val.Vec != nil && *val.Vec != nil {
			for _, item := range **val.Vec {
				w, a := collectScValAddresses(item)
				if wallet == "" {
					wallet = w
				}
				if asset == "" {
					asset = a
				}
			}
		}
	case xdr.ScValTypeScvMap:
		if val.Map != nil && *val.Map != nil {
			for _, entry := range **val.Map {
				w, a := collectScValAddresses(entry.Val)
				if wallet == "" {
					wallet = w
				}
				if asset == "" {
					asset = a
				}
			}
		}
	}
	return wallet, asset
}

func validAccountAddress(address string) bool {
	return strkey.IsValidEd25519PublicKey(address)
}

func validContractAddress(address string) bool {
	return strkey.IsValidContractAddress(address)
}

func mergeDecoded(target *decodedEvent, src decodedEvent) {
	if src.activityType != "" {
		target.activityType = src.activityType
	}
	if src.address != "" {
		target.address = src.address
	}
	if src.assetID != "" {
		target.assetID = src.assetID
	}
	if src.amountRaw != "" {
		target.amountRaw = src.amountRaw
	}
	if src.shareRaw != "" {
		target.shareRaw = src.shareRaw
	}
	if src.direction != "" {
		target.direction = src.direction
	}
	for k, v := range src.metadata {
		target.metadata[k] = v
	}
}

func jsonString(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(big.NewFloat(val).Text('f', -1)), ".0"), ".")
	case json.Number:
		return val.String()
	default:
		return ""
	}
}

func scValSymbol(val xdr.ScVal) string {
	switch val.Type {
	case xdr.ScValTypeScvSymbol:
		if val.Sym != nil {
			return string(*val.Sym)
		}
	case xdr.ScValTypeScvString:
		if val.Str != nil {
			return string(*val.Str)
		}
	}
	return ""
}

func scValAddress(val xdr.ScVal) string {
	if val.Type != xdr.ScValTypeScvAddress || val.Address == nil {
		return ""
	}
	addr := *val.Address
	switch addr.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if addr.AccountId == nil {
			return ""
		}
		encoded, err := strkey.Encode(strkey.VersionByteAccountID, addr.AccountId.Ed25519[:])
		if err != nil {
			return ""
		}
		return encoded
	case xdr.ScAddressTypeScAddressTypeContract:
		if addr.ContractId == nil {
			return ""
		}
		encoded, err := strkey.Encode(strkey.VersionByteContract, addr.ContractId[:])
		if err != nil {
			return ""
		}
		return encoded
	default:
		return ""
	}
}

func scValNumeric(val xdr.ScVal) string {
	switch val.Type {
	case xdr.ScValTypeScvI32:
		if val.I32 != nil {
			return big.NewInt(int64(*val.I32)).String()
		}
	case xdr.ScValTypeScvU32:
		if val.U32 != nil {
			return new(big.Int).SetUint64(uint64(*val.U32)).String()
		}
	case xdr.ScValTypeScvI64:
		if val.I64 != nil {
			return big.NewInt(int64(*val.I64)).String()
		}
	case xdr.ScValTypeScvU64:
		if val.U64 != nil {
			return new(big.Int).SetUint64(uint64(*val.U64)).String()
		}
	case xdr.ScValTypeScvI128:
		if val.I128 != nil {
			return int128ToString(*val.I128)
		}
	case xdr.ScValTypeScvU128:
		if val.U128 != nil {
			return uint128ToString(*val.U128)
		}
	case xdr.ScValTypeScvVec:
		if val.Vec != nil && *val.Vec != nil {
			for _, item := range **val.Vec {
				if n := scValNumeric(item); n != "" {
					return n
				}
			}
		}
	case xdr.ScValTypeScvMap:
		if val.Map != nil && *val.Map != nil {
			for _, entry := range **val.Map {
				if n := scValNumeric(entry.Val); n != "" {
					return n
				}
			}
		}
	}
	return ""
}

func uint128ToString(val xdr.UInt128Parts) string {
	hi := new(big.Int).SetUint64(uint64(val.Hi))
	lo := new(big.Int).SetUint64(uint64(val.Lo))
	hi.Lsh(hi, 64)
	hi.Add(hi, lo)
	return hi.String()
}

func int128ToString(val xdr.Int128Parts) string {
	hi := new(big.Int).SetUint64(uint64(val.Hi))
	lo := new(big.Int).SetUint64(uint64(val.Lo))

	if uint64(val.Hi)&(uint64(1)<<63) != 0 {
		hi.Sub(hi, new(big.Int).Lsh(big.NewInt(1), 64))
	}
	hi.Lsh(hi, 64)
	hi.Add(hi, lo)
	return hi.String()
}
