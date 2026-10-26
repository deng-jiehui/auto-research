package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/deng-jiehui/auto-research/internal/app"
	"github.com/deng-jiehui/auto-research/internal/config"
	"github.com/deng-jiehui/auto-research/internal/domain"
	"github.com/deng-jiehui/auto-research/internal/infra/documents"
	"github.com/deng-jiehui/auto-research/internal/infra/mcp"
	"github.com/deng-jiehui/auto-research/internal/infra/sqlite"
	"github.com/deng-jiehui/auto-research/internal/transport/httpapi"
	"github.com/goharness/goharness/pkg/state"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "serve" {
		return errors.New("用法：autoresearch serve --config config.example.json")
	}
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(out)
	configPath := flags.String("config", "config.example.json", "配置文件路径")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("serve 不接受位置参数")
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	store, err := sqlite.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()
	logger := slog.New(slog.NewTextHandler(out, nil))
	projects := app.NewProjectService(store)
	sessions := app.NewSessionService(store)
	papers := app.NewPaperService(store)
	source, closeSource, err := mcp.OpenSource(ctx, cfg.Academic, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("初始化学术来源失败: %w", err)
	}
	defer closeSource()
	if cfg.Academic.Effective().Mode == "arxiv_mcp" {
		source = documents.NewArxivSource(source, nil, cfg.DocumentPDFCommand)
	}
	academic := app.NewAcademicService(source, cfg.Academic.Effective().Mode)
	evidence := app.NewEvidenceService(store)
	documentService := app.NewDocumentService(store, papers, academic)
	directions := app.NewDirectionService(store, store)
	research := app.NewResearchService(store)
	turns := app.NewTurnService(store, store, store)
	if err = turns.SetAgentMode(cfg.AgentMode); err != nil {
		return fmt.Errorf("初始化 Agent 模式失败: %w", err)
	}
	turns.SetAcademicTools(app.AcademicToolsDeps{Academic: academic, Papers: papers, Evidence: evidence, Documents: documentService})
	turns.SetResearchService(research)
	turns.SetResearchResources(directions, &app.ProjectResources{Projects: store, Directions: directions, Research: research, Papers: papers, Documents: documentService})
	turns.SetWorkspacePolicy(app.WorkspacePolicy{Root: cfg.WorkspaceRoot, Shell: cfg.WorkspaceShell})
	apiKey := ""
	if cfg.APIKeyEnv != "" {
		apiKey = os.Getenv(cfg.APIKeyEnv)
	}
	if _, err = turns.ConfigureModel(app.ModelConfigInput{Mode: cfg.ModelMode, Endpoint: cfg.ModelEndpoint, Model: cfg.ModelName, APIKey: apiKey}); err != nil {
		return fmt.Errorf("初始化模型失败: %w", err)
	}
	turns.SetCheckpointFactory(func(scope domain.Scope, sessionID, turnID string) state.Store {
		return sqlite.NewCheckpointStore(store, scope, sessionID, turnID)
	})
	turns.Start(ctx)
	defer turns.Stop()
	handler := httpapi.NewHandlerWithResearch(projects, sessions, turns, papers, academic, evidence, store, os.Getenv(cfg.AuthTokenEnv), logger, documentService, directions)
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 130 * time.Second, IdleTimeout: 60 * time.Second}
	listener, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("监听失败: %w", err)
	}
	logger.Info("AutoResearch 本地服务已启动", "address", listener.Addr().String())
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			server.Close()
			return err
		}
		return nil
	}
}
