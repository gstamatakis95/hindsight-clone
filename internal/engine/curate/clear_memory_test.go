package curate

import (
	"testing"
)

// TestClearMemoryLeavesPendingRederiveAlone: an edited fact waits for its rederive
// with no embedding and stays stamped consolidated; clearing its observations must not make it
// consolidatable before the rederive writes the embedding.
func TestClearMemoryLeavesPendingRederiveAlone(t *testing.T) {
	e, a := adminEnv(t)
	text := "Alice likes oolong tea"
	req := UpdateMemoryRequest{Bank: testBank, ID: idF1, Text: &text}
	if _, _, err := e.curator().Update(e.ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ClearMemoryObservations(e.ctx, testBank, idF1, false); err != nil {
		t.Fatal(err)
	}
	if e.unconsolidated(idF1) {
		t.Error("f1 is consolidatable with a NULL embedding after the clear")
	}
	e.rederiveNext()
	if !e.unconsolidated(idF1) {
		t.Error("f1 not consolidatable after its rederive")
	}
}
