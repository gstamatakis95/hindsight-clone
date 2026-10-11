// Package operation serves memory.v1.OperationService: reading a bank's operations (the jobs every async RPC
// returns) and lifecycle events, and cancelling or retrying an operation. Its one type, [Service], built by [New]
// from [Deps], runs no job and no sweep.
//
// # Where it sits
//
// operation is a service. It imports domain and the store boundary only, and never another service, jobs, an
// adapter, grpcapi, maintenance, gen, pgx, net/http, the S3 SDK or Datadog (depguard). cmd/memd/wire_operation.go
// builds the Service and hands it to grpcapi's OperationHandler.
//
// # Reads
//
// [Service.GetOperation], [Service.ListOperations] and [Service.ListEvents] each run in one short read-only
// transaction after checking the bank is live: an unknown or tombstoned bank is domain.ErrNotFound (SPEC: once a
// bank is deleting, reads return NOT_FOUND; GetBank NOT_FOUND is the delete's completion signal). ListOperations
// pages newest first. ListEvents returns the events after a seq, of the requested types (empty = all), and the last
// seq scanned; a limit of 0 or less is 100 and any limit is capped at 1000 (the store's EventRepo.List treats <= 0
// as no limit, so the service always passes a bound).
//
// # Control
//
// [Service.CancelOperation] cancels through Queue.Cancel: a pending row becomes CANCELLED at once; a processing row
// records the request and becomes CANCELLED when its handler stops at its next checkpoint. A terminal operation is
// domain.ErrFailedPrecondition, and so is any delete_bank: the bank is already tombstoned, and a stopped delete
// would leave it so.
//
// [Service.RetryOperation] re-queues a FAILED or CANCELLED operation through Queue.Retry (PENDING, run_at = now,
// attempt reset); any other status is domain.ErrFailedPrecondition. On a bank being deleted only delete_bank may be
// retried (that resumes the delete); any other job would only be skipped by claims and removed with the bank, so
// it is refused like every other write to a deleting bank (domain.ErrBankDeleting).
//
// # Tests
//
// Tests run on a pgtest database: the ListEvents default and cap, and the delete_bank and deleting-bank rules of
// cancel and retry.
package operation
