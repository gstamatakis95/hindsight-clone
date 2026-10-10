package postgres

import (
	"encoding/json"
	"fmt"
	"memd/internal/adapter/postgres/db"
	"memd/internal/domain"
	"time"

	"github.com/google/uuid"
	pgvector "github.com/pgvector/pgvector-go"
)

// parseID parses a server-generated id (unit, chunk, entity, operation). A malformed id is
// domain.ErrNotFound: no row can carry it.
func parseID[T ~string](id T) (uuid.UUID, error) {
	u, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: id %q", domain.ErrNotFound, id)
	}
	return u, nil
}

// parseNewID parses the id of a row about to be inserted; an empty id gets a fresh UUID and a malformed one
// is domain.ErrInvalidArgument.
func parseNewID[T ~string](id T) (uuid.UUID, error) {
	if id == "" {
		return uuid.New(), nil
	}
	u, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: id %q is not a UUID", domain.ErrInvalidArgument, id)
	}
	return u, nil
}

// parseIDs parses ids, silently dropping malformed ones (they match no row).
func parseIDs[T ~string](ids []T) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if u, err := uuid.Parse(string(id)); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// nullID converts an optional id: empty is NULL, a malformed one is domain.ErrInvalidArgument.
func nullID[T ~string](id T) (uuid.NullUUID, error) {
	if id == "" {
		return uuid.NullUUID{}, nil
	}
	u, err := uuid.Parse(string(id))
	if err != nil {
		return uuid.NullUUID{}, fmt.Errorf("%w: id %q is not a UUID", domain.ErrInvalidArgument, id)
	}
	return uuid.NullUUID{UUID: u, Valid: true}, nil
}

// idOf converts a UUID back into a string-based id type.
func idOf[T ~string](u uuid.UUID) T { return T(u.String()) }

// idsOf converts UUIDs back into string-based ids.
func idsOf[T ~string](us []uuid.UUID) []T {
	out := make([]T, len(us))
	for i, u := range us {
		out[i] = T(u.String())
	}
	return out
}

// nullIDOf converts an optional UUID back into an id; NULL is empty.
func nullIDOf[T ~string](u uuid.NullUUID) T {
	if !u.Valid {
		return ""
	}
	return T(u.UUID.String())
}

// textPtr converts an optional string column value: empty is NULL.
func textPtr[T ~string](s T) *string {
	if s == "" {
		return nil
	}
	v := string(s)
	return &v
}

// textOf converts an optional text column back; NULL is empty.
func textOf[T ~string](s *string) T {
	if s == nil {
		return ""
	}
	return T(*s)
}

// vec converts an embedding into a pgvector value; an empty embedding is NULL.
func vec(e []float32) *pgvector.Vector {
	if len(e) == 0 {
		return nil
	}
	v := pgvector.NewVector(e)
	return &v
}

// vecOf converts a stored vector back; NULL is nil.
func vecOf(v *pgvector.Vector) []float32 {
	if v == nil {
		return nil
	}
	return v.Slice()
}

// orNow returns t, or nil when t is zero so the column default (the database's now()) applies.
func orNow(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// tagsOrEmpty returns tags, or an empty (non-NULL) slice for nil.
func tagsOrEmpty(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// marshal encodes v as a JSON column value.
func marshal(what string, v any) (json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("%w: encode %s: %w", domain.ErrInvalidArgument, what, err)
	}
	return raw, nil
}

// marshalPtr encodes an optional value as a nullable JSON column value; nil is NULL.
func marshalPtr[T any](what string, v *T) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	return marshal(what, v)
}

// unmarshal decodes a JSON column value into v; an empty or null value leaves v unchanged.
func unmarshal(what string, raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%w: decode %s: %w", domain.ErrUnavailable, what, err)
	}
	return nil
}

// unmarshalPtr decodes a nullable JSON column into a new value; NULL is nil.
func unmarshalPtr[T any](what string, raw json.RawMessage) (*T, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	v := new(T)
	if err := unmarshal(what, raw, v); err != nil {
		return nil, err
	}
	return v, nil
}

// bankOf builds a domain.Bank from its row.
func bankOf(r db.Bank) (domain.Bank, error) {
	b := domain.Bank{
		Ref:  domain.BankRef{Tenant: r.TenantID, Bank: r.ID},
		Name: r.Name, ChangeSeq: r.ChangeSeq, DeletingAt: r.DeletingAt, LastMemoryWriteAt: r.LastMemoryWriteAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	return b, unmarshal("bank config", r.Config, &b.Config)
}

// unitOf builds a domain.Unit from its row, with the reader-filled entity names and sources.
func unitOf(r db.MemoryUnit, entities []string, sources []uuid.UUID) (domain.Unit, error) {
	u := domain.Unit{
		ID: idOf[domain.UnitID](r.ID), Bank: domain.BankRef{Tenant: r.TenantID, Bank: r.BankID},
		DocumentID: textOf[domain.DocumentID](r.DocumentID), ChunkID: nullIDOf[domain.ChunkID](r.ChunkID),
		Type: domain.FactType(r.FactType), Text: r.Text, Context: r.Context, Tags: r.Tags,
		Entities: entities, Embedding: vecOf(r.Embedding),
		OccurredStart: r.OccurredStart, OccurredEnd: r.OccurredEnd, MentionedAt: r.MentionedAt,
		Timeless: r.Timeless, ProofCount: int(r.ProofCount), SourceIDs: idsOf[domain.UnitID](sources),
		Version: r.Version, Seq: r.Seq, ConsolidatedAt: r.ConsolidatedAt,
		ConsolidationFailedAt: r.ConsolidationFailedAt, EditedAt: r.EditedAt, State: domain.MemoryStateValid,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if err := unmarshal("unit metadata", r.Metadata, &u.Metadata); err != nil {
		return domain.Unit{}, err
	}
	return u, nil
}
