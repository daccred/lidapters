# Lidapters

Public adapter module for Lightgate protocol adapters.

This module contains shared adapter contracts and protocol adapter packages. Blend lives under `adapters/blend`; future adapters such as Aquarius or SoroSwap should live under `adapters/<protocol>`. Adapter packages must not import relay, ingest, database, queue, or runtime packages.

## Imports

```go
import (
	blend "github.com/daccred/lidapters/adapters/blend"
	contractsv1 "github.com/daccred/lidapters/contracts/v1"
)
```

The module follows semantic versioning. Consumers should pin a release such as:

```sh
go get github.com/daccred/lidapters@v0.1.0
```

## Commands

```sh
make test
make tidy
```
