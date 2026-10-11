package mentalmodel

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/jobs"
	"memd/internal/recall"
	"memd/internal/store"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// modelIDPattern is a valid model id: lowercase alphanumerics and hyphens.
var modelIDPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ListRequest asks for one page of a bank's mental models (ListMentalModels).
type ListRequest struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// FolderPrefix limits models to a folder subtree; empty = all.
	FolderPrefix string
	// View is how much of each model to load.
	View domain.MentalModelView
	// Tags filters by the models' own tags, flat form only; empty tags = none, unset match = any.
	Tags domain.TagFilter
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// ModelPage is one page of mental models.
type ModelPage struct {
	// Items are the page's models.
	Items []domain.MentalModel
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// HistoryRequest asks for one page of a model's history (ListMentalModelHistory).
type HistoryRequest struct {
	// Bank owns the model.
	Bank domain.BankRef
	// ID is the model.
	ID domain.MentalModelID
	// Kind limits entries to one kind; unspecified = all.
	Kind domain.HistoryKind
	// PageSize caps the page (domain.ListPage); 0 = the default.
	PageSize int
	// PageToken continues a previous listing; empty = first page.
	PageToken string
}

// HistoryPage is one page of a model's history.
type HistoryPage struct {
	// Items are the page's entries, newest first.
	Items []domain.MentalModelHistoryEntry
	// NextPageToken continues the listing; empty = last page.
	NextPageToken string
}

// SpecPatch computes a new spec from the current one (UpdateMentalModel). It must be pure: it runs again when
// the transaction retries.
type SpecPatch func(cur domain.MentalModelSpec) (domain.MentalModelSpec, error)

// CreateMentalModel validates and stores spec (bank created on first write) and queues the first build as
// a manual, full refresh, in one transaction.
func (s *Service) CreateMentalModel(
	ctx context.Context, b domain.BankRef, spec domain.MentalModelSpec,
) (domain.MentalModel, domain.Operation, error) {
	if spec.ID == "" {
		spec.ID = domain.MentalModelID(uuid.NewString())
	}
	spec, err := normalizeSpec(spec)
	if err != nil {
		return domain.MentalModel{}, domain.Operation{}, err
	}
	var m domain.MentalModel
	var op domain.Operation
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
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
		id, _, err := jobs.EnqueueRefresh(ctx, tx, req.job(), s.d.Clock.Now())
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

// GetMentalModel reads the model, computes stale and trims it to view (unspecified = full).
func (s *Service) GetMentalModel(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID, view domain.MentalModelView,
) (domain.MentalModel, error) {
	m, err := loadModel(ctx, s.d.DB, b, id)
	if err != nil {
		return domain.MentalModel{}, err
	}
	if view == domain.MentalModelViewUnspecified {
		view = domain.MentalModelViewFull
	}
	return s.present(ctx, m, view)
}

// ListMentalModels returns one page of the bank's models ordered by id, in view (unspecified = basic). The
// page size follows domain.ListPage and the tag filter must be flat (domain.ErrInvalidArgument otherwise).
func (s *Service) ListMentalModels(ctx context.Context, req ListRequest) (ModelPage, error) {
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return ModelPage{}, fmt.Errorf("mentalmodel: list: %w", err)
	}
	if err := req.Tags.RequireFlat(); err != nil {
		return ModelPage{}, fmt.Errorf("mentalmodel: list: %w", err)
	}
	items, next, err := s.d.DB.Read().ListMentalModels(ctx, store.ModelQuery{
		Bank: req.Bank, FolderPrefix: normalizeFolder(req.FolderPrefix), Tags: domain.NormalizeTags(req.Tags.Tags),
		TagsMatch: req.Tags.Match, PageSize: size, PageToken: req.PageToken,
	})
	if err != nil {
		return ModelPage{}, fmt.Errorf("mentalmodel: list: %w", err)
	}
	view := req.View
	if view == domain.MentalModelViewUnspecified {
		view = domain.MentalModelViewBasic
	}
	for i := range items {
		if items[i], err = s.present(ctx, items[i], view); err != nil {
			return ModelPage{}, err
		}
	}
	return ModelPage{Items: items, NextPageToken: next}, nil
}

// UpdateMentalModel applies patch in one transaction. A changed source query or scope resets
// last_seq_seen, so the next refresh runs in full (mode_fallback_reason source_query_changed).
func (s *Service) UpdateMentalModel(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID, patch SpecPatch,
) (domain.MentalModel, error) {
	var m domain.MentalModel
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
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
	return s.present(ctx, m, domain.MentalModelViewFull)
}

// RefreshMentalModel queues a manual refresh of an existing model, or joins and upgrades the queued one.
func (s *Service) RefreshMentalModel(ctx context.Context, req RefreshRequest) (domain.Operation, bool, error) {
	req.Manual = true
	var op domain.Operation
	var joined bool
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error {
		if _, err := tx.MentalModels().Get(ctx, req.Bank, req.ID); err != nil {
			return err
		}
		if _, err := tx.Seq(ctx, req.Bank); err != nil {
			return err
		}
		id, j, err := jobs.EnqueueRefresh(ctx, tx, req.job(), s.d.Clock.Now())
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

// ListMentalModelHistory returns one page of the model's history, newest first, paged with an offset token; the
// page size follows domain.ListPage.
func (s *Service) ListMentalModelHistory(ctx context.Context, req HistoryRequest) (HistoryPage, error) {
	offset := 0
	if req.PageToken != "" {
		n, err := strconv.Atoi(req.PageToken)
		if err != nil || n < 0 {
			return HistoryPage{}, domain.InvalidField("page_token", "bad page token")
		}
		offset = n
	}
	size, err := domain.ListPage.Size(req.PageSize)
	if err != nil {
		return HistoryPage{}, fmt.Errorf("mentalmodel: history: %w", err)
	}
	var entries []domain.MentalModelHistoryEntry
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
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
func (s *Service) DeleteMentalModel(ctx context.Context, b domain.BankRef, id domain.MentalModelID) error {
	err := s.d.DB.InTx(ctx, func(tx store.Tx) error { return tx.MentalModels().Delete(ctx, b, id) })
	if err != nil {
		return fmt.Errorf("mentalmodel: delete %s: %w", id, err)
	}
	return nil
}

// DeleteMentalModelFolder deletes every model whose folder is path or lies under it and returns how many. An
// empty path is rejected so a typo can't delete every page.
func (s *Service) DeleteMentalModelFolder(ctx context.Context, b domain.BankRef, path string) (int, error) {
	path = normalizeFolder(path)
	if path == "" {
		return 0, fmt.Errorf("%w: folder_path is required", domain.ErrInvalidArgument)
	}
	models, err := listAll(ctx, s.d.DB.Read(), store.ModelQuery{Bank: b, FolderPrefix: path})
	if err != nil {
		return 0, err
	}
	n := 0
	err = s.d.DB.InTx(ctx, func(tx store.Tx) error {
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

// present computes stale (full view only; other views leave it false) and trims m to view.
func (s *Service) present(
	ctx context.Context, m domain.MentalModel, view domain.MentalModelView,
) (domain.MentalModel, error) {
	m.Stale = false
	if view == domain.MentalModelViewFull {
		cfg, err := s.stalenessConfig(ctx, m)
		if err != nil {
			return domain.MentalModel{}, err
		}
		stale, _, err := s.needsRefresh(ctx, m, cfg)
		if err != nil {
			return domain.MentalModel{}, fmt.Errorf("mentalmodel: staleness of %s: %w", m.Spec.ID, err)
		}
		m.Stale = stale
	}
	m.Embedding = nil
	switch view {
	case domain.MentalModelViewBasic:
		m.ContentMD, m.StructuredOutputJSON, m.LastTrace = "", nil, nil
	case domain.MentalModelViewContent:
		m.LastTrace = nil
	}
	return m, nil
}

// lockedModel takes tx.Seq(b) first, which locks the bank row and so serializes every read-modify-write
// of a model row (runner write, reconcile, update), then reads the model.
func lockedModel(
	ctx context.Context, tx store.Tx, b domain.BankRef, id domain.MentalModelID,
) (domain.MentalModel, error) {
	if _, err := tx.Seq(ctx, b); err != nil {
		return domain.MentalModel{}, err
	}
	return tx.MentalModels().Get(ctx, b, id)
}

// stalenessConfig is the config needsRefresh reads for m on a model read: the model's config when its scope has
// a fuzzy leaf (the fuzzy floor matters), else the zero config, so a plain read resolves no config.
func (s *Service) stalenessConfig(ctx context.Context, m domain.MentalModel) (domain.BankConfig, error) {
	if !hasFuzzyLeaf(resolveScope(m).Tags.Groups) {
		return domain.BankConfig{}, nil
	}
	return s.modelConfig(ctx, m)
}

// normalizeSpec validates spec and normalizes its tags and folder path.
func normalizeSpec(spec domain.MentalModelSpec) (domain.MentalModelSpec, error) {
	if !modelIDPattern.MatchString(string(spec.ID)) {
		return spec, fmt.Errorf("%w: mental model id %q must be lowercase alphanumerics and hyphens",
			domain.ErrInvalidArgument, spec.ID)
	}
	if expr := strings.TrimSpace(spec.Schedule.Cron); expr != "" {
		if _, err := parseCron(expr); err != nil {
			return spec, domain.InvalidField("spec.schedule.cron", "%v", err)
		}
	}
	scope := spec.Scope.Tags
	if len(scope.Tags) > 0 {
		return spec, domain.InvalidField("spec.scope.tags.tags",
			"scope tags.tags must be empty: a model reads with its own tags (spec.tags)")
	}
	if err := scope.Validate(); err != nil {
		return spec, fmt.Errorf("mentalmodel: scope tags: %w", err)
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
		a.Scope.Tags.Match != b.Scope.Tags.Match || !sameTagGroups(a.Scope.Tags.Groups, b.Scope.Tags.Groups) ||
		!sameFactTypes(a.Scope.FactTypes, b.Scope.FactTypes)
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

// PreviewMentalModelRefresh runs the refresh pipeline for model id synchronously as a manual run and writes
// nothing at all (no version, no usage, no seq bump); strategy overrides the model's (unspecified = the model's).
// The outcome carries the draft, its diff against the stored content and whether a real run would write it.
func (s *Service) PreviewMentalModelRefresh(
	ctx context.Context, b domain.BankRef, id domain.MentalModelID, strategy domain.RefreshStrategy,
) (RefreshOutcome, error) {
	return s.refresh(ctx, b, id, refreshOptions{Manual: true, Preview: true, Strategy: strategy})
}

// SearchMentalModels ranks the bank's pages by fused full-text and semantic scores (recall.Engine.SearchPages),
// returning at most req.Limit hits under domain.SearchResults (0 = 10, capped at 100, negative =
// domain.ErrInvalidArgument).
func (s *Service) SearchMentalModels(ctx context.Context, req recall.SearchPagesRequest) ([]domain.PageHit, error) {
	limit, err := domain.SearchResults.Size(req.Limit)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: search: %w", err)
	}
	req.Limit = limit
	hits, err := s.d.Recall.SearchPages(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("mentalmodel: search: %w", err)
	}
	return hits, nil
}
