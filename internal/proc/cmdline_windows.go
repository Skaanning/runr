//go:build windows

package proc

import "strings"

// CommandLine returns one line for cmd.exe that runs command the way runr
// does: in dir, with env (KEY=VALUE pairs) added to the environment, by
// cmd /c. The variables stay set in the console it is pasted into. Values
// with % or quotes are written as they are; cmd may interpret them. cmd
// expands %VAR% when it reads the whole line, before the sets run, so a
// %VAR% in the command that the console already defines keeps its old value.
func CommandLine(dir string, env []string, command string) string {
	var b strings.Builder
	b.WriteString(`cd /d "` + dir + `"`)
	for _, kv := range env {
		b.WriteString(` && set "` + kv + `"`)
	}
	b.WriteString(` && cmd /d /s /c "` + command + `"`)
	return b.String()
}
