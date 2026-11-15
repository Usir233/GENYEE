package browserskill

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

func validateNavigationURL(method string, params map[string]any) error {
	switch method {
	case "navigate":
		if params == nil {
			return errors.New("navigate requires an http or https URL")
		}
		raw, ok := params["url"].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			return errors.New("navigate requires an http or https URL")
		}
		return checkHTTPNavigationURL(raw)
	case "tab_create":
		if params == nil {
			return nil
		}
		rawVal, exists := params["url"]
		if !exists || rawVal == nil {
			return nil
		}
		raw, ok := rawVal.(string)
		if !ok {
			return errors.New("tab_create url must be a string")
		}
		if raw == "" || raw == "about:blank" {
			return nil
		}
		return checkHTTPNavigationURL(raw)
	default:
		return nil
	}
}

func checkHTTPNavigationURL(raw string) error {
	if len(raw) > 8192 {
		return errors.New("browser URL exceeds maximum length")
	}
	if strings.TrimSpace(raw) != raw {
		return errors.New("browser URL must not contain leading or trailing whitespace")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < 0x20 || raw[i] == 0x7f {
			return errors.New("browser URL contains invalid control characters")
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("browser URL must use http or https")
	}
	return nil
}

// NavigationIncomplete checks the known lifecycle result, never arbitrary page text.
func NavigationIncomplete(method string, raw json.RawMessage) bool {
	switch method {
	case "navigate", "navigate_back", "navigate_forward", "reload", "wait_for_navigation":
		var result struct {
			Reached string `json:"reached"`
		}
		return json.Unmarshal(raw, &result) == nil && result.Reached == "timeout"
	}
	return false
}

// Progress needs a readable address, never URL credentials or query tokens.
func statusPageURL(raw string) string {
	if len(raw) > 8192 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}
