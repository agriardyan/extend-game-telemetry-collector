// Copyright (c) 2023-2025 AccelByte Inc. All Rights Reserved.
// This is licensed software from AccelByte Inc, for limitations
// and restrictions contact your company contract manager.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/config"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/dedup"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/processor"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/service"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/kafka"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/mongodb"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/noop"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/postgres"
	"github.com/agriardyan/extend-game-telemetry-collector/pkg/storage/plugins/s3"

	"github.com/go-openapi/loads"

	"github.com/agriardyan/extend-game-telemetry-collector/pkg/common"

	"github.com/AccelByte/accelbyte-go-sdk/services-api/pkg/repository"

	"github.com/AccelByte/accelbyte-go-sdk/services-api/pkg/factory"
	"github.com/AccelByte/accelbyte-go-sdk/services-api/pkg/service/iam"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	pb "github.com/agriardyan/extend-game-telemetry-collector/pkg/pb"

	sdkAuth "github.com/AccelByte/accelbyte-go-sdk/services-api/pkg/utils/auth"
	prometheusGrpc "github.com/grpc-ecosystem/go-grpc-prometheus"
	prometheusCollectors "github.com/prometheus/client_golang/prometheus/collectors"
)

const (
	metricsEndpoint     = "/metrics"
	metricsPort         = 8080
	grpcServerPort      = 6565
	grpcGatewayHTTPPort = 8000
)

var (
	serviceName = "extend-app-service-extension"
)

func parseSlogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "fatal", "panic":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration from environment variables
	appCfg, err := config.LoadConfig()
	if err != nil {
		// Use basic logger since we don't have config yet
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Setup logger with configured log level
	slogLevel := parseSlogLevel(appCfg.Server.LogLevel)
	opts := &slog.HandlerOptions{
		Level: slogLevel,
	}
	handler := slog.NewJSONHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	logger.Info("configuration loaded from environment variables")

	loggingOptions := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.FinishCall, logging.PayloadReceived, logging.PayloadSent),
		logging.WithFieldsFromContext(func(ctx context.Context) logging.Fields {
			if span := trace.SpanContextFromContext(ctx); span.IsSampled() {
				return logging.Fields{"traceID", span.TraceID().String()}
			}

			return nil
		}),
		logging.WithLevels(logging.DefaultClientCodeToLevel),
		logging.WithDurationField(logging.DurationToDurationField),
	}

	unaryServerInterceptors := []grpc.UnaryServerInterceptor{
		prometheusGrpc.UnaryServerInterceptor,
		logging.UnaryServerInterceptor(common.InterceptorLogger(logger), loggingOptions...),
	}
	streamServerInterceptors := []grpc.StreamServerInterceptor{
		prometheusGrpc.StreamServerInterceptor,
		logging.StreamServerInterceptor(common.InterceptorLogger(logger), loggingOptions...),
	}

	// Preparing the IAM authorization
	var tokenRepo repository.TokenRepository = sdkAuth.DefaultTokenRepositoryImpl()
	var configRepo repository.ConfigRepository = sdkAuth.DefaultConfigRepositoryImpl()
	var refreshRepo repository.RefreshTokenRepository = &sdkAuth.RefreshTokenImpl{RefreshRate: 0.8, AutoRefresh: true}

	oauthService := iam.OAuth20Service{
		Client:                 factory.NewIamClient(configRepo),
		TokenRepository:        tokenRepo,
		RefreshTokenRepository: refreshRepo,
		ConfigRepository:       configRepo,
	}

	// Check if auth is enabled from environment (not in config struct)
	if strings.ToLower(os.Getenv("PLUGIN_GRPC_SERVER_AUTH_ENABLED")) != "false" {
		refreshInterval := 600
		if val := os.Getenv("REFRESH_INTERVAL"); val != "" {
			if parsed, err := strconv.Atoi(val); err == nil {
				refreshInterval = parsed
			}
		}
		common.Validator = common.NewTokenValidator(oauthService, time.Duration(refreshInterval)*time.Second, true)
		err := common.Validator.Initialize(ctx)
		if err != nil {
			logger.Info(err.Error())
		}

		unaryServerInterceptor := common.NewUnaryAuthServerIntercept()
		serverServerInterceptor := common.NewStreamAuthServerIntercept()

		unaryServerInterceptors = append(unaryServerInterceptors, unaryServerInterceptor)
		streamServerInterceptors = append(streamServerInterceptors, serverServerInterceptor)
		logger.Info("added auth interceptors")
	}

	// Create gRPC Server
	s := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(unaryServerInterceptors...),
		grpc.ChainStreamInterceptor(streamServerInterceptors...),
	)

	// Configure IAM authorization
	clientId := configRepo.GetClientId()
	clientSecret := configRepo.GetClientSecret()
	err = oauthService.LoginClient(&clientId, &clientSecret)
	if err != nil {
		logger.Error("error unable to login using clientId and clientSecret", "error", err)
		os.Exit(1)
	}

	// Initialize deduplicator
	var deduplicator dedup.Deduplicator[*storage.TelemetryEvent]
	if appCfg.Deduplication.Enabled {
		switch appCfg.Deduplication.Type {
		case "memory":
			deduplicator = dedup.NewMemoryDeduplicator[*storage.TelemetryEvent](appCfg.Deduplication.TTL)
			logger.Info("deduplication enabled", "type", "memory", "ttl", appCfg.Deduplication.TTL)

		case "redis":
			redisConfig := dedup.RedisConfig{
				Addr:     appCfg.Deduplication.Redis.Addr,
				Password: appCfg.Deduplication.Redis.Password,
				DB:       appCfg.Deduplication.Redis.DB,
			}
			deduplicator = dedup.NewRedisDeduplicator[*storage.TelemetryEvent](redisConfig, appCfg.Deduplication.TTL)
			logger.Info("deduplication enabled", "type", "redis", "addr", redisConfig.Addr, "ttl", appCfg.Deduplication.TTL)

		default:
			deduplicator = dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()
			logger.Info("deduplication disabled")
		}
	} else {
		deduplicator = dedup.NewNoopDeduplicator[*storage.TelemetryEvent]()
		logger.Info("deduplication disabled")
	}

	// Initialize storage plugins — each plugin receives its typed config via its constructor.
	var enabledPlugins []storage.StoragePlugin[*storage.TelemetryEvent]

	for _, pluginName := range appCfg.GetEnabledPlugins() {
		var plugin storage.StoragePlugin[*storage.TelemetryEvent]

		switch pluginName {
		case "postgres":
			plugin = postgres.NewPostgresPlugin(postgres.PostgresPluginConfig{
				DSN:     appCfg.Storage.Postgres.PostgresDSN,
				Table:   appCfg.Storage.Postgres.PostgresTable,
				Workers: appCfg.Storage.Postgres.Workers,
			})

		case "s3":
			plugin = s3.NewS3Plugin(s3.S3PluginConfig{
				Bucket: appCfg.Storage.S3.S3Bucket,
				Prefix: appCfg.Storage.S3.S3Prefix,
				Region: appCfg.Storage.S3.S3Region,
			})

		case "kafka":
			brokers := strings.Split(appCfg.Storage.Kafka.KafkaBrokers, ",")
			for i, b := range brokers {
				brokers[i] = strings.TrimSpace(b)
			}
			plugin = kafka.NewKafkaPlugin(kafka.KafkaPluginConfig{
				Brokers:       brokers,
				Topic:         appCfg.Storage.Kafka.KafkaTopic,
				Compression:   appCfg.Storage.Kafka.KafkaCompression,
				BatchSize:     appCfg.Storage.Kafka.BatchSize,
				FlushInterval: appCfg.Storage.Kafka.FlushInterval,
			})

		case "mongodb":
			plugin = mongodb.NewMongoDBPlugin(mongodb.MongoDBPluginConfig{
				URI:        appCfg.Storage.MongoDB.MongoURI,
				Database:   appCfg.Storage.MongoDB.MongoDatabase,
				Collection: appCfg.Storage.MongoDB.MongoCollection,
				Workers:    appCfg.Storage.MongoDB.Workers,
			})

		case "noop":
			plugin = noop.NewNoopPlugin[*storage.TelemetryEvent]()

		default:
			logger.Error("unknown plugin", "plugin", pluginName)
			os.Exit(1)
		}

		if err := plugin.Initialize(ctx); err != nil {
			logger.Error("failed to initialize plugin", "plugin", pluginName, "error", err)
			os.Exit(1)
		}

		enabledPlugins = append(enabledPlugins, plugin)
		logger.Info("plugin initialized", "plugin", pluginName)
	}

	if len(enabledPlugins) == 0 {
		logger.Error("no storage plugins enabled")
		os.Exit(1)
	}

	logger.Info("storage plugins ready", "count", len(enabledPlugins))

	// Initialize async processor
	processorConfig := processor.Config{
		Workers:       appCfg.Processor.Workers,
		ChannelBuffer: appCfg.Processor.ChannelBuffer,
		BatchSize:     appCfg.Processor.DefaultBatchSize,
		FlushInterval: appCfg.Processor.DefaultFlushInterval,
	}

	telemetryProcessor := processor.NewProcessor(
		processorConfig,
		enabledPlugins,
		deduplicator,
		logger,
	)
	telemetryProcessor.Start()

	logger.Info("async processor started",
		"workers", processorConfig.Workers,
		"channel_buffer", processorConfig.ChannelBuffer,
		"batch_size", processorConfig.BatchSize,
		"flush_interval", processorConfig.FlushInterval)

	// Register Telemetry Service
	// Namespace is static — read once from env at startup and injected into the service.
	namespace := os.Getenv("AB_NAMESPACE")
	if namespace == "" {
		namespace = "accelbyte"
	}
	telemetrySvc := service.NewTelemetryService(
		namespace,
		tokenRepo,
		configRepo,
		refreshRepo,
		telemetryProcessor,
		logger,
	)
	pb.RegisterServiceServer(s, telemetrySvc)

	// Enable gRPC Reflection
	reflection.Register(s)

	// Enable gRPC Health Check
	grpc_health_v1.RegisterHealthServer(s, health.NewServer())

	// Create a new HTTP server for the gRPC-Gateway
	grpcGateway, err := common.NewGateway(ctx, fmt.Sprintf("localhost:%d", grpcServerPort), appCfg.Server.BasePath)
	if err != nil {
		logger.Error("failed to create gRPC-Gateway", "error", err)
		os.Exit(1)
	}

	// Start the gRPC-Gateway HTTP server
	go func() {
		swaggerDir := "gateway/apidocs" // Path to swagger directory
		grpcGatewayHTTPServer := newGRPCGatewayHTTPServer(fmt.Sprintf(":%d", grpcGatewayHTTPPort), grpcGateway, logger, swaggerDir)
		logger.Info("starting gRPC-Gateway HTTP server", "port", grpcGatewayHTTPPort)
		if err := grpcGatewayHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("failed to run gRPC-Gateway HTTP server", "error", err)
			os.Exit(1)
		}
	}()

	prometheusGrpc.Register(s)

	// Register Prometheus Metrics
	prometheusRegistry := prometheus.NewRegistry()
	prometheusRegistry.MustRegister(
		prometheusCollectors.NewGoCollector(),
		prometheusCollectors.NewProcessCollector(prometheusCollectors.ProcessCollectorOpts{}),
		prometheusGrpc.DefaultServerMetrics,
	)

	go func() {
		http.Handle(metricsEndpoint, promhttp.HandlerFor(prometheusRegistry, promhttp.HandlerOpts{}))
		if err := http.ListenAndServe(fmt.Sprintf(":%d", metricsPort), nil); err != nil {
			logger.Error("failed to start metrics server", "error", err)
			os.Exit(1)
		}
	}()
	logger.Info("serving prometheus metrics", "port", metricsPort, "endpoint", metricsEndpoint)

	// Set Tracer Provider
	if val := os.Getenv("OTEL_SERVICE_NAME"); val != "" {
		serviceName = "extend-app-se-" + strings.ToLower(val)
	}
	tracerProvider, err := common.NewTracerProvider(serviceName)
	if err != nil {
		logger.Error("failed to create tracer provider", "error", err)
		os.Exit(1)
	}
	otel.SetTracerProvider(tracerProvider)
	defer func(ctx context.Context) {
		if err := tracerProvider.Shutdown(ctx); err != nil {
			logger.Error("failed to shutdown tracer provider", "error", err)
			os.Exit(1)
		}
	}(ctx)

	// Set Text Map Propagator
	otel.SetTextMapPropagator(
		propagation.NewCompositeTextMapPropagator(
			b3.New(),
			propagation.TraceContext{},
			propagation.Baggage{},
		),
	)

	// Start gRPC Server
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", grpcServerPort))
	if err != nil {
		logger.Error("failed to listen to tcp", "port", grpcServerPort, "error", err)
		os.Exit(1)
	}
	go func() {
		if err = s.Serve(lis); err != nil {
			logger.Error("failed to run gRPC server", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("app server started", "service", serviceName)

	// Wait for shutdown signal
	shutdownCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-shutdownCtx.Done()
	logger.Info("shutdown signal received")

	// Graceful shutdown
	logger.Info("initiating graceful shutdown")

	// Shutdown processor (flush pending events)
	if err := telemetryProcessor.Shutdown(30 * time.Second); err != nil {
		logger.Error("processor shutdown error", "error", err)
	}

	// Close deduplicator
	if err := deduplicator.Close(); err != nil {
		logger.Error("deduplicator close error", "error", err)
	}

	// Close all storage plugins
	for _, plugin := range enabledPlugins {
		if err := plugin.Close(); err != nil {
			logger.Error("failed to close plugin", "plugin", plugin.Name(), "error", err)
		}
	}

	logger.Info("graceful shutdown complete")
}

func newGRPCGatewayHTTPServer(
	addr string, handler http.Handler, logger *slog.Logger, swaggerDir string,
) *http.Server {
	// Create a new ServeMux
	mux := http.NewServeMux()

	// Add the gRPC-Gateway handler
	mux.Handle("/", handler)

	// Get basePath from environment for backward compatibility
	basePath := os.Getenv("SERVER_BASE_PATH")
	if basePath == "" {
		basePath = os.Getenv("BASE_PATH")
	}
	if basePath == "" {
		basePath = "/telemetry"
	}

	// Serve Swagger UI and JSON
	serveSwaggerUI(mux, basePath)
	serveSwaggerJSON(mux, swaggerDir, basePath)

	// Add logging middleware
	loggedMux := loggingMiddleware(logger, mux)

	return &http.Server{
		Addr:     addr,
		Handler:  loggedMux,
		ErrorLog: log.New(os.Stderr, "httpSrv: ", log.LstdFlags), // Configure the logger for the HTTP server
	}
}

// loggingMiddleware is a middleware that logs HTTP requests
func loggingMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(start)
		logger.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", duration,
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

		// Update the base path
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
