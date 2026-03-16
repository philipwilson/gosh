package expander

import (
	"path/filepath"
	"strconv"
	"strings"
)

// patternMatch is like filepath.Match but allows * to match /
// (which filepath.Match treats as a path separator). Parameter
// expansion patterns are string patterns, not path patterns.
func patternMatch(pattern, s string) bool {
	// Replace / with a placeholder that * will match.
	const ph = "\x00"
	p := strings.ReplaceAll(pattern, "/", ph)
	s = strings.ReplaceAll(s, "/", ph)
	matched, _ := filepath.Match(p, s)
	return matched
}

// removePrefix removes the shortest (or longest) prefix matching
// the glob pattern from value.
func removePrefix(value, pattern string, longest bool) string {
	runes := []rune(value)
	if longest {
		for i := len(runes); i >= 0; i-- {
			if patternMatch(pattern, string(runes[:i])) {
				return string(runes[i:])
			}
		}
	} else {
		for i := 0; i <= len(runes); i++ {
			if patternMatch(pattern, string(runes[:i])) {
				return string(runes[i:])
			}
		}
	}
	return value
}

// removeSuffix removes the shortest (or longest) suffix matching
// the glob pattern from value.
func removeSuffix(value, pattern string, longest bool) string {
	runes := []rune(value)
	if longest {
		for i := 0; i <= len(runes); i++ {
			if patternMatch(pattern, string(runes[i:])) {
				return string(runes[:i])
			}
		}
	} else {
		for i := len(runes); i >= 0; i-- {
			if patternMatch(pattern, string(runes[i:])) {
				return string(runes[:i])
			}
		}
	}
	return value
}

// splitSlashPattern splits word on the first unescaped "/" into
// pattern and replacement. If there is no "/", replacement is "".
func splitSlashPattern(word string) (pat, rep string) {
	runes := []rune(word)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			i++ // skip escaped char
			continue
		}
		if runes[i] == '/' {
			return string(runes[:i]), string(runes[i+1:])
		}
	}
	return word, ""
}

// substitutePattern replaces the first (or all) occurrences of a glob
// pattern in value with replacement. Matches are found by testing
// substrings at each position for the shortest match.
func substitutePattern(value, pattern, replacement string, all bool) string {
	if pattern == "" {
		return value
	}
	runes := []rune(value)
	var result strings.Builder
	i := 0
	for i < len(runes) {
		// Try longest match first (bash behavior).
		matchEnd := -1
		for end := len(runes); end > i; end-- {
			if patternMatch(pattern, string(runes[i:end])) {
				matchEnd = end
				break
			}
		}
		if matchEnd >= 0 {
			result.WriteString(replacement)
			i = matchEnd
			if !all {
				result.WriteString(string(runes[i:]))
				return result.String()
			}
		} else {
			result.WriteRune(runes[i])
			i++
		}
	}
	return result.String()
}

// substringExtract implements ${var:offset} and ${var:offset:length}.
// Supports negative offset (from end, requires leading space in shell)
// and negative length (stop before end).
func substringExtract(value, spec string) string {
	runes := []rune(value)
	n := len(runes)

	// Split spec on ":" to get offset and optional length.
	parts := strings.SplitN(spec, ":", 2)
	offsetStr := strings.TrimSpace(parts[0])

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		return value
	}

	// Negative offset counts from end.
	if offset < 0 {
		offset = n + offset
		if offset < 0 {
			offset = 0
		}
	}
	if offset > n {
		return ""
	}

	if len(parts) == 1 {
		return string(runes[offset:])
	}

	lengthStr := strings.TrimSpace(parts[1])
	length, err := strconv.Atoi(lengthStr)
	if err != nil {
		return value
	}

	if length < 0 {
		// Negative length means stop before end.
		end := n + length
		if end <= offset {
			return ""
		}
		return string(runes[offset:end])
	}

	end := offset + length
	if end > n {
		end = n
	}
	return string(runes[offset:end])
}
