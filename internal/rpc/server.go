// Package rpc exposes the typed internal RPC boundary described by proto/.
// HTTP remains the desktop compatibility API; workers and remote driver hosts
// can use this gRPC server without importing the control-plane implementation.
package rpc

import (
	"context"
	"encoding/json"
	"net"

	pb "github.com/iothunter/iothunter/gen/proto"
	core "github.com/iothunter/iothunter/internal/core"
	peripheralspkg "github.com/iothunter/iothunter/internal/peripherals"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	Engine *core.Engine
}

func NewServer(engine *core.Engine) *grpc.Server {
	server := grpc.NewServer()
	pb.RegisterCapabilityWorkerServer(server, &capabilityServer{engine: engine})
	pb.RegisterPeripheralServiceServer(server, &peripheralServer{engine: engine})
	return server
}

func Serve(ctx context.Context, engine *core.Engine, listener net.Listener) error {
	server := NewServer(engine)
	go func() {
		<-ctx.Done()
		server.GracefulStop()
	}()
	return server.Serve(listener)
}

type capabilityServer struct {
	pb.UnimplementedCapabilityWorkerServer
	engine *core.Engine
}

func (s *capabilityServer) Execute(ctx context.Context, request *pb.CapabilityRequest) (*pb.CapabilityResult, error) {
	if request == nil || request.GetCapabilityId() == "" {
		return nil, status.Error(codes.InvalidArgument, "capability_id is required")
	}
	inputs := map[string]any{}
	if len(request.GetInputsJson()) > 0 {
		if err := json.Unmarshal(request.GetInputsJson(), &inputs); err != nil {
			return nil, status.Error(codes.InvalidArgument, "inputs_json is invalid")
		}
	}
	task := core.Task{ID: request.GetTaskId(), Permissions: core.PermissionSet{Network: request.GetPermissions().GetNetwork(), Filesystem: request.GetPermissions().GetFilesystem(), Device: request.GetPermissions().GetDevice(), Destructive: request.GetPermissions().GetDestructive()}, Budget: core.Budget{MaxRuntimeSeconds: int(request.GetBudget().GetMaxRuntimeSeconds()), MaxToolCalls: int(request.GetBudget().GetMaxToolCalls()), MaxTokens: int(request.GetBudget().GetMaxTokens())}}
	if task.ID != "" {
		if stored, ok := s.engine.Store.Task(task.ID); ok {
			task = stored
		}
	}
	result, err := s.engine.Request(ctx, core.CapabilityRequest{RequestID: request.GetRequestId(), TaskID: task.ID, AgentID: request.GetAgentId(), CapabilityID: request.GetCapabilityId(), Objective: request.GetObjective(), Inputs: inputs, Permissions: task.Permissions, Budget: task.Budget}, task)
	encoded, _ := json.Marshal(map[string]any{"summary": result.Summary, "confidence": result.Confidence, "evidence": result.Evidence, "artifacts": result.Artifacts, "metrics": result.Metrics})
	response := &pb.CapabilityResult{RequestId: result.RequestID, CapabilityId: result.CapabilityID, Status: result.Status, Summary: result.Summary, ResultJson: encoded, Error: result.Error}
	if err != nil {
		response.Status = "failed"
		response.Error = err.Error()
	}
	return response, nil
}

type peripheralServer struct {
	pb.UnimplementedPeripheralServiceServer
	engine *core.Engine
}

func (s *peripheralServer) Invoke(ctx context.Context, request *pb.PeripheralInvokeRequest) (*pb.PeripheralInvokeResult, error) {
	if request == nil || request.GetSessionId() == "" || request.GetCommand() == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id and command are required")
	}
	args := map[string]any{}
	if len(request.GetArgsJson()) > 0 {
		if err := json.Unmarshal(request.GetArgsJson(), &args); err != nil {
			return nil, status.Error(codes.InvalidArgument, "args_json is invalid")
		}
	}
	if s.engine.Peripherals == nil {
		return nil, status.Error(codes.Unavailable, "peripheral manager is unavailable")
	}
	result, err := s.engine.Peripherals.Invoke(ctx, peripheralspkg.InvokeRequest{SessionID: request.GetSessionId(), Command: request.GetCommand(), Args: args})
	if err != nil {
		return &pb.PeripheralInvokeResult{Status: "failed", Error: err.Error()}, nil
	}
	data, _ := json.Marshal(result.Data)
	return &pb.PeripheralInvokeResult{Status: result.Status, DataJson: data, Bytes: result.Bytes}, nil
}
