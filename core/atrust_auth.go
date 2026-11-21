package core

import (
	"fmt"
	"strings"
)

// ATrustAuthChoice mirrors an entry returned by an aTrust server's authServerInfoList.
type ATrustAuthChoice struct {
	LoginDomain string
	AuthType    string
	AuthName    string
	SubType     string
}

func isSupportedATrustAuthType(t string) bool {
	switch ATrustAuthType(t) {
	case ATrustAuthPassword, ATrustAuthCAS, ATrustAuthOAuth2, ATrustAuthSMS:
		return true
	default:
		return false
	}
}

// SelectATrustAuthInfo resolves the best (AuthType, LoginDomain) pair from the
// server's advertised authServerInfoList.
//
// When requestedDomain is empty—or equals the legacy ZJU default "Radius" while
// the server does not advertise a "Radius" domain—it automatically picks the
// server's default matching login method (matching the official aTrust client).
func SelectATrustAuthInfo(
	list []ATrustAuthChoice,
	requestedType ATrustAuthType,
	requestedDomain string,
	hasUserPass bool,
	hasPhone bool,
) (ATrustAuthChoice, error) {
	if len(list) == 0 {
		return ATrustAuthChoice{}, fmt.Errorf("aTrust server returned no authentication methods")
	}

	reqType := strings.TrimSpace(string(requestedType))
	reqDomain := strings.TrimSpace(requestedDomain)

	// 1. Exact match (or case-insensitive domain match) when both are specified.
	if reqType != "" && reqDomain != "" {
		for _, item := range list {
			if item.AuthType == reqType && item.LoginDomain == reqDomain {
				return item, nil
			}
		}
		for _, item := range list {
			if item.AuthType == reqType && strings.EqualFold(item.LoginDomain, reqDomain) {
				return item, nil
			}
		}
	}

	// Filter to auth types supported by InfiConnect/zju-connect.
	supported := make([]ATrustAuthChoice, 0, len(list))
	for _, item := range list {
		if isSupportedATrustAuthType(item.AuthType) {
			supported = append(supported, item)
		}
	}
	if len(supported) == 0 {
		return ATrustAuthChoice{}, fmt.Errorf(
			"aTrust server has no supported authentication method; server advertised: %s",
			formatATrustAuthChoices(list),
		)
	}

	// Narrow candidates by requested or inferred auth type.
	candidates := supported
	if reqType != "" {
		var byType []ATrustAuthChoice
		for _, item := range supported {
			if item.AuthType == reqType {
				byType = append(byType, item)
			}
		}
		if len(byType) == 0 {
			return ATrustAuthChoice{}, fmt.Errorf(
				"auth type %q is not offered by server; available methods: %s",
				reqType, formatATrustAuthChoices(list),
			)
		}
		candidates = byType
	} else {
		// Auto-detect auth type based on provided credentials, falling back to
		// all supported methods if the server doesn't offer that type.
		var preferredType string
		switch {
		case hasUserPass:
			preferredType = string(ATrustAuthPassword)
		case hasPhone:
			preferredType = string(ATrustAuthSMS)
		}
		if preferredType != "" {
			var byPref []ATrustAuthChoice
			for _, item := range supported {
				if item.AuthType == preferredType {
					byPref = append(byPref, item)
				}
			}
			if len(byPref) > 0 {
				candidates = byPref
			}
		}
	}

	// Match requested domain if provided.
	if reqDomain != "" {
		for _, item := range candidates {
			if item.LoginDomain == reqDomain {
				return item, nil
			}
		}
		for _, item := range candidates {
			if strings.EqualFold(item.LoginDomain, reqDomain) {
				return item, nil
			}
		}
		// Legacy profiles pre-filled loginDomain="Radius" from ZJU defaults.
		// If the server doesn't have "Radius", gracefully auto-detect instead of failing.
		if !strings.EqualFold(reqDomain, "Radius") {
			return ATrustAuthChoice{}, fmt.Errorf(
				"login domain %q not found for auth type %s; available methods: %s",
				reqDomain,
				func() string {
					if reqType != "" {
						return reqType
					}
					return "auto"
				}(),
				formatATrustAuthChoices(list),
			)
		}
	}

	// Prefer entry explicitly marked as default by the aTrust server.
	for _, item := range candidates {
		if strings.EqualFold(item.SubType, "default") {
			return item, nil
		}
	}
	return candidates[0], nil
}

func formatATrustAuthChoices(list []ATrustAuthChoice) string {
	parts := make([]string, 0, len(list))
	for _, item := range list {
		if item.AuthName != "" {
			parts = append(parts, fmt.Sprintf("%s@%s (%s)", item.AuthType, item.LoginDomain, item.AuthName))
		} else {
			parts = append(parts, fmt.Sprintf("%s@%s", item.AuthType, item.LoginDomain))
		}
	}
	return strings.Join(parts, ", ")
}
