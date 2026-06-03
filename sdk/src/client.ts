import type {
  AckCommandInput,
  ClockContent,
  Command,
  CreateCommandInput,
  Device,
  DeviceRegistration,
  DeviceTokenReset,
  Firmware,
  GatewayEvent,
  Geofence,
  OTAStatus,
  Profile,
  RealtimeStatus,
  RegisterDeviceInput,
  Rule,
  TelemetryPoint,
  TrackPoint,
} from './types';

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export interface ClientOptions {
  /** Gateway base URL, e.g. http://192.168.3.109:7001 */
  baseUrl: string;
  /** Admin session token (operator/console endpoints). */
  adminToken?: string;
  /** Device token (device data-plane endpoints). */
  deviceToken?: string;
  /** Enrollment key sent as X-Provision-Key when registering (if the gateway requires it). */
  provisionKey?: string;
  /** Inject a fetch implementation (tests, non-browser runtimes). Defaults to global fetch. */
  fetch?: FetchLike;
}

export class GatewayError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = 'GatewayError';
    this.status = status;
  }
}

type Auth = 'admin' | 'device' | 'none';

/**
 * LightGatewayClient wraps the gateway's functional API. Use it from an operator
 * app (set adminToken) or a device agent (set deviceToken). The same instance
 * can carry both.
 */
export class LightGatewayClient {
  readonly baseUrl: string;
  adminToken: string;
  deviceToken: string;
  provisionKey: string;
  private fetchImpl: FetchLike;

  constructor(opts: ClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/+$/, '');
    this.adminToken = opts.adminToken ?? '';
    this.deviceToken = opts.deviceToken ?? '';
    this.provisionKey = opts.provisionKey ?? '';
    const f = opts.fetch ?? (typeof fetch !== 'undefined' ? (fetch as FetchLike) : undefined);
    if (!f) throw new Error('no fetch implementation available; pass options.fetch');
    this.fetchImpl = f;
  }

  setAdminToken(token: string) {
    this.adminToken = token;
  }
  setDeviceToken(token: string) {
    this.deviceToken = token;
  }

  private async req<T>(path: string, init: RequestInit = {}, auth: Auth = 'admin'): Promise<T> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...((init.headers as Record<string, string>) ?? {}),
    };
    if (auth === 'admin' && this.adminToken) headers.Authorization = `Bearer ${this.adminToken}`;
    if (auth === 'device' && this.deviceToken) headers['X-Device-Token'] = this.deviceToken;
    const response = await this.fetchImpl(`${this.baseUrl}${path}`, { ...init, headers });
    const text = await response.text();
    const payload = text ? safeParse(text) : {};
    if (!response.ok) {
      throw new GatewayError(response.status, (payload as { error?: string }).error ?? `request failed: ${response.status}`);
    }
    return payload as T;
  }

  // ---- auth (admin) ----
  authStatus() {
    return this.req<{ authRequired: boolean }>('/api/v1/auth/status', {}, 'none');
  }
  async login(username: string, password: string) {
    const res = await this.req<{ token?: string; authRequired?: boolean }>(
      '/api/v1/auth/login',
      { method: 'POST', body: JSON.stringify({ username, password }) },
      'none',
    );
    if (res.token) this.adminToken = res.token;
    return res;
  }
  async logout() {
    try {
      await this.req('/api/v1/auth/logout', { method: 'POST' });
    } finally {
      this.adminToken = '';
    }
  }

  // ---- devices ----
  async listDevices(): Promise<Device[]> {
    return (await this.req<{ items: Device[] }>('/api/v1/devices')).items;
  }
  getDevice(deviceId: string) {
    return this.req<Device>(`/api/v1/devices/${enc(deviceId)}`);
  }
  /** Device self-registration. Returns a one-time token on first registration.
   * Sends the provisioning key when configured (if the gateway requires it). */
  registerDevice(input: RegisterDeviceInput) {
    const headers: Record<string, string> = {};
    if (this.provisionKey) headers['X-Provision-Key'] = this.provisionKey;
    return this.req<DeviceRegistration>('/api/v1/devices/register', { method: 'POST', headers, body: JSON.stringify(input) }, 'none');
  }
  resetDeviceToken(deviceId: string) {
    return this.req<DeviceTokenReset>(`/api/v1/devices/${enc(deviceId)}/token/reset`, { method: 'POST' });
  }
  setDeviceDisabled(deviceId: string, disabled: boolean) {
    return this.req<Device>(`/api/v1/devices/${enc(deviceId)}/status`, { method: 'POST', body: JSON.stringify({ disabled }) });
  }

  // ---- telemetry ----
  async listTelemetry(deviceId: string, limit = 50): Promise<TelemetryPoint[]> {
    return (await this.req<{ items: TelemetryPoint[] }>(`/api/v1/devices/${enc(deviceId)}/telemetry?limit=${limit}`)).items;
  }
  /** Device-side telemetry report (uses the device token). */
  reportTelemetry(deviceId: string, key: string, value: unknown, unit?: string) {
    return this.req<TelemetryPoint>(
      `/api/v1/devices/${enc(deviceId)}/telemetry`,
      { method: 'POST', body: JSON.stringify({ key, value, unit }) },
      'device',
    );
  }

  // ---- commands ----
  createCommand(deviceId: string, input: CreateCommandInput) {
    return this.req<Command>(`/api/v1/devices/${enc(deviceId)}/commands`, { method: 'POST', body: JSON.stringify(input) });
  }
  async listCommands(deviceId: string, limit = 50): Promise<Command[]> {
    return (await this.req<{ items: Command[] }>(`/api/v1/devices/${enc(deviceId)}/commands?limit=${limit}`)).items;
  }
  /** Device-side: long-poll for the next command (uses the device token). */
  nextCommand(deviceId: string, timeoutSeconds = 30) {
    return this.req<{ command: Command | null }>(
      `/api/v1/devices/${enc(deviceId)}/commands/next?timeout=${timeoutSeconds}`,
      {},
      'device',
    );
  }
  /** Device-side: acknowledge a command (uses the device token). */
  ackCommand(deviceId: string, commandId: string, input: AckCommandInput) {
    return this.req<Command>(
      `/api/v1/devices/${enc(deviceId)}/commands/${enc(commandId)}/ack`,
      { method: 'POST', body: JSON.stringify(input) },
      'device',
    );
  }

  // ---- events / profiles / content ----
  async listEvents(limit = 100): Promise<GatewayEvent[]> {
    return (await this.req<{ items: GatewayEvent[] }>(`/api/v1/events?limit=${limit}`)).items;
  }
  async listProfiles(): Promise<Profile[]> {
    return (await this.req<{ items: Profile[] }>('/api/v1/profiles')).items;
  }
  getClockContent(lat: number, lon: number, tz: string) {
    const qs = new URLSearchParams({ lat: String(lat), lon: String(lon), tz });
    return this.req<ClockContent>(`/api/v1/content/clock?${qs.toString()}`, {}, 'none');
  }

  // ---- geofence / track ----
  setGeofence(deviceId: string, geofence: Geofence) {
    return this.req<Device>(`/api/v1/devices/${enc(deviceId)}/geofence`, { method: 'POST', body: JSON.stringify(geofence) });
  }
  async getTrack(deviceId: string, limit = 200): Promise<TrackPoint[]> {
    return (await this.req<{ items: TrackPoint[] }>(`/api/v1/devices/${enc(deviceId)}/track?limit=${limit}`)).items;
  }

  // ---- firmware / OTA ----
  async listFirmware(category?: string): Promise<Firmware[]> {
    const qs = category ? `?category=${enc(category)}` : '';
    return (await this.req<{ items: Firmware[] }>(`/api/v1/firmware${qs}`)).items;
  }
  /** Upload a firmware binary (admin). data is the raw binary. */
  uploadFirmware(meta: { category: string; model?: string; version: string; notes?: string }, data: ArrayBuffer | Uint8Array) {
    const params = new URLSearchParams({ category: meta.category, version: meta.version });
    if (meta.model) params.set('model', meta.model);
    if (meta.notes) params.set('notes', meta.notes);
    const body = data instanceof Uint8Array ? data : new Uint8Array(data);
    return this.req<Firmware>(`/api/v1/firmware?${params.toString()}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: body as unknown as BodyInit,
    });
  }
  setOtaTarget(deviceId: string, version: string) {
    return this.req<Device>(`/api/v1/devices/${enc(deviceId)}/ota/target`, { method: 'POST', body: JSON.stringify({ version }) });
  }
  rolloutFirmware(firmwareId: string) {
    return this.req<{ devices: number }>(`/api/v1/firmware/${enc(firmwareId)}/rollout`, { method: 'POST' });
  }
  /** Device-side: check whether an update is available (uses the device token). */
  getOtaStatus(deviceId: string) {
    return this.req<OTAStatus>(`/api/v1/devices/${enc(deviceId)}/ota`, {}, 'device');
  }

  // ---- realtime (voice) control plane ----
  getRealtimeStatus(deviceId: string) {
    return this.req<RealtimeStatus>(`/api/v1/devices/${enc(deviceId)}/realtime`);
  }
  /** Push a tts.say to a connected voice device. */
  say(deviceId: string, text: string) {
    return this.req<{ ok: boolean }>(`/api/v1/devices/${enc(deviceId)}/realtime/say`, { method: 'POST', body: JSON.stringify({ text }) });
  }

  // ---- automation rules ----
  async listRules(): Promise<Rule[]> {
    return (await this.req<{ items: Rule[] }>('/api/v1/rules')).items;
  }
  createRule(rule: Omit<Rule, 'id' | 'createdAt'>) {
    return this.req<Rule>('/api/v1/rules', { method: 'POST', body: JSON.stringify(rule) });
  }
  setRuleEnabled(ruleId: string, enabled: boolean) {
    return this.req<Rule>(`/api/v1/rules/${enc(ruleId)}/enable`, { method: 'POST', body: JSON.stringify({ enabled }) });
  }
  deleteRule(ruleId: string) {
    return this.req<{ ok: boolean }>(`/api/v1/rules/${enc(ruleId)}`, { method: 'DELETE' });
  }

  // ---- ergonomic, profile-aware control helpers ----
  light(deviceId: string) {
    const send = (type: string, payload: Record<string, unknown>) => this.createCommand(deviceId, { type, payload });
    return {
      power: (on: boolean) => send('light.power', { on }),
      brightness: (value: number) => send('light.brightness', { value }),
      color: (hex: string) => send('light.color', { hex }),
      colorRGB: (r: number, g: number, b: number) => send('light.color', { r, g, b }),
      effect: (name: 'static' | 'breath' | 'rainbow', speed = 5) => send('light.effect', { name, speed }),
      schedule: (on: string, off: string) => send('light.schedule', { on, off }),
    };
  }
  clock(deviceId: string) {
    const send = (type: string, payload: Record<string, unknown>) => this.createCommand(deviceId, { type, payload });
    return {
      mode: (mode: 'clock' | 'calendar' | 'weather') => send('display.mode', { mode }),
      brightness: (value: number) => send('display.brightness', { value }),
      syncTime: (epoch: number, tz: string) => send('time.sync', { epoch, tz }),
      pushWeather: (temp: number, cond: string, city?: string) => send('weather.push', { temp, cond, city }),
    };
  }
  gps(deviceId: string) {
    const send = (type: string, payload: Record<string, unknown>) => this.createCommand(deviceId, { type, payload });
    return {
      interval: (seconds: number) => send('gps.interval', { seconds }),
      geofence: (centerLat: number, centerLng: number, radiusM: number) =>
        send('geofence.set', { center: [centerLat, centerLng], radius_m: radiusM }),
    };
  }
  voice(deviceId: string) {
    const send = (type: string, payload: Record<string, unknown>) => this.createCommand(deviceId, { type, payload });
    return {
      wake: (enabled: boolean) => send('voice.wake', { enabled }),
      say: (text: string) => this.say(deviceId, text),
    };
  }
}

function enc(s: string): string {
  return encodeURIComponent(s);
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return {};
  }
}
