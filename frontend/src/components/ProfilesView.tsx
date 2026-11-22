import React, { useState } from "react";
import {
  CheckCircle2,
  ClipboardPaste,
  Edit3,
  FileUp,
  KeyRound,
  Play,
  Plus,
  RefreshCw,
  Server,
  Square,
  Trash2,
  X,
} from "lucide-react";
import {
  AppService,
  type ATrustAuthType,
  type AppSettings,
  type CustomDNS,
  type Profile,
  type Protocol,
  type SaveProfileInput,
  type StatusSnapshot,
} from "../mygo";
import { type Dict, fmt } from "../i18n";

interface ProfilesViewProps {
  profiles: Profile[];
  activeProfileId: string;
  sessionsByProfile: Record<string, StatusSnapshot>;
  settings: AppSettings;
  dict: Dict;
  onImportTomlFile: () => void;
  onConnectProfile: (profileId: string) => void;
  onDisconnectProfile: (profileId: string) => void;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

function createEmptyProfile(settings: AppSettings): Profile {
  return {
    id: "",
    name: "",
    protocol: "easyconnect",
    serverAddress: "",
    serverPort: 443,
    username: "",
    password: "",
    totpSecret: "",
    socksBind: settings.defaultSocksBind || "127.0.0.1:1080",
    httpBind: settings.defaultHttpBind || "127.0.0.1:1081",
    proxyAll: settings.defaultProxyAll,
    remoteDnsServer: settings.defaultRemoteDns || "auto",
    secondaryDnsServer: settings.defaultSecondaryDns || "auto",
    dnsTtl: 3600,
    autoReconnect: settings.autoReconnect,
    loginDomain: "",
    updateBestNodesInterval: 300,
    sessionRefreshInterval: 1800,
    customProxyDomains: [],
    customDns: [],
    macAddresses: [],
    tunMode: true,
    tunCustomRoutes: [],
    tunExcludeRoutes: [],
  };
}

export const ProfilesView: React.FC<ProfilesViewProps> = ({
  profiles,
  activeProfileId,
  sessionsByProfile,
  settings,
  dict,
  onImportTomlFile,
  onConnectProfile,
  onDisconnectProfile,
  onNotify,
}) => {
  const [editing, setEditing] = useState<Profile | null>(null);
  const [clearPassword, setClearPassword] = useState(false);
  const [clearTotp, setClearTotp] = useState(false);
  const [clearCertPass, setClearCertPass] = useState(false);
  const [clearSocksPass, setClearSocksPass] = useState(false);
  const [customDomainsText, setCustomDomainsText] = useState("");
  const [macsText, setMacsText] = useState("");
  const [tunCustomText, setTunCustomText] = useState("");
  const [tunExcludeText, setTunExcludeText] = useState("");
  const [saving, setSaving] = useState(false);

  const [pasteModalOpen, setPasteModalOpen] = useState(false);
  const [tomlName, setTomlName] = useState("");
  const [tomlBody, setTomlBody] = useState("");
  const [importing, setImporting] = useState(false);

  const openEditor = (prof?: Profile) => {
    const p = prof
      ? {
          ...prof,
          password: "",
          totpSecret: "",
          certPassword: "",
          socksPasswd: "",
          customDns: prof.customDns ? prof.customDns.map((c) => ({ ...c })) : [],
          customProxyDomains: prof.customProxyDomains ? [...prof.customProxyDomains] : [],
          macAddresses: prof.macAddresses ? [...prof.macAddresses] : [],
          tunCustomRoutes: prof.tunCustomRoutes ? [...prof.tunCustomRoutes] : [],
          tunExcludeRoutes: prof.tunExcludeRoutes ? [...prof.tunExcludeRoutes] : [],
        }
      : createEmptyProfile(settings);
    setEditing(p);
    setClearPassword(false);
    setClearTotp(false);
    setClearCertPass(false);
    setClearSocksPass(false);
    setCustomDomainsText((p.customProxyDomains || []).join("\n"));
    setMacsText((p.macAddresses || []).join(", "));
    setTunCustomText((p.tunCustomRoutes || []).join("\n"));
    setTunExcludeText((p.tunExcludeRoutes || []).join("\n"));
  };

  const closeEditor = () => {
    setEditing(null);
  };

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!editing || saving) return;
    setSaving(true);
    try {
      const domains = customDomainsText
        .split(/\r?\n|,/)
        .map((s) => s.trim())
        .filter(Boolean);
      const macs = macsText
        .split(/[\r\n,]+/)
        .map((s) => s.trim())
        .filter(Boolean);
      const tunCustom = tunCustomText
        .split(/\r?\n|,/)
        .map((s) => s.trim())
        .filter(Boolean);
      const tunExclude = tunExcludeText
        .split(/\r?\n|,/)
        .map((s) => s.trim())
        .filter(Boolean);
      const customDns = (editing.customDns || []).filter(
        (c) => c.hostName.trim() && c.ip.trim(),
      );

      const payload: SaveProfileInput = {
        profile: {
          ...editing,
          serverPort: Number(editing.serverPort) || 443,
          dnsTtl: Number(editing.dnsTtl) || 3600,
          updateBestNodesInterval: Number(editing.updateBestNodesInterval) || 300,
          sessionRefreshInterval: Number(editing.sessionRefreshInterval) || 1800,
          customProxyDomains: domains,
          macAddresses: macs,
          tunCustomRoutes: tunCustom,
          tunExcludeRoutes: tunExclude,
          customDns,
        },
        clearPassword,
        clearTotp,
        clearCertPass,
        clearSocksPass,
      };
      await AppService.saveProfile(payload);
      onNotify(dict.profiles.savedToast, "success");
      setEditing(null);
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setSaving(false);
    }
  };

  const handleDelete = async (p: Profile) => {
    const sess = sessionsByProfile[p.id];
    const isRunning =
      sess &&
      (sess.state === "connected" ||
        sess.state === "connecting" ||
        sess.state === "authenticating" ||
        sess.state === "reconnecting");
    const msg = isRunning
      ? fmt(dict.profiles.deleteConnectedConfirm, { name: p.name })
      : fmt(dict.profiles.deleteConfirm, { name: p.name });
    const ok = window.confirm(msg);
    if (!ok) return;
    try {
      await AppService.deleteProfile(p.id);
      onNotify(dict.profiles.deletedToast, "success");
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const handleClearSession = async (p: Profile) => {
    try {
      await AppService.clearProfileSession(p.id);
      onNotify(dict.profiles.clearSessionDone, "success");
    } catch (err) {
      onNotify(String(err), "error");
    }
  };

  const handleImportRawToml = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!tomlBody.trim() || importing) return;
    setImporting(true);
    try {
      const imported = await AppService.importTOML(tomlBody, tomlName.trim());
      onNotify(fmt(dict.profiles.importedToast, { name: imported.name }), "success");
      setPasteModalOpen(false);
      setTomlName("");
      setTomlBody("");
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setImporting(false);
    }
  };

  const updateCustomDns = (idx: number, field: keyof CustomDNS, value: string) => {
    if (!editing) return;
    const next = [...(editing.customDns || [])];
    next[idx] = { ...next[idx], [field]: value };
    setEditing({ ...editing, customDns: next });
  };

  return (
    <div className="page-stack">
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          gap: 10,
          flexWrap: "wrap",
        }}
      >
        <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
          <button
            type="button"
            className="btn btn-primary"
            onClick={() => openEditor()}
          >
            <Plus size={15} />
            {dict.profiles.newProfile}
          </button>
          <button type="button" className="btn" onClick={onImportTomlFile}>
            <FileUp size={15} />
            {dict.profiles.importFile}
          </button>
          <button
            type="button"
            className="btn"
            onClick={() => setPasteModalOpen(true)}
          >
            <ClipboardPaste size={15} />
            {dict.profiles.pasteToml}
          </button>
        </div>
      </div>

      {profiles.length === 0 ? (
        <div className="card empty-state">
          <Server size={36} strokeWidth={1.5} />
          <div style={{ fontWeight: 600, color: "var(--text-primary)" }}>
            {dict.profiles.emptyTitle}
          </div>
          <div style={{ maxWidth: 420, fontSize: 12.5 }}>
            {dict.profiles.emptyDesc}
          </div>
        </div>
      ) : (
        <div style={{ display: "flex", flexDirection: "column", gap: 10 }}>
          {profiles.map((p) => {
            const isActive = p.id === activeProfileId;
            const sess = sessionsByProfile[p.id];
            const sessState = sess?.state || "idle";
            const isSessConnected = sessState === "connected";
            const isSessBusy =
              sessState === "connecting" ||
              sessState === "authenticating" ||
              sessState === "reconnecting" ||
              sessState === "disconnecting";
            return (
              <div
                key={p.id}
                className={`profile-card ${isActive ? "active" : ""}`}
              >
                <div className="profile-main">
                  <div className="profile-title-row">
                    <span className="profile-name">{p.name}</span>
                    <span
                      className={`badge ${p.protocol === "atrust" ? "badge-accent" : "badge-neutral"}`}
                    >
                      {p.protocol === "atrust" ? "aTrust" : "EasyConnect"}
                    </span>
                    {isSessConnected && (
                      <span className="badge badge-success mono">
                        {dict.state.connected}
                        {sess?.virtualIp ? ` · ${sess.virtualIp}` : ""}
                      </span>
                    )}
                    {isSessBusy && (
                      <span className="badge badge-warning">
                        {dict.state[sessState]}
                      </span>
                    )}
                    {sessState === "error" && (
                      <span className="badge badge-danger">
                        {dict.state.error}
                      </span>
                    )}
                    {isActive && (
                      <span className="badge badge-neutral">
                        <CheckCircle2 size={11} />
                        {dict.profiles.active}
                      </span>
                    )}
                    {(p.hasPassword || p.hasTotp) && (
                      <span className="badge badge-neutral">
                        <KeyRound size={11} />
                        {dict.profiles.keychainSaved}
                      </span>
                    )}
                  </div>
                  <div className="profile-meta mono">
                    <span>
                      {p.serverAddress}:{p.serverPort}
                    </span>
                    {p.username && <span>@{p.username}</span>}
                    <span>SOCKS5 {sess?.socksAddr || p.socksBind}</span>
                    <span>HTTP {sess?.httpAddr || p.httpBind}</span>
                    {p.tunMode && (
                      <span className="badge badge-success">Split-TUN</span>
                    )}
                    {p.proxyAll && (
                      <span className="badge badge-warning">Proxy All</span>
                    )}
                  </div>
                </div>

                <div className="profile-actions">
                  {!isActive && (
                    <button
                      type="button"
                      className="btn btn-sm"
                      onClick={() => AppService.setActiveProfile(p.id)}
                    >
                      {dict.profiles.setActive}
                    </button>
                  )}
                  {isSessConnected || isSessBusy ? (
                    <button
                      type="button"
                      className="btn btn-sm btn-danger"
                      onClick={() => onDisconnectProfile(p.id)}
                    >
                      <Square size={12} />
                      {dict.profiles.disconnectNow}
                    </button>
                  ) : (
                    <button
                      type="button"
                      className="btn btn-sm btn-primary"
                      onClick={() => onConnectProfile(p.id)}
                    >
                      <Play size={13} />
                      {dict.profiles.connectNow}
                    </button>
                  )}
                  {p.protocol === "atrust" && (
                    <button
                      type="button"
                      className="btn btn-sm"
                      title={dict.profiles.clearSession}
                      onClick={() => handleClearSession(p)}
                    >
                      <RefreshCw size={13} />
                    </button>
                  )}
                  <button
                    type="button"
                    className="btn btn-sm"
                    title={dict.common.edit}
                    onClick={() => openEditor(p)}
                  >
                    <Edit3 size={13} />
                  </button>
                  <button
                    type="button"
                    className="btn btn-sm btn-danger"
                    title={dict.common.delete}
                    onClick={() => handleDelete(p)}
                  >
                    <Trash2 size={13} />
                  </button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Profile Editor Modal */}
      {editing && (
        <div className="modal-backdrop">
          <div className="modal-panel wide" role="dialog" aria-modal="true">
            <div className="modal-header">
              <h3 className="modal-title">
                {editing.id ? dict.profiles.editProfile : dict.profiles.newProfile}
              </h3>
              <button
                type="button"
                className="btn btn-ghost btn-icon"
                onClick={closeEditor}
              >
                <X size={16} />
              </button>
            </div>

            <form
              onSubmit={handleSave}
              style={{ display: "flex", flexDirection: "column", minHeight: 0, flex: 1 }}
            >
              <div className="modal-body">
                {/* General */}
                <div className="section-heading">{dict.profiles.secBasic}</div>
                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.name} *</label>
                    <input
                      className="input"
                      required
                      value={editing.name}
                      onChange={(e) =>
                        setEditing({ ...editing, name: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">{dict.profiles.protocol}</label>
                    <select
                      className="select"
                      value={editing.protocol}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          protocol: e.target.value as Protocol,
                        })
                      }
                    >
                      <option value="easyconnect">EasyConnect</option>
                      <option value="atrust">aTrust</option>
                    </select>
                  </div>
                </div>

                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">
                      {dict.profiles.serverAddress} *
                    </label>
                    <input
                      className="input mono"
                      required
                      placeholder="vpn.example.edu.cn"
                      value={editing.serverAddress}
                      onChange={(e) =>
                        setEditing({ ...editing, serverAddress: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">{dict.profiles.serverPort}</label>
                    <input
                      className="input mono"
                      type="number"
                      min={1}
                      max={65535}
                      value={editing.serverPort}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          serverPort: Number(e.target.value),
                        })
                      }
                    />
                  </div>
                </div>

                {/* Credentials */}
                <div className="section-heading">{dict.profiles.secCredentials}</div>
                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.username}</label>
                    <input
                      className="input"
                      value={editing.username || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, username: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">
                      <span>{dict.profiles.password}</span>
                      {editing.hasPassword && (
                        <label
                          style={{
                            display: "inline-flex",
                            alignItems: "center",
                            gap: 4,
                            fontSize: 11,
                            color: "var(--danger)",
                            cursor: "pointer",
                          }}
                        >
                          <input
                            type="checkbox"
                            checked={clearPassword}
                            onChange={(e) => setClearPassword(e.target.checked)}
                          />
                          {dict.profiles.clearSecret}
                        </label>
                      )}
                    </label>
                    <input
                      className="input"
                      type="password"
                      disabled={clearPassword}
                      placeholder={
                        editing.hasPassword ? dict.profiles.passwordKeepHint : ""
                      }
                      value={editing.password || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, password: e.target.value })
                      }
                    />
                  </div>
                </div>

                <div className="field">
                  <label className="field-label">
                    <span>{dict.profiles.totpSecret}</span>
                    {editing.hasTotp && (
                      <label
                        style={{
                          display: "inline-flex",
                          alignItems: "center",
                          gap: 4,
                          fontSize: 11,
                          color: "var(--danger)",
                          cursor: "pointer",
                        }}
                      >
                        <input
                          type="checkbox"
                          checked={clearTotp}
                          onChange={(e) => setClearTotp(e.target.checked)}
                        />
                        {dict.profiles.clearSecret}
                      </label>
                    )}
                  </label>
                  <input
                    className="input mono"
                    type="password"
                    disabled={clearTotp}
                    placeholder={
                      editing.hasTotp ? dict.profiles.totpKeepHint : "Base32 TOTP secret (optional)"
                    }
                    value={editing.totpSecret || ""}
                    onChange={(e) =>
                      setEditing({ ...editing, totpSecret: e.target.value })
                    }
                  />
                </div>

                {/* Protocol Specific */}
                {editing.protocol === "easyconnect" ? (
                  <>
                    <div className="section-heading">
                      {dict.profiles.secEasyConnect}
                    </div>
                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.certFile}
                        </label>
                        <input
                          className="input mono"
                          value={editing.certFile || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, certFile: e.target.value })
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          <span>{dict.profiles.certPassword}</span>
                          {editing.hasCertPass && (
                            <label
                              style={{
                                display: "inline-flex",
                                alignItems: "center",
                                gap: 4,
                                fontSize: 11,
                                color: "var(--danger)",
                                cursor: "pointer",
                              }}
                            >
                              <input
                                type="checkbox"
                                checked={clearCertPass}
                                onChange={(e) => setClearCertPass(e.target.checked)}
                              />
                              {dict.profiles.clearSecret}
                            </label>
                          )}
                        </label>
                        <input
                          className="input"
                          type="password"
                          disabled={clearCertPass}
                          placeholder={
                            editing.hasCertPass ? dict.profiles.passwordKeepHint : ""
                          }
                          value={editing.certPassword || ""}
                          onChange={(e) =>
                            setEditing({
                              ...editing,
                              certPassword: e.target.value,
                            })
                          }
                        />
                      </div>
                    </div>

                    <div className="field">
                      <label className="field-label">{dict.profiles.twfId}</label>
                      <input
                        className="input mono"
                        value={editing.twfId || ""}
                        onChange={(e) =>
                          setEditing({ ...editing, twfId: e.target.value })
                        }
                      />
                    </div>

                    <div className="grid-2">
                      <CheckboxRow
                        label={dict.profiles.disableMultiLine}
                        checked={!!editing.disableMultiLine}
                        onChange={(v) =>
                          setEditing({ ...editing, disableMultiLine: v })
                        }
                      />
                      <CheckboxRow
                        label={dict.profiles.disableServerConfig}
                        checked={!!editing.disableServerConfig}
                        onChange={(v) =>
                          setEditing({ ...editing, disableServerConfig: v })
                        }
                      />
                      <CheckboxRow
                        label={dict.profiles.skipDomainResource}
                        checked={!!editing.skipDomainResource}
                        onChange={(v) =>
                          setEditing({ ...editing, skipDomainResource: v })
                        }
                      />
                      <CheckboxRow
                        label={dict.profiles.disableZjuConfig}
                        checked={!!editing.disableZjuConfig}
                        onChange={(v) =>
                          setEditing({ ...editing, disableZjuConfig: v })
                        }
                      />
                    </div>

                    <div className="field">
                      <label className="field-label">
                        {dict.profiles.customProxyDomains}
                      </label>
                      <textarea
                        className="textarea mono"
                        placeholder={"internal.example.edu\n*.corp.local"}
                        value={customDomainsText}
                        onChange={(e) => setCustomDomainsText(e.target.value)}
                      />
                    </div>
                  </>
                ) : (
                  <>
                    <div className="section-heading">{dict.profiles.secATrust}</div>
                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.authType}
                        </label>
                        <select
                          className="select"
                          value={editing.authType || ""}
                          onChange={(e) =>
                            setEditing({
                              ...editing,
                              authType: e.target.value as ATrustAuthType,
                            })
                          }
                        >
                          <option value="">{dict.profiles.authTypeAuto}</option>
                          <option value="auth/psw">
                            {dict.profiles.authTypePsw}
                          </option>
                          <option value="auth/cas">
                            {dict.profiles.authTypeCas}
                          </option>
                          <option value="auth/httpsOauth2">
                            {dict.profiles.authTypeOAuth2}
                          </option>
                          <option value="auth/smsCheckCode">
                            {dict.profiles.authTypeSms}
                          </option>
                        </select>
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.loginDomain}
                        </label>
                        <input
                          className="input"
                          placeholder={dict.profiles.loginDomainPlaceholder}
                          value={editing.loginDomain || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, loginDomain: e.target.value })
                          }
                        />
                      </div>
                    </div>

                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">{dict.profiles.phone}</label>
                        <input
                          className="input mono"
                          value={editing.phone || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, phone: e.target.value })
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.deviceId}
                        </label>
                        <input
                          className="input mono"
                          value={editing.deviceId || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, deviceId: e.target.value })
                          }
                        />
                      </div>
                    </div>

                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">{dict.profiles.sid}</label>
                        <input
                          className="input mono"
                          value={editing.sid || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, sid: e.target.value })
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.signKey}
                        </label>
                        <input
                          className="input mono"
                          value={editing.signKey || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, signKey: e.target.value })
                          }
                        />
                      </div>
                    </div>

                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.casTicket}
                        </label>
                        <input
                          className="input mono"
                          value={editing.casTicket || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, casTicket: e.target.value })
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.oauth2Code}
                        </label>
                        <input
                          className="input mono"
                          value={editing.oauth2Code || ""}
                          onChange={(e) =>
                            setEditing({ ...editing, oauth2Code: e.target.value })
                          }
                        />
                      </div>
                    </div>

                    <div className="grid-3">
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.macAddresses}
                        </label>
                        <input
                          className="input mono"
                          value={macsText}
                          onChange={(e) => setMacsText(e.target.value)}
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.bestNodesInterval}
                        </label>
                        <input
                          className="input mono"
                          type="number"
                          min={30}
                          value={editing.updateBestNodesInterval || 300}
                          onChange={(e) =>
                            setEditing({
                              ...editing,
                              updateBestNodesInterval: Number(e.target.value),
                            })
                          }
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.sessionRefreshInterval}
                        </label>
                        <input
                          className="input mono"
                          type="number"
                          min={60}
                          value={editing.sessionRefreshInterval || 1800}
                          onChange={(e) =>
                            setEditing({
                              ...editing,
                              sessionRefreshInterval: Number(e.target.value),
                            })
                          }
                        />
                      </div>
                    </div>
                  </>
                )}

                {/* Proxy & Routing */}
                <div className="section-heading">{dict.profiles.secProxy}</div>
                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.socksBind}</label>
                    <input
                      className="input mono"
                      value={editing.socksBind}
                      onChange={(e) =>
                        setEditing({ ...editing, socksBind: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">{dict.profiles.httpBind}</label>
                    <input
                      className="input mono"
                      value={editing.httpBind}
                      onChange={(e) =>
                        setEditing({ ...editing, httpBind: e.target.value })
                      }
                    />
                  </div>
                </div>

                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.socksUser}</label>
                    <input
                      className="input"
                      value={editing.socksUser || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, socksUser: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">
                      <span>{dict.profiles.socksPasswd}</span>
                      {editing.hasSocksPass && (
                        <label
                          style={{
                            display: "inline-flex",
                            alignItems: "center",
                            gap: 4,
                            fontSize: 11,
                            color: "var(--danger)",
                            cursor: "pointer",
                          }}
                        >
                          <input
                            type="checkbox"
                            checked={clearSocksPass}
                            onChange={(e) => setClearSocksPass(e.target.checked)}
                          />
                          {dict.profiles.clearSecret}
                        </label>
                      )}
                    </label>
                    <input
                      className="input"
                      type="password"
                      disabled={clearSocksPass}
                      placeholder={
                        editing.hasSocksPass ? dict.profiles.passwordKeepHint : ""
                      }
                      value={editing.socksPasswd || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, socksPasswd: e.target.value })
                      }
                    />
                  </div>
                </div>

                <div className="grid-2">
                  <CheckboxRow
                    label={dict.profiles.proxyAll}
                    checked={!!editing.proxyAll}
                    onChange={(v) => setEditing({ ...editing, proxyAll: v })}
                  />
                  <CheckboxRow
                    label={dict.profiles.tcpTunnelMode}
                    checked={!!editing.tcpTunnelMode}
                    onChange={(v) => setEditing({ ...editing, tcpTunnelMode: v })}
                  />
                </div>

                <div
                  style={{
                    padding: "12px 14px",
                    borderRadius: 10,
                    background: "var(--bg-input)",
                    border: "1px solid var(--border-subtle)",
                    display: "flex",
                    flexDirection: "column",
                    gap: 10,
                  }}
                >
                  <CheckboxRow
                    label={dict.profiles.tunMode}
                    checked={!!editing.tunMode}
                    onChange={(v) => setEditing({ ...editing, tunMode: v })}
                  />
                  <div className="field-hint">{dict.profiles.tunModeHint}</div>
                  {editing.tunMode && (
                    <div className="grid-2">
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.tunCustomRoutes}
                        </label>
                        <textarea
                          className="textarea mono"
                          style={{ minHeight: 68 }}
                          placeholder={"192.168.215.0/24\n10.20.0.0/16"}
                          value={tunCustomText}
                          onChange={(e) => setTunCustomText(e.target.value)}
                        />
                      </div>
                      <div className="field">
                        <label className="field-label">
                          {dict.profiles.tunExcludeRoutes}
                        </label>
                        <textarea
                          className="textarea mono"
                          style={{ minHeight: 68 }}
                          placeholder={"192.168.1.0/24"}
                          value={tunExcludeText}
                          onChange={(e) => setTunExcludeText(e.target.value)}
                        />
                      </div>
                    </div>
                  )}
                </div>

                <div className="field">
                  <label className="field-label">
                    {dict.profiles.dialDirectProxy}
                  </label>
                  <input
                    className="input mono"
                    placeholder="socks5://127.0.0.1:7890"
                    value={editing.dialDirectProxy || ""}
                    onChange={(e) =>
                      setEditing({ ...editing, dialDirectProxy: e.target.value })
                    }
                  />
                </div>

                {/* DNS */}
                <div className="section-heading">{dict.profiles.secDns}</div>
                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.remoteDns}</label>
                    <input
                      className="input mono"
                      value={editing.remoteDnsServer || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, remoteDnsServer: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">
                      {dict.profiles.secondaryDns}
                    </label>
                    <input
                      className="input mono"
                      value={editing.secondaryDnsServer || ""}
                      onChange={(e) =>
                        setEditing({
                          ...editing,
                          secondaryDnsServer: e.target.value,
                        })
                      }
                    />
                  </div>
                </div>

                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">{dict.profiles.localDns}</label>
                    <input
                      className="input mono"
                      value={editing.localDnsServer || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, localDnsServer: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">{dict.profiles.dnsTtl}</label>
                    <input
                      className="input mono"
                      type="number"
                      min={60}
                      value={editing.dnsTtl || 3600}
                      onChange={(e) =>
                        setEditing({ ...editing, dnsTtl: Number(e.target.value) })
                      }
                    />
                  </div>
                </div>

                <CheckboxRow
                  label={dict.profiles.disableRemoteDns}
                  checked={!!editing.disableRemoteDns}
                  onChange={(v) => setEditing({ ...editing, disableRemoteDns: v })}
                />

                <div className="field">
                  <div className="field-label">
                    <span>{dict.profiles.customDns}</span>
                    <button
                      type="button"
                      className="btn btn-sm"
                      onClick={() =>
                        setEditing({
                          ...editing,
                          customDns: [
                            ...(editing.customDns || []),
                            { hostName: "", ip: "" },
                          ],
                        })
                      }
                    >
                      <Plus size={12} />
                      {dict.profiles.addCustomDns}
                    </button>
                  </div>
                  {(editing.customDns || []).map((row, idx) => (
                    <div
                      key={idx}
                      style={{ display: "flex", gap: 8, alignItems: "center" }}
                    >
                      <input
                        className="input mono"
                        placeholder="host.internal"
                        value={row.hostName}
                        onChange={(e) =>
                          updateCustomDns(idx, "hostName", e.target.value)
                        }
                      />
                      <input
                        className="input mono"
                        placeholder="10.0.0.5"
                        value={row.ip}
                        onChange={(e) => updateCustomDns(idx, "ip", e.target.value)}
                      />
                      <button
                        type="button"
                        className="btn btn-icon btn-danger"
                        onClick={() =>
                          setEditing({
                            ...editing,
                            customDns: (editing.customDns || []).filter(
                              (_, i) => i !== idx,
                            ),
                          })
                        }
                      >
                        <X size={14} />
                      </button>
                    </div>
                  ))}
                </div>

                {/* Keepalive & Advanced */}
                <div className="section-heading">{dict.profiles.secAdvanced}</div>
                <div className="grid-2">
                  <CheckboxRow
                    label={dict.profiles.autoReconnect}
                    checked={editing.autoReconnect}
                    onChange={(v) => setEditing({ ...editing, autoReconnect: v })}
                  />
                  <CheckboxRow
                    label={dict.profiles.disableKeepAlive}
                    checked={!!editing.disableKeepAlive}
                    onChange={(v) =>
                      setEditing({ ...editing, disableKeepAlive: v })
                    }
                  />
                  <CheckboxRow
                    label={dict.profiles.autoDetectInterface}
                    checked={!!editing.autoDetectInterface}
                    onChange={(v) =>
                      setEditing({ ...editing, autoDetectInterface: v })
                    }
                  />
                  <CheckboxRow
                    label={dict.profiles.debugLog}
                    checked={!!editing.debugLog}
                    onChange={(v) => setEditing({ ...editing, debugLog: v })}
                  />
                </div>

                <div className="grid-2">
                  <div className="field">
                    <label className="field-label">
                      {dict.profiles.keepAliveUrl}
                    </label>
                    <input
                      className="input mono"
                      placeholder="https://internal.example.edu/health"
                      value={editing.keepAliveUrl || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, keepAliveUrl: e.target.value })
                      }
                    />
                  </div>
                  <div className="field">
                    <label className="field-label">
                      {dict.profiles.bindInterface}
                    </label>
                    <input
                      className="input mono"
                      placeholder="en0 / eth0"
                      value={editing.bindInterface || ""}
                      onChange={(e) =>
                        setEditing({ ...editing, bindInterface: e.target.value })
                      }
                    />
                  </div>
                </div>
              </div>

              <div className="modal-footer">
                <button type="button" className="btn" onClick={closeEditor}>
                  {dict.common.cancel}
                </button>
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={saving}
                >
                  {dict.common.save}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Paste TOML Modal */}
      {pasteModalOpen && (
        <div className="modal-backdrop">
          <div className="modal-panel" role="dialog" aria-modal="true">
            <div className="modal-header">
              <h3 className="modal-title">{dict.profiles.pasteTomlTitle}</h3>
              <button
                type="button"
                className="btn btn-ghost btn-icon"
                onClick={() => setPasteModalOpen(false)}
              >
                <X size={16} />
              </button>
            </div>
            <form onSubmit={handleImportRawToml}>
              <div className="modal-body">
                <div className="field-hint">{dict.profiles.pasteTomlHint}</div>
                <div className="field">
                  <label className="field-label">
                    {dict.profiles.tomlNameLabel}
                  </label>
                  <input
                    className="input"
                    placeholder={dict.profiles.tomlNamePlaceholder}
                    value={tomlName}
                    onChange={(e) => setTomlName(e.target.value)}
                  />
                </div>
                <div className="field">
                  <label className="field-label">
                    {dict.profiles.tomlContentLabel}
                  </label>
                  <textarea
                    className="textarea mono"
                    style={{ minHeight: 180 }}
                    required
                    placeholder={`protocol = "atrust"\nserver_address = "vpn.example.edu.cn"\nserver_port = 443\nusername = "alice"`}
                    value={tomlBody}
                    onChange={(e) => setTomlBody(e.target.value)}
                  />
                </div>
              </div>
              <div className="modal-footer">
                <button
                  type="button"
                  className="btn"
                  onClick={() => setPasteModalOpen(false)}
                >
                  {dict.common.cancel}
                </button>
                <button
                  type="submit"
                  className="btn btn-primary"
                  disabled={importing || !tomlBody.trim()}
                >
                  {dict.common.confirm}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
};

const CheckboxRow: React.FC<{
  label: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}> = ({ label, checked, onChange }) => (
  <label
    style={{
      display: "flex",
      alignItems: "center",
      gap: 8,
      cursor: "pointer",
      fontSize: 13,
      color: "var(--text-primary)",
    }}
  >
    <input
      type="checkbox"
      checked={checked}
      onChange={(e) => onChange(e.target.checked)}
    />
    {label}
  </label>
);
