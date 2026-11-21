package core

import "time"

// Protocol identifies the VPN protocol supported by InfiConnect.
type Protocol string

const (
	ProtocolEasyConnect Protocol = "easyconnect"
	ProtocolATrust      Protocol = "atrust"
)

// ATrustAuthType identifies the primary login method for aTrust.
type ATrustAuthType string

const (
	ATrustAuthAuto     ATrustAuthType = ""
	ATrustAuthPassword ATrustAuthType = "auth/psw"
	ATrustAuthCAS      ATrustAuthType = "auth/cas"
	ATrustAuthOAuth2   ATrustAuthType = "auth/httpsOauth2"
	ATrustAuthSMS      ATrustAuthType = "auth/smsCheckCode"
)

// CustomDNS represents a static hostname-to-IP override.
type CustomDNS struct {
	HostName string `json:"hostName"`
	IP       string `json:"ip"`
}

// Profile is InfiConnect's own connection profile schema.
// Secrets (Password, TOTPSecret, SocksPasswd, CertPassword) are hydrated from
// the OS credential store before calling Engine.Connect and are stripped when
// persisted to JSON on disk.
type Profile struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Protocol      Protocol `json:"protocol"`
	ServerAddress string   `json:"serverAddress"`
	ServerPort    int      `json:"serverPort"`

	// Credentials (omitted from disk JSON; hydrated from keychain in memory).
	Username     string `json:"username,omitempty"`
	Password     string `json:"password,omitempty"`
	TOTPSecret   string `json:"totpSecret,omitempty"`
	HasPassword  bool   `json:"hasPassword,omitempty"`
	HasTOTP      bool   `json:"hasTotp,omitempty"`
	HasCertPass  bool   `json:"hasCertPass,omitempty"`
	HasSocksPass bool   `json:"hasSocksPass,omitempty"`

	// EasyConnect-specific options.
	CertFile           string   `json:"certFile,omitempty"`
	CertPassword       string   `json:"certPassword,omitempty"`
	TwfID              string   `json:"twfId,omitempty"`
	DisableMultiLine   bool     `json:"disableMultiLine,omitempty"`
	DisableServerConfig bool    `json:"disableServerConfig,omitempty"`
	SkipDomainResource bool     `json:"skipDomainResource,omitempty"`
	DisableZJUConfig   bool     `json:"disableZjuConfig,omitempty"`
	CustomProxyDomains []string `json:"customProxyDomains,omitempty"`

	// aTrust-specific options.
	AuthType                ATrustAuthType `json:"authType,omitempty"`
	Phone                   string         `json:"phone,omitempty"`
	LoginDomain             string         `json:"loginDomain,omitempty"`
	CASTicket               string         `json:"casTicket,omitempty"`
	OAuth2Code              string         `json:"oauth2Code,omitempty"`
	SID                     string         `json:"sid,omitempty"`
	DeviceID                string         `json:"deviceId,omitempty"`
	MACAddresses            []string       `json:"macAddresses,omitempty"`
	SignKey                 string         `json:"signKey,omitempty"`
	ClientDataPath          string         `json:"clientDataPath,omitempty"`
	UpdateBestNodesInterval int            `json:"updateBestNodesInterval,omitempty"`
	SessionRefreshInterval  int            `json:"sessionRefreshInterval,omitempty"`

	// Local proxy & routing options.
	SocksBind        string   `json:"socksBind"`
	SocksUser        string   `json:"socksUser,omitempty"`
	SocksPasswd      string   `json:"socksPasswd,omitempty"`
	HTTPBind         string   `json:"httpBind"`
	ProxyAll         bool     `json:"proxyAll,omitempty"`
	DialDirectProxy  string   `json:"dialDirectProxy,omitempty"`
	TCPTunnelMode    bool     `json:"tcpTunnelMode,omitempty"`
	TUNMode          bool     `json:"tunMode,omitempty"`
	TUNCustomRoutes  []string `json:"tunCustomRoutes,omitempty"`
	TUNExcludeRoutes []string `json:"tunExcludeRoutes,omitempty"`

	// DNS options.
	DisableRemoteDNS   bool        `json:"disableRemoteDns,omitempty"`
	RemoteDNSServer    string      `json:"remoteDnsServer,omitempty"`
	SecondaryDNSServer string      `json:"secondaryDnsServer,omitempty"`
	LocalDNSServer     string      `json:"localDnsServer,omitempty"`
	DNSTTL             uint64      `json:"dnsTtl,omitempty"`
	CustomDNS          []CustomDNS `json:"customDns,omitempty"`

	// Keepalive & network interface options.
	DisableKeepAlive    bool   `json:"disableKeepAlive,omitempty"`
	KeepAliveURL        string `json:"keepAliveUrl,omitempty"`
	BindInterface       string `json:"bindInterface,omitempty"`
	AutoDetectInterface bool   `json:"autoDetectInterface,omitempty"`
	AutoReconnect       bool   `json:"autoReconnect"`
	DebugLog            bool   `json:"debugLog,omitempty"`

	CreatedAt time.Time `json:"createdAt,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ApplyDefaults fills missing optional fields with sensible defaults.
func (p *Profile) ApplyDefaults() {
	if p.Protocol == "" {
		p.Protocol = ProtocolEasyConnect
	}
	if p.ServerPort <= 0 {
		p.ServerPort = 443
	}
	if p.SocksBind == "" {
		p.SocksBind = "127.0.0.1:1080"
	}
	if p.HTTPBind == "" {
		p.HTTPBind = "127.0.0.1:1081"
	}
	if p.DNSTTL == 0 {
		p.DNSTTL = 3600
	}
	if p.RemoteDNSServer == "" {
		p.RemoteDNSServer = "auto"
	}
	if p.SecondaryDNSServer == "" {
		p.SecondaryDNSServer = "auto"
	}
	if p.Protocol == ProtocolATrust {
		if p.UpdateBestNodesInterval <= 0 {
			p.UpdateBestNodesInterval = 300
		}
		if p.SessionRefreshInterval <= 0 {
			p.SessionRefreshInterval = 1800
		}
	}
}

// State is the VPN session lifecycle state.
type State string

const (
	StateIdle           State = "idle"
	StateConnecting     State = "connecting"
	StateAuthenticating State = "authenticating"
	StateConnected      State = "connected"
	StateReconnecting   State = "reconnecting"
	StateDisconnecting  State = "disconnecting"
	StateError          State = "error"
)

// ChallengeType identifies the interactive authentication challenge kind.
type ChallengeType string

const (
	ChallengeTypeCode          ChallengeType = "code"
	ChallengeTypeTextCaptcha   ChallengeType = "text_captcha"
	ChallengeTypeClickCaptcha  ChallengeType = "click_captcha"
	ChallengeTypeExternalLogin ChallengeType = "external_login"
)

// CodeKind identifies a code-based challenge subtype.
type CodeKind string

const (
	CodeKindSMS    CodeKind = "sms"
	CodeKindTOTP   CodeKind = "totp"
	CodeKindRadius CodeKind = "radius"
	CodeKindDevice CodeKind = "device"
)

// ExternalLoginKind identifies an external browser login subtype.
type ExternalLoginKind string

const (
	ExternalLoginCAS    ExternalLoginKind = "cas"
	ExternalLoginOAuth2 ExternalLoginKind = "oauth2"
)

// Point is an integer coordinate on a click captcha image (in original image pixels).
type Point struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// Challenge describes an active interactive authentication challenge awaiting UI input.
type Challenge struct {
	ID                   string            `json:"id"`
	Type                 ChallengeType     `json:"type"`
	CodeKind             CodeKind          `json:"codeKind,omitempty"`
	ExternalKind         ExternalLoginKind `json:"externalKind,omitempty"`
	Message              string            `json:"message,omitempty"`
	CanSkipSecondaryAuth bool              `json:"canSkipSecondaryAuth,omitempty"`
	ImageBase64          string            `json:"imageBase64,omitempty"`
	ImageMime            string            `json:"imageMime,omitempty"`
	ImageWidth           int               `json:"imageWidth,omitempty"`
	ImageHeight          int               `json:"imageHeight,omitempty"`
	LoginURL             string            `json:"loginUrl,omitempty"`
	CreatedAt            time.Time         `json:"createdAt"`
	ExpiresAt            time.Time         `json:"expiresAt"`
}

// ChallengeAnswer carries the user's answer to a pending Challenge.
type ChallengeAnswer struct {
	ID                string  `json:"id"`
	Code              string  `json:"code,omitempty"`
	SkipSecondaryAuth bool    `json:"skipSecondaryAuth,omitempty"`
	Points            []Point `json:"points,omitempty"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	CallbackURL       string  `json:"callbackUrl,omitempty"`
}

// LogLevel is the severity level of a log entry.
type LogLevel string

const (
	LogDebug LogLevel = "debug"
	LogInfo  LogLevel = "info"
	LogWarn  LogLevel = "warn"
	LogError LogLevel = "error"
)

// LogEntry is a structured log event emitted by the core engine.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Level     LogLevel  `json:"level"`
	Source    string    `json:"source"`
	Message   string    `json:"message"`
}

// TrafficStats holds cumulative byte counts and instantaneous transfer rates.
type TrafficStats struct {
	BytesSent   uint64 `json:"bytesSent"`
	BytesRecv   uint64 `json:"bytesRecv"`
	RateSent    uint64 `json:"rateSent"`
	RateRecv    uint64 `json:"rateRecv"`
	ActiveConns int64  `json:"activeConns"`
}

// IPResourceItem represents an IP range pushed by the VPN server.
type IPResourceItem struct {
	IPMin    string `json:"ipMin"`
	IPMax    string `json:"ipMax"`
	CIDR     string `json:"cidr,omitempty"`
	PortMin  int    `json:"portMin"`
	PortMax  int    `json:"portMax"`
	Protocol string `json:"protocol"`
}

// DomainResourceItem represents a domain rule pushed by the VPN server.
type DomainResourceItem struct {
	Domain   string `json:"domain"`
	PortMin  int    `json:"portMin"`
	PortMax  int    `json:"portMax"`
	Protocol string `json:"protocol"`
}

// DNSOverrideItem represents a server-pushed hostname -> IP mapping.
type DNSOverrideItem struct {
	Host string   `json:"host"`
	IPs  []string `json:"ips"`
}

// ResourceSnapshot is a point-in-time snapshot of server-pushed VPN resources.
type ResourceSnapshot struct {
	VirtualIP       string               `json:"virtualIp,omitempty"`
	DNSServers      []string             `json:"dnsServers,omitempty"`
	IPResources     []IPResourceItem     `json:"ipResources"`
	DomainResources []DomainResourceItem `json:"domainResources"`
	DNSOverrides    []DNSOverrideItem    `json:"dnsOverrides"`
	ProxyAll        bool                 `json:"proxyAll"`
	UpdatedAt       time.Time            `json:"updatedAt,omitempty"`
}

// RouteCheckResult describes whether a given target host/IP:port will route through VPN.
type RouteCheckResult struct {
	Input       string `json:"input"`
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Network     string `json:"network"`
	ViaVPN      bool   `json:"viaVpn"`
	Reason      string `json:"reason"`
	MatchedRule string `json:"matchedRule,omitempty"`
}

// StatusSnapshot describes the complete current engine status.
type StatusSnapshot struct {
	State            State        `json:"state"`
	ProfileID        string       `json:"profileId,omitempty"`
	ProfileName      string       `json:"profileName,omitempty"`
	Protocol         Protocol     `json:"protocol,omitempty"`
	Server           string       `json:"server,omitempty"`
	VirtualIP        string       `json:"virtualIp,omitempty"`
	SocksAddr        string       `json:"socksAddr,omitempty"`
	HTTPAddr         string       `json:"httpAddr,omitempty"`
	ConnectedAt      *time.Time   `json:"connectedAt,omitempty"`
	ReconnectAttempt int          `json:"reconnectAttempt,omitempty"`
	LastError        string       `json:"lastError,omitempty"`
	ActiveChallenge  *Challenge   `json:"activeChallenge,omitempty"`
	Traffic          TrafficStats `json:"traffic"`
}

// EventType identifies the kind of event emitted by Engine.
type EventType string

const (
	EventState             EventType = "state"
	EventChallenge         EventType = "challenge"
	EventChallengeResolved EventType = "challenge_resolved"
	EventLog               EventType = "log"
	EventTraffic           EventType = "traffic"
	EventResources         EventType = "resources"
	EventError             EventType = "error"
)

// Event is an asynchronous notification emitted by Engine.
type Event struct {
	Type        EventType         `json:"type"`
	Status      *StatusSnapshot   `json:"status,omitempty"`
	Challenge   *Challenge        `json:"challenge,omitempty"`
	ChallengeID string            `json:"challengeId,omitempty"`
	Log         *LogEntry         `json:"log,omitempty"`
	Traffic     *TrafficStats     `json:"traffic,omitempty"`
	Resources   *ResourceSnapshot `json:"resources,omitempty"`
	Error       string            `json:"error,omitempty"`
}
