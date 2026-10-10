package bank

import (
	"context"
	"memd/internal/domain"
	"memd/internal/port"
)

// TemplateCodec exports a bank's config, directives and model specs, and creates banks from them.
type TemplateCodec interface {
	// Export returns bank b's own config, directives and mental-model specs (no memories); an unknown or
	// tombstoned bank is domain.ErrNotFound.
	Export(ctx context.Context, b port.BankRef) (domain.BankTemplate, error)
	// Create makes bank b from t in one transaction, reassigning directive ids and enqueueing a first
	// build of every mental model; an existing bank is domain.ErrAlreadyExists. It is CreateNamed with
	// an empty name, kept for callers that need only the bank.
	Create(ctx context.Context, b port.BankRef, t domain.BankTemplate) (domain.Bank, error)
	// CreateNamed is Create with the bank's display name; it also returns the first-build operation of
	// every mental model, in template order (CreateBankFromTemplateResponse.mental_model_builds).
	CreateNamed(
		ctx context.Context, b port.BankRef, name string, t domain.BankTemplate,
	) (domain.Bank, []domain.Operation, error)
}

// ModelSpecReader reads the specs of a bank's mental models. It is the port.Reader method proposed in CCR
// WP-13-1; port.Reader satisfies it once the CCR lands.
type ModelSpecReader interface {
	// MentalModelSpecs returns the spec of every mental model of b, ordered by id; an unknown or
	// tombstoned bank is domain.ErrNotFound.
	MentalModelSpecs(ctx context.Context, b port.BankRef) ([]domain.MentalModelSpec, error)
}

// RefreshQueuer enqueues mental-model refreshes; the mental-model engine (WP-12) owns the refresh payload
// and its scheduling rules, so WP-13 never builds a refresh job itself (decision 1303).
type RefreshQueuer interface {
	// QueueRefresh enqueues (or joins) a refresh of model id of bank b inside tx and returns its operation
	// id. The caller has already called tx.Seq(b).
	QueueRefresh(ctx context.Context, tx port.Tx, b port.BankRef, id port.MentalModelID) (port.OperationID, error)
}
