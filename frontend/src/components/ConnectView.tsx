import React, { useEffect, useState } from "react";
import {
  Activity,
  AlertCircle,
  ArrowDown,
  ArrowUp,
  Check,
  Clock,
  Copy,
  FileUp,
  Globe,
  KeyRound,
  Network,
  Plus,
  Power,
  Server,
  Shield,
  Terminal,
} from "lucide-react";
import {
  AppService,
  type Profile,
  type StatusSnapshot,
  type SysproxyState,
  type TrafficStats,
} from "../mygo";
import { type Dict, fmt } from "../i18n";
import { formatBytes, formatDuration, formatRate } from "../utils/format";
import { TrafficSparkline, type TrafficSample } from "./TrafficSparkline";

interface ConnectViewProps {
  status: StatusSnapshot;
  traffic: TrafficStats;
  samples: TrafficSample[];
  profiles: Profile[];
  activeProfileId: string;
  sysproxy: SysproxyState;
  dict: Dict;
  onGoProfiles: () => void;
  onImportTomlFile: () => void;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

export const ConnectView: React.FC<ConnectViewProps> = ({
  status,
  traffic,
  samples,
  profiles,
  activeProfileId,
  sysproxy,
  dict,
  onGoProfiles,
  onImportTomlFile,
  onNotify,
}) => {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [copiedKey, setCopiedKey] = useState<string>("");
  const [busyAction, setBusyAction] = useState(false);

  const isConnected = status.state === "connected";
  const isBusy =
    status.state === "connecting" ||
    status.state === "authenticating" ||
    status.state === "reconnecting" ||
    status.state === "disconnecting";
  const isError = status.state === "error";

  useEffect(() => {
    if (!isConnected) return;
    setNowMs(Date.now());
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [isConnected]);

  const activeProfile =
    profiles.find((p) => p.id === activeProfileId) || profiles[0] || null;

  const socksDisplay =
    status.socksAddr || activeProfile?.socksBind || "127.0.0.1:1080";
  const httpDisplay =
    status.httpAddr || activeProfile?.httpBind || "127.0.0.1:1081";

  const handleToggleConnect = async () => {
    if (busyAction) return;
    setBusyAction(true);
    try {
      if (isConnected || isBusy) {
        await AppService.disconnect();
      } else {
        if (!activeProfile) {
          onGoProfiles();
          return;
        }
        await AppService.connect(activeProfile.id);
      }
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setBusyAction(false);
    }
  };

  const handleProfileSelect = async (id: string) => {
    try {
      await AppService.setActiveProfile(id);
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const handleToggleSysproxy = async () => {
    if (!sysproxy.supported || !isConnected) return;
    try {
      await AppService.setSystemProxy(!sysproxy.enabled);
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const handleCopy = async (key: string, value: string) => {
    try {
      await AppService.copyText(value);
      setCopiedKey(key);
      onNotify(`${dict.common.copied}: ${value}`, "success");
      window.setTimeout(() => {
        setCopiedKey((prev) => (prev === key ? "" : prev));
      }, 1600);
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const visualStateClass = isConnected
    ? "connected"
    : isBusy
      ? "busy"
      : isError
        ? "error"
        : "";

  const powerTooltip = isConnected
    ? dict.connect.clickToDisconnect
    : isBusy
      ? dict.connect.clickToCancel
      : dict.connect.clickToConnect;

  return (
    <div className="page-stack">
      {/* Hero Connection Card */}
      <div className="card hero-card">
        <div className={`power-btn-wrap ${visualStateClass}`}>
          <div className="power-ring" />
          <button
            type="button"
            className={`power-btn ${visualStateClass}`}
            title={powerTooltip}
            disabled={busyAction || (!isConnected && !isBusy && !activeProfile)}
            onClick={handleToggleConnect}
          >
            <Power size={34} strokeWidth={2.2} />
          </button>
        </div>

        <div className="hero-info">
          <div className="hero-status-line">
            <h2 className="hero-state-text">{dict.state[status.state]}</h2>
            {isConnected && (
              <span className="badge badge-success mono">
                <Clock size={12} />
                {formatDuration(status.connectedAt, nowMs)}
              </span>
            )}
            {status.state === "reconnecting" && (status.reconnectAttempt ?? 0) > 0 && (
              <span className="badge badge-warning">
                {fmt(dict.connect.reconnectAttempt, {
                  n: status.reconnectAttempt || 1,
                })}
              </span>
            )}
            {status.activeChallenge && (
              <span className="badge badge-warning">
                <KeyRound size={12} />
                {dict.connect.openAuth}
              </span>
            )}
          </div>

          {profiles.length === 0 ? (
            <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
              <div style={{ color: "var(--text-secondary)", fontSize: 13 }}>
                {dict.connect.noProfileDesc}
              </div>
              <div style={{ display: "flex", gap: 8 }}>
                <button
                  type="button"
                  className="btn btn-primary"
                  onClick={onGoProfiles}
                >
                  <Plus size={15} />
                  {dict.connect.createProfile}
                </button>
                <button type="button" className="btn" onClick={onImportTomlFile}>
                  <FileUp size={15} />
                  {dict.connect.importToml}
                </button>
              </div>
            </div>
          ) : (
            <>
              <div className="hero-profile-bar">
                <select
                  className="select"
                  style={{ maxWidth: 320 }}
                  disabled={isConnected || isBusy}
                  value={activeProfile?.id || ""}
                  onChange={(e) => handleProfileSelect(e.target.value)}
                >
                  {profiles.map((p) => (
                    <option key={p.id} value={p.id}>
                      {p.name} ({p.protocol === "atrust" ? "aTrust" : "EasyConnect"} ·{" "}
                      {p.serverAddress}:{p.serverPort})
                    </option>
                  ))}
                </select>
                <button
                  type="button"
                  className={`btn ${isConnected || isBusy ? "btn-danger" : "btn-primary"}`}
                  disabled={busyAction}
                  onClick={handleToggleConnect}
                >
                  {isConnected
                    ? dict.connect.clickToDisconnect
                    : isBusy
                      ? dict.connect.clickToCancel
                      : dict.connect.clickToConnect}
                </button>
              </div>

              <div className="hero-meta-row">
                <span className="hero-meta-chip">
                  <Shield size={13} />
                  {dict.connect.protocol}:{" "}
                  <strong>
                    {(status.protocol || activeProfile?.protocol) === "atrust"
                      ? "aTrust"
                      : "EasyConnect"}
                  </strong>
                </span>
                <span className="hero-meta-chip mono">
                  <Server size={13} />
                  {status.server ||
                    (activeProfile
                      ? `${activeProfile.serverAddress}:${activeProfile.serverPort}`
                      : "—")}
                </span>
                {status.virtualIp && (
                  <span className="hero-meta-chip mono">
                    <Network size={13} />
                    {dict.connect.virtualIp}: <strong>{status.virtualIp}</strong>
                  </span>
                )}
                {activeProfile?.tunMode && (
                  <span className="badge badge-success">Split-TUN</span>
                )}
              </div>
            </>
          )}
        </div>
      </div>

      {/* Error Banner */}
      {status.lastError && (
        <div className="alert-banner alert-danger">
          <AlertCircle size={16} style={{ flexShrink: 0, marginTop: 1 }} />
          <div className="selectable" style={{ flex: 1 }}>
            {status.lastError}
          </div>
        </div>
      )}

      {/* System Proxy + Local Proxy Endpoints */}
      <div className="grid-2">
        <div className="card" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between", gap: 14 }}>
          <div>
            <div className="card-header" style={{ marginBottom: 8 }}>
              <h3 className="card-title">
                <Globe size={16} color="var(--accent)" />
                {dict.connect.sysproxyTitle}
              </h3>
              <button
                type="button"
                role="switch"
                aria-checked={sysproxy.enabled}
                className={`switch success ${sysproxy.enabled ? "checked" : ""}`}
                disabled={!sysproxy.supported || !isConnected}
                onClick={handleToggleSysproxy}
              >
                <span className="switch-thumb" />
              </button>
            </div>
            <p className="card-subtitle">{dict.connect.sysproxyDesc}</p>
          </div>

          <div>
            {!sysproxy.supported ? (
              <div className="alert-banner alert-warning">
                <AlertCircle size={15} style={{ flexShrink: 0, marginTop: 1 }} />
                <span>{sysproxy.message || dict.connect.sysproxyUnsupported}</span>
              </div>
            ) : !isConnected ? (
              <span className="badge badge-neutral">
                {dict.connect.sysproxyNeedConnect}
              </span>
            ) : (
              <span
                className={`badge ${sysproxy.enabled ? "badge-success" : "badge-neutral"}`}
              >
                {sysproxy.enabled ? dict.common.enabled : dict.common.disabled}
              </span>
            )}
          </div>
        </div>

        <div className="card">
          <div className="card-header" style={{ marginBottom: 10 }}>
            <div>
              <h3 className="card-title">
                <Network size={16} color="var(--accent)" />
                {dict.connect.localProxies}
              </h3>
              <p className="card-subtitle">{dict.connect.localProxiesDesc}</p>
            </div>
          </div>

          <div className="proxy-row">
            <div>
              <div style={{ fontSize: 11, color: "var(--text-muted)", fontWeight: 600 }}>
                SOCKS5
              </div>
              <div className="mono selectable" style={{ fontWeight: 600 }}>
                socks5://{socksDisplay}
              </div>
            </div>
            <button
              type="button"
              className="btn btn-sm"
              onClick={() => handleCopy("socks", `socks5://${socksDisplay}`)}
            >
              {copiedKey === "socks" ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === "socks" ? dict.common.copied : dict.common.copy}
            </button>
          </div>

          <div className="proxy-row">
            <div>
              <div style={{ fontSize: 11, color: "var(--text-muted)", fontWeight: 600 }}>
                HTTP / HTTPS
              </div>
              <div className="mono selectable" style={{ fontWeight: 600 }}>
                http://{httpDisplay}
              </div>
            </div>
            <button
              type="button"
              className="btn btn-sm"
              onClick={() => handleCopy("http", `http://${httpDisplay}`)}
            >
              {copiedKey === "http" ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === "http" ? dict.common.copied : dict.common.copy}
            </button>
          </div>

          <div style={{ marginTop: 10, display: "flex", justifyContent: "flex-end" }}>
            <button
              type="button"
              className="btn btn-sm btn-ghost"
              onClick={() =>
                handleCopy(
                  "env",
                  `export http_proxy=http://${httpDisplay} https_proxy=http://${httpDisplay} all_proxy=socks5://${socksDisplay}`,
                )
              }
            >
              <Terminal size={13} />
              {dict.connect.copyEnv}
            </button>
          </div>
        </div>
      </div>

      {/* Live Traffic Card */}
      <div className="card">
        <div className="card-header">
          <h3 className="card-title">
            <Activity size={16} color="var(--accent)" />
            {dict.connect.trafficTitle}
          </h3>
          <span className="badge badge-neutral mono">
            {dict.connect.activeConns}: {traffic.activeConns}
          </span>
        </div>

        <div className="grid-4" style={{ marginBottom: 14 }}>
          <div className="stat-tile">
            <span className="stat-label">
              <ArrowUp size={13} color="var(--accent)" />
              {dict.connect.upload}
            </span>
            <span className="stat-value mono">{formatRate(traffic.rateSent)}</span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">
              <ArrowDown size={13} color="var(--success)" />
              {dict.connect.download}
            </span>
            <span className="stat-value mono">{formatRate(traffic.rateRecv)}</span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">{dict.connect.totalSent}</span>
            <span className="stat-value mono">{formatBytes(traffic.bytesSent)}</span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">{dict.connect.totalRecv}</span>
            <span className="stat-value mono">{formatBytes(traffic.bytesRecv)}</span>
          </div>
        </div>

        <TrafficSparkline
          samples={samples}
          upLabel={dict.connect.upload}
          downLabel={dict.connect.download}
        />
      </div>
    </div>
  );
};
