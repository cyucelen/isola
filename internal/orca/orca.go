// Package orca writes isola into Orca's worktree setup hook. Orca (onorca.dev)
// gives each task its own git worktree and runs the repo's orca.yaml
// `scripts.setup` block when it creates a worktree, so adding `isola up` there
// brings the worktree's isolated environment up automatically. Teardown of a
// removed worktree needs no archive hook: isola reconciles removed worktrees
// automatically (see internal/process.ReconcileOrphans).
package orca

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is Orca's per-repo config, at the repository root.
const FileName = "orca.yaml"

// setupCmd starts the worktree's environment on create.
const setupCmd = "isola up"

// Upsert ensures orca.yaml at path runs `isola up` in Orca's worktree setup hook
// (scripts.setup), creating the file if absent and preserving existing setup
// content and the rest of the file. Returns whether the file changed. Teardown
// is not wired: isola reconciles removed worktrees automatically.
func Upsert(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}

	var doc yaml.Node
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return false, fmt.Errorf("parsing %s: %w", filepath.Base(path), err)
		}
	}
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s: top level is not a mapping", filepath.Base(path))
	}
	scripts := mapGet(root, "scripts")
	if scripts == nil || scripts.Kind != yaml.MappingNode {
		scripts = &yaml.Node{Kind: yaml.MappingNode}
		mapSet(root, "scripts", scripts)
	}

	// setup: append `isola up` unless already present.
	setup := scalarChild(scripts, "setup")
	if containsLine(setup.Value, setupCmd) {
		return false, nil
	}
	setup.Value = appendBlock(setup.Value, setupCmd)
	setup.Style = yaml.LiteralStyle

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return false, fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return false, fmt.Errorf("writing %s: %w", filepath.Base(path), err)
	}
	return true, nil
}

// scalarChild returns the scalar under key in mapping m, creating an empty one
// if it is missing or not a scalar.
func scalarChild(m *yaml.Node, key string) *yaml.Node {
	n := mapGet(m, key)
	if n == nil || n.Kind != yaml.ScalarNode {
		n = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
		mapSet(m, key, n)
	}
	return n
}

// appendBlock appends add after existing content (or returns add if empty).
func appendBlock(existing, add string) string {
	if v := strings.TrimRight(existing, "\n"); v != "" {
		return v + "\n" + add
	}
	return add
}

// containsLine reports whether any line of s (trimmed) equals line.
func containsLine(s, line string) bool {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == line {
			return true
		}
	}
	return false
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
}
