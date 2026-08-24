package grpc

import (
	"context"

	pb "github.com/alfirus/vectorizer/vectorizerpb"
	"github.com/alfirus/vectorizer/internal/handlers"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/store"
)

type Server struct {
	pb.UnimplementedVectorizerServiceServer
	store *store.Store
	brain *llmbrain.Service
	chatHandler *handlers.ChatHandler
}

func New(store *store.Store, brain *llmbrain.Service) *Server {
	return &Server{
		store: store,
		brain: brain,
		chatHandler: handlers.NewChatHandler(store, brain),
	}
}

func (s *Server) AddMessage(ctx context.Context, req *pb.AddMessageRequest) (*pb.AddMessageResponse, error) {
	msg := models.NewMessage(req.WorkspaceId, req.SessionId, req.Role, req.Content)
	if err := s.store.AddMessage(msg, req.Content); err != nil {
		return nil, err
	}
	return &pb.AddMessageResponse{Id: msg.ID, Stored: true}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	n := int(req.NResults)
	if n <= 0 { n = 5 }
	results, err := s.store.Search(req.Query, req.WorkspaceId, req.SessionId, req.Role, n)
	if err != nil { return nil, err }
	resp := &pb.SearchResponse{}
	for _, r := range results {
		meta := map[string]string{}
		for k, v := range r.Metadata {
			if str, ok := v.(string); ok { meta[k] = str }
		}
		resp.Results = append(resp.Results, &pb.SearchResult{
			Id: r.ID, Document: r.Document, Metadata: meta, Distance: r.Distance,
		})
	}
	return resp, nil
}

func (s *Server) Chat(ctx context.Context, req *pb.ChatRequest) (*pb.ChatResponse, error) {
	// Simplified: delegate to store search + brain
	ctxStr := ""
	if results, err := s.store.Search(req.Query, req.WorkspaceId, "", "", 5); err == nil {
		for _, r := range results { ctxStr += r.Document + "\n" }
	}
	if s.brain == nil {
		return &pb.ChatResponse{Answer: ctxStr}, nil
	}
	answer, err := s.brain.Chat(
		"Answer about peer from context",
		ctxStr, req.Query,
	)
	if err != nil { return nil, err }
	return &pb.ChatResponse{Answer: answer}, nil
}

func (s *Server) Health(ctx context.Context, req *pb.HealthRequest) (*pb.HealthResponse, error) {
	return &pb.HealthResponse{Status: "ok", Version: "0.2.0"}, nil
}
