package linking

import (
	"context"
	"memd/internal/domain"
	"memd/internal/fake"
	"memd/internal/store/postgres"
	"memd/internal/store/postgres/pgtest"
	"testing"
)

// testBank is the bank every linking test writes to.
var testBank = domain.BankRef{Tenant: "acme", Bank: "notes"}

// env is one test's store on its own database and the bag-of-words embedder the fixtures embed with.
type env struct {
	t     *testing.T
	ctx   context.Context
	store *postgres.DB
	emb   *fake.Embedder
}

// newEnv returns an env over a fresh database.
func newEnv(t *testing.T) *env {
	t.Helper()
	return &env{t: t, ctx: context.Background(), store: pgtest.NewStore(t), emb: fake.NewEmbedder(fake.EmbedSimilar)}
}

// ptr returns a pointer to v.
func ptr[T any](v T) *T { return &v }
