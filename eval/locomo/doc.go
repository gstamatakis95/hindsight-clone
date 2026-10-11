// Package locomo loads the LoCoMo long-conversation benchmark (locomo10.json, https://github.com/snap-research/locomo)
// into eval/harness conversations. The dataset is not vendored; download it yourself.
//
// # Mapping
//
// [Load] opens a file and [Parse] decodes the JSON array; both return one harness.Conversation per sample, whose id is
// the sample_id and whose bank therefore holds exactly that sample:
//
//   - Sessions: every conversation key session_N, ordered by N, becomes one harness.Session with id
//     "<sample_id>-session_N", its time parsed from session_N_date_time as UTC ("1:56 pm on 8 May, 2023", or the
//     abbreviated month form), its content one "speaker: text" line per turn (a shared image is noted by its BLIP
//     caption), and the context "Conversation between <speaker_a> and <speaker_b>.".
//   - Questions: every qa entry except the adversarial category 5, which has no gold answer, becomes one
//     harness.Question with id "<sample_id>-q<index>", the answer as text (a JSON number is kept verbatim, null is
//     empty), and a query time equal to the last session's time so relative expressions resolve as at the end of the
//     conversation. Category 2 questions use the temporal judge prompt, the rest the default.
//   - Categories: 1-multi-hop, 2-temporal, 3-open-domain, 4-single-hop, else category-N. The dataset does not name its
//     categories; the names follow the mapping most published LoCoMo evals use, and the number stays in the name
//     because that mapping is unverified.
//
// Malformed sessions, dates or answers fail the load with the sample id in the error.
//
// # Testing
//
// load_test.go parses testdata/sample.json, a trimmed sample in the dataset's shape, and checks the session order,
// dates, rendering, skipped adversarial questions, categories and error cases.
package locomo
