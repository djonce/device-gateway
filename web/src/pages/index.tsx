import {
	Activity,
	CheckCircle2,
	Copy,
	Cpu,
	History,
	KeyRound,
	Loader2,
	Play,
	Plus,
	Power,
	RadioTower,
	RefreshCw,
	Send,
	ShieldOff,
	Smartphone,
	Terminal,
} from 'lucide-react';
import type React from 'react';
import { useEffect, useMemo, useState } from 'react';
import {
	Command,
	Device,
	GatewayEvent,
	RegisterDeviceInput,
	TelemetryPoint,
	createCommand,
  listCommands,
  listDevices,
	listEvents,
	listTelemetry,
	registerDemoDevice,
	resetDeviceToken,
	updateDeviceStatus,
} from '@/services/api';

type TableRow = {
  key: string;
  cells: string[];
};

const commandPayloads: Record<string, string> = {
  'shell.exec': '{"command":"uptime"}',
  'gpio.write': '{"pin":2,"value":true}',
  'sensor.read': '{"sensor":"all"}',
  'log.collect': '{"path":"/tmp/agent.log","tail":120}',
  'camera.snapshot': '{}',
  'serial.write': '{"data":"help","newline":true}',
  'serial.recent': '{}',
};

const typeIcons: Record<Device['type'], typeof Activity> = {
	esp: Cpu,
	android: Smartphone,
	orangepi: RadioTower,
  'linux-node': Terminal,
  unknown: Activity,
};

const demoDevices: RegisterDeviceInput[] = [
  {
    id: 'esp-livingroom-001',
    name: 'Living Room ESP',
    type: 'esp' as const,
    agentVersion: 'esp-agent/0.1.0',
    labels: { room: 'livingroom', transport: 'http' },
    capabilities: [{ name: 'sensor.read' }, { name: 'gpio.write' }],
  },
  {
    id: 'android-lab-001',
    name: 'Android Lab Phone',
    type: 'android' as const,
    agentVersion: 'android-agent/0.1.0',
    labels: { owner: 'lab', network: 'wifi' },
    capabilities: [{ name: 'battery.read' }, { name: 'log.collect' }],
  },
  {
    id: 'opi-zero2w-001',
    name: 'Orange Pi Zero 2W',
    type: 'orangepi' as const,
    agentVersion: 'linux-agent/0.1.0',
    labels: { role: 'edge-gateway', arch: 'arm64' },
    capabilities: [{ name: 'shell.exec' }, { name: 'camera.snapshot' }, { name: 'lan.scan' }],
  },
];

export default function HomePage() {
  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedId, setSelectedId] = useState<string>('');
  const [telemetry, setTelemetry] = useState<TelemetryPoint[]>([]);
  const [commands, setCommands] = useState<Command[]>([]);
  const [events, setEvents] = useState<GatewayEvent[]>([]);
  const [commandType, setCommandType] = useState('shell.exec');
  const [payload, setPayload] = useState('{"command":"uptime"}');
  const [lastToken, setLastToken] = useState('');
  const [loading, setLoading] = useState(false);
  const [actionBusy, setActionBusy] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [lastUpdatedAt, setLastUpdatedAt] = useState('');
  const [notice, setNotice] = useState('');
  const [error, setError] = useState('');

  const selected = useMemo(() => devices.find((device) => device.id === selectedId) ?? devices[0], [devices, selectedId]);
  const payloadError = useMemo(() => {
    if (!payload.trim()) return '';
    try {
      JSON.parse(payload);
      return '';
    } catch {
      return 'JSON payload is invalid';
    }
  }, [payload]);
  const counts = useMemo(
    () => ({
      online: devices.filter((device) => device.state === 'online').length,
      stale: devices.filter((device) => device.state === 'stale').length,
      offline: devices.filter((device) => device.state === 'offline').length,
      disabled: devices.filter((device) => device.disabled).length,
    }),
    [devices],
  );

  async function refresh(deviceId?: string, options: { silent?: boolean } = {}) {
    if (!options.silent) {
      setLoading(true);
    }
    if (!options.silent) {
      setError('');
    }
    try {
      const [deviceResponse, eventResponse] = await Promise.all([listDevices(), listEvents()]);
      setDevices(deviceResponse.items);
      setEvents(eventResponse.items);
      const nextSelectedId = deviceId || selectedId || deviceResponse.items[0]?.id || '';
      setSelectedId(nextSelectedId);
      if (nextSelectedId) {
        const [telemetryResponse, commandResponse] = await Promise.all([
          listTelemetry(nextSelectedId),
          listCommands(nextSelectedId),
        ]);
        setTelemetry(telemetryResponse.items);
        setCommands(commandResponse.items);
      } else {
        setTelemetry([]);
        setCommands([]);
      }
      setLastUpdatedAt(new Date().toISOString());
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      if (!options.silent) {
        setLoading(false);
      }
    }
  }

  async function seedDemoDevices() {
    setLoading(true);
    setError('');
    setNotice('');
    try {
      await Promise.all(demoDevices.map((device) => registerDemoDevice(device)));
      await refresh(demoDevices[0].id);
      setNotice('Demo devices are ready');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      setLoading(false);
    }
  }

  async function submitCommand() {
    if (!selected) return;
    if (payloadError) return;
    setError('');
    setNotice('');
    setActionBusy('command');
    try {
      const parsedPayload = payload.trim() ? JSON.parse(payload) : {};
      await createCommand(selected.id, { type: commandType, payload: parsedPayload, ttlSeconds: 300 });
      await refresh(selected.id);
      setNotice(`${commandType} queued for ${selected.name}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Command failed');
    } finally {
      setActionBusy('');
    }
  }

  async function resetToken() {
    if (!selected) return;
    setError('');
    setNotice('');
    setActionBusy('token');
    try {
      const result = await resetDeviceToken(selected.id);
      setLastToken(result.token);
      await refresh(selected.id);
      setNotice('Device token reset');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Reset token failed');
    } finally {
      setActionBusy('');
    }
  }

  async function toggleDisabled() {
    if (!selected) return;
    setError('');
    setNotice('');
    setActionBusy('status');
    try {
      await updateDeviceStatus(selected.id, !selected.disabled);
      await refresh(selected.id);
      setNotice(selected.disabled ? 'Device enabled' : 'Device disabled');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Update device failed');
    } finally {
      setActionBusy('');
    }
  }

  function selectDevice(deviceId: string) {
    setSelectedId(deviceId);
    setLastToken('');
    refresh(deviceId, { silent: true });
  }

  function updateCommandType(nextCommandType: string) {
    setCommandType(nextCommandType);
    setPayload(commandPayloads[nextCommandType] ?? '{}');
  }

  useEffect(() => {
    refresh();
  }, []);

  useEffect(() => {
    if (!autoRefresh) return undefined;
    const timer = window.setInterval(() => refresh(selectedId, { silent: true }), 8000);
    return () => window.clearInterval(timer);
  }, [autoRefresh, selectedId]);

  useEffect(() => {
    setLastToken('');
  }, [selected?.id]);

  return (
    <main className="shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brandMark">
            <RadioTower size={22} />
          </div>
          <div>
            <h1>Light Gateway</h1>
            <p>Device control plane</p>
          </div>
        </div>

        <div className="metricGrid">
          <Metric label="Online" value={counts.online} tone="good" />
          <Metric label="Stale" value={counts.stale} tone="warn" />
          <Metric label="Disabled" value={counts.disabled} tone="bad" />
        </div>

        <div className="toolbar">
          <button type="button" className="iconButton" onClick={() => refresh(selected?.id)} title="Refresh devices" disabled={loading}>
            <RefreshCw size={18} className={loading ? 'spin' : ''} />
          </button>
          <button type="button" className="textButton" onClick={seedDemoDevices} disabled={loading}>
            <Plus size={16} />
            Seed Demo
          </button>
        </div>

        <div className="deviceList">
          {devices.map((device) => {
            const Icon = typeIcons[device.type] ?? Activity;
            return (
              <button
                key={device.id}
                type="button"
                className={`deviceRow ${selected?.id === device.id ? 'active' : ''} ${device.disabled ? 'disabled' : ''}`}
                onClick={() => selectDevice(device.id)}
              >
                <span className="deviceIcon">
                  <Icon size={18} />
                </span>
                <span>
                  <strong>{device.name}</strong>
                  <small>{device.id}</small>
                </span>
                <i className={`stateDot ${device.disabled ? 'disabled' : device.state}`} />
              </button>
            );
          })}
          {devices.length === 0 && <div className="empty">No devices yet. Seed demo data or register an agent.</div>}
        </div>
      </aside>

      <section className="workspace">
        <header className="topbar">
          <div>
            <span className="eyebrow">Unified terminal access</span>
            <h2>{selected?.name ?? 'Waiting for devices'}</h2>
          </div>
          <div className="statusPill">
            <span className={`stateDot ${selected?.disabled ? 'disabled' : selected?.state ?? 'offline'}`} />
            {selected?.disabled ? 'disabled' : selected?.state ?? 'no device'}
          </div>
        </header>

        <div className="feedbackSlot">
          {error && <div className="errorBanner">{error}</div>}
          {!error && notice && (
            <div className="noticeBanner">
              <CheckCircle2 size={16} />
              {notice}
            </div>
          )}
        </div>
        <div className="loadingSlot" aria-hidden={!loading}>
          {loading && <div className="loadingLine" />}
        </div>

        {selected ? (
          <div className="contentGrid">
            <section className="panel devicePanel">
              <PanelTitle icon={<Activity size={18} />} title="Device Profile" />
              <dl className="details">
                <div>
                  <dt>Type</dt>
                  <dd>{selected.type}</dd>
                </div>
                <div>
                  <dt>Agent</dt>
                  <dd>{selected.agentVersion || '-'}</dd>
                </div>
                <div>
                  <dt>Last seen</dt>
                  <dd>{formatTime(selected.lastSeenAt)}</dd>
                </div>
                <div>
                  <dt>Labels</dt>
                  <dd>{formatKV(selected.labels)}</dd>
                </div>
                <div>
                  <dt>Token issued</dt>
                  <dd>{formatTime(selected.tokenIssuedAt)}</dd>
                </div>
                <div>
                  <dt>Token used</dt>
                  <dd>{formatTime(selected.tokenLastUsedAt)}</dd>
                </div>
              </dl>
              <div className="capabilities">
                {(selected.capabilities ?? []).map((capability) => (
                  <span key={capability.name}>{capability.name}</span>
                ))}
              </div>
              <div className="actionRow">
                <button type="button" className="textButton" onClick={resetToken} disabled={Boolean(actionBusy)}>
                  {actionBusy === 'token' ? <Loader2 size={16} className="spin" /> : <KeyRound size={16} />}
                  {actionBusy === 'token' ? 'Resetting...' : 'Reset Token'}
                </button>
                <button type="button" className="textButton" onClick={toggleDisabled} disabled={Boolean(actionBusy)}>
                  {actionBusy === 'status' ? <Loader2 size={16} className="spin" /> : selected.disabled ? <Power size={16} /> : <ShieldOff size={16} />}
                  {actionBusy === 'status' ? 'Updating...' : selected.disabled ? 'Enable Device' : 'Disable Device'}
                </button>
              </div>
              {lastToken && (
                <div className="secretBox">
                  <div>
                    <strong>New device token</strong>
                    <small>Visible once. Store it in the agent token file.</small>
                  </div>
                  <code>{lastToken}</code>
                  <button
                    type="button"
                    className="iconButton"
                    title="Copy token"
                    onClick={() => navigator.clipboard?.writeText(lastToken)}
                  >
                    <Copy size={16} />
                  </button>
                </div>
              )}
              <pre className="jsonBlock">{stringifyPretty(selected.metadata ?? {})}</pre>
            </section>

            <section className="panel commandPanel">
              <PanelTitle icon={<Send size={18} />} title="Command Dispatch" />
              <div className="formGrid">
                <label>
                  Command type
                  <select value={commandType} onChange={(event) => updateCommandType(event.target.value)}>
                    <option value="shell.exec">shell.exec</option>
                    <option value="gpio.write">gpio.write</option>
                    <option value="sensor.read">sensor.read</option>
                    <option value="log.collect">log.collect</option>
                    <option value="camera.snapshot">camera.snapshot</option>
                    <option value="serial.write">serial.write</option>
                    <option value="serial.recent">serial.recent</option>
                  </select>
                </label>
                <label>
                  JSON payload
                  <textarea
                    className={payloadError ? 'invalidInput' : ''}
                    value={payload}
                    onChange={(event) => setPayload(event.target.value)}
                    rows={5}
                    spellCheck={false}
                  />
                </label>
                <div className="formMeta">
                  <span className={payloadError ? 'fieldError' : ''}>{payloadError || `${commandType} payload preset`}</span>
                  <button type="button" className="linkButton" onClick={() => setPayload(commandPayloads[commandType] ?? '{}')}>
                    Reset payload
                  </button>
                </div>
                <button
                  type="button"
                  className="primaryButton"
                  onClick={submitCommand}
                  disabled={Boolean(payloadError) || actionBusy === 'command' || selected.disabled}
                >
                  {actionBusy === 'command' ? <Loader2 size={16} className="spin" /> : <Play size={16} />}
                  {actionBusy === 'command' ? 'Queueing...' : selected.disabled ? 'Device Disabled' : 'Queue Command'}
                </button>
                <div className="refreshControl">
                  <label className="switchLabel">
                    <input type="checkbox" checked={autoRefresh} onChange={(event) => setAutoRefresh(event.target.checked)} />
                    <span>Auto refresh</span>
                  </label>
                  <span>{lastUpdatedAt ? `Updated ${formatTime(lastUpdatedAt)}` : 'Not refreshed yet'}</span>
                </div>
              </div>
            </section>

            <section className="panel">
              <PanelTitle icon={<Activity size={18} />} title="Telemetry" />
              <Table
                columns={['Key', 'Value', 'Time']}
                rows={telemetry.map((point) => ({
                  key: point.id,
                  cells: [point.key, `${stringify(point.value)} ${point.unit ?? ''}`, formatTime(point.timestamp)],
                }))}
                empty="No telemetry yet"
              />
            </section>

            <section className="panel">
              <PanelTitle icon={<Terminal size={18} />} title="Commands" />
              <Table
                columns={['Type', 'Status', 'Timing', 'Result']}
                rows={commands.map((command) => ({
                  key: command.id,
                  cells: [
                    command.type,
                    command.status,
                    `${formatTime(command.createdAt)} / ${formatTime(command.finishedAt)}`,
                    command.error || truncateInline(stringifyPretty(command.result ?? command.payload ?? {}), 80),
                  ],
                }))}
                empty="No commands yet"
              />
            </section>

            <section className="panel eventPanel">
              <PanelTitle icon={<History size={18} />} title="Recent Events" />
              <div className="eventList">
                {events.map((event) => (
                  <div key={event.id} className="eventRow">
                    <span>{formatTime(event.at)}</span>
                    <strong>{event.type}</strong>
                    <small>{event.deviceId || 'system'} · {event.message}</small>
                  </div>
                ))}
              </div>
            </section>
          </div>
        ) : (
          <div className="noSelection">
            <RadioTower size={36} />
            <h3>No devices registered</h3>
            <button type="button" className="primaryButton" onClick={seedDemoDevices}>
              <Plus size={16} />
              Seed Demo Devices
            </button>
          </div>
        )}
      </section>
    </main>
  );
}

function Metric({ label, value, tone }: { label: string; value: number; tone: string }) {
  return (
    <div className={`metric ${tone}`}>
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function PanelTitle({ icon, title }: { icon: React.ReactNode; title: string }) {
  return (
    <div className="panelTitle">
      {icon}
      <h3>{title}</h3>
    </div>
  );
}

function Table({ columns, rows, empty }: { columns: string[]; rows: TableRow[]; empty: string }) {
  if (rows.length === 0) {
    return <div className="empty tableEmpty">{empty}</div>;
  }
  return (
    <table>
      <thead>
        <tr>
          {columns.map((column) => (
            <th key={column}>{column}</th>
          ))}
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.key}>
            {row.cells.map((cell, cellIndex) => (
              <td key={`${cell}-${cellIndex}`}>{cell}</td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function formatTime(value?: string) {
  if (!value) return '-';
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(value));
}

function formatKV(value?: Record<string, string>) {
  if (!value || Object.keys(value).length === 0) return '-';
  return Object.entries(value)
    .map(([key, item]) => `${key}=${item}`)
    .join(', ');
}

function stringify(value: unknown) {
  if (typeof value === 'string') return value;
  return JSON.stringify(value);
}

function stringifyPretty(value: unknown) {
  return JSON.stringify(value, null, 2);
}

function truncateInline(value: string, limit: number) {
  const compact = value.replace(/\s+/g, ' ').trim();
  return compact.length > limit ? `${compact.slice(0, limit)}...` : compact;
}
