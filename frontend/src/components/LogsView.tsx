import React, { useEffect, useMemo, useRef, useState } from "react";
import { Download, Search, ShieldCheck, Trash2 } from "lucide-react";
import { AppService, type LogEntry, type LogLevel, type Profile } from "../mygo";
import { type Dict, fmt } from "../i18n";
import { formatTimestamp } from "../utils/format";

interface LogsViewProps {
  logs: LogEntry[];
  profiles: Profile[];
  dict: Dict;
  onCleared: () => void;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

const LEVEL_RANK: Record<LogLevel, number> = {
  debug: 0,
  info: 1,
  warn: 2,
  error: 3,
};

export const LogsView: React.FC<LogsViewProps> = ({
  logs,
  profiles,
  dict,
  onCleared,
  onNotify,
}) => {
  const [minLevel, setMinLevel] = useState<"all" | LogLevel>("all");
  const [profileFilter, setProfileFilter] = useState<string>("");
  const [search, setSearch] = useState("");
  const [autoScroll, setAutoScroll] = useState(true);
  const consoleRef = useRef<HTMLDivElement | null>(null);

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase();
    const minRank = minLevel === "all" ? -1 : LEVEL_RANK[minLevel];
    return logs.filter((entry) => {
      if (LEVEL_RANK[entry.level] < minRank) return false;
      if (profileFilter && entry.profileId !== profileFilter) return false;
      if (!q) return true;
      return (
        entry.message.toLowerCase().includes(q) ||
        entry.source.toLowerCase().includes(q) ||
        (entry.profileName && entry.profileName.toLowerCase().includes(q))
      );
    });
  }, [logs, minLevel, profileFilter, search]);

  useEffect(() => {
    if (!autoScroll || !consoleRef.current) return;
    consoleRef.current.scrollTop = consoleRef.current.scrollHeight;
  }, [filtered, autoScroll]);

  const handleClear = async () => {
    try {
      await AppService.clearLogs();
      onCleared();
      onNotify(dict.logs.clearedToast, "success");
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const handleExport = async () => {
    try {
      const savedPath = await AppService.exportSanitizedLogs();
      if (savedPath) {
        onNotify(fmt(dict.logs.exportedToast, { path: savedPath }), "success");
      }
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  return (
    <div className="page-stack">
      <div className="card" style={{ padding: 14 }}>
        <div
          style={{
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 10,
            flexWrap: "wrap",
            marginBottom: 10,
          }}
        >
          <div className="tabs">
            {(["all", "debug", "info", "warn", "error"] as const).map((lvl) => (
              <button
                key={lvl}
                type="button"
                className={`tab-btn ${minLevel === lvl ? "active" : ""}`}
                onClick={() => setMinLevel(lvl)}
              >
                {lvl === "all" ? dict.logs.levelAll : lvl.toUpperCase()}
              </button>
            ))}
          </div>

          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            {profiles.length > 1 && (
              <select
                className="select"
                style={{ width: "auto", minWidth: 130, paddingTop: 6, paddingBottom: 6 }}
                value={profileFilter}
                onChange={(e) => setProfileFilter(e.target.value)}
              >
                <option value="">{dict.logs.allProfiles}</option>
                {profiles.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            )}

            <div style={{ position: "relative", width: 200 }}>
              <Search
                size={14}
                style={{
                  position: "absolute",
                  left: 10,
                  top: "50%",
                  transform: "translateY(-50%)",
                  color: "var(--text-muted)",
                }}
              />
              <input
                className="input"
                style={{ paddingLeft: 30, paddingTop: 6, paddingBottom: 6 }}
                placeholder={dict.common.search}
                value={search}
                onChange={(e) => setSearch(e.target.value)}
              />
            </div>

            <label
              style={{
                display: "inline-flex",
                alignItems: "center",
                gap: 6,
                fontSize: 12.5,
                color: "var(--text-secondary)",
                cursor: "pointer",
              }}
            >
              <input
                type="checkbox"
                checked={autoScroll}
                onChange={(e) => setAutoScroll(e.target.checked)}
              />
              {dict.logs.autoScroll}
            </label>

            <button type="button" className="btn btn-sm" onClick={handleClear}>
              <Trash2 size={13} />
              {dict.common.clear}
            </button>

            <button
              type="button"
              className="btn btn-sm btn-primary"
              onClick={handleExport}
            >
              <Download size={13} />
              {dict.logs.exportSanitized}
            </button>
          </div>
        </div>

        <div
          className="field-hint"
          style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 10 }}
        >
          <ShieldCheck size={14} color="var(--success)" />
          {dict.logs.exportHint}
        </div>

        <div className="log-console selectable" ref={consoleRef}>
          {filtered.length === 0 ? (
            <div className="empty-state" style={{ padding: 48 }}>
              {dict.logs.empty}
            </div>
          ) : (
            filtered.map((entry, idx) => (
              <div key={idx} className="log-line">
                <span className="log-ts">{formatTimestamp(entry.timestamp)}</span>
                <span className={`log-lvl ${entry.level}`}>{entry.level}</span>
                {entry.profileName && (
                  <span className="log-profile">[{entry.profileName}]</span>
                )}
                <span className="log-src">[{entry.source || "core"}]</span>
                <span className="log-msg">{entry.message}</span>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
};
