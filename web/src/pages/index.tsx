import {
	Activity,
	CheckCircle2,
	Clock,
	CloudSun,
	Copy,
	Cpu,
	Gauge,
	History,
	KeyRound,
	Lightbulb,
	LineChart,
	Loader2,
	LogOut,
	MapPin,
	Mic,
	Play,
	Plus,
	Power,
	RadioTower,
	RefreshCw,
	Send,
	ShieldOff,
	Smartphone,
	Terminal,
	UploadCloud,
	Zap,
} from 'lucide-react';
import type React from 'react';
import { useEffect, useMemo, useState } from 'react';
import {
	ClockContent,
	Command,
	Device,
	Firmware,
	GatewayEvent,
	Geofence,
	RegisterDeviceInput,
	TelemetryPoint,
	TrackPoint,
	authStatus,
	createCommand,
	createRule,
	deleteRule,
	getClockContent,
	getRealtimeStatus,
	getStats,
	getTelemetryHistory,
	getTelemetrySeries,
	getToken,
	getTrack,
	type FleetStats,
	type Rule,
	type RuleAction,
	type RuleTrigger,
	type TelemetryBucket,
  listCommands,
  listDevices,
	listEvents,
	listFirmware,
	listRules,
	listTelemetry,
	login,
	logout,
	registerDemoDevice,
	resetDeviceToken,
	rolloutFirmware,
	sayToDevice,
	setGeofence,
	setOtaTarget,
	setRuleEnabled,
	updateDeviceStatus,
	uploadFirmware,
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
    id: 'esp-nightlight-001',
    name: 'Bedroom Night Light',
    type: 'esp' as const,
    category: 'light' as const,
    model: 'esp32-rgb-strip',
    agentVersion: 'esp32-light/0.1.0',
    labels: { room: 'bedroom', transport: 'wifi' },
  },
  {
    id: 'esp-deskclock-001',
    name: 'Desk Weather Clock',
    type: 'esp' as const,
    category: 'clock' as const,
    model: 'esp32-clock-weather',
    agentVersion: 'esp32-clock/0.1.0',
    labels: { room: 'study', transport: 'wifi', lat: '31.2304', lon: '121.4737' },
    metadata: { tz: 'Asia/Shanghai' },
  },
  {
    id: 'esp-tracker-001',
    name: 'Asset GPS Tracker',
    type: 'esp' as const,
    category: 'gps' as const,
    model: 'esp32-neo6m',
    agentVersion: 'esp32-gps/0.1.0',
    labels: { asset: 'van-7', transport: 'wifi' },
  },
  {
    id: 'esp-xiaozhi-001',
    name: 'XiaoZhi Voice Assistant',
    type: 'esp' as const,
    category: 'voice' as const,
    model: 'esp32-s3-xiaozhi',
    agentVersion: 'esp32-voice/0.1.0',
    labels: { room: 'study', transport: 'wifi' },
  },
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
  const [authRequired, setAuthRequired] = useState(false);
  const [authed, setAuthed] = useState(false);
  const [loginUser, setLoginUser] = useState('admin');
  const [loginPass, setLoginPass] = useState('');
  const [loginError, setLoginError] = useState('');
  const [loggingIn, setLoggingIn] = useState(false);
  const [devices, setDevices] = useState<Device[]>([]);
  const [selectedId, setSelectedId] = useState<string>('');
  const [telemetry, setTelemetry] = useState<TelemetryPoint[]>([]);
  const [commands, setCommands] = useState<Command[]>([]);
  const [events, setEvents] = useState<GatewayEvent[]>([]);
  const [stats, setStats] = useState<FleetStats | null>(null);
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
  const numericKeys = useMemo(
    () => Array.from(new Set(telemetry.filter((point) => typeof point.value === 'number').map((point) => point.key))),
    [telemetry],
  );

  async function refresh(deviceId?: string, options: { silent?: boolean } = {}) {
    if (!options.silent) {
      setLoading(true);
    }
    if (!options.silent) {
      setError('');
    }
    try {
      const [deviceResponse, eventResponse, statsResponse] = await Promise.all([listDevices(), listEvents(), getStats()]);
      setDevices(deviceResponse.items);
      setEvents(eventResponse.items);
      setStats(statsResponse);
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
      if (err instanceof Error && err.message === 'UNAUTHORIZED') {
        setAuthed(false);
        return;
      }
      setError(err instanceof Error ? err.message : 'Unknown error');
    } finally {
      if (!options.silent) {
        setLoading(false);
      }
    }
  }

  async function submitLogin(event: React.FormEvent) {
    event.preventDefault();
    setLoginError('');
    setLoggingIn(true);
    try {
      await login(loginUser, loginPass);
      setAuthed(true);
    } catch {
      setLoginError('用户名或密码错误');
    } finally {
      setLoggingIn(false);
    }
  }

  async function doLogout() {
    await logout();
    setAuthed(false);
    setLoginPass('');
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

  async function sendDeviceCommand(type: string, payload: Record<string, unknown>) {
    if (!selected) return;
    setError('');
    setNotice('');
    setActionBusy('cmd');
    try {
      await createCommand(selected.id, { type, payload, ttlSeconds: 120 });
      await refresh(selected.id);
      setNotice(`${type} 已下发给 ${selected.name}`);
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
    authStatus()
      .then((status) => {
        setAuthRequired(status.authRequired);
        if (!status.authRequired || getToken()) setAuthed(true);
      })
      .catch(() => {
        setAuthRequired(false);
        setAuthed(true);
      });
  }, []);

  useEffect(() => {
    if (authed) refresh();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [authed]);

  useEffect(() => {
    if (!authed || !autoRefresh) return undefined;
    const timer = window.setInterval(() => refresh(selectedId, { silent: true }), 8000);
    return () => window.clearInterval(timer);
  }, [authed, autoRefresh, selectedId]);

  useEffect(() => {
    setLastToken('');
  }, [selected?.id]);

  if (authRequired && !authed) {
    return (
      <main className="loginShell" style={{ display: 'grid', placeItems: 'center', minHeight: '100vh' }}>
        <form className="panel" style={{ width: 320, padding: 24 }} onSubmit={submitLogin}>
          <PanelTitle icon={<KeyRound size={18} />} title="管理后台登录" />
          <div className="formGrid">
            <label>
              用户名
              <input value={loginUser} onChange={(event) => setLoginUser(event.target.value)} autoComplete="username" />
            </label>
            <label>
              密码
              <input
                type="password"
                value={loginPass}
                onChange={(event) => setLoginPass(event.target.value)}
                autoComplete="current-password"
              />
            </label>
            <button type="submit" className="primaryButton" disabled={loggingIn}>
              {loggingIn ? <Loader2 size={16} className="spin" /> : <KeyRound size={16} />}
              登录
            </button>
            {loginError && <span className="fieldError">{loginError}</span>}
          </div>
        </form>
      </main>
    );
  }

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
          {authRequired && (
            <button type="button" className="iconButton" onClick={doLogout} title="退出登录">
              <LogOut size={18} />
            </button>
          )}
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

        {stats && <FleetDashboard stats={stats} />}
        {authed && <AutomationPanel />}

        {selected ? (
          <div className="contentGrid">
            {selected.category === 'light' && (
              <LightPanel disabled={selected.disabled} busy={actionBusy === 'cmd'} onSend={sendDeviceCommand} />
            )}
            {selected.category === 'clock' && (
              <ClockPanel device={selected} busy={actionBusy === 'cmd'} onSend={sendDeviceCommand} />
            )}
            {selected.category === 'gps' && (
              <GpsPanel device={selected} busy={actionBusy === 'cmd'} onSend={sendDeviceCommand} />
            )}
            {selected.category === 'voice' && (
              <VoicePanel device={selected} busy={actionBusy === 'cmd'} onSend={sendDeviceCommand} />
            )}
            {selected.category && <OtaPanel device={selected} onChanged={() => refresh(selected.id)} />}
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

            {numericKeys.length > 0 && <TelemetryChart device={selected} numericKeys={numericKeys} />}

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

function LightPanel({
  disabled,
  busy,
  onSend,
}: {
  disabled: boolean;
  busy: boolean;
  onSend: (type: string, payload: Record<string, unknown>) => void;
}) {
  const [on, setOn] = useState(true);
  const [brightness, setBrightness] = useState(80);
  const [color, setColor] = useState('#ffd9a0');
  const [effect, setEffect] = useState('static');
  const [speed, setSpeed] = useState(5);
  const [scheduleOn, setScheduleOn] = useState('22:00');
  const [scheduleOff, setScheduleOff] = useState('07:00');
  const locked = disabled || busy;

  function togglePower() {
    const next = !on;
    setOn(next);
    onSend('light.power', { on: next });
  }

  return (
    <section className="panel commandPanel">
      <PanelTitle icon={<Lightbulb size={18} />} title="灯光控制" />
      <div className="formGrid">
        <div className="formMeta">
          <button type="button" className="primaryButton" onClick={togglePower} disabled={locked}>
            <Power size={16} />
            {on ? '关灯' : '开灯'}
          </button>
          <span
            title={color}
            style={{
              width: 28,
              height: 28,
              borderRadius: 8,
              border: '1px solid rgba(0,0,0,0.15)',
              background: on ? color : '#1b2330',
              opacity: on ? Math.max(0.25, brightness / 100) : 1,
            }}
          />
        </div>

        <label>
          亮度 · {brightness}%
          <input
            type="range"
            min={0}
            max={100}
            value={brightness}
            onChange={(event) => setBrightness(Number(event.target.value))}
            onMouseUp={() => onSend('light.brightness', { value: brightness })}
            onTouchEnd={() => onSend('light.brightness', { value: brightness })}
            disabled={locked}
          />
        </label>

        <label>
          颜色
          <input
            type="color"
            value={color}
            onChange={(event) => setColor(event.target.value)}
            onBlur={() => onSend('light.color', { hex: color })}
            disabled={locked}
          />
        </label>

        <label>
          灯效
          <select value={effect} onChange={(event) => setEffect(event.target.value)} disabled={locked}>
            <option value="static">静态</option>
            <option value="breath">呼吸</option>
            <option value="rainbow">彩虹</option>
          </select>
        </label>

        <label>
          灯效速度 · {speed}
          <input
            type="range"
            min={1}
            max={10}
            value={speed}
            onChange={(event) => setSpeed(Number(event.target.value))}
            disabled={locked}
          />
        </label>
        <button
          type="button"
          className="textButton"
          onClick={() => onSend('light.effect', { name: effect, speed })}
          disabled={locked}
        >
          <Send size={16} />
          应用灯效
        </button>

        <div className="formMeta">
          <label className="switchLabel">
            定时开
            <input type="time" value={scheduleOn} onChange={(event) => setScheduleOn(event.target.value)} disabled={locked} />
          </label>
          <label className="switchLabel">
            定时关
            <input type="time" value={scheduleOff} onChange={(event) => setScheduleOff(event.target.value)} disabled={locked} />
          </label>
        </div>
        <button
          type="button"
          className="textButton"
          onClick={() => onSend('light.schedule', { on: scheduleOn, off: scheduleOff })}
          disabled={locked}
        >
          <Send size={16} />
          保存定时
        </button>
      </div>
    </section>
  );
}

function ClockPanel({
  device,
  busy,
  onSend,
}: {
  device: Device;
  busy: boolean;
  onSend: (type: string, payload: Record<string, unknown>) => void;
}) {
  const [mode, setMode] = useState('clock');
  const [brightness, setBrightness] = useState(80);
  const [lat, setLat] = useState(device.labels?.lat ?? '31.2304');
  const [lon, setLon] = useState(device.labels?.lon ?? '121.4737');
  const [tz, setTz] = useState((device.metadata?.tz as string) ?? 'Asia/Shanghai');
  const [preview, setPreview] = useState<ClockContent | null>(null);
  const [localError, setLocalError] = useState('');
  const [fetching, setFetching] = useState(false);
  const locked = device.disabled || busy;

  function pickMode(next: string) {
    setMode(next);
    onSend('display.mode', { mode: next });
  }

  async function refreshAndPush() {
    setLocalError('');
    setFetching(true);
    try {
      const content = await getClockContent(Number(lat), Number(lon), tz);
      setPreview(content);
      onSend('time.sync', { epoch: content.time.epoch, tz });
      if (content.weather) {
        onSend('weather.push', { temp: content.weather.tempC, cond: content.weather.text });
      }
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '获取天气失败');
    } finally {
      setFetching(false);
    }
  }

  return (
    <section className="panel commandPanel">
      <PanelTitle icon={<Clock size={18} />} title="时钟 / 天气屏" />
      <div className="formGrid">
        <div className="formMeta">
          {['clock', 'calendar', 'weather'].map((value) => (
            <button
              key={value}
              type="button"
              className={value === mode ? 'primaryButton' : 'textButton'}
              onClick={() => pickMode(value)}
              disabled={locked}
            >
              {value === 'clock' ? '时钟' : value === 'calendar' ? '日历' : '天气'}
            </button>
          ))}
        </div>

        <label>
          屏幕亮度 · {brightness}%
          <input
            type="range"
            min={0}
            max={100}
            value={brightness}
            onChange={(event) => setBrightness(Number(event.target.value))}
            onMouseUp={() => onSend('display.brightness', { value: brightness })}
            onTouchEnd={() => onSend('display.brightness', { value: brightness })}
            disabled={locked}
          />
        </label>

        <div className="formMeta">
          <label className="switchLabel">
            纬度
            <input value={lat} onChange={(event) => setLat(event.target.value)} disabled={locked} />
          </label>
          <label className="switchLabel">
            经度
            <input value={lon} onChange={(event) => setLon(event.target.value)} disabled={locked} />
          </label>
        </div>
        <label>
          时区 (IANA)
          <input value={tz} onChange={(event) => setTz(event.target.value)} disabled={locked} />
        </label>

        <button type="button" className="primaryButton" onClick={refreshAndPush} disabled={locked || fetching}>
          {fetching ? <Loader2 size={16} className="spin" /> : <CloudSun size={16} />}
          {fetching ? '获取中...' : '获取天气并推送到屏幕'}
        </button>

        {localError && <span className="fieldError">{localError}</span>}
        {preview && (
          <div className="formMeta">
            <span>
              {preview.weather
                ? `${preview.weather.text} ${preview.weather.tempC.toFixed(1)}°C · 湿度 ${preview.weather.humidity}%`
                : `天气不可用${preview.weatherError ? `（${preview.weatherError}）` : ''}`}
            </span>
            <span>{formatTime(preview.time.iso)}</span>
          </div>
        )}
      </div>
    </section>
  );
}

type ScreenPt = { x: number; y: number };
type MapGeo = {
  empty: boolean;
  points: ScreenPt[];
  polyline: string;
  last: ScreenPt | null;
  fenceCircle: { cx: number; cy: number; r: number } | null;
};

// buildMap projects track points + geofence into an SVG viewBox using a simple
// equirectangular projection (x scaled by cos(lat)), so the geofence renders as
// a true circle. No external map library needed.
function buildMap(track: TrackPoint[], fence?: Geofence): MapGeo {
  const W = 320;
  const H = 220;
  const pad = 18;
  const blank: MapGeo = { empty: true, points: [], polyline: '', last: null, fenceCircle: null };
  if (track.length === 0 && !fence) return blank;

  const refLat = fence?.centerLat ?? track[0]?.lat ?? 0;
  const cosMid = Math.cos((refLat * Math.PI) / 180) || 1;
  const proj = (lat: number, lng: number): ScreenPt => ({ x: lng * cosMid, y: lat });

  const projected = track.map((p) => proj(p.lat, p.lng));
  const bounds = [...projected];
  const rDeg = fence ? fence.radiusM / 111320 : 0;
  if (fence) {
    const c = proj(fence.centerLat, fence.centerLng);
    bounds.push({ x: c.x - rDeg, y: c.y - rDeg }, { x: c.x + rDeg, y: c.y + rDeg });
  }

  const minX = Math.min(...bounds.map((p) => p.x));
  const maxX = Math.max(...bounds.map((p) => p.x));
  const minY = Math.min(...bounds.map((p) => p.y));
  const maxY = Math.max(...bounds.map((p) => p.y));
  const spanX = maxX - minX || 1e-6;
  const spanY = maxY - minY || 1e-6;
  const scale = Math.min((W - 2 * pad) / spanX, (H - 2 * pad) / spanY);
  const offX = (W - spanX * scale) / 2;
  const offY = (H - spanY * scale) / 2;
  const toScreen = (p: ScreenPt): ScreenPt => ({ x: offX + (p.x - minX) * scale, y: H - (offY + (p.y - minY) * scale) });

  const screenPts = projected.map(toScreen);
  let fenceCircle: MapGeo['fenceCircle'] = null;
  if (fence) {
    const c = toScreen(proj(fence.centerLat, fence.centerLng));
    fenceCircle = { cx: c.x, cy: c.y, r: rDeg * scale };
  }
  return {
    empty: false,
    points: screenPts,
    polyline: screenPts.length > 1 ? screenPts.map((p) => `${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(' ') : '',
    last: screenPts.length ? screenPts[screenPts.length - 1] : null,
    fenceCircle,
  };
}

function GpsPanel({
  device,
  busy,
  onSend,
}: {
  device: Device;
  busy: boolean;
  onSend: (type: string, payload: Record<string, unknown>) => void;
}) {
  const [track, setTrack] = useState<TrackPoint[]>([]);
  const [intervalSec, setIntervalSec] = useState(10);
  const [centerLat, setCenterLat] = useState(device.geofence?.centerLat?.toString() ?? '');
  const [centerLng, setCenterLng] = useState(device.geofence?.centerLng?.toString() ?? '');
  const [radiusM, setRadiusM] = useState(device.geofence?.radiusM?.toString() ?? '300');
  const [localError, setLocalError] = useState('');
  const [savingFence, setSavingFence] = useState(false);
  const locked = device.disabled || busy;

  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const response = await getTrack(device.id, 200);
        if (alive) setTrack(response.items);
      } catch {
        /* track unavailable; keep last */
      }
    }
    load();
    const timer = window.setInterval(load, 8000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [device.id]);

  const latest = track.length ? track[track.length - 1] : null;
  const geo = useMemo(() => buildMap(track, device.geofence), [track, device.geofence]);

  async function saveFence() {
    setLocalError('');
    const lat = Number(centerLat);
    const lng = Number(centerLng);
    const r = Number(radiusM);
    if (!centerLat || !centerLng || !(r > 0)) {
      setLocalError('请填写中心经纬度和半径(米)');
      return;
    }
    setSavingFence(true);
    try {
      await setGeofence(device.id, { centerLat: lat, centerLng: lng, radiusM: r });
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '设置围栏失败');
    } finally {
      setSavingFence(false);
    }
  }

  function useLatestAsCenter() {
    if (latest) {
      setCenterLat(latest.lat.toFixed(6));
      setCenterLng(latest.lng.toFixed(6));
    }
  }

  return (
    <section className="panel commandPanel">
      <PanelTitle icon={<MapPin size={18} />} title="GPS 定位 / 轨迹" />
      <div className="formGrid">
        <svg viewBox="0 0 320 220" style={{ width: '100%', background: '#0e1622', borderRadius: 8 }}>
          {geo.fenceCircle && (
            <circle
              cx={geo.fenceCircle.cx}
              cy={geo.fenceCircle.cy}
              r={geo.fenceCircle.r}
              fill="rgba(20,100,244,0.15)"
              stroke="#1464f4"
              strokeDasharray="4 3"
            />
          )}
          {geo.polyline && <polyline points={geo.polyline} fill="none" stroke="#3ad1a8" strokeWidth={2} />}
          {geo.points.map((p, index) => (
            <circle key={index} cx={p.x} cy={p.y} r={1.6} fill="#3ad1a8" opacity={0.6} />
          ))}
          {geo.last && <circle cx={geo.last.x} cy={geo.last.y} r={4} fill="#ffd9a0" stroke="#1c2331" />}
          {geo.empty && (
            <text x={160} y={110} fill="#5f6b7d" fontSize={12} textAnchor="middle">
              暂无轨迹
            </text>
          )}
        </svg>

        <div className="formMeta">
          <span>{latest ? `位置 ${latest.lat.toFixed(5)}, ${latest.lng.toFixed(5)}` : '等待定位...'}</span>
          <span className={device.geofenceState === 'outside' ? 'fieldError' : ''}>围栏 {device.geofenceState || '未知'}</span>
        </div>

        <label>
          上报频率 · {intervalSec}s
          <input
            type="range"
            min={1}
            max={60}
            value={intervalSec}
            onChange={(event) => setIntervalSec(Number(event.target.value))}
            onMouseUp={() => onSend('gps.interval', { seconds: intervalSec })}
            onTouchEnd={() => onSend('gps.interval', { seconds: intervalSec })}
            disabled={locked}
          />
        </label>

        <div className="formMeta">
          <label className="switchLabel">
            中心纬度
            <input value={centerLat} onChange={(event) => setCenterLat(event.target.value)} disabled={locked} />
          </label>
          <label className="switchLabel">
            中心经度
            <input value={centerLng} onChange={(event) => setCenterLng(event.target.value)} disabled={locked} />
          </label>
        </div>
        <label>
          围栏半径 (米)
          <input value={radiusM} onChange={(event) => setRadiusM(event.target.value)} disabled={locked} />
        </label>
        <div className="formMeta">
          <button type="button" className="textButton" onClick={useLatestAsCenter} disabled={locked || !latest}>
            用当前位置
          </button>
          <button type="button" className="primaryButton" onClick={saveFence} disabled={locked || savingFence}>
            {savingFence ? <Loader2 size={16} className="spin" /> : <MapPin size={16} />}
            保存围栏
          </button>
        </div>
        {localError && <span className="fieldError">{localError}</span>}
      </div>
    </section>
  );
}

function VoicePanel({
  device,
  busy,
  onSend,
}: {
  device: Device;
  busy: boolean;
  onSend: (type: string, payload: Record<string, unknown>) => void;
}) {
  const [connected, setConnected] = useState(false);
  const [text, setText] = useState('晚安');
  const [wake, setWake] = useState(true);
  const [localError, setLocalError] = useState('');
  const [sending, setSending] = useState(false);
  const locked = device.disabled || busy;

  useEffect(() => {
    let alive = true;
    async function load() {
      try {
        const status = await getRealtimeStatus(device.id);
        if (alive) setConnected(status.connected);
      } catch {
        /* status unavailable */
      }
    }
    load();
    const timer = window.setInterval(load, 5000);
    return () => {
      alive = false;
      window.clearInterval(timer);
    };
  }, [device.id]);

  async function say() {
    setLocalError('');
    setSending(true);
    try {
      await sayToDevice(device.id, text);
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '推送失败');
    } finally {
      setSending(false);
    }
  }

  function toggleWake() {
    const next = !wake;
    setWake(next);
    onSend('voice.wake', { enabled: next });
  }

  return (
    <section className="panel commandPanel">
      <PanelTitle icon={<Mic size={18} />} title="小智 / 语音" />
      <div className="formGrid">
        <div className="formMeta">
          <span className={connected ? '' : 'fieldError'}>
            实时通道 {connected ? '已连接' : '未连接'}
          </span>
          <span>WebSocket {`/api/v1/devices/${device.id}/ws`}</span>
        </div>

        <label>
          让设备播报 (tts.say)
          <input value={text} onChange={(event) => setText(event.target.value)} disabled={locked} />
        </label>
        <button type="button" className="primaryButton" onClick={say} disabled={locked || sending || !connected}>
          {sending ? <Loader2 size={16} className="spin" /> : <Mic size={16} />}
          {connected ? '推送播报' : '设备未连接'}
        </button>

        <div className="formMeta">
          <label className="switchLabel">
            <input type="checkbox" checked={wake} onChange={toggleWake} disabled={locked} />
            <span>唤醒 (voice.wake)</span>
          </label>
        </div>

        {localError && <span className="fieldError">{localError}</span>}
        <span className="fieldHint">
          占位语音管线：实时通道与 hello/text.input/tts.say 协议已通；真实 ASR/TTS/LLM 与设备端音频固件为后续单独迭代。
        </span>
      </div>
    </section>
  );
}

function OtaPanel({ device, onChanged }: { device: Device; onChanged: () => void }) {
  const [firmwares, setFirmwares] = useState<Firmware[]>([]);
  const [version, setVersion] = useState('');
  const [file, setFile] = useState<File | null>(null);
  const [busy, setBusy] = useState('');
  const [localError, setLocalError] = useState('');

  async function reload() {
    try {
      const res = await listFirmware(device.category || undefined);
      setFirmwares(res.items);
    } catch {
      /* ignore */
    }
  }
  useEffect(() => {
    reload();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [device.category]);

  const upToDate = Boolean(device.fwVersion && device.targetFwVersion && device.fwVersion === device.targetFwVersion);

  async function doUpload() {
    if (!file || !version.trim() || !device.category) {
      setLocalError('需要选择固件文件并填写版本号');
      return;
    }
    setLocalError('');
    setBusy('upload');
    try {
      await uploadFirmware({ category: device.category, model: device.model, version: version.trim() }, file);
      setVersion('');
      setFile(null);
      await reload();
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '上传失败');
    } finally {
      setBusy('');
    }
  }

  async function applyTarget(v: string) {
    setLocalError('');
    setBusy('target');
    try {
      await setOtaTarget(device.id, v);
      onChanged();
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '设置目标失败');
    } finally {
      setBusy('');
    }
  }

  async function doRollout(id: string) {
    setLocalError('');
    setBusy('rollout');
    try {
      await rolloutFirmware(id);
      onChanged();
    } catch (err) {
      setLocalError(err instanceof Error ? err.message : '灰度失败');
    } finally {
      setBusy('');
    }
  }

  return (
    <section className="panel">
      <PanelTitle icon={<UploadCloud size={18} />} title="固件 / OTA" />
      <div className="formGrid">
        <div className="formMeta">
          <span>当前版本 {device.fwVersion || '未知'}</span>
          <span className={device.targetFwVersion && !upToDate ? 'fieldError' : ''}>
            目标 {device.targetFwVersion || '未设置'}
            {device.targetFwVersion ? (upToDate ? '（已最新）' : '（待更新）') : ''}
          </span>
        </div>

        {firmwares.length === 0 && <span className="fieldHint">该类别（{device.category}）暂无固件，先上传。</span>}
        {firmwares.map((fw) => (
          <div key={fw.id} className="formMeta">
            <span>
              {fw.version} · {(fw.size / 1024).toFixed(0)}KB · {fw.sha256.slice(0, 8)}
            </span>
            <span>
              <button type="button" className="linkButton" disabled={Boolean(busy)} onClick={() => applyTarget(fw.version)}>
                设为目标
              </button>
              {' · '}
              <button type="button" className="linkButton" disabled={Boolean(busy)} onClick={() => doRollout(fw.id)}>
                灰度本类
              </button>
            </span>
          </div>
        ))}

        <label>
          新版本号
          <input value={version} onChange={(event) => setVersion(event.target.value)} placeholder="如 1.2.0" />
        </label>
        <input type="file" onChange={(event) => setFile(event.target.files?.[0] ?? null)} />
        <button type="button" className="primaryButton" onClick={doUpload} disabled={busy === 'upload'}>
          {busy === 'upload' ? <Loader2 size={16} className="spin" /> : <UploadCloud size={16} />}
          上传固件（{device.category}）
        </button>
        {localError && <span className="fieldError">{localError}</span>}
      </div>
    </section>
  );
}

const RULE_CATEGORIES = ['', 'light', 'clock', 'gps', 'voice'];

function ruleSummary(rule: Rule): string {
  const t = rule.trigger;
  const trigger =
    t.type === 'telemetry'
      ? `${t.key} ${t.op} ${t.value}${t.category ? ` [${t.category}]` : ''}`
      : `事件 ${t.eventType}${t.category ? ` [${t.category}]` : ''}`;
  const a = rule.action;
  const action =
    a.type === 'command'
      ? `→ ${a.commandType}${a.targetCategory ? ` @${a.targetCategory}` : a.targetDeviceId ? ` @${a.targetDeviceId}` : ' @触发设备'}`
      : `→ webhook`;
  return `${trigger} ${action}`;
}

function AutomationPanel() {
  const [rules, setRules] = useState<Rule[]>([]);
  const [name, setName] = useState('');
  const [triggerType, setTriggerType] = useState<'telemetry' | 'event'>('telemetry');
  const [tKey, setTKey] = useState('env.temp');
  const [tOp, setTOp] = useState<'gt' | 'gte' | 'lt' | 'lte' | 'eq' | 'ne'>('gt');
  const [tValue, setTValue] = useState('30');
  const [tEventType, setTEventType] = useState('geofence.exit');
  const [tCategory, setTCategory] = useState('');
  const [actionType, setActionType] = useState<'command' | 'webhook'>('command');
  const [aTargetCategory, setATargetCategory] = useState('');
  const [aCommandType, setACommandType] = useState('light.power');
  const [aPayload, setAPayload] = useState('{"on":true}');
  const [aWebhookUrl, setAWebhookUrl] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  async function reload() {
    try {
      setRules((await listRules()).items);
    } catch {
      /* ignore */
    }
  }
  useEffect(() => {
    reload();
  }, []);

  async function create() {
    setError('');
    if (!name.trim()) {
      setError('规则需要名称');
      return;
    }
    const trigger: RuleTrigger =
      triggerType === 'telemetry'
        ? { type: 'telemetry', key: tKey.trim(), op: tOp, value: Number(tValue), category: tCategory || undefined }
        : { type: 'event', eventType: tEventType.trim(), category: tCategory || undefined };
    let action: RuleAction;
    if (actionType === 'command') {
      let payload: Record<string, unknown> | undefined;
      try {
        payload = aPayload.trim() ? JSON.parse(aPayload) : undefined;
      } catch {
        setError('命令 payload JSON 无效');
        return;
      }
      action = { type: 'command', targetCategory: aTargetCategory || undefined, commandType: aCommandType.trim(), payload };
    } else {
      if (!aWebhookUrl.trim()) {
        setError('Webhook 需要 URL');
        return;
      }
      action = { type: 'webhook', webhookUrl: aWebhookUrl.trim() };
    }
    setBusy(true);
    try {
      await createRule({ name: name.trim(), enabled: true, trigger, action });
      setName('');
      await reload();
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败');
    } finally {
      setBusy(false);
    }
  }

  async function toggle(rule: Rule) {
    await setRuleEnabled(rule.id, !rule.enabled);
    await reload();
  }
  async function remove(rule: Rule) {
    await deleteRule(rule.id);
    await reload();
  }

  return (
    <section className="panel">
      <PanelTitle icon={<Zap size={18} />} title="自动化规则" />
      <div className="formGrid">
        {rules.length === 0 && <span className="fieldHint">还没有规则。比如「env.temp &gt; 30 → 给 light 类下发 light.power」。</span>}
        {rules.map((rule) => (
          <div key={rule.id} className="formMeta">
            <label className="switchLabel">
              <input type="checkbox" checked={rule.enabled} onChange={() => toggle(rule)} />
              <strong>{rule.name}</strong>
            </label>
            <span>{ruleSummary(rule)}</span>
            <button type="button" className="linkButton" onClick={() => remove(rule)}>
              删除
            </button>
          </div>
        ))}

        <label>
          规则名称
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="如 高温自动开灯" />
        </label>

        <div className="formMeta">
          <label className="switchLabel">
            触发
            <select value={triggerType} onChange={(e) => setTriggerType(e.target.value as 'telemetry' | 'event')}>
              <option value="telemetry">遥测阈值</option>
              <option value="event">事件</option>
            </select>
          </label>
          {triggerType === 'telemetry' ? (
            <>
              <input value={tKey} onChange={(e) => setTKey(e.target.value)} placeholder="遥测 key" style={{ width: 110 }} />
              <select value={tOp} onChange={(e) => setTOp(e.target.value as typeof tOp)}>
                {['gt', 'gte', 'lt', 'lte', 'eq', 'ne'].map((o) => (
                  <option key={o} value={o}>
                    {o}
                  </option>
                ))}
              </select>
              <input value={tValue} onChange={(e) => setTValue(e.target.value)} style={{ width: 70 }} />
            </>
          ) : (
            <input value={tEventType} onChange={(e) => setTEventType(e.target.value)} placeholder="事件类型" style={{ width: 160 }} />
          )}
          <select value={tCategory} onChange={(e) => setTCategory(e.target.value)}>
            {RULE_CATEGORIES.map((c) => (
              <option key={c} value={c}>
                {c === '' ? '任意类别' : c}
              </option>
            ))}
          </select>
        </div>

        <div className="formMeta">
          <label className="switchLabel">
            动作
            <select value={actionType} onChange={(e) => setActionType(e.target.value as 'command' | 'webhook')}>
              <option value="command">下发命令</option>
              <option value="webhook">Webhook</option>
            </select>
          </label>
          {actionType === 'command' ? (
            <>
              <select value={aTargetCategory} onChange={(e) => setATargetCategory(e.target.value)}>
                <option value="">目标=触发设备</option>
                {RULE_CATEGORIES.filter((c) => c).map((c) => (
                  <option key={c} value={c}>
                    广播 {c}
                  </option>
                ))}
              </select>
              <input value={aCommandType} onChange={(e) => setACommandType(e.target.value)} placeholder="命令 type" style={{ width: 120 }} />
              <input value={aPayload} onChange={(e) => setAPayload(e.target.value)} placeholder="payload JSON" style={{ width: 150 }} />
            </>
          ) : (
            <input value={aWebhookUrl} onChange={(e) => setAWebhookUrl(e.target.value)} placeholder="https://..." style={{ width: 280 }} />
          )}
        </div>

        <button type="button" className="primaryButton" onClick={create} disabled={busy}>
          {busy ? <Loader2 size={16} className="spin" /> : <Zap size={16} />}
          新建规则
        </button>
        {error && <span className="fieldError">{error}</span>}
      </div>
    </section>
  );
}

function FleetDashboard({ stats }: { stats: FleetStats }) {
  const stat = (label: string, value: number, tone = '') => (
    <span className={tone}>
      {label} <strong>{value}</strong>
    </span>
  );
  return (
    <section className="panel">
      <PanelTitle icon={<Gauge size={18} />} title="舰队概览" />
      <div className="formGrid">
        <div className="formMeta" style={{ flexWrap: 'wrap', gap: 14 }}>
          {stat('在线', stats.devicesByState.online ?? 0)}
          {stat('弱网', stats.devicesByState.stale ?? 0)}
          {stat('离线', stats.devicesByState.offline ?? 0, (stats.devicesByState.offline ?? 0) > 0 ? 'fieldError' : '')}
          {stat('禁用', stats.disabled)}
          {stat('实时连接', stats.realtimeConnections)}
        </div>
        <div className="formMeta" style={{ flexWrap: 'wrap', gap: 14 }}>
          {Object.entries(stats.devicesByCategory).map(([k, v]) => (
            <span key={k}>
              {k} <strong>{v}</strong>
            </span>
          ))}
        </div>
        <div className="formMeta" style={{ flexWrap: 'wrap', gap: 14 }}>
          {stat('遥测累计', stats.telemetryReceived)}
          {stat('命令', stats.commandsCreated)}
          {stat('事件', stats.events)}
          {stat('固件', stats.firmware)}
        </div>
      </div>
    </section>
  );
}

function buildSparkline(series: TelemetryBucket[]): { line: string; lastX: number; lastY: number; lo: number; hi: number } {
  if (series.length === 0) return { line: '', lastX: 0, lastY: 0, lo: 0, hi: 0 };
  const W = 320;
  const H = 120;
  const pad = 10;
  const lo = Math.min(...series.map((b) => b.min));
  const hi = Math.max(...series.map((b) => b.max));
  const span = hi - lo || 1;
  const n = series.length;
  const x = (i: number) => pad + (n === 1 ? 0 : (i * (W - 2 * pad)) / (n - 1));
  const y = (v: number) => H - pad - ((v - lo) / span) * (H - 2 * pad);
  const line = series.map((b, i) => `${x(i).toFixed(1)},${y(b.avg).toFixed(1)}`).join(' ');
  return { line, lastX: x(n - 1), lastY: y(series[n - 1].avg), lo, hi };
}

const RANGE_SECONDS: Record<string, number> = { '1h': 3600, '24h': 86400, '7d': 604800, '30d': 2592000 };
function resolutionLabel(res: number): string {
  if (res >= 86400) return '1d';
  if (res >= 3600) return '1h';
  return '1m';
}

function TelemetryChart({ device, numericKeys }: { device: Device; numericKeys: string[] }) {
  const [key, setKey] = useState(numericKeys[0] ?? '');
  const [range, setRange] = useState<'live' | '1h' | '24h' | '7d' | '30d'>('live');
  const [series, setSeries] = useState<TelemetryBucket[]>([]);
  const [resolution, setResolution] = useState(60);

  useEffect(() => {
    if (!numericKeys.includes(key)) setKey(numericKeys[0] ?? '');
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [numericKeys]);

  useEffect(() => {
    let alive = true;
    if (!key) {
      setSeries([]);
      return;
    }
    const load =
      range === 'live'
        ? getTelemetrySeries(device.id, key, 60, 120).then((res) => ({ items: res.items, resolution: 60 }))
        : (() => {
            const to = Math.floor(Date.now() / 1000);
            return getTelemetryHistory(device.id, key, to - RANGE_SECONDS[range], to);
          })();
    Promise.resolve(load)
      .then((res) => {
        if (alive) {
          setSeries(res.items);
          setResolution(res.resolution);
        }
      })
      .catch(() => {
        /* ignore */
      });
    return () => {
      alive = false;
    };
  }, [device.id, key, range]);

  const spark = useMemo(() => buildSparkline(series), [series]);

  return (
    <section className="panel">
      <PanelTitle icon={<LineChart size={18} />} title="遥测趋势" />
      <div className="formGrid">
        <div className="formMeta">
          <label className="switchLabel">
            指标
            <select value={key} onChange={(event) => setKey(event.target.value)}>
              {numericKeys.map((k) => (
                <option key={k} value={k}>
                  {k}
                </option>
              ))}
            </select>
          </label>
          <label className="switchLabel">
            范围
            <select value={range} onChange={(event) => setRange(event.target.value as typeof range)}>
              <option value="live">实时</option>
              <option value="1h">1 小时</option>
              <option value="24h">24 小时</option>
              <option value="7d">7 天</option>
              <option value="30d">30 天</option>
            </select>
          </label>
        </div>
        <svg viewBox="0 0 320 120" style={{ width: '100%', background: '#0e1622', borderRadius: 8 }}>
          {spark.line ? (
            <>
              <polyline points={spark.line} fill="none" stroke="#3ad1a8" strokeWidth={2} />
              <circle cx={spark.lastX} cy={spark.lastY} r={3} fill="#ffd9a0" />
            </>
          ) : (
            <text x={160} y={60} fill="#5f6b7d" fontSize={12} textAnchor="middle">
              暂无数据
            </text>
          )}
        </svg>
        <div className="formMeta">
          <span>
            {series.length} 桶 · 分辨率 {resolutionLabel(resolution)}
            {range === 'live' ? '（实时 raw）' : '（rollup）'}
          </span>
          <span>范围 {spark.lo.toFixed(1)} ~ {spark.hi.toFixed(1)}</span>
        </div>
      </div>
    </section>
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
