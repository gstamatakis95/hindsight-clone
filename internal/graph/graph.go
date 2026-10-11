package graph

import (
	"context"
	"memd/internal/domain"
	"time"
)

// Kind names an edge type in the memory graph.
type Kind string

// Link kinds.
const (
	// KindTemporal joins units close in time (capped by retain.temporal_link_cap per unit).
	KindTemporal Kind = "temporal"
	// KindSemantic joins units whose embeddings are close (capped by retain.semantic_link_k per unit).
	KindSemantic Kind = "semantic"
	// KindCausal joins a cause to its effect, extracted from one batch.
	KindCausal Kind = "causal"
)

// Link is a directed, weighted edge between two live units of one bank.
type Link struct {
	// Bank owns both units.
	Bank domain.BankRef
	// From is the source unit.
	From domain.UnitID
	// To is the target unit.
	To domain.UnitID
	// Kind is the edge type.
	Kind Kind
	// Weight is the edge weight (similarity, proximity or confidence) in [0,1].
	Weight float64
	// CreatedAt is the creation time; zero = now.
	CreatedAt time.Time
}

// Query is one expansion from seed units over the bank's links: the recall graph arm.
type Query struct {
	// Bank is the bank searched; every link and reached unit belongs to it.
	Bank domain.BankRef
	// Seeds are the starting units (never hits).
	Seeds []domain.UnitID
	// Kinds limits the link kinds followed at every step; empty = all.
	Kinds []Kind
	// MaxHops bounds the expansion; it is clamped to 1-2.
	MaxHops int
	// Limit caps the hits; <= 0 returns no hits.
	Limit int
	// Types is a filter hint: the fact types of the reached units; empty = all.
	Types []domain.FactType
	// Tags is a filter hint: the reached units' tags must satisfy it; nil = no filter.
	Tags *domain.TagPredicate
	// Seq is a filter hint: the reached units' seq must lie in it.
	Seq domain.SeqRange
}

// Hit is one reached unit and its path score: the best (max) product of link weights over the paths reaching it.
// Higher is better.
type Hit struct {
	// ID is the reached unit.
	ID domain.UnitID
	// Score is the path score.
	Score float64
}

// Reader reads the memory graph.
type Reader interface {
	// Expand walks q.Bank's links forward (from -> to) from q.Seeds, at most q.MaxHops hops, following only
	// q.Kinds, and returns up to q.Limit reached units, best score first (ties by id). Seeds are never hits, and
	// only live units of the bank are reached. The Types, Tags and Seq filters are hints the adapter MAY apply
	// before the limit; callers always re-check the hits against the store (GetUnits plus the same filters), and
	// the graph may lag the store, because the graph arm is one of several fused recall arms.
	Expand(ctx context.Context, q Query) ([]Hit, error)
	// Degree returns how many links of kind touch unit in either direction (the linker's per-unit caps). Inside a
	// transaction it sees the transaction's own writes.
	Degree(ctx context.Context, unit domain.UnitID, kind Kind) (int, error)
}

// Writer changes the memory graph.
type Writer interface {
	// AddLinks stores links; an existing (from, to, kind) edge is kept with the new weight, and a self-link is
	// skipped.
	AddLinks(ctx context.Context, ls []Link) error
	// DeleteUnitLinks removes the links of the given kinds (empty = all) touching unit in either direction, so
	// rederive can rebuild them, and returns how many it removed.
	DeleteUnitLinks(ctx context.Context, unit domain.UnitID, kinds []Kind) (int, error)
}

// Tx is the graph bound to one store transaction (store.Tx.Graph): its writes commit or roll back with the
// transaction, and its reads see them.
type Tx interface {
	Reader
	Writer
}
