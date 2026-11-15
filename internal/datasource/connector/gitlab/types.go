package gitlab

import (
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Usir233/GENYEE/internal/datasource"
	"github.com/Usir233/GENYEE/internal/types"
)

func hasControlChar(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

func validateProjectID(id string) error {
	id = strings.TrimSpace(id)
	if id == "" || hasControlChar(id) || strings.ContainsAny(id, "\\?#") {
		return fmt.Errorf("%w: invalid project_id", datasource.ErrInvalidConfig)
	}
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		if n <= 0 {
			return fmt.Errorf("%w: invalid project_id", datasource.ErrInvalidConfig)
		}
		return nil
	}
	decoded := id
	if strings.Contains(id, "%") {
		unescaped, err := url.PathUnescape(id)
		if err != nil {
			return fmt.Errorf("%w: invalid project_id encoding", datasource.ErrInvalidConfig)
		}
		decoded = unescaped
	}
	if hasControlChar(decoded) || strings.ContainsAny(decoded, "\\?#") {
		return fmt.Errorf("%w: invalid project_id", datasource.ErrInvalidConfig)
	}
	parts := strings.Split(decoded, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%w: invalid project_id", datasource.ErrInvalidConfig)
		}
	}
	return nil
}

func validateGitRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == "." || ref == ".." || strings.Contains(ref, "..") ||
		strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") ||
		hasControlChar(ref) || strings.ContainsAny(ref, "\\?#") {
		return fmt.Errorf("%w: invalid git ref", datasource.ErrInvalidConfig)
	}
	return nil
}

type projectSelection struct {
	ProjectID string   `json:"project_id"`
	Ref       string   `json:"ref"`
	Paths     []string `json:"paths"`
}

type config struct {
	Projects []projectSelection `json:"projects"`
}

func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	raw, ok := ds.Settings["projects"]
	if !ok {
		return nil, fmt.Errorf("%w: settings.projects is required", datasource.ErrInvalidConfig)
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: settings.projects must be an array", datasource.ErrInvalidConfig)
	}
	out := &config{Projects: make([]projectSelection, 0, len(items))}
	seen := map[string]bool{}
	for _, rawProject := range items {
		m, ok := rawProject.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: invalid project selection", datasource.ErrInvalidConfig)
		}
		id, _ := m["project_id"].(string)
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("%w: project_id must be unique and non-empty", datasource.ErrInvalidConfig)
		}
		if err := validateProjectID(id); err != nil {
			return nil, err
		}
		seen[id] = true
		ref, _ := m["ref"].(string)
		ref = strings.TrimSpace(ref)
		if ref != "" {
			if err := validateGitRef(ref); err != nil {
				return nil, err
			}
		}
		p := projectSelection{ProjectID: id, Ref: ref}
		if rawPaths, exists := m["paths"]; exists {
			values, ok := rawPaths.([]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: paths must be an array", datasource.ErrInvalidConfig)
			}
			for _, value := range values {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%w: path must be a string", datasource.ErrInvalidConfig)
				}
				normalized, err := normalizePath(s)
				if err != nil {
					return nil, err
				}
				p.Paths = append(p.Paths, normalized)
			}
		}
		p.Paths = collapsePaths(p.Paths)
		out.Projects = append(out.Projects, p)
	}
	if len(out.Projects) == 0 {
		return nil, fmt.Errorf("%w: at least one project is required", datasource.ErrInvalidConfig)
	}
	return out, nil
}

func normalizePath(value string) (string, error) {
	v := strings.Trim(strings.TrimSpace(value), "/")
	if v == "" {
		return "", nil
	}
	if strings.Contains(v, "\\") {
		return "", fmt.Errorf("%w: path must use forward slashes", datasource.ErrInvalidConfig)
	}
	if path.Clean(v) != v || v == "." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") {
		return "", fmt.Errorf("%w: invalid repository path", datasource.ErrInvalidConfig)
	}
	return v, nil
}

func collapsePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == "" {
			return nil
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if len(out) > 0 && (p == out[len(out)-1] || strings.HasPrefix(p, out[len(out)-1]+"/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}
