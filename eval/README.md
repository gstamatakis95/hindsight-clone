# memd eval

`eval` runs benchmarks against memd over gRPC, optionally side by side with stock Hindsight over HTTP. It writes each
run to its own directory: per-category report, per-question results, run metadata. Runs can be resumed after a crash
and compared with each other.

| Benchmark | Data | Default mode | What it measures |
|---|---|---|---|
| `locomo` | `locomo10.json`, downloaded and sha256-checked | `rag` | LoCoMo, scored like Hindsight's published numbers (see [LoCoMo methodology](#locomo-methodology)) |
| `longmemeval` | `longmemeval_s_cleaned.json` (277 MB), downloaded | `reflect` | LongMemEval S, LongMemEval's own judge prompts |
| `longmemeval-oracle` | `longmemeval_oracle.json` (15 MB), downloaded | `reflect` | The same questions over their evidence sessions only |
| `golden` | `eval/golden/*.yaml`, built in | `recall` | Retained facts can be recalled; substring judge, exits 1 on any failure (CI gate) |

`go run ./eval/cmd/eval list` prints the registered benchmarks.

## Quick start

```sh
# No server, no model: the golden fixtures against the in-process stub (a harness smoke test).
make bench BENCH=golden ARGS=-stub

# LoCoMo against a local memd, with the example config (edit the gateway URL and models first).
export MEMD_EVAL_JUDGE_API_KEY=...
make bench CONFIG=eval/configs/locomo.yaml
make bench CONFIG=eval/configs/locomo.yaml ARGS='-limit 1 -questions 20'   # a cheap subset first

make bench-resume RUN=eval-results/locomo-20261011-101500                  # after a crash or Ctrl-C
make bench-compare A=eval-results/locomo-<before> B=eval-results/locomo-<after>
```

The `make` targets wrap `go run ./eval/cmd/eval run|compare|list`. Run them from the repo root.

## Running LoCoMo against a real AI gateway

memd needs Postgres and MinIO, and an OpenAI-compatible gateway that serves a chat model for each LLM slot, a reranker
and a 768-dimension embedding model. The eval's answer and judge models use the same kind of gateway (it can be the
same one).

```sh
make up        # Postgres + MinIO (docker compose), creates the memd bucket
make migrate   # schema

# memd's models: MEMD_MODEL binds every LLM slot (EXTRACT, TIME_PARSE, CONSOLIDATE, DEDUP_CHECK, REFLECT,
# STRUCTURED_OUTPUT, MENTAL_MODEL_DELTA, SYNTHESIZE); MEMD_MODEL_<SLOT> overrides one slot. RERANK is bound
# only by MEMD_MODEL_RERANK (without it recall skips the cross-encoder). The embedding model must return 768
# dimensions; memd checks this at startup.
export MEMD_GATEWAY_URL=https://gateway.example/v1 MEMD_GATEWAY_API_KEY=... \
  MEMD_MODEL=gemini-2.5-flash-lite \
  MEMD_MODEL_RERANK=<your reranker> MEMD_EMBED_MODEL=nomic-embed-text-v1.5 \
  MEMD_GATEWAY_MAX_CONCURRENT=32 \
  MEMD_S3_ENDPOINT=http://localhost:59000 MEMD_S3_BUCKET=memd MEMD_S3_ACCESS_KEY=memd \
  MEMD_S3_SECRET_KEY=memd-secret MEMD_S3_PATH_STYLE=true
make run       # memd all on :50051 (go run ./cmd/memd lists every setting)

# In another shell: the eval's answer and judge models. Keys come only from the environment.
export MEMD_EVAL_JUDGE_API_KEY=...            # MEMD_EVAL_ANSWER_API_KEY too, if the answer model needs another
make bench CONFIG=eval/configs/locomo.yaml    # set judge.url, judge.model and answer.model in the file first
```

Lower `MEMD_GATEWAY_MAX_CONCURRENT` (or one slot's `MEMD_GATEWAY_MAX_CONCURRENT_<SLOT>`) if the gateway rate-limits.
The first run downloads `locomo10.json` into `~/.cache/memd-eval/locomo/` (`data_dir` / `-data-dir` to change it).
A full run is 10 conversations, 272 sessions and 1540 questions.

## How a run works

For each conversation (up to `parallel` at once) and each target, the runner:
1. Creates one bank, `<bank prefix><conversation id>`.
2. Retains each session with one synchronous `Retain`, in session order. The session date is the timestamp and the
   session id is the `document_id`.
3. Waits at the barrier. It polls `GetBankStats` until `pending_operations_by_kind` is empty on `quiet_polls` polls in
   a row, so consolidation and other background jobs have finished. It gives up after `settle_timeout`.
4. Asks each question in the run's mode:
   - `rag`: calls `Recall`, has the eval's answer model answer from the recalled memories (rendered as JSON), and grades
     that answer. An empty recall counts as wrong, with no answer or judge call.
   - `reflect`: calls `Reflect` and grades memd's own answer.
   - `recall`: calls `Recall` and grades whether the recalled memories contain the answer.
5. Grades with the judge: the LLM judge through the gateway, or the substring judge (`golden`).
6. Reads the bank's operations for background token usage and the number of failed jobs.

A conversation that fails to retain or settle marks its questions as errors, and the run continues.

## LoCoMo methodology

Hindsight's published LoCoMo numbers come from its Agent Memory Benchmark (AMB, github.com/vectorize-io/agent-memory-benchmark,
read at commit d97d4960). AMB runs LoCoMo in its default `rag` mode against Hindsight's HTTP API: one recall per
question, an answer model on the benchmark side, then an LLM judge. `locomo` reproduces that by default.

| | Hindsight (AMB) | memd eval (`locomo`) |
|---|---|---|
| Data | `locomo10.json` from snap-research/locomo | Same file, pinned to commit `cbfbc1db`, sha256 `79fa87e9…` |
| Questions | Categories 1-4; adversarial (5) excluded: 1540 | Same |
| Bank | One per conversation | Same |
| Session document | One per session, id `<sample>_session_N`; content `json.dumps` of the turns; context `Conversation between A and B (session_N of <sample>)`; timestamp from `session_N_date_time` | Same id, context and timestamp; content is the turns as compact JSON (same fields, no spaces after separators, UTF-8 instead of `\u` escapes) |
| Ingest | Async retain batches, then wait until no operation is in flight | Sync `Retain` per session, then the barrier |
| Answer | `rag`: one recall with `budget: high`, `max_tokens: 32768`, source chunks up to 16384 tokens, entities off, query time set; the answer model gets the whole recall response as JSON in AMB's LoCoMo prompt and returns `{reasoning, answer}` | Same recall settings on memd's `Recall` (`BUDGET_HIGH`, `max_tokens`, `include.chunks`); the recall result as JSON with Hindsight's field names; the same prompt, text for text; JSON reply requested in the prompt |
| Query time | Date of the last session key in **string** order (`session_9` sorts after `session_19`), i.e. session 9 for every locomo10 conversation, not the final session | Same, on purpose, so the "current date" the answer model sees matches |
| Empty recall | Wrong, not judged | Same |
| Judge | AMB's LoCoMo prompt (generous on wording and date formats), `{correct, reason}` | Same prompt, text for text; JSON reply requested in the prompt |
| Models | Judge default `gemini-2.5-flash-lite`, temperature 0. The answer model defaults to Groq `openai/gpt-oss-120b` in AMB's code, while Hindsight's CI notes say Gemini answers and judges | Whatever you configure (`judge.model`, `answer.model`), temperature 0. To compare with a published number, use the same answer and judge models |
| Score | Correct / all 1540 | Correct / all graded (errors count as wrong) |

Deliberate differences:
- **Session order.** AMB lists session keys in string order. memd retains in session-number order, which is
  chronological, so consolidation sees the conversation in order. Hindsight ingests asynchronously and dates every
  item, so order does not matter much there.
- **Session JSON whitespace.** The JSON is compact and keeps non-ASCII characters as UTF-8. Python's `json.dumps`
  adds spaces and escapes non-ASCII. Both carry the same content.
- **Structured output.** AMB uses the model provider's structured-output schema. The eval asks for the same JSON
  object in the prompt instead, since not every gateway supports schemas, and falls back to the raw reply.
- **Abbreviated months.** All 272 locomo10 session dates use full month names. memd also parses abbreviated months
  ("22 Oct, 2023") for session timestamps. For the query time it accepts only full names, like AMB.

### Categories

The dataset does not name its categories, and evals disagree on the names. The numbers are what the dataset
defines, so memd keeps the number in every bucket name. Compare per category by number.

| Number | Questions | Evidence turns per question (mean; share with more than one) | memd bucket | AMB label |
|---|---|---|---|---|
| 1 | 282 | 3.13; 98% | `1-multi-hop` | single-hop |
| 2 | 321 | 1.17; 12% | `2-temporal` | temporal |
| 3 | 96 | 2.08; 49% (inference: "Would Caroline likely…") | `3-open-domain` | multi-hop |
| 4 | 841 | 1.07; 5% | `4-single-hop` | open-domain |
| 5 | 446 | adversarial, no answer | excluded | excluded |

memd's names follow the questions themselves, checked against `locomo10.json`. Category 1 questions cite several
turns, category 4 questions cite one, and category 3 questions ask for inference beyond the conversation. The LoCoMo
paper's question types are single-hop, multi-hop, temporal, open-domain knowledge and adversarial, but the file never
says which number is which. AMB labels 1 as single-hop, 3 as multi-hop and 4 as open-domain, so AMB's "single-hop"
score is memd's `1-multi-hop`. Overall accuracy is unaffected either way.

## Run directories

Each run writes `eval-results/<benchmark>-<UTC time>/` (`out_dir` / `-out-dir`):

| File | Contents |
|---|---|
| `config.yaml` | The resolved config (no API keys); `-resume` reads it back |
| `meta.json` | The checkout's git commit and dirty flag, benchmark, dataset path, URL and sha256, mode, judge and answer models, recall settings, bank prefix, each target with the models it reports per slot (memd: `GetBankConfig`), accuracy, pending questions and token usage, start, end and resume times, `complete` |
| `results.jsonl` | One line per answered question: target, conversation, id, category, query, gold, response, reasoning (rag), number of memories, verdict, judge reason, error, latency per operation (ms), token usage, time |
| `progress.jsonl` | One line per retained session, settled bank, background usage read, or conversation failure |
| `report.json`, `report.md` | Accuracy per category, token usage per slot, p50/p95 latency per operation, problems; the Markdown is also printed |

The banks stay on the server after a run, so you can inspect them. Each new run gets fresh banks: the default bank
prefix is `eval-<unix time>-`.

### Resume

`eval run -resume <run dir>` (`make bench-resume RUN=<run dir>`) finishes an interrupted run. It reuses the run's
config, banks and journal and skips completed work:
- Sessions already retained are not retained again, so their extraction is not paid for twice.
- Banks that passed the barrier are not polled again.
- Questions with a result are not asked again. Questions that failed with an error are retried.

Flags may change operational settings (`-parallel`, `-call-timeout`, barrier timings). They may not change what
the run measures: benchmark, mode, bank prefix, judge and answer models, or dataset. A resumed run reports the same as
an uninterrupted one. Token usage counts every attempt, since it was spent.

### Compare

`eval compare <run A> <run B>` (`make bench-compare A=… B=…`) takes run directories or `report.json` paths. It prints,
per target the runs share:
- accuracy per category with the delta in percentage points
- total tokens per slot, with the relative change
- p50/p95 latency per operation, with the relative change

It first warns about anything other than the system under test that differs: benchmark, mode, dataset sha256, judge
or answer model, or an incomplete run.

## Configuration

A YAML file (`-config`) holds the run's settings. Flags with the same meaning override it. Precedence, lowest
first:
1. Built-in defaults, including `MEMD_EVAL_JUDGE_URL`, `MEMD_EVAL_JUDGE_MODEL`, `MEMD_EVAL_ANSWER_URL` and
   `MEMD_EVAL_ANSWER_MODEL`.
2. The benchmark's defaults.
3. The file.
4. The flags.

API keys come only from `MEMD_EVAL_JUDGE_API_KEY`, `MEMD_EVAL_ANSWER_API_KEY` (falls back to the judge's) and
`MEMD_EVAL_HINDSIGHT_API_KEY`. They are never written to disk. Unknown keys in the file are errors.
`eval/configs/locomo.yaml` documents every section. In short:

| Key | Flag | Default |
|---|---|---|
| `benchmark` | `-benchmark` | (required) |
| `mode` | `-mode` | the benchmark's (`reflect` otherwise) |
| `data`, `data_dir` | `-data`, `-data-dir` | download into `~/.cache/memd-eval` |
| `memd.addr`, `memd.tenant`, `memd.stub`, `memd.skip` | `-addr`, `-tenant`, `-stub`, `-skip-memd` | `localhost:50051`, `eval` |
| `hindsight.url` | `-hindsight-url` | none (adds stock Hindsight as a second target) |
| `judge.kind`, `judge.url`, `judge.model` | `-judge`, `-judge-url`, `-judge-model` | `llm` (golden: `contains`) |
| `answer.url`, `answer.model` | `-answer-url`, `-answer-model` | judge's URL; model required in `rag` mode |
| `subset.conversations`, `subset.questions`, `subset.categories` | `-limit`, `-questions`, `-category a,b` | everything |
| `parallel`, `call_timeout` | `-parallel`, `-call-timeout` | 4, 5m |
| `barrier.poll`, `barrier.quiet_polls`, `barrier.settle_timeout` | `-poll`, `-quiet-polls`, `-settle-timeout` | 1s, 2, 15m (golden: 200ms, 90s) |
| `recall.budget`, `recall.max_tokens`, `recall.chunk_tokens` | `-recall-budget`, `-recall-max-tokens`, `-recall-chunk-tokens` | server defaults (locomo: high, 32768, 16384) |
| `bank_prefix`, `out_dir` | `-bank-prefix`, `-out-dir` | `eval-<unix time>-`, `eval-results` |

Use a judge model other than the one memd runs on: a model grading its own answers mostly agrees with itself.

## Reading the report

- **Accuracy by category:** `correct/total` per category, plus an overall row, with one column per target. Errors
  (failed retain, barrier timeout, RPC failure) count as wrong and are listed with their count. In recall mode,
  LongMemEval abstention and preference questions can't be graded, because there is no fact to find. They show as
  `n/a` and are left out of the total.
- **Token usage per slot:**
  - `EXTRACT`, `TIME_PARSE`, `EMBED` come from sync Retain responses.
  - `REFLECT` comes from Reflect responses.
  - `CONSOLIDATE` and the other memd slots come from the bank's background operations.
  - `ANSWER` and `JUDGE` are the eval's own models.
  - Hindsight reports totals only, so its tokens show under `RETAIN` and `REFLECT`.
- **Latency:** nearest-rank p50 and p95 in milliseconds, with sample counts, for each operation:
  - `retain`, per session
  - `barrier`, per bank
  - `recall`, `answer` and `reflect`, per question
  - `judge`
- **Problems:** failed background operations, conversation-level errors, and questions not run yet (resume to
  finish them).

## Golden fixtures

`eval/golden/*.yaml` files are written in YAML's JSON-compatible flow style and are built into the binary
(`-data <dir>` runs another directory). A fixture looks like this:

```yaml
{
  "name": "personal-facts",
  "sessions": [{"id": "pf-1", "time": "2024-03-04T09:15:00Z", "context": "...", "content": "user: ..."}],
  "checks": [{"id": "city", "category": "presence", "query": "Which city?", "accept": ["Lisbon"]}]
}
```

A check passes when any recalled memory contains one of its `accept` strings (case-insensitive). The golden checks
only prove that a retained fact can be recalled. The categories `presence-dated` and `presence-updated` don't test
temporal reasoning or knowledge updates; the benchmarks do. `go test ./eval/golden/` runs every fixture against the
in-process stub.

## Adding a benchmark

A benchmark is one small package that implements `benchmark.Benchmark` and registers itself:

```go
package mybench

func init() { benchmark.Register(Bench{}) }

type Bench struct{}

func (Bench) Name() string        { return "mybench" }
func (Bench) Description() string { return "My dataset, one line for eval list" }
func (Bench) Dataset() benchmark.File {
	return benchmark.File{Name: "data.json", URL: "https://example.com/<pinned commit>/data.json", SHA256: "<hex>"}
}
func (Bench) Load(path string) ([]harness.Conversation, error) { /* one Conversation per bank */ }
func (Bench) Defaults() benchmark.Defaults { return benchmark.Defaults{Mode: harness.ModeRAG, Judge: "llm"} }
```

Then add `_ "memd/eval/mybench"` to `eval/cmd/eval/benchmarks.go`.
- `Load` returns one `harness.Conversation` per bank: its sessions to retain and its questions.
- Each question's `Category` is its report bucket. Its `Kind` picks the judge prompt and, in rag mode, the answer
  prompt. A new prompt is a template in `eval/judge/templates`, or `eval/judge/templates/answer` for an answer prompt.
- `Defaults` should reproduce how the benchmark's published numbers were produced.
- A benchmark that should fail the run on a bad result also implements `benchmark.Gater`.

## Packages

- `benchmark`: the `Benchmark` interface, the registry, and the sha256-checked download cache.
- `harness`: the types, the runner, the barrier, the journal (resume), the report and the comparison.
- `target`: memd (gRPC) and Hindsight (HTTP). Every call to memd's API is here.
- `judge`: the gateway client, the LLM judge, the rag answer model and the substring judge.
- `locomo`, `longmemeval`, `golden`: the benchmarks.
- `stub`: the in-process stub memd server, used by the tests and `-stub`.
- `cmd/eval`: the CLI (`run`, `compare`, `list`).
- `configs`: example run configs.
