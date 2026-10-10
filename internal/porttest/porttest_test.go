package porttest

import (
	"math"
	"memd/internal/domain"
	"memd/internal/port"
	"regexp"
	"testing"
)

// Compile-time guards: the Run* functions and factories keep the signatures implementations call.
var (
	_ func(*testing.T, StoreFactory) = RunStoreSuite
	_ func(*testing.T, StoreFactory) = RunSeqSuite
	_ func(*testing.T, QueueFactory) = RunQueueSuite
	_ func(*testing.T, BlobFactory)  = RunBlobSuite

	_ StoreFactory = func(*testing.T) port.Store { return nil }
	_ QueueFactory = func(*testing.T) (port.Store, port.Claimer) { return nil, nil }
	_ BlobFactory  = func(*testing.T) port.Blob { return nil }
)

// The suites run from the implementations' own packages. These placeholders name the work package that
// wires each (suite, implementation) pair, so `go test -v` shows what is still pending.

// TestStoreSuitePostgres is pending WP-01.
func TestStoreSuitePostgres(t *testing.T) {
	t.Skip("pending WP-01: internal/adapter/postgres runs RunStoreSuite on testcontainers")
}

// TestStoreSuiteFake is pending WP-02.
func TestStoreSuiteFake(t *testing.T) {
	t.Skip("pending WP-02: internal/fake runs RunStoreSuite")
}

// TestSeqSuitePostgres is pending WP-01.
func TestSeqSuitePostgres(t *testing.T) {
	t.Skip("pending WP-01: internal/adapter/postgres runs RunSeqSuite on testcontainers")
}

// TestSeqSuiteFake is pending WP-02.
func TestSeqSuiteFake(t *testing.T) {
	t.Skip("pending WP-02: internal/fake runs RunSeqSuite")
}

// TestQueueSuitePostgres is pending WP-01.
func TestQueueSuitePostgres(t *testing.T) {
	t.Skip("pending WP-01: internal/adapter/postgres runs RunQueueSuite on testcontainers")
}

// TestQueueSuiteFake is pending WP-02.
func TestQueueSuiteFake(t *testing.T) {
	t.Skip("pending WP-02: internal/fake runs RunQueueSuite")
}

// TestBlobSuiteS3 is pending WP-03.
func TestBlobSuiteS3(t *testing.T) {
	t.Skip("pending WP-03: internal/adapter/s3 runs RunBlobSuite on MinIO")
}

// TestBlobSuiteFake is pending WP-02.
func TestBlobSuiteFake(t *testing.T) {
	t.Skip("pending WP-02: internal/fake runs RunBlobSuite")
}

// TestNewUUID checks the id helper produces distinct canonical version-4 UUIDs (the uuid columns reject
// anything else).
func TestNewUUID(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	seen := map[string]bool{}
	for range 100 {
		id := newUUID()
		if !re.MatchString(id) {
			t.Fatalf("newUUID() = %q, not a canonical v4 UUID", id)
		}
		if seen[id] {
			t.Fatalf("newUUID() repeated %q", id)
		}
		seen[id] = true
	}
}

// TestAngleVec checks the embedding helper: EmbeddingDim unit vectors whose cosine to angleVec(0) is cos(d).
func TestAngleVec(t *testing.T) {
	for _, deg := range []float64{0, 30, 60, 90, 120} {
		v, q := angleVec(deg), angleVec(0)
		if len(v) != domain.EmbeddingDim {
			t.Fatalf("angleVec(%v) has %d dims, want %d", deg, len(v), domain.EmbeddingDim)
		}
		var dot, norm float64
		for i := range v {
			dot += float64(v[i]) * float64(q[i])
			norm += float64(v[i]) * float64(v[i])
		}
		if math.Abs(norm-1) > 1e-6 || math.Abs(dot-cosDeg(deg)) > 1e-6 {
			t.Errorf("angleVec(%v): norm %v, cosine %v; want 1, %v", deg, norm, dot, cosDeg(deg))
		}
	}
}

// TestTagPredicatesDiscriminate checks that the SearchVector tag fixtures exercise the predicates: across the
// five tag sets, strict and non-strict modes differ and no predicate is trivially all-or-nothing by accident.
func TestTagPredicatesDiscriminate(t *testing.T) {
	tagSets := [][]string{{}, {"alpha"}, {"beta"}, {"alpha", "beta"}, {"gamma"}}
	count := func(p *port.SQLPredicate) int {
		n := 0
		for _, tags := range tagSets {
			if p.Eval(tags) {
				n++
			}
		}
		return n
	}
	want := map[string]int{
		"nil": 5, "any alpha": 3, "any_strict alpha": 2, "all alpha beta": 2, "all_strict alpha beta": 1,
		"exact empty": 1, "exact beta": 1, "alpha and not beta": 1, "exact gamma or all_strict alpha beta": 2,
		"not any_strict alpha": 3, "empty and": 5, "empty or": 0, "or(alpha, beta) and not exact alpha beta": 2,
	}
	for _, c := range tagPredicates() {
		if got := count(c.p); got != want[c.name] {
			t.Errorf("predicate %q matches %d of the fixture tag sets, want %d", c.name, got, want[c.name])
		}
	}
}
