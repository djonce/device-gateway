import { test } from 'node:test';
import assert from 'node:assert/strict';
import { LightGatewayClient, type FetchLike } from '../src/client';
import { buildProvisionForm, provisionDevice } from '../src/provisioning';
import { VoiceSession } from '../src/realtime';

interface Captured {
  url: string;
  init?: RequestInit;
}

function fakeFetch(responseBody: unknown, captured: Captured[], status = 200): FetchLike {
  return async (url, init) => {
    captured.push({ url, init });
    return {
      ok: status >= 200 && status < 300,
      status,
      text: async () => (responseBody === undefined ? '' : JSON.stringify(responseBody)),
    } as unknown as Response;
  };
}

function headerOf(c: Captured, name: string): string | undefined {
  return (c.init?.headers as Record<string, string> | undefined)?.[name];
}

test('listDevices issues GET with admin bearer token', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001/', adminToken: 'ADM', fetch: fakeFetch({ items: [{ id: 'd1' }] }, cap) });
  const devices = await client.listDevices();
  assert.equal(devices[0]!.id, 'd1');
  assert.equal(cap[0]!.url, 'http://gw:7001/api/v1/devices');
  assert.equal(headerOf(cap[0]!, 'Authorization'), 'Bearer ADM');
});

test('light helper builds the light.color command', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001', adminToken: 'ADM', fetch: fakeFetch({ id: 'cmd-1' }, cap) });
  await client.light('d1').color('#ffd9a0');
  assert.equal(cap[0]!.url, 'http://gw:7001/api/v1/devices/d1/commands');
  assert.equal(cap[0]!.init?.method, 'POST');
  assert.deepEqual(JSON.parse(cap[0]!.init!.body as string), { type: 'light.color', payload: { hex: '#ffd9a0' } });
});

test('gps geofence helper matches the profile payload contract', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001', adminToken: 'ADM', fetch: fakeFetch({ id: 'cmd-2' }, cap) });
  await client.gps('tracker').geofence(31.2, 121.4, 300);
  assert.deepEqual(JSON.parse(cap[0]!.init!.body as string), {
    type: 'geofence.set',
    payload: { center: [31.2, 121.4], radius_m: 300 },
  });
});

test('device telemetry uses the device token header', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001', deviceToken: 'DEV', fetch: fakeFetch({ id: 'tel-1' }, cap) });
  await client.reportTelemetry('d1', 'gps.fix', { lat: 1, lng: 2 });
  assert.equal(headerOf(cap[0]!, 'X-Device-Token'), 'DEV');
  assert.equal(cap[0]!.url, 'http://gw:7001/api/v1/devices/d1/telemetry');
});

test('login stores the returned admin token', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001', fetch: fakeFetch({ token: 'SESSION' }, cap) });
  const res = await client.login('admin', 'pw');
  assert.equal(res.token, 'SESSION');
  assert.equal(client.adminToken, 'SESSION');
  assert.equal(cap[0]!.url, 'http://gw:7001/api/v1/auth/login');
});

test('GatewayError carries the status on failure', async () => {
  const cap: Captured[] = [];
  const client = new LightGatewayClient({ baseUrl: 'http://gw:7001', adminToken: 'ADM', fetch: fakeFetch({ error: 'admin authentication required' }, cap, 401) });
  await assert.rejects(() => client.listDevices(), (err: unknown) => err instanceof Error && (err as { status?: number }).status === 401);
});

test('buildProvisionForm produces the portal /save body', () => {
  const { url, body } = buildProvisionForm({ ssid: 'home', password: 'p', gateway: 'http://gw:7001', deviceId: 'esp-1', extra: { tz: 'CST-8' } });
  assert.equal(url, 'http://192.168.4.1/save');
  const params = new URLSearchParams(body);
  assert.equal(params.get('ssid'), 'home');
  assert.equal(params.get('gateway'), 'http://gw:7001');
  assert.equal(params.get('deviceId'), 'esp-1');
  assert.equal(params.get('tz'), 'CST-8');
});

test('provisionDevice POSTs urlencoded form to the portal', async () => {
  const cap: Captured[] = [];
  await provisionDevice({ ssid: 'home', password: 'p', gateway: 'http://gw:7001', fetch: fakeFetch({}, cap) });
  assert.equal(cap[0]!.url, 'http://192.168.4.1/save');
  assert.equal((cap[0]!.init?.headers as Record<string, string>)['Content-Type'], 'application/x-www-form-urlencoded');
  assert.ok((cap[0]!.init?.body as string).includes('ssid=home'));
});

test('VoiceSession builds the ws url with token and sends text', () => {
  const sent: string[] = [];
  const session = new VoiceSession({
    baseUrl: 'http://gw:7001',
    deviceId: 'xiaozhi-1',
    deviceToken: 'DEV',
    webSocketFactory: () => ({
      send: (d: string) => sent.push(d),
      close: () => {},
      onopen: null,
      onmessage: null,
      onclose: null,
      onerror: null,
    }),
  });
  assert.equal(session.url, 'ws://gw:7001/api/v1/devices/xiaozhi-1/ws?token=DEV');
  session.connect({});
  session.sendText('你好');
  assert.deepEqual(JSON.parse(sent[0]!), { type: 'text.input', payload: { text: '你好' } });
});
