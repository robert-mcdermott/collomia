package tui

import (
	"strings"
	"testing"
)

func TestThemeByName(t *testing.T) {
	for _, theme := range themes {
		got, ok := themeByName(theme.Name)
		if !ok || got.Name != theme.Name {
			t.Fatalf("themeByName(%q) not found", theme.Name)
		}
	}
	if _, ok := themeByName("no-such-theme"); ok {
		t.Fatal("unknown theme should not resolve")
	}
	if def := defaultTheme(); def.Name != defaultThemeName {
		t.Fatalf("default theme = %q, want %q", def.Name, defaultThemeName)
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
