package config

import "sort"

// findCycles returns every dependency cycle among projects, each as a path whose
// first and last element are the same project (e.g. [api auth api]). Unknown
// dependencies and self-dependencies are ignored; they are reported separately
// by Validate.
func (c *Config) findCycles() [][]string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(c.Projects))
	var stack []string
	var cycles [][]string

	var visit func(name string)
	visit = func(name string) {
		state[name] = visiting
		stack = append(stack, name)
		for _, dep := range c.Projects[name].DependsOn {
			if _, ok := c.Projects[dep]; !ok || dep == name {
				continue
			}
			switch state[dep] {
			case unvisited:
				visit(dep)
			case visiting:
				start := len(stack) - 1
				for stack[start] != dep {
					start--
				}
				cycle := append([]string{}, stack[start:]...)
				cycles = append(cycles, append(cycle, dep))
			}
		}
		stack = stack[:len(stack)-1]
		state[name] = done
	}

	for _, name := range sortedKeys(c.Projects) {
		if state[name] == unvisited {
			visit(name)
		}
	}
	return cycles
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
