package grpcapi

import (
	"fmt"
	"memd/internal/domain"
	"memd/internal/memory"

	"google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/grpc/status"

	pb "memd/gen/memory/v1"
)

// Converters for MemoryService.RetainBatch.

// retainBatchRequestFromProto converts a RetainBatchRequest; the handler sets Bank. Each item is decoded on its
// own: an item that fails to decode is rejected with its error (memory.RetainBatchRequest.Rejected) instead of
// failing the call. A malformed request-level field fails the call.
func retainBatchRequestFromProto(r *pb.RetainBatchRequest) (memory.RetainBatchRequest, error) {
	var d decoder
	out := memory.RetainBatchRequest{
		Items:             make([]memory.RetainItem, len(r.GetItems())),
		DocumentTags:      r.GetDocumentTags(),
		Overrides:         d.retainOverrides(r.GetOverrides()),
		SkipConsolidation: r.GetSkipConsolidation(),
	}
	for k, it := range r.GetItems() {
		var id decoder
		out.Items[k] = id.retainItem(it)
		if id.err == nil {
			continue
		}
		if out.Rejected == nil {
			out.Rejected = make([]error, len(out.Items))
		}
		out.Rejected[k] = id.err
	}
	return out, d.err
}

// batchStatuses maps memory.BatchStatus to the proto enum.
var batchStatuses = map[memory.BatchStatus]pb.RetainBatchResult_Status{
	memory.BatchAccepted:   pb.RetainBatchResult_STATUS_ACCEPTED,
	memory.BatchUnchanged:  pb.RetainBatchResult_STATUS_UNCHANGED,
	memory.BatchStale:      pb.RetainBatchResult_STATUS_STALE,
	memory.BatchSuperseded: pb.RetainBatchResult_STATUS_SUPERSEDED,
	memory.BatchRejected:   pb.RetainBatchResult_STATUS_REJECTED,
	memory.BatchFailed:     pb.RetainBatchResult_STATUS_FAILED,
}

// retainBatchResultToProto converts the per-item results.
func retainBatchResultToProto(r memory.RetainBatchResult) *pb.RetainBatchResponse {
	out := &pb.RetainBatchResponse{Results: make([]*pb.RetainBatchResult, len(r.Items))}
	for k, it := range r.Items {
		out.Results[k] = &pb.RetainBatchResult{
			DocumentId: string(it.DocumentID), Status: batchStatuses[it.Status],
			Operation: operationPtrToProto(it.Operation), Error: itemErrorToProto(k, it.Err),
		}
	}
	return out
}

// itemErrorToProto converts the error of item k into what a unary call would carry in its status: the canonical
// code name, the ErrorInfo reason, the message and the field violations, each field prefixed with items[k].
// nil stays nil.
func itemErrorToProto(k int, err error) *pb.ItemError {
	if err == nil {
		return nil
	}
	st := status.Convert(toStatus(err))
	out := &pb.ItemError{
		Code:   code.Code_name[int32(st.Code())], //nolint:gosec // gRPC codes are 0..16
		Reason: reasonFor(st.Code(), err), Message: st.Message(),
	}
	for _, v := range domain.FieldViolations(err) {
		out.Fields = append(out.Fields, fmt.Sprintf("items[%d].%s", k, v.Field))
	}
	return out
}
