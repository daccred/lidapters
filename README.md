# Lidapters

> **This repository is frozen.** Development of the adapter library continues
> inside [relay.lightgate.xyz](https://github.com/daccred/relay.lightgate.xyz)
> under `internal/lidapters/`, where the full git history of this repository is
> preserved via subtree merge. The final module release is
> [v0.12.1](https://github.com/daccred/lidapters/releases/tag/v0.12.1); no
> further tags will be published. History, releases, and issues remain public
> record. Report adapter bugs on the relay repository.

Public adapter module for Lightgate protocol adapters.

The module is nested per protocol. Shared, protocol-agnostic seam types live in
`bindings`; each protocol owns its own package tree. The adapters must not
import relay, ingest, database, queue, or runtime packages.

```
lidapters/
├── deployments.toml    canonical cross-protocol deploy-ledger pins
├── deployments.go      pin loader (root package lidapters)
├── bindings/           ProtocolAdapter seam, envelopes, gold rows, config IoC
├── blend/              Blend adapter (Transform, DecodeState, config state)
│   ├── contracts/      Blend-domain types: pool/reserve/oracle state, enums
│   └── discovery/      event-based pool enumeration from raw close-meta
└── aquarius/           Aquarius adapter scaffold (not implemented yet)
```

## Imports

```go
import (
	"github.com/daccred/lidapters/bindings"
	"github.com/daccred/lidapters/blend"
	"github.com/daccred/lidapters/blend/contracts"
	"github.com/daccred/lidapters/blend/discovery"
)
```

The module follows semantic versioning. Consumers should pin a release such as:

```sh
go get github.com/daccred/lidapters@v0.4.0
```

## Commands

```sh
make test
make tidy
```
