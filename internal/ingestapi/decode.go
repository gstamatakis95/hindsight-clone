package ingestapi

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/grpcapi"
	"memd/internal/ingest"
	"memd/internal/memory"
	"slices"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pb "memd/gen/memory/v1"
)

// item is one decoded message on its way into a batch.
type item struct {
	msg ingest.Message
	// req is the message's one-item RetainBatch request.
	req memory.RetainBatchRequest
	// key groups items that can share a RetainBatch call: same bank and same request-level fields.
	key string
}

// batch is one RetainBatch call and the messages its items came from, in item order.
type batch struct {
	key   string
	req   memory.RetainBatchRequest
	msgs  []ingest.Message
	bytes int
}

// decode turns a message into an item, or into the settlement of a message that can't be retained: an
// undecodable body (UNDECODABLE), an envelope for another tenant or org than the pinned one (TENANT_MISMATCH,
// ORG_MISMATCH), or an invalid envelope (its domain reason, INVALID_ARGUMENT): each is dead-lettered.
func (c *Consumer) decode(m ingest.Message) (*item, decision) {
	var e pb.IngestEnvelope
	if err := unmarshal(m, &e); err != nil {
		return nil, deadLetter(reasonUndecodable, err.Error())
	}
	tenant, reason, detail := pin(c.cfg.Tenant, e.GetTenantId(), "tenant_id", reasonTenantMismatch)
	if reason != "" {
		return nil, deadLetter(reason, detail)
	}
	org, reason, detail := pin(c.cfg.Org, e.GetOrgId(), "org_id", reasonOrgMismatch)
	if reason != "" {
		return nil, deadLetter(reason, detail)
	}
	e.OrgId = org
	req, err := grpcapi.IngestEnvelopeFromProto(tenant, &e)
	if err != nil {
		return nil, deadLetter(reasonOf(err), err.Error())
	}
	return &item{msg: m, req: req, key: groupKey(&e, req.Bank)}, decision{}
}

// unmarshal decodes the message body by its content-type attribute: protojson by default, binary protobuf for
// ingest.ContentTypeProto. Unknown JSON fields are errors, so a misspelt field never drops data silently.
func unmarshal(m ingest.Message, e *pb.IngestEnvelope) error {
	switch ct := m.Attributes[ingest.AttrContentType]; ct {
	case "", ingest.ContentTypeJSON:
		return protojson.Unmarshal(m.Body, e) //nolint:wrapcheck // the error text is the dead letter's detail
	case ingest.ContentTypeProto:
		return proto.Unmarshal(m.Body, e) //nolint:wrapcheck // the error text is the dead letter's detail
	default:
		return fmt.Errorf("unsupported %s %q (want %s or %s)", ingest.AttrContentType, ct, ingest.ContentTypeJSON,
			ingest.ContentTypeProto)
	}
}

// pin resolves a tenant or org id against the pinned one: empty takes the pinned id, a different one is the
// mismatch reason. Unpinned, the envelope's id stands (and is validated later).
func pin(pinned, got, field, mismatch string) (id, reason, detail string) {
	switch {
	case pinned == "" || got == pinned:
		return got, "", ""
	case got == "":
		return pinned, "", ""
	default:
		return "", mismatch, fmt.Sprintf("%s %q is not the pinned %q", field, got, pinned)
	}
}

// groupKey is the key of the RetainBatch call an envelope can join: its bank plus its request-level fields
// (document tags, overrides, skip_consolidation), which a call applies to all its items. The fields are compared
// as their deterministic binary encoding.
func groupKey(e *pb.IngestEnvelope, b domain.BankRef) string {
	shared := &pb.IngestEnvelope{
		DocumentTags: e.GetDocumentTags(), Overrides: e.GetOverrides(), SkipConsolidation: e.GetSkipConsolidation(),
	}
	raw, _ := proto.MarshalOptions{Deterministic: true}.Marshal(shared) // a decoded message always encodes
	return fmt.Sprintf("%q/%q/%q/%x", b.Tenant, b.Org, b.Bank, raw)
}

// packBatches adds it to the first batch of its key that stays within Config.BatchSize items and
// Config.MaxRequestBytes of content with it, else starts a new batch. Batches keep the order their first message
// arrived in.
func (c *Consumer) packBatches(batches []*batch, it *item) []*batch {
	size := len(it.req.Items[0].Content)
	i := slices.IndexFunc(batches, func(b *batch) bool {
		return b.key == it.key && len(b.msgs) < c.cfg.BatchSize && b.bytes+size <= c.cfg.MaxRequestBytes
	})
	if i < 0 {
		req := it.req
		req.Items = nil
		batches = append(batches, &batch{key: it.key, req: req})
		i = len(batches) - 1
	}
	b := batches[i]
	b.req.Items = append(b.req.Items, it.req.Items[0])
	b.msgs = append(b.msgs, it.msg)
	b.bytes += size
	return batches
}
