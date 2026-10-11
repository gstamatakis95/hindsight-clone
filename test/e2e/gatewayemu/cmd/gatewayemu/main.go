// Command gatewayemu serves the deterministic gateway emulator (memd/test/e2e/gatewayemu) on a TCP address,
// so memd can run locally without a real AI gateway: the README quick start points MEMD_GATEWAY_URL at it
// and binds every slot to its emu-<slot> model. It answers every prompt memd sends with schema-valid
// output and 768-dimensional embeddings; it is for development and tests, not for real answers.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"memd/test/e2e/gatewayemu"
	"net/http"
	"os"
	"strings"
	"time"
)

// main serves the emulator until killed.
func main() {
	addr := flag.String("addr", "127.0.0.1:4000", "listen address")
	flag.Parse()
	srv := &http.Server{Addr: *addr, Handler: gatewayemu.New(gatewayemu.Options{}), ReadHeaderTimeout: 10 * time.Second}
	var env []string
	for _, slot := range []string{
		"extract", "time_parse", "consolidate", "dedup_check", "reflect", "structured_output",
		"mental_model_delta", "synthesize", "rerank",
	} {
		env = append(env, fmt.Sprintf("MEMD_MODEL_%s=%s", strings.ToUpper(slot), gatewayemu.ModelFor(slot)))
	}
	log.Printf("gatewayemu: serving http://%s%s\n  export MEMD_GATEWAY_URL=http://%s%s %s",
		*addr, gatewayemu.BasePath, *addr, gatewayemu.BasePath, strings.Join(env, " "))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		os.Exit(1)
	}
}
