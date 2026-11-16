package main

import (
	"encoding/json"
	"net/http"

	cp "github.com/OraCache/oracache/gen/controlplane/v1"
	"github.com/OraCache/oracache/internal/cpapi"
)

// statusHandler reads the same config and term-ordered hints served to gateways.
// It does not register nodes, expire routes, or change configuration.
func statusHandler(api *cpapi.Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "read only", http.StatusMethodNotAllowed)
			return
		}
		snapshot, err := api.GetConfig(r.Context(), &cp.GetConfigRequest{NodeId: "status", Kind: cp.AgentKind_AGENT_KIND_GATEWAY})
		if err != nil || snapshot.GetConfig() == nil {
			http.Error(w, "no config", http.StatusServiceUnavailable)
			return
		}
		hints := snapshot.GetLeaderHints()
		if hints == nil {
			hints = []*cp.LeaderHint{}
		}
		out := struct {
			ConfigRevision uint64            `json:"config_revision"`
			Config         *cp.ClusterConfig `json:"config"`
			LeaderHints    []*cp.LeaderHint  `json:"leader_hints"`
		}{snapshot.Revision, snapshot.Config, hints}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}
