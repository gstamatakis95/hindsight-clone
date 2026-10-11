package gatewayemu

import (
	"net/http"
	"time"
)

// Fault scripts a misbehaviour for the requests recognised as Op. Faults apply in the order they were
// injected; the first one that matches a request and has uses left shapes the response.
type Fault struct {
	// Op is the operation to affect (an Op* constant or a schema name); "" matches every request.
	Op string
	// Times is how many matching requests the fault affects; 0 or less means every one until ClearFaults.
	Times int
	// Delay is waited before responding; it ends early when the request is cancelled.
	Delay time.Duration
	// Status, when non-zero, is returned with Body instead of a normal answer (500 and 429 are retried by the
	// gateway client, 400 is not; a 400 whose body says context_length_exceeded is a context overflow).
	Status int
	// Body is the error message sent with Status; empty means "emulated failure".
	Body string
	// InvalidJSON returns 200 with output that is not valid JSON: the chat content for chats, the whole body
	// for embeddings and rerank.
	InvalidJSON bool
}

// faultState is an injected fault and its remaining uses.
type faultState struct {
	f    Fault
	left int
}

// Inject adds a scripted fault.
func (s *Server) Inject(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = append(s.faults, &faultState{f: f, left: f.Times})
}

// ClearFaults removes every scripted fault.
func (s *Server) ClearFaults() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.faults = nil
}

// takeFault returns the fault for a request recognised as op, consuming one use, or nil.
func (s *Server) takeFault(op string) *Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, fs := range s.faults {
		if fs.f.Op != "" && fs.f.Op != op {
			continue
		}
		f := fs.f
		if fs.f.Times > 0 {
			fs.left--
			if fs.left <= 0 {
				s.faults = append(s.faults[:i:i], s.faults[i+1:]...)
			}
		}
		return &f
	}
	return nil
}

// applyFault runs the fault's delay and, for a status fault, writes the error. It reports whether the
// response has been written (status fault or cancelled request).
func applyFault(w http.ResponseWriter, r *http.Request, f *Fault) (written bool, status int) {
	if f == nil {
		return false, 0
	}
	if f.Delay > 0 {
		t := time.NewTimer(f.Delay)
		defer t.Stop()
		select {
		case <-t.C:
		case <-r.Context().Done():
			// The client is gone; nothing it could read.
			return true, statusClientClosed
		}
	}
	if f.Status != 0 {
		body := f.Body
		if body == "" {
			body = "emulated failure"
		}
		writeError(w, f.Status, body)
		return true, f.Status
	}
	return false, 0
}

// statusClientClosed is the status recorded for a request cancelled during a scripted delay (nginx's 499).
const statusClientClosed = 499

// invalidJSON is the malformed output an InvalidJSON fault returns.
const invalidJSON = `{"emulated": invalid json`
