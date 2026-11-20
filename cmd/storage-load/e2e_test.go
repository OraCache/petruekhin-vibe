package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	pb "github.com/OraCache/petruekhin-vibe/storage-load/gen/gateway/v1"
	"google.golang.org/grpc"
)

type fixture struct {
	pb.UnimplementedCacheGatewayServer
}

func (fixture) Put(context.Context, *pb.PutRequest) (*pb.PutResponse, error) {
	return &pb.PutResponse{Status: pb.PutStatus_PUT_STATUS_OK}, nil
}

func (fixture) Get(context.Context, *pb.GetRequest) (*pb.GetResponse, error) {
	return &pb.GetResponse{Status: pb.GetStatus_GET_STATUS_HIT, Data: make([]byte, payloadSize)}, nil
}

func TestLoadMetrics(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterCacheGatewayServer(server, fixture{})
	go server.Serve(ln)
	t.Cleanup(server.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRunLoadProcess$", "--",
		"-nodes="+ln.Addr().String(), "-read-rps=100", "-step=2s", "-catch-up", "-metrics-listen=127.0.0.1:0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cancel()
			_ = cmd.Wait()
		}
	}()
	lines := bufio.NewScanner(stderr)
	var endpoint string
	for lines.Scan() {
		line := lines.Text()
		t.Log(line)
		if _, addr, ok := strings.Cut(line, "metrics listening on "); ok {
			endpoint = "http://" + addr + "/metrics"
			break
		}
	}
	if endpoint == "" {
		t.Fatal("generator did not start a metrics listener")
	}

	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("scrape: %s, %v", resp.Status, err)
		}
		text := string(body)
		if strings.Contains(text, `storage_load_rpc_completed_total{operation="Put",outcome="ok"}`) {
			for _, sample := range []string{
				`storage_load_slots_total{operation="Get",state="scheduled"}`,
				`storage_load_slots_total{operation="Get",state="sent"}`,
				`storage_load_slots_total{operation="Get",state="scheduler_late"}`,
				`storage_load_slots_total{operation="Get",state="inflight_limit"}`,
				`storage_load_rpc_completed_total{operation="Get",outcome="ok"}`,
				`storage_load_success_latency_seconds_bucket{operation="Get",le="+Inf"}`,
			} {
				if !strings.Contains(text, sample) {
					t.Errorf("missing %s in scrape", sample)
				}
			}
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRunLoadProcess(t *testing.T) {
	if len(os.Args) > 3 && os.Args[2] == "--" {
		os.Args = append(os.Args[:1], os.Args[3:]...)
		main()
	}
}
