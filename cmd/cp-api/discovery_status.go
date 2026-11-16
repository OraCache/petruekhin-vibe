package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/OraCache/oracache/internal/discovery"
)

// discoveryStatusHandler exposes eligible observations and remaining leases only.
func discoveryStatusHandler(catalog *discovery.Catalog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, _ := catalog.Snapshot(time.Now())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}
