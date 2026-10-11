// Package locomo loads the LoCoMo long-conversation benchmark (locomo10.json, https://github.com/snap-research/locomo)
// into eval/harness conversations and registers it with eval/benchmark as "locomo". The dataset is downloaded from a
// pinned commit of the LoCoMo repository and checked against its sha256.
//
// The mapping and the benchmark's defaults follow Hindsight's published LoCoMo methodology (its Agent Memory
// Benchmark), so memd's numbers compare directly with Hindsight's; eval/README.md describes it and the deliberate
// differences.
//
// # Mapping
//
// [Load] opens a file and [Parse] decodes the JSON array; both return one harness.Conversation per sample, whose id is
// the sample_id and whose bank therefore holds exactly that sample:
//
//   - Sessions: every non-empty conversation key session_N, in order of N, becomes one harness.Session with id
//     "<sample_id>_session_N", its time parsed from session_N_date_time as UTC ("1:56 pm on 8 May, 2023", or the
//     abbreviated month form), its content the session's turns as a compact JSON array with every field of the dataset
//     (speaker, dia_id, text, img_url, blip_caption), and the context "Conversation between <speaker_a> and
//     <speaker_b> (session_N of <sample_id>)".
//   - Questions: every qa entry except the adversarial category 5 becomes one harness.Question with id
//     "<sample_id>_q<index>", the answer as text (a JSON number is kept verbatim, null is empty) and the KindLoCoMo
//     judge and answer prompts. The query time is the date of the last session key in string order whose date parses
//     with the full month name ("session_9" sorts after "session_19"), as in Hindsight's benchmark.
//   - Categories: 1-multi-hop, 2-temporal, 3-open-domain, 4-single-hop, else category-N. The dataset does not name
//     its categories; these names follow its questions (category 1 questions cite three evidence turns on average and
//     almost all cite more than one, category 4 questions cite one, category 3 questions ask for inference), and the
//     number stays in the name because other evals label the numbers differently.
//
// The registered [Benchmark] defaults to rag mode with the LLM judge and recall at budget high, 32768 result tokens and
// source chunks up to 16384 tokens. Malformed sessions, dates or answers fail the load with the sample id in the
// error.
//
// # Testing
//
// load_test.go parses testdata/sample.json, a trimmed sample in the dataset's shape, and checks the whole mapping.
package locomo
