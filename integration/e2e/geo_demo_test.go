//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	pb "github.com/OraCache/oracache/gen/gateway/v1"
	membership "github.com/OraCache/oracache/gen/membership/v1"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestStagedGeoDemo(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	project := "oracache-geo-e2e-" + uuid.NewString()
	image := os.Getenv("ORACACHE_CP_IMAGE")
	build := image == ""
	if build {
		image = project + ":local"
	}
	command := func(ctx context.Context, name string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+project, "COMPOSE_PROFILES=", "ORACACHE_GEO_IMAGE="+image, "ORACACHE_GEO_HOST_PORT=0")
		out, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); ok {
			err = fmt.Errorf("%w: %s", err, exit.Stderr)
		}
		return strings.TrimSpace(string(out)), err
	}
	compose := func(ctx context.Context, args ...string) (string, error) {
		return command(ctx, "docker", append([]string{"compose", "-f", "compose.geo-demo.yaml"}, args...)...)
	}
	makeDemo := func(targets ...string) {
		t.Helper()
		args := []string{"-j1"}
		if !build {
			args = append(args, "-o", "demo-build") // CI already built the image.
		}
		if out, err := command(ctx, "make", append(args, targets...)...); err != nil {
			t.Fatalf("make %v: %v\n%s", targets, err, out)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := compose(ctx, args...)
		if err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
		return out
	}
	profiles := []string{"--profile", "region-b", "--profile", "learner"}
	bNodes := []string{"b-1", "b-2", "b-3"}
	bPaused := false
	t.Logf("Compose project: %s", project)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if bPaused {
			_, _ = compose(cleanup, append([]string{"--profile", "region-b", "unpause"}, bNodes...)...)
		}
		if t.Failed() {
			out, err := compose(cleanup, append(slices.Clone(profiles), "logs", "--tail=25")...)
			t.Logf("Compose logs: %v\n%s", err, out)
		}
		if out, err := command(cleanup, "make", "demo-down"); err != nil {
			t.Errorf("make demo-down: %v\n%s", err, out)
		}
		if build {
			// A failed build may not have created the image.
			if exec.CommandContext(cleanup, "docker", "image", "inspect", image).Run() == nil {
				if out, err := exec.CommandContext(cleanup, "docker", "image", "rm", image).CombinedOutput(); err != nil {
					t.Errorf("remove test image: %v\n%s", err, out)
				}
			}
		}
	})
	if !build {
		if out, err := exec.CommandContext(ctx, "docker", "image", "inspect", image).CombinedOutput(); err != nil {
			t.Fatalf("prebuilt ORACACHE_CP_IMAGE %q required: %v\n%s", image, err, out)
		}
	}
	absent := func(services ...string) {
		t.Helper()
		present := strings.Fields(run(append(slices.Clone(profiles), "ps", "--all", "--services")...))
		for _, service := range services {
			if slices.Contains(present, service) {
				t.Fatalf("%s started before its stage; project services: %v", service, present)
			}
		}
	}
	port := func(service, internal string) string { return run("port", service, internal) }
	dial := func(endpoint string) *grpc.ClientConn {
		conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		return conn
	}
	httpClient := &http.Client{Timeout: time.Second}
	defer httpClient.CloseIdleConnections()
	getJSON := func(ctx context.Context, endpoint string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+endpoint, nil)
		if err != nil {
			return err
		}
		res, err := httpClient.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("GET %s: %s", endpoint, res.Status)
		}
		return json.NewDecoder(res.Body).Decode(out)
	}

	makeDemo("demo-init", "NODES=a-1 a-2 a-3")
	makeDemo("demo-up-a", "demo-up-monitoring")
	absent(append([]string{"a-4"}, bNodes...)...)
	conn := dial(port("api-gateway", "28080"))
	cache := pb.NewCacheGatewayClient(conn)
	admin := membership.NewMembershipAdminClient(conn)
	routes := "http://" + port("api-gateway", "9466") + "/routes"
	first := waitCPRoute(t, ctx, routes, "")
	aVoters := []string{"a-1", "a-2", "a-3"}
	if !slices.Contains(aVoters, first.Write[0].NodeID) {
		t.Fatalf("initial write route: %+v", first)
	}
	const key = "demo:geo"
	put := func(value string, expected uint64) *pb.PutResponse {
		t.Helper()
		rpc, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := cache.Put(rpc, &pb.PutRequest{Key: key, Data: []byte(value), ExpectedRev: proto.Uint64(expected)})
		if err != nil || out.GetStatus() != pb.PutStatus_PUT_STATUS_OK || out.GetRev() <= expected {
			t.Fatalf("Put %q (expected rev %d): response=%v error=%v", value, expected, out, err)
		}
		return out
	}
	read := func(ctx context.Context, cache pb.CacheGatewayClient, value string, rev uint64, node string) error {
		out, err := cache.Get(ctx, &pb.GetRequest{Key: key, Rev: rev})
		if err == nil && out.GetStatus() == pb.GetStatus_GET_STATUS_HIT && out.GetRev() == rev &&
			string(out.GetData()) == value && (node == "" || out.GetServedBy().GetInstance() == node) {
			return nil
		}
		return fmt.Errorf("Get: response=%v error=%v; want %q rev %d from %s", out, err, value, rev, node)
	}
	seed := put("before-b", 0)
	if seed.GetServedBy().GetInstance() != first.Write[0].NodeID {
		t.Fatalf("initial Put used %v, want %s", seed.GetServedBy(), first.Write[0].NodeID)
	}
	waitGeo(t, ctx, "seed through gateway", func(ctx context.Context) error {
		return read(ctx, cache, "before-b", seed.GetRev(), first.Write[0].NodeID)
	})

	prom := port("prometheus", "9090") + "/api/v1/"
	wantHealth := map[string]string{
		"cp-api:9464": "up", "api-gateway:9466": "up",
		"a-1:28191": "up", "a-2:28191": "up", "a-3:28191": "up", "a-4:28191": "down",
		"b-1:28191": "down", "b-2:28191": "down", "b-3:28191": "down",
	}
	checkTargets := func() {
		t.Helper()
		waitGeo(t, ctx, fmt.Sprintf("Prometheus targets %v", wantHealth), func(ctx context.Context) error {
			var out struct {
				Status string
				Data   struct {
					ActiveTargets []struct {
						Labels map[string]string
						Health string
					}
				}
			}
			if err := getJSON(ctx, prom+"targets", &out); err != nil {
				return err
			}
			got := make(map[string]string)
			for _, target := range out.Data.ActiveTargets {
				got[target.Labels["instance"]] = target.Health
			}
			if out.Status != "success" || !maps.Equal(got, wantHealth) {
				return fmt.Errorf("status=%s targets=%v", out.Status, got)
			}
			return nil
		})
	}
	checkTargets()
	grafana := port("grafana", "3000") + "/api/dashboards/uid/oracache-mvp"
	var rolesQuery string
	waitGeo(t, ctx, "MVP dashboard provisioned", func(ctx context.Context) error {
		var out struct {
			Meta      struct{ Provisioned bool }
			Dashboard struct {
				UID    string
				Panels []struct {
					ID      int
					Targets []struct{ Expr, LegendFormat string }
				}
			}
		}
		if err := getJSON(ctx, grafana, &out); err != nil {
			return err
		}
		if !out.Meta.Provisioned || out.Dashboard.UID != "oracache-mvp" {
			return fmt.Errorf("dashboard: %+v", out)
		}
		for _, panel := range out.Dashboard.Panels {
			if panel.ID == 4 && len(panel.Targets) == 1 && panel.Targets[0].LegendFormat == "{{node_id}}" {
				rolesQuery = panel.Targets[0].Expr
				return nil
			}
		}
		return fmt.Errorf("MVP role timeline must show node_id")
	})
	t.Logf("A accepted revision %d while B and a-4 were absent", seed.GetRev())

	makeDemo("demo-init", "NODES=b-1 b-2 b-3")
	makeDemo("demo-up-b")
	absent("a-4")
	type replicaStatus struct {
		Role         string
		Ready        bool
		IsLearner    bool
		Member       bool
		Running      bool
		AppliedIndex uint64
	}
	bStatuses := make(map[string]string)
	bClients := make(map[string]pb.CacheGatewayClient)
	for _, node := range bNodes {
		wantHealth[node+":28191"] = "up"
		bStatuses[node] = port(node, "28191") + "/status"
		bClients[node] = pb.NewCacheGatewayClient(dial(port(node, "28091")))
	}
	checkTargets()
	verifyB := func(value string, rev uint64) {
		t.Helper()
		waitGeo(t, ctx, fmt.Sprintf("B leader has %q at source revision %d", value, rev), func(ctx context.Context) error {
			var states []string
			for _, node := range bNodes {
				var st replicaStatus
				err := getJSON(ctx, bStatuses[node], &st)
				if err == nil && st.Ready && st.Role == "StateLeader" {
					// Direct storage replies do not carry the gateway's ServedBy metadata.
					if err := read(ctx, bClients[node], value, rev, ""); err != nil {
						return fmt.Errorf("direct read from %s: %w", node, err)
					}
					t.Logf("direct read from %s confirmed revision %d", node, rev)
					return nil
				}
				states = append(states, fmt.Sprintf("%s: %+v (%v)", node, st, err))
			}
			return fmt.Errorf("no ready B leader: %s", strings.Join(states, "; "))
		})
	}
	verifyB("before-b", seed.GetRev())

	makeDemo("demo-init", "NODES=a-4")
	makeDemo("demo-up-learner")
	wantHealth["a-4:28191"] = "up"
	checkTargets()
	learnerStatus := port("a-4", "28191") + "/status"
	const group, region = "region-a/shard-0/replication-1", "region-a"
	getMembership := func(ctx context.Context) (*membership.Membership, error) {
		return admin.GetMembership(ctx, &membership.GetMembershipRequest{GroupId: group, Region: region})
	}
	member := func(m *membership.Membership, id uint64) *membership.Member {
		for _, peer := range m.GetMembers() {
			if peer.GetPeer().GetId() == id {
				return peer
			}
		}
		return nil
	}
	waitGeo(t, ctx, "a-4 is scraped but not attached", func(ctx context.Context) error {
		var st replicaStatus
		if err := getJSON(ctx, learnerStatus, &st); err != nil {
			return err
		}
		m, err := getMembership(ctx)
		if err == nil && st.Running && !st.Member && !st.IsLearner && len(m.GetMembers()) == 3 && member(m, 4) == nil {
			return nil
		}
		return fmt.Errorf("a-4 status=%+v membership=%v error=%v", st, m, err)
	})
	change := func(req *membership.ChangeMembershipRequest) *membership.ChangeMembershipResponse {
		t.Helper()
		req.GroupId, req.Region = group, region
		var out *membership.ChangeMembershipResponse
		waitGeo(t, ctx, req.OperationId, func(ctx context.Context) error {
			var err error
			out, err = admin.ChangeMembership(ctx, req)
			switch status.Code(err) {
			case codes.OK, codes.Unavailable, codes.DeadlineExceeded, codes.FailedPrecondition:
				return err
			default:
				t.Fatalf("%s: %v", req.OperationId, err)
				return err
			}
		})
		if out.GetOperationId() != req.OperationId || out.GetChangeIndex() == 0 {
			t.Fatalf("%s: unexpected response %v", req.OperationId, out)
		}
		return out
	}
	added := change(&membership.ChangeMembershipRequest{OperationId: "add-a4", Action: "add-learner", Peer: &membership.Peer{
		Id: 4, NodeId: "a-4", Address: "a-4:28091", RaftAddress: "http://a-4:28191/raft",
	}})
	var old *membership.Peer
	waitGeo(t, ctx, "a-4 applies its learner entry and A has a follower to replace", func(ctx context.Context) error {
		var st replicaStatus
		if err := getJSON(ctx, learnerStatus, &st); err != nil {
			return err
		}
		m, err := getMembership(ctx)
		learner := member(m, 4)
		if err == nil && st.IsLearner && st.AppliedIndex >= added.GetChangeIndex() &&
			learner.GetLearner() && !learner.GetVoter() && m.GetLeaderId() != 0 && !m.GetJoint() {
			for _, peer := range m.GetMembers() {
				if peer.GetVoter() && peer.GetPeer().GetId() != m.GetLeaderId() && slices.Contains(aVoters, peer.GetPeer().GetNodeId()) {
					old = peer.GetPeer()
					return nil
				}
			}
		}
		return fmt.Errorf("a-4 status=%+v membership=%v error=%v", st, m, err)
	})
	change(&membership.ChangeMembershipRequest{OperationId: "replace-with-a4", Action: "replace", PeerId: 4, RemoveId: old.GetId()})
	waitGeo(t, ctx, "finalized A membership", func(ctx context.Context) error {
		m, err := getMembership(ctx)
		voter := member(m, 4)
		if err == nil && !m.GetJoint() && len(m.GetMembers()) == 3 && member(m, old.GetId()) == nil &&
			slices.Contains(m.GetRemovedIds(), old.GetId()) && voter.GetVoter() && !voter.GetLearner() {
			return nil
		}
		return fmt.Errorf("membership: %v, error: %v", m, err)
	})
	makeDemo("demo-stop-node", "ID="+old.GetNodeId())
	wantHealth[old.GetNodeId()+":28191"] = "down"
	checkTargets()
	aVoters[slices.Index(aVoters, old.GetNodeId())] = "a-4"
	var serving string
	waitGeo(t, ctx, "surviving A route serves the original value", func(ctx context.Context) error {
		view, err := readCPRoutes(ctx, httpClient, routes)
		serving = view.Write[0].NodeID
		if err != nil || !slices.Contains(aVoters, serving) || serving != view.Read[0].NodeID {
			return fmt.Errorf("routes=%+v error=%v", view, err)
		}
		return read(ctx, cache, "before-b", seed.GetRev(), serving)
	})
	updated := put("after-replace", seed.GetRev())
	if updated.GetServedBy().GetInstance() != serving {
		t.Fatalf("post-replacement Put used %v, want %s", updated.GetServedBy(), serving)
	}
	verifyB("after-replace", updated.GetRev())
	t.Logf("replaced %s with a-4; gateway write advanced revision %d → %d", old.GetNodeId(), seed.GetRev(), updated.GetRev())
	waitGeo(t, ctx, "timeline identifies the leader and the stopped node by node_id", func(ctx context.Context) error {
		var out struct {
			Data struct {
				Result []struct {
					Metric map[string]string
					Value  []json.RawMessage
				}
			}
		}
		if err := getJSON(ctx, prom+"query?query="+url.QueryEscape(rolesQuery), &out); err != nil {
			return err
		}
		roles := make(map[string]string)
		for _, series := range out.Data.Result {
			if len(series.Value) == 2 {
				roles[series.Metric["node_id"]] = string(series.Value[1])
			}
		}
		if roles[serving] != `"2"` || roles[old.GetNodeId()] != `"-1"` {
			return fmt.Errorf("expected leader %s and stopped node %s in the timeline, got %v", serving, old.GetNodeId(), roles)
		}
		return nil
	})
	run("kill", "-s", "SIGKILL", serving)
	successor := waitCPRoute(t, ctx, routes, serving).Write[0].NodeID
	waitGeo(t, ctx, "successor reads the last acknowledged value", func(ctx context.Context) error {
		return read(ctx, cache, "after-replace", updated.GetRev(), successor)
	})
	// Leave an unacknowledged WAN change to be delivered after A's cold restart.
	bPaused = true
	run(append([]string{"--profile", "region-b", "pause"}, bNodes...)...)
	afterKill := put("after-kill", updated.GetRev())
	if afterKill.GetServedBy().GetInstance() != successor {
		t.Fatalf("post-crash write did not use successor: %v", afterKill.GetServedBy())
	}
	for _, voter := range aVoters {
		if voter != serving {
			run("kill", "-s", "SIGKILL", voter)
		}
	}
	// No A process survives to repair an empty replica. Reuse the same volumes and IDs.
	// The original crashed leader is required for this two-of-three quorum.
	makeDemo("demo-start-node", "ID="+serving)
	makeDemo("demo-start-node", "ID="+successor)
	waitGeo(t, ctx, "cold A restart preserves acknowledged value", func(ctx context.Context) error {
		return read(ctx, cache, "after-kill", afterKill.GetRev(), "")
	})
	waitGeo(t, ctx, "cold A restart preserves replacement membership", func(ctx context.Context) error {
		m, err := getMembership(ctx)
		if err == nil && len(m.GetMembers()) == 3 && !m.GetJoint() && member(m, 4).GetVoter() && slices.Contains(m.GetRemovedIds(), old.GetId()) {
			return nil
		}
		return fmt.Errorf("membership=%v error=%v", m, err)
	})
	run(append([]string{"--profile", "region-b", "unpause"}, bNodes...)...)
	bPaused = false
	verifyB("after-kill", afterKill.GetRev())
	restarted := put("after-restart", afterKill.GetRev())
	verifyB("after-restart", restarted.GetRev())
	t.Logf("cold A restart required %s; successor %s had acknowledged revision %d", serving, successor, afterKill.GetRev())
	makeDemo("demo-stop-learner", "demo-stop-b", "demo-stop-monitoring", "demo-stop-a")
	if running := run(append(slices.Clone(profiles), "ps", "--status", "running", "--services")...); running != "" {
		t.Fatalf("demo stop targets left services running: %s", running)
	}
}

func waitGeo(t *testing.T, ctx context.Context, description string, probe func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var last error
	for ctx.Err() == nil {
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := probe(attempt)
		cancel()
		if err == nil {
			return
		}
		if ctx.Err() == nil {
			last = err
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
	t.Fatalf("waiting for %s: %v (last probe: %v)", description, ctx.Err(), last)
}
