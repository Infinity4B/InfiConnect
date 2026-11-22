import React, { useEffect, useMemo, useState } from "react";
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
  Layers,
  Network,
  Plus,
  Power,
  PowerOff,
  Server,
  Shield,
  Terminal,
} from "lucide-react";
import {
  AppService,
  type Profile,
  type State,
  type StatusSnapshot,
  type SysproxyState,
  type TrafficStats,
} from "../mygo";
import { type Dict, fmt } from "../i18n";
import { formatBytes, formatDuration, formatRate } from "../utils/format";
import { TrafficSparkline, type TrafficSample } from "./TrafficSparkline";

interface ConnectViewProps {
  profiles: Profile[];
  sessionsByProfile: Record<string, StatusSnapshot>;
  trafficByProfile: Record<string, TrafficStats>;
  samplesByProfile: Record<string, TrafficSample[]>;
  selectedProfileId: string;
  sysproxy: SysproxyState;
  dict: Dict;
  onSelectProfile: (profileId: string) => void;
  onGoProfiles: () => void;
  onImportTomlFile: () => void;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

const ZERO_TRAFFIC: TrafficStats = {
  bytesSent: 0,
  bytesRecv: 0,
  rateSent: 0,
  rateRecv: 0,
  activeConns: 0,
};

function isBusyState(st: State): boolean {
  return (
    st === "connecting" ||
    st === "authenticating" ||
    st === "reconnecting" ||
    st === "disconnecting"
  );
}

export const ConnectView: React.FC<ConnectViewProps> = ({
  profiles,
  sessionsByProfile,
  trafficByProfile,
  samplesByProfile,
  selectedProfileId,
  sysproxy,
  dict,
  onSelectProfile,
  onGoProfiles,
  onImportTomlFile,
  onNotify,
}) => {
  const [nowMs, setNowMs] = useState(() => Date.now());
  const [copiedKey, setCopiedKey] = useState<string>("");
  const [busyProfileIds, setBusyProfileIds] = useState<Record<string, boolean>>({});
  const [disconnectingAll, setDisconnectingAll] = useState(false);
  const [trafficScope, setTrafficScope] = useState<"current" | "all">("current");

  const sessionList = useMemo(
    () => Object.values(sessionsByProfile),
    [sessionsByProfile],
  );
  const connectedCount = useMemo(
    () => sessionList.filter((s) => s.state === "connected").length,
    [sessionList],
  );
  const activeCount = useMemo(
    () =>
      sessionList.filter((s) => s.state === "connected" || isBusyState(s.state))
        .length,
    [sessionList],
  );

  useEffect(() => {
    if (connectedCount === 0) return;
    setNowMs(Date.now());
    const id = window.setInterval(() => setNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [connectedCount]);

  // Reset traffic scope to current if fewer than 2 active connections
  useEffect(() => {
    if (connectedCount < 2 && trafficScope === "all") {
      setTrafficScope("current");
    }
  }, [connectedCount, trafficScope]);

  const selectedProfile =
    profiles.find((p) => p.id === selectedProfileId) || profiles[0] || null;

  const status: StatusSnapshot = useMemo(() => {
    if (selectedProfile && sessionsByProfile[selectedProfile.id]) {
      return sessionsByProfile[selectedProfile.id];
    }
    return {
      state: "idle",
      profileId: selectedProfile?.id,
      profileName: selectedProfile?.name,
      protocol: selectedProfile?.protocol,
      server: selectedProfile
        ? `${selectedProfile.serverAddress}:${selectedProfile.serverPort}`
        : undefined,
      traffic: ZERO_TRAFFIC,
    };
  }, [selectedProfile, sessionsByProfile]);

  const isConnected = status.state === "connected";
  const isBusy = isBusyState(status.state);
  const isError = status.state === "error";

  const selectedTraffic =
    (selectedProfile && trafficByProfile[selectedProfile.id]) ||
    status.traffic ||
    ZERO_TRAFFIC;

  const aggregateTraffic = useMemo<TrafficStats>(() => {
    let bytesSent = 0;
    let bytesRecv = 0;
    let rateSent = 0;
    let rateRecv = 0;
    let activeConns = 0;
    for (const st of sessionList) {
      if (st.state !== "connected") continue;
      const t = (st.profileId && trafficByProfile[st.profileId]) || st.traffic;
      if (!t) continue;
      bytesSent += t.bytesSent || 0;
      bytesRecv += t.bytesRecv || 0;
      rateSent += t.rateSent || 0;
      rateRecv += t.rateRecv || 0;
      activeConns += t.activeConns || 0;
    }
    return { bytesSent, bytesRecv, rateSent, rateRecv, activeConns };
  }, [sessionList, trafficByProfile]);

  const aggregateSamples = useMemo<TrafficSample[]>(() => {
    const lists = sessionList
      .filter((s) => s.state === "connected" && s.profileId)
      .map((s) => samplesByProfile[s.profileId!] || [])
      .filter((arr) => arr.length > 0);
    if (lists.length === 0) return [];
    const maxLen = Math.max(...lists.map((a) => a.length));
    const out: TrafficSample[] = [];
    for (let i = 0; i < maxLen; i++) {
      let rateSent = 0;
      let rateRecv = 0;
      for (const arr of lists) {
        const idx = arr.length - maxLen + i;
        if (idx >= 0) {
          rateSent += arr[idx].rateSent;
          rateRecv += arr[idx].rateRecv;
        }
      }
      out.push({ rateSent, rateRecv });
    }
    return out;
  }, [sessionList, samplesByProfile]);

  const displayedTraffic =
    trafficScope === "all" && connectedCount > 1
      ? aggregateTraffic
      : selectedTraffic;
  const displayedSamples =
    trafficScope === "all" && connectedCount > 1
      ? aggregateSamples
      : (selectedProfile && samplesByProfile[selectedProfile.id]) || [];

  const socksDisplay =
    status.socksAddr || selectedProfile?.socksBind || "127.0.0.1:1080";
  const httpDisplay =
    status.httpAddr || selectedProfile?.httpBind || "127.0.0.1:1081";

  const socksAutoAssigned =
    Boolean(status.socksAddr) &&
    Boolean(selectedProfile?.socksBind) &&
    status.socksAddr !== selectedProfile?.socksBind;
  const httpAutoAssigned =
    Boolean(status.httpAddr) &&
    Boolean(selectedProfile?.httpBind) &&
    status.httpAddr !== selectedProfile?.httpBind;

  const setProfileBusy = (id: string, val: boolean) => {
    setBusyProfileIds((prev) => {
      if (val) return { ...prev, [id]: true };
      const next = { ...prev };
      delete next[id];
      return next;
    });
  };

  const handleToggleProfile = async (prof: Profile) => {
    if (busyProfileIds[prof.id]) return;
    const st = sessionsByProfile[prof.id];
    const active = st && (st.state === "connected" || isBusyState(st.state));
    setProfileBusy(prof.id, true);
    try {
      if (active) {
        await AppService.disconnect(prof.id);
      } else {
        onSelectProfile(prof.id);
        await AppService.connect(prof.id);
      }
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setProfileBusy(prof.id, false);
    }
  };

  const handleDismissError = async (profId: string) => {
    if (busyProfileIds[profId]) return;
    setProfileBusy(profId, true);
    try {
      await AppService.disconnect(profId);
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setProfileBusy(profId, false);
    }
  };

  const handleDisconnectAll = async () => {
    if (disconnectingAll) return;
    setDisconnectingAll(true);
    try {
      await AppService.disconnectAll();
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setDisconnectingAll(false);
    }
  };

  const sysproxyBoundToSelected =
    sysproxy.enabled &&
    selectedProfile &&
    (sysproxy.profileId === selectedProfile.id ||
      (!sysproxy.profileId && isConnected));

  const sysproxyBoundToOther =
    sysproxy.enabled &&
    selectedProfile &&
    Boolean(sysproxy.profileId) &&
    sysproxy.profileId !== selectedProfile.id;

  const handleToggleSysproxy = async () => {
    if (!sysproxy.supported || !selectedProfile || !isConnected) return;
    try {
      if (sysproxyBoundToSelected) {
        await AppService.setSystemProxy(selectedProfile.id, false);
      } else {
        await AppService.setSystemProxy(selectedProfile.id, true);
      }
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
      {/* Multi-Connection Overview List */}
      {profiles.length > 0 && (
        <div className="card">
          <div className="card-header" style={{ marginBottom: 10 }}>
            <div style={{ display: "flex", alignItems: "center", gap: 8 }}>
              <h3 className="card-title">
                <Layers size={16} color="var(--accent)" />
                {dict.connect.connectionsTitle}
              </h3>
              {activeCount > 0 && (
                <span className="badge badge-success">
                  {fmt(dict.connect.activeCount, { n: activeCount })}
                </span>
              )}
            </div>
            {activeCount > 0 && (
              <button
                type="button"
                className="btn btn-sm btn-danger"
                disabled={disconnectingAll}
                onClick={handleDisconnectAll}
              >
                <PowerOff size={13} />
                {dict.connect.disconnectAll}
              </button>
            )}
          </div>

          <div className="conn-list">
            {profiles.map((p) => {
              const s = sessionsByProfile[p.id];
              const st: State = s?.state || "idle";
              const rowConnected = st === "connected";
              const rowBusy = isBusyState(st);
              const rowError = st === "error";
              const rowSelected = selectedProfile?.id === p.id;
              const rowTraffic = trafficByProfile[p.id] || s?.traffic;

              const dotCls = rowConnected
                ? "connected"
                : rowBusy
                  ? "busy"
                  : rowError
                    ? "error"
                    : "";

              return (
                <div
                  key={p.id}
                  className={`conn-row ${rowSelected ? "selected" : ""} ${rowConnected ? "connected" : ""}`}
                  onClick={() => onSelectProfile(p.id)}
                >
                  <div className="conn-row-left">
                    <span className={`status-dot ${dotCls}`} />
                    <div className="conn-row-info">
                      <div className="conn-row-title">
                        <span className="conn-row-name">{p.name}</span>
                        <span
                          className={`badge ${p.protocol === "atrust" ? "badge-accent" : "badge-neutral"}`}
                        >
                          {p.protocol === "atrust" ? "aTrust" : "EasyConnect"}
                        </span>
                        {p.tunMode && (
                          <span className="badge badge-success">Split-TUN</span>
                        )}
                        {rowConnected && (
                          <span className="badge badge-success mono">
                            <Clock size={11} />
                            {formatDuration(s?.connectedAt, nowMs)}
                          </span>
                        )}
                        {rowBusy && (
                          <span className="badge badge-warning">
                            {dict.state[st]}
                          </span>
                        )}
                        {rowError && (
                          <span className="badge badge-danger">
                            {dict.state.error}
                          </span>
                        )}
                        {sysproxy.enabled && sysproxy.profileId === p.id && (
                          <span className="badge badge-accent">
                            {dict.connect.sysproxyTitle}
                          </span>
                        )}
                      </div>
                      <div className="conn-row-meta mono">
                        <span>
                          {p.serverAddress}:{p.serverPort}
                        </span>
                        {s?.virtualIp && <span>IP {s.virtualIp}</span>}
                        {rowConnected && s?.socksAddr && (
                          <span>SOCKS5 {s.socksAddr}</span>
                        )}
                        {rowConnected && s?.httpAddr && (
                          <span>HTTP {s.httpAddr}</span>
                        )}
                        {rowConnected && rowTraffic && (
                          <span>
                            ↑{formatRate(rowTraffic.rateSent)} ↓
                            {formatRate(rowTraffic.rateRecv)}
                          </span>
                        )}
                      </div>
                    </div>
                  </div>

                  <div
                    className="conn-row-actions"
                    onClick={(e) => e.stopPropagation()}
                  >
                    <button
                      type="button"
                      className={`btn btn-sm ${rowConnected || rowBusy ? "btn-danger" : "btn-primary"}`}
                      disabled={!!busyProfileIds[p.id]}
                      onClick={() => handleToggleProfile(p)}
                    >
                      <Power size={13} />
                      {rowConnected
                        ? dict.connect.disconnect
                        : rowBusy
                          ? dict.connect.cancel
                          : dict.connect.connect}
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Selected Connection Hero Card */}
      <div className="card hero-card">
        <div className={`power-btn-wrap ${visualStateClass}`}>
          <div className="power-ring" />
          <button
            type="button"
            className={`power-btn ${visualStateClass}`}
            title={powerTooltip}
            disabled={
              !selectedProfile ||
              !!busyProfileIds[selectedProfile.id]
            }
            onClick={() => selectedProfile && handleToggleProfile(selectedProfile)}
          >
            <Power size={34} strokeWidth={2.2} />
          </button>
        </div>

        <div className="hero-info">
          <div className="hero-status-line">
            <h2 className="hero-state-text">
              {selectedProfile
                ? `${selectedProfile.name} · ${dict.state[status.state]}`
                : dict.state[status.state]}
            </h2>
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
                <button
                  type="button"
                  className={`btn ${isConnected || isBusy ? "btn-danger" : "btn-primary"}`}
                  disabled={
                    !selectedProfile || !!busyProfileIds[selectedProfile.id]
                  }
                  onClick={() =>
                    selectedProfile && handleToggleProfile(selectedProfile)
                  }
                >
                  {isConnected
                    ? dict.connect.clickToDisconnect
                    : isBusy
                      ? dict.connect.clickToCancel
                      : dict.connect.clickToConnect}
                </button>
                {isError && selectedProfile && (
                  <button
                    type="button"
                    className="btn btn-sm"
                    disabled={!!busyProfileIds[selectedProfile.id]}
                    onClick={() => handleDismissError(selectedProfile.id)}
                  >
                    {dict.connect.dismissError}
                  </button>
                )}
              </div>

              <div className="hero-meta-row">
                <span className="hero-meta-chip">
                  <Shield size={13} />
                  {dict.connect.protocol}:{" "}
                  <strong>
                    {(status.protocol || selectedProfile?.protocol) === "atrust"
                      ? "aTrust"
                      : "EasyConnect"}
                  </strong>
                </span>
                <span className="hero-meta-chip mono">
                  <Server size={13} />
                  {status.server ||
                    (selectedProfile
                      ? `${selectedProfile.serverAddress}:${selectedProfile.serverPort}`
                      : "—")}
                </span>
                {status.virtualIp && (
                  <span className="hero-meta-chip mono">
                    <Network size={13} />
                    {dict.connect.virtualIp}: <strong>{status.virtualIp}</strong>
                  </span>
                )}
                {selectedProfile?.tunMode && (
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
        <div
          className="card"
          style={{
            display: "flex",
            flexDirection: "column",
            justifyContent: "space-between",
            gap: 14,
          }}
        >
          <div>
            <div className="card-header" style={{ marginBottom: 8 }}>
              <h3 className="card-title">
                <Globe size={16} color="var(--accent)" />
                {dict.connect.sysproxyTitle}
              </h3>
              <button
                type="button"
                role="switch"
                aria-checked={!!sysproxyBoundToSelected}
                className={`switch success ${sysproxyBoundToSelected ? "checked" : ""}`}
                disabled={!sysproxy.supported || !isConnected}
                onClick={handleToggleSysproxy}
              >
                <span className="switch-thumb" />
              </button>
            </div>
            <p className="card-subtitle">{dict.connect.sysproxyDesc}</p>
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            {!sysproxy.supported ? (
              <div className="alert-banner alert-warning">
                <AlertCircle size={15} style={{ flexShrink: 0, marginTop: 1 }} />
                <span>{sysproxy.message || dict.connect.sysproxyUnsupported}</span>
              </div>
            ) : sysproxyBoundToOther ? (
              <>
                <span className="badge badge-warning">
                  {fmt(dict.connect.sysproxyOnOther, {
                    name: sysproxy.profileName || sysproxy.profileId || "",
                  })}
                </span>
                {isConnected && (
                  <button
                    type="button"
                    className="btn btn-sm btn-primary"
                    onClick={handleToggleSysproxy}
                  >
                    {dict.connect.switchSysproxyHere}
                  </button>
                )}
              </>
            ) : !isConnected ? (
              <span className="badge badge-neutral">
                {dict.connect.sysproxyNeedConnect}
              </span>
            ) : (
              <span
                className={`badge ${sysproxyBoundToSelected ? "badge-success" : "badge-neutral"}`}
              >
                {sysproxyBoundToSelected
                  ? dict.connect.sysproxyOnThis
                  : dict.common.disabled}
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
              <div
                style={{
                  fontSize: 11,
                  color: "var(--text-muted)",
                  fontWeight: 600,
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                }}
              >
                <span>SOCKS5</span>
                {socksAutoAssigned && (
                  <span className="badge badge-accent">
                    {dict.connect.portAutoAssigned}
                  </span>
                )}
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
              <div
                style={{
                  fontSize: 11,
                  color: "var(--text-muted)",
                  fontWeight: 600,
                  display: "flex",
                  alignItems: "center",
                  gap: 6,
                }}
              >
                <span>HTTP / HTTPS</span>
                {httpAutoAssigned && (
                  <span className="badge badge-accent">
                    {dict.connect.portAutoAssigned}
                  </span>
                )}
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
          <div style={{ display: "flex", alignItems: "center", gap: 10 }}>
            <h3 className="card-title">
              <Activity size={16} color="var(--accent)" />
              {dict.connect.trafficTitle}
            </h3>
            {connectedCount > 1 && (
              <div className="tabs">
                <button
                  type="button"
                  className={`tab-btn ${trafficScope === "current" ? "active" : ""}`}
                  onClick={() => setTrafficScope("current")}
                >
                  {dict.connect.trafficScopeCurrent}
                </button>
                <button
                  type="button"
                  className={`tab-btn ${trafficScope === "all" ? "active" : ""}`}
                  onClick={() => setTrafficScope("all")}
                >
                  {dict.connect.trafficScopeAll} ({connectedCount})
                </button>
              </div>
            )}
          </div>
          <span className="badge badge-neutral mono">
            {dict.connect.activeConns}: {displayedTraffic.activeConns}
          </span>
        </div>

        <div className="grid-4" style={{ marginBottom: 14 }}>
          <div className="stat-tile">
            <span className="stat-label">
              <ArrowUp size={13} color="var(--accent)" />
              {dict.connect.upload}
            </span>
            <span className="stat-value mono">
              {formatRate(displayedTraffic.rateSent)}
            </span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">
              <ArrowDown size={13} color="var(--success)" />
              {dict.connect.download}
            </span>
            <span className="stat-value mono">
              {formatRate(displayedTraffic.rateRecv)}
            </span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">{dict.connect.totalSent}</span>
            <span className="stat-value mono">
              {formatBytes(displayedTraffic.bytesSent)}
            </span>
          </div>
          <div className="stat-tile">
            <span className="stat-label">{dict.connect.totalRecv}</span>
            <span className="stat-value mono">
              {formatBytes(displayedTraffic.bytesRecv)}
            </span>
          </div>
        </div>

        <TrafficSparkline
          samples={displayedSamples}
          upLabel={dict.connect.upload}
          downLabel={dict.connect.download}
        />
      </div>
    </div>
  );
};
