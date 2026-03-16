// Package expander: extglob.go implements extended globbing pattern matching.
//
// Extended glob operators (enabled via shopt -s extglob):
//
//	?(pattern-list)  matches zero or one of the patterns
//	*(pattern-list)  matches zero or more of the patterns
//	+(pattern-list)  matches one or more of the patterns
//	@(pattern-list)  matches exactly one of the patterns
//	!(pattern-list)  matches anything except the patterns
//
// Pattern-list entries are separated by |. Patterns may contain
// standard glob metacharacters (*, ?, [...]) and may nest.
//
// Implementation uses recursive backtracking rather than regex
// conversion, because Go's RE2 regex engine does not support
// lookaheads (needed for the !(pattern) operator).
package expander

import (
	"strings"
)

// HasExtglob returns true if the pattern contains extglob operators.
func HasExtglob(pattern string) bool {
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++ // skip escaped char
			continue
		}
		if i+1 < len(runes) && runes[i+1] == '(' && isExtglobPrefixRune(runes[i]) {
			return true
		}
	}
	return false
}

// ExtglobMatch performs extglob-aware pattern matching on the full
// string s. Glob wildcards (* and ?) match any character including /.
func ExtglobMatch(pattern, s string) bool {
	return matchR([]rune(pattern), []rune(s), 0, 0, false)
}

// ExtglobMatchPath performs extglob-aware pattern matching where
// glob wildcards (* and ?) do NOT match the / path separator.
func ExtglobMatchPath(pattern, s string) bool {
	return matchR([]rune(pattern), []rune(s), 0, 0, true)
}

// matchR is the core recursive backtracking matcher. It tests whether
// pat[pi:] matches s[si:]. pathMode controls whether * and ? cross /.
func matchR(pat, s []rune, pi, si int, pathMode bool) bool {
	for pi < len(pat) {
		ch := pat[pi]

		// Check for extglob operator: ?( *( +( @( !(
		if pi+1 < len(pat) && pat[pi+1] == '(' && isExtglobPrefixRune(ch) {
			inner, end := readExtglobGroupRunes(pat, pi+2)
			if end >= 0 {
				alts := splitExtglobAlts(inner)
				restPi := end + 1

				switch ch {
				case '@':
					return matchOneOf(alts, pat, s, si, restPi, pathMode)
				case '?':
					// Zero: skip group entirely.
					if matchR(pat, s, restPi, si, pathMode) {
						return true
					}
					// One: try each alternative.
					return matchOneOf(alts, pat, s, si, restPi, pathMode)
				case '+':
					return matchRepeated(alts, pat, s, si, restPi, pathMode, false)
				case '*':
					return matchRepeated(alts, pat, s, si, restPi, pathMode, true)
				case '!':
					return matchNegation(alts, pat, s, si, restPi, pathMode)
				}
			}
		}

		switch ch {
		case '*':
			pi++
			// Try matching * against 0..n characters.
			for j := si; j <= len(s); j++ {
				if pathMode && j > si && s[j-1] == '/' {
					break
				}
				if matchR(pat, s, pi, j, pathMode) {
					return true
				}
			}
			return false

		case '?':
			if si >= len(s) {
				return false
			}
			if pathMode && s[si] == '/' {
				return false
			}
			pi++
			si++

		case '[':
			if si >= len(s) {
				return false
			}
			matched, nextPi := matchCharClass(pat, pi, s[si])
			if !matched {
				return false
			}
			pi = nextPi
			si++

		case '\\':
			pi++
			if pi >= len(pat) {
				return false
			}
			if si >= len(s) || pat[pi] != s[si] {
				return false
			}
			pi++
			si++

		default:
			if si >= len(s) || ch != s[si] {
				return false
			}
			pi++
			si++
		}
	}
	return si == len(s)
}

// matchOneOf tries matching one of the alternatives at position si.
// For each alternative, finds all prefix lengths it can consume,
// then continues with the rest of the pattern.
func matchOneOf(alts [][]rune, pat, s []rune, si, restPi int, pathMode bool) bool {
	for _, alt := range alts {
		ends := altEndPositions(alt, s, si, pathMode)
		for _, e := range ends {
			if matchR(pat, s, restPi, e, pathMode) {
				return true
			}
		}
	}
	return false
}

// matchRepeated handles *(...) and +(...) by BFS over reachable positions.
func matchRepeated(alts [][]rune, pat, s []rune, si, restPi int, pathMode bool, allowZero bool) bool {
	// Track visited positions to avoid infinite loops.
	visited := make(map[int]bool)
	visited[si] = true
	queue := []int{si}

	if allowZero && matchR(pat, s, restPi, si, pathMode) {
		return true
	}

	for len(queue) > 0 {
		pos := queue[0]
		queue = queue[1:]

		for _, alt := range alts {
			ends := altEndPositions(alt, s, pos, pathMode)
			for _, e := range ends {
				if e <= pos || visited[e] {
					continue // must make progress
				}
				visited[e] = true
				queue = append(queue, e)
				if matchR(pat, s, restPi, e, pathMode) {
					return true
				}
			}
		}
	}
	return false
}

// matchNegation handles !(p1|p2): matches any substring at si that
// does NOT match any alternative, then continues with the rest.
func matchNegation(alts [][]rune, pat, s []rune, si, restPi int, pathMode bool) bool {
	// Try all possible lengths (including zero).
	for end := si; end <= len(s); end++ {
		// Check that s[si:end] doesn't match any alternative.
		anyMatch := false
		for _, alt := range alts {
			if matchR(alt, s[si:end], 0, 0, pathMode) {
				anyMatch = true
				break
			}
		}
		if !anyMatch {
			if matchR(pat, s, restPi, end, pathMode) {
				return true
			}
		}
	}
	return false
}

// altEndPositions returns all end positions where alt matches s[si:end].
func altEndPositions(alt, s []rune, si int, pathMode bool) []int {
	var result []int
	for end := si; end <= len(s); end++ {
		if matchR(alt, s[si:end], 0, 0, pathMode) {
			result = append(result, end)
		}
	}
	return result
}

// matchCharClass matches s[si] against a [...] character class at pat[pi].
// Returns (matched, nextPi) where nextPi is after the closing ].
func matchCharClass(pat []rune, pi int, ch rune) (bool, int) {
	if pi >= len(pat) || pat[pi] != '[' {
		return false, pi
	}
	pi++ // skip [

	negate := false
	if pi < len(pat) && (pat[pi] == '!' || pat[pi] == '^') {
		negate = true
		pi++
	}

	matched := false
	// First char after [ (or [! / [^) can be ] without closing the class.
	first := true
	for pi < len(pat) {
		if pat[pi] == ']' && !first {
			pi++ // skip ]
			if negate {
				return !matched, pi
			}
			return matched, pi
		}
		first = false

		// Check for range: a-z
		lo := pat[pi]
		pi++
		if pi+1 < len(pat) && pat[pi] == '-' && pat[pi+1] != ']' {
			pi++ // skip -
			hi := pat[pi]
			pi++
			if ch >= lo && ch <= hi {
				matched = true
			}
		} else {
			if ch == lo {
				matched = true
			}
		}
	}
	// Unterminated class — no match.
	return false, pi
}

// isExtglobPrefixRune returns true if ch can precede '(' in an extglob operator.
func isExtglobPrefixRune(ch rune) bool {
	return ch == '?' || ch == '*' || ch == '+' || ch == '@' || ch == '!'
}

// readExtglobGroupRunes reads from position start (after the opening '(')
// until the matching ')'. Returns the inner runes and the index of ')'.
// Returns nil, -1 if no matching ')' is found.
func readExtglobGroupRunes(runes []rune, start int) ([]rune, int) {
	depth := 1
	i := start
	for i < len(runes) {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return runes[start:i], i
			}
		case '\\':
			if i+1 < len(runes) {
				i++ // skip escaped char
			}
		}
		i++
	}
	return nil, -1
}

// splitExtglobAlts splits on unescaped '|' at depth 0 (outside nested parens).
func splitExtglobAlts(runes []rune) [][]rune {
	var result [][]rune
	var current []rune
	depth := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			current = append(current, runes[i], runes[i+1])
			i++
			continue
		}
		switch runes[i] {
		case '(':
			depth++
			current = append(current, runes[i])
		case ')':
			depth--
			current = append(current, runes[i])
		case '|':
			if depth == 0 {
				result = append(result, current)
				current = nil
				continue
			}
			current = append(current, runes[i])
		default:
			current = append(current, runes[i])
		}
	}
	result = append(result, current)
	return result
}

// broadenExtglob replaces each extglob group in a pattern with '*'
// so that filepath.Glob can find candidate matches.
func broadenExtglob(pattern string) string {
	var sb strings.Builder
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		if i+1 < len(runes) && runes[i+1] == '(' && isExtglobPrefixRune(runes[i]) {
			_, end := readExtglobGroupRunes(runes, i+2)
			if end >= 0 {
				sb.WriteRune('*')
				i = end
				continue
			}
		}
		sb.WriteRune(runes[i])
	}
	return sb.String()
}

// caseInsensitiveExtglobGlob performs case-insensitive globbing with
// extglob patterns. It broadens the pattern, applies case-insensitive
// globbing, then filters with case-insensitive extglob matching.
func caseInsensitiveExtglobGlob(pattern string) ([]string, error) {
	broadened := broadenExtglob(pattern)
	candidates, err := caseInsensitiveGlob(broadened)
	if err != nil {
		return nil, err
	}
	lowerPat := strings.ToLower(pattern)
	var result []string
	for _, c := range candidates {
		if ExtglobMatchPath(lowerPat, strings.ToLower(c)) {
			result = append(result, c)
		}
	}
	return result, nil
}

