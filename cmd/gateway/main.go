package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"light-gateway/internal/auth"
	"light-gateway/internal/device"
	"light-gateway/internal/realtime"
	"light-gateway/internal/weather"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	addr := env("LIGHT_GATEWAY_ADDR", ":7001")
	dataPath := env("LIGHT_GATEWAY_DATA", "data/light-gateway.db")

	store, err := device.NewStore(dataPath)
	if err != nil {
		logger.Error("failed to open store", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	// Periodically expire old telemetry rollup buckets per their retention.
	go func() {
		store.PruneRollups()
		for range time.Tick(time.Minute) {
			store.PruneRollups()
		}
	}()

	mux := http.NewServeMux()
	weatherSvc := weather.NewService(logger)
	voiceCfg := realtime.PipelineConfig{
		LLMURL:       os.Getenv("LIGHT_VOICE_LLM_URL"),
		LLMKey:       os.Getenv("LIGHT_VOICE_LLM_KEY"),
		LLMModel:     os.Getenv("LIGHT_VOICE_LLM_MODEL"),
		SystemPrompt: os.Getenv("LIGHT_VOICE_PROMPT"),
		ASRURL:       os.Getenv("LIGHT_VOICE_ASR_URL"),
		TTSURL:       os.Getenv("LIGHT_VOICE_TTS_URL"),
		AudioCodec:   os.Getenv("LIGHT_VOICE_CODEC"), // pcm16 (default) or opus
	}
	var voicePipeline realtime.Pipeline
	if voiceCfg.LLMURL != "" || voiceCfg.ASRURL != "" || voiceCfg.TTSURL != "" || voiceCfg.AudioCodec != "" {
		voicePipeline = realtime.NewHTTPPipeline(voiceCfg)
		logger.Info("voice pipeline configured", "llm", voiceCfg.LLMURL != "", "asr", voiceCfg.ASRURL != "", "tts", voiceCfg.TTSURL != "", "codec", voiceCfg.AudioCodec)
	}
	hub := realtime.NewHub(logger, voicePipeline) // nil -> echo placeholder
	hub.OnEvent(store.RecordEvent)                // mirror realtime connect/session events into the event stream

	authn := auth.New(os.Getenv("LIGHT_ADMIN_USER"), os.Getenv("LIGHT_ADMIN_PASSWORD"))
	if authn.Enabled() {
		logger.Info("admin auth enabled")
	} else {
		logger.Warn("admin auth disabled — set LIGHT_ADMIN_PASSWORD to require login for the management API")
	}

	api := device.NewAPI(store, logger, weatherSvc, hub, authn)
	provisionKey := os.Getenv("LIGHT_PROVISION_KEY")
	api.SetProvisionKey(provisionKey)
	if provisionKey != "" {
		logger.Info("device enrollment restricted — registration requires a provisioning key or admin session")
	} else {
		logger.Warn("open device registration — set LIGHT_PROVISION_KEY to require an enrollment key")
	}
	api.RegisterRoutes(mux)
	handler := withCORS(withRequestLog(logger, mux))

	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("light gateway listening", "addr", addr, "data", dataPath)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped unexpectedly", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("light gateway stopped")
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func withRequestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		logger.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start).String())
	})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
