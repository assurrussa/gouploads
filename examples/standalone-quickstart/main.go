package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	outboxmigrations "github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsfailedrepo"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsrepo"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlinit"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/assurrussa/outbox/outbox"
	outboxlogger "github.com/assurrussa/outbox/outbox/logger"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/assurrussa/gouploads/host"
)

type demoStrategy struct{}

func (s *demoStrategy) CanUpload(_ context.Context, _ host.UploadContext) error {
	return nil
}

func (s *demoStrategy) GetConfig(_ context.Context, _ host.UploadContext) *host.FileUploadConfig {
	return &host.FileUploadConfig{
		MaxFileSize:       500 * 1024 * 1024, // 500 MB
		AllowedExtensions: []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".mp4", ".webm", ".pdf"},
		AllowedMimeTypes: map[string][]string{
			".jpg":  {"image/jpeg"},
			".jpeg": {"image/jpeg"},
			".png":  {"image/png"},
			".webp": {"image/webp"},
			".gif":  {"image/gif"},
			".mp4":  {"video/mp4"},
			".webm": {"video/webm"},
			".pdf":  {"application/pdf"},
		},
	}
}

func (s *demoStrategy) GetAfterJobs(_ context.Context, _ host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}

func envOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func main() {
	if err := run(); err != nil {
		slog.Error("application terminated with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.Info("starting gouploads standalone quickstart")
	slog.Warn("Demo server is bound to 127.0.0.1:8080. This quickstart is for local development only.")

	pgURL := envOrDefault("DATABASE_URL", "postgres://postgres:password@127.0.0.1:5432/gouploads?sslmode=disable")
	s3Endpoint := envOrDefault("S3_ENDPOINT", "http://127.0.0.1:9000")
	s3Bucket := envOrDefault("S3_BUCKET", "uploads-public")
	s3StagingBucket := envOrDefault("S3_STAGING_BUCKET", "uploads-staging")
	s3AccessKey := envOrDefault("S3_ACCESS_KEY", "minioadmin")
	s3SecretKey := envOrDefault("S3_SECRET_KEY", "minioadmin")

	// 1. Connect to PostgreSQL
	db, err := pgsqlinit.Create(ctx, pgURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}

	// 2. Apply migrations (both outbox and gouploads schema)
	poolEngine, ok := db.DB().(storage.DBPgxEnginePool)
	if !ok {
		return errors.New("underlying DB is not a DBPgxEnginePool")
	}
	sqlDB := stdlib.OpenDBFromPool(poolEngine.Pool())
	defer func() { _ = sqlDB.Close() }()

	outboxProvider, err := goose.NewProvider(
		goose.DialectPostgres,
		sqlDB,
		outboxmigrations.FS,
		goose.WithTableName("outbox_schema_versions"),
	)
	if err != nil {
		return fmt.Errorf("init outbox migrations provider: %w", err)
	}
	if _, err := outboxProvider.Up(ctx); err != nil {
		return fmt.Errorf("apply outbox migrations: %w", err)
	}

	gouploadsFS, err := host.MigrationsFS()
	if err != nil {
		return fmt.Errorf("load gouploads migrations FS: %w", err)
	}
	gouploadsProvider, err := goose.NewProvider(
		goose.DialectPostgres,
		sqlDB,
		gouploadsFS,
		goose.WithTableName("gouploads_schema_versions"),
	)
	if err != nil {
		return fmt.Errorf("init gouploads migrations provider: %w", err)
	}
	if _, err := gouploadsProvider.Up(ctx); err != nil {
		return fmt.Errorf("apply gouploads migrations: %w", err)
	}
	slog.Info("database migrations applied successfully")

	// 3. Initialize Outbox queue
	jobsRepo := jobsrepo.Must(jobsrepo.NewOptions(db))
	jobsFailedRepo := jobsfailedrepo.Must(jobsfailedrepo.NewOptions(db))
	outboxService, err := outbox.New(
		outbox.WithJobsRepo(jobsRepo),
		outbox.WithJobsStatRepo(jobsRepo),
		outbox.WithJobsFailedRepo(jobsFailedRepo),
		outbox.WithTransactor(transaction.New(db.DB())),
		outbox.WithWorkers(2),
		outbox.WithIdleTime(200*time.Millisecond),
		outbox.WithReserveFor(time.Minute),
		outbox.WithLogger(outboxlogger.Discard()),
	)
	if err != nil {
		return fmt.Errorf("create outbox service: %w", err)
	}

	publicBaseURL, err := url.JoinPath(s3Endpoint, s3Bucket)
	if err != nil {
		return fmt.Errorf("compose public base url: %w", err)
	}

	// 4. Configure storage and create OriginalRuntime
	storageCfg := host.StorageConfig{
		Driver: host.StorageDriverS3,
		Public: host.StoragePublicConfig{
			BaseURL: publicBaseURL,
			Prefix:  "media/v1",
		},
		S3: host.StorageS3Config{
			Endpoint:       s3Endpoint,
			Region:         "us-east-1",
			Bucket:         s3Bucket,
			StagingBucket:  s3StagingBucket,
			AccessKey:      s3AccessKey,
			SecretKey:      s3SecretKey,
			ForcePathStyle: true,
		},
	}

	runtime, err := host.NewOriginalRuntime(storageCfg, host.OriginalRuntimeDeps{
		Database:    db,
		Transaction: transaction.New(db.DB()),
		Outbox:      outboxService,
		Logger:      host.NewSlogLogger(slog.Default()),
	})
	if err != nil {
		return fmt.Errorf("initialize gouploads runtime: %w", err)
	}

	// Register outbox background jobs and run worker
	if err := outboxService.RegisterJobs(runtime.Jobs...); err != nil {
		return fmt.Errorf("register outbox jobs: %w", err)
	}
	workerErrChan := make(chan error, 1)
	go func() {
		if err := outboxService.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("outbox worker stopped with error", "error", err)
			workerErrChan <- err
		}
	}()
	slog.Info("outbox worker started")

	// 5. Construct HTTP Upload Handler and Standard net/http handler
	// In this demo, a stable UserUUID is generated for the session so TUS ownership checks succeed.
	demoUserUUID := host.NewUserID()
	contextBuilder := func(_ context.Context, _ map[string]string) (host.UploadContext, error) {
		return host.UploadContext{
			UserID:    1,
			UserUUID:  demoUserUUID,
			SessionID: "demo-session",
		}, nil
	}
	urlComposer := func(path string) string {
		return host.ComposeFileURL(host.FilesBaseURL(storageCfg), host.FilesBucket(storageCfg), path)
	}

	policy := host.UploadHandlerPolicy{
		AllowDefaultStrategy: true,
		AllowAnonymousTUS:    false,
		// TrustRouteGuards is false: standalone demo without external route guards relies
		// directly on the Authorize callback for explicit object authorization.
		Authorize: func(_ context.Context, _ host.UploadContext, _ string, _ host.File) error {
			return nil // Demo authorizer permits operations for demo user
		},
	}

	uploadHandler := host.NewUploadHandlerWithPolicy(
		runtime.Uploader,
		runtime.Files,
		runtime.TusStore,
		host.NewSlogLogger(slog.Default()),
		contextBuilder,
		urlComposer,
		policy,
	)
	uploadHandler.RegisterStrategy("default", &demoStrategy{})

	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	if err != nil {
		return fmt.Errorf("failed to create standard upload handler: %w", err)
	}

	// 6. Mount onto standard net/http ServeMux
	mux := http.NewServeMux()
	mux.Handle("/files/", stdHandler)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, "index.html")
	})

	server := &http.Server{
		Addr:              "127.0.0.1:8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErrChan := make(chan error, 1)
	go func() {
		slog.Info("server listening on http://127.0.0.1:8080")
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("http server failed", "error", err)
			serverErrChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("shutting down...")
	case err := <-workerErrChan:
		slog.Error("outbox worker crashed", "error", err)
		stop()
		return fmt.Errorf("outbox worker crashed: %w", err)
	case err := <-serverErrChan:
		slog.Error("http server crashed", "error", err)
		stop()
		return fmt.Errorf("http server crashed: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	slog.Info("server exited cleanly")
	return nil
}
