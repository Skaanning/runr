package proc

import "testing"

func TestKeepColors(t *testing.T) {
	for in, want := range map[string]string{
		"plain":                                          "plain",
		"\x1b[32mok\x1b[0m done":                         "\x1b[32mok\x1b[0m done",
		"\x1b[1;38;5;208mx\x1b[m":                        "\x1b[1;38;5;208mx\x1b[m",
		"\x1b[38:2:255:0:0mred":                          "\x1b[38:2:255:0:0mred",
		"\x1b[?25l\x1b[2J\x1b[m\x1b[Hhello\x1b[K":        "\x1b[mhello",
		"\x1b]0;C:\\Windows\\system32\\cmd.exe\x07title": "title",
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\":       "link",
		"\x1b[?9001h\x1b[?1004h":                         "",
		"a\x1b[3;1Hb":                                    "ab",
		"\x1b=keypad\x1b>":                               "keypad",
		"cut \x1b[3":                                     "cut ",
		"\x1b[>4;2m modifyOtherKeys isn't a color":       " modifyOtherKeys isn't a color",
		"\x1b": "",
	} {
		if got := keepColors(in); got != want {
			t.Errorf("keepColors(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestConsoleLine(t *testing.T) {
	// As ConPTY sends them (windows-latest CI runner).
	for in, want := range map[string]string{
		"\x1b[m\x1b[32mterminal":                  "\x1b[32mterminal",
		"\x1b[mto stderr":                         "to stderr",
		"\x1b[0m\x1b[mready":                      "ready",
		"\x1b[?25l\x1b[2J\x1b[m\x1b[Hhello\x1b[K": "hello",
		"\x1b[m":              "",
		"red \x1b[31mx\x1b[m": "red \x1b[31mx\x1b[m",
	} {
		if got := consoleLine(in); got != want {
			t.Errorf("consoleLine(%q) = %q, want %q", in, got, want)
		}
	}
}
