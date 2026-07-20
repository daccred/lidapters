package discovery

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

type DiscoveredPool struct {
	PoolID, RouterID, TxHash string
	LedgerSeq                int64
}

func DiscoverPoolsFromLedgerCloseMeta(lcm xdr.LedgerCloseMeta, routers map[string]struct{}) ([]DiscoveredPool, error) {
	seen := map[string]DiscoveredPool{}
	for i := 0; i < lcm.CountTransactions(); i++ {
		h := lcm.TransactionHash(i)
		tx := hex.EncodeToString(h[:])
		for _, e := range events(lcm.TxApplyProcessing(i)) {
			p, ok := decode(e, routers)
			if !ok {
				continue
			}
			p.LedgerSeq = int64(lcm.LedgerSequence())
			p.TxHash = tx
			seen[p.PoolID] = p
		}
	}
	out := make([]DiscoveredPool, 0, len(seen))
	for _, p := range seen {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PoolID < out[j].PoolID })
	return out, nil
}
func DiscoverPoolsFromMeta(raw []byte, routers map[string]struct{}) ([]DiscoveredPool, error) {
	var l xdr.LedgerCloseMeta
	if err := xdr.SafeUnmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("aquarius discovery: %w", err)
	}
	return DiscoverPoolsFromLedgerCloseMeta(l, routers)
}
func events(m xdr.TransactionMeta) []xdr.ContractEvent {
	if m.V == 3 && m.V3 != nil && m.V3.SorobanMeta != nil {
		return m.V3.SorobanMeta.Events
	}
	if m.V == 4 && m.V4 != nil {
		var out []xdr.ContractEvent
		for _, op := range m.V4.Operations {
			out = append(out, op.Events...)
		}
		return out
	}
	return nil
}
func decode(e xdr.ContractEvent, routers map[string]struct{}) (DiscoveredPool, bool) {
	if e.Type != xdr.ContractEventTypeContract || e.ContractId == nil {
		return DiscoveredPool{}, false
	}
	router, err := strkey.Encode(strkey.VersionByteContract, e.ContractId[:])
	if err != nil {
		return DiscoveredPool{}, false
	}
	if _, ok := routers[router]; !ok {
		return DiscoveredPool{}, false
	}
	v, ok := e.Body.GetV0()
	if !ok || len(v.Topics) == 0 {
		return DiscoveredPool{}, false
	}
	name := strings.ToLower(symbol(v.Topics[0]))
	if name != "add_pool" && name != "pool_created" && name != "init_pool" && name != "deploy" {
		return DiscoveredPool{}, false
	}
	pool := findContract(v.Data, router)
	if pool == "" {
		for _, t := range v.Topics[1:] {
			if x := findContract(t, router); x != "" {
				pool = x
				break
			}
		}
	}
	return DiscoveredPool{PoolID: pool, RouterID: router}, pool != ""
}
func symbol(v xdr.ScVal) string {
	if s, ok := v.GetSym(); ok {
		return string(s)
	}
	return ""
}
func findContract(v xdr.ScVal, exclude string) string {
	if a, ok := v.GetAddress(); ok && a.Type == xdr.ScAddressTypeScAddressTypeContract {
		if s, e := strkey.Encode(strkey.VersionByteContract, a.ContractId[:]); e == nil && s != exclude {
			return s
		}
	}
	if x, ok := v.GetVec(); ok && x != nil {
		for _, i := range *x {
			if s := findContract(i, exclude); s != "" {
				return s
			}
		}
	}
	if m, ok := v.GetMap(); ok && m != nil {
		for _, i := range *m {
			if s := findContract(i.Val, exclude); s != "" {
				return s
			}
		}
	}
	return ""
}
