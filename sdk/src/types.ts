// Types mirroring the Light Gateway API. Kept dependency-free and in sync with
// internal/device (Go) and the gateway REST/WS surface.

export type DeviceType = 'esp' | 'android' | 'orangepi' | 'linux-node' | 'unknown';
export type DeviceCategory = '' | 'light' | 'clock' | 'gps' | 'voice';
export type OnlineState = 'online' | 'stale' | 'offline';
export type CommandStatus = 'queued' | 'delivered' | 'succeeded' | 'failed' | 'expired';

export interface Capability {
  name: string;
  description?: string;
  schema?: Record<string, string>;
}

export interface Geofence {
  centerLat: number;
  centerLng: number;
  radiusM: number;
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

export interface CreateCommandInput {
  type: string;
  payload?: Record<string, unknown>;
  requestedBy?: string;
  ttlSeconds?: number;
}

export interface AckCommandInput {
  status: 'succeeded' | 'failed';
  result?: Record<string, unknown>;
  error?: string;
}

export interface GatewayEvent {
  id: string;
  type: string;
  deviceId?: string;
  message: string;
  at: string;
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

export interface TrackPoint {
  lat: number;
  lng: number;
  speed?: number;
  timestamp: string;
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

export interface OTAStatus {
  updateAvailable: boolean;
  currentVersion: string;
  targetVersion?: string;
  firmwareId?: string;
  version?: string;
  sha256?: string;
  size?: number;
  downloadUrl?: string;
}

export interface RealtimeStatus {
  connected: boolean;
  connections: number;
}

export interface RuleTrigger {
  type: 'telemetry' | 'event';
  key?: string;
  op?: 'gt' | 'gte' | 'lt' | 'lte' | 'eq' | 'ne';
  value?: number;
  eventType?: string;
  deviceId?: string;
  category?: string;
}

export interface RuleAction {
  type: 'command' | 'webhook';
  targetDeviceId?: string;
  targetCategory?: string;
  commandType?: string;
  payload?: Record<string, unknown>;
  webhookUrl?: string;
}

export interface Rule {
  id: string;
  name: string;
  enabled: boolean;
  trigger: RuleTrigger;
  action: RuleAction;
  createdAt: string;
}

export type Role = 'viewer' | 'operator' | 'admin';

export interface ApiKey {
  id: string;
  name: string;
  role: Role;
  createdAt: string;
}

// Realtime voice envelope (lightgw.voice.v0).
export interface VoiceEnvelope {
  type: string;
  seq?: number;
  ts?: number;
  payload?: Record<string, unknown>;
}
