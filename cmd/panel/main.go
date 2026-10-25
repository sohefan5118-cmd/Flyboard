package main

import (
	"context"
	"flyboard/internal/config"
	"flyboard/internal/httpapi"
	"flyboard/internal/store"
	"log"
	"log/slog"
	"net/http"
	"os"
)

func main() {
	c, e := config.Load()
	if e != nil {
		log.Fatal(e)
	}
	s, e := store.Open(context.Background(), c.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer s.DB.Close()
	log.Printf("panel listening on %s", c.Listen)
	log.Fatal(http.ListenAndServe(c.Listen, httpapi.New(c, s, slog.New(slog.NewJSONHandler(os.Stdout, nil)))))
}
