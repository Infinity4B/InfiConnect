package core

import (
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// zjuConnectTOML mirrors upstream zju-connect/configs.Config TOML tags so we can
// import existing zju-connect config files cleanly without leaking upstream config
// structs above the core layer.
type zjuConnectTOML struct {
	Protocol            string `toml:"protocol"`
	ServerAddress       string `toml:"server_address"`
	ServerPort          int    `toml:"server_port"`
	Username            string `toml:"username"`
	Password            string `toml:"password"`
	TOTPSecret          string `toml:"totp_secret"`
	SocksBind           string `toml:"socks_bind"`
	SocksUser           string `toml:"socks_user"`
	SocksPasswd         string `toml:"socks_passwd"`
	HTTPBind            string `toml:"http_bind"`
	DialDirectProxy     string `toml:"dial_direct_proxy"`
	DisableServerConfig bool   `toml:"disable_server_config"`
	DisableRemoteDNS    bool   `toml:"disable_remote_dns"`
	DNSTTL              uint64 `toml:"dns_ttl"`
	RemoteDNSServer     string `toml:"remote_dns_server"`
	SecondaryDNSServer  string `toml:"secondary_dns_server"`
	LocalDNSServer      string `toml:"local_dns_server"`
	CustomDNSList       []struct {
		HostName string `toml:"host_name"`
		IP       string `toml:"ip"`
	} `toml:"custom_dns"`
	DisableKeepAlive    bool   `toml:"disable_keep_alive"`
	KeepAliveURL        string `toml:"keep_alive_url"`
	TCPTunnelMode       bool   `toml:"tcp_tunnel_mode"`
	TUNMode             *bool  `toml:"tun_mode"`
	ProxyAll            bool   `toml:"proxy_all"`
	DebugDump           bool   `toml:"debug_dump"`
	BindInterface       string `toml:"bind_interface"`
	AutoDetectInterface bool   `toml:"auto_detect_interface"`

	// EasyConnect fields
	CertFile           string   `toml:"cert_file"`
	CertPassword       string   `toml:"cert_password"`
	DisableZJUConfig   bool     `toml:"disable_zju_config"`
	SkipDomainResource bool     `toml:"skip_domain_resource"`
	DisableMultiLine   bool     `toml:"disable_multi_line"`
	CustomProxyDomain  []string `toml:"custom_proxy_domain"`
	TwfID              string   `toml:"twf_id"`

	// aTrust fields
	AuthType                string   `toml:"auth_type"`
	Phone                   string   `toml:"phone"`
	LoginDomain             string   `toml:"login_domain"`
	ClientDataFile          string   `toml:"client_data_file"`
	CasTicket               string   `toml:"cas_ticket"`
	OAuth2Code              string   `toml:"oauth2_code"`
	SID                     string   `toml:"sid"`
	DeviceID                string   `toml:"device_id"`
	MACAddresses            []string `toml:"mac_addresses"`
	SignKey                 string   `toml:"sign_key"`
	UpdateBestNodesInterval int      `toml:"update_best_nodes_interval"`
	SessionRefreshInterval  int      `toml:"session_refresh_interval"`
}

// ImportZJUConnectTOML parses a ZJU-Connect TOML configuration file into an
// InfiConnect Profile.
func ImportZJUConnectTOML(data []byte, suggestedName string) (Profile, error) {
	var raw zjuConnectTOML
	if err := toml.Unmarshal(data, &raw); err != nil {
		return Profile{}, fmt.Errorf("parse TOML config: %w", err)
	}

	proto := ProtocolEasyConnect
	switch strings.ToLower(strings.TrimSpace(raw.Protocol)) {
	case "", "easyconnect":
		proto = ProtocolEasyConnect
	case "atrust":
		proto = ProtocolATrust
	default:
		return Profile{}, fmt.Errorf("unsupported protocol in TOML: %q", raw.Protocol)
	}

	name := strings.TrimSpace(suggestedName)
	if name == "" {
		if raw.ServerAddress != "" {
			name = raw.ServerAddress
		} else {
			name = fmt.Sprintf("Imported (%s)", proto)
		}
	}

	tunMode := true
	if raw.TUNMode != nil {
		tunMode = *raw.TUNMode
	}

	p := Profile{
		Name:                    name,
		Protocol:                proto,
		ServerAddress:           strings.TrimSpace(raw.ServerAddress),
		ServerPort:              raw.ServerPort,
		Username:                raw.Username,
		Password:                raw.Password,
		TOTPSecret:              raw.TOTPSecret,
		CertFile:                raw.CertFile,
		CertPassword:            raw.CertPassword,
		TwfID:                   raw.TwfID,
		DisableMultiLine:        raw.DisableMultiLine,
		DisableServerConfig:     raw.DisableServerConfig,
		SkipDomainResource:      raw.SkipDomainResource,
		DisableZJUConfig:        raw.DisableZJUConfig,
		CustomProxyDomains:      raw.CustomProxyDomain,
		AuthType:                ATrustAuthType(raw.AuthType),
		Phone:                   raw.Phone,
		LoginDomain:             raw.LoginDomain,
		CASTicket:               raw.CasTicket,
		OAuth2Code:              raw.OAuth2Code,
		SID:                     raw.SID,
		DeviceID:                raw.DeviceID,
		MACAddresses:            raw.MACAddresses,
		SignKey:                 raw.SignKey,
		ClientDataPath:          raw.ClientDataFile,
		UpdateBestNodesInterval: raw.UpdateBestNodesInterval,
		SessionRefreshInterval:  raw.SessionRefreshInterval,
		SocksBind:               normalizeBindDefault(raw.SocksBind, "127.0.0.1:1080"),
		SocksUser:               raw.SocksUser,
		SocksPasswd:             raw.SocksPasswd,
		HTTPBind:                normalizeBindDefault(raw.HTTPBind, "127.0.0.1:1081"),
		ProxyAll:                raw.ProxyAll,
		DialDirectProxy:         raw.DialDirectProxy,
		TCPTunnelMode:           raw.TCPTunnelMode,
		TUNMode:                 tunMode,
		DisableRemoteDNS:        raw.DisableRemoteDNS,
		RemoteDNSServer:         raw.RemoteDNSServer,
		SecondaryDNSServer:      raw.SecondaryDNSServer,
		LocalDNSServer:          raw.LocalDNSServer,
		DNSTTL:                  raw.DNSTTL,
		DisableKeepAlive:        raw.DisableKeepAlive,
		KeepAliveURL:            raw.KeepAliveURL,
		BindInterface:           raw.BindInterface,
		AutoDetectInterface:     raw.AutoDetectInterface,
		AutoReconnect:           true,
		DebugLog:                raw.DebugDump,
	}

	for _, c := range raw.CustomDNSList {
		if c.HostName != "" && c.IP != "" {
			p.CustomDNS = append(p.CustomDNS, CustomDNS{HostName: c.HostName, IP: c.IP})
		}
	}

	p.ApplyDefaults()
	return p, nil
}

func normalizeBindDefault(val, fallback string) string {
	v := strings.TrimSpace(val)
	if v == "" {
		return fallback
	}
	if strings.HasPrefix(v, ":") {
		return "127.0.0.1" + v
	}
	return v
}
