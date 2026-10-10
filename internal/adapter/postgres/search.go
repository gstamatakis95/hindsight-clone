package postgres

import (
	"context"
	"memd/internal/port"
)

// SearchVector is not implemented yet.
func (r *reader) SearchVector(ctx context.Context, q port.VectorQuery) ([]port.Hit, error) {
	return nil, errNotImplemented
}

// SearchText is not implemented yet.
func (r *reader) SearchText(ctx context.Context, q port.TextQuery) ([]port.Hit, error) {
	return nil, errNotImplemented
}

// SearchTemporal is not implemented yet.
func (r *reader) SearchTemporal(ctx context.Context, q port.TemporalQuery) ([]port.Hit, error) {
	return nil, errNotImplemented
}

// ExpandGraph is not implemented yet.
func (r *reader) ExpandGraph(ctx context.Context, q port.GraphQuery) ([]port.Hit, error) {
	return nil, errNotImplemented
}
