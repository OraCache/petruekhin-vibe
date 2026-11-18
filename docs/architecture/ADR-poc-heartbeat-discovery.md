# PoC heartbeat discovery

Status: superseded by [static config + leader hints](ADR-poc-static-leader-hints.md).
The leased-discovery implementation and flags have been removed. The decisions
below are historical, not current startup instructions or availability guarantees.

Original status: implementation policy for the owner-approved ADR-004/RFC-004 PoC addition.
Scope: one region, one partition, one static Raft group, RAM only. This does not
settle the production RFC's authority, HA, membership, persistence or resharding.

## Decisions

- Storage alone configures the static peer list and CP endpoint. CP configures
  region/group identity, not storage addresses or a permanent leader. Discovery
  is opt-in; the existing static configuration API remains separate.
- Heartbeat interval defaults to 5s, discovery TTL to 20s; both are configurable
  on CP (TTL must exceed interval; interval 1s–5m, TTL at most 24h). This TTL
  is not cache-data TTL.
- Heartbeats advertise client address, node/region/group/partition, ready,
  draining, observed leader and term. CP uses its own receipt clock for expiry.
  Expired, not-ready and draining records are excluded from routing; a newer
  valid heartbeat restores freshness without modifying Raft membership.
- A strictly increasing per-process sequence rejects duplicates/reordering
  without refreshing receipt time; per-node term regression is rejected too.
  Sequence high-water marks are retained until CP shutdown. Restart/rejoin of
  an empty storage process under its old ID is unsupported, as in the base PoC.
  A previously unseen delayed higher sequence is indistinguishable from a new
  observation and gets receiver TTL; this is not replay protection.
- Gateway gets a full observation snapshot on every discovery pull, with each
  record's remaining lease in milliseconds (rounded down). Local deadlines use
  the pull's *start* time, conservatively charging the complete RPC duration.
  Each pull has an increasing CP snapshot revision; reordered responses cannot
  renew leases. CP restart requires gateway restart (no durable epochs here).
- Gateway fails closed after local lease expiry even if CP is unavailable.
  Cached preferences and leader hints may select only currently eligible
  discovered addresses, rechecked before every attempt. Existing request and
  attempt deadlines/retry limits remain. An in-flight call is bounded by its
  attempt deadline, not cancelled on discovery expiry.
- Highest observed term among fresh eligible reports controls the initial
  target; conflicting leader observations at that term fall back to deterministic
  candidate probing, never elect a leader. Raft still authorizes every operation.

## Trust boundary

This is trusted-network self-registration over the existing plaintext gRPC.
Validate bounded identifiers, configured region/group/partition, TCP host:port,
positive sequence, roles, duplicate groups, and leader self-report consistency.
There is no authentication, peer allowlist, authorization, cryptographic replay
protection or proof of actual Raft membership. A syntactically valid declaration
is not a membership change or a production registration protocol. Limit catalog
size; no sender wall-clock freshness checks or claimed Byzantine guarantees.

## Operational boundaries

CP loss permits only the remaining local leases, then UNAVAILABLE. CP can have
an empty catalog at startup. Gateway has no storage addresses. TTL suspicion
neither removes a peer nor authorizes restarting it. Recreate the entire
isolated group for a fresh run; never restart an empty former member into a
surviving group. Static mode does not gain lease guarantees.
