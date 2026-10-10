# api/AGENTS.md: rules for editing the memd protos

`api/memory/v1` is the contract (package `memory.v1`, Go package `memd/gen/memory/v1;memoryv1`). SPEC.md
"gRPC surface" is the source of truth. `docs/decisions/002-proto-file-split.md` explains which file each type
lives in.

## Rules

- **Comment everything.** Give every service, rpc, message, field, oneof, enum and enum value (nested ones and
  `*_UNSPECIFIED` too) a *leading* comment that carries real information: defaults, units, what unset means, and
  which RPC reads the field. `buf lint` runs STANDARD + COMMENTS, and trailing comments don't count. Each file
  starts with a comment above `syntax` describing the file.
- **Never renumber or reuse field numbers.** When you remove a field or enum value, add `reserved` for both its
  number and its name. Never change a field's type, label or name in place.
- **Never move a type between files.** `buf breaking` (category FILE) treats it as breaking.
- **New config goes in `BankConfig`.** Every new behaviour knob must be overridable per request (every
  request that does work takes `BankConfig overrides`) unless the spec says otherwise. Use `optional` for scalars
  whose unset value must differ from zero. Document the default in the field comment.
- **Keep the naming rules.** Every RPC gets its own `<Rpc>Request` / `<Rpc>Response`. Every enum value is
  prefixed with its enum name, and value 0 is `*_UNSPECIFIED`. List RPCs page with `page_size` + `page_token` /
  `next_page_token`. Patch RPCs take a `google.protobuf.FieldMask update_mask`.
- **Keep lines to 120 columns or fewer** and the files `buf format` clean. If `buf format` joins a line that ends up
  too long, keep the break with a trailing comment (see `observation.proto`).
- **Keep the import graph a DAG.** A file may import only from lower layers:

  | Layer | Files |
  |---|---|
  | 0 | `models.proto`, `events.proto` (well-known types only) |
  | 1 | `common.proto` (models) |
  | 2 | `config.proto` (common, models) |
  | 3 | `operation.proto` (common, config, events), `tenant.proto`, `memory.proto`, `memories.proto` |
  | 4 | `mental_model.proto`, `document.proto`, `observation.proto` (may import operation) |
  | 5 | `bank.proto` (may import mental_model, operation) |

  Put a **new** type that several files need in the lowest file all of them already import, usually
  `common.proto`. Never import upward or sideways. **Existing** types never move between files (see above); if a
  new dependency would create a cycle, raise a CCR instead of moving types.

## Workflow

- After any change, run `make gen` (`buf generate` into `gen/memory/v1`) and commit `gen/` in the same change.
  Never hand-edit `gen/`.
- Before you finish, run `buf format -d --exit-code`, `buf lint` and `buf breaking --against '.git#branch=main'`.
  CI runs `buf breaking` against main.
- Contract changes after WP-00 need a CCR in `docs/ccr/` that names the spec lines affected, and the spec must be
  updated with them. Don't change the wire contract silently.
