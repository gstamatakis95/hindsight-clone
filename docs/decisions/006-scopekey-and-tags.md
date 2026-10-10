# 006 — ScopeKey encoding and tag normalization

## Context
The Ports block says `ScopeKey.String()` is "a length-prefixed encoding ("3:foo,5:bar,b"), so no tag
content can make two scopes collide". It also says tags are "normalized by domain.NormalizeTags: NFC,
trimmed, lowercased, deduplicated, sorted". The example is ambiguous: it could encode the bank too.

## Decision
- **Encoding.** `String()` encodes the tags only. Each tag is written as `<byte length>:<tag>`, and the
  tags are joined by `,`. The spec example is the tag list `["foo", "bar,b"]`: the comma inside
  `bar,b` is covered by the length prefix. The shared scope (no tags) encodes as `""`.
- **Bank is not part of it.** Storage keys scopes by (tenant_id, bank_id, scope_key), so repeating the
  bank inside the string would add nothing.
- **Decoding.** `domain.DecodeScopeTags` reverses the encoding and rejects malformed input with
  `ErrInvalidArgument`.
- **Where it lives.** Encoding and normalization are in `internal/domain` (`scope.go`, `tags.go`).
  `port.ScopeKey` is an alias.
- **Normalization.** Each tag is trimmed with `strings.TrimSpace` and lowercased with `strings.ToLower`.
  Empty tags are dropped, the rest are sorted and deduplicated, and the result is never nil.
- **NFC is pending.** NFC needs `golang.org/x/text/unicode/norm`, which is not an approved dependency,
  so NFC is requested in CCR `docs/ccr/WP-00-1.md` and not applied yet. Until it lands, a precomposed
  "é" and a decomposed "e" + combining accent are different tags.
- **Matching semantics.** `domain.MatchTags`, `MatchGroup` and `MatchFilter` follow Hindsight's
  `search/tags.py`:
  - any / all admit untagged units;
  - the `_strict` variants exclude them;
  - exact is set equality, and an empty exact filter selects only untagged units;
  - an empty filter means no filter in every mode but exact;
  - tag-leaf mode defaults to any_strict;
  - several top-level groups are AND-ed.

## Alternatives
- Encode `tenant/bank` into the key. Rejected: redundant with the columns, and it would make the key
  differ between banks for the same scope.
- Hand-roll NFC composition tables. Rejected: error-prone; the dependency is the right fix.

## Spec lines involved
Ports `ScopeKey`; proto `TagsMatch`, `TagLeaf` ("default TAGS_MATCH_ANY_STRICT"); Hindsight
`hindsight_api/engine/search/tags.py`.
