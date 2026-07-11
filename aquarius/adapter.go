package aquarius

import (
	"fmt"
	"sort"
	"strings"

	"github.com/daccred/lidapters/bindings"
)

type Config struct {
	AdapterID        string
	Protocol         string
	Routers          map[string]struct{}
	PoolWasmHashes   map[string]string // hash -> pool type
	AllowUnknownWasm bool
}

func DefaultConfig() Config { return Config{AdapterID: "aquarius", Protocol: "aquarius"} }

type Adapter struct {
	cfg       Config
	contracts map[string]struct{}
	assets    map[string]struct{}
}

var _ bindings.ProtocolAdapter = (*Adapter)(nil)
var _ bindings.StateReporter = (*Adapter)(nil)
var _ bindings.AssetRegistrar = (*Adapter)(nil)

// New preserves the original scaffold constructor for downstream callers.
func New() *Adapter { a, _ := NewWithConfig(Config{}); return a }

func NewWithConfig(config Config) (*Adapter, error) {
	cfg := DefaultConfig()
	{
		c := config
		if c.AdapterID != "" {
			cfg.AdapterID = c.AdapterID
		}
		if c.Protocol != "" {
			cfg.Protocol = c.Protocol
		}
		cfg.AllowUnknownWasm = c.AllowUnknownWasm
		cfg.Routers = copySet(c.Routers)
		cfg.PoolWasmHashes = map[string]string{}
		for k, v := range c.PoolWasmHashes {
			cfg.PoolWasmHashes[strings.ToLower(k)] = v
		}
	}
	if cfg.AdapterID == "" || cfg.Protocol == "" {
		return nil, fmt.Errorf("aquarius: adapter id and protocol are required")
	}
	a := &Adapter{cfg: cfg, contracts: map[string]struct{}{}, assets: map[string]struct{}{}}
	for id := range cfg.Routers {
		a.contracts[id] = struct{}{}
	}
	return a, nil
}

func copySet(in map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	for k := range in {
		if strings.TrimSpace(k) != "" {
			out[k] = struct{}{}
		}
	}
	return out
}
func (a *Adapter) ID() string       { return a.cfg.AdapterID }
func (a *Adapter) Protocol() string { return a.cfg.Protocol }
func (a *Adapter) OwnsContract(id string) bool {
	_, ok := a.contracts[id]
	if ok {
		return true
	}
	_, ok = a.assets[id]
	return ok
}
func (a *Adapter) RegisterContracts(ids ...string) {
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			a.contracts[id] = struct{}{}
		}
	}
}
func (a *Adapter) RegisterAssetContracts(ids ...string) {
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			a.assets[id] = struct{}{}
		}
	}
}
func (a *Adapter) StateAssetContracts(s *bindings.LedgerState) []string {
	set := map[string]struct{}{}
	if s != nil {
		for _, p := range s.AMMPools {
			if p.Protocol == a.cfg.Protocol {
				for _, t := range p.Tokens {
					if t.AssetID != "" {
						set[t.AssetID] = struct{}{}
					}
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
func (a *Adapter) StateStats(s *bindings.LedgerState) bindings.StateStats {
	var x bindings.StateStats
	if s == nil {
		return x
	}
	for _, p := range s.AMMPools {
		if p.Protocol == a.cfg.Protocol {
			x.Pools++
		}
	}
	seen := map[string]struct{}{}
	for _, p := range s.AMMPositions {
		seen[p.Address] = struct{}{}
	}
	x.Users = len(seen)
	return x
}
