import React, { useCallback, useEffect, useState } from "react";
import {
  FolderKanban,
  Globe,
  Power,
  ScrollText,
  Settings,
  Shield,
} from "lucide-react";
import {
  AppService,
  events,
  type AppInfo,
  type AppSettings,
  type Challenge,
  type LogEntry,
  type Profile,
  type ResourceSnapshot,
  type StatusSnapshot,
  type SysproxyState,
  type TrafficStats,
} from "./mygo";
import { fmt, getDict } from "./i18n";
import { ConnectView } from "./components/ConnectView";
import { ProfilesView } from "./components/ProfilesView";
import { ResourcesView } from "./components/ResourcesView";
import { LogsView } from "./components/LogsView";
import { SettingsView } from "./components/SettingsView";
import { ChallengeModal } from "./components/ChallengeModal";
import type { TrafficSample } from "./components/TrafficSparkline";

type PageKey = "connect" | "profiles" | "resources" | "logs" | "settings";

interface ToastItem {
  id: number;
  message: string;
  kind?: "success" | "error";
}

const DEFAULT_STATUS: StatusSnapshot = {
  state: "idle",
  traffic: {
    bytesSent: 0,
    bytesRecv: 0,
    rateSent: 0,
    rateRecv: 0,
    activeConns: 0,
  },
};

const DEFAULT_RESOURCES: ResourceSnapshot = {
  ipResources: [],
  domainResources: [],
  dnsOverrides: [],
  proxyAll: false,
};

const DEFAULT_SETTINGS: AppSettings = {
  defaultSocksBind: "127.0.0.1:1080",
  defaultHttpBind: "127.0.0.1:1081",
  defaultRemoteDns: "auto",
  defaultSecondaryDns: "auto",
  defaultProxyAll: false,
  autoReconnect: true,
  closeToTray: true,
  openAtLogin: false,
  language: "zh-CN",
  theme: "system",
  checkUpdatesOnStart: true,
};

const MAX_SAMPLES = 45;
const MAX_LOGS = 2000;

export const App: React.FC = () => {
  const [page, setPage] = useState<PageKey>("connect");
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [status, setStatus] = useState<StatusSnapshot>(DEFAULT_STATUS);
  const [traffic, setTraffic] = useState<TrafficStats>(DEFAULT_STATUS.traffic);
  const [samples, setSamples] = useState<TrafficSample[]>([]);
  const [resources, setResources] = useState<ResourceSnapshot>(DEFAULT_RESOURCES);
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [activeProfileId, setActiveProfileId] = useState<string>("");
  const [sysproxy, setSysproxy] = useState<SysproxyState>({
    enabled: false,
    supported: true,
  });
  const [settings, setSettings] = useState<AppSettings>(DEFAULT_SETTINGS);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [activeChallenge, setActiveChallenge] = useState<Challenge | null>(null);
  const [toasts, setToasts] = useState<ToastItem[]>([]);

  const dict = getDict(settings.language);

  const notify = useCallback((message: string, kind?: "success" | "error") => {
    const id = Date.now() + Math.random();
    setToasts((prev) => [...prev.slice(-3), { id, message, kind }]);
    window.setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 3600);
  }, []);

  // Apply theme attribute to document root
  useEffect(() => {
    document.documentElement.setAttribute("data-theme", settings.theme || "system");
  }, [settings.theme]);

  // Initial hydration + event subscriptions
  useEffect(() => {
    let alive = true;

    (async () => {
      try {
        const [info, st, res, profs, sp, cfg, initialLogs] = await Promise.all([
          AppService.getAppInfo(),
          AppService.getStatus(),
          AppService.getResources(),
          AppService.listProfiles(),
          AppService.getSystemProxyState(),
          AppService.getSettings(),
          AppService.getLogs("debug", 500),
        ]);
        if (!alive) return;
        setAppInfo(info);
        setStatus(st);
        setTraffic(st.traffic);
        setActiveChallenge(st.activeChallenge || null);
        setResources(res);
        setProfiles(profs.profiles || []);
        setActiveProfileId(profs.activeProfileId || "");
        setSysproxy(sp);
        setSettings(cfg);
        setLogs(initialLogs || []);

        if (cfg.checkUpdatesOnStart && info.updateEnabled) {
          AppService.checkForUpdates().catch(() => {});
        }
      } catch (err) {
        if (alive) notify(String(err), "error");
      }
    })();

    const unsubs = [
      events.vpnState.on((st) => {
        setStatus(st);
        if (st.traffic) setTraffic(st.traffic);
        if (st.activeChallenge) {
          setActiveChallenge(st.activeChallenge);
        } else if (st.state !== "authenticating") {
          setActiveChallenge(null);
        }
        if (st.state === "idle") {
          setSamples([]);
          setResources(DEFAULT_RESOURCES);
        }
      }),
      events.vpnChallenge.on((ch) => {
        setActiveChallenge(ch);
      }),
      events.vpnChallengeResolved.on((id) => {
        setActiveChallenge((prev) => (prev && prev.id === id ? null : prev));
      }),
      events.vpnLog.on((entry) => {
        setLogs((prev) => {
          const next = [...prev, entry];
          return next.length > MAX_LOGS ? next.slice(next.length - MAX_LOGS) : next;
        });
      }),
      events.vpnTraffic.on((stats) => {
        setTraffic(stats);
        setSamples((prev) => {
          const next = [
            ...prev,
            { rateSent: stats.rateSent, rateRecv: stats.rateRecv },
          ];
          return next.length > MAX_SAMPLES
            ? next.slice(next.length - MAX_SAMPLES)
            : next;
        });
      }),
      events.vpnResources.on((res) => {
        setResources(res);
      }),
      events.vpnSysproxy.on((sp) => {
        setSysproxy(sp);
      }),
      events.vpnProfiles.on((res) => {
        setProfiles(res.profiles || []);
        setActiveProfileId(res.activeProfileId || "");
      }),
    ];

    return () => {
      alive = false;
      for (const unsub of unsubs) {
        if (typeof unsub === "function") unsub();
      }
    };
  }, [notify]);

  const handleImportTomlFile = async () => {
    try {
      const prof = await AppService.importTOMLFile();
      if (prof) {
        notify(fmt(dict.profiles.importedToast, { name: prof.name }), "success");
      }
    } catch (err) {
      notify(String(err), "error");
    }
  };

  const handleConnectProfile = async (profileId: string) => {
    try {
      setPage("connect");
      await AppService.connect(profileId);
    } catch (err) {
      notify(String(err), "error");
    }
  };

  const isMac = appInfo?.os === "darwin";
  const isConnected = status.state === "connected";
  const isConnectedOrBusy =
    status.state === "connected" ||
    status.state === "connecting" ||
    status.state === "authenticating" ||
    status.state === "reconnecting" ||
    status.state === "disconnecting";

  const statusDotClass =
    status.state === "connected"
      ? "connected"
      : status.state === "error"
        ? "error"
        : status.state !== "idle"
          ? "busy"
          : "";

  const resourceCount =
    (resources.ipResources?.length || 0) + (resources.domainResources?.length || 0);

  const pageTitle: Record<PageKey, string> = {
    connect: dict.connect.title,
    profiles: dict.profiles.title,
    resources: dict.resources.title,
    logs: dict.logs.title,
    settings: dict.settings.title,
  };

  return (
    <div className="app-shell">
      {/* Sidebar */}
      <aside className={`sidebar ${isMac ? "macos" : ""}`}>
        <div className="titlebar-drag">
          <div className="brand-mark">
            <div className="brand-logo">
              <Shield size={13} strokeWidth={2.5} />
            </div>
            <span>InfiConnect</span>
          </div>
        </div>

        <nav className="sidebar-nav">
          <button
            type="button"
            className={`nav-item ${page === "connect" ? "active" : ""}`}
            onClick={() => setPage("connect")}
          >
            <Power size={16} />
            <span>{dict.nav.connect}</span>
          </button>

          <button
            type="button"
            className={`nav-item ${page === "profiles" ? "active" : ""}`}
            onClick={() => setPage("profiles")}
          >
            <FolderKanban size={16} />
            <span>{dict.nav.profiles}</span>
            {profiles.length > 0 && (
              <span className="nav-badge">{profiles.length}</span>
            )}
          </button>

          <button
            type="button"
            className={`nav-item ${page === "resources" ? "active" : ""}`}
            onClick={() => setPage("resources")}
          >
            <Globe size={16} />
            <span>{dict.nav.resources}</span>
            {resourceCount > 0 && (
              <span className="nav-badge">{resourceCount}</span>
            )}
          </button>

          <button
            type="button"
            className={`nav-item ${page === "logs" ? "active" : ""}`}
            onClick={() => setPage("logs")}
          >
            <ScrollText size={16} />
            <span>{dict.nav.logs}</span>
            {logs.length > 0 && <span className="nav-badge">{logs.length}</span>}
          </button>

          <button
            type="button"
            className={`nav-item ${page === "settings" ? "active" : ""}`}
            onClick={() => setPage("settings")}
          >
            <Settings size={16} />
            <span>{dict.nav.settings}</span>
          </button>
        </nav>

        <div className="sidebar-footer">
          <div className="status-pill">
            <span className={`status-dot ${statusDotClass}`} />
            <div className="status-pill-text">
              <div className="status-pill-state">{dict.state[status.state]}</div>
              <div className="status-pill-sub mono">
                {status.virtualIp || status.profileName || `v${appInfo?.version || "0.1.0"}`}
              </div>
            </div>
          </div>
        </div>
      </aside>

      {/* Main Content */}
      <div className="main-column">
        <header className="main-topbar">
          <div className="main-topbar-title">{pageTitle[page]}</div>
          <div className="main-topbar-actions">
            {sysproxy.enabled && (
              <span className="badge badge-success">
                {dict.connect.sysproxyTitle}: {dict.common.enabled}
              </span>
            )}
          </div>
        </header>

        <main className="main-scroll">
          {page === "connect" && (
            <ConnectView
              status={status}
              traffic={traffic}
              samples={samples}
              profiles={profiles}
              activeProfileId={activeProfileId}
              sysproxy={sysproxy}
              dict={dict}
              onGoProfiles={() => setPage("profiles")}
              onImportTomlFile={handleImportTomlFile}
              onNotify={notify}
            />
          )}

          {page === "profiles" && (
            <ProfilesView
              profiles={profiles}
              activeProfileId={activeProfileId}
              settings={settings}
              isConnectedOrBusy={isConnectedOrBusy}
              dict={dict}
              onImportTomlFile={handleImportTomlFile}
              onConnectProfile={handleConnectProfile}
              onNotify={notify}
            />
          )}

          {page === "resources" && (
            <ResourcesView
              resources={resources}
              isConnected={isConnected}
              dict={dict}
              onNotify={notify}
            />
          )}

          {page === "logs" && (
            <LogsView
              logs={logs}
              dict={dict}
              onCleared={() => setLogs([])}
              onNotify={notify}
            />
          )}

          {page === "settings" && (
            <SettingsView
              settings={settings}
              appInfo={appInfo}
              dict={dict}
              onSettingsUpdated={setSettings}
              onNotify={notify}
            />
          )}
        </main>
      </div>

      {/* Interactive Auth Challenge Modal */}
      {activeChallenge && (
        <ChallengeModal
          challenge={activeChallenge}
          dict={dict}
          onError={(msg) => notify(msg, "error")}
        />
      )}

      {/* Toast Stack */}
      {toasts.length > 0 && (
        <div className="toast-stack">
          {toasts.map((t) => (
            <div key={t.id} className={`toast ${t.kind || ""}`}>
              {t.message}
            </div>
          ))}
        </div>
      )}
    </div>
  );
};
