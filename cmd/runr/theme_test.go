package main

import (
	"strings"
	"testing"
)

func TestTheme(t *testing.T) {
	testEnv(t)
	want := strings.Join([]string{
		"* terminal     the terminal's own colors",
		"  catppuccin   mocha · latte",
		"  tokyo-night  night · day",
		"  gruvbox      dark · light",
		"  nord         dark only",
		"",
	}, "\n")
	if got := mustRun(t, "theme"); got != want {
		t.Fatalf("runr theme:\n%s\nwant:\n%s", got, want)
	}

	if got := mustRun(t, "theme", "nord"); got != "Theme set to nord\n" {
		t.Fatalf("runr theme nord = %q", got)
	}
	if got := loadConfig(t).Settings.ThemeValue; got != "nord" {
		t.Fatalf("theme = %q, want nord", got)
	}
	if got := mustRun(t, "theme"); !strings.Contains(got, "  terminal ") || !strings.Contains(got, "* nord ") {
		t.Fatalf("current theme not marked:\n%s", got)
	}

	_, _, err := run(t, "theme", "solarized")
	wantErr(t, err, `unknown theme "solarized" (one of terminal, catppuccin, tokyo-night, gruvbox, nord)`)
	if got := loadConfig(t).Settings.ThemeValue; got != "nord" {
		t.Fatalf("theme = %q after a failed set, want nord", got)
	}

	_, _, err = run(t, "theme", "nord", "gruvbox")
	wantErr(t, err, "accepts at most 1 arg")
}
