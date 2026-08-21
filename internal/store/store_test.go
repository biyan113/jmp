package store

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAddAndQuery(t *testing.T) {
	s := &Store{}
	s.Add("/home/user/projects/myapp")
	s.Add("/home/user/projects/myapp")
	s.Add("/home/user/documents")

	results := s.QueryEntries([]string{"myapp"})
	if len(results) == 0 {
		t.Fatal("expected results for myapp")
	}
	if results[0].Path != "/home/user/projects/myapp" {
		t.Errorf("expected myapp, got %s", results[0].Path)
	}
	if results[0].Visits != 2 {
		t.Errorf("expected 2 visits, got %d", results[0].Visits)
	}
}

func TestMultiTermQuery(t *testing.T) {
	s := &Store{}
	s.Add("/home/user/projects/myapp")
	s.Add("/home/user/other/projects")

	results := s.QueryEntries([]string{"projects", "myapp"})
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Path != "/home/user/projects/myapp" {
		t.Errorf("unexpected path: %s", results[0].Path)
	}
}

func TestRemove(t *testing.T) {
	s := &Store{}
	s.Add("/tmp/foo")
	if !s.Remove("/tmp/foo") {
		t.Fatal("expected remove to return true")
	}
	if s.Remove("/tmp/foo") {
		t.Fatal("expected second remove to return false")
	}
}

func TestFrecencyDecay(t *testing.T) {
	s := &Store{}
	s.Entries = []*Entry{
		{Path: "/old", Weight: 100.0, Visits: 100, LastVisit: time.Now().Add(-30 * 24 * time.Hour)},
		{Path: "/new", Weight: 10.0, Visits: 10, LastVisit: time.Now()},
	}

	old := s.Entries[0].Frecency()
	newE := s.Entries[1].Frecency()
	if newE <= old {
		t.Errorf("expected new entry (%f) to beat old (%f) on frecency", newE, old)
	}
}

func TestSaveLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.json")

	s := &Store{path: path}
	s.Add("/tmp/testdir")
	if err := s.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	s2, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(s2.Entries) != 1 {
		t.Errorf("expected 1 entry, got %d", len(s2.Entries))
	}
}

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		target  string
		pattern string
		want    bool
	}{
		{"projects", "prj", true},
		{"projects", "pcts", true},
		{"myapp", "map", true},
		{"myapp", "mxp", false},
		{"documents", "docs", true},
		{"ab", "abc", false},
	}
	for _, c := range cases {
		got := fuzzyMatchComponent(c.target, c.pattern)
		if got != c.want {
			t.Errorf("fuzzyMatchComponent(%q, %q) = %v, want %v", c.target, c.pattern, got, c.want)
		}
	}
}

func TestMatchTypePriority(t *testing.T) {
	s := &Store{}
	s.Add("/home/user/projects/app")
	s.Add("/home/user/production/app")

	results := s.Query([]string{"prj"})
	if len(results) == 0 {
		t.Fatal("expected fuzzy results for prj")
	}
	found := false
	for _, r := range results {
		if strings.Contains(r.Entry.Path, "projects") {
			found = true
			if r.MatchType != MatchFuzzy {
				t.Errorf("expected MatchFuzzy, got %v", r.MatchType)
			}
		}
	}
	if !found {
		t.Error("expected projects path in results")
	}

	results2 := s.Query([]string{"proj"})
	for _, r := range results2 {
		if strings.Contains(r.Entry.Path, "projects") && r.MatchType != MatchSubstr {
			t.Errorf("expected MatchSubstr for proj, got %v", r.MatchType)
		}
	}
}

func TestSetWeight(t *testing.T) {
	s := &Store{}
	s.Add("/tmp/foo")
	if !s.SetWeight("/tmp/foo", 42.0) {
		t.Fatal("expected SetWeight to return true")
	}
	results := s.Query([]string{"foo"})
	if len(results) == 0 || results[0].Entry.Weight != 42.0 {
		t.Error("weight not updated")
	}
}

func TestAliasLifecycle(t *testing.T) {
	s := &Store{}
	s.SetAlias("@proj", "/tmp/project")
	path, ok := s.ResolveAlias("proj")
	if !ok {
		t.Fatal("expected alias to resolve")
	}
	if path != filepath.Clean("/tmp/project") {
		t.Fatalf("unexpected alias path: %s", path)
	}
	aliases := s.AliasesForPath("/tmp/project")
	if len(aliases) != 1 || aliases[0] != "@proj" {
		t.Fatalf("unexpected aliases: %#v", aliases)
	}
	if !s.RemoveAlias("@proj") {
		t.Fatal("expected alias removal")
	}
	if _, ok := s.ResolveAlias("proj"); ok {
		t.Fatal("expected alias to be removed")
	}
}

func TestConcurrentSaveUsesUniqueTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.json")
	s := &Store{path: path}
	s.Add("/tmp/testdir")

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.Save()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("save failed: %v", err)
		}
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("load after concurrent saves failed: %v", err)
	}
}

func TestCleanMissingRemovesGoneDirsAndKeepsLive(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	gone := filepath.Join(root, "gone")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatalf("mkdir live: %v", err)
	}
	if err := os.MkdirAll(gone, 0o755); err != nil {
		t.Fatalf("mkdir gone: %v", err)
	}

	s := &Store{}
	s.Add(live)
	s.Add(gone)
	s.SetAlias("live", live)
	s.SetAlias("gone", gone)
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("remove gone: %v", err)
	}

	missing := s.MissingEntries()
	if len(missing) != 1 || missing[0].Path != gone {
		t.Fatalf("expected only gone path missing, got %#v", missing)
	}

	removedPaths, removedAliases := s.CleanMissing()
	if len(removedPaths) != 1 || removedPaths[0] != gone {
		t.Fatalf("unexpected removed paths: %#v", removedPaths)
	}
	if len(removedAliases) != 1 || removedAliases[0] != "gone" {
		t.Fatalf("unexpected removed aliases: %#v", removedAliases)
	}
	if len(s.All()) != 1 || s.All()[0].Path != live {
		t.Fatalf("expected only live entry to remain, got %#v", s.All())
	}
	if _, ok := s.ResolveAlias("gone"); ok {
		t.Fatal("expected gone alias to be removed")
	}
	if path, ok := s.ResolveAlias("live"); !ok || path != live {
		t.Fatalf("expected live alias to remain, got %s ok=%v", path, ok)
	}
}

func TestCleanMissingTreatsFileAsStale(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(filePath, []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	s := &Store{Entries: []*Entry{{Path: filePath, Weight: 1, LastVisit: time.Now()}}}
	removed, _ := s.CleanMissing()
	if len(removed) != 1 || removed[0] != filePath {
		t.Fatalf("expected file path to be pruned, got %#v", removed)
	}
}

func TestIsExistingDir(t *testing.T) {
	dir := t.TempDir()
	if !IsExistingDir(dir) {
		t.Fatalf("expected temp dir to exist: %s", dir)
	}
	missing := filepath.Join(dir, "nope")
	if IsExistingDir(missing) {
		t.Fatalf("expected missing path to be false: %s", missing)
	}
}

func TestAging(t *testing.T) {
	s := &Store{}
	for i := 0; i < 100; i++ {
		s.Entries = append(s.Entries, &Entry{
			Path:      filepath.Join(os.TempDir(), "dir"+string(rune('a'+i))),
			Weight:    100.0,
			Visits:    1,
			LastVisit: time.Now(),
		})
	}
	s.Add("/tmp/trigger")
	for _, e := range s.Entries {
		if e.Weight > 100.0 {
			t.Errorf("expected weight <= 100 after aging, got %f for %s", e.Weight, e.Path)
		}
	}
}
