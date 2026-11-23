package main

import (
	"context"
	"crypto/rand"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ShieldzDev/go-ndi/models"
)

func main() {
	secret := []byte(os.Getenv("SECRET"))
	if len(secret) < 32 {
		var generated [32]byte
		if _, err := rand.Read(generated[:]); err != nil {
			slog.Error("generate secret", "error", err)
			os.Exit(1)
		}
		secret = generated[:]
		slog.Warn("SECRET not set; using an ephemeral key (tokens expire on restart)")
	}
	port := strings.TrimSpace(os.Getenv("PORT"))
	if port == "" {
		port = "8900"
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		slog.Error("PORT must be a number between 1 and 65535")
		os.Exit(1)
	}
	s := models.NewServer(secret)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.RunJanitor(ctx)
	addr := ":" + port
	log.Printf("listening on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, s.Router()); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
