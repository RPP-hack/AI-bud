package ragclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	conversation "ai-bud/services/api/internal/conversation/domain"
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
	if strings.HasPrefix(in.Question, "model_") || in.Question == "search_unavailable" {
		return nil, status.Error(codes.Unavailable, in.Question)
	}
	if in.Question == "greeting" {
		return &pb.AnswerResponse{Status: pb.AnswerStatus_ANSWER_STATUS_CONVERSATION, Mode: "rules"}, nil
	}
	if in.Question == "fail" {
		return nil, status.Error(codes.Unavailable, "private details")
	}
	if in.Question == "timeout" {
		return nil, status.Error(codes.DeadlineExceeded, "private details")
	}
	if in.Question == "limited" {
		return nil, status.Error(codes.ResourceExhausted, "private details")
	}
	if in.Question == "invalid response" {
		return &pb.AnswerResponse{}, nil
	}
	if in.Question == "sources" {
		return &pb.AnswerResponse{Status: pb.AnswerStatus_ANSWER_STATUS_ANSWERED, Mode: "offline", Steps: []*pb.Step{{Text: "Read BUD", SourceIds: []string{"S1"}}}, Sources: map[string]*pb.Source{"S1": {DocumentId: "jira", Text: "Instruction", Origin: "demo"}}}, nil
	}
	if in.Question == "followup" && (len(in.History) != 2 || in.History[0].Role != "user" || in.History[0].Content != "Jira 403" || in.History[1].Role != "assistant") {
		s.t.Error("history was not sent", in.History)
	}
	return &pb.AnswerResponse{RequestId: in.RequestId, Status: pb.AnswerStatus_ANSWER_STATUS_INSUFFICIENT_EVIDENCE, Mode: "rules"}, nil
}

func (s *testServer) Search(ctx context.Context, in *pb.SearchRequest) (*pb.SearchResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if len(md.Get("authorization")) != 1 || md.Get("authorization")[0] != "Bearer token" || in.Actor.UserId != "employee" {
		s.t.Error("missing identity or credential")
	}
	if in.Query == "fail" {
		return nil, status.Error(codes.Unavailable, "private details")
	}
	return &pb.SearchResponse{RequestId: in.RequestId, Sources: []*pb.Source{{DocumentId: "jira", VersionId: "v1", ChunkId: "chunk1", Text: "Instruction", Title: "Jira access", Origin: "demo"}}}, nil
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
	if _, err := c.Generate(context.Background(), a, Input{Question: "followup", History: []conversation.Message{{Role: "user", Content: "Jira 403"}, {Role: "assistant", Content: "BUD"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Generate(context.Background(), a, Input{Question: "fail"}); !errors.Is(err, conversation.ErrUnavailable) {
		t.Fatal(err)
	}
	for question, want := range map[string]error{"": conversation.ErrInvalid, "timeout": conversation.ErrDeadline, "limited": conversation.ErrLimited, "invalid response": conversation.ErrUnavailable} {
		if _, err := c.Generate(context.Background(), a, Input{Question: question}); !errors.Is(err, want) {
			t.Fatalf("%q: %v", question, err)
		}
	}
	answer, err := c.Answer(context.Background(), a, Input{Question: "sources"})
	if err != nil || len(answer.Steps) != 1 || answer.Steps[0].SourceIDs[0] != "S1" || answer.Sources["S1"].DocumentID != "jira" {
		t.Fatal(answer, err)
	}
	results, err := c.Search(context.Background(), a, Input{Question: "Jira"})
	if err != nil || len(results.Sources) != 1 || results.RequestID == "" || results.Sources[0].VersionID != "v1" || results.Sources[0].Origin != "demo" {
		t.Fatal(results, err)
	}
	for question, code := range map[string]codes.Code{"": codes.InvalidArgument, "fail": codes.Unavailable} {
		if _, err := c.Search(context.Background(), a, Input{Question: question}); status.Code(err) != code {
			t.Fatal(question, err)
		}
	}
	for _, code := range []string{"model_timeout", "model_rate_limited", "model_incomplete", "search_unavailable"} {
		if _, err := c.Generate(context.Background(), a, Input{Question: code}); err == nil || err.Error() != code {
			t.Fatal(code, err)
		}
	}
	if result, err := c.Generate(context.Background(), a, Input{Question: "greeting"}); err != nil || result.Status != "conversation" {
		t.Fatal(result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Answer(ctx, a, Input{Question: "cancel"}); status.Code(err) != codes.Canceled {
		t.Fatal(err)
	}
}

func TestClientConfiguration(t *testing.T) {
	for _, config := range [][2]string{{"", "token"}, {"localhost:50051", ""}} {
		if _, err := New(config[0], config[1]); err == nil {
			t.Fatal("accepted missing config")
		}
	}
	c, err := New("localhost:50051", "token")
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestInputValidation(t *testing.T) {
	zero, large := uint32(0), uint32(51)
	for _, in := range []Input{{Question: " "}, {Question: "x\x00"}, {Question: "hello", Mode: "invalid"}, {Question: "hello", TopK: &zero}, {Question: "hello", TopK: &large}} {
		if !errors.Is(in.Validate(), conversation.ErrInvalid) {
			t.Fatal(in)
		}
	}
}
