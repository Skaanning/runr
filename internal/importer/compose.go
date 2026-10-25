package importer

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type composeFile struct {
	Name     string    `yaml:"name"`
	Services yaml.Node `yaml:"services"`
}

type composeService struct {
	DependsOn yaml.Node   `yaml:"depends_on"`
	Ports     []yaml.Node `yaml:"ports"`
}

// parseCompose reads the services of a compose file, in file order. Each
// runs as `docker compose [-f file] up --no-deps --no-log-prefix <service>`
// (fileArg "" leaves out -f), so that runr, not compose, starts the
// dependencies.
func parseCompose(data []byte, fileArg string) (name string, services []Service, err error) {
	var f composeFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return "", nil, err
	}
	root := resolve(&f.Services)
	if root.Kind == 0 {
		return f.Name, nil, nil
	}
	if root.Kind != yaml.MappingNode {
		return "", nil, fmt.Errorf("line %d: services: expected a mapping", root.Line)
	}

	known := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if known[key.Value] {
			return "", nil, fmt.Errorf("line %d: service %s is defined twice", key.Line, key.Value)
		}
		known[key.Value] = true
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, value := root.Content[i], root.Content[i+1]
		var svc composeService
		if err := value.Decode(&svc); err != nil {
			return "", nil, fmt.Errorf("service %s: %w", key.Value, err)
		}
		deps, err := dependsOn(&svc.DependsOn)
		if err != nil {
			return "", nil, fmt.Errorf("service %s: %w", key.Value, err)
		}
		for _, dep := range deps {
			if !known[dep] {
				return "", nil, fmt.Errorf("service %s depends on %s, which isn't in the file", key.Value, dep)
			}
		}

		args := []string{"docker", "compose"}
		if fileArg != "" {
			args = append(args, "-f", fileArg)
		}
		args = append(args, "up", "--no-deps", "--no-log-prefix", key.Value)
		services = append(services, Service{
			Name:      key.Value,
			Args:      args,
			DependsOn: deps,
			Port:      firstHostPort(svc.Ports),
		})
	}
	return f.Name, services, nil
}

// resolve follows an alias (*anchor) to the node it refers to.
func resolve(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// dependsOn reads depends_on in either form: a list of service names, or a
// map from service name to options (whose conditions are ignored).
func dependsOn(n *yaml.Node) ([]string, error) {
	n = resolve(n)
	var deps []string
	switch n.Kind {
	case 0:
	case yaml.SequenceNode:
		for _, item := range n.Content {
			deps = append(deps, resolve(item).Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			deps = append(deps, n.Content[i].Value)
		}
	default:
		return nil, fmt.Errorf("line %d: depends_on: expected a list or a mapping", n.Line)
	}
	return deps, nil
}

// firstHostPort is the first TCP port that ports publishes on the host, in
// short ("8080:80", "127.0.0.1:8080:80/tcp") or long syntax; 0 if none. A
// container port alone gets a random host port, so it doesn't count.
func firstHostPort(ports []yaml.Node) int {
	for i := range ports {
		n := resolve(&ports[i])
		var port int
		switch n.Kind {
		case yaml.ScalarNode:
			port = shortSyntaxPort(n.Value)
		case yaml.MappingNode:
			var long struct {
				Published string `yaml:"published"`
				Protocol  string `yaml:"protocol"`
			}
			if n.Decode(&long) == nil && (long.Protocol == "" || long.Protocol == "tcp") {
				port = portNumber(long.Published)
			}
		}
		if port != 0 {
			return port
		}
	}
	return 0
}

// shortSyntaxPort reads [[IP:]HOST:]CONTAINER[/PROTOCOL] and returns HOST
// (the first port of a range).
func shortSyntaxPort(s string) int {
	s, proto, _ := strings.Cut(s, "/")
	if proto != "" && proto != "tcp" {
		return 0
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return 0
	}
	host := s[:i]
	if j := strings.LastIndex(host, ":"); j >= 0 {
		host = host[j+1:] // after the IP, which may be [::1]
	}
	return portNumber(host)
}

// portNumber parses a port or the start of a range like "8000-8010"; 0 if
// it isn't one (e.g. an unset ${VARIABLE}).
func portNumber(s string) int {
	start, _, _ := strings.Cut(strings.TrimSpace(s), "-")
	port, err := strconv.Atoi(start)
	if err != nil || port < 1 || port > 65535 {
		return 0
	}
	return port
}
