package matcher

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bytenote/jmp/internal/store"
)

// Result wraps an entry with its computed score for display.
type Result struct {
	Entry *store.Entry
	Score float64
}

// Best returns the single best match, or nil if no matches.
func Best(s *store.Store, terms []string) *store.Entry {
	results := Rank(s, terms)
	if len(results) == 0 {
		return nil
	}
	return results[0].Entry
}

// Rank returns all matching entries, sorted best-first.
// Scoring strategy (layered):
//  1. Frecency base score
//  2. MatchType multiplier: substring=1.0, fuzzy=0.6
//  3. Bonus: last path component exact match ×3
//  4. Bonus: last path component prefix match ×1.5
//  5. Bonus: starred entries ×1.25, alias match ×2, current repo proximity ×1.2
//  6. Penalty: path doesn't exist on disk ×0.1
func Rank(s *store.Store, terms []string) []Result {
	queryResults := s.Query(terms)
	results := make([]Result, 0, len(queryResults))

	lastTerm := ""
	if len(terms) > 0 {
		lastTerm = strings.ToLower(terms[len(terms)-1])
	}

	for _, qr := range queryResults {
		e := qr.Entry
		score := e.Frecency()

		// Fuzzy matches are deprioritised so substring results always win.
		if qr.MatchType == store.MatchFuzzy {
			score *= 0.6
		}

		base := strings.ToLower(filepath.Base(e.Path))
		if lastTerm != "" && base == lastTerm {
			score *= 3.0
		} else if lastTerm != "" && strings.HasPrefix(base, lastTerm) {
			score *= 1.5
		}

		if e.Starred {
			score *= 1.25
		}
		for _, alias := range s.AliasesForPath(e.Path) {
			if aliasMatches(alias, terms) {
				score *= 2.0
				break
			}
		}
		if relatedToCurrentDir(e.Path) {
			score *= 1.2
		}

		// Deprioritize paths that no longer exist.
		if _, err := os.Stat(e.Path); os.IsNotExist(err) {
			score *= 0.1
		}

		results = append(results, Result{Entry: e, Score: score})
	}

	sortResults(results)
	return results
}

// Suggest returns approximate matches for failed queries.
func Suggest(s *store.Store, terms []string, limit int) []Result {
	query := strings.ToLower(strings.Join(terms, " "))
	if query == "" || limit <= 0 {
		return nil
	}
	results := make([]Result, 0)
	for _, e := range s.All() {
		score := similarityScore(query, strings.ToLower(e.Path))
		base := strings.ToLower(filepath.Base(e.Path))
		components := strings.FieldsFunc(strings.ToLower(e.Path), func(r rune) bool {
			return r == '/' || r == '\\' || r == '-' || r == '_' || r == '.'
		})
		if strings.Contains(base, query) {
			score += 5
		}
		for _, term := range terms {
			term = strings.ToLower(term)
			if strings.Contains(base, term) {
				score += 2
			}
			for _, component := range components {
				if fuzzyContains(component, term) {
					score += len(term)
					break
				}
			}
		}
		for _, alias := range s.AliasesForPath(e.Path) {
			alias = strings.TrimPrefix(strings.ToLower(alias), "@")
			for _, term := range terms {
				term = strings.TrimPrefix(strings.ToLower(term), "@")
				if strings.Contains(alias, term) {
					score += len(term) * 3
					continue
				}
				if fuzzyContains(alias, term) {
					score += len(term) * 2
				}
			}
		}
		if score <= 0 {
			continue
		}
		results = append(results, Result{Entry: e, Score: float64(score) + e.Frecency()/100})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

func sortResults(r []Result) {
	for i := 1; i < len(r); i++ {
		for j := i; j > 0 && r[j].Score > r[j-1].Score; j-- {
			r[j], r[j-1] = r[j-1], r[j]
		}
	}
}

func aliasMatches(alias string, terms []string) bool {
	alias = strings.TrimPrefix(strings.ToLower(alias), "@")
	for _, term := range terms {
		term = strings.TrimPrefix(strings.ToLower(term), "@")
		if term != "" && strings.Contains(alias, term) {
			return true
		}
	}
	return false
}

func relatedToCurrentDir(path string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	cwd = filepath.Clean(cwd)
	path = filepath.Clean(path)
	return strings.HasPrefix(path, cwd+string(os.PathSeparator)) || strings.HasPrefix(cwd, path+string(os.PathSeparator))
}

func similarityScore(query, target string) int {
	score := 0
	for _, term := range strings.Fields(query) {
		if strings.Contains(target, term) {
			score += len(term) * 2
			continue
		}
		if fuzzyContains(target, term) {
			score += len(term)
		}
	}
	return score
}

func fuzzyContains(target, pattern string) bool {
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
