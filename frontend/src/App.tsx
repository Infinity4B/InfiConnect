import React, { useCallback, useEffect, useMemo, useState } from "react";
import {
  FolderKanban,
  Globe,
  Power,
  ScrollText,
  Settings,
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
  type State,
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

function isBusyState(st: State): boolean {
  return (
    st === "connecting" ||
    st === "authenticating" ||
    st === "reconnecting" ||
    st === "disconnecting"
  );
}

export const App: React.FC = () => {
  const [page, setPage] = useState<PageKey>("connect");
  const [appInfo, setAppInfo] = useState<AppInfo | null>(null);
  const [sessionsByProfile, setSessionsByProfile] = useState<
    Record<string, StatusSnapshot>
  >({});
  const [trafficByProfile, setTrafficByProfile] = useState<
    Record<string, TrafficStats>
  >({});
  const [samplesByProfile, setSamplesByProfile] = useState<
    Record<string, TrafficSample[]>
  >({});
  const [resourcesByProfile, setResourcesByProfile] = useState<
    Record<string, ResourceSnapshot>
  >({});
  const [profiles, setProfiles] = useState<Profile[]>([]);
  const [activeProfileId, setActiveProfileId] = useState<string>("");
  const [selectedProfileId, setSelectedProfileId] = useState<string>("");
  const [sysproxy, setSysproxy] = useState<SysproxyState>({
    enabled: false,
    supported: true,
  });
  const [settings, setSettings] = useState<AppSettings>(DEFAULT_SETTINGS);
  const [logs, setLogs] = useState<LogEntry[]>([]);
  const [challenges, setChallenges] = useState<Challenge[]>([]);
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

  // Keep selectedProfileId valid whenever profiles list updates
  useEffect(() => {
    if (profiles.length === 0) {
      setSelectedProfileId("");
      return;
    }
    setSelectedProfileId((prev) => {
      if (prev && profiles.some((p) => p.id === prev)) return prev;
      if (activeProfileId && profiles.some((p) => p.id === activeProfileId)) {
        return activeProfileId;
      }
      return profiles[0].id;
    });
  }, [profiles, activeProfileId]);

  const applySessionsList = useCallback((list: StatusSnapshot[]) => {
    const nextSessions: Record<string, StatusSnapshot> = {};
    const activeIds = new Set<string>();
    const activeChallenges: Challenge[] = [];

    for (const st of list || []) {
      if (!st.profileId || st.state === "idle") continue;
      nextSessions[st.profileId] = st;
      activeIds.add(st.profileId);
      if (st.activeChallenge) {
        activeChallenges.push(st.activeChallenge);
      }
    }

    setSessionsByProfile(nextSessions);

    setTrafficByProfile((prev) => {
      const next: Record<string, TrafficStats> = {};
      for (const [pid, st] of Object.entries(nextSessions)) {
        next[pid] = prev[pid] || st.traffic;
      }
      return next;
    });

    setSamplesByProfile((prev) => {
      let changed = false;
      const next: Record<string, TrafficSample[]> = {};
      for (const pid of Object.keys(prev)) {
        if (activeIds.has(pid)) {
          next[pid] = prev[pid];
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });

    setResourcesByProfile((prev) => {
      let changed = false;
      const next: Record<string, ResourceSnapshot> = {};
      for (const pid of Object.keys(prev)) {
        if (nextSessions[pid]?.state === "connected") {
          next[pid] = prev[pid];
        } else {
          changed = true;
        }
      }
      return changed ? next : prev;
    });

    setChallenges((prev) => {
      const byId = new Map<string, Challenge>();
      for (const ch of prev) {
        if (!ch.profileId || activeIds.has(ch.profileId)) {
          byId.set(ch.id, ch);
        }
      }
      for (const ch of activeChallenges) {
        byId.set(ch.id, ch);
      }
      return Array.from(byId.values());
    });
  }, []);

  // Initial hydration + event subscriptions
  useEffect(() => {
    let alive = true;

    (async () => {
      try {
        const [info, sessList, allRes, profs, sp, cfg, initialLogs] =
          await Promise.all([
            AppService.getAppInfo(),
            AppService.getSessions(),
            AppService.getAllResources(),
            AppService.listProfiles(),
            AppService.getSystemProxyState(),
            AppService.getSettings(),
            AppService.getLogs("debug", 500),
          ]);
        if (!alive) return;
        setAppInfo(info);
        applySessionsList(sessList || []);

        const resMap: Record<string, ResourceSnapshot> = {};
        for (const r of allRes || []) {
          if (r.profileId) resMap[r.profileId] = r;
        }
        setResourcesByProfile(resMap);

        setProfiles(profs.profiles || []);
        setActiveProfileId(profs.activeProfileId || "");
        setSelectedProfileId(
          profs.activeProfileId || profs.profiles?.[0]?.id || "",
        );
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
      events.vpnSessions.on((list) => {
        applySessionsList(list || []);
      }),
      events.vpnState.on((st) => {
        const pid = st.profileId;
        if (!pid) return;

        setSessionsByProfile((prev) => {
          const next = { ...prev };
          if (st.state === "idle") {
            delete next[pid];
          } else {
            next[pid] = st;
          }
          return next;
        });

        if (st.state === "idle") {
          setTrafficByProfile((prev) => {
            if (!(pid in prev)) return prev;
            const next = { ...prev };
            delete next[pid];
            return next;
          });
          setSamplesByProfile((prev) => {
            if (!(pid in prev)) return prev;
            const next = { ...prev };
            delete next[pid];
            return next;
          });
          setResourcesByProfile((prev) => {
            if (!(pid in prev)) return prev;
            const next = { ...prev };
            delete next[pid];
            return next;
          });
          setChallenges((prev) => prev.filter((c) => c.profileId !== pid));
        } else {
          if (st.traffic) {
            setTrafficByProfile((prev) => ({ ...prev, [pid]: st.traffic }));
          }
          if (st.activeChallenge) {
            const ch = st.activeChallenge;
            setChallenges((prev) =>
              prev.some((x) => x.id === ch.id) ? prev : [...prev, ch],
            );
          } else if (st.state !== "authenticating") {
            setChallenges((prev) => prev.filter((c) => c.profileId !== pid));
          }
        }
      }),
      events.vpnChallenge.on((ch) => {
        setChallenges((prev) =>
          prev.some((x) => x.id === ch.id) ? prev : [...prev, ch],
        );
      }),
      events.vpnChallengeResolved.on((id) => {
        setChallenges((prev) => prev.filter((c) => c.id !== id));
      }),
      events.vpnLog.on((entry) => {
        setLogs((prev) => {
          const next = [...prev, entry];
          return next.length > MAX_LOGS ? next.slice(next.length - MAX_LOGS) : next;
        });
      }),
      events.vpnTraffic.on((stats) => {
        const pid = stats.profileId;
        if (!pid) return;
        setTrafficByProfile((prev) => ({ ...prev, [pid]: stats }));
        setSamplesByProfile((prev) => {
          const existing = prev[pid] || [];
          const nextArr = [
            ...existing,
            { rateSent: stats.rateSent, rateRecv: stats.rateRecv },
          ];
          return {
            ...prev,
            [pid]:
              nextArr.length > MAX_SAMPLES
                ? nextArr.slice(nextArr.length - MAX_SAMPLES)
                : nextArr,
          };
        });
      }),
      events.vpnResources.on((res) => {
        if (!res.profileId) return;
        setResourcesByProfile((prev) => ({ ...prev, [res.profileId!]: res }));
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
  }, [notify, applySessionsList]);

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
      setSelectedProfileId(profileId);
      setPage("connect");
      await AppService.connect(profileId);
    } catch (err) {
      notify(String(err), "error");
    }
  };

  const handleDisconnectProfile = async (profileId: string) => {
    try {
      await AppService.disconnect(profileId);
    } catch (err) {
      notify(String(err), "error");
    }
  };

  const sessionList = useMemo(
    () => Object.values(sessionsByProfile),
    [sessionsByProfile],
  );
  const connectedSessions = useMemo(
    () => sessionList.filter((s) => s.state === "connected"),
    [sessionList],
  );
  const busySessions = useMemo(
    () => sessionList.filter((s) => isBusyState(s.state)),
    [sessionList],
  );
  const errorSessions = useMemo(
    () => sessionList.filter((s) => s.state === "error"),
    [sessionList],
  );

  const isMac = appInfo?.os === "darwin";
  const connectedCount = connectedSessions.length;
  const activeCount = connectedCount + busySessions.length;

  const statusDotClass =
    connectedCount > 0
      ? "connected"
      : busySessions.length > 0
        ? "busy"
        : errorSessions.length > 0
          ? "error"
          : "";

  const sidebarStateText =
    connectedCount > 1
      ? fmt(dict.connect.connectedCount, { n: connectedCount })
      : connectedCount === 1
        ? dict.state.connected
        : busySessions.length > 0
          ? dict.state[busySessions[0].state]
          : errorSessions.length > 0
            ? dict.state.error
            : dict.state.idle;

  const sidebarSubText =
    connectedCount > 1
      ? connectedSessions
          .map((s) => s.profileName || s.virtualIp)
          .filter(Boolean)
          .join(" · ")
      : connectedCount === 1
        ? connectedSessions[0].virtualIp ||
          connectedSessions[0].profileName ||
          `v${appInfo?.version || "0.1.0"}`
        : busySessions.length > 0
          ? busySessions[0].profileName || ""
          : `v${appInfo?.version || "0.1.0"}`;

  const resourceCount = useMemo(() => {
    let total = 0;
    for (const res of Object.values(resourcesByProfile)) {
      total += (res.ipResources?.length || 0) + (res.domainResources?.length || 0);
    }
    return total;
  }, [resourcesByProfile]);

  const pageTitle: Record<PageKey, string> = {
    connect: dict.connect.title,
    profiles: dict.profiles.title,
    resources: dict.resources.title,
    logs: dict.logs.title,
    settings: dict.settings.title,
  };

  const activeChallenge = challenges[0] || null;

  return (
    <div className="app-shell">
      {/* Sidebar */}
      <aside className={`sidebar ${isMac ? "macos" : ""}`}>
        <div className="titlebar-drag">
          <div className="brand-mark">
            <div className="brand-logo" aria-hidden="true">
              <svg viewBox="0 0 32 32" width="22" height="22">
                <defs>
                  <linearGradient id="icBrandBg" x1="12%" y1="6%" x2="88%" y2="94%">
                    <stop offset="0%" stopColor="#2563eb" />
                    <stop offset="55%" stopColor="#1d4ed8" />
                    <stop offset="100%" stopColor="#0e7490" />
                  </linearGradient>
                  <linearGradient id="icBrandRibbon" x1="4" y1="10" x2="28" y2="22" gradientUnits="userSpaceOnUse">
                    <stop offset="0%" stopColor="#ffffff" />
                    <stop offset="58%" stopColor="#e0f2fe" />
                    <stop offset="100%" stopColor="#67e8f9" />
                  </linearGradient>
                </defs>
                <rect width="32" height="32" rx="8" fill="url(#icBrandBg)" />
                <path
                  d="M12.75 12.75 L19.25 19.25 A4.6 4.6 0 1 0 19.25 12.75 L12.75 19.25 A4.6 4.6 0 1 1 12.75 12.75 Z"
                  fill="none"
                  stroke="url(#icBrandRibbon)"
                  strokeWidth="2.9"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
                <line
                  x1="13.5"
                  y1="13.5"
                  x2="18.5"
                  y2="18.5"
                  stroke="#0b1d4e"
                  strokeOpacity="0.45"
                  strokeWidth="4.2"
                  strokeLinecap="butt"
                />
                <line
                  x1="12.75"
                  y1="12.75"
                  x2="19.25"
                  y2="19.25"
                  stroke="url(#icBrandRibbon)"
                  strokeWidth="2.9"
                  strokeLinecap="butt"
                />
              </svg>
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
            {connectedCount > 0 && (
              <span className="nav-badge success">{connectedCount}</span>
            )}
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
              <div className="status-pill-state">{sidebarStateText}</div>
              <div className="status-pill-sub mono">{sidebarSubText}</div>
            </div>
          </div>
        </div>
      </aside>

      {/* Main Content */}
      <div className="main-column">
        <header className="main-topbar">
          <div className="main-topbar-title">{pageTitle[page]}</div>
          <div className="main-topbar-actions">
            {activeCount > 0 && (
              <span className="badge badge-accent">
                {fmt(dict.connect.activeCount, { n: activeCount })}
              </span>
            )}
            {sysproxy.enabled && (
              <span className="badge badge-success">
                {dict.connect.sysproxyTitle}
                {sysproxy.profileName ? `: ${sysproxy.profileName}` : `: ${dict.common.enabled}`}
              </span>
            )}
          </div>
        </header>

        <main className="main-scroll">
          {page === "connect" && (
            <ConnectView
              profiles={profiles}
              sessionsByProfile={sessionsByProfile}
              trafficByProfile={trafficByProfile}
              samplesByProfile={samplesByProfile}
              selectedProfileId={selectedProfileId}
              sysproxy={sysproxy}
              dict={dict}
              onSelectProfile={(id) => {
                setSelectedProfileId(id);
                AppService.setActiveProfile(id).catch(() => {});
              }}
              onGoProfiles={() => setPage("profiles")}
              onImportTomlFile={handleImportTomlFile}
              onNotify={notify}
            />
          )}

          {page === "profiles" && (
            <ProfilesView
              profiles={profiles}
              activeProfileId={activeProfileId}
              sessionsByProfile={sessionsByProfile}
              settings={settings}
              dict={dict}
              onImportTomlFile={handleImportTomlFile}
              onConnectProfile={handleConnectProfile}
              onDisconnectProfile={handleDisconnectProfile}
              onNotify={notify}
            />
          )}

          {page === "resources" && (
            <ResourcesView
              connectedSessions={connectedSessions}
              resourcesByProfile={resourcesByProfile}
              dict={dict}
              onNotify={notify}
            />
          )}

          {page === "logs" && (
            <LogsView
              logs={logs}
              profiles={profiles}
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
          queueTotal={challenges.length}
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
