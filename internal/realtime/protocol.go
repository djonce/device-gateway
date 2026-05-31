package realtime

import (
	"encoding/json"
	"time"
)

// ProtocolVersion identifies the (placeholder) voice wire protocol. Bump when
// the envelope or message types change.
const ProtocolVersion = "lightgw.voice.v0"

// Envelope is the JSON message envelope exchanged over the real-time channel.
// Both directions share this shape:
//
//	device -> gateway: hello | ping | text.input | audio.append | audio.commit
//	gateway -> device: welcome | pong | asr.partial | asr.final | tts.say | audio.ack | error
type Envelope struct {
	Type    string         `json:"type"`
	Seq     int            `json:"seq,omitempty"`
	TS      int64          `json:"ts,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

func nowMs() int64 { return time.Now().UnixMilli() }

func encode(e Envelope) []byte {
	if e.TS == 0 {
		e.TS = nowMs()
	}
	b, _ := json.Marshal(e)
	return b
}

func welcomeMessage(deviceID, codec string) []byte {
	return encode(Envelope{Type: "welcome", Payload: map[string]any{
		"deviceId":     deviceID,
		"protocol":     ProtocolVersion,
		"codec":        codec, // advertised audio codec: "pcm16" or "opus"
		"streaming":    true,  // tts.audio is delivered as chunks (seq, final)
		"capabilities": []string{"text.input", "audio.append", "audio.commit"},
	}})
}

func errorMsg(message string) []byte {
	return encode(Envelope{Type: "error", Payload: map[string]any{"message": message}})
}

// SayText pushes a tts.say message to a connected device (used by the console to
// make the device speak). Returns false if the device is not connected.
func (h *Hub) SayText(deviceID, text string) bool {
	return h.Send(deviceID, encode(Envelope{Type: "tts.say", Payload: map[string]any{"text": text, "source": "console"}}))
}
