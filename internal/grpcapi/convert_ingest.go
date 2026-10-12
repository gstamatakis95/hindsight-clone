package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/memory"

	pb "memd/gen/memory/v1"
)

// Converter for memory.v1.IngestEnvelope, the queue message internal/ingestapi consumes.

// IngestEnvelopeFromProto converts a queue message's envelope into a one-item RetainBatch request for tenant (the
// consumer settles which tenant a message is for first). It checks what the RetainBatch handler checks before the
// memory service runs: the tenant, org and bank ids, that the envelope has an item and that the item decodes, and
// the request-level overrides. Any problem is domain.ErrInvalidArgument naming the envelope field; the item's own
// rules (document_id, size, item overrides) are left to RetainBatch, which rejects the item.
func IngestEnvelopeFromProto(tenant string, e *pb.IngestEnvelope) (memory.RetainBatchRequest, error) {
	if err := domain.CheckName("tenant_id", "tenant", tenant); err != nil {
		return memory.RetainBatchRequest{}, err //nolint:wrapcheck // an InvalidField, already the domain class
	}
	if err := checkName("org_id", e.GetOrgId()); err != nil {
		return memory.RetainBatchRequest{}, err
	}
	if err := checkName("bank_id", e.GetBankId()); err != nil {
		return memory.RetainBatchRequest{}, err
	}
	if e.GetItem() == nil {
		return memory.RetainBatchRequest{}, domain.InvalidField("item", "the envelope has no item")
	}
	var d decoder
	out := memory.RetainBatchRequest{
		Bank:              domain.NewBankRef(tenant, e.GetOrgId(), e.GetBankId()),
		Items:             []memory.RetainItem{d.retainItem(e.GetItem())},
		DocumentTags:      e.GetDocumentTags(),
		Overrides:         d.retainOverrides(e.GetOverrides()),
		SkipConsolidation: e.GetSkipConsolidation(),
	}
	if d.err != nil {
		return memory.RetainBatchRequest{}, d.err
	}
	if err := validate(out.Overrides); err != nil {
		return memory.RetainBatchRequest{}, err
	}
	return out, nil
}
