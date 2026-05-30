package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"light-gateway/internal/device"
)

type config struct {
	GatewayURL      string
	DeviceID        string
	DeviceName      string
	TokenFile       string
	HeartbeatEvery  time.Duration
	CommandTimeout  time.Duration
	AllowedCommands map[string]bool
}

type agent struct {
	config config
	client *http.Client
	logger *slog.Logger
	token  string
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	agent := &agent{
		config: cfg,
		client: &http.Client{Timeout: 40 * time.Second},
		logger: logger,
	}
	if err := agent.run(context.Background()); err != nil {
		logger.Error("agent stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	hostname, _ := os.Hostname()
	defaultID := env("LIGHT_AGENT_ID", hostname)
	cfg := config{}
	flag.StringVar(&cfg.GatewayURL, "gateway", env("LIGHT_AGENT_GATEWAY", "http://127.0.0.1:7001"), "Light Gateway base URL")
	flag.StringVar(&cfg.DeviceID, "id", defaultID, "device id")
	flag.StringVar(&cfg.DeviceName, "name", env("LIGHT_AGENT_NAME", hostname), "device display name")
	flag.StringVar(&cfg.TokenFile, "token-file", env("LIGHT_AGENT_TOKEN_FILE", "data/agent-token"), "file used to persist the device token")
	heartbeatSeconds := flag.Int("heartbeat", envInt("LIGHT_AGENT_HEARTBEAT_SECONDS", 15), "heartbeat interval in seconds")
	timeoutSeconds := flag.Int("command-timeout", envInt("LIGHT_AGENT_COMMAND_TIMEOUT_SECONDS", 10), "command execution timeout in seconds")
	allowed := flag.String("allowed-commands", env("LIGHT_AGENT_ALLOWED_COMMANDS", "uptime,df,free,uname,whoami,pwd,date"), "comma separated shell command allowlist")
	flag.Parse()
	cfg.GatewayURL = strings.TrimRight(cfg.GatewayURL, "/")
	cfg.HeartbeatEvery = time.Duration(*heartbeatSeconds) * time.Second
	cfg.CommandTimeout = time.Duration(*timeoutSeconds) * time.Second
	cfg.AllowedCommands = parseAllowlist(*allowed)
	return cfg
}

func (a *agent) run(ctx context.Context) error {
	token, err := readToken(a.config.TokenFile)
	if err != nil {
		return err
	}
	a.token = token
	if err := a.register(ctx); err != nil {
		return err
	}
	if a.token == "" {
		return errors.New("device token is missing; reset this device token from the console and write it to the token file")
	}

	go a.heartbeatLoop(ctx)
	return a.commandLoop(ctx)
}

func (a *agent) register(ctx context.Context) error {
	req := device.RegisterDeviceRequest{
		ID:           a.config.DeviceID,
		Name:         a.config.DeviceName,
		Type:         device.DeviceTypeLinuxNode,
		AgentVersion: "linux-agent/0.1.0",
		Labels: map[string]string{
			"runtime": runtime.GOOS,
			"arch":    runtime.GOARCH,
		},
		Capabilities: []device.Capability{
			{Name: "system.info", Description: "collect host runtime information"},
			{Name: "shell.exec", Description: "execute allowlisted shell commands"},
			{Name: "log.collect", Description: "collect bounded log file tail"},
		},
		Metadata: systemMetadata(),
	}
	var registration device.DeviceRegistration
	if err := a.doJSON(ctx, http.MethodPost, "/api/v1/devices/register", "", req, &registration); err != nil {
		return err
	}
	if registration.Token != "" {
		a.token = registration.Token
		if err := writeToken(a.config.TokenFile, a.token); err != nil {
			return err
		}
		a.logger.Info("registered and stored new device token", "device", a.config.DeviceID)
		return nil
	}
	a.logger.Info("registered existing device", "device", a.config.DeviceID)
	return nil
}

func (a *agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.HeartbeatEvery)
	defer ticker.Stop()
	for {
		if err := a.heartbeat(ctx); err != nil {
			a.logger.Warn("heartbeat failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *agent) heartbeat(ctx context.Context) error {
	req := device.HeartbeatRequest{
		AgentVersion: "linux-agent/0.1.0",
		Metadata:     systemMetadata(),
	}
	var out device.Device
	return a.doJSON(ctx, http.MethodPost, "/api/v1/devices/"+urlPath(a.config.DeviceID)+"/heartbeat", a.token, req, &out)
}

func (a *agent) commandLoop(ctx context.Context) error {
	for {
		var response struct {
			Command *device.Command `json:"command"`
		}
		err := a.doJSON(ctx, http.MethodGet, "/api/v1/devices/"+urlPath(a.config.DeviceID)+"/commands/next?timeout=30", a.token, nil, &response)
		if err != nil {
			a.logger.Warn("command poll failed", "error", err)
			sleepOrDone(ctx, 5*time.Second)
			continue
		}
		if response.Command == nil {
			continue
		}
		a.logger.Info("command received", "id", response.Command.ID, "type", response.Command.Type)
		ack := a.execute(ctx, *response.Command)
		if err := a.ack(ctx, response.Command.ID, ack); err != nil {
			a.logger.Warn("command ack failed", "id", response.Command.ID, "error", err)
		}
	}
}

func (a *agent) execute(ctx context.Context, cmd device.Command) device.AckCommandRequest {
	switch cmd.Type {
	case "system.info":
		return device.AckCommandRequest{Status: device.CommandStatusSucceeded, Result: systemMetadata()}
	case "shell.exec":
		return a.executeShell(ctx, cmd.Payload)
	case "log.collect":
		return a.collectLog(cmd.Payload)
	default:
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "unsupported command type"}
	}
}

func (a *agent) executeShell(ctx context.Context, payload map[string]any) device.AckCommandRequest {
	command, _ := payload["command"].(string)
	command = strings.TrimSpace(command)
	if command == "" {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "payload.command is required"}
	}
	name := commandName(command)
	if !a.config.AllowedCommands[name] {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "command is not allowlisted"}
	}
	execCtx, cancel := context.WithTimeout(ctx, a.config.CommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(execCtx, "sh", "-c", command).CombinedOutput()
	result := map[string]any{
		"command": command,
		"output":  truncate(string(out), 8000),
	}
	if execCtx.Err() == context.DeadlineExceeded {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Result: result, Error: "command timed out"}
	}
	if err != nil {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Result: result, Error: err.Error()}
	}
	return device.AckCommandRequest{Status: device.CommandStatusSucceeded, Result: result}
}

func (a *agent) collectLog(payload map[string]any) device.AckCommandRequest {
	path, _ := payload["path"].(string)
	path = filepath.Clean(path)
	if !strings.HasPrefix(path, "/var/log/") && !strings.HasPrefix(path, "/tmp/") {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "path must be under /var/log or /tmp"}
	}
	limit := 4096
	if rawLimit, ok := payload["limit"].(float64); ok && rawLimit > 0 && rawLimit < 65536 {
		limit = int(rawLimit)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: err.Error()}
	}
	if len(bytes) > limit {
		bytes = bytes[len(bytes)-limit:]
	}
	return device.AckCommandRequest{
		Status: device.CommandStatusSucceeded,
		Result: map[string]any{"path": path, "tail": string(bytes)},
	}
}

func (a *agent) ack(ctx context.Context, commandID string, ack device.AckCommandRequest) error {
	var out device.Command
	path := "/api/v1/devices/" + urlPath(a.config.DeviceID) + "/commands/" + urlPath(commandID) + "/ack"
	return a.doJSON(ctx, http.MethodPost, path, a.token, ack, &out)
}

func (a *agent) doJSON(ctx context.Context, method, path, token string, in any, out any) error {
	var body io.Reader
	if in != nil {
		payloadBytes, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payloadBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.config.GatewayURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Device-Token", token)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s failed: %s", method, path, strings.TrimSpace(string(respBytes)))
	}
	if out == nil || len(respBytes) == 0 {
		return nil
	}
	return json.Unmarshal(respBytes, out)
}

func readToken(path string) (string, error) {
	bytes, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

func writeToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(token+"\n"), 0o600)
}

func systemMetadata() map[string]any {
	hostname, _ := os.Hostname()
	return map[string]any{
		"hostname": hostname,
		"os":       runtime.GOOS,
		"arch":     runtime.GOARCH,
		"cpus":     runtime.NumCPU(),
		"pid":      os.Getpid(),
	}
}

func parseAllowlist(value string) map[string]bool {
	out := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			out[item] = true
		}
	}
	return out
}

func commandName(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "\n...truncated..."
}

func sleepOrDone(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func urlPath(value string) string {
	return url.PathEscape(value)
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
