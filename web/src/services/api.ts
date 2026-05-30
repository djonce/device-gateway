export type DeviceType = 'esp' | 'android' | 'orangepi' | 'linux-node' | 'unknown';
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
  agentVersion?: string;
  labels?: Record<string, string>;
  capabilities?: Capability[];
  metadata?: Record<string, unknown>;
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

async function request<T>(url: string, init?: RequestInit): Promise<T> {
  const response = await fetch(url, {
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
    ...init,
  });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) {
    throw new Error(payload.error ?? `Request failed: ${response.status}`);
  }
  return payload as T;
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
