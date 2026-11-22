package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureNeverOverwritesExistingWeight(t *testing.T) {
	s := &Store{}
	s.AddManual("/tmp/heavy", 500)
	if s.Ensure("/tmp/heavy", 10) {
		t.Fatal("Ensure reported creating an entry that already existed")
	}
	if e, _ := s.Entry("/tmp/heavy"); e.Weight != 500 {
		t.Fatalf("weight changed to %.1f, want 500", e.Weight)
	}
	if !s.Ensure("/tmp/new", 10) {
		t.Fatal("Ensure did not create a missing entry")
	}
}

// Many processes doing read-modify-write must not lose each other's updates.
func TestUpdateIsSafeAcrossConcurrentWriters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json")
	const writers = 20
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := New(path) // a separate Store per writer, like separate shells
			errs <- s.Update(func() error {
				s.Add("/tmp/shared")
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("update failed: %v", err)
		}
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := s.Entry("/tmp/shared"); !ok || e.Visits != writers {
		t.Fatalf("visits = %+v, want %d (updates were lost)", e, writers)
	}
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Errorf("lock file left behind: %v", err)
	}
}

func TestUpdateDoesNotSaveWhenFnFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json")
	s := New(path)
	boom := errors.New("boom")
	if err := s.Update(func() error { s.Add("/tmp/x"); return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("database was written despite the failed update")
	}
}

func TestAcquireLockTimesOutAndBreaksStaleLocks(t *testing.T) {
	lock := filepath.Join(t.TempDir(), "x.lock")
	release, err := AcquireLock(lock, 0, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(lock, 30*time.Millisecond, time.Hour); !errors.Is(err, ErrLocked) {
		t.Fatalf("second acquire: err = %v, want ErrLocked", err)
	}
	// A lock older than the stale threshold is taken over.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	release2, err := AcquireLock(lock, 0, time.Second)
	if err != nil {
		t.Fatalf("stale lock not broken: %v", err)
	}
	release2()
	release()
}

func TestLoadReportsCorruptionAndRecoversFromBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "db.json")
	s := New(path)
	s.AddManual("/tmp/keep", 42)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Second save snapshots the first as the backup.
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(BackupPath(path)); err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Load err = %v, want ErrCorrupt", err)
	}
	got, note, err := LoadOrRecover(path)
	if err != nil {
		t.Fatal(err)
	}
	if note == "" {
		t.Error("recovery happened silently")
	}
	if e, ok := got.Entry("/tmp/keep"); !ok || e.Weight != 42 {
		t.Fatalf("backup not restored: %+v", e)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Errorf("damaged file not kept aside: %v", matches)
	}
	if _, err := Load(path); err != nil {
		t.Errorf("database still unreadable after recovery: %v", err)
	}
}

func TestCorruptFileNeverReplacesGoodBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json")
	if err := os.WriteFile(path, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(path)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(BackupPath(path)); !os.IsNotExist(err) {
		t.Fatal("an unparsable database was copied to the backup")
	}
}

func TestMergeReportsAndPropagatesDeletion(t *testing.T) {
	a, b := New(""), New("")
	a.Add("/p/one")
	a.Add("/p/two")
	// b learns both, then a deletes one.
	if st := b.MergeFrom(a); len(st.Added) != 2 {
		t.Fatalf("Added = %v, want 2 paths", st.Added)
	}
	a.Remove("/p/one")

	st := b.MergeFrom(a)
	if len(st.Removed) != 1 || st.Removed[0] != "/p/one" {
		t.Fatalf("Removed = %v, want [/p/one]", st.Removed)
	}
	// And the deletion must not be undone when a merges b back.
	b2 := New("")
	b2.Add("/p/one") // stale copy from before the deletion...
	b2.Entries[0].LastVisit = time.Now().Add(-time.Hour)
	if st := a.MergeFrom(b2); len(st.Added) != 0 {
		t.Fatalf("deleted entry resurrected: %v", st.Added)
	}
}

func TestMergeKeepsEntryVisitedAfterDeletion(t *testing.T) {
	a, b := New(""), New("")
	a.Add("/p/x")
	a.Remove("/p/x")
	a.Deleted["/p/x"] = time.Now().Add(-time.Hour) // deleted an hour ago
	b.Add("/p/x")                                  // visited just now elsewhere

	if st := b.MergeFrom(a); len(st.Removed) != 0 {
		t.Fatal("an entry visited after the deletion was removed")
	}
	if st := a.MergeFrom(b); len(st.Added) != 1 {
		t.Fatalf("entry revisited after deletion did not come back: %+v", st)
	}
	if _, still := a.Deleted["/p/x"]; still {
		t.Error("tombstone kept for a live entry")
	}
}

func TestCleanMissingLeavesNoTombstones(t *testing.T) {
	s := New("")
	s.Add(filepath.Join(t.TempDir(), "gone"))
	if removed, _ := s.CleanMissing(); len(removed) != 1 {
		t.Fatalf("removed = %v", removed)
	}
	if len(s.Deleted) != 0 {
		t.Fatal("clean must stay local: a path missing here may exist on another device")
	}
}

func TestMergeAliasNewestWinsAndRemovalPropagates(t *testing.T) {
	a, b := New(""), New("")
	if err := a.SetAlias("proj", "/a/old"); err != nil {
		t.Fatal(err)
	}
	a.AliasTimes["proj"] = time.Now().Add(-time.Hour)
	if err := b.SetAlias("proj", "/b/new"); err != nil {
		t.Fatal(err)
	}

	if st := a.MergeFrom(b); len(st.AliasesUpdated) != 1 {
		t.Fatalf("newer alias did not win: %+v", st)
	}
	if p, _ := a.ResolveAlias("proj"); !strings.HasSuffix(p, filepath.FromSlash("/b/new")) {
		t.Fatalf("alias = %s", p)
	}
	// The older definition must not win in the other direction.
	c := New("")
	_ = c.SetAlias("proj", "/c/older")
	c.AliasTimes["proj"] = time.Now().Add(-2 * time.Hour)
	if st := a.MergeFrom(c); st.Changes() != 0 {
		t.Fatalf("older alias overwrote newer: %+v", st)
	}

	b.RemoveAlias("proj")
	if st := a.MergeFrom(b); len(st.AliasesRemoved) != 1 {
		t.Fatalf("alias removal did not propagate: %+v", st)
	}
	if st := b.MergeFrom(a); len(st.AliasesAdded) != 0 {
		t.Fatal("removed alias resurrected")
	}
}

func TestAliasNameValidation(t *testing.T) {
	cases := map[string]bool{
		"proj": true, "@proj": true, "My-Proj_2.0": true,
		"": false, "@": false, "a b": false, "-x": false, "a/b": false,
	}
	for name, want := range cases {
		if got := ValidAliasName(name); got != want {
			t.Errorf("ValidAliasName(%q) = %v, want %v", name, got, want)
		}
	}
	if err := New("").SetAlias("a b", "/tmp"); err == nil {
		t.Error("SetAlias accepted an invalid name")
	}
}

func TestOldDatabaseFormatStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "db.json")
	old := `{"entries":[{"path":"/p/a","weight":3,"last_visit":"2026-01-02T03:04:05Z","visits":3}],"aliases":{"a":"/p/a"}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Remove("/p/a") || !s.RemoveAlias("a") {
		t.Fatal("could not modify a store loaded from the pre-tombstone format")
	}
}
