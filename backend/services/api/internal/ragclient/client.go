// Package ragclient adapts the internal Python gRPC service to Go's public API.
package ragclient

import (
	conversation "ai-bud/services/api/internal/conversation/domain"
	"context"
	"crypto/rand"
	"errors"
	"time"

	"ai-bud/services/api/internal/identity"
	pb "ai-bud/services/api/internal/ragclient/ragv1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const Timeout = 30 * time.Second

type Input = conversation.Input
type Source = conversation.Source
type Step = conversation.Step
type Answer = conversation.Answer
type SearchResult = conversation.SearchResult

type Gateway interface {
	Answer(context.Context, identity.Actor, Input) (Answer, error)
	Search(context.Context, identity.Actor, Input) (SearchResult, error)
}

type Client struct {
	conn  *grpc.ClientConn
	stub  pb.RagServiceClient
	token string
}

func New(target, token string) (*Client, error) {
	if target == "" || token == "" {
		return nil, errors.New("RAG target and service token are required")
	}
	// This binary requires DEMO_MODE=true; plaintext is limited to local development.
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(64<<10)))
	if err != nil {
		return nil, err
	}
	return &Client{conn: conn, stub: pb.NewRagServiceClient(conn), token: token}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.token), cancel
}

func actorMessage(a identity.Actor) *pb.Actor {
	return &pb.Actor{UserId: a.ID, Role: a.Role, Team: a.Team}
}

func (c *Client) Answer(ctx context.Context, a identity.Actor, in Input) (Answer, error) {
	if err := in.Validate(); err != nil {
		return Answer{}, status.Error(codes.InvalidArgument, "invalid_input")
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	mode := pb.AnswerMode_ANSWER_MODE_OFFLINE
	if in.Mode == "llm" {
		mode = pb.AnswerMode_ANSWER_MODE_LLM
	}
	history := make([]*pb.ConversationMessage, 0, len(in.History))
	for _, message := range in.History {
		history = append(history, &pb.ConversationMessage{Role: message.Role, Content: message.Content})
	}
	r, err := c.stub.Answer(ctx, &pb.AnswerRequest{RequestId: rand.Text(), Question: in.Question, Actor: actorMessage(a), Mode: mode, TopK: in.TopK, History: history})
	if err != nil {
		return Answer{}, err
	}
	states := map[pb.AnswerStatus]string{
		pb.AnswerStatus_ANSWER_STATUS_ANSWERED: "answered", pb.AnswerStatus_ANSWER_STATUS_SOURCES_FOUND: "sources_found",
		pb.AnswerStatus_ANSWER_STATUS_INSUFFICIENT_EVIDENCE: "insufficient_evidence", pb.AnswerStatus_ANSWER_STATUS_CONFLICT: "conflict",
		pb.AnswerStatus_ANSWER_STATUS_NEEDS_REVIEW: "needs_review",
	}
	state, ok := states[r.Status]
	if !ok {
		return Answer{}, status.Error(codes.Internal, "invalid_rag_response")
	}
	out := Answer{RequestID: r.RequestId, Status: state, Mode: r.Mode, Steps: []Step{}, Sources: map[string]Source{}, NextStep: r.NextStep, Model: r.Model, Action: r.Action}
	for key, source := range r.Sources {
		out.Sources[key] = sourceView(source)
	}
	for _, step := range r.Steps {
		out.Steps = append(out.Steps, Step{Text: step.Text, SourceIDs: append([]string{}, step.SourceIds...)})
	}
	return out, nil
}

func (c *Client) Search(ctx context.Context, a identity.Actor, in Input) (SearchResult, error) {
	if err := in.Validate(); err != nil {
		return SearchResult{}, status.Error(codes.InvalidArgument, "invalid_input")
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	r, err := c.stub.Search(ctx, &pb.SearchRequest{RequestId: rand.Text(), Query: in.Question, Actor: actorMessage(a), TopK: in.TopK})
	if err != nil {
		return SearchResult{}, err
	}
	out := SearchResult{RequestID: r.RequestId, Sources: []Source{}}
	for _, source := range r.Sources {
		out.Sources = append(out.Sources, sourceView(source))
	}
	return out, nil
}

func sourceView(s *pb.Source) Source {
	return Source{DocumentID: s.DocumentId, VersionID: s.VersionId, ChunkID: s.ChunkId, Title: s.Title, Text: s.Text,
		SourceURL: s.SourceUrl, PageNumber: s.PageNumber, Origin: s.Origin, Stale: s.Stale, ReviewBy: s.ReviewBy,
		StartChar: s.StartChar, EndChar: s.EndChar}
}

func (c *Client) Generate(ctx context.Context, actor identity.Actor, input conversation.Input) (conversation.Answer, error) {
	answer, err := c.Answer(ctx, actor, input)
	if err == nil {
		return answer, nil
	}
	switch status.Code(err) {
	case codes.InvalidArgument:
		return answer, conversation.ErrInvalid
	case codes.DeadlineExceeded:
		return answer, conversation.ErrDeadline
	case codes.ResourceExhausted:
		return answer, conversation.ErrLimited
	default:
		return answer, conversation.ErrUnavailable
	}
}
