package fake

import (
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// bankRepo is the BankRepo of one transaction.
type bankRepo struct{ t *tx }

var _ port.BankRepo = bankRepo{}

// Get returns the bank (a tombstoned one included); unknown is domain.ErrNotFound.
func (r bankRepo) Get(_ context.Context, b port.BankRef) (domain.Bank, error) {
	row, ok := r.t.st.banks[b]
	if !ok {
		return domain.Bank{}, notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	return bankOut(row), nil
}

// Ensure returns the bank, creating it with empty overrides; a tombstoned bank is domain.ErrBankDeleting.
func (r bankRepo) Ensure(_ context.Context, b port.BankRef) (domain.Bank, error) {
	row, ok := r.t.st.banks[b]
	if !ok {
		row = bankRow{bank: domain.Bank{Ref: b, CreatedAt: r.t.now, UpdatedAt: r.t.now}}
		r.t.st.banks[b] = row
	}
	if row.bank.Deleting() {
		return domain.Bank{}, fmt.Errorf("%w: bank %s/%s", domain.ErrBankDeleting, b.Tenant, b.Bank)
	}
	return bankOut(row), nil
}

// Create inserts a bank with its name and config; an existing bank is domain.ErrAlreadyExists.
func (r bankRepo) Create(_ context.Context, bank domain.Bank) error {
	if _, ok := r.t.st.banks[bank.Ref]; ok {
		return fmt.Errorf("%w: bank %s/%s", domain.ErrAlreadyExists, bank.Ref.Tenant, bank.Ref.Bank)
	}
	r.t.st.banks[bank.Ref] = bankRow{
		bank:   domain.Bank{Ref: bank.Ref, Name: bank.Name, CreatedAt: r.t.now, UpdatedAt: r.t.now},
		config: jsonOf(&bank.Config),
	}
	return nil
}

// SetConfig replaces the bank's own overrides; unknown is domain.ErrNotFound.
func (r bankRepo) SetConfig(_ context.Context, b port.BankRef, cfg domain.BankConfig) error {
	row, ok := r.t.st.banks[b]
	if !ok {
		return notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	row.config = jsonOf(&cfg)
	row.bank.UpdatedAt = r.t.now
	r.t.st.banks[b] = row
	return nil
}

// MarkDeleting sets deleting_at once; unknown is domain.ErrNotFound.
func (r bankRepo) MarkDeleting(_ context.Context, b port.BankRef, at time.Time) error {
	row, ok := r.t.st.banks[b]
	if !ok {
		return notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	if row.bank.DeletingAt == nil {
		row.bank.DeletingAt = ptrCopy(&at)
		row.bank.UpdatedAt = r.t.now
		r.t.st.banks[b] = row
	}
	return nil
}

// tombstoned returns the bank row, failing unless it exists and is tombstoned.
func (r bankRepo) tombstoned(b port.BankRef) error {
	row, ok := r.t.st.banks[b]
	if !ok {
		return notFound("bank %s/%s", b.Tenant, b.Bank)
	}
	if !row.bank.Deleting() {
		return precondition("bank %s/%s is not being deleted", b.Tenant, b.Bank)
	}
	return nil
}

// DeleteBatch deletes up to limit content rows of the tombstoned bank, dependent tables first, and returns
// how many it deleted; 0 means only the bank row, its events and its operations are left.
func (r bankRepo) DeleteBatch(_ context.Context, b port.BankRef, limit int) (int, error) {
	if err := r.tombstoned(b); err != nil {
		return 0, err
	}
	st, left := r.t.st, limit
	steps := []func(n int) int{
		func(n int) int {
			return deleteWhere(st.unitEntities, n, func(k unitEntityKey, _ struct{}) bool { return st.unitInBank(k.unit, b) })
		},
		func(n int) int {
			return deleteWhere(st.cooc, n, func(k entityPair, _ int) bool { return st.entityInBank(k.a, b) })
		},
		func(n int) int {
			return deleteWhere(st.links, n, func(_ linkKey, v linkRow) bool { return v.l.Bank == b })
		},
		func(n int) int {
			return deleteWhere(st.evidence, n, func(k evidenceKey, _ string) bool { return st.unitInBank(k.obs, b) })
		},
		func(n int) int {
			return deleteWhere(st.dirty, n, func(k bankUnitKey, _ int64) bool { return k.bank == b })
		},
		func(n int) int {
			return deleteWhere(st.entMaint, n, func(k bankEntityKey, _ int64) bool { return k.bank == b })
		},
		func(n int) int {
			return deleteSlice(&st.obsVersions, n, func(v obsVersionRow) bool { return v.bank == b })
		},
		func(n int) int {
			return deleteSlice(&st.audit, n, func(v domain.AuditEntry) bool { return v.Bank == b })
		},
		func(n int) int {
			return deleteSlice(&st.history, n, func(v historyRow) bool { return v.key.bank == b })
		},
		func(n int) int {
			return deleteWhere(st.models, n, func(k modelKey, _ modelRow) bool { return k.bank == b })
		},
		func(n int) int {
			return deleteWhere(st.directives, n, func(k directiveKey, _ directiveRow) bool { return k.bank == b })
		},
		func(n int) int {
			return deleteWhere(st.archive, n, func(_ port.UnitID, v archiveRow) bool { return v.u.Bank == b })
		},
		func(n int) int {
			return deleteWhere(st.units, n, func(_ port.UnitID, v unitRow) bool { return v.u.Bank == b })
		},
		func(n int) int {
			return deleteWhere(st.entities, n, func(_ port.EntityID, v entityRow) bool { return v.e.Bank == b })
		},
		func(n int) int {
			return deleteWhere(st.chunks, n, func(k chunkKey, _ domain.Chunk) bool { return k.bank == b })
		},
		func(n int) int {
			return deleteWhere(st.documents, n, func(k docKey, _ docRow) bool { return k.bank == b })
		},
	}
	for _, step := range steps {
		if left <= 0 {
			break
		}
		left -= step(left)
	}
	return limit - left, nil
}

// Delete removes the tombstoned bank with everything it still owns, its events and operations included.
func (r bankRepo) Delete(ctx context.Context, b port.BankRef) error {
	if err := r.tombstoned(b); err != nil {
		return err
	}
	for {
		n, err := r.DeleteBatch(ctx, b, 1<<30)
		if err != nil || n == 0 {
			break
		}
	}
	st := r.t.st
	deleteSlice(&st.events, len(st.events), func(v eventRow) bool { return v.e.Bank == b })
	deleteWhere(st.ops, len(st.ops), func(_ port.OperationID, v opRow) bool { return v.op.Bank == b })
	delete(st.banks, b)
	return nil
}

// TenantConfig returns the tenant's config, or the zero config when none is set.
func (r bankRepo) TenantConfig(_ context.Context, tenant string) (domain.BankConfig, error) {
	return configOf(r.t.st.tenants[tenant]), nil
}

// SetTenantConfig replaces the tenant's config.
func (r bankRepo) SetTenantConfig(_ context.Context, tenant string, cfg domain.BankConfig) error {
	r.t.st.tenants[tenant] = jsonOf(&cfg)
	return nil
}

// bankOut returns a caller-owned copy of a bank row.
func bankOut(row bankRow) domain.Bank {
	b := row.bank
	b.Config = configOf(row.config)
	b.DeletingAt, b.LastMemoryWriteAt = ptrCopy(b.DeletingAt), ptrCopy(b.LastMemoryWriteAt)
	return b
}

// unitInBank reports whether id is a live or archived unit of b.
func (s *state) unitInBank(id port.UnitID, b port.BankRef) bool {
	if row, ok := s.units[id]; ok {
		return row.u.Bank == b
	}
	if row, ok := s.archive[id]; ok {
		return row.u.Bank == b
	}
	return false
}

// entityInBank reports whether id is an entity of b.
func (s *state) entityInBank(id port.EntityID, b port.BankRef) bool {
	row, ok := s.entities[id]
	return ok && row.e.Bank == b
}

// deleteWhere deletes up to n entries of m for which del is true and returns how many it deleted.
func deleteWhere[K comparable, V any](m map[K]V, n int, del func(K, V) bool) int {
	var doomed []K
	for k, v := range m {
		if len(doomed) >= n {
			break
		}
		if del(k, v) {
			doomed = append(doomed, k)
		}
	}
	for _, k := range doomed {
		delete(m, k)
	}
	return len(doomed)
}

// deleteSlice deletes up to n elements of *xs matching del, keeping the order of the rest, and returns how
// many it deleted.
func deleteSlice[T any](xs *[]T, n int, del func(T) bool) int {
	removed := 0
	*xs = slices.DeleteFunc(*xs, func(v T) bool {
		if removed < n && del(v) {
			removed++
			return true
		}
		return false
	})
	return removed
}
