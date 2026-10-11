package fake

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"memd/internal/domain"
	"memd/internal/port"
	"slices"
	"strings"
	"time"
)

// refreshSerialPrefix is the serial key prefix linking a refresh_mental_model operation to its model:
// "mm:" + model id (decision 1500).
const refreshSerialPrefix = "mm:"

// bankStats counts b's rows; an unknown or tombstoned bank is domain.ErrNotFound.
func bankStats(ctx context.Context, st *state, b port.BankRef) (domain.BankStats, error) {
	if err := liveBank(ctx, st, b); err != nil {
		return domain.BankStats{}, err
	}
	bank := bankOut(st.banks[b])
	out := domain.BankStats{
		UnitsByFactType:         map[domain.FactType]int64{},
		PendingOperationsByKind: map[domain.OperationKind]int64{},
		LastMemoryWriteAt:       bank.LastMemoryWriteAt,
		ChangeSeq:               bank.ChangeSeq,
	}
	for _, row := range st.units {
		if row.u.Bank == b {
			out.UnitsByFactType[row.u.Type]++
			if unconsolidated(row.u) {
				out.UnconsolidatedUnits++
			}
		}
	}
	out.InvalidatedUnits = countIf(st.archive, func(a archiveRow) bool { return a.u.Bank == b })
	out.Documents = countIf(st.documents, func(d docRow) bool { return d.d.Bank == b })
	out.Entities = countIf(st.entities, func(e entityRow) bool { return e.e.Bank == b })
	out.MentalModels = countIf(st.models, func(m modelRow) bool { return m.m.Bank == b })
	for _, row := range st.ops {
		s := row.op.Status
		if row.op.Bank == b && (s == domain.OperationStatusPending || s == domain.OperationStatusProcessing) {
			out.PendingOperationsByKind[row.op.Kind]++
		}
	}
	return out, nil
}

// countIf counts the values of m for which keep is true.
func countIf[K comparable, V any](m map[K]V, keep func(V) bool) int64 {
	var n int64
	for _, v := range m {
		if keep(v) {
			n++
		}
	}
	return n
}

// listDocuments returns up to q.Limit (> 0) of q.Bank's documents matching q, ordered by (created_at, id)
// descending and strictly after q.After, filled like DocumentRepo.Get.
func listDocuments(ctx context.Context, st *state, q port.DocumentQuery) ([]domain.Document, error) {
	if err := checkLimit("ListDocuments", q.Limit); err != nil {
		return nil, err
	}
	if f := q.TimeField; f != domain.DocTimeFieldUnspecified && f != domain.DocTimeFieldCreatedAt &&
		f != domain.DocTimeFieldUpdatedAt {
		return nil, fmt.Errorf("%w: ListDocuments time field %q", domain.ErrInvalidArgument, f)
	}
	if err := liveBank(ctx, st, q.Bank); err != nil {
		return nil, err
	}
	var rows []docRow
	for k, row := range st.documents {
		if k.bank == q.Bank && docMatches(row.d, q) {
			rows = append(rows, row)
		}
	}
	slices.SortFunc(rows, func(x, y docRow) int {
		return cmp.Or(y.d.CreatedAt.Compare(x.d.CreatedAt), cmp.Compare(y.d.ID, x.d.ID))
	})
	out := []domain.Document{}
	for _, row := range capped(rows, q.Limit) {
		out = append(out, st.docOut(row))
	}
	return out, nil
}

// docMatches applies q's id substring, tag, time and cursor filters to d.
func docMatches(d domain.Document, q port.DocumentQuery) bool {
	at := d.CreatedAt
	if q.TimeField == domain.DocTimeFieldUpdatedAt {
		at = d.UpdatedAt
	}
	return (q.Q == "" || containsFold(string(d.ID), q.Q)) &&
		(len(q.Tags) == 0 || domain.MatchTags(d.Tags, q.Tags, q.TagsMatch)) &&
		inTimeRange(&at, q.Start, q.End) &&
		(q.After == nil || before(d.CreatedAt, string(d.ID), q.After.CreatedAt, string(q.After.ID)))
}

// listMentalModels returns one page of q.Bank's models matching q, ordered by id, and the next page token
// (the last returned id when more matching models follow, "" otherwise). PageSize 0 means all; a negative
// one is domain.ErrInvalidArgument.
func listMentalModels(ctx context.Context, st *state, q port.ModelQuery) ([]domain.MentalModel, string, error) {
	if q.PageSize < 0 {
		return nil, "", fmt.Errorf("%w: ListMentalModels page size %d", domain.ErrInvalidArgument, q.PageSize)
	}
	if err := liveBank(ctx, st, q.Bank); err != nil {
		return nil, "", err
	}
	out := []domain.MentalModel{}
	for k, row := range st.models {
		if k.bank != q.Bank || string(k.id) <= q.PageToken {
			continue
		}
		if m := modelOut(row); modelMatches(m, q) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(x, y domain.MentalModel) int { return cmp.Compare(x.Spec.ID, y.Spec.ID) })
	if q.PageSize == 0 || len(out) <= q.PageSize {
		return out, "", nil
	}
	out = out[:q.PageSize]
	return out, string(out[len(out)-1].Spec.ID), nil
}

// modelMatches applies q's folder, tag and trigger filters to m.
func modelMatches(m domain.MentalModel, q port.ModelQuery) bool {
	p, folder := q.FolderPrefix, m.Spec.FolderPath
	return (p == "" || folder == p || strings.HasPrefix(folder, p+"/")) &&
		(len(q.Tags) == 0 || domain.MatchTags(m.Spec.Tags, q.Tags, q.TagsMatch)) &&
		(!q.RefreshAfterConsolidation || m.Spec.Trigger.RefreshAfterConsolidation)
}

// modelsCiting returns the sorted ids of b's models whose source ids intersect ids.
func modelsCiting(ctx context.Context, st *state, b port.BankRef, ids []port.UnitID) ([]port.MentalModelID, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	out := []port.MentalModelID{}
	cited := func(id port.UnitID) bool { return slices.Contains(ids, id) }
	for k, row := range st.models {
		if k.bank == b && slices.ContainsFunc(row.m.SourceIDs, cited) {
			out = append(out, k.id)
		}
	}
	slices.Sort(out)
	return out, nil
}

// compareModelKeys orders models by (tenant, bank, model id).
func compareModelKeys(x, y modelKey) int {
	return cmp.Or(compareBanks(x.bank, y.bank), cmp.Compare(x.id, y.id))
}

// compareBanks orders banks by (tenant, bank).
func compareBanks(x, y port.BankRef) int {
	return cmp.Or(cmp.Compare(x.Tenant, y.Tenant), cmp.Compare(x.Bank, y.Bank))
}

// liveModelKeys returns the keys of every model on a bank that is not tombstoned, ordered by (tenant, bank,
// model id).
func (s *state) liveModelKeys() []modelKey {
	var keys []modelKey
	for k := range s.models {
		if s.bankLive(k.bank) {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, compareModelKeys)
	return keys
}

// refreshOps returns the refresh_mental_model operations of model k (serial key "mm:" + id, same bank).
func (s *state) refreshOps(k modelKey) []opRow {
	var out []opRow
	for _, row := range s.ops {
		if row.op.Bank == k.bank && row.op.Kind == domain.OperationKindRefreshMentalModel &&
			row.op.SerialKey == refreshSerialPrefix+string(k.id) {
			out = append(out, row)
		}
	}
	return out
}

// cronModels returns every model with a non-empty refresh_cron on a bank that is not tombstoned, with the
// newest created_at of its refresh operations (any status; zero when none), ordered by (tenant, bank, id).
func cronModels(ctx context.Context, st *state) ([]port.CronModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	out := []port.CronModel{}
	for _, k := range st.liveModelKeys() {
		m := modelOut(st.models[k])
		if m.Spec.Trigger.RefreshCron == "" {
			continue
		}
		var last time.Time
		for _, op := range st.refreshOps(k) {
			if op.op.CreatedAt.After(last) {
				last = op.op.CreatedAt
			}
		}
		out = append(out, port.CronModel{Model: m, LastRefreshEnqueuedAt: last})
	}
	return out, nil
}

// failedRefreshes reports, per model on a bank that is not tombstoned, its latest COMPLETED or FAILED refresh
// operation (finished_at, then created_at, then id, all descending; CANCELLED is ignored) when that one
// FAILED, ordered by (tenant, bank, model id).
func failedRefreshes(ctx context.Context, st *state) ([]port.FailedRefresh, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	out := []port.FailedRefresh{}
	for _, k := range st.liveModelKeys() {
		var latest *opRow
		for _, row := range st.refreshOps(k) {
			s := row.op.Status
			if s != domain.OperationStatusCompleted && s != domain.OperationStatusFailed {
				continue
			}
			if latest == nil || laterFinish(row, *latest) {
				latest = &row
			}
		}
		if latest == nil || latest.op.Status != domain.OperationStatusFailed {
			continue
		}
		out = append(out, port.FailedRefresh{
			Bank: k.bank, ModelID: k.id, OperationID: latest.op.ID, FailedAt: finishedAt(*latest),
			Error: latest.op.Error,
		})
	}
	return out, nil
}

// finishedAt returns the operation's finished_at, zero when unset.
func finishedAt(row opRow) time.Time {
	if row.finishedAt == nil {
		return time.Time{}
	}
	return *row.finishedAt
}

// laterFinish reports whether x sorts before y by (finished_at, created_at, id) descending.
func laterFinish(x, y opRow) bool {
	return cmp.Or(finishedAt(x).Compare(finishedAt(y)), x.op.CreatedAt.Compare(y.op.CreatedAt),
		cmp.Compare(x.op.ID, y.op.ID)) > 0
}

// banksToConsolidate returns every bank that is not tombstoned and has an unconsolidated fact with an
// embedding, ordered by (tenant, bank).
func banksToConsolidate(ctx context.Context, st *state) ([]port.BankRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxErr(err)
	}
	banks := map[port.BankRef]bool{}
	for _, row := range st.units {
		if consolidationCandidate(row.u) && st.bankLive(row.u.Bank) {
			banks[row.u.Bank] = true
		}
	}
	out := slices.SortedFunc(maps.Keys(banks), compareBanks)
	if out == nil {
		out = []port.BankRef{}
	}
	return out, nil
}
