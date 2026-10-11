package main

import (
	"context"
	"errors"
	"fmt"
	"memd/internal/domain"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/reflect"
	"memd/internal/port"
	"memd/internal/server"
	"net/http"
	"sync"
	"time"
)

// Small adapters the composition root needs where no engine provides the interface: they only translate
// between ports and the server's or reflect's consumer interfaces, with no business logic.

// systemClock is the production port.Clock.
type systemClock struct{}

var _ port.Clock = systemClock{}

// Now returns the wall-clock time.
func (systemClock) Now() time.Time { return time.Now() }

// operationReader serves GetOperation, ListOperations and ListEvents over port.Queue and port.EventRepo,
// each in one short read-only transaction. An unknown or tombstoned bank is domain.ErrNotFound (SPEC: once a
// bank is deleting, reads return NOT_FOUND; GetBank NOT_FOUND is the delete's completion signal).
type operationReader struct{ s port.Store }

var _ server.OperationReader = (*operationReader)(nil)

// readBank runs fn in a transaction after checking b is a live bank.
func (o *operationReader) readBank(ctx context.Context, b port.BankRef, fn func(tx port.Tx) error) error {
	return o.s.InTx(ctx, func(tx port.Tx) error { //nolint:wrapcheck // port errors are already typed
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
	ctx context.Context, b port.BankRef, id port.OperationID,
) (domain.Operation, error) {
	var op domain.Operation
	err := o.readBank(ctx, b, func(tx port.Tx) error {
		var err error
		op, err = tx.Queue().Get(ctx, b, id)
		return err
	})
	return op, err
}

// ListOperations pages b's operations, newest first.
func (o *operationReader) ListOperations(
	ctx context.Context, b port.BankRef, f port.OperationFilter,
) ([]domain.Operation, string, error) {
	var ops []domain.Operation
	var next string
	err := o.readBank(ctx, b, func(tx port.Tx) error {
		var err error
		ops, next, err = tx.Queue().List(ctx, b, f)
		return err
	})
	return ops, next, err
}

// ListEvents returns b's events after afterSeq, of types (empty = all), and the last seq scanned.
func (o *operationReader) ListEvents(
	ctx context.Context, b port.BankRef, afterSeq int64, limit int, types []domain.EventType,
) ([]domain.LifecycleEvent, int64, error) {
	var events []domain.LifecycleEvent
	var last int64
	err := o.readBank(ctx, b, func(tx port.Tx) error {
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
	s      port.Store
}

var _ reflect.PageReader = (*pageReader)(nil)

// SearchPages ranks b's pages for query.
func (p *pageReader) SearchPages(
	ctx context.Context, b port.BankRef, query string, limit int,
) ([]domain.PageHit, error) {
	hits, err := p.search.Search(ctx, mentalmodel.SearchPagesRequest{Bank: b, Query: query, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("search pages: %w", err)
	}
	return hits, nil
}

// ReadPages returns the pages with ids in order, skipping unknown ones.
func (p *pageReader) ReadPages(
	ctx context.Context, b port.BankRef, ids []port.MentalModelID,
) ([]domain.MentalModel, error) {
	var out []domain.MentalModel
	err := p.s.InTx(ctx, func(tx port.Tx) error {
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

// pinger is the part of a pgx pool the Postgres readiness probe uses.
type pinger interface {
	// Ping checks the connection.
	Ping(ctx context.Context) error
}

// postgresProbe reports Postgres readiness with a ping.
type postgresProbe struct{ db pinger }

var _ server.ReadinessProbe = postgresProbe{}

// Ready pings Postgres.
func (p postgresProbe) Ready(ctx context.Context) error {
	if err := p.db.Ping(ctx); err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	return nil
}

// gatewayProbe reports the gateway reachable when an HTTP request to its base URL gets any response. It
// makes no model call (health checks must not spend tokens or show up as model traffic) and caches the
// answer for gatewayProbeTTL, so frequent health polls don't hit the gateway each time.
type gatewayProbe struct {
	url string
	hc  *http.Client

	mu      sync.Mutex
	checked time.Time
	err     error
}

// gatewayProbeTTL is how long a gateway probe result is reused.
const gatewayProbeTTL = 10 * time.Second

var _ server.ReadinessProbe = (*gatewayProbe)(nil)

// Ready reports whether the gateway answered recently.
func (g *gatewayProbe) Ready(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.checked.IsZero() && time.Since(g.checked) < gatewayProbeTTL {
		return g.err
	}
	g.err = g.check(ctx)
	g.checked = time.Now()
	return g.err
}

// check sends one GET to the base URL; any HTTP status counts as reachable.
func (g *gatewayProbe) check(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.url, nil)
	if err != nil {
		return fmt.Errorf("gateway: %w", err)
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return fmt.Errorf("gateway unreachable: %w", err)
	}
	_ = resp.Body.Close()
	return nil
}
