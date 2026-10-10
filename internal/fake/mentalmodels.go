package fake

import (
	"cmp"
	"context"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"time"
)

// modelRepo is the MentalModelRepo of one transaction.
type modelRepo struct{ t *tx }

var _ port.MentalModelRepo = modelRepo{}

// Get returns the model; unknown is domain.ErrNotFound.
func (r modelRepo) Get(_ context.Context, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error) {
	row, ok := r.t.st.models[modelKey{b, id}]
	if !ok {
		return domain.MentalModel{}, notFound("mental model %s", id)
	}
	return modelOut(row), nil
}

// Create inserts a new model (the bank must exist); an existing id is domain.ErrAlreadyExists.
func (r modelRepo) Create(_ context.Context, m domain.MentalModel) error {
	k := modelKey{m.Bank, m.Spec.ID}
	if _, ok := r.t.st.models[k]; ok {
		return fmt.Errorf("%w: mental model %s", domain.ErrAlreadyExists, m.Spec.ID)
	}
	if _, ok := r.t.st.banks[m.Bank]; !ok {
		return notFound("bank %s/%s", m.Bank.Tenant, m.Bank.Bank)
	}
	m.CreatedAt = r.t.now
	r.t.st.models[k] = modelIn(m, r.t.now)
	return nil
}

// Update replaces the model's spec, content and refresh state; unknown is domain.ErrNotFound.
func (r modelRepo) Update(_ context.Context, m domain.MentalModel) error {
	k := modelKey{m.Bank, m.Spec.ID}
	row, ok := r.t.st.models[k]
	if !ok {
		return notFound("mental model %s", m.Spec.ID)
	}
	m.CreatedAt = row.m.CreatedAt
	r.t.st.models[k] = modelIn(m, r.t.now)
	return nil
}

// Delete removes the model and its history; unknown is domain.ErrNotFound.
func (r modelRepo) Delete(_ context.Context, b port.BankRef, id port.MentalModelID) error {
	k := modelKey{b, id}
	if _, ok := r.t.st.models[k]; !ok {
		return notFound("mental model %s", id)
	}
	delete(r.t.st.models, k)
	deleteSlice(&r.t.st.history, len(r.t.st.history), func(h historyRow) bool { return h.key == k })
	return nil
}

// AppendHistory adds an entry, then keeps only the newest keep entries of that kind (keep <= 0 = all).
// Unknown models are domain.ErrNotFound.
func (r modelRepo) AppendHistory(
	_ context.Context, b port.BankRef, id port.MentalModelID, e domain.MentalModelHistoryEntry, keep int,
) error {
	k := modelKey{b, id}
	if _, ok := r.t.st.models[k]; !ok {
		return notFound("mental model %s", id)
	}
	if e.ChangedAt.IsZero() {
		e.ChangedAt = r.t.now
	}
	st := r.t.st
	st.history = append(st.history, historyRow{key: k, e: e, order: st.next()})
	if keep <= 0 {
		return nil
	}
	n := 0
	for _, h := range st.history {
		if h.key == k && h.e.Kind == e.Kind {
			n++
		}
	}
	deleteSlice(&st.history, n-keep, func(h historyRow) bool { return h.key == k && h.e.Kind == e.Kind })
	return nil
}

// History returns up to limit entries (limit <= 0 = all), newest first; kind unspecified = every kind.
func (r modelRepo) History(
	_ context.Context, b port.BankRef, id port.MentalModelID, kind domain.HistoryKind, limit int,
) ([]domain.MentalModelHistoryEntry, error) {
	k := modelKey{b, id}
	var rows []historyRow
	for _, h := range r.t.st.history {
		if h.key == k && (kind == domain.HistoryKindUnspecified || h.e.Kind == kind) {
			rows = append(rows, h)
		}
	}
	slices.SortFunc(rows, func(x, y historyRow) int { return cmp.Compare(y.order, x.order) })
	rows = capped(rows, limit)
	out := make([]domain.MentalModelHistoryEntry, len(rows))
	for i, h := range rows {
		out[i] = h.e
	}
	return out, nil
}

// modelIn stores m with its spec as JSON and its slices copied.
func modelIn(m domain.MentalModel, now time.Time) modelRow {
	spec := jsonOf(&m.Spec)
	m.Spec = domain.MentalModelSpec{}
	m.StructuredOutputJSON = slices.Clone(m.StructuredOutputJSON)
	m.SourceIDs, m.Embedding = slices.Clone(m.SourceIDs), slices.Clone(m.Embedding)
	m.LastRefreshedAt, m.LastRefreshFailedAt = ptrCopy(m.LastRefreshedAt), ptrCopy(m.LastRefreshFailedAt)
	m.LastTrace = fromJSON[domain.RefreshTrace](jsonOf(m.LastTrace))
	m.UpdatedAt = now
	return modelRow{m: m, spec: spec}
}

// modelOut returns a caller-owned copy of a stored model.
func modelOut(row modelRow) domain.MentalModel {
	m := row.m
	if s := fromJSON[domain.MentalModelSpec](row.spec); s != nil {
		m.Spec = *s
	}
	m.StructuredOutputJSON = slices.Clone(m.StructuredOutputJSON)
	m.SourceIDs, m.Embedding = slices.Clone(m.SourceIDs), slices.Clone(m.Embedding)
	m.LastRefreshedAt, m.LastRefreshFailedAt = ptrCopy(m.LastRefreshedAt), ptrCopy(m.LastRefreshFailedAt)
	m.LastTrace = fromJSON[domain.RefreshTrace](jsonOf(m.LastTrace))
	return m
}

// directiveRepo is the DirectiveRepo of one transaction.
type directiveRepo struct{ t *tx }

var _ port.DirectiveRepo = directiveRepo{}

// Upsert creates the directive (a new id when d.ID is empty; the bank must exist) or replaces it, keeping
// its creation time and list position; it returns the id.
func (r directiveRepo) Upsert(_ context.Context, d domain.Directive) (port.DirectiveID, error) {
	if _, ok := r.t.st.banks[d.Bank]; !ok {
		return "", notFound("bank %s/%s", d.Bank.Tenant, d.Bank.Bank)
	}
	if d.ID == "" {
		d.ID = port.DirectiveID(newUUID())
	}
	k := directiveKey{d.Bank, d.ID}
	row, ok := r.t.st.directives[k]
	if !ok {
		row = directiveRow{order: r.t.st.next()}
		d.CreatedAt = r.t.now
	} else {
		d.CreatedAt = row.d.CreatedAt
	}
	d.Rules, d.Tags, d.UpdatedAt = slices.Clone(d.Rules), slices.Clone(d.Tags), r.t.now
	row.d = d
	r.t.st.directives[k] = row
	return d.ID, nil
}

// List returns the bank's directives (only active ones when activeOnly), oldest first.
func (r directiveRepo) List(_ context.Context, b port.BankRef, activeOnly bool) ([]domain.Directive, error) {
	var rows []directiveRow
	for k, row := range r.t.st.directives {
		if k.bank == b && (!activeOnly || row.d.Active) {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(x, y directiveRow) int {
		return cmp.Or(x.d.CreatedAt.Compare(y.d.CreatedAt), cmp.Compare(x.order, y.order))
	})
	out := make([]domain.Directive, len(rows))
	for i, row := range rows {
		d := row.d
		d.Rules, d.Tags = slices.Clone(d.Rules), slices.Clone(d.Tags)
		out[i] = d
	}
	return out, nil
}

// Delete removes the directive; unknown is domain.ErrNotFound.
func (r directiveRepo) Delete(_ context.Context, b port.BankRef, id port.DirectiveID) error {
	k := directiveKey{b, id}
	if _, ok := r.t.st.directives[k]; !ok {
		return notFound("directive %s", id)
	}
	delete(r.t.st.directives, k)
	return nil
}
