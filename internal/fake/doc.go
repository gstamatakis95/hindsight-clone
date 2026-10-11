// Package fake provides scripted, deterministic stand-ins for the model ports (LLM, Embedder, Reranker), the Blob
// port and the Clock, for engine, worker and cmd/memd tests. There is no fake Store: code that touches the store is
// tested against real Postgres (internal/adapter/postgres/pgtest).
//
// # Architecture
//
// fake implements internal/port (domain -> port -> engine/adapter -> server/worker -> cmd/memd/wire.go); of memd's
// packages it imports only internal/domain, internal/port and internal/jsonschema, and it never imports an adapter,
// pgx, the S3 SDK or net/http. It is a regular (non-test) package so any package's tests can import it; production code
// must not. Fakes return concrete types (*Blob, *LLM, ...) that tests pass wherever a port is expected.
//
// # Quick start
//
//	clock := fake.NewClock(time.Time{})          // frozen at DefaultClockStart; Advance / Set it
//	llm := fake.NewLLM()                         // port.LLM: script replies per (slot, prompt name)
//	llm.RespondJSON(domain.ModelSlotExtract, "extract_facts", `{"facts":[]}`)
//	emb := fake.NewEmbedder(fake.EmbedSimilar)   // port.Embedder: 768-dim, deterministic
//	rr := fake.NewReranker()                     // port.Reranker: token overlap unless scripted
//	blob := fake.NewBlob()                       // port.Blob
//
// A Clock only drives the code it is passed to: Postgres judges due-ness and heartbeat staleness by its own clock, so
// a test that needs a parked job to become due moves the job's run_at (or the clock relative to it) with SQL.
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
