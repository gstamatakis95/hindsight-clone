# api/AGENTS.md: rules for editing the memd proto

`api/memory/v1/memory.proto` is the whole API in one file (package `memory.v1`, Go package
`memd/gen/memory/v1;memoryv1`). SPEC.md "gRPC surface" is the source of truth for behaviour.

## Layout

The file has six parts, in this order:

1. A header comment with the conventions every service shares (tenant header, unknown banks, the config
   hierarchy, async operations, events, naming).
2. All service blocks.
3. One section per service, in the same order as the service blocks, under a
   `// ======================= <Service> =======================` banner. Inside it, one
   `// ---- <Rpc> ----` subsection per RPC in RPC order, holding its request, response and helper messages, then
   a `// ---- Resources ----` subsection with the service's resource messages (Memory, Document, Operation, ...).
4. Shared types: messages used by more than one service and owned by none.
5. Configuration: `BankConfig` and its phase groups, the model bindings, then the per-RPC `*Overrides` messages.
6. Shared enums.

Where a new type goes:
- A resource message lives in its owning service's Resources, even when other services use it (Memory,
  Directive, MentalModelSpec, Operation).
- A helper message or enum lives in the subsection of the first RPC that uses it, even when later RPCs of the
  same service use it too (Detail sits under GetMentalModel).
- Shared types and shared enums hold only types that no service owns.

## Rules

- **Comment everything.** Give every service, rpc, message, field, oneof, enum and enum value (nested ones and
  `*_UNSPECIFIED` too) a *leading* comment that carries real information: defaults, units, what unset means, and
  which RPC reads the field. `buf lint` runs STANDARD + COMMENTS, and trailing comments don't count.
- **Field numbers.** Until the first release, fields may be renamed and renumbered. After it, never renumber or
  reuse a field number: when you remove a field or enum value, add `reserved` for both its number and its name,
  and never change a field's type, label or name in place.
- **New config goes in `BankConfig`,** in its phase group (`RetainConfig`, `ObservationConfig`, `RecallConfig`,
  `ReflectConfig`, `MentalModelConfig`, `ModelBindings`). Every request that does work takes its own overrides
  message (`RetainOverrides`, `RecallOverrides`, ...) holding only what that RPC may change; add the knob there
  too when the RPC should be able to set it, and map it in `internal/grpcapi/convert_overrides.go`. A model
  binding is atomic (one FieldMask path). Use `optional` for scalars whose unset value must differ from zero.
  Document the default in the field comment.
- **Keep the naming rules.** Every RPC gets its own `<Rpc>Request` / `<Rpc>Response`. Every enum value is
  prefixed with its enum name, and value 0 is `*_UNSPECIFIED`. List RPCs page with `page_size` + `page_token` /
  `next_page_token`. Patch RPCs take a `google.protobuf.FieldMask update_mask`.
- **Keep lines to 120 columns or fewer** and the file `buf format` clean. If `buf format` joins a line that ends
  up too long, keep the break with a trailing comment (see `PreviewConsolidationStrategies`).

## Workflow

- After any change, run `make gen` (`buf generate` into `gen/memory/v1`) and commit `gen/` in the same change.
  Never hand-edit `gen/`.
- Before you finish, run `buf format -d --exit-code` and `make lint` (which runs `buf lint`). Until the first
  release there is no `buf breaking` check; after it, `buf breaking` runs against the release tag.
- Update SPEC.md with any change to the wire contract.
