package main

import (
	"encoding/json"
	"fmt"
	"github.com/OraCache/oracache/internal/cpapi"
	"github.com/OraCache/oracache/internal/discovery"
	"net/http"
	"time"
)

// discoveryStatusHandler exposes eligible observations and remaining leases only.
func discoveryStatusHandler(catalog *discovery.Catalog) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		snapshot, _ := catalog.Snapshot(time.Now())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot)
	})
}

// Discovery is opt-in and cannot be combined with static placement.
func discoveryOptions(region, group, configPath string, ttl, interval time.Duration) ([]cpapi.Option, *discovery.Catalog, error) {
	if region == "" && group == "" {
		return nil, nil, nil
	}
	if configPath != "" {
		return nil, nil, fmt.Errorf("discovery and --config are mutually exclusive")
	}
	// Match the CP agent's supported interval range; do not advertise an interval
	// which the agent will silently clamp or truncate to zero milliseconds.
	if interval < time.Second || interval > 5*time.Minute || ttl <= interval {
		return nil, nil, fmt.Errorf("discovery TTL must exceed heartbeat interval (1s..5m)")
	}
	catalog, err := discovery.New(region, group, ttl)
	if err != nil {
		return nil, nil, err
	}
	return []cpapi.Option{cpapi.WithDiscovery(catalog), cpapi.WithHeartbeatInterval(interval)}, catalog, nil
}
