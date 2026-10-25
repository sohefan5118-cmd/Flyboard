package main

import (
	"context"
	"flyboard/internal/config"
	"flyboard/internal/pull"
	"flyboard/internal/safefetch"
	"flyboard/internal/secretbox"
	"flyboard/internal/store"
	"log"
	"time"
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
	b, e := secretbox.Load(c.MasterKeyFile)
	if e != nil {
		log.Fatal(e)
	}
	svc := pull.Service{DB: s.DB, Box: b, Fetch: safefetch.Fetcher{Timeout: 45 * time.Second, MaxBytes: 4 << 20}}
	log.Print("worker started")
	for {
		worked, e := svc.RunOne(context.Background())
		if e != nil {
			log.Printf("job: %v", e)
		}
		if !worked {
			time.Sleep(time.Second)
		}
	}
}
