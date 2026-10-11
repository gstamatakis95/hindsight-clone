//go:build e2e

package e2e

import (
	"context"
	"memd/test/e2e/gatewayemu"
	"memd/test/e2e/harness"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// exampleTimeout bounds one example run.
const exampleTimeout = 4 * time.Minute

// TestExamples builds every program under examples/ and runs each against `memd all` with the emulated gateway,
// the setup examples/README.md describes. An example exits non-zero when the behaviour it demonstrates does not
// hold, so a pass means every scenario worked end to end.
func TestExamples(t *testing.T) {
	root := harness.RepoRoot(t)
	bin := harness.BuildMemd(t)
	s := harness.StartStack(t)
	_, gw := harness.StartEmulator(t, gatewayemu.Options{})
	memd := s.StartMemd(t, bin, harness.MemdOptions{GatewayURL: gw.URL + gatewayemu.BasePath, Config: fastWorker})

	out := t.TempDir()
	build := exec.CommandContext(t.Context(), "go", "build", "-o", out, "./examples/...") //nolint:gosec // our temp dir
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build examples: %v\n%s", err, b)
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no example binaries in %s: %v", out, err)
	}
	for _, e := range entries {
		t.Run(e.Name(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), exampleTimeout)
			defer cancel()
			bin := filepath.Join(out, e.Name())
			cmd := exec.CommandContext(ctx, bin, "-addr", memd.Addr, "-tenant", e2eTenant) //nolint:gosec // just built
			output, err := cmd.CombinedOutput()
			t.Logf("%s", output)
			if err != nil {
				t.Fatalf("%s: %v\n--- memd log tail ---\n%s", e.Name(), err, tail(memd.Logs(), 4000))
			}
		})
	}
}
