package main

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/reflect"
	"memd/internal/grpcapi"
	"memd/internal/store"
)

// Small adapters the composition root needs where no engine provides the interface: they only translate
// between the store and the gRPC handlers' or reflect's interfaces, with no business logic.

// operationReader serves GetOperation, ListOperations and ListEvents over store.Queue and store.EventRepo,
// each in one short read-only transaction. An unknown or tombstoned bank is domain.ErrNotFound (SPEC: once a
// bank is deleting, reads return NOT_FOUND; GetBank NOT_FOUND is the delete's completion signal).
type operationReader struct{ s store.DB }

var _ grpcapi.OperationReader = (*operationReader)(nil)

// readBank runs fn in a transaction after checking b is a live bank.
func (o *operationReader) readBank(ctx context.Context, b domain.BankRef, fn func(tx store.Tx) error) error {
	return o.s.InTx(ctx, func(tx store.Tx) error { //nolint:wrapcheck // port errors are already typed
		bank, err := tx.Banks().Get(ctx, b)
		if err != nil {
			return err
		}
		if bank.DeletingAt != nil {
			return fmt.Errorf("%w: bank %s is being deleted", domain.ErrNotFound, b.Bank)
		}
		return fn(tx)
	})
}

// GetOperation returns one operation of b.
func (o *operationReader) GetOperation(
	ctx context.Context, b domain.BankRef, id domain.OperationID,
) (domain.Operation, error) {
	var op domain.Operation
	err := o.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		op, err = tx.Queue().Get(ctx, b, id)
		return err
	})
	return op, err
}

// ListOperations pages b's operations, newest first.
func (o *operationReader) ListOperations(
	ctx context.Context, b domain.BankRef, f store.OperationFilter,
) ([]domain.Operation, string, error) {
	var ops []domain.Operation
	var next string
	err := o.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		ops, next, err = tx.Queue().List(ctx, b, f)
		return err
	})
	return ops, next, err
}

// ListEvents page bounds: the soft limit of a request that sets none, and the cap of any request (the
// memory and bank listings' bounds; decision 2502).
const (
	defaultEventsLimit = 100
	maxEventsLimit     = 1000
)

// ListEvents returns b's events after afterSeq, of types (empty = all), and the last seq scanned. A limit of
// 0 or less is defaultEventsLimit and one above maxEventsLimit is capped (EventRepo.List itself treats
// <= 0 as no limit).
func (o *operationReader) ListEvents(
	ctx context.Context, b domain.BankRef, afterSeq int64, limit int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64, error) {
	if limit <= 0 {
		limit = defaultEventsLimit
	}
	limit = min(limit, maxEventsLimit)
	var events []domain.LifecycleEvent
	var last int64
	err := o.readBank(ctx, b, func(tx store.Tx) error {
		var err error
		events, last, err = tx.Events().List(ctx, b, afterSeq, limit, types)
		return err
	})
	return events, last, err
}

// pageReader is the reflect page tools' view of mental models: search through the PageSearcher, reads of
// whole pages through the MentalModelRepo.
type pageReader struct {
	search mentalmodel.PageSearcher
	s      store.DB
}

var _ reflect.PageReader = (*pageReader)(nil)

// SearchPages ranks b's pages for query.
func (p *pageReader) SearchPages(
	ctx context.Context, b domain.BankRef, query string, limit int,
) ([]domain.PageHit, error) {
	hits, err := p.search.Search(ctx, mentalmodel.SearchPagesRequest{Bank: b, Query: query, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("search pages: %w", err)
	}
	return hits, nil
}

// ReadPages returns the pages with ids in order, skipping unknown ones.
func (p *pageReader) ReadPages(
	ctx context.Context, b domain.BankRef, ids []domain.MentalModelID,
) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	err := p.s.InTx(ctx, func(tx store.Tx) error {
		out = out[:0]
		for _, id := range ids {
			m, err := tx.MentalModels().Get(ctx, b, id)
			if errors.Is(err, domain.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			out = append(out, m)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read pages: %w", err)
	}
	return out, nil
}
