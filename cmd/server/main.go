package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"family-finances/internal/adapter/llm"
	"family-finances/internal/adapter/web"
	"family-finances/internal/adapter/web/handler"
	"family-finances/internal/infrastructure/config"
	"family-finances/internal/infrastructure/sqlite"
	"family-finances/internal/usecase"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := config.Load()
	if err != nil {
		log.Error("load config", "err", err)
		os.Exit(1)
	}

	db, err := sqlite.Open(cfg.DatabasePath)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := sqlite.Migrate(db); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	txRepo := sqlite.NewTransactionRepo(db)
	catRepo := sqlite.NewCategoryRepo(db)

	llmClient := llm.NewClient(llm.Config{
		APIKey:  cfg.OpenAIAPIKey,
		BaseURL: cfg.OpenAIBaseURL,
		Model:   cfg.OpenAIModel,
	})

	classifyPending := usecase.NewClassifyPending(txRepo, catRepo, llmClient, log)
	importBill := usecase.NewImportBill(txRepo, catRepo).WithTrigger(classifyPending.Trigger)
	queryRep := usecase.NewQueryReport(txRepo, catRepo)
	queryStats := usecase.NewQueryStats(txRepo, catRepo)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	go classifyPending.Run(ctx, 30*time.Second, 200)

	renderer, err := web.NewRenderer()
	if err != nil {
		log.Error("init renderer", "err", err)
		os.Exit(1)
	}
	h := handler.NewWithAuthKey(renderer, importBill, queryRep, queryStats, txRepo, catRepo, catRepo, log, cfg.AuthKey)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(middleware.Compress(5))

	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})

	r.Get("/auth/login", h.LoginForm)
	r.Post("/auth/login", h.LoginSubmit)
	r.Get("/auth/logout", h.Logout)

	staticHandler := http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS())))
	r.Handle("/static/*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// 静态资源嵌在二进制里，只随部署变化；给浏览器 1 小时缓存
		w.Header().Set("Cache-Control", "public, max-age=3600")
		staticHandler.ServeHTTP(w, req)
	}))

	r.Group(func(r chi.Router) {
		r.Use(h.RequireAuth)
		r.Get("/", h.Stats)
		r.Get("/stats", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/", http.StatusMovedPermanently)
		})
		r.Get("/cashflow", h.Dashboard)
		r.Get("/rules", h.Rules)
		r.Post("/rules", h.CreateRule)
		r.Post("/rules/{id}", h.UpdateRule)
		r.Post("/rules/{id}/active", h.ToggleRule)
		r.Post("/rules/{id}/delete", h.DeleteRule)
		r.Get("/api/stats", h.StatsAPI)
		r.Get("/api/stats/top", h.StatsTopAPI)
		r.Get("/transactions", h.ListTransactions)
		r.Get("/transactions/new", func(w http.ResponseWriter, req *http.Request) {
			http.Redirect(w, req, "/imports", http.StatusMovedPermanently)
		})
		r.Get("/api/transactions", h.ListTransactionsAPI)
		r.Patch("/api/transactions/{id}", h.UpdateTransaction)
		r.Get("/imports", h.ImportForm)
		r.Post("/imports", h.ImportSubmit)
		r.Post("/imports/manual", h.ManualEntrySubmit)
		r.Get("/partials/report", h.PartialReport)
	})

	log.Info("listening", "addr", cfg.ServerAddr, "db", cfg.DatabasePath, "openai_key", cfg.MaskedAPIKey())
	server := &http.Server{
		Addr:    cfg.ServerAddr,
		Handler: r,
		// 防 slowloris：慢速/挂起连接不能无限占用
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute, // 账单上传体积可能较大
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
