package grpc

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pb "github.com/alfirus/vectorizer/vectorizerpb"
	"github.com/alfirus/vectorizer/internal/handlers"
	"github.com/alfirus/vectorizer/internal/llmbrain"
	"github.com/alfirus/vectorizer/internal/models"
	"github.com/alfirus/vectorizer/internal/security"
	"github.com/alfirus/vectorizer/internal/store"
	"github.com/alfirus/vectorizer/internal/version"
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
	if err := scopedWorkspace(ctx, req.WorkspaceId); err != nil {
		return nil, err
	}
	msg := models.NewMessage(req.WorkspaceId, req.SessionId, req.Role, req.Content)
	if err := s.store.AddMessage(msg, req.Content); err != nil {
		return nil, err
	}
	return &pb.AddMessageResponse{Id: msg.ID, Stored: true}, nil
}

func (s *Server) Search(ctx context.Context, req *pb.SearchRequest) (*pb.SearchResponse, error) {
	if err := scopedWorkspace(ctx, req.WorkspaceId); err != nil {
		return nil, err
	}
	n := int(req.NResults)
	if n <= 0 || n > 100 {
		n = 5
	}
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
	if err := scopedWorkspace(ctx, req.WorkspaceId); err != nil {
		return nil, err
	}
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
	return &pb.HealthResponse{Status: "ok", Version: version.Version}, nil
}

// AuthConfig mirrors the REST auth material. When neither an API key nor
// JWT is configured the interceptor denies everything except Health
// (fail closed, consistent with the REST fail-closed startup in main.go).
type AuthConfig struct {
	APIKey    string
	JWTSecret string
	UseJWT    bool
}

type ctxClaimsKey struct{}

// ClaimsFromContext returns the JWT claims attached by AuthInterceptor,
// or nil when the call was authorized via API key.
func ClaimsFromContext(ctx context.Context) *security.Claims {
	if c, ok := ctx.Value(ctxClaimsKey{}).(*security.Claims); ok {
		return c
	}
	return nil
}

// AuthInterceptor enforces the same auth as the REST chain: an API key
// (x-api-key metadata, exact match) or a JWT bearer token. Health is
// public like /api/v1/health. Everything else requires auth material to
// be configured AND valid credentials on the call.
func AuthInterceptor(cfg AuthConfig) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if info.FullMethod == "/vectorizer.VectorizerService/Health" {
			return handler(ctx, req)
		}
		md, _ := metadata.FromIncomingContext(ctx)
		if cfg.APIKey != "" {
			for _, v := range md.Get("x-api-key") {
				if v == cfg.APIKey {
					return handler(ctx, req)
				}
			}
		}
		if cfg.UseJWT && cfg.JWTSecret != "" {
			token := bearerToken(md)
			if token == "" {
				// also accept X-API-Key as bearer for compat (REST parity)
				if vs := md.Get("x-api-key"); len(vs) > 0 {
					token = vs[0]
				}
			}
			if token != "" {
				if claims, err := security.ParseToken(cfg.JWTSecret, token); err == nil {
					return handler(context.WithValue(ctx, ctxClaimsKey{}, claims), req)
				}
			}
		}
		if cfg.APIKey == "" && !(cfg.UseJWT && cfg.JWTSecret != "") {
			return nil, status.Error(codes.Unavailable, "server has no auth configured")
		}
		return nil, status.Error(codes.Unauthenticated, "missing or invalid credentials")
	}
}

func bearerToken(md metadata.MD) string {
	for _, v := range md.Get("authorization") {
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			return strings.TrimSpace(v[7:])
		}
	}
	return ""
}

// scopedWorkspace enforces the JWT workspace claim against the request's
// workspace for non-admin tokens. API-key calls (nil claims) skip it.
func scopedWorkspace(ctx context.Context, workspaceID string) error {
	claims := ClaimsFromContext(ctx)
	if claims == nil || claims.Admin || claims.Workspace == "" {
		return nil
	}
	if workspaceID == "" || workspaceID != claims.Workspace {
		return status.Error(codes.PermissionDenied, "workspace mismatch")
	}
	return nil
}
