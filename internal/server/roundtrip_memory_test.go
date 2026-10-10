package server

import (
	pb "memd/gen/memory/v1"
)

// stripScores clears what a plain domain.Unit cannot carry: scores and the stale flag are recall-only.
func stripScores(ms ...*pb.Memory) {
	for _, m := range ms {
		m.Scores = nil
		m.Stale = false
	}
}

// Round trips for memory.proto (MemoryService).
func init() {
	addRT(func(m *pb.EntityHint) (*pb.EntityHint, error) {
		return entityHintsToProto(entityHintsFromProto([]*pb.EntityHint{m}))[0], nil
	}, nil)
	addRT(func(m *pb.RetainItem) (*pb.RetainItem, error) {
		var d decoder
		return retainItemToProto(d.retainItem(m)), d.err
	}, nil)
	addRT(func(m *pb.RetainRequest) (*pb.RetainRequest, error) {
		r, err := retainRequestFromProto(m)
		return retainRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.RetainedDocument) (*pb.RetainedDocument, error) {
		return retainedDocToProto(retainedDocFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.RetainResponse) (*pb.RetainResponse, error) {
		r, err := retainResultFromProto(m)
		return retainResultToProto(r), err
	}, nil)
	addRT(func(m *pb.RecallInclude) (*pb.RecallInclude, error) {
		return recallIncludeToProto(recallIncludeFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.RecallRequest) (*pb.RecallRequest, error) {
		r, err := recallRequestFromProto(m)
		return recallRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.EntityState) (*pb.EntityState, error) {
		return entityStateToProto(entityStateFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.RecallResponse) (*pb.RecallResponse, error) {
		r, err := recallResultFromProto(m)
		return recallResultToProto(r), err
	}, func(m *pb.RecallResponse) {
		// Results always carry scores (an unset Scores comes back zero); source facts are plain units.
		for _, r := range m.Results {
			if r.Scores == nil {
				r.Scores = &pb.Scores{}
			}
		}
		for _, f := range m.SourceFacts {
			stripScores(f)
		}
	})
	addRT(func(m *pb.ReflectInclude) (*pb.ReflectInclude, error) {
		return reflectIncludeToProto(reflectIncludeFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.ReflectRequest) (*pb.ReflectRequest, error) {
		r, err := reflectRequestFromProto(m)
		return reflectRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.ReflectResponse) (*pb.ReflectResponse, error) {
		r, err := reflectResultFromProto(m)
		return reflectResultToProto(r), err
	}, func(m *pb.ReflectResponse) {
		// BasedOn is a value in the engine: one with no citations is not sent. Cited memories are plain units.
		normBasedOn(m.BasedOn)
		if b := m.BasedOn; b != nil && len(b.Memories) == 0 && len(b.MentalModelIds) == 0 && len(b.DirectiveIds) == 0 {
			m.BasedOn = nil
		}
	})
	addRT(func(m *pb.BasedOn) (*pb.BasedOn, error) {
		var d decoder
		out := basedOnToProto(d.basedOn(m))
		if out == nil { // no citations converts to nil; compare it as the empty message the generator made.
			out = &pb.BasedOn{}
		}
		return out, d.err
	}, normBasedOn)
}

// normBasedOn strips the scores of cited memories, which are plain units in the engine.
func normBasedOn(b *pb.BasedOn) {
	if b != nil {
		stripScores(b.Memories...)
	}
}

// Round trips for memories.proto (MemoryUnitService).
func init() {
	addExempt[*pb.GetMemoryRequest]("ids only; decoded inline by the handler, covered by TestEveryRPCReachable")
	addExempt[*pb.GetObservationHistoryRequest](
		"ids only; decoded inline by the handler, covered by TestEveryRPCReachable")
	addExempt[*pb.GetMemoryResponse]("envelope around Memory (unitToProto)")

	addRT(func(m *pb.ListMemoriesRequest) (*pb.ListMemoriesRequest, error) {
		r, err := listMemoriesRequestFromProto(m)
		return listMemoriesRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.ListMemoriesResponse) (*pb.ListMemoriesResponse, error) {
		p, err := listMemoriesPageFromProto(m)
		return listMemoriesPageToProto(p), err
	}, func(m *pb.ListMemoriesResponse) {
		// Listed items are plain units.
		stripScores(m.Items...)
	})
	addRT(func(m *pb.ObservationVersion) (*pb.ObservationVersion, error) {
		var d decoder
		return observationVersionToProto(d.observationVersion(m)), d.err
	}, nil)
	addRT(func(m *pb.GetObservationHistoryResponse) (*pb.GetObservationHistoryResponse, error) {
		v, err := observationHistoryFromProto(m)
		return observationHistoryToProto(v), err
	}, nil)
	addRT(func(m *pb.EntityList) (*pb.EntityList, error) {
		return entityListToProto(entityListFromProto(m)), nil
	}, nil)
	addRT(func(m *pb.UpdateMemoryRequest) (*pb.UpdateMemoryRequest, error) {
		r, err := updateMemoryRequestFromProto(m)
		return updateMemoryRequestToProto(r), err
	}, nil)
	addRT(func(m *pb.UpdateMemoryResponse) (*pb.UpdateMemoryResponse, error) {
		r, err := updateResultFromProto(m)
		return updateResultToProto(r), err
	}, func(m *pb.UpdateMemoryResponse) {
		// The unit is a value in Curator.Update's result (an unset memory comes back empty) and a plain unit.
		if m.Memory == nil {
			m.Memory = &pb.Memory{}
		}
		stripScores(m.Memory)
	})
}
