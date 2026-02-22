// Copyright (c) 2023-2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/common"
	"github.com/go-openapi/loads"
)

const (
	GRPCGatewayHTTPPort = 8000
)

// GatewayConfig holds configuration for HTTP gateway setup
type GatewayConfig struct {
	Port       int
	GRPCAddr   string
	BasePath   string
	SwaggerDir string
	Logger     *slog.Logger
}

// StartGRPCGateway creates and starts the gRPC-Gateway HTTP server
func StartGRPCGateway(ctx context.Context, cfg GatewayConfig) error {
	grpcGateway, err := common.NewGateway(ctx, cfg.GRPCAddr, cfg.BasePath)
	if err != nil {
		return fmt.Errorf("failed to create gRPC-Gateway: %w", err)
	}

	server := newGRPCGatewayHTTPServer(
		fmt.Sprintf(":%d", cfg.Port),
		grpcGateway,
		cfg.Logger,
		cfg.SwaggerDir,
		cfg.BasePath,
	)

	go func() {
		cfg.Logger.Info("starting gRPC-Gateway HTTP server", "port", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			cfg.Logger.Error("failed to run gRPC-Gateway HTTP server", "error", err)
			os.Exit(1)
		}
	}()

	return nil
}

func newGRPCGatewayHTTPServer(
	addr string,
	handler http.Handler,
	logger *slog.Logger,
	swaggerDir string,
	basePath string,
) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/", handler)

	serveSwaggerUI(mux, basePath)
	serveSwaggerJSON(mux, swaggerDir, basePath)

	loggedMux := loggingMiddleware(logger, mux)

	return &http.Server{
		Addr:     addr,
		Handler:  loggedMux,
		ErrorLog: log.New(os.Stderr, "httpSrv: ", log.LstdFlags),
	}
}

func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start),
		)
	})
}

func serveSwaggerUI(mux *http.ServeMux, basePath string) {
	swaggerUIDir := "third_party/swagger-ui"
	fileServer := http.FileServer(http.Dir(swaggerUIDir))
	swaggerUiPath := fmt.Sprintf("%s/apidocs/", basePath)
	mux.Handle(swaggerUiPath, http.StripPrefix(swaggerUiPath, fileServer))
}

func serveSwaggerJSON(mux *http.ServeMux, swaggerDir string, basePath string) {
	fileHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		matchingFiles, err := filepath.Glob(filepath.Join(swaggerDir, "*.swagger.json"))
		if err != nil || len(matchingFiles) == 0 {
			http.Error(w, "Error finding Swagger JSON file", http.StatusInternalServerError)
			return
		}

		firstMatchingFile := matchingFiles[0]
		swagger, err := loads.Spec(firstMatchingFile)
		if err != nil {
			http.Error(w, "Error parsing Swagger JSON file", http.StatusInternalServerError)
			return
		}

		swagger.Spec().BasePath = basePath

		updatedSwagger, err := swagger.Spec().MarshalJSON()
		if err != nil {
			http.Error(w, "Error serializing updated Swagger JSON", http.StatusInternalServerError)
			return
		}
		var prettySwagger bytes.Buffer
		err = json.Indent(&prettySwagger, updatedSwagger, "", "  ")
		if err != nil {
			http.Error(w, "Error formatting updated Swagger JSON", http.StatusInternalServerError)
			return
		}

		_, err = w.Write(prettySwagger.Bytes())
		if err != nil {
			http.Error(w, "Error writing Swagger JSON response", http.StatusInternalServerError)
			return
		}
	})
	apidocsPath := fmt.Sprintf("%s/apidocs/api.json", basePath)
	mux.Handle(apidocsPath, fileHandler)
}
