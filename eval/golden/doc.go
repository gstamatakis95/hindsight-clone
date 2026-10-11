// Package golden holds the golden retain -> recall fixtures (*.yaml in this directory, built into the binary) and
// their loader, and registers them with eval/benchmark as "golden": recall mode, the substring judge, a 200 ms poll
// and a 90 s barrier, and a gate that fails the run unless every check passed.
//
// Each fixture is one bank: its sessions are retained, then each check recalls a query and passes when a recalled
// memory contains one of the accepted substrings, case-insensitively. The checks are graded by judge.Contains, with no
// model in the loop, so they can run against `memd all` behind the deterministic gateway emulator (test/e2e's
// TestGoldenAgainstMemdAll) or against the in-process eval/stub server. They only prove that a retained fact can be
// recalled; the presence-dated and presence-updated categories do not test temporal reasoning or knowledge updates (the
// benchmarks do).
//
// # Fixture format
//
// Fixtures are written in YAML's JSON-compatible flow style and decoded with encoding/json, strictly: unknown fields
// and trailing content are errors.
//
//	{
//	  "name": "personal-facts",
//	  "sessions": [{"id": "pf-1", "time": "2024-03-04T09:15:00Z", "context": "...", "content": "user: ..."}],
//	  "checks": [{"id": "city", "category": "presence", "query": "Which city?", "accept": ["Lisbon"]}]
//	}
//
// A fixture needs a name, at least one session and at least one check. Session ids become document ids, so they must be
// unique within the fixture (a repeat would silently upsert over the earlier session), as must check ids; every session
// needs content and every check a query and at least one accepted string.
//
// # Loading
//
// [Load] reads every *.yaml file of a directory (the built-in fixtures when the directory is "") in file-name order and
// returns one harness.Conversation per fixture: the conversation id is the fixture name (names must be unique across
// files), sessions map one to one, and each check becomes a question whose category is the check's category, else the
// fixture name, with the first accepted string as its answer.
//
//	convs, err := golden.Load("")
//
// # Testing
//
// golden_test.go rejects malformed fixtures and runs the built-in ones through the harness against eval/stub in recall
// mode, failing if any check fails or the run takes two minutes or longer.
package golden
