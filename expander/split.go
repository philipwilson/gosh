package expander

import (
	"gosh/lexer"
	"strings"
)

// splitFieldsInArgs performs IFS word splitting on each arg word.
// Only parts marked Expanded (from unquoted variable/command/arithmetic
// expansion) are split. Literal and quoted parts are not split.
// A word that consists entirely of empty Expanded parts (with no
// literal or quoted text) is removed entirely.
func splitFieldsInArgs(args []lexer.Word, ifs string) []lexer.Word {
	var result []lexer.Word
	for _, w := range args {
		if !hasExpandedPart(w) {
			result = append(result, w)
			continue
		}
		result = append(result, splitWord(w, ifs)...)
	}
	return result
}

// hasExpandedPart returns true if a word contains any Expanded parts.
func hasExpandedPart(w lexer.Word) bool {
	for _, p := range w {
		if p.Quote == lexer.Expanded {
			return true
		}
	}
	return false
}

// splitWord splits a single word on IFS boundaries within Expanded parts.
// Non-Expanded parts (literal text, quoted text) are never split.
// Returns zero or more words.
func splitWord(w lexer.Word, ifs string) []lexer.Word {
	var words []lexer.Word
	var cur lexer.Word // parts accumulating for the current output word

	for _, part := range w {
		if part.Quote != lexer.Expanded {
			cur = append(cur, part)
			continue
		}

		fields, startsIFS, endsIFS := ifsSplit(part.Text, ifs)

		if len(fields) == 0 {
			// Empty expansion. If it starts/ends with IFS (e.g., all
			// whitespace), emit the current accumulator as a word.
			if (startsIFS || endsIFS) && len(cur) > 0 {
				words = append(words, cur)
				cur = nil
			}
			continue
		}

		// First field: if the expansion starts with IFS, split from
		// any preceding literal text before joining the first field.
		if startsIFS && len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
		if fields[0] != "" {
			cur = append(cur, lexer.WordPart{Text: fields[0], Quote: lexer.Unquoted})
		}

		// Middle fields (index 1..n-2): each becomes a separate word.
		for j := 1; j < len(fields)-1; j++ {
			if len(cur) > 0 {
				words = append(words, cur)
			}
			cur = lexer.Word{{Text: fields[j], Quote: lexer.Unquoted}}
		}

		// Last field (if more than one): emit current, start fresh.
		if len(fields) > 1 {
			if len(cur) > 0 {
				words = append(words, cur)
			}
			last := fields[len(fields)-1]
			if last != "" {
				cur = lexer.Word{{Text: last, Quote: lexer.Unquoted}}
			} else {
				cur = nil
			}
		}

		// If the expansion ends with IFS and there's following text,
		// emit the current accumulator so following parts start fresh.
		if endsIFS && len(cur) > 0 {
			words = append(words, cur)
			cur = nil
		}
	}

	if len(cur) > 0 {
		words = append(words, cur)
	}

	return words
}

// ifsSplit splits a string on IFS characters following POSIX rules.
// IFS whitespace (space, tab, newline present in ifs) is trimmed from
// the start/end and collapsed between fields. Non-whitespace IFS
// characters each act as an individual delimiter (producing empty
// fields between consecutive occurrences). Returns the fields and
// whether the string started/ended with IFS characters.
func ifsSplit(s, ifs string) (fields []string, startsIFS, endsIFS bool) {
	if s == "" {
		return nil, false, false
	}
	if ifs == "" {
		return []string{s}, false, false
	}

	runes := []rune(s)
	n := len(runes)

	isIFS := func(r rune) bool { return strings.ContainsRune(ifs, r) }
	isWhiteIFS := func(r rune) bool {
		return (r == ' ' || r == '\t' || r == '\n') && strings.ContainsRune(ifs, r)
	}

	startsIFS = isIFS(runes[0])
	endsIFS = isIFS(runes[n-1])

	i := 0

	// Skip leading IFS whitespace.
	for i < n && isWhiteIFS(runes[i]) {
		i++
	}

	// Leading non-whitespace IFS char → empty first field.
	if i < n && isIFS(runes[i]) && !isWhiteIFS(runes[i]) {
		fields = append(fields, "")
	}

	for i < n {
		// Collect non-IFS characters into a field.
		start := i
		for i < n && !isIFS(runes[i]) {
			i++
		}
		fields = append(fields, string(runes[start:i]))

		if i >= n {
			break
		}

		// Consume delimiter: optional whitespace, optional non-whitespace, optional whitespace.
		for i < n && isWhiteIFS(runes[i]) {
			i++
		}
		if i < n && isIFS(runes[i]) && !isWhiteIFS(runes[i]) {
			i++
			for i < n && isWhiteIFS(runes[i]) {
				i++
			}
			// Trailing non-whitespace delimiter at end → empty trailing field.
			if i >= n {
				fields = append(fields, "")
			}
		}
	}

	return fields, startsIFS, endsIFS
}
