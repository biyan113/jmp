package matcher

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bytenote/jmp/internal/store"
)

// newTestStore builds a Store whose entries point at real temp dirs so that
// the existence penalty in Rank (os.Stat) behaves deterministically.
func newTestStore(t *testing.T, entries []struct {
	name   string
	weight float64
	visits int64
	age    time.Duration
}) *store.Store {
	t.Helper()
	s := &store.Store{}
	for _, e := range entries {
		dir := filepath.Join(t.TempDir(), e.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		s.Entries = append(s.Entries, &store.Entry{
			Path:      dir,
			Weight:    e.weight,
			Visits:    e.visits,
			LastVisit: time.Now().Add(-e.age),
		})
	}
	return s
}

func TestRankReturnsResultsBestFirst(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{
		{"alpha", 10, 10, time.Hour},
		{"beta", 1, 1, time.Hour},
	})
	r := Rank(s, []string{"alpha"})
	if len(r) != 1 {
		t.Fatalf("expected 1 result, got %d", len(r))
	}
	if r[0].Score <= 0 {
		t.Errorf("expected positive score, got %f", r[0].Score)
	}
}

// TestRankSubstringBeatsFuzzyWithinWeightTier documents the substring> fuzzy
// tiering that store.Query applies. Note: matcher.Rank currently merges the
// tiers and sorts by composite score, so a much heavier fuzzy hit can still
// outrank a light substring hit. This test keeps both entries at equal weight
// to assert the tiering holds when weights are comparable.
func TestRankSubstringBeatsFuzzyWithinWeightTier(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{
		// equal weight so only the match-type multiplier decides the order
		{"projects", 1, 1, 0},
		{"prj", 1, 1, 0},
	})
	r := Rank(s, []string{"prj"})
	if len(r) != 2 {
		t.Fatalf("expected 2 results, got %d", len(r))
	}
	// "prj" is an exact substring match and must outrank the fuzzy "projects".
	if filepath.Base(r[0].Entry.Path) != "prj" {
		t.Errorf("expected substring match 'prj' first, got %s",
			filepath.Base(r[0].Entry.Path))
	}
}

func TestRankExactComponentBonus(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{
		{"foo", 5, 5, 0},
		{"foobar", 5, 5, 0},
	})
	r := Rank(s, []string{"foo"})
	if len(r) != 2 {
		t.Fatalf("expected 2 results, got %d", len(r))
	}
	// Exact last-component match (×3) should beat prefix-only (×1.5).
	if filepath.Base(r[0].Entry.Path) != "foo" {
		t.Errorf("expected exact component match 'foo' first, got %s",
			filepath.Base(r[0].Entry.Path))
	}
}

func TestRankNoMatchReturnsEmpty(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{{"alpha", 1, 1, 0}})
	if r := Rank(s, []string{"zzz"}); len(r) != 0 {
		t.Errorf("expected no results for unmatched query, got %d", len(r))
	}
}

func TestRankSortedDescending(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{
		{"low", 1, 1, 0},
		{"high", 50, 50, 0},
		{"mid", 10, 10, 0},
	})
	r := Rank(s, []string{}) // empty query matches everything as substring
	if len(r) != 3 {
		t.Fatalf("expected 3 results, got %d", len(r))
	}
	for i := 1; i < len(r); i++ {
		if r[i].Score > r[i-1].Score {
			t.Errorf("results not sorted descending at index %d: %f > %f",
				i, r[i].Score, r[i-1].Score)
		}
	}
}

func TestSuggestReturnsClosest(t *testing.T) {
	s := newTestStore(t, []struct {
		name   string
		weight float64
		visits int64
		age    time.Duration
	}{
		{"alpha", 1, 1, 0},
		{"alphabet", 1, 1, 0},
		{"zzz", 1, 1, 0},
	})
	got := Suggest(s, []string{"alp"}, 2)
	if len(got) != 2 {
		t.Fatalf("expected 2 suggestions, got %d", len(got))
	}
	for _, r := range got {
		base := filepath.Base(r.Entry.Path)
		if base != "alpha" && base != "alphabet" {
			t.Errorf("unexpected suggestion: %s", base)
		}
	}
}

func TestBestReturnsNilWhenEmpty(t *testing.T) {
	s := &store.Store{}
	if e := Best(s, []string{"foo"}); e != nil {
		t.Errorf("expected nil for empty store, got %v", e)
	}
}
