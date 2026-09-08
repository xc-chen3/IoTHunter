package rpc

import (
	"context"
	"encoding/json"
	"net"
	"testing"

	pb "github.com/iothunter/iothunter/gen/proto"
	core "github.com/iothunter/iothunter/internal/core"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
)

func TestCapabilityGRPCBoundary(t *testing.T) {
	engine := core.NewEngine(mustStore(t), 1)
	listener := bufconn.Listen(1024 * 1024)
	server := NewServer(engine)
	go server.Serve(listener)
	defer server.Stop()
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithInsecure())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	inputs, _ := json.Marshal(map[string]any{"vendor": "Acme", "model": "Router"})
	result, err := pb.NewCapabilityWorkerClient(conn).Execute(context.Background(), &pb.CapabilityRequest{RequestId: "REQ-1", TaskId: "TASK-1", AgentId: "recon-default", CapabilityId: "target.fingerprint", Objective: "fingerprint", InputsJson: inputs, Permissions: &pb.PermissionSet{Filesystem: "workspace-readonly"}, Budget: &pb.Budget{MaxRuntimeSeconds: 10, MaxToolCalls: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if result.GetStatus() != "completed" || result.GetSummary() == "" || len(result.GetResultJson()) == 0 {
		t.Fatalf("unexpected gRPC result: %+v", result)
	}
}

func mustStore(t *testing.T) *core.Store {
	t.Helper()
	store, err := core.NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	return store
}
