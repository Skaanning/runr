package proc

import (
	"fmt"
	"runtime"
	"sort"
	"strings"

	"github.com/joho/godotenv"
)

// DefaultEnv is applied to every process before its env_file and env. Programs
// write to pipes rather than a terminal, which makes many of them drop colors
// and buffer output; these variables ask them not to.
var DefaultEnv = map[string]string{
	"FORCE_COLOR":      "1",
	"CLICOLOR_FORCE":   "1",
	"PYTHONUNBUFFERED": "1",
}

// BuildEnv returns base (KEY=VALUE pairs, e.g. os.Environ()) overlaid with each
// layer in turn; later layers win. Keys are case-insensitive on Windows.
func BuildEnv(base []string, layers ...map[string]string) []string {
	env := make([]string, 0, len(base))
	index := map[string]int{} // normalized key -> position in env
	set := func(key, value string) {
		kv := key + "=" + value
		if i, ok := index[normalizeKey(key)]; ok {
			env[i] = kv
			return
		}
		index[normalizeKey(key)] = len(env)
		env = append(env, kv)
	}

	for _, kv := range base {
		if kv == "" {
			continue
		}
		// Search from index 1: Windows has entries like "=C:=C:\dir" whose
		// key starts with '='.
		i := strings.IndexByte(kv[1:], '=') + 1
		if i == 0 {
			continue // no '='
		}
		set(kv[:i], kv[i+1:])
	}
	for _, layer := range layers {
		keys := make([]string, 0, len(layer))
		for k := range layer {
			keys = append(keys, k)
		}
		sort.Strings(keys) // deterministic order for new keys
		for _, k := range keys {
			set(k, layer[k])
		}
	}
	return env
}

func normalizeKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}

// LoadEnvFile reads a .env file (KEY=VALUE lines, # comments, optional quotes
// and "export " prefixes).
func LoadEnvFile(path string) (map[string]string, error) {
	vars, err := godotenv.Read(path)
	if err != nil {
		return nil, fmt.Errorf("reading env file: %w", err)
	}
	return vars, nil
}
