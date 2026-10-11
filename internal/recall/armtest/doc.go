// Package armtest is the contract suite every recall arm runs from its own tests. [Run] takes a constructor that
// returns a fresh, empty [Fixture]: the store.DB the suite seeds and the [recall.Arm] under test, built over that
// store. The suite seeds three banks with the same data (two banks of one tenant and a same-named bank of another
// tenant): units that match a query by embedding, text and occurrence time, and units reachable from them only over
// semantic links. It then checks that the arm
//
//   - returns at least one hit and at most Query.Limit, best first (non-increasing score);
//   - stays within the query's bank: every hit is a unit seeded in it;
//   - never writes: no bank's change sequence moves while it runs;
//   - honours ctx cancellation: a cancelled context fails the call.
//
// The built-in arms run it in internal/recall/arms against pgtest.
package armtest
