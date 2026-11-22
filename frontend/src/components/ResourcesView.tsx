import React, { useEffect, useMemo, useState } from "react";
import {
  CheckCircle2,
  Compass,
  Globe,
  Network,
  Search,
  Server,
  Shield,
  XCircle,
} from "lucide-react";
import {
  AppService,
  type ResourceSnapshot,
  type RouteCheckResult,
  type StatusSnapshot,
} from "../mygo";
import type { Dict } from "../i18n";

interface ResourcesViewProps {
  connectedSessions: StatusSnapshot[];
  resourcesByProfile: Record<string, ResourceSnapshot>;
  dict: Dict;
  onNotify: (msg: string, kind?: "success" | "error") => void;
}

type ResourceTab = "ip" | "domain" | "dns";

const EMPTY_RESOURCES: ResourceSnapshot = {
  ipResources: [],
  domainResources: [],
  dnsOverrides: [],
  proxyAll: false,
};

export const ResourcesView: React.FC<ResourcesViewProps> = ({
  connectedSessions,
  resourcesByProfile,
  dict,
  onNotify,
}) => {
  const [selectedProfileId, setSelectedProfileId] = useState<string>("");
  const [routeScope, setRouteScope] = useState<string>(""); // "" = all connected VPNs
  const [tab, setTab] = useState<ResourceTab>("ip");
  const [query, setQuery] = useState("");
  const [routeTarget, setRouteTarget] = useState("");
  const [routePort, setRoutePort] = useState("443");
  const [routeProto, setRouteProto] = useState("tcp");
  const [routeResult, setRouteResult] = useState<RouteCheckResult | null>(null);
  const [checking, setChecking] = useState(false);

  const isConnected = connectedSessions.length > 0;

  useEffect(() => {
    if (connectedSessions.length === 0) {
      setSelectedProfileId("");
      setRouteScope("");
      return;
    }
    setSelectedProfileId((prev) => {
      if (prev && connectedSessions.some((s) => s.profileId === prev)) {
        return prev;
      }
      return connectedSessions[0].profileId || "";
    });
    setRouteScope((prev) => {
      if (prev === "") return "";
      if (connectedSessions.some((s) => s.profileId === prev)) return prev;
      return "";
    });
  }, [connectedSessions]);

  const resources = useMemo(() => {
    if (selectedProfileId && resourcesByProfile[selectedProfileId]) {
      return resourcesByProfile[selectedProfileId];
    }
    const first = connectedSessions[0]?.profileId;
    if (first && resourcesByProfile[first]) {
      return resourcesByProfile[first];
    }
    return EMPTY_RESOURCES;
  }, [selectedProfileId, connectedSessions, resourcesByProfile]);

  const q = query.trim().toLowerCase();

  const filteredIPs = useMemo(() => {
    const list = resources.ipResources || [];
    if (!q) return list;
    return list.filter(
      (r) =>
        (r.cidr && r.cidr.toLowerCase().includes(q)) ||
        r.ipMin.toLowerCase().includes(q) ||
        r.ipMax.toLowerCase().includes(q) ||
        r.protocol.toLowerCase().includes(q),
    );
  }, [resources.ipResources, q]);

  const filteredDomains = useMemo(() => {
    const list = resources.domainResources || [];
    if (!q) return list;
    return list.filter(
      (r) =>
        r.domain.toLowerCase().includes(q) ||
        r.protocol.toLowerCase().includes(q),
    );
  }, [resources.domainResources, q]);

  const filteredDNS = useMemo(() => {
    const list = resources.dnsOverrides || [];
    if (!q) return list;
    return list.filter(
      (r) =>
        r.host.toLowerCase().includes(q) ||
        r.ips.some((ip) => ip.toLowerCase().includes(q)),
    );
  }, [resources.dnsOverrides, q]);

  const handleCheckRoute = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!routeTarget.trim() || checking) return;
    setChecking(true);
    try {
      const res = await AppService.checkRoute(
        routeScope,
        routeTarget.trim(),
        Number(routePort) || 443,
        routeProto,
      );
      setRouteResult(res);
    } catch (err) {
      onNotify(String(err), "error");
    } finally {
      setChecking(false);
    }
  };

  return (
    <div className="page-stack">
      {/* Connection Switcher when multiple VPNs are connected */}
      {connectedSessions.length > 1 && (
        <div
          className="card"
          style={{
            padding: "12px 16px",
            display: "flex",
            alignItems: "center",
            justifyContent: "space-between",
            gap: 12,
            flexWrap: "wrap",
          }}
        >
          <span style={{ fontSize: 12.5, fontWeight: 600, color: "var(--text-secondary)" }}>
            {dict.resources.selectConnection}
          </span>
          <div className="tabs">
            {connectedSessions.map((s) => {
              const pid = s.profileId || "";
              return (
                <button
                  key={pid}
                  type="button"
                  className={`tab-btn ${selectedProfileId === pid ? "active" : ""}`}
                  onClick={() => setSelectedProfileId(pid)}
                >
                  {s.profileName || pid}
                  {s.virtualIp ? ` (${s.virtualIp})` : ""}
                </button>
              );
            })}
          </div>
        </div>
      )}

      {/* Summary Stats */}
      <div className="grid-3">
        <div className="stat-tile">
          <span className="stat-label">
            <Network size={13} />
            {dict.resources.virtualIp}
          </span>
          <span className="stat-value mono">{resources.virtualIp || "—"}</span>
        </div>
        <div className="stat-tile">
          <span className="stat-label">
            <Server size={13} />
            {dict.resources.dnsServers}
          </span>
          <span className="stat-value mono" style={{ fontSize: 13.5 }}>
            {resources.dnsServers && resources.dnsServers.length > 0
              ? resources.dnsServers.join(", ")
              : "—"}
          </span>
        </div>
        <div className="stat-tile">
          <span className="stat-label">
            <Shield size={13} />
            {dict.resources.routingMode}
          </span>
          <span className="stat-value" style={{ fontSize: 14.5 }}>
            {resources.proxyAll
              ? dict.resources.modeGlobal
              : dict.resources.modeSplit}
          </span>
        </div>
      </div>

      {/* Route Checker */}
      <div className="card">
        <div className="card-header" style={{ marginBottom: 10 }}>
          <div>
            <h3 className="card-title">
              <Compass size={16} color="var(--accent)" />
              {dict.resources.routeCheckerTitle}
            </h3>
            <p className="card-subtitle">{dict.resources.routeCheckerDesc}</p>
          </div>
          {connectedSessions.length > 1 && (
            <select
              className="select"
              style={{ width: "auto", minWidth: 170 }}
              value={routeScope}
              onChange={(e) => setRouteScope(e.target.value)}
            >
              <option value="">{dict.resources.routeScopeAll}</option>
              {connectedSessions.map((s) => (
                <option key={s.profileId} value={s.profileId}>
                  {s.profileName || s.profileId}
                </option>
              ))}
            </select>
          )}
        </div>

        <form
          onSubmit={handleCheckRoute}
          style={{ display: "flex", gap: 8, flexWrap: "wrap" }}
        >
          <input
            className="input mono"
            style={{ flex: 1, minWidth: 240 }}
            placeholder={dict.resources.routePlaceholder}
            value={routeTarget}
            onChange={(e) => setRouteTarget(e.target.value)}
          />
          <input
            className="input mono"
            style={{ width: 88 }}
            type="number"
            min={1}
            max={65535}
            value={routePort}
            onChange={(e) => setRoutePort(e.target.value)}
            title="Default Port"
          />
          <select
            className="select"
            style={{ width: 92 }}
            value={routeProto}
            onChange={(e) => setRouteProto(e.target.value)}
          >
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
            <option value="icmp">ICMP</option>
          </select>
          <button
            type="submit"
            className="btn btn-primary"
            disabled={checking || !routeTarget.trim()}
          >
            {dict.resources.checkBtn}
          </button>
        </form>

        {routeResult && (
          <div
            style={{
              marginTop: 12,
              padding: "10px 14px",
              borderRadius: 8,
              background: "var(--bg-input)",
              border: "1px solid var(--border-subtle)",
              display: "flex",
              alignItems: "center",
              justifyContent: "space-between",
              gap: 12,
              flexWrap: "wrap",
            }}
          >
            <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
              <span
                className={`badge ${routeResult.viaVpn ? "badge-success" : "badge-neutral"}`}
              >
                {routeResult.viaVpn ? (
                  <CheckCircle2 size={13} />
                ) : (
                  <XCircle size={13} />
                )}
                {routeResult.viaVpn
                  ? dict.resources.viaVpn
                  : dict.resources.viaDirect}
              </span>
              {routeResult.viaVpn && routeResult.profileName && (
                <span className="badge badge-accent">{routeResult.profileName}</span>
              )}
              <span className="mono" style={{ fontWeight: 600 }}>
                {routeResult.host}:{routeResult.port} ({routeResult.network.toUpperCase()})
              </span>
            </div>
            <div style={{ fontSize: 12.5, color: "var(--text-secondary)" }}>
              {routeResult.reason}
              {routeResult.matchedRule && (
                <span className="mono" style={{ marginLeft: 8, color: "var(--accent)" }}>
                  [{dict.resources.matchedRule}: {routeResult.matchedRule}]
                </span>
              )}
            </div>
          </div>
        )}
      </div>

      {/* Resource Table */}
      <div className="card">
        <div
          className="card-header"
          style={{ flexWrap: "wrap", marginBottom: 12 }}
        >
          <div className="tabs">
            <button
              type="button"
              className={`tab-btn ${tab === "ip" ? "active" : ""}`}
              onClick={() => setTab("ip")}
            >
              {dict.resources.tabIp} ({(resources.ipResources || []).length})
            </button>
            <button
              type="button"
              className={`tab-btn ${tab === "domain" ? "active" : ""}`}
              onClick={() => setTab("domain")}
            >
              {dict.resources.tabDomain} ({(resources.domainResources || []).length})
            </button>
            <button
              type="button"
              className={`tab-btn ${tab === "dns" ? "active" : ""}`}
              onClick={() => setTab("dns")}
            >
              {dict.resources.tabDns} ({(resources.dnsOverrides || []).length})
            </button>
          </div>

          <div style={{ position: "relative", minWidth: 220 }}>
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
              style={{ paddingLeft: 30 }}
              placeholder={dict.common.search}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
        </div>

        {!isConnected &&
        (resources.ipResources || []).length === 0 &&
        (resources.domainResources || []).length === 0 ? (
          <div className="empty-state">
            <Globe size={32} strokeWidth={1.5} />
            <div>{dict.resources.notConnected}</div>
          </div>
        ) : (
          <div className="table-wrap">
            {tab === "ip" && (
              <table className="data-table mono">
                <thead>
                  <tr>
                    <th>{dict.resources.colRange}</th>
                    <th>{dict.resources.colPorts}</th>
                    <th>{dict.resources.colProto}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredIPs.length === 0 ? (
                    <tr>
                      <td colSpan={3} style={{ textAlign: "center", color: "var(--text-muted)" }}>
                        {dict.resources.noMatches}
                      </td>
                    </tr>
                  ) : (
                    filteredIPs.map((r, i) => (
                      <tr key={i}>
                        <td className="selectable">
                          {r.cidr || (r.ipMin === r.ipMax ? r.ipMin : `${r.ipMin} - ${r.ipMax}`)}
                        </td>
                        <td>
                          {r.portMin === r.portMax
                            ? r.portMin
                            : `${r.portMin}-${r.portMax}`}
                        </td>
                        <td>{r.protocol || "all"}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}

            {tab === "domain" && (
              <table className="data-table mono">
                <thead>
                  <tr>
                    <th>{dict.resources.colDomain}</th>
                    <th>{dict.resources.colPorts}</th>
                    <th>{dict.resources.colProto}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredDomains.length === 0 ? (
                    <tr>
                      <td colSpan={3} style={{ textAlign: "center", color: "var(--text-muted)" }}>
                        {dict.resources.noMatches}
                      </td>
                    </tr>
                  ) : (
                    filteredDomains.map((r, i) => (
                      <tr key={i}>
                        <td className="selectable">{r.domain}</td>
                        <td>
                          {r.portMin === r.portMax
                            ? r.portMin
                            : `${r.portMin}-${r.portMax}`}
                        </td>
                        <td>{r.protocol || "all"}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}

            {tab === "dns" && (
              <table className="data-table mono">
                <thead>
                  <tr>
                    <th>{dict.resources.colHost}</th>
                    <th>{dict.resources.colIps}</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredDNS.length === 0 ? (
                    <tr>
                      <td colSpan={2} style={{ textAlign: "center", color: "var(--text-muted)" }}>
                        {dict.resources.noMatches}
                      </td>
                    </tr>
                  ) : (
                    filteredDNS.map((r, i) => (
                      <tr key={i}>
                        <td className="selectable">{r.host}</td>
                        <td className="selectable">{r.ips.join(", ")}</td>
                      </tr>
                    ))
                  )}
                </tbody>
              </table>
            )}
          </div>
        )}
      </div>
    </div>
  );
};
