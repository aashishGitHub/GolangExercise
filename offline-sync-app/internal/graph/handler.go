package graph

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/99designs/gqlgen/complexity"
	"github.com/99designs/gqlgen/graphql"
	"github.com/99designs/gqlgen/graphql/errcode"
	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/handler/extension"
	"github.com/99designs/gqlgen/graphql/handler/transport"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

type ServerConfig struct {
	Limits         Limits
	Loader         LoaderConfig
	RequestTimeout time.Duration
	Introspection  bool
	NewLoaders     func(Store) *Loaders
}

func DefaultServerConfig() ServerConfig {
	return ServerConfig{
		Limits:         DefaultLimits,
		Loader:         LoaderConfig{Wait: 2 * time.Millisecond, MaxBatch: 500},
		RequestTimeout: 10 * time.Second,
	}
}

// auth runs outermost so an unauthenticated request never allocates loaders or touches the store.
func NewHandler(store Store, auth func(http.Handler) http.Handler, cfg ServerConfig) http.Handler {
	srv := handler.New(NewExecutableSchema(Config{
		Resolvers:  &Resolver{Store: store},
		Complexity: complexityRoot(),
	}))
	srv.AddTransport(transport.POST{})
	srv.SetParserTokenLimit(cfg.Limits.MaxTokens)
	srv.SetErrorPresenter(presentError)
	if cfg.Introspection {
		srv.Use(extension.Introspection{})
	}
	srv.Use(queryLimits{cfg.Limits})
	srv.Use(extension.FixedComplexityLimit(cfg.Limits.MaxComplexity, complexity.WithFixedScalarValue(0)))

	return auth(requestScope(store, cfg, srv))
}

func requestScope(store Store, cfg ServerConfig, next http.Handler) http.Handler {
	newLoaders := cfg.NewLoaders
	if newLoaders == nil {
		newLoaders = func(s Store) *Loaders { return NewLoaders(s, cfg.Loader) }
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), cfg.RequestTimeout)
		defer cancel()
		ctx = WithLoaders(ctx, newLoaders(store))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Errors without a path are protocol-level (parse, validation, limits) and safe to show; resolver errors carry a path and may embed SQL.
func presentError(ctx context.Context, err error) *gqlerror.Error {
	presented := graphql.DefaultErrorPresenter(ctx, err)
	if len(presented.Path) == 0 || presented.Extensions["code"] != nil {
		return presented
	}
	log.Printf("graphql: resolver error at %v: %s", presented.Path, presented.Message)
	masked := &gqlerror.Error{Message: "internal error", Path: presented.Path}
	errcode.Set(masked, "INTERNAL_SERVER_ERROR")
	return masked
}
