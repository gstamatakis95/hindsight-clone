// Package store is a stub of the DB and transaction types.
package store

import "context"

// Queue is a stub.
type Queue struct{}

// Enqueue is a stub.
func (Queue) Enqueue(ctx context.Context) {}

// Tx is a stub.
type Tx struct{}

// Seq is a stub.
func (Tx) Seq(bank string) {}

// Queue is a stub.
func (Tx) Queue() Queue { return Queue{} }

// DB is a stub.
type DB struct{}

// InTx is a stub.
func (DB) InTx(ctx context.Context, fn func(Tx) error) error { return fn(Tx{}) }
