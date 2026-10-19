package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/api"
	"github.com/oh-my-cpa/oh-my-cpa/internal/auth"
	"github.com/oh-my-cpa/oh-my-cpa/internal/automation"
	"github.com/oh-my-cpa/oh-my-cpa/internal/config"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/discovery"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/demo"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/feishu"
	"github.com/oh-my-cpa/oh-my-cpa/internal/ollama"
	"github.com/oh-my-cpa/oh-my-cpa/internal/pricing"
	"github.com/oh-my-cpa/oh-my-cpa/internal/release"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/timezone"
	"github.com/oh-my-cpa/oh-my-cpa/internal/updater"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage/ingest"
)

type App struct {
	cfg        config.Config
	db         *repository.DB
	repo       *repository.Repository
	cipher     *crypto.Cipher
	handler    *api.Handler
	httpServer *http.Server
	logger     *slog.Logger
	// pipeline captures CPA request records into the local database. Nil when
	// ingestion is disabled or no CPA instance is configured yet.
	pipeline *ingest.Pipeline
	// pricing keeps model prices fresh from OpenRouter; nil-safe service.
	pricing *pricing.Service
	// release observes both products' published versions. It is nil in demo mode,
	// where nothing may leave the process at all.
	release *release.Service
	// maintenance owns its own database connection, so it is closed with the app.
	maintenance *repository.MaintenanceService
	// automation runs account-level auto-management (weekly-exhaustion disable and
	// reset-time enable). Nil in demo mode, which performs no external write.
	automation *automation.Manager
	// ollama scrapes the configured Ollama accounts. Nil in demo mode, which must not
	// make an outbound request.
	ollama *ollama.Collector
	// feishu is the notification bot: the long connection, the classification loop and
	// the outbound sender. Nil in demo mode.
	feishu *feishu.Service
	// upstream is the in-process CPA fixture. It is non-nil only in demo mode, and
	// it is the reason a demo deployment can boot with no gateway at all.
	upstream *demo.Upstream
}

func New(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.IsDemoMode {
		// Started before anything reads the configuration, because the fixture's
		// loopback address and its per-process key are the demo's whole upstream: the
		// session manager below derives from that key, and the instance the
		// application bootstraps points at that address. Nothing else is reachable,
		// so the demo cannot be pointed at a real gateway even if the deployment
		// inherits configuration for one.
		upstream, err := demo.StartUpstream(logger)
		if err != nil {
			return nil, err
		}
		cfg.CPA = config.CPAConfig{
			BaseURL:       upstream.BaseURL(),
			ManagementKey: upstream.ManagementKey(),
		}
		logger.Info("demo mode enabled", "upstream", upstream.BaseURL(), "data_dir", cfg.DataDir)
		app, err := newApp(ctx, cfg, logger)
		if err != nil {
			_ = upstream.Close()
			return nil, err
		}
		app.upstream = upstream
		return app, nil
	}
	return newApp(ctx, cfg, logger)
}

// newApp builds the application for an already-resolved configuration. Demo mode
// and the self-hosted path share it entirely; the only difference between them is
// where the configuration came from.
func newApp(ctx context.Context, cfg config.Config, logger *slog.Logger) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if cfg.MasterKey == "" {
		return nil, errors.New("OMCPA_MASTER_KEY is required")
	}
	cipher, err := crypto.New(cfg.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("initialize secret cipher: %w", err)
	}
	// Oh My CPA has no separate administrator password: the CPA management key
	// is the only login credential. The app still boots without one so the UI
	// can explain what to configure; the login endpoint then reports 503.
	var authManager *auth.Manager
	if strings.TrimSpace(cfg.CPA.ManagementKey) != "" {
		authManager, err = auth.New(cfg.CPA.ManagementKey, cfg.BasePath, cfg.PublicURL)
		if err != nil {
			return nil, fmt.Errorf("initialize administrator authentication: %w", err)
		}
	}
	if cfg.IsDemoMode {
		// A demo restarts from a clean fixture every time. The platform scales a demo
		// instance to zero and back, and a database left behind by an earlier boot
		// would end its history hours ago - the fifteen-minute and one-hour windows
		// would be empty on a page whose whole point is that it is alive.
		if err := demo.ResetDatabase(cfg.DatabasePath); err != nil {
			return nil, err
		}
	}
	db, err := repository.Open(ctx, cfg.DatabasePath,
		repository.WithMigrationBackup(cipher, filepath.Join(cfg.DataDir, "backups"), 5),
	)
	if err != nil {
		return nil, err
	}
	repo := repository.New(db)
	if err := repo.LoadTimezone(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("load timezone: %w", err)
	}
	logger = slog.New(timezone.Handler{Handler: logger.Handler(), Location: repo.Timezone().Location})
	slog.SetDefault(logger)
	if err := bootstrapDefaultInstance(ctx, cfg, repo, cipher); err != nil {
		db.Close()
		return nil, err
	}
	if cfg.IsDemoMode {
		// The fixture is seeded before the server accepts a request, so the first
		// page a visitor opens already has the history a running gateway would have.
		stats, err := demo.Seed(ctx, repo, time.Now().UTC())
		if err != nil {
			db.Close()
			return nil, err
		}
		logger.Info("demo fixture ready",
			"requests", stats.Requests,
			"prices", stats.Prices,
			"aliases", stats.Aliases,
			"seed_duration_ms", stats.DurationMS)
		if err := seedDemoResources(ctx, cfg, repo, cipher, logger); err != nil {
			db.Close()
			return nil, err
		}
	}
	handler := api.NewHandler(cfg, repo, cipher, logger, authManager)
	// Account auto-management shares one service between the HTTP routes and the
	// background pass, so the per-account lock and the intent version agree.
	automationService := handler.BuildAutomationService()
	handler.SetAutomation(automationService)

	// Feishu notifications. The bot is built here so its authenticated settings
	// routes exist; the long connection is started by Run under the process
	// lifetime context and stopped on shutdown. A failure to build it is logged and
	// leaves the routes unregistered rather than failing startup.
	feishuAPI, feishuErr := handler.BuildFeishuAPI(ctx, handler.BuildFeishuReportSource(), feishuOverlay(cfg))
	if feishuErr != nil {
		logger.Warn("feishu notifications could not be built", "error", feishuErr)
	} else {
		handler.SetFeishuAPI(feishuAPI)
	}

	// Zero-config pricing: the service syncs from OpenRouter once at startup and
	// then daily, and custom rows and operator pins always win. Failures degrade
	// to stale prices, never to wrong ones.
	pricingService := pricing.NewService(repo, pricing.NewOpenRouterClient(), logger)
	pricingService.SetModelLister(&cpaModelLister{repo: repo, cipher: cipher, cfg: cfg})
	handler.SetPricing(pricingService)

	pipeline, err := buildUsagePipeline(cfg, repo, handler, logger, cipher)
	if err != nil {
		db.Close()
		return nil, err
	}
	if pipeline != nil && pipeline.Runner() != nil {
		pipeline.Runner().SetRefreshHandler(pricingService.NotifyModelsChanged)
	}
	// Release observation. A demo does not build one at all: the fixture answers the
	// page's version questions, and "the demonstration performs no outbound request"
	// stays a property of the code rather than a promise about the environment.
	var releaseService *release.Service
	var maintenanceService *repository.MaintenanceService
	if cfg.IsDemoMode {
		// A demo answers this page from its own fixture. The service is the production
		// one with only its feed source swapped, so the page renders the real
		// comparison and the real merged log, and the demonstration still performs no
		// outbound request.
		releaseService, err = demo.BuildReleaseService(repo, logger, time.Now().UTC())
		if err != nil {
			db.Close()
			return nil, err
		}
		// The fixture is read once before the first request. A demonstration that only
		// filled its index when a visitor pressed a button would show the page's empty
		// state on the very page the fixture exists to demonstrate - and the read is
		// local, so populating it here costs no request.
		releaseService.CheckAll(ctx)
		handler.SetRelease(releaseService)
	} else {
		releaseService, err = release.New(release.Options{
			Repository:    repo,
			Logger:        logger,
			OMCRepository: cfg.Release.OMCRepository,
			CPARepository: cfg.Release.CPARepository,
		})
		if err != nil {
			db.Close()
			return nil, err
		}
		// Validators describe notes that lived in a previous process, so they are
		// dropped before the first check: a 304 would otherwise claim "unchanged"
		// about notes this process cannot show.
		if err := releaseService.ForgetStoredValidators(ctx); err != nil {
			logger.Warn("could not clear stored release validators", "error", err)
		}
		handler.SetRelease(releaseService)

		// The console holds no Docker access of its own. Updates go through the sidecar,
		// and a native install, which has no containers to replace, is told apart from a
		// containerised one that simply has no sidecar yet.
		if cfg.Updater.Enabled {
			_, dockerErr := os.Stat("/.dockerenv")
			handler.SetUpdater(updater.NewClient(cfg.Updater.URL, cfg.MasterKey), release.NewManifestClient(), dockerErr == nil)
			// A console update ends the process that admitted it, so its outcome is
			// collected here, by the process that replaced it.
			handler.WatchInterruptedUpdate()
		}

		if db != nil {
			maintenanceService, err = repository.NewMaintenanceService(ctx, db)
			if err != nil {
				db.Close()
				return nil, err
			}
			handler.SetMaintenance(maintenanceService)
		}
	}

	// Account auto-management performs a CPA write when it disables or enables a
	// credential, so a demo never builds the loop. An explicit environment false
	// leaves it unbuilt too; an unset default still builds it so a later web enable
	// takes effect without a restart.
	var automationManager *automation.Manager
	if !cfg.IsDemoMode && cfg.Automation.LoopAllowed() {
		automationManager = &automation.Manager{
			Service:  automationService,
			Interval: cfg.Automation.EvaluationInterval,
			Logger:   logger,
		}
	}

	// The Ollama collector scrapes ollama.com with each account's cookie, so a demo
	// never builds it. An operator may also switch it off without removing accounts.
	// The same instance is handed to the handler, so the console's manual refresh and
	// the create/update triggers share its lock, concurrency cap and backoff.
	var ollamaCollector *ollama.Collector
	if !cfg.IsDemoMode && cfg.Ollama.CollectorEnabled {
		ollamaCollector = handler.BuildOllamaService()
		handler.SetOllamaService(ollamaCollector)
	}

	var feishuService *feishu.Service
	if feishuErr == nil && feishuAPI != nil {
		feishuService = feishuAPI.Service()
	}

	return &App{
		cfg:     cfg,
		db:      db,
		repo:    repo,
		cipher:  cipher,
		handler: handler,
		logger:  logger,

		pipeline: pipeline,

		pricing:     pricingService,
		release:     releaseService,
		maintenance: maintenanceService,
		automation:  automationManager,
		ollama:      ollamaCollector,
		feishu:      feishuService,
	}, nil
}

// feishuOverlay projects the Feishu environment configuration onto the bot's
// override. The plaintext app secret is carried here only.
func feishuOverlay(cfg config.Config) feishu.Overlay {
	return feishu.Overlay{
		ForceDisabled: cfg.Feishu.ForceDisabled,
		Enabled:       cfg.Feishu.Enabled,
		AppID:         cfg.Feishu.AppID,
		AppSecret:     cfg.Feishu.AppSecret,
		ChatID:        cfg.Feishu.ChatID,
	}
}

// usageUpstream adapts the CPA management client to the collector's narrower
// view of it. The concrete *management.UsageStream already satisfies
// ingest.Stream; only the client method signature needs wrapping, which keeps
// the ingest package free of any dependency on the management client type.
type usageUpstream struct{ client *management.Client }

func (u usageUpstream) ProbeUsageChannel(ctx context.Context) error {
	return u.client.ProbeUsageChannel(ctx)
}

func (u usageUpstream) OpenUsageStream(ctx context.Context, channel string) (ingest.Stream, error) {
	stream, err := u.client.OpenUsageStream(ctx, channel)
	if err != nil {
		return nil, err
	}
	return stream, nil
}

func (u usageUpstream) PopUsageQueue(ctx context.Context, count int) ([]string, error) {
	return u.client.PopUsageQueue(ctx, count)
}

func (u usageUpstream) UsageQueueJSON(ctx context.Context, count int) ([]string, error) {
	return u.client.UsageQueueJSON(ctx, count)
}

// cpaModelLister satisfies pricing.ModelLister by discovering all models and
// aliases configured across all providers and auth-files in the default CPA instance.
type cpaModelLister struct {
	repo   *repository.Repository
	cipher *crypto.Cipher
	cfg    config.Config
}

func (l *cpaModelLister) ListConfiguredModelCatalog(ctx context.Context) (map[string]string, error) {
	snapshot, err := l.ListConfiguredModelSnapshot(ctx)
	return snapshot.Models, err
}

func (l *cpaModelLister) ListConfiguredModelSnapshot(ctx context.Context) (pricing.ModelCatalogSnapshot, error) {
	instance, err := l.repo.GetInstance(ctx, "default")
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no rows") {
			return pricing.ModelCatalogSnapshot{}, errors.New("CPA instance is not configured")
		}
		return pricing.ModelCatalogSnapshot{}, err
	}
	if strings.TrimSpace(instance.BaseURL) == "" {
		return pricing.ModelCatalogSnapshot{}, errors.New("CPA instance is not configured")
	}
	key, err := l.cipher.Decrypt(instance.ManagementKeyCiphertext, instance.ManagementKeyNonce)
	if err != nil {
		return pricing.ModelCatalogSnapshot{}, fmt.Errorf("decrypt CPA management key: %w", err)
	}
	defer func() {
		for i := range key {
			key[i] = 0
		}
	}()
	client, err := management.NewClient(instance.BaseURL, string(key), l.cfg.RequestTimeout, l.cfg.TLSSkipVerify)
	if err != nil {
		return pricing.ModelCatalogSnapshot{}, fmt.Errorf("build CPA management client: %w", err)
	}
	snapshot, err := client.ListConfiguredModelSnapshot(ctx)
	if err != nil {
		return pricing.ModelCatalogSnapshot{}, err
	}
	providers := make([]pricing.CatalogProvider, 0, len(snapshot.Providers))
	for _, provider := range snapshot.Providers {
		providers = append(providers, pricing.CatalogProvider{ID: provider.ID, Family: provider.Family, Name: provider.Name, Prefix: provider.Prefix, EndpointHost: provider.EndpointHost, Channel: provider.Channel, Priority: provider.Priority, IsOAuth: provider.IsOAuth, Models: provider.ListPricingModelIDs()})
	}
	return pricing.ModelCatalogSnapshot{Models: snapshot.Models, Providers: providers}, nil
}

// buildUsagePipeline wires capture, decode and maintenance over the default CPA
// instance. It is intentionally skipped, not failed, when CPA is not configured:
// the UI must still boot and explain what is missing.
func buildUsagePipeline(cfg config.Config, repo *repository.Repository, handler *api.Handler, logger *slog.Logger, fingerprinter interface {
	Fingerprint(...string) (string, error)
}) (*ingest.Pipeline, error) {
	if !cfg.Usage.Enabled || cfg.Usage.Mode == string(ingest.ModeOff) {
		logger.Info("usage ingestion disabled", "mode", cfg.Usage.Mode, "enabled", cfg.Usage.Enabled)
		return nil, nil
	}
	if strings.TrimSpace(cfg.CPA.BaseURL) == "" || strings.TrimSpace(cfg.CPA.ManagementKey) == "" {
		logger.Info("usage ingestion waiting for a configured CPA instance")
		return nil, nil
	}
	client, err := management.NewClient(cfg.CPA.BaseURL, cfg.CPA.ManagementKey, cfg.RequestTimeout, cfg.TLSSkipVerify)
	if err != nil {
		return nil, fmt.Errorf("build CPA usage client: %w", err)
	}
	runner, err := ingest.NewRunner("default", usageUpstream{client}, repo, repo, logger, ingest.Config{
		Mode:            ingest.Mode(cfg.Usage.Mode),
		IdleInterval:    cfg.Usage.IdleInterval,
		MaxIdleInterval: cfg.Usage.MaxIdleInterval,
		BatchSize:       cfg.Usage.BatchSize,
		CollectErrors:   cfg.Usage.CollectErrors,
	})
	if err != nil {
		return nil, fmt.Errorf("build usage collector: %w", err)
	}
	processor, err := ingest.NewProcessorWithFingerprinter(repo, logger, 0, cfg.Usage.IdleInterval, fingerprinter)
	if err != nil {
		return nil, fmt.Errorf("build usage processor: %w", err)
	}
	maintenance, err := ingest.NewMaintenance(repo, logger, cfg.Usage.AggregateInterval, cfg.Usage.RetentionDays, cfg.Usage.InboxRetentionDays)
	if err != nil {
		return nil, fmt.Errorf("build usage maintenance: %w", err)
	}
	pipeline, err := ingest.NewPipeline(runner, processor, maintenance, repo)
	if err != nil {
		return nil, fmt.Errorf("build usage pipeline: %w", err)
	}
	handler.SetUsagePipeline(pipeline)
	return pipeline, nil
}

func (a *App) Handler() http.Handler {
	return a.handler.Router()
}

func (a *App) Run(ctx context.Context) error {
	// Background loops run under a context this function owns, so every way out -
	// a signal, a failed pipeline, a failed listener - stops them the same way.
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	// The Ollama collector's process lifetime context is installed BEFORE the
	// listener accepts anything. A request that arrives in the first instant then
	// finds a started service; without this it would reach a collector that has no
	// lifecycle context yet and either fall back to the request context (which ends
	// with the request, truncating a scrape and its persistence) or refuse. Starting
	// here makes the pre-listener window impossible rather than merely unlikely.
	if a.ollama != nil {
		a.ollama.Start(runCtx)
	}

	a.httpServer = &http.Server{
		Addr:              a.cfg.ListenAddr,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		a.logger.Info("HTTP server listening", "addr", a.cfg.ListenAddr, "base_path", a.cfg.BasePath)
		serverErrors <- a.httpServer.ListenAndServe()
	}()

	pipelineErrors := make(chan error, 1)
	if a.pipeline != nil {
		go func() {
			a.logger.Info("usage ingestion started",
				"mode", a.cfg.Usage.Mode,
				"idle_interval", a.cfg.Usage.IdleInterval.String(),
				"batch_size", a.cfg.Usage.BatchSize,
				"retention_days", a.cfg.Usage.RetentionDays,
				"inbox_retention_days", a.cfg.Usage.InboxRetentionDays)
			pipelineErrors <- a.pipeline.Run(runCtx)
		}()
	}

	// The pricing loop is best-effort: losing it keeps prices stale but never
	// stops request capture or the HTTP server. A demo does not run it at all: its
	// prices are part of the fixture, and syncing them would make the process
	// reach openrouter.ai, which a public demonstration must not do.
	if !a.cfg.IsDemoMode {
		go func() {
			if err := a.pricing.Run(runCtx); err != nil {
				a.logger.Warn("pricing sync loop stopped", "error", err)
			}
		}()
	}

	// The quota scheduler actively refreshes each credential on a per-account
	// cadence. It is best-effort like pricing: losing it leaves quota readings
	// stale, but never breaks request capture or the HTTP server. It performs
	// outbound probes, so a demo never runs it.
	if !a.cfg.IsDemoMode && a.cfg.Quota.SchedulerEnabled {
		if scheduler := a.handler.QuotaScheduler(a.logger); scheduler != nil {
			go func() {
				a.logger.Info("quota refresh scheduler started",
					"watch_interval", a.cfg.Quota.WatchInterval.String(),
					"active_interval", a.cfg.Quota.ActiveInterval.String(),
					"normal_interval", a.cfg.Quota.NormalInterval.String(),
					"idle_interval", a.cfg.Quota.IdleInterval.String())
				if err := scheduler.Run(runCtx); err != nil {
					a.logger.Warn("quota refresh scheduler stopped", "error", err)
				}
			}()
		}
	}

	// Account auto-management is best-effort like the quota scheduler: losing it
	// parks the plans in the database for the next pass and never breaks request
	// capture or the HTTP server. It performs a CPA write, so a demo never starts it.
	if a.automation != nil {
		go func() {
			if err := a.automation.Run(runCtx); err != nil {
				a.logger.Warn("account auto-management stopped", "error", err)
			}
		}()
	}

	// The Ollama collector scrapes ollama.com. Best-effort and never built in a demo;
	// a failed pass leaves the previous reading and a persisted backoff behind.
	if a.ollama != nil {
		go func() {
			if err := a.ollama.Run(runCtx); err != nil {
				a.logger.Warn("ollama collection stopped", "error", err)
			}
		}()
	}

	// The Feishu bot opens one long connection and runs its own classification loop.
	// Start is a no-op for a disabled, forced-off or degraded configuration, so an
	// unusable Feishu setup never fails startup.
	if a.feishu != nil && !a.cfg.IsDemoMode {
		if err := a.feishu.Start(runCtx); err != nil {
			a.logger.Warn("feishu notifications did not start", "error", err)
		}
	}

	// The release sweep is best-effort in the same way and for the same reason: a
	// version that was not checked is stale information, not a broken deployment.
	// OMCPA_UPDATE_CHECK_ENABLED=false switches it off while leaving the page's own
	// check and the manual button working, because those are an operator asking a
	// question rather than the process deciding to reach the internet.
	// Never in a demo, and that is not implied by the service being nil-safe: the demo
	// builds a fixture-backed service so the page renders, and a sweep over that service
	// would still be a loop this process started on its own. The demonstration starts
	// exactly one loop, the HTTP server.
	if a.release != nil && a.cfg.Release.Enabled && !a.cfg.IsDemoMode {
		go func() {
			a.logger.Info("release check sweep started", "interval", a.cfg.Release.Interval.String())
			if err := a.release.Run(runCtx, a.cfg.Release.Interval); err != nil {
				a.logger.Warn("release check sweep stopped", "error", err)
			}
		}()
	}

	// Automatic quota refresh is best effort too: a round that cannot reach CPA leaves the
	// stored readings as they were. It reads providers through the gateway with each
	// credential, so a demo, whose gateway is the fixture, never starts it.
	// It is joined before Run returns, like the pipeline, because a round in flight writes
	// observations to the database the caller closes next.
	warmupDone := make(chan struct{})
	if a.cfg.IsDemoMode {
		close(warmupDone)
	} else {
		go func() {
			defer close(warmupDone)
			if err := a.handler.RunCredentialWarmup(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Warn("credential window warmup loop stopped", "error", err)
			}
		}()
	}
	defer func() { stop(); <-warmupDone }()
	quotaRefreshDone := make(chan struct{})
	if a.cfg.IsDemoMode {
		close(quotaRefreshDone)
	} else {
		go func() {
			defer close(quotaRefreshDone)
			if err := a.handler.RunQuotaAutoRefresh(runCtx); err != nil && !errors.Is(err, context.Canceled) {
				a.logger.Warn("automatic quota refresh loop stopped", "error", err)
			}
		}()
	}

	var runErr error
	hasPipelineReturned := a.pipeline == nil
	select {
	case err := <-pipelineErrors:
		hasPipelineReturned = true
		// Losing the capture loop means the dashboard stops gaining history;
		// treat it as fatal rather than serving silently stale numbers.
		if err != nil && !errors.Is(err, context.Canceled) {
			runErr = fmt.Errorf("usage pipeline stopped: %w", err)
		}
	case <-ctx.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			runErr = err
		}
	}

	// The caller closes the database once this returns, so the listener is shut
	// and the pipeline joined first: a request or a final ingest flush must not
	// find the store gone.
	stop()
	// The Feishu bot owns a long connection and its owns goroutines; join it before
	// the database closes so a final outbox write cannot find the store gone.
	if a.feishu != nil {
		a.feishu.Stop()
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a.handler.CloseBrowserRuns()
	a.handler.CloseUpdateWatchers()
	if err := a.httpServer.Shutdown(shutdownContext); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown HTTP server: %w", err)
	}
	select {
	case <-quotaRefreshDone:
	case <-shutdownContext.Done():
		a.logger.Warn("automatic quota refresh did not stop before the shutdown deadline")
	}
	if !hasPipelineReturned {
		select {
		case <-pipelineErrors:
		case <-shutdownContext.Done():
			a.logger.Warn("usage pipeline did not stop before the shutdown deadline")
		}
	}
	return runErr
}

func (a *App) Close() error {
	if a == nil {
		return nil
	}
	a.handler.CloseBrowserRuns()
	a.handler.CloseUpdateWatchers()
	// The fixture listens on a loopback socket, so it has to be closed with the
	// database it describes.
	if a.upstream != nil {
		_ = a.upstream.Close()
	}
	// The maintenance service holds a connection of its own; closing the main pool
	// first would leave it open on a database nothing else is using.
	if a.maintenance != nil {
		_ = a.maintenance.Close()
	}
	if a.db == nil {
		return nil
	}
	return a.db.Close()
}

// seedDemoResources fills the resource list consumed by the overview.
//
// It runs the ordinary discovery path against the in-process fixture instead of
// writing resource rows directly, so the demo's resources are produced by the
// same code, and the same adapter rules, a self-hosted deployment uses.
func seedDemoResources(ctx context.Context, cfg config.Config, repo *repository.Repository, cipher *crypto.Cipher, logger *slog.Logger) error {
	instance, err := repo.GetInstance(ctx, "default")
	if err != nil {
		return fmt.Errorf("load demo instance: %w", err)
	}
	client, err := management.NewClient(instance.BaseURL, cfg.CPA.ManagementKey, cfg.RequestTimeout, cfg.TLSSkipVerify)
	if err != nil {
		return fmt.Errorf("build demo management client: %w", err)
	}
	resources, discoveryErrors, err := discovery.NewDiscoverer(cipher).Discover(ctx, client, instance.ID)
	if err != nil {
		return fmt.Errorf("discover demo resources: %w", err)
	}
	if _, err := repo.UpsertDiscoveredResources(ctx, instance.ID, resources, time.Now().UTC(), true); err != nil {
		return fmt.Errorf("store demo resources: %w", err)
	}
	now := time.Now().UTC()
	_ = repo.UpdateInstanceStatus(ctx, instance.ID, "ok", strings.Join(discoveryErrors, "; "), &now)
	logger.Info("demo resources discovered", "count", len(resources), "errors", len(discoveryErrors))
	return nil
}

func bootstrapDefaultInstance(ctx context.Context, cfg config.Config, repo *repository.Repository, cipher *crypto.Cipher) error {
	if strings.TrimSpace(cfg.CPA.BaseURL) == "" || strings.TrimSpace(cfg.CPA.ManagementKey) == "" {
		// Allow the binary to start before CPA is configured. The discovery API
		// will report a clear configuration error until the instance is added.
		return nil
	}
	if _, err := management.NewClient(cfg.CPA.BaseURL, cfg.CPA.ManagementKey, cfg.RequestTimeout, cfg.TLSSkipVerify); err != nil {
		return fmt.Errorf("validate CPA configuration: %w", err)
	}
	ciphertext, nonce, err := cipher.Encrypt([]byte(cfg.CPA.ManagementKey))
	if err != nil {
		return fmt.Errorf("encrypt CPA management key: %w", err)
	}
	now := time.Now().UTC()
	instance := domain.CPAInstance{
		ID:                      "default",
		Name:                    "Default CPA",
		BaseURL:                 strings.TrimRight(cfg.CPA.BaseURL, "/"),
		UsageAddr:               cfg.CPA.UsageAddr,
		ManagementKeyCiphertext: ciphertext,
		ManagementKeyNonce:      nonce,
		Status:                  "unknown",
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if existing, err := repo.GetInstance(ctx, instance.ID); err == nil {
		instance.CreatedAt = existing.CreatedAt
		// An environment-provided key is intentionally refreshed at startup so
		// rotation does not require manually editing SQLite.
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load default CPA instance: %w", err)
	}
	return repo.UpsertInstance(ctx, instance)
}

func (l *cpaModelLister) ListConfiguredModels(ctx context.Context) ([]string, error) {
	models, err := l.ListConfiguredModelCatalog(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(models))
	for m := range models {
		result = append(result, m)
	}
	return result, nil
}
