package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// client is one connected device's real-time session.
type client struct {
	deviceID  string
	conn      *Conn
	send      chan []byte
	audio     []byte // accumulated audio.append chunks for the current utterance
	audioCodec string // codec of the inbound audio ("pcm16" default, or "opus")
}

// Hub tracks live real-time connections by device id, routes messages, and runs
// the voice pipeline for inbound utterances.
type Hub struct {
	mu       sync.RWMutex
	clients  map[string]*client
	logger   *slog.Logger
	pipeline Pipeline
	onEvent  func(deviceID, eventType, message string, meta map[string]any)
}

// NewHub creates a hub. A nil pipeline uses the echo placeholder.
func NewHub(logger *slog.Logger, pipeline Pipeline) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	if pipeline == nil {
		pipeline = echoPipeline{}
	}
	return &Hub{clients: map[string]*client{}, logger: logger, pipeline: pipeline}
}

// OnEvent registers a callback fired on connect/disconnect/session events so the
// gateway can mirror them into its event stream. Optional.
func (h *Hub) OnEvent(fn func(deviceID, eventType, message string, meta map[string]any)) {
	h.onEvent = fn
}

func (h *Hub) emit(deviceID, eventType, message string, meta map[string]any) {
	if h.onEvent != nil {
		h.onEvent(deviceID, eventType, message, meta)
	}
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	if old, ok := h.clients[c.deviceID]; ok {
		old.conn.Close() // kick the previous connection for this device
		close(old.send)
	}
	h.clients[c.deviceID] = c
	h.mu.Unlock()
	h.emit(c.deviceID, "voice.connected", "realtime channel connected", nil)
}

func (h *Hub) unregister(c *client) {
	h.mu.Lock()
	if cur, ok := h.clients[c.deviceID]; ok && cur == c {
		delete(h.clients, c.deviceID)
		close(c.send)
		h.mu.Unlock()
		h.emit(c.deviceID, "voice.disconnected", "realtime channel disconnected", nil)
		return
	}
	h.mu.Unlock()
}

// Connected reports whether a device currently has a live channel.
func (h *Hub) Connected(deviceID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.clients[deviceID]
	return ok
}

// Count returns the number of live connections.
func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Send enqueues a message to a device. Returns false if the device is not
// connected or its outbound buffer is full (slow client). Non-blocking.
func (h *Hub) Send(deviceID string, msg []byte) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.clients[deviceID]
	if !ok {
		return false
	}
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

// Serve runs the read loop and write pump for one connection until it closes.
// The caller (HTTP handler) must have already authenticated the device and
// upgraded the connection. Blocks until the connection ends.
func (h *Hub) Serve(conn *Conn, deviceID string) {
	c := &client{deviceID: deviceID, conn: conn, send: make(chan []byte, 32)}
	h.register(c)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range c.send {
			if err := conn.WriteText(msg); err != nil {
				return
			}
		}
	}()

	h.Send(deviceID, welcomeMessage(deviceID, h.pipeline.AudioCodec()))

	for {
		op, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if op != opText {
			continue
		}
		h.dispatch(c, data)
	}

	h.unregister(c)
	conn.Close()
	<-done
}

// dispatch routes one inbound message. Control/buffering messages are handled
// synchronously; pipeline work (text.input / audio.commit) runs in a goroutine
// so a slow ASR/LLM/TTS never blocks the read loop. dispatch runs on the single
// per-connection read goroutine, so c.audio needs no additional locking.
func (h *Hub) dispatch(c *client, data []byte) {
	var in Envelope
	if err := json.Unmarshal(data, &in); err != nil {
		h.Send(c.deviceID, errorMsg("invalid envelope"))
		return
	}
	switch in.Type {
	case "ping":
		h.Send(c.deviceID, encode(Envelope{Type: "pong"}))
	case "hello":
		h.Send(c.deviceID, welcomeMessage(c.deviceID, h.pipeline.AudioCodec()))
	case "audio.append":
		if codec, ok := in.Payload["codec"].(string); ok && codec != "" {
			c.audioCodec = codec
		}
		if s, ok := in.Payload["pcm"].(string); ok {
			if chunk, err := base64.StdEncoding.DecodeString(s); err == nil {
				c.audio = append(c.audio, chunk...)
			}
		}
		h.Send(c.deviceID, encode(Envelope{Type: "audio.ack", Payload: map[string]any{"bytes": len(c.audio)}}))
	case "audio.reset":
		c.audio = nil
	case "audio.commit":
		audio := c.audio
		codec := c.audioCodec
		c.audio = nil
		go h.runAudioPipeline(c.deviceID, audio, codec)
	case "text.input":
		text, _ := in.Payload["text"].(string)
		go h.runTextPipeline(c.deviceID, text)
	default:
		h.Send(c.deviceID, errorMsg("unknown type: "+in.Type))
	}
}

// runTextPipeline handles a typed utterance: echo it as asr.final, then reply.
func (h *Hub) runTextPipeline(deviceID, text string) {
	h.emit(deviceID, "voice.session", "text input received", map[string]any{"text": text})
	h.Send(deviceID, encode(Envelope{Type: "asr.final", Payload: map[string]any{"text": text}}))
	h.respondAndSpeak(deviceID, text)
}

// runAudioPipeline handles a committed audio utterance: ASR -> reply -> TTS.
func (h *Hub) runAudioPipeline(deviceID string, audio []byte, codec string) {
	h.emit(deviceID, "voice.session", "audio utterance committed", map[string]any{"bytes": len(audio), "codec": codec})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	text, err := h.pipeline.Transcribe(ctx, audio, codec)
	if err != nil {
		h.Send(deviceID, errorMsg("asr failed: "+err.Error()))
		return
	}
	h.Send(deviceID, encode(Envelope{Type: "asr.final", Payload: map[string]any{"text": text}}))
	h.respondAndSpeak(deviceID, text)
}

// respondAndSpeak runs the dialogue LLM, sends the reply text, then streams TTS
// audio back as a sequence of tts.audio chunks (final=true on the last).
func (h *Hub) respondAndSpeak(deviceID, userText string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	reply, err := h.pipeline.Respond(ctx, userText)
	if err != nil {
		h.Send(deviceID, errorMsg("llm failed: "+err.Error()))
		return
	}
	h.Send(deviceID, encode(Envelope{Type: "tts.say", Payload: map[string]any{"text": reply}}))

	codec := h.pipeline.AudioCodec()
	seq := 0
	err = h.pipeline.SynthesizeStream(ctx, reply, func(chunk []byte, final bool) error {
		ok := h.Send(deviceID, encode(Envelope{Type: "tts.audio", Payload: map[string]any{
			"pcm":   base64.StdEncoding.EncodeToString(chunk),
			"codec": codec,
			"seq":   seq,
			"final": final,
		}}))
		seq++
		if !ok {
			return errors.New("client disconnected")
		}
		return nil
	})
	if err != nil {
		h.logger.Warn("tts stream failed", "error", err, "deviceId", deviceID)
	}
}
