// Package longmemeval loads the LongMemEval benchmark (longmemeval_s, _m or _oracle,
// https://github.com/xiaowu0162/LongMemEval) into eval/harness conversations. The dataset is not vendored; download it
// yourself.
//
// # Mapping
//
// [Load] opens a file and [Parse] decodes the JSON array; both return one harness.Conversation per question instance,
// whose id is the question_id, so every question gets its own bank holding only its haystack:
//
//   - Sessions: each haystack session becomes one harness.Session, its time parsed from haystack_dates as UTC
//     ("2023/05/30 (Tue) 23:40"), its content one "role: content" line per turn, and the context "Chat session between
//     the user and an AI assistant.". The session id is its haystack_session_ids entry; a missing or repeated id gets
//     an "s<index>" suffix so the second session does not upsert over the first. Sessions are sorted chronologically
//     (stable for equal times).
//   - Question: the single question, with its question_date as query time, its answer as text (a JSON number is kept
//     verbatim, null is empty), and its question_type as report category.
//   - Judge kind: a question id ending in _abs is an abstention question; otherwise temporal-reasoning uses the
//     temporal prompt, knowledge-update the update prompt, single-session-preference the preference rubric, and every
//     other type the default prompt. The prompts follow LongMemEval's official scorer (eval/judge).
//
// Haystack arrays of different lengths, unparseable dates and malformed answers fail the load with the question id in
// the error.
//
// # Testing
//
// load_test.go parses testdata/sample.json, a trimmed instance in the dataset's shape, and checks ordering, judge
// kinds, missing and duplicate session ids and error cases.
package longmemeval
