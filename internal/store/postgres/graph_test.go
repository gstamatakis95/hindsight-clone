package postgres_test

import (
	"memd/internal/graph/graphtest"
	"memd/internal/store/postgres/pgtest"
	"testing"
)

// TestGraph runs the graph contract suite against memory_links: links written through Tx.Graph, read through
// DB.Graph.
func TestGraph(t *testing.T) {
	t.Parallel()
	graphtest.Run(t, func(t *testing.T) graphtest.Fixture {
		s := pgtest.NewStore(t)
		return graphtest.Fixture{DB: s, Graph: s.Graph()}
	})
}
