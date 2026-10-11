package recall

import (
	"context"
	"errors"
	"memd/internal/ai"
	"memd/internal/domain"
	"memd/internal/store"
	"slices"
	"testing"
)

// addPage stores a mental model of testBank with content (embedded with e's embedder when embed is set).
func (e *env) addPage(t *testing.T, id, folder, name, content string, embed bool) {
	t.Helper()
	m := domain.MentalModel{
		Bank: testBank, Spec: domain.MentalModelSpec{ID: domain.MentalModelID(id), Name: name, FolderPath: folder},
	}
	e.tx(t, func(ctx context.Context, tx store.Tx, _ int64) error {
		if err := tx.MentalModels().Create(ctx, m); err != nil {
			return err
		}
		m.ContentMD = content
		if embed {
			m.Embedding = e.emb.Vector(name + "\n\n" + content)
		}
		return tx.MentalModels().Update(ctx, m)
	})
}

// TestSearchPages checks ranking, the folder filter, snippets, the empty query, and that a failing embedder falls
// back to term ranking; ReadPages returns pages in id order, skipping unknown ids.
func TestSearchPages(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.addPage(t, "deploy", "ops", "Deploy process", "# Deploy\n\nWe deploy with Argo every Tuesday.", true)
	e.addPage(t, "hiring", "people", "Hiring", "# Hiring\n\nInterviews take two rounds.", true)
	e.addPage(t, "oncall", "ops", "On-call", "# On-call\n\nThe on-call rotation is weekly.", true)
	ctx := context.Background()
	hits, err := e.rc.SearchPages(ctx, SearchPagesRequest{Bank: testBank, Query: "how do we deploy?"})
	if err != nil || len(hits) == 0 || hits[0].MentalModelID != "deploy" ||
		hits[0].Snippet != "We deploy with Argo every Tuesday." {
		t.Fatalf("hits %+v %v", hits, err)
	}
	hits, _ = e.rc.SearchPages(ctx, SearchPagesRequest{Bank: testBank, Query: "interviews", FolderPrefix: "ops"})
	for _, h := range hits {
		if h.FolderPath != "ops" {
			t.Fatalf("folder filter leaked %+v", h)
		}
	}
	if _, err := e.rc.SearchPages(ctx, SearchPagesRequest{Bank: testBank, Query: " ? "}); !errors.Is(err,
		domain.ErrInvalidArgument) {
		t.Fatalf("empty query: %v", err)
	}
	down := e.deps
	down.Embedder = downEmbedder{}
	hits, err = New(down).SearchPages(ctx, SearchPagesRequest{Bank: testBank, Query: "how do we deploy?"})
	if err != nil || len(hits) != 1 || hits[0].MentalModelID != "deploy" {
		t.Fatalf("embedder down: hits %+v: %v", hits, err)
	}
	pages, err := e.rc.ReadPages(ctx, testBank, []domain.MentalModelID{"oncall", "nope", "deploy"})
	var got []domain.MentalModelID
	for _, p := range pages {
		got = append(got, p.Spec.ID)
	}
	if err != nil || !slices.Equal(got, []domain.MentalModelID{"oncall", "deploy"}) {
		t.Fatalf("ReadPages = %v, %v", got, err)
	}
}

// downEmbedder is an ai.Embedder that always fails.
type downEmbedder struct{}

func (downEmbedder) EmbedDocuments(context.Context, []string) ([][]float32, error) {
	return nil, domain.ErrUnavailable
}

func (downEmbedder) EmbedQuery(context.Context, string) ([]float32, error) {
	return nil, domain.ErrUnavailable
}

var _ ai.Embedder = downEmbedder{}
