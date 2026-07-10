package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return &Store{
		dir:      dir,
		filePath: filepath.Join(dir, "registry.json"),
		lockPath: filepath.Join(dir, "registry.lock"),
	}
}

// stateDir returns a real, existing directory to stand in for a repo's .isola.
func stateDir(t *testing.T) string { t.Helper(); return t.TempDir() }

func TestRegisterListAndPorts(t *testing.T) {
	s := newTestStore(t)
	a := Project{Name: "projA", StateDir: stateDir(t), ProxyPorts: []int{3000, 8000}}
	b := Project{Name: "projB", StateDir: stateDir(t), ProxyPorts: []int{3000, 8001}}
	if err := s.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(b); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v (err %v), want 2", list, err)
	}
	ports, _ := s.Ports()
	want := []int{3000, 8000, 8001}
	if len(ports) != 3 || ports[0] != want[0] || ports[1] != want[1] || ports[2] != want[2] {
		t.Errorf("Ports = %v, want %v (deduped, sorted)", ports, want)
	}
}

func TestRegisterReplacesSameStateDir(t *testing.T) {
	s := newTestStore(t)
	dir := stateDir(t)
	_ = s.Register(Project{Name: "p", StateDir: dir, ProxyPorts: []int{3000}})
	if err := s.Register(Project{Name: "p", StateDir: dir, ProxyPorts: []int{3000, 9000}}); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || len(list[0].ProxyPorts) != 2 {
		t.Fatalf("expected one refreshed entry with 2 ports, got %v", list)
	}
}

func TestRegisterNameClash(t *testing.T) {
	s := newTestStore(t)
	_ = s.Register(Project{Name: "dup", StateDir: stateDir(t), ProxyPorts: []int{3000}})
	err := s.Register(Project{Name: "dup", StateDir: stateDir(t), ProxyPorts: []int{3001}})
	if err == nil {
		t.Fatal("expected a name-clash error for a different state dir")
	}
}

func TestPrunesMissingStateDir(t *testing.T) {
	s := newTestStore(t)
	gone := stateDir(t)
	_ = s.Register(Project{Name: "gone", StateDir: gone, ProxyPorts: []int{3000}})
	live := stateDir(t)
	_ = s.Register(Project{Name: "live", StateDir: live, ProxyPorts: []int{3001}})

	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].Name != "live" {
		t.Errorf("stale entry not pruned: %v", list)
	}
}

func TestDeregister(t *testing.T) {
	s := newTestStore(t)
	dir := stateDir(t)
	_ = s.Register(Project{Name: "p", StateDir: dir, ProxyPorts: []int{3000}})
	if err := s.Deregister(dir); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 0 {
		t.Errorf("expected empty after deregister, got %v", list)
	}
}

func TestLookup(t *testing.T) {
	s := newTestStore(t)
	dir := stateDir(t)
	_ = s.Register(Project{Name: "findme", StateDir: dir, ProxyPorts: []int{3000}})
	p, ok, err := s.Lookup("findme")
	if err != nil || !ok || p.StateDir != dir {
		t.Fatalf("Lookup = %+v ok=%v err=%v", p, ok, err)
	}
	if _, ok, _ := s.Lookup("absent"); ok {
		t.Error("Lookup of absent project should be ok=false")
	}
}
