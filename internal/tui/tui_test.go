package tui

import (
	"path/filepath"
	"testing"

	"github.com/bytenote/jmp/internal/store"
	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T, paths ...string) (*Model, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "db.json")
	s, err := store.Load(dbPath)
	if err != nil {
		t.Fatalf("load store: %v", err)
	}
	for _, p := range paths {
		s.Add(p)
	}
	return New(s, dbPath, "test"), s
}

func TestBoostKeyAddsWeight(t *testing.T) {
	m, s := newTestModel(t, "/tmp/demo/alpha")

	m.mode = modeBoost
	m.input.SetValue("7.5")
	_, _ = m.handleBoostKey(tea.KeyMsg{Type: tea.KeyEnter})

	e, ok := s.Entry("/tmp/demo/alpha")
	if !ok {
		t.Fatal("entry missing after boost")
	}
	if e.Weight != 8.5 {
		t.Errorf("expected weight 8.5, got %.1f", e.Weight)
	}
	if m.mode != modeList {
		t.Errorf("expected to return to list mode, got %v", m.mode)
	}
	if m.status != "BOOSTED +7.5 ×1" {
		t.Errorf("unexpected status: %q", m.status)
	}
}

func TestBoostKeyAppliesToMarked(t *testing.T) {
	m, s := newTestModel(t, "/tmp/demo/a", "/tmp/demo/b")
	m.marked["/tmp/demo/a"] = true
	m.marked["/tmp/demo/b"] = true

	m.mode = modeBoost
	m.input.SetValue("3")
	_, _ = m.handleBoostKey(tea.KeyMsg{Type: tea.KeyEnter})

	for _, path := range []string{"/tmp/demo/a", "/tmp/demo/b"} {
		e, ok := s.Entry(path)
		if !ok {
			t.Fatalf("entry %s missing after boost", path)
		}
		if e.Weight != 4 {
			t.Errorf("%s: expected weight 4, got %.1f", path, e.Weight)
		}
	}
	if len(m.marked) != 0 {
		t.Errorf("expected marks cleared, got %d", len(m.marked))
	}
}

func TestBoostKeyRejectsInvalidInput(t *testing.T) {
	m, s := newTestModel(t, "/tmp/demo/alpha")
	m.mode = modeBoost
	m.input.SetValue("abc")
	_, _ = m.handleBoostKey(tea.KeyMsg{Type: tea.KeyEnter})

	e, _ := s.Entry("/tmp/demo/alpha")
	if e.Weight != 1 {
		t.Errorf("expected weight unchanged, got %.1f", e.Weight)
	}
	if !m.isErr || m.status != "INVALID BOOST" {
		t.Errorf("expected INVALID BOOST error, got %q (err=%v)", m.status, m.isErr)
	}
}
