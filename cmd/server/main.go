package main

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

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

	addTx := usecase.NewAddTransaction(txRepo, catRepo)
	queryRep := usecase.NewQueryReport(txRepo, catRepo)

	renderer, err := web.NewRenderer()
	if err != nil {
		log.Error("init renderer", "err", err)
		os.Exit(1)
	}
	h := handler.New(renderer, addTx, queryRep, txRepo, catRepo, log)

	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)

	r.Get("/", h.Dashboard)
	r.Get("/transactions", h.ListTransactions)
	r.Get("/transactions/new", h.NewTransactionForm)
	r.Post("/transactions", h.CreateTransaction)
	r.Get("/partials/report", h.PartialReport)

	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(web.StaticFS()))))

	log.Info("listening", "addr", cfg.ServerAddr, "db", cfg.DatabasePath, "openai_key", cfg.MaskedAPIKey())
	if err := http.ListenAndServe(cfg.ServerAddr, r); err != nil {
		log.Error("server exited", "err", err)
		os.Exit(1)
	}
}
