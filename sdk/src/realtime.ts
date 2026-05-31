import type { VoiceEnvelope } from './types';

/** Minimal WebSocket surface so any implementation (browser, ws, RN) fits. */
export interface WebSocketLike {
  send(data: string): void;
  close(): void;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: unknown) => void) | null;
  onerror: ((ev: unknown) => void) | null;
}

export type WebSocketFactory = (url: string) => WebSocketLike;

export interface VoiceHandlers {
  onOpen?(): void;
  onWelcome?(payload: Record<string, unknown>): void;
  onAsrFinal?(text: string): void;
  onTtsSay?(text: string): void;
  /** A streamed TTS audio chunk. info.final marks the last chunk of an utterance. */
  onTtsAudio?(pcmBase64: string, info: { final: boolean; codec?: string; seq?: number }): void;
  onError?(message: string): void;
  onClose?(): void;
  /** Catch-all for any envelope (called after specific handlers). */
  onMessage?(env: VoiceEnvelope): void;
}

export interface VoiceSessionOptions {
  baseUrl: string;
  deviceId: string;
  deviceToken: string;
  /** Provide a WebSocket factory for non-browser runtimes (e.g. the `ws` package). */
  webSocketFactory?: WebSocketFactory;
}

/**
 * VoiceSession is the device-side (or test) client for the realtime voice
 * channel. It connects over WebSocket and speaks the lightgw.voice.v0 protocol.
 */
export class VoiceSession {
  private opts: VoiceSessionOptions;
  private ws: WebSocketLike | undefined;
  private handlers: VoiceHandlers = {};

  constructor(opts: VoiceSessionOptions) {
    this.opts = opts;
  }

  get url(): string {
    const wsBase = this.opts.baseUrl.replace(/^http/i, 'ws').replace(/\/+$/, '');
    return `${wsBase}/api/v1/devices/${encodeURIComponent(this.opts.deviceId)}/ws?token=${encodeURIComponent(
      this.opts.deviceToken,
    )}`;
  }

  connect(handlers: VoiceHandlers = {}): void {
    this.handlers = handlers;
    const factory =
      this.opts.webSocketFactory ??
      ((url: string) => {
        const G = globalThis as { WebSocket?: new (u: string) => unknown };
        if (!G.WebSocket) throw new Error('no global WebSocket; pass webSocketFactory');
        return new G.WebSocket(url) as unknown as WebSocketLike;
      });
    const ws = factory(this.url);
    this.ws = ws;
    ws.onopen = () => this.handlers.onOpen?.();
    ws.onclose = () => this.handlers.onClose?.();
    ws.onerror = () => this.handlers.onError?.('websocket error');
    ws.onmessage = (ev) => this.dispatch(typeof ev.data === 'string' ? ev.data : String(ev.data));
  }

  private dispatch(data: string): void {
    let env: VoiceEnvelope;
    try {
      env = JSON.parse(data) as VoiceEnvelope;
    } catch {
      return;
    }
    const p = env.payload ?? {};
    switch (env.type) {
      case 'welcome':
        this.handlers.onWelcome?.(p);
        break;
      case 'asr.final':
        this.handlers.onAsrFinal?.(String(p.text ?? ''));
        break;
      case 'tts.say':
        this.handlers.onTtsSay?.(String(p.text ?? ''));
        break;
      case 'tts.audio':
        this.handlers.onTtsAudio?.(String(p.pcm ?? ''), {
          final: Boolean(p.final),
          codec: typeof p.codec === 'string' ? p.codec : undefined,
          seq: typeof p.seq === 'number' ? p.seq : undefined,
        });
        break;
      case 'error':
        this.handlers.onError?.(String(p.message ?? 'error'));
        break;
    }
    this.handlers.onMessage?.(env);
  }

  private send(env: VoiceEnvelope): void {
    if (!this.ws) throw new Error('not connected');
    this.ws.send(JSON.stringify(env));
  }

  hello() {
    this.send({ type: 'hello' });
  }
  ping() {
    this.send({ type: 'ping' });
  }
  /** Send a typed utterance (test the dialogue pipeline without audio). */
  sendText(text: string) {
    this.send({ type: 'text.input', payload: { text } });
  }
  /** Append a base64-encoded audio chunk for the current utterance. codec defaults to pcm16. */
  appendAudio(pcmBase64: string, codec = 'pcm16') {
    this.send({ type: 'audio.append', payload: { pcm: pcmBase64, codec } });
  }
  /** Commit the buffered audio utterance for ASR -> reply -> TTS. */
  commitAudio() {
    this.send({ type: 'audio.commit' });
  }
  resetAudio() {
    this.send({ type: 'audio.reset' });
  }

  close(): void {
    this.ws?.close();
    this.ws = undefined;
  }
}
