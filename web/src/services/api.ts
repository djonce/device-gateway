export type DeviceType = 'esp' | 'android' | 'orangepi' | 'linux-node' | 'unknown';
export type DeviceCategory = '' | 'light' | 'clock' | 'gps' | 'voice';
export type OnlineState = 'online' | 'stale' | 'offline';
export type CommandStatus = 'queued' | 'delivered' | 'succeeded' | 'failed' | 'expired';

export interface Capability {
  name: string;
  description?: string;
  schema?: Record<string, string>;
}

export interface Device {
  id: string;
  name: string;
  type: DeviceType;
  category?: DeviceCategory;
  model?: string;
  profile?: string;
  fwVersion?: string;
  targetFwVersion?: string;
  geofence?: Geofence;
  geofenceState?: string;
  agentVersion?: string;
  labels?: Record<string, string>;
  capabilities?: Capability[];
  metadata?: Record<string, unknown>;
  disabled: boolean;
  tokenIssuedAt?: string;
  tokenLastUsedAt?: string;
  lastSeenAt: string;
  registeredAt: string;
  updatedAt: string;
  state: OnlineState;
}

export interface TelemetryPoint {
  id: string;
  deviceId: string;
  key: string;
  value: unknown;
  unit?: string;
  timestamp: string;
}

export interface Command {
  id: string;
  deviceId: string;
  type: string;
  payload?: Record<string, unknown>;
  status: CommandStatus;
  result?: Record<string, unknown>;
  error?: string;
  requestedBy?: string;
  createdAt: string;
  deliveredAt?: string;
  finishedAt?: string;
  expiresAt?: string;
}

export interface GatewayEvent {
  id: string;
  type: string;
  deviceId?: string;
  message: string;
  at: string;
  metadata?: Record<string, unknown>;
}

export interface RegisterDeviceInput {
  id: string;
  name: string;
  type: DeviceType;
  category?: DeviceCategory;
  model?: string;
  profile?: string;
  fwVersion?: string;
  agentVersion?: string;
  labels?: Record<string, string>;
  capabilities?: Capability[];
  metadata?: Record<string, unknown>;
}

export interface CommandSpec {
  type: string;
  description?: string;
  payloadKeys?: string[];
}

export interface TelemetrySpec {
  key: string;
  unit?: string;
  description?: string;
}

export interface Profile {
  id: string;
  category: DeviceCategory;
  title: string;
  realTime?: boolean;
  commands: CommandSpec[];
  telemetry: TelemetrySpec[];
}

export interface DailyForecast {
  date: string;
  code: number;
  text: string;
  maxC: number;
  minC: number;
}

export interface Weather {
  tempC: number;
  humidity: number;
  code: number;
  text: string;
  daily?: DailyForecast[];
  fetchedAt: string;
}

export interface ClockContent {
  time: { epoch: number; iso: string; tz: string };
  weather?: Weather;
  weatherError?: string;
}

export interface Geofence {
  centerLat: number;
  centerLng: number;
  radiusM: number;
}

export interface TrackPoint {
  lat: number;
  lng: number;
  speed?: number;
  timestamp: string;
}

export interface DeviceRegistration {
  device: Device;
  token?: string;
  reused: boolean;
}

export interface DeviceTokenReset {
  deviceId: string;
  token: string;
  issuedAt: string;
}

const TOKEN_KEY = 'lightgw_admin_token';

export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? '';
  } catch {
    return '';
  }
}
export function setToken(token: string) {
  try {
    localStorage.setItem(TOKEN_KEY, token);
  } catch {
    /* ignore */
  }
}
export function clearToken() {
  try {
    localStorage.removeItem(TOKEN_KEY);
  } catch {
    /* ignore */
  }
}

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((init?.headers as Record<string, string>) ?? {}),
  };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await fetch(url, { ...init, headers });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401) {
      clearToken();
      throw new Error('UNAUTHORIZED');
    }
    throw new Error(payload.error ?? `Request failed: ${response.status}`);
  }
  return payload as T;
}

export interface AuthStatus {
  authRequired: boolean;
}

export function authStatus() {
  return request<AuthStatus>('/api/v1/auth/status');
}

export async function login(username: string, password: string) {
  const res = await request<{ token?: string; authRequired?: boolean }>('/api/v1/auth/login', {
    method: 'POST',
    body: JSON.stringify({ username, password }),
  });
  if (res.token) setToken(res.token);
  return res;
}

export async function logout() {
  try {
    await request('/api/v1/auth/logout', { method: 'POST' });
  } catch {
    /* ignore */
  }
  clearToken();
}

export function listDevices() {
  return request<{ items: Device[] }>('/api/v1/devices');
}

export function getDevice(deviceId: string) {
  return request<Device>(`/api/v1/devices/${encodeURIComponent(deviceId)}`);
}

export function listTelemetry(deviceId: string) {
  return request<{ items: TelemetryPoint[] }>(`/api/v1/devices/${encodeURIComponent(deviceId)}/telemetry?limit=20`);
}

export function listCommands(deviceId: string) {
  return request<{ items: Command[] }>(`/api/v1/devices/${encodeURIComponent(deviceId)}/commands?limit=20`);
}

export function listEvents() {
  return request<{ items: GatewayEvent[] }>('/api/v1/events?limit=30');
}

export function listProfiles() {
  return request<{ items: Profile[] }>('/api/v1/profiles');
}

export function getClockContent(lat: number, lon: number, tz: string) {
  const params = new URLSearchParams({ lat: String(lat), lon: String(lon), tz });
  return request<ClockContent>(`/api/v1/content/clock?${params.toString()}`);
}

export function getTrack(deviceId: string, limit = 200) {
  return request<{ items: TrackPoint[] }>(`/api/v1/devices/${encodeURIComponent(deviceId)}/track?limit=${limit}`);
}

export function setGeofence(deviceId: string, geofence: Geofence) {
  return request<Device>(`/api/v1/devices/${encodeURIComponent(deviceId)}/geofence`, {
    method: 'POST',
    body: JSON.stringify(geofence),
  });
}

export interface RealtimeStatus {
  connected: boolean;
  connections: number;
}

export function getRealtimeStatus(deviceId: string) {
  return request<RealtimeStatus>(`/api/v1/devices/${encodeURIComponent(deviceId)}/realtime`);
}

export function sayToDevice(deviceId: string, text: string) {
  return request<{ ok: boolean }>(`/api/v1/devices/${encodeURIComponent(deviceId)}/realtime/say`, {
    method: 'POST',
    body: JSON.stringify({ text }),
  });
}

export interface Firmware {
  id: string;
  category: DeviceCategory;
  model?: string;
  version: string;
  size: number;
  sha256: string;
  notes?: string;
  createdAt: string;
}

export function listFirmware(category?: DeviceCategory) {
  const qs = category ? `?category=${encodeURIComponent(category)}` : '';
  return request<{ items: Firmware[] }>(`/api/v1/firmware${qs}`);
}

export async function uploadFirmware(
  meta: { category: DeviceCategory; model?: string; version: string; notes?: string },
  file: File,
) {
  const params = new URLSearchParams({ category: meta.category, version: meta.version });
  if (meta.model) params.set('model', meta.model);
  if (meta.notes) params.set('notes', meta.notes);
  const buf = await file.arrayBuffer();
  const headers: Record<string, string> = { 'Content-Type': 'application/octet-stream' };
  const token = getToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  const response = await fetch(`/api/v1/firmware?${params.toString()}`, {
    method: 'POST',
    headers,
    body: buf,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    if (response.status === 401) {
      clearToken();
      throw new Error('UNAUTHORIZED');
    }
    throw new Error(payload.error ?? `Upload failed: ${response.status}`);
  }
  return payload as Firmware;
}

export function setOtaTarget(deviceId: string, version: string) {
  return request<Device>(`/api/v1/devices/${encodeURIComponent(deviceId)}/ota/target`, {
    method: 'POST',
    body: JSON.stringify({ version }),
  });
}

export function rolloutFirmware(firmwareId: string) {
  return request<{ devices: number }>(`/api/v1/firmware/${encodeURIComponent(firmwareId)}/rollout`, {
    method: 'POST',
  });
}

export function createCommand(deviceId: string, body: { type: string; payload?: Record<string, unknown>; ttlSeconds?: number }) {
  return request<Command>(`/api/v1/devices/${encodeURIComponent(deviceId)}/commands`, {
    method: 'POST',
    body: JSON.stringify({ ...body, requestedBy: 'console' }),
  });
}

export function registerDemoDevice(device: RegisterDeviceInput) {
  return request<DeviceRegistration>('/api/v1/devices/register', {
    method: 'POST',
    body: JSON.stringify(device),
  });
}

export function resetDeviceToken(deviceId: string) {
  return request<DeviceTokenReset>(`/api/v1/devices/${encodeURIComponent(deviceId)}/token/reset`, {
    method: 'POST',
  });
}

export function updateDeviceStatus(deviceId: string, disabled: boolean) {
  return request<Device>(`/api/v1/devices/${encodeURIComponent(deviceId)}/status`, {
    method: 'POST',
    body: JSON.stringify({ disabled }),
  });
}
