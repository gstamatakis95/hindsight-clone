package store

import (
	"memd/internal/domain"
	"time"
)

// VectorQuery is one semantic-arm search.
type VectorQuery struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Embedding is the query vector (domain.EmbeddingDim floats, search_query: prefix applied).
	Embedding []float32
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *domain.TagPredicate
	// MinSimilarity is the cosine-similarity floor (min_scores.semantic); 0 = none.
	MinSimilarity float64
	// Seq bounds the units' seq.
	Seq domain.SeqRange
	// Limit caps the hits.
	Limit int
}

// TextQuery is one keyword-arm (BM25) search.
type TextQuery struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Text is the query text.
	Text string
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *domain.TagPredicate
	// MinScore is the BM25 score floor (min_scores.keyword); 0 = none.
	MinScore float64
	// Seq bounds the units' seq.
	Seq domain.SeqRange
	// Limit caps the hits.
	Limit int
}

// TemporalQuery is one temporal-arm search: units whose occurrence (or mention) overlaps Window.
type TemporalQuery struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Window is the time window; at least one bound is set.
	Window domain.TimeWindow
	// Buckets spreads hits evenly over this many sub-windows; 0 or 1 = no bucketing.
	Buckets int
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *domain.TagPredicate
	// Embedding is the query vector for the relevance filter; empty = no filter.
	Embedding []float32
	// MinSimilarity keeps only units whose cosine similarity to Embedding is >= it (Hindsight's in-window
	// pool). The filter is on only when Embedding is set and MinSimilarity > 0; while on, units with no
	// embedding (edited, awaiting rederive) are excluded.
	MinSimilarity float64
	// Seq bounds the units' seq.
	Seq domain.SeqRange
	// Limit caps the hits.
	Limit int
}

// Hit is one search result: a unit id and its arm-specific score (cosine similarity for semantic, BM25
// for keyword, proximity for temporal). Higher is better.
type Hit struct {
	// ID is the unit.
	ID domain.UnitID
	// Score is the arm's score.
	Score float64
}

// ScopeQuery selects the live units (facts and observations) of one bank in a mental model's scope.
type ScopeQuery struct {
	// Bank is the bank searched.
	Bank domain.BankRef
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Tags filters by tags; nil = no filter.
	Tags *domain.TagPredicate
	// Seq bounds the units' seq.
	Seq domain.SeqRange
}

// ModelQuery selects mental models of one bank for listing.
type ModelQuery struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// FolderPrefix keeps models whose folder_path equals it or lies under it ("a" matches "a" and "a/b",
	// not "ab"); empty = all.
	FolderPrefix string
	// Tags filters by model tags with domain.MatchTags semantics; empty = no filter.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// RefreshAfterConsolidation keeps only models whose trigger sets refresh_after_consolidation.
	RefreshAfterConsolidation bool
	// PageSize caps the page; 0 = all.
	PageSize int
	// PageToken is the next page token of the previous page (the last listed id); empty = first page.
	PageToken string
}

// UnconsolidatedBank is a bank with facts waiting for consolidation, as the consolidation reconcile sees it.
type UnconsolidatedBank struct {
	// Bank is the bank.
	Bank domain.BankRef
	// Oldest is the created_at of its oldest fact waiting for consolidation.
	Oldest time.Time
}

// CronModel is a model with a refresh_cron schedule, as the cron sweep sees it.
type CronModel struct {
	// Model is the model.
	Model domain.MentalModel
	// LastRefreshEnqueuedAt is the created_at of the model's newest refresh_mental_model operation (any
	// status); zero when it has none.
	LastRefreshEnqueuedAt time.Time
}

// FailedRefresh is a model whose latest terminal refresh_mental_model operation is FAILED.
type FailedRefresh struct {
	// Bank owns the model.
	Bank domain.BankRef
	// ModelID is the model.
	ModelID domain.MentalModelID
	// OperationID is the failed operation.
	OperationID domain.OperationID
	// FailedAt is the operation's finished_at.
	FailedAt time.Time
	// Error is the operation's recorded error.
	Error string
}

// UnitQuery filters ListUnits. Zero fields do not filter.
type UnitQuery struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// Types limits fact types; empty = all.
	Types []domain.FactType
	// Q is a case-insensitive substring filter on the text; empty = none.
	Q string
	// DocumentID limits units to one document.
	DocumentID domain.DocumentID
	// EntityID limits units to live units attached to the entity (archived units never match).
	EntityID domain.EntityID
	// State selects live (valid), archived (invalidated) or both (unspecified) tables.
	State domain.MemoryState
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at. A unit whose field is
	// null never matches a bound on it.
	TimeField domain.TimeField
	// Tags filters by normalized tags with domain.MatchTags semantics; empty = none.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// After continues a listing: only units strictly after this cursor in the listing order; nil = first.
	After *UnitCursor
	// Limit caps the result (> 0).
	Limit int
}

// UnitCursor is a keyset position in a unit listing.
type UnitCursor struct {
	// CreatedAt is the last listed unit's created_at.
	CreatedAt time.Time `json:"c"`
	// ID is the last listed unit's id.
	ID domain.UnitID `json:"i"`
}

// DocumentQuery filters ListDocuments. Zero fields do not filter.
type DocumentQuery struct {
	// Bank is the bank listed.
	Bank domain.BankRef
	// Q is a case-insensitive substring filter on the document id.
	Q string
	// Tags filters by normalized document tags with domain.MatchTags semantics; empty = none.
	Tags []string
	// TagsMatch is how Tags compare.
	TagsMatch domain.TagsMatch
	// Start is the inclusive lower bound on TimeField; nil = unbounded.
	Start *time.Time
	// End is the exclusive upper bound on TimeField; nil = unbounded.
	End *time.Time
	// TimeField is the timestamp Start and End filter on; unspecified = created_at.
	TimeField domain.DocTimeField
	// After continues a listing: only documents strictly after this cursor; nil = first page.
	After *DocumentCursor
	// Limit caps the result (> 0).
	Limit int
}

// DocumentCursor is a keyset position in a document listing.
type DocumentCursor struct {
	// CreatedAt is the last listed document's created_at.
	CreatedAt time.Time `json:"c"`
	// ID is the last listed document's id.
	ID domain.DocumentID `json:"i"`
}
