package server

import (
	"context"
	"memd/internal/domain"
	"memd/internal/engine/bank"
	"memd/internal/engine/consolidate"
	"memd/internal/engine/curate"
	"memd/internal/engine/docs"
	"memd/internal/engine/mentalmodel"
	"memd/internal/engine/recall"
	"memd/internal/engine/retain"
	"memd/internal/port"
	"memd/internal/worker"
	"time"

	reflecteng "memd/internal/engine/reflect"
)

// Deps holds everything the handlers call. cmd/memd/wire.go fills it; a nil dependency makes its RPCs
// fail with UNIMPLEMENTED instead of panicking.
type Deps struct {
	// Ingestor serves Retain.
	Ingestor retain.Ingestor
	// Recaller serves Recall.
	Recaller recall.Recaller
	// Reflector serves Reflect.
	Reflector reflecteng.Reflector
	// Lister serves ListMemories, GetMemory and GetObservationHistory.
	Lister curate.MemoryLister
	// Curator serves UpdateMemory.
	Curator curate.Curator
	// Stats serves GetBankStats and ListTags.
	Stats bank.StatsReader
	// Previewer serves PreviewPrompt.
	Previewer bank.PromptPreviewer
	// Deleter serves DeleteBank (Tombstone).
	Deleter bank.BankDeleter
	// Templates serves ExportBankTemplate and CreateBankFromTemplate.
	Templates bank.TemplateCodec
	// Retagger serves UpdateDocument.
	Retagger docs.Retagger
	// Strategies serves PreviewConsolidationStrategies.
	Strategies consolidate.StrategyMatcher
	// Refresher serves DryRunRefresh.
	Refresher mentalmodel.RefreshRunner
	// Pages serves SearchPages.
	Pages mentalmodel.PageSearcher
	// Control serves CancelOperation and RetryOperation.
	Control worker.OperationControl
	// Config resolves GetBankConfig's effective view.
	Config port.ConfigResolver
	// Tenants serves TenantService.
	Tenants TenantAdmin
	// Banks serves CreateBank, GetBank, GetBankConfig (overrides), UpdateBankConfig and ResetBankConfig.
	Banks BankAdmin
	// Directives serves UpsertDirective, ListDirectives and DeleteDirective.
	Directives DirectiveAdmin
	// Documents serves ListDocuments, GetDocument and ReprocessDocument.
	Documents DocumentAdmin
	// Observations serves Consolidate, ListScopes, ClearObservations and ClearMemoryObservations.
	Observations ObservationAdmin
	// MentalModels serves the mental-model RPCs other than DryRunRefresh and SearchPages.
	MentalModels MentalModelAdmin
	// Operations serves GetOperation, ListOperations and ListEvents, and loads the operation an
	// UpdateDocument started.
	Operations OperationReader
	// ValidateOverrides rejects override fields an RPC can't use (config.ValidateOverrides); rpc is the
	// method name, e.g. "Recall". nil = no check here (the engines may still check).
	ValidateOverrides func(rpc string, o domain.BankConfig) error
	// DefaultDeadline applies to calls that arrive without a deadline; 0 = none.
	DefaultDeadline time.Duration
	// Probes are the readiness checks (Postgres, gateway) behind grpc.health.v1 Check.
	Probes []ReadinessProbe
}

// ReadinessProbe is one readiness check behind grpc.health.v1.
type ReadinessProbe interface {
	// Ready returns nil when the dependency answers.
	Ready(ctx context.Context) error
}

// ConfigPatch computes a new config from the current one; it must be pure.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type ConfigPatch = bank.ConfigPatch

// SpecPatch computes a new mental-model spec from the current one; it must be pure.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type SpecPatch = mentalmodel.SpecPatch

// TenantAdmin reads and patches the tenant-level config.
type TenantAdmin interface {
	// TenantConfig returns the tenant's config; a tenant with none set returns the zero config.
	TenantConfig(ctx context.Context, tenant string) (domain.BankConfig, error)
	// UpdateTenantConfig applies patch to the stored tenant config in one transaction and returns the
	// stored result.
	UpdateTenantConfig(ctx context.Context, tenant string, patch ConfigPatch) (domain.BankConfig, error)
}

// BankAdmin creates and reads banks and patches their own overrides.
type BankAdmin interface {
	// CreateBank inserts bank (Ref, Name, Config) and returns it as stored; an existing id is
	// domain.ErrAlreadyExists.
	CreateBank(ctx context.Context, bank domain.Bank) (domain.Bank, error)
	// GetBank returns the bank, a tombstoned one included (DeletingAt set); unknown = domain.ErrNotFound.
	GetBank(ctx context.Context, b port.BankRef) (domain.Bank, error)
	// UpdateBankConfig applies patch to the bank's own overrides in one transaction and returns the bank.
	// It is a write: an unknown bank is created with tenant defaults (BankRepo.Ensure), a tombstoned one is
	// domain.ErrBankDeleting.
	UpdateBankConfig(ctx context.Context, b port.BankRef, patch ConfigPatch) (domain.Bank, error)
}

// DirectiveAdmin manages a bank's directives.
type DirectiveAdmin interface {
	// UpsertDirective creates d (empty ID) or replaces it, and returns it as stored.
	UpsertDirective(ctx context.Context, d domain.Directive) (domain.Directive, error)
	// ListDirectives returns the bank's directives whose tags overlap tags (all when tags is empty), only
	// active ones when activeOnly.
	ListDirectives(ctx context.Context, b port.BankRef, tags []string, activeOnly bool) ([]domain.Directive, error)
	// DeleteDirective removes one directive; unknown = domain.ErrNotFound.
	DeleteDirective(ctx context.Context, b port.BankRef, id port.DirectiveID) error
}

// ListDocumentsRequest mirrors memory.v1.ListDocumentsRequest.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type ListDocumentsRequest = docs.ListDocumentsRequest

// DocumentPage is one page of documents.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type DocumentPage = docs.DocumentPage

// DocumentView is a document as GetDocument returns it.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type DocumentView = docs.DocumentView

// DocumentAdmin lists, reads and reprocesses documents.
type DocumentAdmin interface {
	// ListDocuments returns one page of documents.
	ListDocuments(ctx context.Context, req ListDocumentsRequest) (DocumentPage, error)
	// GetDocument returns one document, with its original text when includeText; unknown =
	// domain.ErrNotFound.
	GetDocument(ctx context.Context, b port.BankRef, id port.DocumentID, includeText bool) (DocumentView, error)
	// ReprocessDocument re-extracts the document from its stored text (resetting curation), replacing its
	// stored overrides when overrides is non-nil, and returns the retain operation.
	ReprocessDocument(
		ctx context.Context, b port.BankRef, id port.DocumentID, overrides *domain.BankConfig,
	) (domain.Operation, error)
}

// ScopePage is one page of observation scopes.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type ScopePage = curate.ScopePage

// ClearResult reports what a clear removed and the consolidation it queued.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type ClearResult = curate.ClearResult

// ObservationAdmin runs and clears consolidation.
type ObservationAdmin interface {
	// Consolidate queues a manual consolidation over scopes (all when empty) with request overrides and
	// returns its operation.
	Consolidate(
		ctx context.Context, b port.BankRef, scopes []domain.TagSet, overrides *domain.BankConfig,
	) (domain.Operation, error)
	// ListScopes returns one page of the bank's observation scopes with their counts.
	ListScopes(ctx context.Context, b port.BankRef, pageSize int, pageToken string) (ScopePage, error)
	// ClearObservations deletes the observations of scopes (all when empty), resets their sources and,
	// when reconsolidate, queues consolidation.
	ClearObservations(
		ctx context.Context, b port.BankRef, scopes []domain.TagSet, reconsolidate bool,
	) (ClearResult, error)
	// ClearMemoryObservations deletes the observations citing one memory, resets their co-sources and,
	// when reconsolidate, queues consolidation.
	ClearMemoryObservations(
		ctx context.Context, b port.BankRef, id port.UnitID, reconsolidate bool,
	) (ClearResult, error)
}

// ListMentalModelsRequest mirrors memory.v1.ListMentalModelsRequest.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type ListMentalModelsRequest = mentalmodel.ListRequest

// MentalModelPage is one page of mental models.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type MentalModelPage = mentalmodel.ModelPage

// RefreshRequest mirrors memory.v1.RefreshMentalModelRequest.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type RefreshRequest = mentalmodel.RefreshRequest

// HistoryRequest mirrors memory.v1.GetMentalModelHistoryRequest.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type HistoryRequest = mentalmodel.HistoryRequest

// HistoryPage is one page of a mental model's history, newest first.
// Alias of the engine type, so the engine implementation satisfies the server interface (decisions 1205, 1302).
type HistoryPage = mentalmodel.HistoryPage

// MentalModelAdmin manages mental models; DryRunRefresh and SearchPages go to their engines directly.
type MentalModelAdmin interface {
	// CreateMentalModel stores spec and queues its first build, returning the model and the build.
	CreateMentalModel(
		ctx context.Context, b port.BankRef, spec domain.MentalModelSpec,
	) (domain.MentalModel, domain.Operation, error)
	// GetMentalModel returns one model loaded to detail; unknown = domain.ErrNotFound.
	GetMentalModel(
		ctx context.Context, b port.BankRef, id port.MentalModelID, detail domain.Detail,
	) (domain.MentalModel, error)
	// ListMentalModels returns one page of models.
	ListMentalModels(ctx context.Context, req ListMentalModelsRequest) (MentalModelPage, error)
	// UpdateMentalModel applies patch to the model's spec in one transaction and returns the model.
	UpdateMentalModel(
		ctx context.Context, b port.BankRef, id port.MentalModelID, patch SpecPatch,
	) (domain.MentalModel, error)
	// RefreshMentalModel queues a manual refresh, or joins and upgrades the queued one (joined = true).
	RefreshMentalModel(ctx context.Context, req RefreshRequest) (op domain.Operation, joined bool, err error)
	// ClearMentalModel empties the model's content so the next refresh is full, and returns the model.
	ClearMentalModel(ctx context.Context, b port.BankRef, id port.MentalModelID) (domain.MentalModel, error)
	// MentalModelHistory returns one page of the model's history.
	MentalModelHistory(ctx context.Context, req HistoryRequest) (HistoryPage, error)
	// DeleteMentalModel removes the model and its history; unknown = domain.ErrNotFound.
	DeleteMentalModel(ctx context.Context, b port.BankRef, id port.MentalModelID) error
	// DeleteFolder removes every model under folder path and returns how many.
	DeleteFolder(ctx context.Context, b port.BankRef, path string) (int, error)
}

// OperationReader reads operations and lifecycle events (Queue.Get, Queue.List and EventRepo.List).
type OperationReader interface {
	// GetOperation returns one operation of b; unknown = domain.ErrNotFound.
	GetOperation(ctx context.Context, b port.BankRef, id port.OperationID) (domain.Operation, error)
	// ListOperations pages the bank's operations, newest first, and returns the next page token.
	ListOperations(ctx context.Context, b port.BankRef, f port.OperationFilter) ([]domain.Operation, string, error)
	// ListEvents returns events with seq > afterSeq (types empty = all) and the last seq scanned.
	ListEvents(
		ctx context.Context, b port.BankRef, afterSeq int64, limit int, types []domain.EventType,
	) ([]domain.LifecycleEvent, int64, error)
}

// The engine implementations satisfy the server's admin interfaces directly (decisions 1205, 1302).
var (
	_ TenantAdmin      = bank.TenantAdmin(nil)
	_ BankAdmin        = bank.BankAdmin(nil)
	_ DirectiveAdmin   = bank.DirectiveAdmin(nil)
	_ DocumentAdmin    = docs.DocumentAdmin(nil)
	_ ObservationAdmin = curate.ObservationAdmin(nil)
	_ MentalModelAdmin = mentalmodel.Admin(nil)
)
