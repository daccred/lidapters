package blend

import _ "embed"

//go:embed testdata/oracle_stored_layout.json
var oracleStoredLayout []byte

// OracleStoredLayoutFixture returns the raw v2 oracle stored-layout fixture
// (the on-ledger oracle storage snapshot used as ground truth for fold tests).
//
// The bytes are embedded into the module at build time, so downstream consumers
// can read this fixture through the pinned module rather than reaching into a
// sibling checkout by filesystem path. Callers must not mutate the returned slice.
func OracleStoredLayoutFixture() []byte { return oracleStoredLayout }
