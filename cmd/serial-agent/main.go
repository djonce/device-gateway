package main

import (
	"bufio"
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
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"

	"light-gateway/internal/device"
)

type config struct {
	GatewayURL     string
	Port           string
	Baud           int
	DeviceID       string
	DeviceName     string
	TokenFile      string
	ProvisionKey   string
	HeartbeatEvery time.Duration
}

type agent struct {
	config config
	client *http.Client
	logger *slog.Logger
	port   serial.Port
	token  string
	mu     sync.Mutex
	lines  []string
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	a := &agent{
		config: cfg,
		client: &http.Client{Timeout: 40 * time.Second},
		logger: logger,
	}
	if err := a.run(context.Background()); err != nil {
		logger.Error("serial agent stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	cfg := config{}
	flag.StringVar(&cfg.GatewayURL, "gateway", env("LIGHT_SERIAL_GATEWAY", "http://127.0.0.1:7001"), "Light Gateway base URL")
	flag.StringVar(&cfg.Port, "port", env("LIGHT_SERIAL_PORT", ""), "serial port path")
	flag.IntVar(&cfg.Baud, "baud", envInt("LIGHT_SERIAL_BAUD", 115200), "serial baud rate")
	flag.StringVar(&cfg.DeviceID, "id", env("LIGHT_SERIAL_DEVICE_ID", ""), "gateway device id")
	flag.StringVar(&cfg.DeviceName, "name", env("LIGHT_SERIAL_DEVICE_NAME", ""), "gateway device name")
	flag.StringVar(&cfg.TokenFile, "token-file", env("LIGHT_SERIAL_TOKEN_FILE", ""), "file used to persist the device token")
	flag.StringVar(&cfg.ProvisionKey, "provision-key", env("LIGHT_SERIAL_PROVISION_KEY", ""), "enrollment key sent on registration (X-Provision-Key)")
	heartbeatSeconds := flag.Int("heartbeat", envInt("LIGHT_SERIAL_HEARTBEAT_SECONDS", 10), "heartbeat interval in seconds")
	flag.Parse()

	if cfg.Port == "" {
		port, err := detectPort()
		if err != nil {
			return cfg, err
		}
		cfg.Port = port
	}
	cfg.GatewayURL = strings.TrimRight(cfg.GatewayURL, "/")
	portName := filepath.Base(cfg.Port)
	if cfg.DeviceID == "" {
		cfg.DeviceID = "devboard-" + sanitizeID(portName)
	}
	if cfg.DeviceName == "" {
		cfg.DeviceName = "Dev Board " + portName
	}
	if cfg.TokenFile == "" {
		cfg.TokenFile = filepath.Join("data", cfg.DeviceID+"-token")
	}
	cfg.HeartbeatEvery = time.Duration(*heartbeatSeconds) * time.Second
	return cfg, nil
}

func (a *agent) run(ctx context.Context) error {
	port, err := serial.Open(a.config.Port, &serial.Mode{BaudRate: a.config.Baud})
	if err != nil {
		return err
	}
	defer port.Close()
	a.port = port

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
	go a.readSerialLoop(ctx)
	return a.commandLoop(ctx)
}

func (a *agent) register(ctx context.Context) error {
	req := device.RegisterDeviceRequest{
		ID:           a.config.DeviceID,
		Name:         a.config.DeviceName,
		Type:         device.DeviceTypeESP,
		AgentVersion: "serial-agent/0.1.0",
		Labels: map[string]string{
			"bridge": "serial",
			"port":   a.config.Port,
			"baud":   strconv.Itoa(a.config.Baud),
			"host":   runtime.GOOS,
		},
		Capabilities: []device.Capability{
			{Name: "serial.line", Description: "serial lines observed from the board"},
			{Name: "serial.write", Description: "write text to the serial port"},
			{Name: "serial.recent", Description: "return recent serial lines buffered by the bridge"},
		},
		Metadata: map[string]any{
			"port": a.config.Port,
			"baud": a.config.Baud,
			"host": runtime.GOOS,
			"arch": runtime.GOARCH,
		},
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
		a.logger.Info("registered serial board and stored token", "device", a.config.DeviceID, "port", a.config.Port)
		return nil
	}
	a.logger.Info("registered existing serial board", "device", a.config.DeviceID, "port", a.config.Port)
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
		AgentVersion: "serial-agent/0.1.0",
		Metadata: map[string]any{
			"port":         a.config.Port,
			"baud":         a.config.Baud,
			"recentLines":  len(a.recentLines()),
			"bridgeHostOS": runtime.GOOS,
		},
	}
	var out device.Device
	return a.doJSON(ctx, http.MethodPost, "/api/v1/devices/"+url.PathEscape(a.config.DeviceID)+"/heartbeat", a.token, req, &out)
}

func (a *agent) readSerialLoop(ctx context.Context) {
	reader := bufio.NewReader(a.port)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, io.EOF) {
				continue
			}
			a.logger.Warn("serial read failed", "error", err)
			time.Sleep(time.Second)
			continue
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		a.pushLine(line)
		req := device.TelemetryRequest{
			Key:   "serial.line",
			Value: line,
			Metadata: map[string]any{
				"port": a.config.Port,
				"baud": a.config.Baud,
			},
		}
		var out device.TelemetryPoint
		if err := a.doJSON(ctx, http.MethodPost, "/api/v1/devices/"+url.PathEscape(a.config.DeviceID)+"/telemetry", a.token, req, &out); err != nil {
			a.logger.Warn("serial telemetry failed", "error", err)
		}
	}
}

func (a *agent) commandLoop(ctx context.Context) error {
	for {
		var response struct {
			Command *device.Command `json:"command"`
		}
		err := a.doJSON(ctx, http.MethodGet, "/api/v1/devices/"+url.PathEscape(a.config.DeviceID)+"/commands/next?timeout=30", a.token, nil, &response)
		if err != nil {
			a.logger.Warn("command poll failed", "error", err)
			sleepOrDone(ctx, 5*time.Second)
			continue
		}
		if response.Command == nil {
			continue
		}
		a.logger.Info("command received", "id", response.Command.ID, "type", response.Command.Type)
		ack := a.execute(*response.Command)
		if err := a.ack(ctx, response.Command.ID, ack); err != nil {
			a.logger.Warn("command ack failed", "id", response.Command.ID, "error", err)
		}
	}
}

func (a *agent) execute(cmd device.Command) device.AckCommandRequest {
	switch cmd.Type {
	case "serial.write":
		raw, _ := cmd.Payload["data"].(string)
		if raw == "" {
			return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "payload.data is required"}
		}
		newline := true
		if value, ok := cmd.Payload["newline"].(bool); ok {
			newline = value
		}
		if newline {
			raw += "\n"
		}
		written, err := a.port.Write([]byte(raw))
		if err != nil {
			return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: err.Error()}
		}
		return device.AckCommandRequest{
			Status: device.CommandStatusSucceeded,
			Result: map[string]any{"bytes": written},
		}
	case "serial.recent":
		return device.AckCommandRequest{
			Status: device.CommandStatusSucceeded,
			Result: map[string]any{"lines": a.recentLines()},
		}
	default:
		return device.AckCommandRequest{Status: device.CommandStatusFailed, Error: "unsupported serial command type"}
	}
}

func (a *agent) ack(ctx context.Context, commandID string, ack device.AckCommandRequest) error {
	var out device.Command
	path := "/api/v1/devices/" + url.PathEscape(a.config.DeviceID) + "/commands/" + url.PathEscape(commandID) + "/ack"
	return a.doJSON(ctx, http.MethodPost, path, a.token, ack, &out)
}

func (a *agent) doJSON(ctx context.Context, method, path, token string, in any, out any) error {
	var body io.Reader
	if in != nil {
		payload, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.config.GatewayURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Device-Token", token)
	}
	if a.config.ProvisionKey != "" {
		req.Header.Set("X-Provision-Key", a.config.ProvisionKey) // only enforced on /register
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

func (a *agent) pushLine(line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lines = append(a.lines, line)
	if len(a.lines) > 50 {
		a.lines = a.lines[len(a.lines)-50:]
	}
}

func (a *agent) recentLines() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, len(a.lines))
	copy(out, a.lines)
	return out
}

func detectPort() (string, error) {
	patterns := []string{
		"/dev/cu.wchusbserial*",
		"/dev/cu.usbserial*",
		"/dev/cu.usbmodem*",
		"/dev/ttyUSB*",
		"/dev/ttyACM*",
	}
	for _, pattern := range patterns {
		matches, _ := filepath.Glob(pattern)
		if len(matches) > 0 {
			return matches[0], nil
		}
	}
	ports, err := serial.GetPortsList()
	if err != nil {
		return "", err
	}
	for _, port := range ports {
		if strings.Contains(port, "usb") || strings.Contains(port, "wch") {
			return port, nil
		}
	}
	return "", errors.New("no USB serial development board port found")
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

func sleepOrDone(ctx context.Context, duration time.Duration) {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func sanitizeID(value string) string {
	value = strings.ToLower(value)
	var builder strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			continue
		}
		builder.WriteByte('-')
	}
	return strings.Trim(builder.String(), "-")
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
