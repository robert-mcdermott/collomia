package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/robert-mcdermott/collomia/internal/agent"
	"github.com/robert-mcdermott/collomia/internal/provider"
)

var hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func TestThemeByName(t *testing.T) {
	expected := []string{
		"collomia",
		"synthwave",
		"outrun",
		"blade-runner-2049",
		"chaos-theory",
		"cyberpunk-2077-blue",
		"cyberpunk-2077-violet",
		"catppuccin-mocha",
		"gruvbox-dark",
		"rose-pine-moon",
		"kanagawa-wave",
		"matrix",
		"monokai",
		"dracula",
		"nord",
		"tokyo-night",
		"fredhutch-dark",
		"fredhutch-light",
		"plain",
	}
	if len(themes) != len(expected) {
		t.Fatalf("theme count = %d, want %d", len(themes), len(expected))
	}

	seen := make(map[string]bool, len(themes))
	for _, theme := range themes {
		if seen[theme.Name] {
			t.Fatalf("duplicate theme name %q", theme.Name)
		}
		seen[theme.Name] = true
		got, ok := themeByName(theme.Name)
		if !ok || got.Name != theme.Name {
			t.Fatalf("themeByName(%q) not found", theme.Name)
		}
	}
	for _, name := range expected {
		if !seen[name] {
			t.Errorf("expected theme %q is missing", name)
		}
	}
	if _, ok := themeByName("no-such-theme"); ok {
		t.Fatal("unknown theme should not resolve")
	}
	if def := defaultTheme(); def.Name != defaultThemeName {
		t.Fatalf("default theme = %q, want %q", def.Name, defaultThemeName)
	}
}

func TestThemeBackgrounds(t *testing.T) {
	for _, theme := range themes {
		colors := map[string]string{
			"primary":    theme.Primary,
			"secondary":  theme.Secondary,
			"accent":     theme.Accent,
			"success":    theme.Success,
			"warning":    theme.Warning,
			"error":      theme.Error,
			"muted":      theme.Muted,
			"border":     theme.Border,
			"status":     theme.StatusBG,
			"background": theme.Background,
		}
		if theme.plain() {
			for role, color := range colors {
				if color != "" {
					t.Fatalf("plain theme must not set %s color, got %q", role, color)
				}
			}
			continue
		}
		for role, color := range colors {
			if !hexColorPattern.MatchString(color) {
				t.Errorf("theme %s has invalid %s color %q", theme.Name, role, color)
			}
		}
		if theme.Background == theme.StatusBG {
			t.Fatalf("theme %s: background should differ from the status bar color so the bar stays visible", theme.Name)
		}
	}
}

func TestMatchCommands(t *testing.T) {
	all := matchCommands("/")
	if len(all) != len(slashCommands) {
		t.Fatalf("bare slash should match all commands, got %d of %d", len(all), len(slashCommands))
	}
	models := matchCommands("/model")
	if len(models) < 2 || models[0].name != "/model" || models[1].name != "/models" {
		t.Fatalf("prefix matches should rank first, got %+v", models)
	}
	if got := matchCommands("/xyzzy"); len(got) != 0 {
		t.Fatalf("no matches expected, got %+v", got)
	}
	if got := matchCommands("/heme"); len(got) == 0 || got[0].name != "/theme" {
		t.Fatalf("substring match expected for /heme, got %+v", got)
	}
}

func TestContextGauge(t *testing.T) {
	theme := defaultTheme()
	if got := contextGauge(theme, 5000, 0, 10); !strings.Contains(got, "5.0k") {
		t.Fatalf("no-window gauge should fall back to token count, got %q", got)
	}
	got := contextGauge(theme, 5000, 10000, 10)
	if !strings.Contains(got, "50%") {
		t.Fatalf("expected 50%% usage, got %q", got)
	}
	if over := contextGauge(theme, 20000, 10000, 10); !strings.Contains(over, "100%") {
		t.Fatalf("usage should clamp at 100%%, got %q", over)
	}
}

func TestOnColorContrast(t *testing.T) {
	if onColor("#FFFFFF") != "#14141E" {
		t.Fatal("white background should get dark text")
	}
	if onColor("#000000") != "#F8F8F5" {
		t.Fatal("black background should get light text")
	}
}

// TestCacheSummaryDistinguishesTheThreeZeroes is the point of the helper: a
// bare "0 cached" cannot tell a user whether the provider has no cache, the
// cache has not been written yet, or reuse is silently failing.
func TestCacheSummaryDistinguishesTheThreeZeroes(t *testing.T) {
	supported := provider.Capabilities{PromptCaching: provider.CapabilitySupported}
	unsupported := provider.Capabilities{PromptCaching: provider.CapabilityUnsupported}
	partial := provider.Capabilities{PromptCaching: provider.CapabilityPartial}

	if got := cacheSummary(provider.Usage{InputTokens: 5000}, unsupported); got != "not supported by this provider/model" {
		t.Errorf("unsupported provider: %q", got)
	}
	if got := cacheSummary(provider.Usage{InputTokens: 5000}, supported); got != "requested, not yet warm" {
		t.Errorf("cold cache: %q", got)
	}
	if got := cacheSummary(provider.Usage{InputTokens: 5000}, partial); !strings.Contains(got, "no cache activity") {
		t.Errorf("endpoint reporting nothing: %q", got)
	}
	// Nothing to say before the first request, whatever the capability is.
	if got := cacheSummary(provider.Usage{}, supported); got != "" {
		t.Errorf("no requests yet: %q", got)
	}
	warm := cacheSummary(provider.Usage{InputTokens: 10_000, CachedTokens: 8_000, CacheWriteTokens: 1_000}, supported)
	if !strings.Contains(warm, "8.0k tok read") || !strings.Contains(warm, "1.0k tok written") || !strings.Contains(warm, "80%") {
		t.Errorf("warm cache: %q", warm)
	}
}

func TestCacheLifetimeSummaryReportsOnlyWhatItMeasured(t *testing.T) {
	// A session that never paused is not evidence that a longer cache
	// lifetime is unnecessary — it is a session with nothing to say. Printing
	// a reassuring zero would be an argument dressed as a measurement.
	for _, quiet := range []agent.CacheGaps{
		{},
		{Gaps: 40, Longest: 90 * time.Second},
	} {
		if got := cacheLifetimeSummary(quiet); got != "" {
			t.Errorf("cacheLifetimeSummary(%+v) = %q, want no claim", quiet, got)
		}
	}
	recoverable := cacheLifetimeSummary(agent.CacheGaps{Gaps: 10, Recoverable: 3, Longest: 12 * time.Minute})
	for _, want := range []string{"3 of 10 gaps", "1-hour cache", "12m0s"} {
		if !strings.Contains(recoverable, want) {
			t.Errorf("summary %q missing %q", recoverable, want)
		}
	}
	if strings.Contains(recoverable, "exceeded an hour") {
		t.Errorf("summary %q claims hour-long gaps that were not measured", recoverable)
	}
	both := cacheLifetimeSummary(agent.CacheGaps{Gaps: 10, Recoverable: 3, ColdEither: 2, Longest: 3 * time.Hour})
	for _, want := range []string{"5 of 10 gaps", "3 would have stayed warm", "2 exceeded an hour"} {
		if !strings.Contains(both, want) {
			t.Errorf("summary %q missing %q", both, want)
		}
	}
}
