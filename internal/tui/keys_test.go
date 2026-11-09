package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestKeyBytes(t *testing.T) {
	for _, tt := range []struct {
		key  tea.Key
		want string
	}{
		{tea.Key{Code: 'a', Text: "a"}, "a"},
		{tea.Key{Code: 'a', ShiftedCode: 'A', Text: "A", Mod: tea.ModShift}, "A"},
		{tea.Key{Code: 'å', Text: "å"}, "å"},
		{tea.Key{Code: tea.KeyEnter}, "\r"},
		{tea.Key{Code: tea.KeyBackspace}, "\x7f"},
		{tea.Key{Code: tea.KeyTab}, "\t"},
		{tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z"},
		{tea.Key{Code: tea.KeyEscape}, "\x1b"},
		{tea.Key{Code: tea.KeySpace, Text: " "}, " "},
		{tea.Key{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{tea.Key{Code: 'd', Mod: tea.ModCtrl}, "\x04"},
		{tea.Key{Code: '[', Mod: tea.ModCtrl}, "\x1b"},
		{tea.Key{Code: 'b', Mod: tea.ModAlt}, "\x1bb"},
		{tea.Key{Code: tea.KeyUp}, "\x1b[A"},
		{tea.Key{Code: tea.KeyLeft}, "\x1b[D"},
		{tea.Key{Code: tea.KeyDelete}, "\x1b[3~"},
		{tea.Key{Code: tea.KeyF1}, "\x1bOP"},
		{tea.Key{Code: tea.KeyLeftShift}, ""},
	} {
		if got := string(keyBytes(tea.KeyPressMsg(tt.key))); got != tt.want {
			t.Errorf("%v: %q, want %q", tt.key, got, tt.want)
		}
	}
}
