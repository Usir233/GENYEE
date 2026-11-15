//go:build !bindings

package main

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDragHandlerJSUsesGenyeeGuard(t *testing.T) {
	if strings.Contains(dragHandlerJS, "__wkDragBound") {
		t.Fatalf("dragHandlerJS still contains legacy __wkDragBound guard")
	}
	if !strings.Contains(dragHandlerJS, "window.__genyeeDragBound") {
		t.Fatalf("dragHandlerJS missing window.__genyeeDragBound guard")
	}
}

func TestWailsThemeSyncJSPerUserThemeResolution(t *testing.T) {
	if strings.Contains(wailsThemeSyncJS, "'GENYEE_theme'") {
		t.Fatalf("wailsThemeSyncJS must not read legacy unscoped GENYEE_theme key")
	}
	for _, needle := range []string{"genyee_user", "GENYEE_", "GENYEE_anon_theme"} {
		if !strings.Contains(wailsThemeSyncJS, needle) {
			t.Fatalf("wailsThemeSyncJS missing expected token %q", needle)
		}
	}

	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not available in PATH; skipping JS runtime verification")
	}

	type simResult struct {
		ThemeMode string `json:"themeMode"`
		Bg        string `json:"bg"`
		Runtime   string `json:"runtime"`
		RGB       []int  `json:"rgb"`
	}

	runSync := func(t *testing.T, storage map[string]string, prefersDark bool) simResult {
		t.Helper()
		storageJSON, err := json.Marshal(storage)
		if err != nil {
			t.Fatalf("marshal storage: %v", err)
		}
		jsSnippetJSON, err := json.Marshal(wailsThemeSyncJS)
		if err != nil {
			t.Fatalf("marshal js: %v", err)
		}
		harness := `
const store = ` + string(storageJSON) + `;
const prefersDark = ` + map[bool]string{true: "true", false: "false"}[prefersDark] + `;
const attrs = {};
let runtimeTheme = "";
let runtimeRGB = [];
globalThis.localStorage = {
  getItem(k) { return Object.prototype.hasOwnProperty.call(store, k) ? store[k] : null; }
};
globalThis.document = {
  documentElement: {
    style: {},
    setAttribute(k, v) { attrs[k] = v; }
  },
  body: { style: {} }
};
globalThis.window = {
  matchMedia() { return { matches: prefersDark }; },
  runtime: {
    WindowSetDarkTheme() { runtimeTheme = "dark"; },
    WindowSetLightTheme() { runtimeTheme = "light"; },
    WindowSetBackgroundColour(r, g, b, a) { runtimeRGB = [r, g, b, a]; }
  }
};
eval(` + string(jsSnippetJSON) + `);
console.log(JSON.stringify({
  themeMode: attrs["theme-mode"],
  bg: globalThis.document.documentElement.style.background,
  runtime: runtimeTheme,
  rgb: runtimeRGB
}));
`
		cmd := exec.Command(nodeBin, "-e", harness)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("node execution failed: %v\n%s", err, string(out))
		}
		var res simResult
		if err := json.Unmarshal(out, &res); err != nil {
			t.Fatalf("unmarshal result %q: %v", string(out), err)
		}
		return res
	}

	t.Run("logged-in user dark preference", func(t *testing.T) {
		res := runSync(t, map[string]string{
			"genyee_user":    `{"id":"u-42"}`,
			"GENYEE_u-42_theme": "dark",
		}, false)
		if res.ThemeMode != "dark" || res.Bg != "#181818" || res.Runtime != "dark" {
			t.Fatalf("expected dark theme for u-42, got %+v", res)
		}
	})

	t.Run("logged-in user falls back to anon preference", func(t *testing.T) {
		res := runSync(t, map[string]string{
			"genyee_user":       `{"id":"u-99"}`,
			"GENYEE_anon_theme": "dark",
		}, false)
		if res.ThemeMode != "dark" || res.Runtime != "dark" {
			t.Fatalf("expected anon dark fallback, got %+v", res)
		}
	})

	t.Run("corrupt genyee_user JSON falls back safely to anon/light", func(t *testing.T) {
		res := runSync(t, map[string]string{
			"genyee_user":       `{not-valid-json`,
			"GENYEE_anon_theme": "system",
		}, true)
		if res.ThemeMode != "dark" || res.Runtime != "dark" {
			t.Fatalf("expected system->dark after corrupt genyee_user, got %+v", res)
		}
	})

	t.Run("invalid stored theme falls back to light", func(t *testing.T) {
		res := runSync(t, map[string]string{
			"GENYEE_anon_theme": "solarized",
		}, true)
		if res.ThemeMode != "light" || res.Bg != "#eee" || res.Runtime != "light" {
			t.Fatalf("expected invalid theme to normalize to light, got %+v", res)
		}
	})
}
