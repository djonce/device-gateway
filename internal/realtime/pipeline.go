package realtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Pipeline turns user input into an assistant reply: ASR (audio->text),
// dialogue (text->reply via LLM), and streaming TTS (reply->audio chunks).
//
// Audio is codec-tagged ("pcm16" default, or "opus"); the gateway treats audio
// bytes as opaque and passes the codec through to ASR/TTS, so Opus flows
// end-to-end without the gateway transcoding. TTS is delivered as a stream of
// chunks so the device can start playing before synthesis finishes.
type Pipeline interface {
	// Respond produces an assistant reply for a user utterance (dialogue LLM).
	Respond(ctx context.Context, text string) (string, error)
	// Transcribe converts audio bytes (in the given codec) to text (ASR).
	Transcribe(ctx context.Context, audio []byte, codec string) (string, error)
	// SynthesizeStream synthesizes speech for text, delivering it as one or more
	// audio chunks via emit (final=true on the last chunk). Implementations that
	// cannot stream may emit a single final chunk; when TTS is unconfigured it
	// emits nothing.
	SynthesizeStream(ctx context.Context, text string, emit func(chunk []byte, final bool) error) error
	// AudioCodec is the codec the gateway advertises/produces ("pcm16"|"opus").
	AudioCodec() string
}

// echoPipeline is the default placeholder used when nothing is configured.
type echoPipeline struct{}

func (echoPipeline) Respond(_ context.Context, text string) (string, error) {
	return "你说的是：" + text, nil
}
func (echoPipeline) Transcribe(context.Context, []byte, string) (string, error) {
	return "(占位：音频转写未接入)", nil
}
func (echoPipeline) SynthesizeStream(context.Context, string, func([]byte, bool) error) error {
	return nil // no audio in placeholder mode
}
func (echoPipeline) AudioCodec() string { return "pcm16" }

// PipelineConfig configures the HTTP pipeline. Empty stage URLs disable that
// stage (so you can wire the LLM first and add ASR/TTS later).
type PipelineConfig struct {
	LLMURL       string // OpenAI-compatible chat completions endpoint
	LLMKey       string
	LLMModel     string
	SystemPrompt string
	ASRURL       string // POST raw audio bytes (X-Audio-Codec header) -> {"text": "..."}
	TTSURL       string // POST {"text":"...","codec":"..."} -> audio bytes (streamed)
	AudioCodec   string // "pcm16" (default) or "opus"
	TTSChunkBy   int    // outbound TTS chunk size in bytes (default 16 KiB)
}

type httpPipeline struct {
	cfg    PipelineConfig
	client *http.Client
}

// NewHTTPPipeline builds an HTTP-backed pipeline. Stages with empty URLs fall
// back to placeholder behavior.
func NewHTTPPipeline(cfg PipelineConfig) Pipeline {
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "你是小智，一个简洁、友好的中文语音助手。回答尽量简短、口语化。"
	}
	if cfg.LLMModel == "" {
		cfg.LLMModel = "gpt-4o-mini"
	}
	if cfg.AudioCodec == "" {
		cfg.AudioCodec = "pcm16"
	}
	if cfg.TTSChunkBy <= 0 {
		cfg.TTSChunkBy = 16 * 1024
	}
	return &httpPipeline{cfg: cfg, client: &http.Client{Timeout: 60 * time.Second}}
}

func (p *httpPipeline) AudioCodec() string { return p.cfg.AudioCodec }

func (p *httpPipeline) Respond(ctx context.Context, text string) (string, error) {
	if p.cfg.LLMURL == "" {
		return echoPipeline{}.Respond(ctx, text)
	}
	body, _ := json.Marshal(map[string]any{
		"model": p.cfg.LLMModel,
		"messages": []map[string]string{
			{"role": "system", "content": p.cfg.SystemPrompt},
			{"role": "user", "content": text},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.LLMURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.cfg.LLMKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.cfg.LLMKey)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("llm status %d: %s", resp.StatusCode, snippet)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return out.Choices[0].Message.Content, nil
}

func (p *httpPipeline) Transcribe(ctx context.Context, audio []byte, codec string) (string, error) {
	if p.cfg.ASRURL == "" {
		return echoPipeline{}.Transcribe(ctx, audio, codec)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.ASRURL, bytes.NewReader(audio))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if codec != "" {
		req.Header.Set("X-Audio-Codec", codec)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("asr status %d", resp.StatusCode)
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.Text, nil
}

func (p *httpPipeline) SynthesizeStream(ctx context.Context, text string, emit func(chunk []byte, final bool) error) error {
	if p.cfg.TTSURL == "" {
		return nil // no TTS configured
	}
	body, _ := json.Marshal(map[string]string{"text": text, "codec": p.cfg.AudioCodec})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TTSURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("tts status %d", resp.StatusCode)
	}
	// Read the response body as it arrives and forward fixed-size chunks. A one
	// chunk look-ahead lets us mark the final chunk correctly.
	return streamChunks(resp.Body, p.cfg.TTSChunkBy, emit)
}

// streamChunks reads r in size-byte reads and calls emit per chunk, with
// final=true on the last. Emits nothing for empty input.
func streamChunks(r io.Reader, size int, emit func(chunk []byte, final bool) error) error {
	if size <= 0 {
		size = 16 * 1024
	}
	buf := make([]byte, size)
	var pending []byte
	havePending := false
	flush := func(final bool) error {
		if !havePending {
			return nil
		}
		havePending = false
		return emit(pending, final)
	}
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if e := flush(false); e != nil {
				return e
			}
			pending = append([]byte(nil), buf[:n]...)
			havePending = true
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return flush(true)
}
