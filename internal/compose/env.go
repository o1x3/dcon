package compose

import (
	"os"
	"strings"
)

// ParseEnvFile reads a dotenv file in the docker compose dialect: one
// KEY=VALUE per line, optional `export ` prefix, blank lines and #-comment
// lines skipped, matching surrounding single or double quotes stripped from
// the value. dcon parses env files itself rather than forwarding --env-file
// to the backend: the backend's dialect and precedence differ (no export
// prefix, no quote stripping), so forwarding silently changed values.
func ParseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseDotenv(string(data)), nil
}

func parseDotenv(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '\'' || v[0] == '"') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out
}
