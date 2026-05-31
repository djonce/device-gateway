import type { FetchLike } from './client';

export interface ProvisionInput {
  /** Device SoftAP captive-portal base URL. Default http://192.168.4.1 */
  portalBaseUrl?: string;
  ssid: string;
  password: string;
  /** Gateway URL the device should connect to, e.g. http://192.168.3.109:7001 */
  gateway: string;
  deviceId?: string;
  deviceName?: string;
  /** Extra portal fields (e.g. tz / lat / lon for clock & gps firmware). */
  extra?: Record<string, string>;
  fetch?: FetchLike;
}

/**
 * provisionDevice submits Wi-Fi + gateway settings to a device's SoftAP captive
 * portal (the firmware's POST /save form). After saving, the device reboots and
 * connects to the gateway.
 *
 * Network note: the caller's host must be on the device's "LightGateway-xxxx"
 * access point. Native apps (Android/iOS/Electron) can POST cross-origin freely;
 * a browser web app may be blocked by CORS unless it is served from the AP, in
 * which case submit a real <form> instead of fetch.
 */
export async function provisionDevice(input: ProvisionInput): Promise<void> {
  const base = (input.portalBaseUrl ?? 'http://192.168.4.1').replace(/\/+$/, '');
  const form = new URLSearchParams();
  form.set('ssid', input.ssid);
  form.set('password', input.password);
  form.set('gateway', input.gateway);
  if (input.deviceId) form.set('deviceId', input.deviceId);
  if (input.deviceName) form.set('deviceName', input.deviceName);
  for (const [key, value] of Object.entries(input.extra ?? {})) form.set(key, value);

  const f = input.fetch ?? (typeof fetch !== 'undefined' ? (fetch as FetchLike) : undefined);
  if (!f) throw new Error('no fetch implementation available; pass input.fetch');

  const response = await f(`${base}/save`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: form.toString(),
  });
  if (!response.ok) {
    throw new Error(`provisioning failed: ${response.status}`);
  }
}

/** Builds the portal /save form body without sending it (useful for native form posts). */
export function buildProvisionForm(input: ProvisionInput): { url: string; body: string } {
  const base = (input.portalBaseUrl ?? 'http://192.168.4.1').replace(/\/+$/, '');
  const form = new URLSearchParams();
  form.set('ssid', input.ssid);
  form.set('password', input.password);
  form.set('gateway', input.gateway);
  if (input.deviceId) form.set('deviceId', input.deviceId);
  if (input.deviceName) form.set('deviceName', input.deviceName);
  for (const [key, value] of Object.entries(input.extra ?? {})) form.set(key, value);
  return { url: `${base}/save`, body: form.toString() };
}
