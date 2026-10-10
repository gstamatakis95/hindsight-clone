# 402 — Config resolver, FieldMask semantics and server defaults

## Context
The spec lists `NewResolver(r port.Reader, m StrategyMatcher, server domain.BankConfig)`. But
`port.Reader` has no bank or tenant config reads; those live on `port.BankRepo`, which is reachable only
through `Tx.Banks()`. The spec also leaves open several things this package needs:
- what an empty FieldMask means;
- how a masked-but-unset field differs from a reset;
- what Effective does for unknown or tombstoned banks;
- where server defaults come from.

## Decision
- **Resolver takes `port.Store`.** It reads the tenant config and the bank in one short read-only
  `InTx`, with no model call inside it.
  - Callers must not call it from inside an open transaction.
  - The `StrategyMatcher` is consumer-declared in `config` (`Match` only), so `consolidate.StrategyMatcher`
    satisfies it and config does not import an engine.
- **Unknown or tombstoned bank:** `Effective` and `ForScope` return `domain.ErrNotFound`, which is the
  read rule.
  - Write paths `Ensure` the bank first. That creates the bank or reports `ErrBankDeleting`
    (FAILED_PRECONDITION).
- **ForScope** takes the strategy list from the merged config with the request level included, so a
  Consolidate call can bring its own strategies. The matched strategy becomes a level between bank and
  request via `config.StrategyLevel`.
- **Merge** starts from a zero config and deep-copies everything it takes. A result never aliases a level,
  so the server defaults the resolver holds can't be changed through a result.
  - Nested messages merge field by field.
  - `models` merges per slot, and the whole binding is replaced (ruling R4).
  - Other lists and maps are replaced.
- **FieldMask, `config.ApplyMask(base, patch, paths)`:**
  - A masked path takes patch's value. A masked scalar or message that is unset in patch becomes unset
    (inherit).
  - A masked list or map that is empty in patch becomes a non-nil empty value. That is an explicit clear
    of the inherited list (decision 010).
  - An empty mask merges patch's set fields over base (the usual proto convention).
  - Nested messages left with no set field are pruned to nil.
  - Unknown paths are INVALID_ARGUMENT.
- **Reset, `config.ResetPaths(cfg, paths)`:** removes the overrides at those paths, so they inherit again.
  No paths resets everything (ResetBankConfig).
- **ServerDefaults(env)** fills every default stated in the spec's BankConfig comments:
  - dedup 0.97 and batch 8 / 256 / 100;
  - disposition 3/3/3;
  - temporal link cap 20;
  - budgets 100 / 300 / 1000, clamp 20..2000, rerank top 300, RRF k 60, alphas 0.2 / 0.2 / 0.1;
  - reflect 10 iterations, 5000 search_observations tokens, entities on, 4096 answer tokens;
  - mental models: debounce 30, no interval floor, history 50, agentic, full;
  - observation scopes: combined, 15 combinations;
  - extraction mode concise; auto consolidation, observations and free-form entities on.

  Knobs whose default the spec leaves open stay unset; the engine that reads a knob owns its default.

  Models come from the environment:
  - `MEMD_MODEL` binds every LLM slot.
  - `MEMD_MODEL_<SLOT>` (for example `MEMD_MODEL_REFLECT` or `MEMD_MODEL_RERANK`) binds one slot.
  - RERANK is bound only by `MEMD_MODEL_RERANK`.
  - An unbound slot fails at `ModelRouter.Resolve` with INVALID_ARGUMENT.

  The binary's own env/flag config (WP-06/WP-14) decides whether a missing model fails at startup.

## Alternatives
- **Add config reads to `port.Reader`.** That needs a CCR, for no benefit over a short read tx.
- **Treat an unknown bank as an empty bank level.** Rejected: reads of unknown banks must be NOT_FOUND,
  and Recall/Reflect would get that for free from the resolver.
- **JSON round-trip for deep copies.** Rejected: `omitempty` loses explicit clears (CCR WP-04-1).

## Spec lines involved
Ports (ConfigResolver, BankRepo), Function signatures (internal/config), BankConfig comments ("Patch RPCs
take a FieldMask, which is also how a list is cleared"), Behavioural rules (unknown bank NOT_FOUND,
tombstoned bank), Process model ("Config: env + flags only").
