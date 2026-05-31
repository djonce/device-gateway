package realtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

func TestComputeAcceptKey(t *testing.T) {
	// RFC 6455 canonical example.
	if got := computeAcceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept key mismatch: %q", got)
	}
}

func decodeEnvelope(t *testing.T, b []byte) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatalf("decode envelope: %v (%s)", err, b)
	}
	return e
}

// nextEnvelope reads one queued message for a client with a timeout.
func nextEnvelope(t *testing.T, c *client) Envelope {
	t.Helper()
	select {
	case b := <-c.send:
		return decodeEnvelope(t, b)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for message")
		return Envelope{}
	}
}

// newTestClient registers a client directly (no socket) for dispatch tests.
func newTestClient(h *Hub, id string) *client {
	c := &client{deviceID: id, send: make(chan []byte, 16)}
	h.mu.Lock()
	h.clients[id] = c
	h.mu.Unlock()
	return c
}

// fakePipeline returns canned values and emits canned TTS chunks.
type fakePipeline struct {
	reply      string
	transcript string
	chunks     [][]byte
	codec      string
}

func (f fakePipeline) Respond(context.Context, string) (string, error) { return f.reply, nil }
func (f fakePipeline) Transcribe(context.Context, []byte, string) (string, error) {
	return f.transcript, nil
}
func (f fakePipeline) SynthesizeStream(_ context.Context, _ string, emit func([]byte, bool) error) error {
	for i, c := range f.chunks {
		if err := emit(c, i == len(f.chunks)-1); err != nil {
			return err
		}
	}
	return nil
}
func (f fakePipeline) AudioCodec() string {
	if f.codec == "" {
		return "pcm16"
	}
	return f.codec
}

func TestDispatchPingAndUnknown(t *testing.T) {
	h := NewHub(nil, nil)
	c := newTestClient(h, "d1")

	h.dispatch(c, []byte(`{"type":"ping"}`))
	if env := nextEnvelope(t, c); env.Type != "pong" {
		t.Fatalf("expected pong, got %q", env.Type)
	}

	h.dispatch(c, []byte(`{"type":"frobnicate"}`))
	if env := nextEnvelope(t, c); env.Type != "error" {
		t.Fatalf("expected error, got %q", env.Type)
	}
}

func TestDispatchTextEchoDefault(t *testing.T) {
	h := NewHub(nil, nil) // echo pipeline
	c := newTestClient(h, "d1")

	h.dispatch(c, []byte(`{"type":"text.input","payload":{"text":"hi"}}`))
	asr := nextEnvelope(t, c)
	if asr.Type != "asr.final" || asr.Payload["text"] != "hi" {
		t.Fatalf("unexpected asr: %+v", asr)
	}
	tts := nextEnvelope(t, c)
	if tts.Type != "tts.say" || tts.Payload["text"] != "你说的是：hi" {
		t.Fatalf("unexpected tts: %+v", tts)
	}
}

func TestDispatchTextUsesPipeline(t *testing.T) {
	h := NewHub(nil, fakePipeline{reply: "REPLY"})
	c := newTestClient(h, "d1")

	h.dispatch(c, []byte(`{"type":"text.input","payload":{"text":"问题"}}`))
	if env := nextEnvelope(t, c); env.Type != "asr.final" || env.Payload["text"] != "问题" {
		t.Fatalf("unexpected asr: %+v", env)
	}
	if env := nextEnvelope(t, c); env.Type != "tts.say" || env.Payload["text"] != "REPLY" {
		t.Fatalf("expected pipeline reply, got %+v", env)
	}
}

func TestDispatchAudioCommitStreamsChunks(t *testing.T) {
	h := NewHub(nil, fakePipeline{
		transcript: "你好",
		reply:      "在的",
		chunks:     [][]byte{[]byte("AU"), []byte("DIO")},
		codec:      "opus",
	})
	c := newTestClient(h, "d1")

	chunk := base64.StdEncoding.EncodeToString([]byte("rawopus"))
	h.dispatch(c, []byte(`{"type":"audio.append","payload":{"codec":"opus","pcm":"`+chunk+`"}}`))
	if env := nextEnvelope(t, c); env.Type != "audio.ack" {
		t.Fatalf("expected audio.ack, got %q", env.Type)
	}
	if c.audioCodec != "opus" {
		t.Fatalf("expected session codec opus, got %q", c.audioCodec)
	}
	h.dispatch(c, []byte(`{"type":"audio.commit"}`))

	if env := nextEnvelope(t, c); env.Type != "asr.final" || env.Payload["text"] != "你好" {
		t.Fatalf("expected asr.final transcript, got %+v", env)
	}
	if env := nextEnvelope(t, c); env.Type != "tts.say" || env.Payload["text"] != "在的" {
		t.Fatalf("expected tts.say reply, got %+v", env)
	}

	// Two streamed tts.audio chunks: seq 0 (not final), seq 1 (final).
	first := nextEnvelope(t, c)
	if first.Type != "tts.audio" || first.Payload["codec"] != "opus" || first.Payload["final"] != false {
		t.Fatalf("unexpected first chunk: %+v", first)
	}
	if d, _ := base64.StdEncoding.DecodeString(first.Payload["pcm"].(string)); string(d) != "AU" {
		t.Fatalf("expected first chunk 'AU', got %q", d)
	}
	second := nextEnvelope(t, c)
	if second.Type != "tts.audio" || second.Payload["final"] != true {
		t.Fatalf("expected final chunk, got %+v", second)
	}
	if d, _ := base64.StdEncoding.DecodeString(second.Payload["pcm"].(string)); string(d) != "DIO" {
		t.Fatalf("expected second chunk 'DIO', got %q", d)
	}
}

func TestStreamChunks(t *testing.T) {
	type got struct {
		data  string
		final bool
	}
	collect := func(data string, size int) []got {
		var out []got
		_ = streamChunks(bytes.NewReader([]byte(data)), size, func(chunk []byte, final bool) error {
			out = append(out, got{string(chunk), final})
			return nil
		})
		return out
	}
	if g := collect("abcdef", 2); len(g) != 3 || g[0] != (got{"ab", false}) || g[2] != (got{"ef", true}) {
		t.Fatalf("unexpected chunking: %+v", g)
	}
	if g := collect("", 4); len(g) != 0 {
		t.Fatalf("expected no chunks for empty input, got %+v", g)
	}
	if g := collect("hi", 100); len(g) != 1 || g[0] != (got{"hi", true}) {
		t.Fatalf("expected single final chunk, got %+v", g)
	}
}

func TestWelcomeAdvertisesCodec(t *testing.T) {
	env := decodeEnvelope(t, welcomeMessage("d1", "opus"))
	if env.Type != "welcome" || env.Payload["codec"] != "opus" || env.Payload["streaming"] != true {
		t.Fatalf("unexpected welcome: %+v", env)
	}
}

func TestHubSendRoutingAndStatus(t *testing.T) {
	h := NewHub(nil, nil)
	c := &client{deviceID: "d1", send: make(chan []byte, 1)}
	h.clients["d1"] = c

	if !h.Connected("d1") || h.Connected("d2") {
		t.Fatalf("connected status wrong")
	}
	if h.Count() != 1 {
		t.Fatalf("expected count 1, got %d", h.Count())
	}
	if !h.Send("d1", []byte("a")) {
		t.Fatalf("expected send to succeed")
	}
	if h.Send("d2", []byte("a")) {
		t.Fatalf("expected send to absent device to fail")
	}
	if h.Send("d1", []byte("b")) {
		t.Fatalf("expected send to full buffer to be dropped")
	}
	if got := <-c.send; string(got) != "a" {
		t.Fatalf("expected 'a', got %q", got)
	}
}

func TestSayText(t *testing.T) {
	h := NewHub(nil, nil)
	c := &client{deviceID: "d1", send: make(chan []byte, 1)}
	h.clients["d1"] = c
	if !h.SayText("d1", "晚安") {
		t.Fatalf("expected SayText to enqueue")
	}
	env := decodeEnvelope(t, <-c.send)
	if env.Type != "tts.say" || env.Payload["text"] != "晚安" || env.Payload["source"] != "console" {
		t.Fatalf("unexpected say message: %+v", env)
	}
}

func TestEchoPipelineDirect(t *testing.T) {
	echo := echoPipeline{}
	reply, _ := echo.Respond(context.Background(), "x")
	if reply != "你说的是：x" {
		t.Fatalf("unexpected echo reply: %q", reply)
	}
	if echo.AudioCodec() != "pcm16" {
		t.Fatalf("expected echo codec pcm16, got %q", echo.AudioCodec())
	}
	chunks := 0
	if err := echo.SynthesizeStream(context.Background(), "x", func([]byte, bool) error { chunks++; return nil }); err != nil {
		t.Fatalf("echo synthesize stream: %v", err)
	}
	if chunks != 0 {
		t.Fatalf("echo synthesize should emit no chunks, got %d", chunks)
	}
}
