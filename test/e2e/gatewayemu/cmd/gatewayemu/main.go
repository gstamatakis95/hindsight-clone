package main

import (
	"errors"
	"flag"
	"log"
	"memd/test/e2e/gatewayemu"
	"net/http"
	"os"
	"time"
)

// main serves the emulator until killed.
func main() {
	addr := flag.String("addr", "127.0.0.1:4000", "listen address")
	config := flag.String("write-config", "", "write a memd server config file binding every slot to the emulator")
	flag.Parse()
	if *config != "" {
		if err := os.WriteFile(*config, []byte(gatewayemu.ServerConfig()), 0o600); err != nil {
			log.Fatal(err)
		}
		log.Printf("gatewayemu: wrote %s; run memd with MEMD_CONFIG=%s", *config, *config)
	}
	srv := &http.Server{Addr: *addr, Handler: gatewayemu.New(gatewayemu.Options{}), ReadHeaderTimeout: 10 * time.Second}
	log.Printf("gatewayemu: serving http://%s%s\n  export MEMD_GATEWAY_URL=http://%s%s",
		*addr, gatewayemu.BasePath, *addr, gatewayemu.BasePath)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Print(err)
		os.Exit(1)
	}
}
