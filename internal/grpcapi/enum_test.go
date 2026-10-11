package grpcapi

import (
	"errors"
	"memd/internal/domain"
	"testing"

	pb "memd/gen/memory/v1"
)

// TestUnknownEnumRejected checks an undefined enum number is INVALID_ARGUMENT.
func TestUnknownEnumRejected(t *testing.T) {
	var d decoder
	d.config(&pb.BankConfig{Retain: &pb.RetainConfig{ExtractionMode: pb.ExtractionMode(99)}})
	if err := d.err; err == nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	if got := toEnum[pb.FactType](domain.FactType("bogus")); got != pb.FactType_FACT_TYPE_UNSPECIFIED {
		t.Fatalf("unknown domain value = %v, want UNSPECIFIED", got)
	}
}
