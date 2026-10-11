package main

import "strings"

// effectiveWorkerID is this process's worker id: "<configured or hostname>-<uuid>". The uuid is fresh per
// process, so two workers started with the same MEMD_WORKER_ID never share an id and the heartbeat fencing
// keeps them apart. Without a configured id or a hostname the prefix is "worker".
func effectiveWorkerID(configured string, hostname func() (string, error), newID func() string) string {
	base := strings.TrimSpace(configured)
	if base == "" {
		if h, err := hostname(); err == nil {
			base = strings.TrimSpace(h)
		}
	}
	if base == "" {
		base = "worker"
	}
	return base + "-" + newID()
}
