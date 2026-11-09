package tui

import (
	tea "charm.land/bubbletea/v2"
)

// keySequences are what a terminal sends for the special keys (VT input,
// which ConPTY understands too).
var keySequences = map[rune]string{
	tea.KeyEnter:     "\r",
	tea.KeyTab:       "\t",
	tea.KeyBackspace: "\x7f",
	tea.KeyEscape:    "\x1b",
	tea.KeySpace:     " ",
	tea.KeyUp:        "\x1b[A",
	tea.KeyDown:      "\x1b[B",
	tea.KeyRight:     "\x1b[C",
	tea.KeyLeft:      "\x1b[D",
	tea.KeyHome:      "\x1b[H",
	tea.KeyEnd:       "\x1b[F",
	tea.KeyInsert:    "\x1b[2~",
	tea.KeyDelete:    "\x1b[3~",
	tea.KeyPgUp:      "\x1b[5~",
	tea.KeyPgDown:    "\x1b[6~",
	tea.KeyF1:        "\x1bOP",
	tea.KeyF2:        "\x1bOQ",
	tea.KeyF3:        "\x1bOR",
	tea.KeyF4:        "\x1bOS",
	tea.KeyF5:        "\x1b[15~",
	tea.KeyF6:        "\x1b[17~",
	tea.KeyF7:        "\x1b[18~",
	tea.KeyF8:        "\x1b[19~",
	tea.KeyF9:        "\x1b[20~",
	tea.KeyF10:       "\x1b[21~",
	tea.KeyF11:       "\x1b[23~",
	tea.KeyF12:       "\x1b[24~",
}

// keyBytes returns what a terminal sends to the program on it for a key
// press, or nil for a key it sends nothing for (a modifier on its own).
func keyBytes(msg tea.KeyPressMsg) []byte {
	k := msg.Key()
	alt := ""
	if k.Mod.Contains(tea.ModAlt) {
		alt = "\x1b"
	}
	switch {
	case k.Mod.Contains(tea.ModCtrl) && k.Code >= 'a' && k.Code <= 'z':
		return []byte(alt + string(rune(k.Code-'a'+1)))
	case k.Mod.Contains(tea.ModCtrl) && k.Code >= '@' && k.Code <= '_': // ctrl+@ … ctrl+_
		return []byte(alt + string(rune(k.Code-'@')))
	case k.Mod.Contains(tea.ModCtrl) && k.Code == tea.KeySpace:
		return []byte(alt + "\x00")
	case k.Mod.Contains(tea.ModShift) && k.Code == tea.KeyTab:
		return []byte(alt + "\x1b[Z")
	case k.Text != "":
		return []byte(alt + k.Text)
	}
	if seq, ok := keySequences[k.Code]; ok {
		return []byte(alt + seq)
	}
	if k.Code >= 0x20 && k.Code < 0x10ffff && !k.Mod.Contains(tea.ModCtrl) {
		return []byte(alt + string(k.Code)) // a printable key without text, e.g. alt+x
	}
	return nil
}
