# 604 — FieldMask patches

## Context
UpdateTenantConfig, UpdateBankConfig and UpdateMentalModel take a `google.protobuf.FieldMask`. The proto
says "a path set to an empty value clears it (this is how a list is cleared)". Decision 010 says a
non-nil empty slice in a stored BankConfig is an explicit clear, which shadows inherited values. The spec
doesn't say what an absent mask means.

## Decision
- **Path validation.** Paths are proto field names, validated with `fieldmaskpb.IsValid` against the
  message type: only singular message fields can be traversed, and lists and maps must be the last
  segment. They are then normalized, so a parent path covers its children. An invalid path is
  INVALID_ARGUMENT.
  - `id` can't be patched on a mental model.
- **Where paths are applied.** Paths are applied to the **domain** struct, located through its JSON tags,
  which are the proto field names (decision 010). They are not applied to a proto copy of the current
  value. That way the stored current value never round-trips through proto, and its explicit clears
  survive patches of other fields.
- **What a masked path does.** It takes the request's value:
  - an unset value clears the field, so it inherits again;
  - a list or map with no elements becomes a non-nil empty one, an explicit clear.
- **Absent or empty mask.** It means every top-level field the request populates (AIP-134 style). Such a
  request can't clear anything.
- **ResetBankConfig.** `paths` reset the named fields to unset; no paths resets the whole bank override
  to the zero config.
- **Purity.** Patches are pure functions (`ConfigPatch`, `SpecPatch`). Pointer structs along a path are
  copied before they are written, so the caller's current value is never mutated, and the implementation
  can run a patch inside its transaction or retry it.

## Known gap (CCR candidate, not fixed here)
BankConfig's JSON tags use `omitempty`. A persisted explicit clear (a non-nil empty list) therefore
reloads from jsonb as nil, which means inherit. Fixing it needs a domain change (a frozen path), or an
adapter-side encoding in WP-01.

## Alternatives
- **Applying the mask on proto messages and converting back.** Rejected: it loses the stored explicit
  clears.
- **Rejecting an empty mask.** Rejected: it's less convenient, and AIP-134 allows the populated-fields
  reading.

## Spec lines involved
gRPC surface (BankConfig comment: "Patch RPCs take a FieldMask, which is also how a list is cleared"),
`UpdateBankConfigRequest.update_mask` and `ResetBankConfigRequest.paths` comments, decision 010.
