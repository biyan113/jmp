package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrCorrupt is returned (wrapped) by Load when the database file exists but
// cannot be parsed.
var ErrCorrupt = errors.New("database is corrupt")

// tombstoneTTL is how long deletion markers are kept so that removals can
// propagate to other devices through sync.
const tombstoneTTL = 180 * 24 * time.Hour

// backupInterval is the minimum age of the .bak file before Save refreshes it.
const backupInterval = 24 * time.Hour

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
	// AliasTimes records when each alias was last set, so sync can resolve
	// conflicting definitions (newest wins).
	AliasTimes map[string]time.Time `json:"alias_times,omitempty"`
	// Deleted / DeletedAliases are tombstones: explicit removals that sync
	// must propagate instead of resurrecting the record from another device.
	Deleted        map[string]time.Time `json:"deleted,omitempty"`
	DeletedAliases map[string]time.Time `json:"deleted_aliases,omitempty"`
	path           string
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

// New returns an empty store bound to dbPath without reading it. Combine with
// Update, which loads the current state under the lock.
func New(dbPath string) *Store {
	s := &Store{path: dbPath}
	s.ensureMaps()
	return s
}

// Load reads the store from disk; creates empty store if not found.
// A file that exists but cannot be parsed yields an error wrapping ErrCorrupt.
func Load(dbPath string) (*Store, error) {
	s := &Store{path: dbPath}
	if err := s.read(); err != nil {
		return nil, err
	}
	return s, nil
}

// read replaces the in-memory state with the file contents. A missing file
// leaves the current state untouched.
func (s *Store) read() error {
	data, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		s.mu.Lock()
		s.ensureMaps()
		s.mu.Unlock()
		return nil
	}
	if err != nil {
		return err
	}
	var fresh Store
	if err := json.Unmarshal(data, &fresh); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrCorrupt, s.path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Entries = fresh.Entries
	s.Aliases = fresh.Aliases
	s.AliasTimes = fresh.AliasTimes
	s.Deleted = fresh.Deleted
	s.DeletedAliases = fresh.DeletedAliases
	s.ensureMaps()
	return nil
}

// BackupPath returns the location of the periodic backup of dbPath.
func BackupPath(dbPath string) string { return dbPath + ".bak" }

// LoadOrRecover behaves like Load, but when the database is corrupt it moves
// the damaged file aside and restores the most recent backup (or starts
// empty). note is non-empty when a recovery happened and describes it.
func LoadOrRecover(dbPath string) (s *Store, note string, err error) {
	s, err = Load(dbPath)
	if err == nil || !errors.Is(err, ErrCorrupt) {
		return s, "", err
	}
	unlock, lerr := AcquireLock(dbPath+".lock", lockWait, lockStale)
	if lerr != nil {
		return nil, "", err
	}
	defer unlock()
	// Another process may have repaired it while we waited for the lock.
	if s, err2 := Load(dbPath); err2 == nil {
		return s, "", nil
	}
	aside := dbPath + ".corrupt-" + time.Now().Format("20060102T150405")
	if rerr := os.Rename(dbPath, aside); rerr != nil {
		return nil, "", err
	}
	s = &Store{path: BackupPath(dbPath)}
	source := "backup " + BackupPath(dbPath)
	if rerr := s.read(); rerr != nil || !fileExists(BackupPath(dbPath)) {
		s = &Store{}
		s.ensureMaps()
		source = "an empty database (no usable backup)"
	}
	s.path = dbPath
	if serr := s.Save(); serr != nil {
		return nil, "", serr
	}
	return s, fmt.Sprintf("database was corrupt; damaged file kept at %s, continuing from %s", aside, source), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Update applies fn as one atomic read-modify-write cycle: it takes the
// cross-process lock, reloads the latest state from disk, runs fn and saves.
// Use it for every mutation so concurrent shells do not overwrite each other.
// If fn returns an error nothing is written.
func (s *Store) Update(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	unlock, err := AcquireLock(s.path+".lock", lockWait, lockStale)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.read(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		return err
	}
	return s.Save()
}

// Save writes the store to disk atomically. It does not take the
// cross-process lock; prefer Update for read-modify-write cycles.
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
	s.refreshBackup()
	return writeFileAtomic(s.path, data)
}

// refreshBackup copies the current on-disk database to <db>.bak when the
// backup is missing or older than backupInterval. Best effort: failures are
// ignored, and an unparsable file is never allowed to replace a good backup.
func (s *Store) refreshBackup() {
	bak := BackupPath(s.path)
	if info, err := os.Stat(bak); err == nil && time.Since(info.ModTime()) < backupInterval {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil || !json.Valid(data) {
		return
	}
	_ = writeFileAtomic(bak, data)
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// NormalizePath returns the canonical absolute form used by the store.
func NormalizePath(path string) string {
	return normalizePath(path)
}

func (s *Store) ensureAliases() { s.ensureMaps() }

func (s *Store) ensureMaps() {
	if s.Aliases == nil {
		s.Aliases = make(map[string]string)
	}
	if s.AliasTimes == nil {
		s.AliasTimes = make(map[string]time.Time)
	}
	if s.Deleted == nil {
		s.Deleted = make(map[string]time.Time)
	}
	if s.DeletedAliases == nil {
		s.DeletedAliases = make(map[string]time.Time)
	}
}

// Add records a visit to path, creating or updating the entry.
func (s *Store) Add(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	delete(s.Deleted, path)
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

// Ensure adds path with the given weight only if it is not recorded yet.
// Unlike AddManual it never touches an existing entry. It reports whether a
// new entry was created.
func (s *Store) Ensure(path string, weight float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			return false
		}
	}
	delete(s.Deleted, path)
	s.Entries = append(s.Entries, &Entry{
		Path:      path,
		Weight:    weight,
		Visits:    1,
		LastVisit: time.Now(),
	})
	return true
}

// AddManual sets the weight of path explicitly, creating the entry if needed.
// An existing entry's weight is overwritten.
func (s *Store) AddManual(path string, weight float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	delete(s.Deleted, path)
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

// Remove deletes an entry by path and leaves a tombstone so the removal
// propagates through sync instead of being undone by another device.
func (s *Store) Remove(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for i, e := range s.Entries {
		if e.Path == path {
			s.Entries = append(s.Entries[:i], s.Entries[i+1:]...)
			s.ensureMaps()
			s.Deleted[path] = time.Now()
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

// IsExistingDir reports whether path is a directory that currently exists.
func IsExistingDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// isStaleDir reports whether path should be dropped from jump data.
// Missing paths and non-directories both count; permission / IO errors do not,
// so a temporarily unreadable mount is not pruned.
func isStaleDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return os.IsNotExist(err)
	}
	return !info.IsDir()
}

// MissingEntries returns entries whose directories no longer exist.
func (s *Store) MissingEntries() []*Entry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]*Entry, 0)
	for _, e := range s.Entries {
		if isStaleDir(e.Path) {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

// CleanMissing removes missing directory entries and aliases that point nowhere.
// It deliberately leaves no tombstones: a path missing on this machine may be
// perfectly valid on another device that shares the database through sync.
func (s *Store) CleanMissing() (removedEntries []string, removedAliases []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	filtered := s.Entries[:0]
	for _, e := range s.Entries {
		if isStaleDir(e.Path) {
			removedEntries = append(removedEntries, e.Path)
			continue
		}
		filtered = append(filtered, e)
	}
	s.Entries = filtered

	s.ensureAliases()
	for name, path := range s.Aliases {
		if isStaleDir(path) {
			removedAliases = append(removedAliases, name)
			delete(s.Aliases, name)
			delete(s.AliasTimes, name)
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

// BoostWeight adds delta to an existing entry's weight and marks it as visited
// now, so the manual boost is not immediately eaten by recency decay.
// The resulting weight is clamped at zero. It reports whether the path exists.
func (s *Store) BoostWeight(path string, delta float64) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path = normalizePath(path)
	for _, e := range s.Entries {
		if e.Path == path {
			e.Weight += delta
			if e.Weight < 0 {
				e.Weight = 0
			}
			e.LastVisit = time.Now()
			s.age()
			return e.Weight, true
		}
	}
	return 0, false
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

// splitComponents splits path into its components on the OS path separators,
// filtering out empty strings. The caller is responsible for lowercasing.
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

// MergeStats describes what a MergeFrom call changed in the receiving store.
type MergeStats struct {
	Added          []string `json:"added"`
	Updated        []string `json:"updated"`
	Removed        []string `json:"removed"`
	AliasesAdded   []string `json:"aliases_added"`
	AliasesUpdated []string `json:"aliases_updated"`
	AliasesRemoved []string `json:"aliases_removed"`
}

// Changes returns the total number of changed records.
func (m MergeStats) Changes() int {
	return len(m.Added) + len(m.Updated) + len(m.Removed) +
		len(m.AliasesAdded) + len(m.AliasesUpdated) + len(m.AliasesRemoved)
}

// MergeFrom merges entries from another store into this one.
//
// Entries present on both sides: weight=max, visits=max, lastVisit=latest.
// Deletions travel as tombstones: a tombstone removes an entry unless that
// entry was visited after the deletion. Aliases defined differently on the two
// sides resolve to the most recently set one.
func (s *Store) MergeFrom(other *Store) MergeStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()
	s.ensureMaps()

	stats := MergeStats{
		Added: []string{}, Updated: []string{}, Removed: []string{},
		AliasesAdded: []string{}, AliasesUpdated: []string{}, AliasesRemoved: []string{},
	}

	idx := make(map[string]*Entry, len(s.Entries))
	for _, e := range s.Entries {
		idx[e.Path] = e
	}

	// 1. Remote tombstones.
	dropped := make(map[string]bool)
	for path, t := range other.Deleted {
		if le, ok := idx[path]; ok {
			if le.LastVisit.After(t) {
				continue // revisited locally after the remote deletion
			}
			dropped[path] = true
			delete(idx, path)
			stats.Removed = append(stats.Removed, path)
		}
		if t.After(s.Deleted[path]) {
			s.Deleted[path] = t
		}
	}
	if len(dropped) > 0 {
		filtered := s.Entries[:0]
		for _, e := range s.Entries {
			if !dropped[e.Path] {
				filtered = append(filtered, e)
			}
		}
		s.Entries = filtered
	}

	// 2. Remote entries.
	for _, re := range other.Entries {
		if t, ok := s.Deleted[re.Path]; ok {
			if !re.LastVisit.After(t) {
				continue // deleted here after the remote last saw it
			}
			delete(s.Deleted, re.Path)
		}
		le, ok := idx[re.Path]
		if !ok {
			cp := *re
			s.Entries = append(s.Entries, &cp)
			idx[cp.Path] = &cp
			stats.Added = append(stats.Added, cp.Path)
			continue
		}
		changed := false
		if re.Weight > le.Weight {
			le.Weight = re.Weight
			changed = true
		}
		if re.Visits > le.Visits {
			le.Visits = re.Visits
			changed = true
		}
		if re.LastVisit.After(le.LastVisit) {
			le.LastVisit = re.LastVisit
			changed = true
		}
		if re.Starred && !le.Starred {
			le.Starred = true
			changed = true
		}
		if le.Category == "" && re.Category != "" {
			le.Category = re.Category
			changed = true
		}
		if changed {
			stats.Updated = append(stats.Updated, le.Path)
		}
	}

	// 3. Remote alias tombstones.
	for name, t := range other.DeletedAliases {
		if _, ok := s.Aliases[name]; ok {
			if s.AliasTimes[name].After(t) {
				continue // re-created locally after the remote deletion
			}
			delete(s.Aliases, name)
			delete(s.AliasTimes, name)
			stats.AliasesRemoved = append(stats.AliasesRemoved, name)
		}
		if t.After(s.DeletedAliases[name]) {
			s.DeletedAliases[name] = t
		}
	}

	// 4. Remote aliases.
	for name, path := range other.Aliases {
		rt := other.AliasTimes[name]
		if t, ok := s.DeletedAliases[name]; ok {
			if !rt.After(t) {
				continue
			}
			delete(s.DeletedAliases, name)
		}
		lp, exists := s.Aliases[name]
		switch {
		case !exists:
			s.Aliases[name] = path
			stats.AliasesAdded = append(stats.AliasesAdded, name)
		case lp != path && rt.After(s.AliasTimes[name]):
			s.Aliases[name] = path
			stats.AliasesUpdated = append(stats.AliasesUpdated, name)
		default:
			if lp != path {
				continue // local definition is newer (or equally old): keep it
			}
		}
		if rt.After(s.AliasTimes[name]) {
			s.AliasTimes[name] = rt
		}
	}

	s.pruneTombstones()
	for _, l := range [][]string{stats.Added, stats.Updated, stats.Removed,
		stats.AliasesAdded, stats.AliasesUpdated, stats.AliasesRemoved} {
		sort.Strings(l)
	}
	return stats
}

func (s *Store) pruneTombstones() {
	cutoff := time.Now().Add(-tombstoneTTL)
	for k, t := range s.Deleted {
		if t.Before(cutoff) {
			delete(s.Deleted, k)
		}
	}
	for k, t := range s.DeletedAliases {
		if t.Before(cutoff) {
			delete(s.DeletedAliases, k)
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

var aliasNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidAliasName reports whether name (with or without the @ prefix) is an
// acceptable alias: letters, digits, '.', '_' and '-', starting with a letter
// or digit.
func ValidAliasName(name string) bool {
	return aliasNameRe.MatchString(normalizeAlias(name))
}

// SetAlias assigns an @alias to a directory path.
func (s *Store) SetAlias(name, path string) error {
	if !ValidAliasName(name) {
		return fmt.Errorf("invalid alias name %q (use letters, digits, '.', '_' or '-')", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureMaps()
	name = normalizeAlias(name)
	s.Aliases[name] = normalizePath(path)
	s.AliasTimes[name] = time.Now()
	delete(s.DeletedAliases, name)
	return nil
}

// RemoveAlias deletes an alias, leaving a tombstone for sync.
func (s *Store) RemoveAlias(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureMaps()
	name = normalizeAlias(name)
	if _, ok := s.Aliases[name]; !ok {
		return false
	}
	delete(s.Aliases, name)
	delete(s.AliasTimes, name)
	s.DeletedAliases[name] = time.Now()
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
