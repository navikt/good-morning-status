package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := "8080"
	s := newServer(log)

	ctx := context.Background()
	if err := s.valkey.MigrateUserData(ctx); err != nil {
		log.Error("migration_failed", "error", err)
	} else {
		log.Info("migration_complete")
	}

	log.Info("starting server", "port", port)
	if err := http.ListenAndServe(":"+port, s.routes()); err != nil {
		panic(err)
	}
}
