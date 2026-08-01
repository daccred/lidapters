# FxDAO fixture registry

Raw on-chain ScVal bytes, sliced from Hubble
`crypto-stellar.crypto_stellar.contract_data.contract_data_xdr`
(ContractDataEntry XDR; month-partition-scoped query over 2026-05, pulled
2026-08-01). Nothing is re-encoded. The same capture set anchors the Rust
engine's FxDAO fixtures; both engines decode the shared raw bytes
independently. Every expectation below was re-derived with an independent
Python-stdlib XDR walk (struct.unpack big-endian, u128 = `(hi << 64) | lo`
from two u64 parts, strkey re-encoded from the 32-byte payload +
CRC16-XMODEM) — never read back from the package under test.

Contract: vaults `CCUN4RXU5VNDHSF4S4RKV4ZJYMX2YWKOH6L4AKEKVNVDQ7HY5QIAO4UB`
([stellar.expert](https://stellar.expert/explorer/public/contract/CCUN4RXU5VNDHSF4S4RKV4ZJYMX2YWKOH6L4AKEKVNVDQ7HY5QIAO4UB)).
All entries are `durability = persistent` except the contract instance.

## Ledger 62,448,349 (2026-05-06, close 1778093393) — the golden write ledger

The last rich vault ledger: two vault updates, one VaultIndex update, and one
**LEDGER_ENTRY_RESTORED** change (the raw XDR restore variant), all in one
ledger.

- `pubnet-L062448349-fxdao-vault-gdvz-usd-{key,val}.xdr` — the GOLDEN vault.
  - Key (96 bytes): `Vec[Symbol("Vault"), Vec[Address(GDVZ4XYY…), Symbol("USD")]]`
    — the enum-variant-with-TUPLE-payload encoding (`Vault((Address, Symbol))`,
    vaults.rs:62 in FxDAO-SC @ b73d8b6): the payload tuple is itself an ScVec,
    so the key nests a Vec INSIDE the variant Vec. A flat
    `Vec[Symbol, Address, Symbol]` decode matches nothing.
  - Val (404 bytes), ScMap in symbol order: account = GDVZ4XYYKURF6OE7AQQPG
    UYU744ULGIJAKZDEETH2YMDABFZQCS3PENZ; denomination = USD;
    index = **19_182_692_307** (u128); next_key = **Some**(VaultKey{ account
    GBWTPGKB…, denomination USD, index 19_837_263_906 }); total_collateral =
    **24_937_500_000**; total_debt = **1_300_000_000**.
- `pubnet-L062448349-fxdao-vault-gbwt-usd-{key,val}.xdr` — the list tail.
  - Val (264 bytes): account = GBWTPGKBA6RIPW6UE7GRIGTCFMI326YC25MCCUV7O72QA4
    JYBICW6LWJ; denomination = USD; index = **19_837_263_906**; next_key =
    **None** (`Vec[Symbol("None")]`); total_collateral = **1_269_584_890_000**;
    total_debt = **64_000_000_000**.
- `pubnet-L062448349-fxdao-vaultindex-gdvz-usd-restored-{key,val}.xdr` — the
  RESTORED entry (raw change variant in the source ledger).
  - Key (132 bytes): `Vec[Symbol("VaultIndex"), Map{denomination: USD, user:
    Address(GDVZ4XYY…)}]` (`VaultIndex(VaultIndexKey)`, vaults.rs:48-53,66).
  - Val (20 bytes): u128 **19_182_692_307** — equals the GDVZ vault's `index`.
  - The vault's own entry was UPDATED in the same ledger while its index
    entry came back from archival: 28-day TTLs (vaults.rs:3-5) make
    restore-then-write the protocol's normal wake-up sequence.
- `pubnet-L062448349-fxdao-vaults-instance-ccun-{key,val}.xdr` — the contract
  instance (key = the 4-byte `ScVal::LedgerKeyContractInstance`).
  - Executable wasm `f3f08b4003bfc22668a75e8717d7c2e364c59ac2ac5b236d02daccbf
    684eea1a` (superseded by 0245bac3… before the final closures — decode is
    layout-driven, not wasm-gated).
  - `VaultsInfo(USD)`: total_vaults = **5** (u64), total_debt =
    **239_040_000_000**, total_col = **2_704_671_870_000**, lowest_key =
    Some(VaultKey{ GCAIQZGU…, USD, index **7_843_392_683** }), min_col_rate =
    **11_000_000** (1.10 × SCALAR_7), min_debt_creation = **1_000_000_000**,
    opening_col_rate = **11_500_000** (1.15 × SCALAR_7).
  - `VaultsInfo(EUR)` / `VaultsInfo(GBP)`: lowest_key None (no vaults).
  - `CoreState` and `Currency(USD|EUR|GBP)` entries are present and
    recognized-not-carried by this package.

## Ledger 62,473,181 (2026-05-07) — mid-window closure ledger

- `pubnet-L062473181-fxdao-vault-gdvz-usd-val.xdr` — GDVZ rewritten after
  GBWT's vault closed (deleted in the same ledger): next_key back to
  **None**, total_collateral **24_937_500_000**, total_debt **1_300_000_000**
  (unchanged amounts; the list rewires around the closure).

## Ledger 62,645,363 (2026-05-19T22:14:05Z) — the terminal ledger

- `pubnet-L062645363-fxdao-vaults-instance-ccun-final-val.xdr` — the LAST
  state-mutating write of the protocol. Executable 0245bac3…;
  `VaultsInfo(USD)`: total_vaults = **0**, total_col = **0**, total_debt =
  **175_040_000_000** (nonzero residual with zero vaults — recorded as
  written; absent-not-zero applies to ABSENT keys, not to stored values),
  lowest_key = None.

## Checkpoint compatibility fixture

- `ledgerstate-checkpoint-pre-vault-extension.json` — a `bindings.LedgerState`
  serialized by the PRE-vault-extension struct (repository state
  `882bc42bb69b0132568d56a1671d343d2b01dc4a`, before the `Vaults`/`VaultsInfo`
  slices existed), with the Blend and AMM slice families populated. The
  compat test decodes it with the extended struct and requires every
  pre-change field intact and the new families absent — the proof that the
  vault extension is additive and existing checkpoints remain JSON-decodable.

## Derivation notes

u128 fields decode from `SCV_U128 (type 9): hi:u64 then lo:u64`, big-endian.
Example walk (GDVZ val, `total_collateral`): bytes
`00 00 00 09 | 00 00 00 00 00 00 00 00 | 00 00 00 05 CE 4D 74 60` →
hi = 0, lo = 0x5CE4D7460 = 24_937_500_000. `OptionalVaultKey` encodes as the
`#[contracttype]` enum: `Vec[Symbol("None")]` or `Vec[Symbol("Some"),
Map{account, denomination, index}]`. The `Vault` value ScMap arrives in
symbol-sorted order: account, denomination, index, next_key,
total_collateral, total_debt.
