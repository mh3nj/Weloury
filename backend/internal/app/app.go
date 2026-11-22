// Package app is Weloury core's composition root. It owns every long-lived
// subsystem (config, logger, database, event bus, IPC server, background
// workers) and coordinates starting and stopping all of them cleanly.
package app

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"weloury/internal/automation"
	"weloury/internal/config"
	"weloury/internal/containers"
	"weloury/internal/diagnostics"
	"weloury/internal/eventbus"
	"weloury/internal/health"
	"weloury/internal/ipc"
	"weloury/internal/logging"
	"weloury/internal/logs"
	"weloury/internal/metrics"
	"weloury/internal/scheduler"
	"weloury/internal/storage"
	"weloury/internal/system"
)

// App wires together every subsystem this build of Weloury core has.
type App struct {
	cfg      config.Config
	logger   *slog.Logger
	closeLog func() error
	db       *sql.DB
	bus      *eventbus.Bus
	events   *storage.EventRepository
	server   *ipc.Server

	logsEngine           *logs.Engine
	healthEngine         *health.Engine
	metricsEngine        *metrics.Engine
	metricsRepo          *storage.MetricsRepository
	schedulerEngine      *scheduler.Engine
	automationEngine     *automation.Engine
	containersProvider   *containers.Provider
	diagnosticsGenerator *diagnostics.Generator
}

// New wires up every subsystem but starts no background work and does not
// begin serving IPC - call Run for that. Separating construction from
// running means a startup failure (a bad config file, a port already in
// use, a locked database, ...) is reported as a clean error from a single
// call site rather than leaving a half-started process behind.
func New() (*App, error) {
	cfgPath, err := config.Path()
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	dataDir, err := config.Dir()
	if err != nil {
		return nil, fmt.Errorf("resolve data dir: %w", err)
	}

	// bus is created before the logger (rather than in its usual spot
	// alongside the other in-memory subsystems, below) specifically so
	// logsEngine can be handed to logging.New as its Tap - see
	// internal/logs's package doc comment on why the log engine
	// observes records via a Tap rather than re-parsing the rotated log
	// file.
	bus := eventbus.New()
	logsEngine := logs.New(bus, 0)

	logger, closeLog, err := logging.New(logging.Options{
		Level:    cfg.LogLevel,
		Format:   cfg.LogFormat,
		FilePath: filepath.Join(dataDir, "weloury-core.log"),
		Tap:      logsEngine.Capture,
	})
	if err != nil {
		return nil, fmt.Errorf("initialize logging: %w", err)
	}

	db, err := storage.Open(context.Background(), filepath.Join(dataDir, "weloury.db"))
	if err != nil {
		_ = closeLog()
		return nil, fmt.Errorf("open database: %w", err)
	}

	events := storage.NewEventRepository(db)
	healthRepo := storage.NewHealthRepository(db)
	metricsRepo := storage.NewMetricsRepository(db)
	schedulerRepo := storage.NewSchedulerRepository(db)
	automationRepo := storage.NewAutomationRepository(db)

	sys := system.New()
	healthEngine := health.New(sys, bus, healthRepo, logger)
	metricsEngine := metrics.New(sys, metricsRepo, logger)
	schedulerEngine := scheduler.New(bus, schedulerRepo, logger)
	automationEngine := automation.New(sys, bus, automationRepo, logger)
	containersProvider := containers.New()
	diagnosticsGenerator := diagnostics.New(sys, healthEngine, events)

	// settings is the live, in-memory copy of the config that the
	// settings.* FWP methods read and update; Set persists through the
	// same config.Save the startup load path uses, so there is exactly
	// one write path and one current value.
	settings := newSettingsStore(cfgPath, cfg)

	router := ipc.NewRouter()
	registerMethods(registration{
		router:      router,
		sys:         sys,
		bus:         bus,
		events:      events,
		health:      healthEngine,
		metrics:     metricsEngine,
		logs:        logsEngine,
		scheduler:   schedulerEngine,
		automation:  automationEngine,
		containers:  containersProvider,
		diagnostics: diagnosticsGenerator,
		getSettings: settings.Get,
		setSettings: settings.Set,
	})

	server, err := ipc.NewServer(ipc.Config{
		Host:   cfg.IPCHost,
		Port:   cfg.IPCPort,
		Router: router,
		Bus:    bus,
		Logger: logger,
	})
	if err != nil {
		_ = db.Close()
		_ = closeLog()
		return nil, fmt.Errorf("start ipc server: %w", err)
	}

	return &App{
		cfg:                  cfg,
		logger:               logger,
		closeLog:             closeLog,
		db:                   db,
		bus:                  bus,
		events:               events,
		server:               server,
		logsEngine:           logsEngine,
		healthEngine:         healthEngine,
		metricsEngine:        metricsEngine,
		metricsRepo:          metricsRepo,
		schedulerEngine:      schedulerEngine,
		automationEngine:     automationEngine,
		containersProvider:   containersProvider,
		diagnosticsGenerator: diagnosticsGenerator,
	}, nil
}

// Server exposes the IPC server so cmd/weloury-core can print the startup
// handshake before entering Run's blocking loop.
func (a *App) Server() *ipc.Server { return a.server }

// Run starts every background subsystem and blocks until ctx is canceled
// (an OS signal, in normal operation - see cmd/weloury-core) or the desktop
// shell requests a graceful shutdown over IPC, then stops everything in a
// coordinated order and returns.
//
// Goroutine lifecycle: every background goroutine started here receives
// runCtx, a single context derived from ctx. Canceling it is the one
// signal all of them - the heartbeat ticker, the event-persistence
// subscriber, the retention pruner, and every engine - shut down on. Run
// does not return until every one of them has (via wg.Wait()), so nothing
// outlives this function: no leaked goroutines, no writes racing a closed
// database after Close is called.
//
// Subsystem start order is not arbitrary. internal/automation.Engine is
// started first, specifically because Engine.Start subscribes to the bus
// synchronously before returning (see its doc comment) - the same fix
// Phase 4 applied to the event-persistence bridge below, generalized:
// *any* subscriber that needs to observe another subsystem's very first
// published event must finish subscribing before that other subsystem
// starts. scheduler.Engine and health.Engine each fire their own first
// scan from a background goroutine immediately upon Start - a real, not
// hypothetical, race if automation's subscription weren't guaranteed
// already active first. metrics.Engine has no equivalent concern (it
// publishes nothing automation rules could trigger on) but is started
// alongside the others for the same "every subsystem is running before
// we accept requests" reasoning.
func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := a.automationEngine.Start(runCtx); err != nil {
		a.logger.Error("failed to start automation engine", "error", err)
	}
	if err := a.schedulerEngine.Start(runCtx); err != nil {
		a.logger.Error("failed to start scheduler engine", "error", err)
	}
	if err := a.healthEngine.Start(runCtx); err != nil {
		a.logger.Error("failed to start health check engine", "error", err)
	}
	a.metricsEngine.Start(runCtx)

	// Subscribed here, synchronously, rather than inside
	// runEventPersistence itself - see that function's doc comment for
	// why: it must be registered before this function publishes
	// TypeBackendStarted a few lines down, and Subscribe's synchronous
	// registration (under eventbus.Bus's mutex) is what makes "call it
	// here, first" an actual guarantee rather than a smaller race.
	// context.Background() (not runCtx) so this subscription's lifetime
	// is controlled by runEventPersistence's own drain-on-shutdown logic
	// rather than the bus racing to close the channel out from under it.
	persistCh, persistUnsub := a.bus.Subscribe(context.Background(), nil, eventbus.DefaultSubscriberBuffer)

	var wg sync.WaitGroup
	wg.Add(7)
	go func() { defer wg.Done(); a.runHeartbeat(runCtx) }()
	go func() { defer wg.Done(); a.runEventPersistence(runCtx, persistCh, persistUnsub) }()
	go func() { defer wg.Done(); a.runRetentionPruner(runCtx) }()
	go func() { defer wg.Done(); a.healthEngine.Wait() }()
	go func() { defer wg.Done(); a.metricsEngine.Wait() }()
	go func() { defer wg.Done(); a.schedulerEngine.Wait() }()
	go func() { defer wg.Done(); a.automationEngine.Wait() }()

	serveErrCh := make(chan error, 1)
	go func() { serveErrCh <- a.server.Serve(runCtx) }()

	a.bus.Publish(eventbus.Event{
		Type:     eventbus.TypeBackendStarted,
		Source:   "app",
		Severity: eventbus.SeverityInfo,
	})
	a.logger.Info("weloury-core started", "ipc_addr", a.server.Addr().String())

	var serveErr error
	select {
	case <-ctx.Done():
		a.logger.Info("shutdown requested via signal")
	case <-a.server.ShutdownRequested():
		a.logger.Info("shutdown requested via ipc")
	case serveErr = <-serveErrCh:
		if serveErr != nil {
			a.logger.Error("ipc server exited unexpectedly", "error", serveErr)
		}
	}

	// Published before canceling background work so the still-running
	// event-persistence subscriber (below) can record it.
	a.bus.Publish(eventbus.Event{
		Type:     eventbus.TypeBackendStopping,
		Source:   "app",
		Severity: eventbus.SeverityInfo,
	})

	cancel()
	if serveErr == nil {
		<-serveErrCh
	}
	wg.Wait()

	a.logger.Info("weloury-core stopped")
	return serveErr
}

// LogStartupContext records a startup qualifier in the log - e.g. whether
// this session began hidden to the tray (see cmd's --tray flag). Kept on
// App (rather than a direct logger call in main) so the composition root
// stays the single owner of log wiring.
func (a *App) LogStartupContext(context string) {
	a.logger.Info("startup context", "context", context)
}

// Close releases resources not already released by Run: the database
// connection and the log file handle. Call it after Run returns.
func (a *App) Close() error {
	var firstErr error
	if err := a.db.Close(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close database: %w", err)
	}
	if err := a.closeLog(); err != nil && firstErr == nil {
		firstErr = fmt.Errorf("close log file: %w", err)
	}
	return firstErr
}
