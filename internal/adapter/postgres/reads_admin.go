package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"memd/internal/port"
	"strings"
	"time"
)

// The WP-15 admin reads (decision 1500): tag counts, bank stats, observation history, mental-model and document
// listings, and the cross-bank cron and failed-refresh sweeps. The unit reads live in reads_units.go.

// likeLiteral escapes LIKE's metacharacters (%, _ and the escape character \) so s matches only itself.
func likeLiteral(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// containsPattern is the ILIKE pattern of a literal substring s; "" means no filter.
func containsPattern(s string) string {
	if s == "" {
		return ""
	}
	return "%" + likeLiteral(s) + "%"
}

// tagFilterArgs maps a listing's flat tag filter onto the queries' (tags_match, filter_tags) parameters: no
// tags is no filter (decision 1500: the filter applies only when tags are set), else the normalized tags under
// mode. The SQL treats unspecified and unknown modes as any, like domain.MatchTags.
func tagFilterArgs(tags []string, mode domain.TagsMatch) (match string, filter []string) {
	if len(tags) == 0 {
		return string(domain.TagsMatchAny), []string{}
	}
	return string(mode), domain.NormalizeTags(tags)
}

// listedBank returns domain.ErrNotFound unless b exists and is not tombstoned: the regime of listings and
// stats (CCR WP-15-1 §0).
func (r *reader) listedBank(ctx context.Context, b port.BankRef) error {
	row, err := r.queries().GetBank(ctx, db.GetBankParams{TenantID: b.Tenant, ID: b.Bank})
	if err != nil {
		return mapErr(fmt.Sprintf("bank %s/%s", b.Tenant, b.Bank), err)
	}
	if row.DeletingAt != nil {
		return fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
	}
	return nil
}

// TagCounts counts the tags of b's source (unspecified = memories: every live unit) in byte order, keeping
// tags that start with prefix and sort strictly after after; limit <= 0 returns all.
func (r *reader) TagCounts(
	ctx context.Context, b port.BankRef, source domain.TagSource, prefix, after string, limit int,
) ([]domain.TagCount, error) {
	switch source {
	case domain.TagSourceUnspecified:
		source = domain.TagSourceMemories
	case domain.TagSourceMemories, domain.TagSourceMentalModels, domain.TagSourceDocuments:
	default:
		return nil, fmt.Errorf("%w: unknown tag source %q", domain.ErrInvalidArgument, source)
	}
	if err := r.listedBank(ctx, b); err != nil {
		return nil, err
	}
	var lim *int32
	if limit > 0 {
		v := toInt32Sat(limit)
		lim = &v
	}
	rows, err := r.queries().ReadsTagCounts(ctx, db.ReadsTagCountsParams{
		PrefixLike: likeLiteral(prefix) + "%", After: after, Lim: lim, Source: string(source),
		TenantID: b.Tenant, BankID: b.Bank,
	})
	if err != nil {
		return nil, mapErr("tag counts", err)
	}
	out := make([]domain.TagCount, len(rows))
	for i, row := range rows {
		out[i] = domain.TagCount{Tag: row.Tag, Count: row.N}
	}
	return out, nil
}

// ObservationHistory returns observation id's version rows, newest first; no rows (or a malformed id) is
// domain.ErrNotFound.
func (r *reader) ObservationHistory(
	ctx context.Context, b port.BankRef, id port.UnitID,
) ([]domain.ObservationVersion, error) {
	if err := r.listedBank(ctx, b); err != nil {
		return nil, err
	}
	uid, err := parseID(id)
	if err != nil {
		return nil, err
	}
	rows, err := r.queries().ReadsObservationHistory(ctx, db.ReadsObservationHistoryParams{
		TenantID: b.Tenant, BankID: b.Bank, ObservationID: uid,
	})
	if err != nil {
		return nil, mapErr(fmt.Sprintf("history of observation %s", id), err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: observation %s has no history", domain.ErrNotFound, id)
	}
	out := make([]domain.ObservationVersion, len(rows))
	for i, row := range rows {
		out[i] = domain.ObservationVersion{
			Version: row.Version, Text: row.Text, SourceIDs: idsOf[domain.UnitID](row.SourceIds), At: row.At,
		}
	}
	return out, nil
}

// BankStats returns b's counts and change state, read in one statement.
func (r *reader) BankStats(ctx context.Context, b port.BankRef) (domain.BankStats, error) {
	row, err := r.queries().ReadsBankStats(ctx, db.ReadsBankStatsParams{TenantID: b.Tenant, BankID: b.Bank})
	if err != nil {
		return domain.BankStats{}, mapErr(fmt.Sprintf("bank %s/%s", b.Tenant, b.Bank), err)
	}
	if row.DeletingAt != nil {
		return domain.BankStats{}, fmt.Errorf("%w: bank %s/%s is being deleted", domain.ErrNotFound, b.Tenant, b.Bank)
	}
	byType, err := countMap[domain.FactType]("units by fact type", row.UnitsByFactType)
	if err != nil {
		return domain.BankStats{}, err
	}
	byKind, err := countMap[domain.OperationKind]("pending operations by kind", row.PendingOperationsByKind)
	if err != nil {
		return domain.BankStats{}, err
	}
	return domain.BankStats{
		UnitsByFactType: byType, InvalidatedUnits: row.InvalidatedUnits, Documents: row.Documents,
		Entities: row.Entities, MentalModels: row.MentalModels, PendingOperationsByKind: byKind,
		UnconsolidatedUnits: row.UnconsolidatedUnits, LastMemoryWriteAt: row.LastMemoryWriteAt,
		ChangeSeq: row.ChangeSeq,
	}, nil
}

// countMap decodes a JSON object of counts (key -> count) into a non-nil typed map.
func countMap[K ~string](what string, raw json.RawMessage) (map[K]int64, error) {
	var m map[string]int64
	if err := unmarshal(what, raw, &m); err != nil {
		return nil, err
	}
	out := make(map[K]int64, len(m))
	for k, n := range m {
		out[K(k)] = n
	}
	return out, nil
}

// ListMentalModels returns one page of q.Bank's models matching q, ordered by id in byte order, and the next
// page token (the last listed id when more models match, else ""). PageSize 0 lists all; a negative one is
// domain.ErrInvalidArgument.
func (r *reader) ListMentalModels(ctx context.Context, q port.ModelQuery) ([]domain.MentalModel, string, error) {
	if q.PageSize < 0 {
		return nil, "", fmt.Errorf("%w: page size %d is negative", domain.ErrInvalidArgument, q.PageSize)
	}
	if err := r.listedBank(ctx, q.Bank); err != nil {
		return nil, "", err
	}
	match, tags := tagFilterArgs(q.Tags, q.TagsMatch)
	var lim *int32
	if q.PageSize > 0 {
		v := toInt32Sat(q.PageSize) // plus one, to learn whether another page follows
		if v < math.MaxInt32 {
			v++
		}
		lim = &v
	}
	rows, err := r.queries().ReadsListMentalModels(ctx, db.ReadsListMentalModelsParams{
		TenantID: q.Bank.Tenant, BankID: q.Bank.Bank, Folder: q.FolderPrefix,
		FolderLike: likeLiteral(q.FolderPrefix) + "/%", RefreshAfter: q.RefreshAfterConsolidation,
		AfterID: q.PageToken, TagsMatch: match, FilterTags: tags, Lim: lim,
	})
	if err != nil {
		return nil, "", mapErr("list mental models", err)
	}
	next := ""
	if q.PageSize > 0 && len(rows) > q.PageSize {
		rows = rows[:q.PageSize]
		next = rows[len(rows)-1].ID
	}
	out := make([]domain.MentalModel, len(rows))
	for i, row := range rows {
		if out[i], err = modelOf(row); err != nil {
			return nil, "", err
		}
	}
	return out, next, nil
}

// CronModels returns every model with a non-empty refresh_cron on a bank that is not tombstoned, with the
// created_at of its newest refresh operation (zero when none), ordered by (tenant, bank, model id).
func (r *reader) CronModels(ctx context.Context) ([]port.CronModel, error) {
	rows, err := r.queries().ReadsCronModels(ctx)
	if err != nil {
		return nil, mapErr("cron models", err)
	}
	out := make([]port.CronModel, len(rows))
	for i, row := range rows {
		m, err := modelOf(row.MentalModel)
		if err != nil {
			return nil, err
		}
		var last time.Time
		if row.Enqueued {
			last = row.LastEnqueuedAt
		}
		out[i] = port.CronModel{Model: m, LastRefreshEnqueuedAt: last}
	}
	return out, nil
}

// FailedRefreshes returns, per existing model on a bank that is not tombstoned, its latest completed or failed
// refresh operation when that one failed (cancelled operations are ignored), ordered by (tenant, bank, model
// id).
func (r *reader) FailedRefreshes(ctx context.Context) ([]port.FailedRefresh, error) {
	rows, err := r.queries().ReadsFailedRefreshes(ctx)
	if err != nil {
		return nil, mapErr("failed refreshes", err)
	}
	out := make([]port.FailedRefresh, len(rows))
	for i, row := range rows {
		f := port.FailedRefresh{
			Bank: port.BankRef{Tenant: row.TenantID, Bank: row.BankID}, ModelID: port.MentalModelID(row.ModelID),
			OperationID: idOf[port.OperationID](row.OperationID), Error: row.Error,
		}
		if row.FinishedAt != nil {
			f.FailedAt = *row.FinishedAt
		}
		out[i] = f
	}
	return out, nil
}

// ListDocuments returns up to q.Limit (> 0) of q.Bank's documents matching q, newest first ((created_at, id)
// descending) and strictly after q.After, each filled as DocumentRepo.Get fills it.
func (r *reader) ListDocuments(ctx context.Context, q port.DocumentQuery) ([]domain.Document, error) {
	if q.Limit <= 0 {
		return nil, fmt.Errorf("%w: limit %d must be positive", domain.ErrInvalidArgument, q.Limit)
	}
	switch q.TimeField {
	case domain.DocTimeFieldUnspecified, domain.DocTimeFieldCreatedAt, domain.DocTimeFieldUpdatedAt:
	default:
		return nil, fmt.Errorf("%w: unknown document time field %q", domain.ErrInvalidArgument, q.TimeField)
	}
	if err := r.listedBank(ctx, q.Bank); err != nil {
		return nil, err
	}
	match, tags := tagFilterArgs(q.Tags, q.TagsMatch)
	p := db.ReadsListDocumentsParams{
		TenantID: q.Bank.Tenant, BankID: q.Bank.Bank, IDLike: containsPattern(q.Q), TagsMatch: match,
		FilterTags: tags, StartAt: q.Start, TimeField: string(q.TimeField), EndAt: q.End,
		Lim: toInt32Sat(q.Limit),
	}
	if q.After != nil {
		p.AfterCreatedAt, p.AfterID = &q.After.CreatedAt, string(q.After.ID)
	}
	rows, err := r.queries().ReadsListDocuments(ctx, p)
	if err != nil {
		return nil, mapErr("list documents", err)
	}
	out := make([]domain.Document, len(rows))
	for i, row := range rows {
		d, err := documentOf(row.Document)
		if err != nil {
			return nil, err
		}
		if d.UnitsByFactType, err = countMap[domain.FactType]("document unit counts", row.UnitsByFactType); err != nil {
			return nil, err
		}
		for _, n := range d.UnitsByFactType {
			d.UnitCount += n
		}
		out[i] = d
	}
	return out, nil
}
