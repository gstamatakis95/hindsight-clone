package worker

import "errors"

// ErrPermanent marks a deterministic failure (schema violation, integrity error, bad payload): the runner
// fails the job at once with no retry. Wrap it: fmt.Errorf("%w: ...", worker.ErrPermanent).
var ErrPermanent = errors.New("permanent job failure")
