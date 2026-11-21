import React, { useEffect, useState } from "react";
import { DownloadCloud, Info, RefreshCw, Save, Shield, ShieldCheck, Trash2 } from "lucide-react";
import {
  AppService,
  type AppInfo,
  type AppSettings,
  type HelperStatus,
  type UpdateCheckResult,
} from "../mygo";
import { type Dict, fmt } from "../i18n";

interface SettingsViewProps {
  settings: AppSettings;
  appInfo: AppInfo | null;
  dict: Dict;
  onSettingsUpdated: (cfg: AppSettings) => void;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

export const SettingsView: React.FC<SettingsViewProps> = ({
  settings,
  appInfo,
  dict,
  onSettingsUpdated,
  onNotify,
}) => {
  const [draft, setDraft] = useState<AppSettings>(settings);
  const [saving, setSaving] = useState(false);
  const [checkingUpdate, setCheckingUpdate] = useState(false);
  const [installingUpdate, setInstallingUpdate] = useState(false);
  const [updateResult, setUpdateResult] = useState<UpdateCheckResult | null>(null);
  const [helperStatus, setHelperStatus] = useState<HelperStatus | null>(null);
  const [helperBusy, setHelperBusy] = useState(false);

  useEffect(() => {
    setDraft(settings);
  }, [settings]);

  useEffect(() => {
    AppService.getTUNHelperStatus()
      .then(setHelperStatus)
      .catch(() => {});
  }, []);

  const handleInstallHelper = async () => {
    if (helperBusy) return;
    setHelperBusy(true);
    try {
      const st = await AppService.installTUNHelper();
      setHelperStatus(st);
      onNotify(dict.settings.tunHelperInstalledToast, "success");
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setHelperBusy(false);
    }
  };

  const handleUninstallHelper = async () => {
    if (helperBusy) return;
    setHelperBusy(true);
    try {
      const st = await AppService.uninstallTUNHelper();
      setHelperStatus(st);
      onNotify(dict.settings.tunHelperUninstalledToast, "success");
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setHelperBusy(false);
    }
  };

  const handleSave = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (saving) return;
    setSaving(true);
    try {
      const saved = await AppService.saveSettings(draft);
      onSettingsUpdated(saved);
      onNotify(dict.settings.savedToast, "success");
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleQuickToggle = async (patch: Partial<AppSettings>) => {
    const next = { ...draft, ...patch };
    setDraft(next);
    try {
      const saved = await AppService.saveSettings(next);
      onSettingsUpdated(saved);
    } catch (err) {
      setDraft(draft);
      onNotify(String(err), "error");
    }
  };

  const handleCheckUpdate = async () => {
    setCheckingUpdate(true);
    try {
      const res = await AppService.checkForUpdates();
      setUpdateResult(res);
      if (!res.enabled) {
        onNotify(dict.settings.updateDisabled);
      } else if (!res.available) {
        onNotify(dict.settings.upToDate, "success");
      }
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setCheckingUpdate(false);
    }
  };

  const handleInstallUpdate = async () => {
    setInstallingUpdate(true);
    try {
      await AppService.installUpdate();
    } catch (err) {
      onNotify(String(err), "error");
      setInstallingUpdate(false);
    }
  };

  return (
    <div className="page-stack">
      {/* Default Profile Options */}
      <form className="card" onSubmit={handleSave}>
        <div className="card-header">
          <h3 className="card-title">{dict.settings.secDefaults}</h3>
          <button
            type="submit"
            className="btn btn-sm btn-primary"
            disabled={saving}
          >
            <Save size={13} />
            {dict.common.save}
          </button>
        </div>

        <div className="grid-2" style={{ marginBottom: 12 }}>
          <div className="field">
            <label className="field-label">{dict.settings.defaultSocks}</label>
            <input
              className="input mono"
              value={draft.defaultSocksBind}
              onChange={(e) =>
                setDraft({ ...draft, defaultSocksBind: e.target.value })
              }
            />
          </div>
          <div className="field">
            <label className="field-label">{dict.settings.defaultHttp}</label>
            <input
              className="input mono"
              value={draft.defaultHttpBind}
              onChange={(e) =>
                setDraft({ ...draft, defaultHttpBind: e.target.value })
              }
            />
          </div>
          <div className="field">
            <label className="field-label">{dict.settings.defaultRemoteDns}</label>
            <input
              className="input mono"
              value={draft.defaultRemoteDns}
              onChange={(e) =>
                setDraft({ ...draft, defaultRemoteDns: e.target.value })
              }
            />
          </div>
          <div className="field">
            <label className="field-label">
              {dict.settings.defaultSecondaryDns}
            </label>
            <input
              className="input mono"
              value={draft.defaultSecondaryDns}
              onChange={(e) =>
                setDraft({ ...draft, defaultSecondaryDns: e.target.value })
              }
            />
          </div>
        </div>

        <div className="toggle-row">
          <div>
            <div className="toggle-row-title">{dict.settings.defaultProxyAll}</div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.defaultProxyAll}
            className={`switch ${draft.defaultProxyAll ? "checked" : ""}`}
            onClick={() =>
              handleQuickToggle({ defaultProxyAll: !draft.defaultProxyAll })
            }
          >
            <span className="switch-thumb" />
          </button>
        </div>

        <div className="toggle-row">
          <div>
            <div className="toggle-row-title">
              {dict.settings.defaultAutoReconnect}
            </div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.autoReconnect}
            className={`switch ${draft.autoReconnect ? "checked" : ""}`}
            onClick={() =>
              handleQuickToggle({ autoReconnect: !draft.autoReconnect })
            }
          >
            <span className="switch-thumb" />
          </button>
        </div>
      </form>

      {/* Behavior & Appearance */}
      <div className="card">
        <div className="card-header">
          <h3 className="card-title">{dict.settings.secBehavior}</h3>
        </div>

        <div className="grid-2" style={{ marginBottom: 10 }}>
          <div className="field">
            <label className="field-label">{dict.settings.language}</label>
            <select
              className="select"
              value={draft.language}
              onChange={(e) => handleQuickToggle({ language: e.target.value })}
            >
              <option value="zh-CN">简体中文</option>
              <option value="en">English</option>
            </select>
          </div>
          <div className="field">
            <label className="field-label">{dict.settings.theme}</label>
            <select
              className="select"
              value={draft.theme}
              onChange={(e) => handleQuickToggle({ theme: e.target.value })}
            >
              <option value="system">{dict.settings.themeSystem}</option>
              <option value="light">{dict.settings.themeLight}</option>
              <option value="dark">{dict.settings.themeDark}</option>
            </select>
          </div>
        </div>

        <div className="toggle-row">
          <div>
            <div className="toggle-row-title">{dict.settings.closeToTray}</div>
            <div className="toggle-row-desc">{dict.settings.closeToTrayDesc}</div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.closeToTray}
            className={`switch ${draft.closeToTray ? "checked" : ""}`}
            onClick={() => handleQuickToggle({ closeToTray: !draft.closeToTray })}
          >
            <span className="switch-thumb" />
          </button>
        </div>

        <div className="toggle-row">
          <div>
            <div className="toggle-row-title">{dict.settings.openAtLogin}</div>
            <div className="toggle-row-desc">{dict.settings.openAtLoginDesc}</div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.openAtLogin}
            className={`switch ${draft.openAtLogin ? "checked" : ""}`}
            onClick={() => handleQuickToggle({ openAtLogin: !draft.openAtLogin })}
          >
            <span className="switch-thumb" />
          </button>
        </div>

        <div className="toggle-row">
          <div>
            <div className="toggle-row-title">
              {dict.settings.checkUpdatesOnStart}
            </div>
          </div>
          <button
            type="button"
            role="switch"
            aria-checked={draft.checkUpdatesOnStart}
            className={`switch ${draft.checkUpdatesOnStart ? "checked" : ""}`}
            onClick={() =>
              handleQuickToggle({
                checkUpdatesOnStart: !draft.checkUpdatesOnStart,
              })
            }
          >
            <span className="switch-thumb" />
          </button>
        </div>

        {helperStatus?.supported && (
          <div className="toggle-row" style={{ alignItems: "center", gap: 12 }}>
            <div>
              <div
                className="toggle-row-title"
                style={{ display: "flex", alignItems: "center", gap: 8 }}
              >
                <span>{dict.settings.tunHelperTitle}</span>
                <span
                  className={`badge ${
                    helperStatus.needsUpdate
                      ? "badge-warning"
                      : helperStatus.running
                        ? "badge-success"
                        : "badge-neutral"
                  }`}
                >
                  {helperStatus.needsUpdate
                    ? dict.settings.tunHelperNeedsUpdate
                    : helperStatus.running
                      ? dict.settings.tunHelperRunning
                      : dict.settings.tunHelperNotInstalled}
                </span>
              </div>
              <div className="toggle-row-desc">{dict.settings.tunHelperDesc}</div>
            </div>
            <div style={{ display: "flex", gap: 8, flexShrink: 0 }}>
              <button
                type="button"
                className={`btn btn-sm ${!helperStatus.running || helperStatus.needsUpdate ? "btn-primary" : ""}`}
                disabled={helperBusy}
                onClick={handleInstallHelper}
              >
                <ShieldCheck size={13} />
                {helperStatus.needsUpdate
                  ? dict.settings.tunHelperUpdate
                  : helperStatus.installed
                    ? dict.settings.tunHelperReinstall
                    : dict.settings.tunHelperInstall}
              </button>
              {helperStatus.installed && (
                <button
                  type="button"
                  className="btn btn-sm btn-danger"
                  disabled={helperBusy}
                  onClick={handleUninstallHelper}
                >
                  <Trash2 size={13} />
                  {dict.settings.tunHelperUninstall}
                </button>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Updates & About */}
      <div className="card">
        <div className="card-header">
          <h3 className="card-title">
            <Info size={16} color="var(--accent)" />
            {dict.settings.secAbout}
          </h3>
          <button
            type="button"
            className="btn btn-sm"
            disabled={checkingUpdate}
            onClick={handleCheckUpdate}
          >
            <RefreshCw size={13} />
            {dict.settings.checkUpdate}
          </button>
        </div>

        {appInfo && (
          <div className="grid-3" style={{ marginBottom: 14 }}>
            <div className="stat-tile">
              <span className="stat-label">{dict.settings.version}</span>
              <span className="stat-value mono" style={{ fontSize: 14 }}>
                v{appInfo.version}
              </span>
            </div>
            <div className="stat-tile">
              <span className="stat-label">{dict.settings.platform}</span>
              <span className="stat-value mono" style={{ fontSize: 14 }}>
                {appInfo.os}/{appInfo.arch}
              </span>
            </div>
            <div className="stat-tile">
              <span className="stat-label">{dict.settings.configDir}</span>
              <span
                className="stat-sub mono selectable"
                style={{
                  color: "var(--text-primary)",
                  wordBreak: "break-all",
                  marginTop: 2,
                }}
              >
                {appInfo.configDir}
              </span>
            </div>
          </div>
        )}

        {updateResult?.available && (
          <div
            className="alert-banner alert-info"
            style={{
              marginBottom: 14,
              alignItems: "center",
              justifyContent: "space-between",
            }}
          >
            <div>
              <strong>
                {fmt(dict.settings.updateAvailable, {
                  version: updateResult.version || "",
                })}
              </strong>
              {updateResult.notes && (
                <div style={{ marginTop: 4, fontSize: 12 }}>
                  {updateResult.notes}
                </div>
              )}
            </div>
            <button
              type="button"
              className="btn btn-sm btn-primary"
              disabled={installingUpdate}
              onClick={handleInstallUpdate}
            >
              <DownloadCloud size={14} />
              {dict.settings.installUpdate}
            </button>
          </div>
        )}

        <div
          style={{
            padding: "12px 14px",
            borderRadius: 8,
            background: "var(--bg-input)",
            border: "1px solid var(--border-subtle)",
            fontSize: 12.5,
            color: "var(--text-secondary)",
          }}
        >
          <div
            style={{
              display: "flex",
              alignItems: "center",
              gap: 6,
              fontWeight: 600,
              color: "var(--text-primary)",
              marginBottom: 4,
            }}
          >
            <Shield size={14} color="var(--accent)" />
            {dict.settings.licenseTitle}
          </div>
          {dict.settings.licenseBody}
        </div>
      </div>
    </div>
  );
};
