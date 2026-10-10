# 100 — Postgres store foundation: transactions, Seq, banks, errors

## Context

The port docs fix most of `Store`, `Tx.Seq` and `BankRepo`, but leave a few adapter-level choices open:
what else `Seq` stamps, what `SetConfig` does on an unknown bank, how malformed ids and driver errors map
to domain errors, and whether `InTx` retries on a deadlock.

## Decision

- **InTx** begins at READ COMMITTED (port obligation), commits when `fn` returns nil and rolls back on an
  error or panic (the panic is re-raised). A transaction that fails with a deadlock (40P01) or a
  serialization failure (40001) is rolled back and `fn` runs again in a fresh transaction, up to 3 attempts
  in all (WP-01 review follow-up). This is safe because `fn` never calls a model (port.Store), so a re-run
  only repeats short database work; callers must not keep side effects outside the database in `fn`. After
  the last attempt the error surfaces as `domain.ErrConflict`.
- **Seq** is one statement: `UPDATE banks SET change_seq = change_seq + 1, last_memory_write_at = now(),
  updated_at = now() WHERE (tenant, id) AND deleting_at IS NULL RETURNING change_seq`. The tx caches the
  value per bank, so the bank is bumped once per tx. The row lock is held to commit, so within a bank seq
  order is commit order (`TestAcceptSeqOrder`). No row matched: a tombstoned bank is `ErrBankDeleting`, an
  unknown one `ErrNotFound`. `last_memory_write_at` is stamped here because every memory-content change
  calls Seq (SPEC "Behavioural rules", change sequence) and `Bank.LastMemoryWriteAt` is "when memory
  content last changed".
- **Ensure** is `INSERT ... ON CONFLICT DO NOTHING` then a read, so it never takes the bank row lock and
  never waits on a writer holding Seq.
- **SetConfig** upserts: a write to an unknown bank creates it (BankRepo doc), a tombstoned bank is
  `ErrBankDeleting`. **MarkDeleting** on an unknown bank is `ErrNotFound`.
- **Errors** (`errors.go`): no rows and foreign-key violations are `ErrNotFound`; unique violations
  `ErrAlreadyExists`; check / not-null violations, SQLSTATE class 22 (data exceptions: invalid text, NUL bytes, bad
  encodings, out-of-range values) and 54000 (program limit exceeded) `ErrInvalidArgument`, so a job fails
  at once instead of retrying (SPEC "Jobs"); 40001, 40P01 and
  55P03 `ErrConflict`; anything else (connection loss, timeouts) `ErrUnavailable`. Errors that already
  carry a domain class, and context cancellation, pass through.
- **Ids**: a malformed UUID in a lookup matches nothing (`ErrNotFound`, or skipped by `GetUnits`); in an
  insert it is `ErrInvalidArgument`; an empty id on insert gets a fresh UUID.
- **GetUnits** returns a repeated id once, in the position of its first occurrence.
- **Reader** (`Store.Read()`) runs each call as its own statement on the pool; calls needing
  transaction-local settings (the semantic arm's `SET LOCAL`) open a short read-only transaction. Inside a
  `Tx` the same methods run on the caller's transaction.

## Alternatives

- Never retrying in InTx (the first version): every caller would need its own retry loop for a race the
  adapter can resolve alone.
- `SetConfig` returning `ErrNotFound` for an unknown bank: contradicts "a write to an unknown bank creates
  it with tenant defaults".
- Stamping `last_memory_write_at` in each repo: many call sites for the same fact; Seq is the one place
  every content change passes through.

## Spec lines involved

- SPEC "Behavioural rules": change sequence (~1984), unknown banks (~1979), DeleteBank (~1980).
- `internal/port/store.go` (InTx, Seq, Reader), `internal/port/repos.go` (BankRepo).
- `internal/adapter/AGENTS.md` (error mapping).
