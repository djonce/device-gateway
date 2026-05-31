// Package realtime provides the gateway's bidirectional real-time channel,
// used by voice-category devices (e.g. 小智) whose audio/session traffic the
// HTTP long-poll command model cannot serve.
//
// It implements a deliberately minimal WebSocket (RFC 6455) on top of the Go
// standard library only — no external dependency — to stay consistent with the
// rest of the gateway. It supports the subset needed here: a single text/binary
// message stream with fragmentation, plus ping/pong/close control frames.
package realtime

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA

	maxMessageBytes = 1 << 20 // 1 MiB guard
)

// Conn is a minimal server-side WebSocket connection.
type Conn struct {
	conn net.Conn
	r    *bufio.Reader
	wmu  sync.Mutex // serializes all writes (data frames + control frames)
}

// computeAcceptKey derives the Sec-WebSocket-Accept value from the client key.
func computeAcceptKey(key string) string {
	h := sha1.New()
	h.Write([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// IsWebSocketUpgrade reports whether the request is a WebSocket handshake.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// Upgrade hijacks an HTTP connection and completes the WebSocket handshake.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !IsWebSocketUpgrade(r) {
		return nil, errors.New("not a websocket upgrade request")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("response writer does not support hijacking")
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + computeAcceptKey(key) + "\r\n\r\n"
	if _, err := brw.WriteString(resp); err != nil {
		conn.Close()
		return nil, err
	}
	if err := brw.Flush(); err != nil {
		conn.Close()
		return nil, err
	}
	return &Conn{conn: conn, r: brw.Reader}, nil
}

// readFrame reads one WebSocket frame and returns (fin, opcode, payload).
func (c *Conn) readFrame() (bool, int, []byte, error) {
	var header [2]byte
	if _, err := io.ReadFull(c.r, header[:]); err != nil {
		return false, 0, nil, err
	}
	fin := header[0]&0x80 != 0
	opcode := int(header[0] & 0x0F)
	masked := header[1]&0x80 != 0
	length := uint64(header[1] & 0x7F)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.r, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > maxMessageBytes {
		return false, 0, nil, fmt.Errorf("frame too large: %d", length)
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(c.r, maskKey[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(c.r, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}
	return fin, opcode, payload, nil
}

// ReadMessage returns the next data message (text/binary), transparently
// handling fragmentation and answering ping/close control frames. It returns
// io.EOF when the peer closes the connection.
func (c *Conn) ReadMessage() (int, []byte, error) {
	var buf []byte
	msgOp := -1
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			if err := c.writeFrame(opPong, payload); err != nil {
				return 0, nil, err
			}
			continue
		case opPong:
			continue
		case opClose:
			_ = c.writeFrame(opClose, nil)
			return 0, nil, io.EOF
		case opText, opBinary:
			msgOp = op
			buf = append(buf, payload...)
		case opContinuation:
			buf = append(buf, payload...)
		default:
			return 0, nil, fmt.Errorf("unsupported opcode %d", op)
		}
		if len(buf) > maxMessageBytes {
			return 0, nil, errors.New("message too large")
		}
		if fin {
			if msgOp == -1 {
				continue
			}
			return msgOp, buf, nil
		}
	}
}

// writeFrame writes a single unmasked server frame.
func (c *Conn) writeFrame(opcode int, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()

	header := make([]byte, 0, 10)
	header = append(header, byte(0x80|opcode)) // FIN + opcode
	n := len(payload)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n < 1<<16:
		header = append(header, 126)
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		header = append(header, ext[:]...)
	default:
		header = append(header, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		header = append(header, ext[:]...)
	}
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.conn.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// WriteText sends a text message.
func (c *Conn) WriteText(data []byte) error { return c.writeFrame(opText, data) }

// Close sends a close frame and closes the underlying connection.
func (c *Conn) Close() error {
	_ = c.writeFrame(opClose, nil)
	return c.conn.Close()
}
