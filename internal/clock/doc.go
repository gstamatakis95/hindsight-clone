// Package clock declares [Clock], the injected source of the current time, with its two implementations: [System],
// the wall clock cmd/memd wires in, and [Manual], a clock tests set and advance by hand.
//
// Logic never calls time.Now (or time.Since or time.After for decisions): recency, debounce, interval floors and
// budgets read an injected Clock, so tests are deterministic. A Manual clock only drives the code it is passed to:
// Postgres judges job due-ness and heartbeat staleness by its own clock, so a test that needs a parked job to become
// due moves the job's run_at with SQL.
//
//	c := clock.NewManual(time.Time{}) // frozen at DefaultStart
//	c.Advance(time.Minute)
package clock
