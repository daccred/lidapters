# Lidapters

Public adapter module for Lightgate protocol adapters.

This module is the Blend protocol adapter. The adapter lives at the module root (`package lidapters`) and the shared adapter contracts live in `contracts` (`package contracts`). The adapter must not import relay, ingest, database, queue, or runtime packages.

## Imports

```go
import (
	"github.com/daccred/lidapters"
	"github.com/daccred/lidapters/contracts"
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
