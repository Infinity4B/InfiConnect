package core

import (
	"regexp"
	"strings"
)

var (
	redactKVPattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|totp_secret|totp|twf_id|twfid|sid|sign_key|signkey|cookie|set-cookie|token|access_token|refresh_token|csrf|x-csrf-token|ticket|oauth2_code|authorization|cert_password|socks_passwd|device_id|deviceid|client_secret)\s*([:=]\s*)"?([^"\s,;&]+)"?`)
	redactURLParam  = regexp.MustCompile(`(?i)([?&](?:ticket|code|token|sid|twf_id|password|passwd|access_token)=)([^&\s]+)`)
	redactBearer    = regexp.MustCompile(`(?i)(Bearer\s+)([A-Za-z0-9._~+/=-]+)`)
	redactPhone     = regexp.MustCompile(`\b(1[3-9]\d)\d{4}(\d{4})\b`)
	redactHexLong   = regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`)
)

// SanitizeLogLine redacts credentials, tokens, session IDs, cookies, tickets,
// phone numbers, and any caller-provided sensitive literals (e.g. usernames).
func SanitizeLogLine(line string, extraSecrets ...string) string {
	out := line
	for _, secret := range extraSecrets {
		s := strings.TrimSpace(secret)
		if len(s) >= 3 {
			out = strings.ReplaceAll(out, s, "[REDACTED]")
		}
	}
	out = redactKVPattern.ReplaceAllString(out, `$1$2[REDACTED]`)
	out = redactURLParam.ReplaceAllString(out, `$1[REDACTED]`)
	out = redactBearer.ReplaceAllString(out, `$1[REDACTED]`)
	out = redactPhone.ReplaceAllString(out, `$1****$2`)
	out = redactHexLong.ReplaceAllString(out, `[REDACTED_HEX]`)
	return out
}

// SanitizeLogs formats and redacts a slice of LogEntry into an exportable log text.
func SanitizeLogs(entries []LogEntry, extraSecrets ...string) string {
	var b strings.Builder
	for _, e := range entries {
		ts := e.Timestamp.Format("2006-01-02 15:04:05.000")
		lvl := strings.ToUpper(string(e.Level))
		if lvl == "" {
			lvl = "INFO"
		}
		src := e.Source
		if src == "" {
			src = "core"
		}
		msg := SanitizeLogLine(e.Message, extraSecrets...)
		b.WriteString(ts)
		b.WriteString(" [")
		b.WriteString(lvl)
		b.WriteString("] [")
		b.WriteString(src)
		b.WriteString("] ")
		b.WriteString(msg)
		b.WriteByte('\n')
	}
	return b.String()
}
