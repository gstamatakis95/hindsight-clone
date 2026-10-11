package server

import (
	"errors"
	"memd/internal/domain"
	"testing"

	pb "memd/gen/memory/v1"
)

// TestUnknownEnumRejected checks an undefined enum number is INVALID_ARGUMENT.
func TestUnknownEnumRejected(t *testing.T) {
	_, err := configFromProto(&pb.BankConfig{ExtractionMode: pb.ExtractionMode(99)})
	if err == nil || !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	if got := toEnum[pb.FactType](domain.FactType("bogus")); got != pb.FactType_FACT_TYPE_UNSPECIFIED {
		t.Fatalf("unknown domain value = %v, want UNSPECIFIED", got)
	}
}
