package cmd

import (
	"fmt"
	"strings"

	"github.com/cyucelen/isola/internal/git"
	"github.com/cyucelen/isola/internal/process"
)

// selectedServices validates the entire selection before any setup or lifecycle
// changes. Preserve user order and handle duplicates only once. An omitted flag
// means the default stack; an explicitly empty flag must not expand to it.
func selectedServices(names []string, explicit bool) ([]string, error) {
	if explicit && len(names) == 0 {
		return nil, fmt.Errorf("--service requires at least one service name")
	}
	var selected []string
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("--service contains an empty service name")
		}
		if _, ok := cfg.Services[name]; !ok {
			return nil, fmt.Errorf("unknown service %q", name)
		}
		if !seen[name] {
			selected = append(selected, name)
			seen[name] = true
		}
	}
	return selected, nil
}

func actOnServices(tree *git.Worktree, services []string, act func(*git.Worktree, string) []process.ServiceResult) []process.ServiceResult {
	if len(services) == 0 {
		return act(tree, "")
	}
	var results []process.ServiceResult
	for _, name := range services {
		results = append(results, act(tree, name)...)
	}
	return results
}
