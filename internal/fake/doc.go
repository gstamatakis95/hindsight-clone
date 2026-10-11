// Package fake provides scripted, deterministic stand-ins for the AI boundary (ai.LLM, ai.Embedder, ai.Reranker) and
// for blob.Store, for service, library, job and cmd/memd tests. There is no fake store.DB: code that touches the store
// is tested against real Postgres (internal/store/postgres/pgtest). The test clock is clock.Manual.
//
// # Where it sits
//
// Of memd's packages fake imports only internal/domain, internal/ai, internal/blob and internal/jsonschema, and it
// never imports an adapter, pgx, the S3 SDK or net/http. It is a regular (non-test) package so any package's tests can
// import it; production code must not. Fakes return concrete types (*Blob, *LLM, ...) that tests pass wherever the
// interface is expected.
//
// # Quick start
//
//	llm := fake.NewLLM()                         // ai.LLM: script replies per (slot, prompt name)
//	llm.RespondJSON(domain.ModelSlotExtract, "extract_facts", `{"facts":[]}`)
//	emb := fake.NewEmbedder(fake.EmbedSimilar)   // ai.Embedder: 768-dim, deterministic
//	rr := fake.NewReranker()                     // ai.Reranker: token overlap unless scripted
//	bs := fake.NewBlob()                         // blob.Store
//
// # Model fakes
//
// LLM.Structured validates req.Schema (internal/jsonschema, the gateway's validator) and the scripted output against
// it, exactly like the gateway: script replies that match the engine's schema. LLM replies are FIFO per (slot, prompt
// name) and the last one repeats; name "" is a per-slot fallback. Unscripted calls fail with ErrNotScripted. Timeout
// (ErrTimeout, which wraps domain.ErrUnavailable and context.DeadlineExceeded), Fail and InvalidJSON script failures;
// Calls and CallsFor return what was sent. Embedder in EmbedHash mode gives unrelated vectors per text; EmbedSimilar
// makes texts that share words close (cosine) and texts that share none orthogonal. No nomic prefixes are applied, so
// EmbedQuery(x) equals EmbedDocuments([x])[0] and Vector(x). Reranker scores are scripted per (query, doc) or per doc,
// else token overlap, with the adapter's sigmoid rule for out-of-range scores. Blob, Embedder and Reranker have
// FailWith to make every later call fail with a given error. Every fake is safe for concurrent use.
package fake
