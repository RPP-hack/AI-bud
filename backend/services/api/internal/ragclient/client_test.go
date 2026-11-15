package ragclient

import (
	"context"
	"net"
	"testing"
	"time"

	"ai-bud/services/api/internal/identity"
	pb "ai-bud/services/api/internal/ragclient/ragv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type testServer struct {
	pb.UnimplementedRagServiceServer
	t *testing.T
}

func (s *testServer) Answer(ctx context.Context, in *pb.AnswerRequest) (*pb.AnswerResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer token" {
		s.t.Error("missing service credential")
	}
	if in.Actor.UserId != "employee" || in.Actor.Role != "intern" || in.Actor.Team != "platform" {
		s.t.Error(in.Actor)
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > Timeout {
		s.t.Error("missing deadline")
	}
	if in.Mode != pb.AnswerMode_ANSWER_MODE_OFFLINE || in.RequestId == "" {
		s.t.Error(in)
	}
	if in.Question == "fail" {
		return nil, status.Error(codes.Unavailable, "private details")
	}
	return &pb.AnswerResponse{RequestId: in.RequestId, Status: pb.AnswerStatus_ANSWER_STATUS_INSUFFICIENT_EVIDENCE, Mode: "rules"}, nil
}

func TestClientContextAndResponseMapping(t *testing.T) {
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer()
	pb.RegisterRagServiceServer(server, &testServer{t: t})
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := &Client{conn: conn, stub: pb.NewRagServiceClient(conn), token: "token"}
	a := identity.Actor{ID: "employee", Role: "intern", Team: "platform"}
	r, err := c.Answer(context.Background(), a, Input{Question: "no evidence"})
	if err != nil || r.Status != "insufficient_evidence" || r.Steps == nil || r.Sources == nil || r.Model != nil || r.Action != nil {
		t.Fatal(r, err)
	}
	if _, err := c.Answer(context.Background(), a, Input{Question: "fail"}); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Answer(ctx, a, Input{Question: "cancel"}); status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
}

func TestInputValidation(t *testing.T) {
	zero, large := uint32(0), uint32(51)
	for _, in := range []Input{{Question: " "}, {Question: "x\x00"}, {Question: "hello", Mode: "invalid"}, {Question: "hello", TopK: &zero}, {Question: "hello", TopK: &large}} {
		if status.Code(in.Validate()) != codes.InvalidArgument {
			t.Fatal(in)
		}
	}
}
