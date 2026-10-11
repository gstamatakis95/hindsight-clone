package mentalmodel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/port"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Page size defaults for the list RPCs.
const (
	defaultPageSize = 50
	maxPageSize     = 500
)

// modelIDPattern is a valid model id: lowercase alphanumerics and hyphens.
var modelIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ListRequest mirrors server.ListMentalModelsRequest field for field, so WP-14 converts it directly.
type ListRequest struct {
	// Bank is the bank listed.
	Bank port.BankRef
	// FolderPrefix limits models to a folder subtree; empty = all.
	FolderPrefix string
	// Detail is how much of each model to load.
	Detail domain.Detail
	// Tags filters by model tags; empty = none.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// PageSize caps the page; 0 = the RPC default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// ModelPage mirrors server.MentalModelPage.
type ModelPage struct {
	// Items are the page's models.
	Items []domain.MentalModel
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// HistoryRequest mirrors server.HistoryRequest.
type HistoryRequest struct {
	// Bank owns the model.
	Bank port.BankRef
	// ID is the model.
	ID port.MentalModelID
	// Kind limits entries to one kind; unspecified = all.
	Kind domain.HistoryKind
	// PageSize caps the page; 0 = the RPC default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// HistoryPage mirrors server.HistoryPage.
type HistoryPage struct {
	// Items are the page's entries, newest first.
	Items []domain.MentalModelHistoryEntry
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// SpecPatch computes a new spec from the current one; pure (it may run again on a transaction retry).
// server.SpecPatch converts to it.
type SpecPatch func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error)

// Admin serves the MentalModelService RPCs other than DryRunRefresh and SearchPages. It mirrors
// server.MentalModelAdmin with package-local request types (the server imports this package, so the
// engine can't name the server's); WP-14 adapts one to the other with plain conversions (decision 1205).
type Admin interface {
	// CreateMentalModel stores spec and queues its first build, returning the model and the build.
	CreateMentalModel(
		ctx context.Context, b port.BankRef, spec domain.MentalModelSpec,
	) (domain.MentalModel, domain.Operation, error)
	// GetMentalModel returns one model loaded to detail (unspecified = full); unknown = domain.ErrNotFound.
	GetMentalModel(
		ctx context.Context, b port.BankRef, id port.MentalModelID, detail domain.Detail,
	) (domain.MentalModel, error)
	// ListMentalModels returns one page of models (detail unspecified = metadata).
	ListMentalModels(ctx context.Context, req ListRequest) (ModelPage, error)
	// UpdateMentalModel applies patch to the model's spec in one transaction and returns the model.
	UpdateMentalModel(
		ctx context.Context, b port.BankRef, id port.MentalModelID, patch SpecPatch,
	) (domain.MentalModel, error)
	// RefreshMentalModel queues a manual refresh, or joins and upgrades the queued one (joined = true).
	RefreshMentalModel(ctx context.Context, req RefreshRequest) (op domain.Operation, joined bool, err error)
	// ClearMentalModel empties the model's content so the next refresh is full, and returns the model.
	ClearMentalModel(ctx context.Context, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error)
	// MentalModelHistory returns one page of the model's history.
	MentalModelHistory(ctx context.Context, req HistoryRequest) (HistoryPage, error)
	// DeleteMentalModel removes the model and its history; unknown = domain.ErrNotFound.
	DeleteMentalModel(ctx context.Context, b port.BankRef, id port.MentalModelID) error
	// DeleteFolder removes every model under folder path (recursive) and returns how many.
	DeleteFolder(ctx context.Context, b port.BankRef, path string) (int, error)
}

// AdminDeps are the Admin's dependencies.
type AdminDeps struct {
	// Store reads and writes models and queues refreshes.
	Store port.Store
	// Models lists a bank's models (port.Reader).
	Models ModelLister
	// Detector computes is_stale on reads; nil = never stale.
	Detector ChangeDetector
	// Clock dates enqueues.
	Clock port.Clock
	// Config reads history_max_entries for Clear; nil = the default (50).
	Config port.ConfigResolver
}

// admin is the Admin implementation.
type admin struct{ d AdminDeps }

var _ Admin = (*admin)(nil)

// NewAdmin returns the mental-model Admin.
func NewAdmin(d AdminDeps) Admin { return &admin{d: d} }

// CreateMentalModel validates and stores spec (bank created on first write) and queues the first build as
// a manual, full refresh, in one transaction.
func (a *admin) CreateMentalModel(
	ctx context.Context, b port.BankRef, spec domain.MentalModelSpec,
) (domain.MentalModel, domain.Operation, error) {
	if spec.ID == "" {
		spec.ID = port.MentalModelID(uuid.NewString())
	}
	spec, err := normalizeSpec(spec)
	if err != nil {
		return domain.MentalModel{}, domain.Operation{}, err
	}
	var m domain.MentalModel
	var op domain.Operation
	err = a.d.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.Banks().Ensure(ctx, b); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, b); err != nil {
			return err
		}
		if err := tx.MentalModels().Create(ctx, domain.MentalModel{Spec: spec, Bank: b}); err != nil {
			return err
		}
		req := RefreshRequest{Bank: b, ID: spec.ID, Manual: true, ForceFull: true}
		id, _, err := EnqueueRefresh(ctx, tx, req, a.d.Clock.Now())
		if err != nil {
			return err
		}
		if op, err = tx.Queue().Get(ctx, b, id); err != nil {
			return err
		}
		m, err = tx.MentalModels().Get(ctx, b, spec.ID)
		return err
	})
	if err != nil {
		return domain.MentalModel{}, domain.Operation{}, fmt.Errorf("mentalmodel: create %s: %w", spec.ID, err)
	}
	return m, op, nil
}

// GetMentalModel reads the model, computes is_stale and trims it to detail.
func (a *admin) GetMentalModel(
	ctx context.Context, b port.BankRef, id port.MentalModelID, detail domain.Detail,
) (domain.MentalModel, error) {
	m, err := loadModel(ctx, a.d.Store, b, id)
	if err != nil {
		return domain.MentalModel{}, err
	}
	if detail == domain.DetailUnspecified {
		detail = domain.DetailFull
	}
	return a.present(ctx, m, detail)
}

// ListMentalModels lists one page through the ModelLister.
func (a *admin) ListMentalModels(ctx context.Context, req ListRequest) (ModelPage, error) {
	size := req.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	items, next, err := a.d.Models.ListMentalModels(ctx, ModelQuery{
		Bank: req.Bank, FolderPrefix: normalizeFolder(req.FolderPrefix), Tags: domain.NormalizeTags(req.Tags),
		TagsMatch: req.TagsMatch, PageSize: min(size, maxPageSize), PageToken: req.PageToken,
	})
	if err != nil {
		return ModelPage{}, fmt.Errorf("mentalmodel: list: %w", err)
	}
	detail := req.Detail
	if detail == domain.DetailUnspecified {
		detail = domain.DetailMetadata
	}
	for i := range items {
		if items[i], err = a.present(ctx, items[i], detail); err != nil {
			return ModelPage{}, err
		}
	}
	return ModelPage{Items: items, NextPageToken: next}, nil
}

// UpdateMentalModel applies patch in one transaction. A changed source query or scope resets
// last_seq_seen, so the next refresh runs in full (mode_fallback_reason source_query_changed).
func (a *admin) UpdateMentalModel(
	ctx context.Context, b port.BankRef, id port.MentalModelID, patch SpecPatch,
) (domain.MentalModel, error) {
	var m domain.MentalModel
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := lockedModel(ctx, tx, b, id)
		if err != nil {
			return err
		}
		spec, err := patch(cur.Spec)
		if err != nil {
			return err
		}
		spec.ID = cur.Spec.ID
		if spec, err = normalizeSpec(spec); err != nil {
			return err
		}
		if scopeChanged(cur.Spec, spec) {
			cur.LastSeqSeen = 0
		}
		cur.Spec = spec
		if err := tx.MentalModels().Update(ctx, cur); err != nil {
			return err
		}
		m, err = tx.MentalModels().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return domain.MentalModel{}, fmt.Errorf("mentalmodel: update %s: %w", id, err)
	}
	return a.present(ctx, m, domain.DetailFull)
}

// RefreshMentalModel queues a manual refresh of an existing model, or joins and upgrades the queued one.
func (a *admin) RefreshMentalModel(ctx context.Context, req RefreshRequest) (domain.Operation, bool, error) {
	req.Manual = true
	var op domain.Operation
	var joined bool
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.MentalModels().Get(ctx, req.Bank, req.ID); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, req.Bank); err != nil {
			return err
		}
		id, j, err := EnqueueRefresh(ctx, tx, req, a.d.Clock.Now())
		if err != nil {
			return err
		}
		joined = j
		op, err = tx.Queue().Get(ctx, req.Bank, id)
		return err
	})
	if err != nil {
		return domain.Operation{}, false, fmt.Errorf("mentalmodel: refresh %s: %w", req.ID, err)
	}
	return op, joined, nil
}

// ClearMentalModel empties content, structured output, sources and the watermark. The cleared content is
// kept as a version history entry, so nothing is lost.
func (a *admin) ClearMentalModel(
	ctx context.Context, b port.BankRef, id port.MentalModelID,
) (domain.MentalModel, error) {
	keep := a.historyKeep(ctx, b)
	var m domain.MentalModel
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error {
		cur, err := lockedModel(ctx, tx, b, id)
		if err != nil {
			return err
		}
		if cur.ContentMD != "" {
			e := domain.MentalModelHistoryEntry{
				Kind: domain.HistoryKindVersion, Version: cur.Version, PreviousContent: cur.ContentMD,
				ChangedAt: a.d.Clock.Now(),
			}
			if err := tx.MentalModels().AppendHistory(ctx, b, id, e, keep); err != nil {
				return err
			}
		}
		cur.ContentMD, cur.StructuredOutputJSON, cur.SourceIDs, cur.Embedding = "", nil, nil, nil
		cur.LastSeqSeen, cur.LastTrace = 0, nil
		if err := tx.MentalModels().Update(ctx, cur); err != nil {
			return err
		}
		m, err = tx.MentalModels().Get(ctx, b, id)
		return err
	})
	if err != nil {
		return domain.MentalModel{}, fmt.Errorf("mentalmodel: clear %s: %w", id, err)
	}
	return a.present(ctx, m, domain.DetailFull)
}

// MentalModelHistory pages the history with an offset token.
func (a *admin) MentalModelHistory(ctx context.Context, req HistoryRequest) (HistoryPage, error) {
	offset := 0
	if req.PageToken != "" {
		n, err := strconv.Atoi(req.PageToken)
		if err != nil || n < 0 {
			return HistoryPage{}, fmt.Errorf("%w: bad page token", domain.ErrInvalidArgument)
		}
		offset = n
	}
	size := req.PageSize
	if size <= 0 {
		size = defaultPageSize
	}
	size = min(size, maxPageSize)
	var entries []domain.MentalModelHistoryEntry
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error {
		if _, err := tx.MentalModels().Get(ctx, req.Bank, req.ID); err != nil {
			return err
		}
		var err error
		entries, err = tx.MentalModels().History(ctx, req.Bank, req.ID, req.Kind, offset+size+1)
		return err
	})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("mentalmodel: history %s: %w", req.ID, err)
	}
	page := HistoryPage{}
	if offset < len(entries) {
		page.Items = entries[offset:min(offset+size, len(entries))]
	}
	if len(entries) > offset+size {
		page.NextPageToken = strconv.Itoa(offset + size)
	}
	return page, nil
}

// DeleteMentalModel removes the model and its history.
func (a *admin) DeleteMentalModel(ctx context.Context, b port.BankRef, id port.MentalModelID) error {
	err := a.d.Store.InTx(ctx, func(tx port.Tx) error { return tx.MentalModels().Delete(ctx, b, id) })
	if err != nil {
		return fmt.Errorf("mentalmodel: delete %s: %w", id, err)
	}
	return nil
}

// DeleteFolder deletes every model whose folder is path or lies under it. An empty path is rejected so a
// typo can't delete every page.
func (a *admin) DeleteFolder(ctx context.Context, b port.BankRef, path string) (int, error) {
	path = normalizeFolder(path)
	if path == "" {
		return 0, fmt.Errorf("%w: folder_path is required", domain.ErrInvalidArgument)
	}
	models, err := listAll(ctx, a.d.Models, ModelQuery{Bank: b, FolderPrefix: path})
	if err != nil {
		return 0, err
	}
	n := 0
	err = a.d.Store.InTx(ctx, func(tx port.Tx) error {
		n = 0
		for _, m := range models {
			err := tx.MentalModels().Delete(ctx, b, m.Spec.ID)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			n++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("mentalmodel: delete folder %s: %w", path, err)
	}
	return n, nil
}

// present computes is_stale (full detail only; other details leave it false) and trims m to detail.
func (a *admin) present(ctx context.Context, m domain.MentalModel, detail domain.Detail) (domain.MentalModel, error) {
	m.IsStale = false
	if a.d.Detector != nil && detail == domain.DetailFull {
		stale, _, err := a.d.Detector.NeedsRefresh(ctx, m)
		if err != nil {
			return domain.MentalModel{}, fmt.Errorf("mentalmodel: staleness of %s: %w", m.Spec.ID, err)
		}
		m.IsStale = stale
	}
	m.Embedding = nil
	switch detail {
	case domain.DetailMetadata:
		m.ContentMD, m.StructuredOutputJSON, m.LastTrace = "", nil, nil
	case domain.DetailContent:
		m.LastTrace = nil
	}
	return m, nil
}

// lockedModel takes tx.Seq(b) first, which locks the bank row and so serializes every read-modify-write
// of a model row (runner write, reconcile, update, clear), then reads the model.
func lockedModel(ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return domain.MentalModel{}, err
	}
	return tx.MentalModels().Get(ctx, b, id)
}

// historyKeep reads history_max_entries for b (default on any error).
func (a *admin) historyKeep(ctx context.Context, b port.BankRef) int {
	if a.d.Config == nil {
		return defaultHistoryMaxEntries
	}
	cfg, err := a.d.Config.Effective(ctx, b)
	if err != nil {
		return defaultHistoryMaxEntries
	}
	return historyKeep(cfg)
}

// normalizeSpec validates spec and normalizes its tags and folder path.
func normalizeSpec(spec domain.MentalModelSpec) (domain.MentalModelSpec, error) {
	if !modelIDPattern.MatchString(string(spec.ID)) {
		return spec, fmt.Errorf("%w: mental model id %q must be lowercase alphanumerics and hyphens",
			domain.ErrInvalidArgument, spec.ID)
	}
	t := spec.Trigger
	if t.RefreshAfterConsolidation && strings.TrimSpace(t.RefreshCron) != "" {
		return spec, fmt.Errorf("%w: refresh_after_consolidation and refresh_cron are mutually exclusive",
			domain.ErrInvalidArgument)
	}
	if expr := strings.TrimSpace(t.RefreshCron); expr != "" {
		if _, err := cronParser().Parse(expr); err != nil {
			return spec, fmt.Errorf("%w: refresh_cron: %w", domain.ErrInvalidArgument, err)
		}
	}
	for _, g := range t.TagGroups {
		if err := g.Validate(); err != nil {
			return spec, fmt.Errorf("mentalmodel: trigger tag_groups: %w", err)
		}
	}
	spec.Tags = domain.NormalizeTags(spec.Tags)
	spec.FolderPath = normalizeFolder(spec.FolderPath)
	return spec, nil
}

// normalizeFolder trims spaces and slashes from a folder path.
func normalizeFolder(p string) string { return strings.Trim(strings.TrimSpace(p), "/") }

// scopeChanged reports whether the source query or the read scope (tags, tag groups, match, fact types)
// differs between two specs. Tags and fact types compare as sets and tag groups by value, nil equal to empty.
func scopeChanged(a, b domain.MentalModelSpec) bool {
	return a.SourceQuery != b.SourceQuery || !domain.SameTagSet(a.Tags, b.Tags) ||
		a.Trigger.TagsMatch != b.Trigger.TagsMatch || !sameTagGroups(a.Trigger.TagGroups, b.Trigger.TagGroups) ||
		!sameFactTypes(a.Trigger.FactTypes, b.Trigger.FactTypes)
}

// sameFactTypes compares two fact-type lists as sets (order and duplicates ignored, nil equal to empty).
func sameFactTypes(a, b []domain.FactType) bool {
	return slices.Equal(sortedSet(a), sortedSet(b))
}

// sortedSet returns the sorted distinct values of s.
func sortedSet[T cmp.Ordered](s []T) []T {
	out := slices.Clone(s)
	slices.Sort(out)
	return slices.Compact(out)
}

// sameTagGroups compares two tag-group lists by value, nil equal to empty.
func sameTagGroups(a, b []domain.TagGroup) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	return reflect.DeepEqual(a, b)
}
