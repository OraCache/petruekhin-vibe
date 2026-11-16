package gateway

import "github.com/OraCache/oracache/internal/leaderhint"

// Preferences are regional: a local read must not teach the master write path
// an address from another region.
type forwardRoute struct {
	partition uint32
	region    string
}

type routeToken struct {
	observation leaderhint.Observation
	leader      Leader
}

type preference struct {
	address string
	token   routeToken
}

// forwardRoute reads leader, replicas and observation from one immutable CP
// snapshot. The token changes only when the observation or resolved leader does,
// not on every refresh or request.
func (r *Router) forwardRoute(partition uint32, write bool) (Leader, []Leader, routeToken, error) {
	s := r.cur.Load()
	if s == nil {
		return Leader{}, nil, routeToken{}, ErrNoConfig
	}
	routes, replicas := s.local, s.localReplicas
	if write {
		routes, replicas = s.write, s.writeReplicas
	}
	leader, err := hintedLeader(s, routes, replicas, partition)
	if err != nil {
		return Leader{}, nil, routeToken{}, err
	}
	token := routeToken{leader: leader, observation: s.hints[leaderhint.Key{Region: leader.Region, Partition: partition}]}
	return leader, replicas[partition], token, nil
}

// configuredReplica is an address allowlist, not a heartbeat eligibility lease.
// Recheck before each attempt in case a newer static config removed a replica.
func (r *Router) configuredReplica(partition uint32, write bool, address string) bool {
	replicas, err := r.Replicas(partition, write)
	if err != nil {
		return false
	}
	for _, replica := range replicas {
		if replica.Address == address {
			return true
		}
	}
	return false
}
