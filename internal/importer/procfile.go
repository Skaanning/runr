package importer

import (
	"fmt"
	"regexp"
	"strings"
)

var procfileLine = regexp.MustCompile(`^([^\s:#][^\s:]*):\s*(.*)$`)

// parseProcfile reads foreman/overmind's format: one "name: command" per
// line; blank lines and lines starting with # are skipped.
func parseProcfile(data string) ([]Service, error) {
	var services []Service
	seen := map[string]bool{}
	for i, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := procfileLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("line %d: expected name: command", i+1)
		}
		name, cmd := m[1], strings.TrimSpace(m[2])
		if cmd == "" {
			return nil, fmt.Errorf("line %d: %s has no command", i+1, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("line %d: %s is defined twice", i+1, name)
		}
		seen[name] = true
		services = append(services, Service{Name: name, Cmd: cmd})
	}
	return services, nil
}
