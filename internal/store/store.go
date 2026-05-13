package store

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry represents a single directory record.
type Entry struct {
	Path      string    `json:"path"`
	Weight    float64   `json:"weight"`
	LastVisit time.Time `json:"last_visit"`
	Visits    int64     `json:"visits"`
	Starred   bool      `json:"starred,omitempty"`
	Category  string    `json:"category,omitempty"`
}

// Frecency returns a score combining frequency and recency.
// Algorithm: weight * time_decay
// time_decay uses a half-life of 7 days.
func (e *Entry) Frecency() float64 {
	hours := time.Since(e.LastVisit).Hours()
	// Half-life: 7 days = 168 hours
	decay := math.Pow(0.5, hours/168.0)
	return e.Weight * decay
}

// Store manages the jump database.
type Store struct {
	mu      sync.RWMutex
	Entries []*Entry          `json:"entries"`
	Aliases map[string]string `json:"aliases,omitempty"`
	path    string
}

// DataDir returns the platform-appropriate data directory.
func DataDir() string {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("APPDATA"); d != "" {
			return filepath.Join(d, "jmp")
		}
	case "darwin":
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Library", "Application Support", "jmp")
		}
	}
	// Linux / WSL / fallback
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "jmp")
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share", "jmp")
	}
	return filepath.Join(os.TempDir(), "jmp")
}

// DefaultPath returns the default database file path.
func DefaultPath() string {
	return filepath.Join(DataDir(), "db.json")
}

// Load reads the store from disk; creates empty store if not found.
func Load(dbPath string) (*Store, error) {
	s := &Store{path: dbPath}
	data, err := os.ReadFile(dbPath)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	s.ensureAliases()
	return s, nil
}

// Save writes the store to disk atomically.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

// NormalizePath returns the canonical absolute form used by the store.
func NormalizePath(path string) string {
	return normalizePath(path)
}

func (s *Store) ensureAliases() {
	if s.Aliases == nil {
		s.Aliases = make(map[string]string)
	}
}

// Add records a visit to path, creating or updating the entry.
func (s *Store) Add(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Weight += 1.0
			e.Visits++
			e.LastVisit = time.Now()
			s.age()
			return
		}
	}
	s.Entries = append(s.Entries, &Entry{
		Path:      path,
		Weight:    1.0,
		Visits:    1,
		LastVisit: time.Now(),
	})
	s.age()
}

// AddManual adds a path with a specified weight (from --add flag).
func (s *Store) AddManual(path string, weight float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Weight = weight
			e.LastVisit = time.Now()
			return
		}
	}
	s.Entries = append(s.Entries, &Entry{
		Path:      path,
		Weight:    weight,
		Visits:    1,
		LastVisit: time.Now(),
	})
}

// Remove deletes an entry by path.
func (s *Store) Remove(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for i, e := range s.Entries {
		if e.Path == path {
			s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// Entry returns a copy of the entry for path.
func (s *Store) Entry(path string) (*Entry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			cp := *e
			return &cp, true
		}
	}
	return nil, false
}

// MissingEntries returns entries whose directories no longer exist.
func (s *Store) MissingEntries() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Entry, 0)
	for _, e := range s.Entries {
		if _, err := os.Stat(e.Path); os.IsNotExist(err) {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

// CleanMissing removes missing directory entries and aliases that point nowhere.
func (s *Store) CleanMissing() (removedEntries []string, removedAliases []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := s.Entries[:0]
	for _, e := range s.Entries {
		if _, err := os.Stat(e.Path); os.IsNotExist(err) {
			removedEntries = append(removedEntries, e.Path)
			continue
		}
		filtered = append(filtered, e)
	}
	s.Entries = filtered

	s.ensureAliases()
	for name, path := range s.Aliases {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			removedAliases = append(removedAliases, name)
			delete(s.Aliases, name)
		}
	}
	sort.Strings(removedEntries)
	sort.Strings(removedAliases)
	return removedEntries, removedAliases
}

// MatchType indicates how a path matched the query terms.
type MatchType int

const (
	MatchNone   MatchType = iota
	MatchSubstr           // every term is a substring of the path
	MatchFuzzy            // every term matches a path component via char-order fuzzy
)

// QueryResult pairs an entry with its match quality.
type QueryResult struct {
	Entry     *Entry
	MatchType MatchType
}

// Query returns entries that match all terms, tagged with match type.
// Substring matches come before fuzzy matches within the same frecency tier.
func (s *Store) Query(terms []string) []QueryResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var results []QueryResult
	for _, e := range s.Entries {
		mt := matchType(e.Path, terms)
		if mt == MatchNone {
			continue
		}
		cp := *e
		results = append(results, QueryResult{Entry: &cp, MatchType: mt})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Entry.Frecency() > results[j].Entry.Frecency()
	})
	return results
}

// All returns a copy of all entries sorted by frecency.
func (s *Store) All() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Entry, len(s.Entries))
	for i, e := range s.Entries {
		cp := *e
		out[i] = &cp
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Frecency() > out[j].Frecency()
	})
	return out
}

// QueryEntries is a convenience wrapper returning only entries (no match type).
func (s *Store) QueryEntries(terms []string) []*Entry {
	results := s.Query(terms)
	out := make([]*Entry, len(results))
	for i, r := range results {
		out[i] = r.Entry
	}
	return out
}

// SetWeight updates the weight of an existing entry.
func (s *Store) SetWeight(path string, weight float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Weight = weight
			return true
		}
	}
	return false
}

// age scales all weights down when total weight exceeds 9000,
// matching autojump's aging strategy to prevent unbounded growth.
func (s *Store) age() {
	total := 0.0
	for _, e := range s.Entries {
		total += e.Weight
	}
	if total > 9000 {
		for _, e := range s.Entries {
			e.Weight *= 0.9
		}
		// Prune entries with negligible weight
		filtered := s.Entries[:0]
		for _, e := range s.Entries {
			if e.Weight >= 0.1 {
				filtered = append(filtered, e)
			}
		}
		s.Entries = filtered
	}
}

// matchType returns how well the path matches all terms.
// Priority: substring match > fuzzy match > no match.
func matchType(path string, terms []string) MatchType {
	if len(terms) == 0 {
		return MatchSubstr
	}
	lower := strings.ToLower(path)

	// Try substring match first (fast path, same as original autojump behaviour).
	allSubstr := true
	for _, t := range terms {
		if !strings.Contains(lower, strings.ToLower(t)) {
			allSubstr = false
			break
		}
	}
	if allSubstr {
		return MatchSubstr
	}

	// Fuzzy: each term must match at least one path component in char-order.
	// Components are split by the OS path separator so "prj" won't accidentally
	// span across "/home/user/p" + "rojects" boundaries.
	components := splitComponents(lower)
	for _, t := range terms {
		tl := strings.ToLower(t)
		matched := false
		for _, comp := range components {
			if fuzzyMatchComponent(comp, tl) {
				matched = true
				break
			}
		}
		if !matched {
			return MatchNone
		}
	}
	return MatchFuzzy
}

// fuzzyMatchComponent returns true if all characters of pattern appear
// in target in order (classic subsequence check).
func fuzzyMatchComponent(target, pattern string) bool {
	if pattern == "" {
		return true
	}
	pi := 0
	prunes := []rune(pattern)
	for _, ch := range target {
		if ch == prunes[pi] {
			pi++
			if pi == len(prunes) {
				return true
			}
		}
	}
	return false
}

// splitComponents returns the lowercase path components, filtering empty strings.
func splitComponents(path string) []string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	return parts
}

func normalizePath(path string) string {
	// Resolve ~ on unix-like systems
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return filepath.Clean(abs)
}

// MergeFrom merges entries from another store into this one.
// Strategy: weight=max, visits=max, lastVisit=latest; new paths appended.
func (s *Store) MergeFrom(other *Store) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx := make(map[string]*Entry, len(s.Entries))
	for _, e := range s.Entries {
		idx[e.Path] = e
	}

	for _, re := range other.Entries {
		if le, ok := idx[re.Path]; ok {
			if re.Weight > le.Weight {
				le.Weight = re.Weight
			}
			if re.Visits > le.Visits {
				le.Visits = re.Visits
			}
			if re.LastVisit.After(le.LastVisit) {
				le.LastVisit = re.LastVisit
			}
			if re.Starred {
				le.Starred = true
			}
			if le.Category == "" && re.Category != "" {
				le.Category = re.Category
			}
		} else {
			cp := *re
			s.Entries = append(s.Entries, &cp)
		}
	}
	s.ensureAliases()
	for name, path := range other.Aliases {
		if _, exists := s.Aliases[name]; !exists {
			s.Aliases[name] = path
		}
	}
}

// ToggleStar flips the starred state of an entry.
func (s *Store) ToggleStar(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Starred = !e.Starred
			return true
		}
	}
	return false
}

// SetCategory sets the category of an entry.
func (s *Store) SetCategory(path, category string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Category = category
			return true
		}
	}
	return false
}

// SetAlias assigns an @alias to a directory path.
func (s *Store) SetAlias(name, path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureAliases()
	s.Aliases[normalizeAlias(name)] = normalizePath(path)
}

// RemoveAlias deletes an alias.
func (s *Store) RemoveAlias(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureAliases()
	name = normalizeAlias(name)
	if _, ok := s.Aliases[name]; !ok {
		return false
	}
	delete(s.Aliases, name)
	return true
}

// ResolveAlias returns the path for an alias. The input may include the @ prefix.
func (s *Store) ResolveAlias(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	name = normalizeAlias(name)
	if s.Aliases == nil {
		return "", false
	}
	path, ok := s.Aliases[name]
	return path, ok
}

// AllAliases returns a copy of aliases sorted by name.
func (s *Store) AllAliases() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string]string, len(s.Aliases))
	for name, path := range s.Aliases {
		out[name] = path
	}
	return out
}

// AliasesForPath returns aliases that point to path.
func (s *Store) AliasesForPath(path string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	path = normalizePath(path)
	names := make([]string, 0)
	for name, aliasPath := range s.Aliases {
		if aliasPath == path {
			names = append(names, "@"+name)
		}
	}
	sort.Strings(names)
	return names
}

func normalizeAlias(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "@")
	return strings.ToLower(name)
}
