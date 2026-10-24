package config

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// Resolve expands the given project and group names into the list of projects
// to run, including all transitive dependencies. The result is a valid start
// order (every project comes after its dependencies) that otherwise follows the
// order in which names were given, with duplicates removed.
func (c *Config) Resolve(names ...string) ([]string, error) {
	var unknown []string
	var requested []string
	for _, name := range names {
		switch {
		case c.Projects[name] != nil:
			requested = append(requested, name)
		case c.Groups[name] != nil:
			requested = append(requested, c.Groups[name]...)
		default:
			if _, isGroup := c.Groups[name]; isGroup {
				continue // empty group
			}
			unknown = append(unknown, fmt.Sprintf("%q", name))
		}
	}
	if len(unknown) == 1 {
		return nil, fmt.Errorf("unknown project or group %s", unknown[0])
	}
	if len(unknown) > 1 {
		return nil, fmt.Errorf("unknown projects or groups %s", strings.Join(unknown, ", "))
	}

	const (
		visiting = 1
		done     = 2
	)
	state := map[string]int{}
	var order []string
	var visit func(name string, from string) error
	visit = func(name, from string) error {
		switch state[name] {
		case done:
			return nil
		case visiting:
			return fmt.Errorf("dependency cycle involving %q", name)
		}
		p := c.Projects[name]
		if p == nil {
			return fmt.Errorf("project %q depends on unknown project %q", from, name)
		}
		state[name] = visiting
		for _, dep := range p.DependsOn {
			if err := visit(dep, name); err != nil {
				return err
			}
		}
		state[name] = done
		order = append(order, name)
		return nil
	}
	for _, name := range requested {
		if err := visit(name, ""); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// Levels groups projects by dependency depth: level 0 holds projects with no
// dependencies inside the given set, level n holds projects whose deepest
// dependency is in level n-1. Projects in the same level can be started (or,
// in reverse level order, stopped) concurrently. Dependencies outside the set
// are ignored. Each level is sorted by name.
func (c *Config) Levels(projects []string) ([][]string, error) {
	inSet := make(map[string]bool, len(projects))
	for _, name := range projects {
		if c.Projects[name] == nil {
			return nil, fmt.Errorf("unknown project %q", name)
		}
		inSet[name] = true
	}

	level := map[string]int{}
	visiting := map[string]bool{}
	var depth func(name string) (int, error)
	depth = func(name string) (int, error) {
		if l, ok := level[name]; ok {
			return l, nil
		}
		if visiting[name] {
			return 0, fmt.Errorf("dependency cycle involving %q", name)
		}
		visiting[name] = true
		l := 0
		for _, dep := range c.Projects[name].DependsOn {
			if !inSet[dep] {
				continue
			}
			d, err := depth(dep)
			if err != nil {
				return 0, err
			}
			l = max(l, d+1)
		}
		visiting[name] = false
		level[name] = l
		return l, nil
	}

	var levels [][]string
	for _, name := range sortedKeys(inSet) {
		l, err := depth(name)
		if err != nil {
			return nil, err
		}
		for len(levels) <= l {
			levels = append(levels, nil)
		}
		levels[l] = append(levels[l], name)
	}
	return levels, nil
}

// Dependents returns the projects that directly depend on name, sorted.
func (c *Config) Dependents(name string) []string {
	var out []string
	for _, other := range sortedKeys(c.Projects) {
		for _, dep := range c.Projects[other].DependsOn {
			if dep == name {
				out = append(out, other)
				break
			}
		}
	}
	return out
}

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

// Rename gives a project or group a new name and updates every reference
// to it: group members and depends_on entries for a project. It reports the
// groups and projects whose references changed, sorted.
func (c *Config) Rename(old, name string) (groups, dependents []string, err error) {
	if old == name {
		return nil, nil, nil
	}
	if !namePattern.MatchString(name) {
		return nil, nil, fmt.Errorf("invalid name %q (use letters, digits, '.', '_' and '-', starting with a letter or digit)", name)
	}
	if c.Projects[name] != nil || c.Groups[name] != nil {
		return nil, nil, fmt.Errorf("a project or group named %q already exists", name)
	}
	if members, ok := c.Groups[old]; ok {
		delete(c.Groups, old)
		c.Groups[name] = members
		return nil, nil, nil
	}
	p := c.Projects[old]
	if p == nil {
		return nil, nil, fmt.Errorf("unknown project or group %q", old)
	}
	delete(c.Projects, old)
	c.Projects[name] = p
	rename := func(list []string) bool {
		i := slices.Index(list, old)
		if i >= 0 {
			list[i] = name
		}
		return i >= 0
	}
	for _, other := range slices.Sorted(maps.Keys(c.Projects)) {
		if rename(c.Projects[other].DependsOn) {
			dependents = append(dependents, other)
		}
	}
	for _, g := range slices.Sorted(maps.Keys(c.Groups)) {
		if rename(c.Groups[g]) {
			groups = append(groups, g)
		}
	}
	return groups, dependents, nil
}
