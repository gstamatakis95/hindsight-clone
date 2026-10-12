package grpcapi

import (
	"memd/internal/domain"
	"memd/internal/memory"
	"slices"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	pb "memd/gen/memory/v1"
)

// Converters for MemoryService's listing and curation.

// listMemoriesRequestFromProto converts a ListMemoriesRequest; Bank carries only bank_id (the handler sets
// the tenant).
func listMemoriesRequestFromProto(r *pb.ListMemoriesRequest) (memory.ListRequest, error) {
	var d decoder
	out := memory.ListRequest{
		FactTypes:    fromEnums[domain.FactType](&d, r.GetFactTypes()),
		TextContains: r.GetTextContains(),
		DocumentID:   domain.DocumentID(r.GetDocumentId()),
		EntityID:     domain.EntityID(r.GetEntityId()),
		State:        fromEnum[domain.MemoryState](&d, r.GetState()),
		Start:        d.tsPtr(r.GetStart()),
		End:          d.tsPtr(r.GetEnd()),
		TimeField:    fromEnum[domain.TimeField](&d, r.GetTimeField()),
		Tags:         d.tagFilter(r.GetTags()),
		PageSize:     int(r.GetPageSize()),
		PageToken:    r.GetPageToken(),
	}
	return out, d.err
}

// listMemoriesPageToProto converts a memory page to a ListMemoriesResponse.
func listMemoriesPageToProto(p memory.Page) *pb.ListMemoriesResponse {
	return &pb.ListMemoriesResponse{Memories: unitsToProto(p.Items), NextPageToken: p.NextPageToken}
}

// memoryEditFields returns the MemoryEdit fields an UpdateMemory applies: the mask's paths, each of which
// must name a top-level MemoryEdit field, or the populated fields of ed when the mask is empty.
func memoryEditFields(ed *pb.MemoryEdit, mask *fieldmaskpb.FieldMask) ([]memory.EditField, error) {
	var out []memory.EditField
	if len(mask.GetPaths()) == 0 {
		ed.ProtoReflect().Range(func(fd protoreflect.FieldDescriptor, _ protoreflect.Value) bool {
			out = append(out, memory.EditField(fd.Name()))
			return true
		})
		return out, nil
	}
	fields := ed.ProtoReflect().Descriptor().Fields()
	for _, p := range mask.GetPaths() {
		if fields.ByName(protoreflect.Name(p)) == nil {
			return nil, domain.InvalidField("update_mask", "invalid update_mask path %q for %s", p,
				ed.ProtoReflect().Descriptor().FullName())
		}
		if f := memory.EditField(p); !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out, nil
}

// updateMemoryRequestFromProto converts an UpdateMemoryRequest; Bank carries only bank_id (the handler
// sets the tenant).
func updateMemoryRequestFromProto(r *pb.UpdateMemoryRequest) (memory.UpdateRequest, error) {
	ed := r.GetMemory()
	if ed == nil {
		ed = &pb.MemoryEdit{}
	}
	fields, err := memoryEditFields(ed, r.GetUpdateMask())
	if err != nil {
		return memory.UpdateRequest{}, err
	}
	var d decoder
	out := memory.UpdateRequest{
		ID:   domain.UnitID(r.GetMemoryId()),
		Edit: memory.Edit{
			Text:          ed.GetText(),
			Context:       ed.GetContext(),
			OccurredStart: d.tsPtr(ed.GetOccurredStart()),
			OccurredEnd:   d.tsPtr(ed.GetOccurredEnd()),
			FactType:      fromEnum[domain.FactType](&d, ed.GetFactType()),
			Entities:      slices.Clone(ed.GetEntities()),
		},
		Fields:          fields,
		ResolveEntities: optCopy(r.ResolveEntities),
		Reason:          r.GetReason(),
		Overrides:       rederiveOverrides(r.GetOverrides()),
	}
	return out, d.err
}

// restoreMemoryRequestFromProto converts a RestoreMemoryRequest; Bank carries only bank_id (the handler sets
// the tenant).
func restoreMemoryRequestFromProto(r *pb.RestoreMemoryRequest) memory.RestoreRequest {
	return memory.RestoreRequest{
		ID:              domain.UnitID(r.GetMemoryId()),
		Reason:          r.GetReason(),
		ResolveEntities: optCopy(r.ResolveEntities),
		Overrides:       rederiveOverrides(r.GetOverrides()),
	}
}
