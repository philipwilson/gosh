package expander

import (
	"fmt"
	"gosh/lexer"
	"strconv"
	"strings"
	"unicode"
)

// braceChar tags each rune with its QuoteContext so we know which
// braces/commas are unquoted and eligible for expansion.
type braceChar struct {
	ch    rune
	quote lexer.QuoteContext
}

// expandBracesInArgs expands braces in each arg word.
// One word may become many.
func expandBracesInArgs(args []lexer.Word) []lexer.Word {
	var result []lexer.Word
	for _, w := range args {
		result = append(result, expandBracesInWord(w)...)
	}
	return result
}

// expandBracesInWord expands a single word, recursively.
func expandBracesInWord(w lexer.Word) []lexer.Word {
	chars := wordToBraceChars(w)

	prefix, body, suffix, ok := findBraceExpr(chars)
	if !ok {
		return []lexer.Word{w}
	}

	// Try sequence first: {1..5}, {a..e}
	if seq := parseSequence(body); seq != nil {
		var result []lexer.Word
		for _, elem := range seq {
			combined := concatBraceChars(prefix, stringToBraceChars(elem, lexer.Unquoted), suffix)
			word := braceCharsToWord(combined)
			result = append(result, expandBracesInWord(word)...)
		}
		return result
	}

	// Comma list: {a,b,c}
	alts := splitBraceAlternatives(body)
	if alts == nil {
		// No commas and not a sequence — literal
		return []lexer.Word{w}
	}

	var result []lexer.Word
	for _, alt := range alts {
		combined := concatBraceChars(prefix, alt, suffix)
		word := braceCharsToWord(combined)
		result = append(result, expandBracesInWord(word)...)
	}
	return result
}

// findBraceExpr scans chars for the first valid unquoted brace expression.
// A valid expression is an unquoted '{' (not preceded by '$') matched by
// an unquoted '}', containing either an unquoted comma at depth 0 or a
// '..' sequence. Returns prefix, body (between braces), suffix, and ok.
func findBraceExpr(chars []braceChar) (prefix, body, suffix []braceChar, ok bool) {
	for i := 0; i < len(chars); i++ {
		if chars[i].ch != '{' || chars[i].quote != lexer.Unquoted {
			continue
		}
		// Skip ${...} — variable expansion, not brace expansion
		if i > 0 && chars[i-1].ch == '$' && chars[i-1].quote == lexer.Unquoted {
			continue
		}

		// Find matching '}'
		depth := 1
		hasComma := false
		hasDotDot := false
		j := i + 1
		for j < len(chars) && depth > 0 {
			if chars[j].ch == '{' && chars[j].quote == lexer.Unquoted {
				// Don't count ${ as nesting
				if j > 0 && chars[j-1].ch == '$' && chars[j-1].quote == lexer.Unquoted {
					j++
					continue
				}
				depth++
			} else if chars[j].ch == '}' && chars[j].quote == lexer.Unquoted {
				depth--
				if depth == 0 {
					break
				}
			} else if chars[j].ch == ',' && chars[j].quote == lexer.Unquoted && depth == 1 {
				hasComma = true
			}
			j++
		}

		if depth != 0 {
			continue // unmatched
		}

		bodyChars := chars[i+1 : j]

		// Check for '..' in the body
		if !hasComma {
			hasDotDot = containsDotDot(bodyChars)
		}

		// Must have a comma or '..' to be valid brace expansion
		if !hasComma && !hasDotDot {
			continue
		}

		// Single-element check: {a} has no comma → already caught above
		return chars[:i], bodyChars, chars[j+1:], true
	}
	return nil, nil, nil, false
}

// containsDotDot returns true if the chars contain an unquoted ".." sequence.
func containsDotDot(chars []braceChar) bool {
	for i := 0; i+1 < len(chars); i++ {
		if chars[i].ch == '.' && chars[i].quote == lexer.Unquoted &&
			chars[i+1].ch == '.' && chars[i+1].quote == lexer.Unquoted {
			return true
		}
	}
	return false
}

// splitBraceAlternatives splits body on unquoted top-level commas.
// Returns nil if there are no commas (not a valid comma brace expression).
func splitBraceAlternatives(body []braceChar) [][]braceChar {
	var alts [][]braceChar
	var cur []braceChar
	depth := 0
	hasComma := false

	for _, bc := range body {
		if bc.ch == '{' && bc.quote == lexer.Unquoted {
			depth++
			cur = append(cur, bc)
		} else if bc.ch == '}' && bc.quote == lexer.Unquoted {
			depth--
			cur = append(cur, bc)
		} else if bc.ch == ',' && bc.quote == lexer.Unquoted && depth == 0 {
			hasComma = true
			alts = append(alts, cur)
			cur = nil
		} else {
			cur = append(cur, bc)
		}
	}

	if !hasComma {
		return nil
	}

	alts = append(alts, cur)
	return alts
}

// parseSequence tries to parse body as a sequence expression (e.g., 1..5, a..e).
// Returns nil if not a valid sequence.
func parseSequence(body []braceChar) []string {
	// All chars must be unquoted for a sequence.
	var text strings.Builder
	for _, bc := range body {
		if bc.quote != lexer.Unquoted {
			return nil
		}
		text.WriteRune(bc.ch)
	}
	s := text.String()

	// Split on ".." — must have exactly one ".."
	idx := strings.Index(s, "..")
	if idx < 0 {
		return nil
	}
	left := s[:idx]
	right := s[idx+2:]

	// Ensure no second ".."
	if strings.Contains(right, "..") {
		return nil
	}

	if left == "" || right == "" {
		return nil
	}

	// Try integer sequence
	leftInt, leftErr := strconv.Atoi(left)
	rightInt, rightErr := strconv.Atoi(right)
	if leftErr == nil && rightErr == nil {
		width := maxPadWidth(left, right)
		return expandIntSequence(leftInt, rightInt, width)
	}

	// Try letter sequence (single ASCII letters)
	if len(left) == 1 && len(right) == 1 {
		l, r := rune(left[0]), rune(right[0])
		if unicode.IsLetter(l) && unicode.IsLetter(r) &&
			l < 128 && r < 128 {
			return expandLetterSequence(l, r)
		}
	}

	return nil
}

// maxPadWidth returns the zero-padding width needed for sequence output.
// If either endpoint has leading zeros, the output is zero-padded to the
// maximum width of the two endpoints.
func maxPadWidth(left, right string) int {
	hasLeadingZero := func(s string) bool {
		if strings.HasPrefix(s, "-") {
			s = s[1:]
		}
		return len(s) > 1 && s[0] == '0'
	}
	if !hasLeadingZero(left) && !hasLeadingZero(right) {
		return 0
	}
	w := len(left)
	if len(right) > w {
		w = len(right)
	}
	return w
}

// expandIntSequence generates integers from start to end (inclusive),
// with optional zero-padding.
func expandIntSequence(start, end, width int) []string {
	step := 1
	if start > end {
		step = -1
	}

	n := abs(end-start) + 1
	result := make([]string, 0, n)

	for i := start; ; i += step {
		s := strconv.Itoa(i)
		if width > 0 {
			if i < 0 {
				s = fmt.Sprintf("-%0*d", width-1, -i)
			} else {
				s = fmt.Sprintf("%0*d", width, i)
			}
		}
		result = append(result, s)
		if i == end {
			break
		}
	}
	return result
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// expandLetterSequence generates letters from start to end (inclusive).
func expandLetterSequence(start, end rune) []string {
	step := rune(1)
	if start > end {
		step = -1
	}

	n := abs(int(end-start)) + 1
	result := make([]string, 0, n)

	for ch := start; ; ch += step {
		result = append(result, string(ch))
		if ch == end {
			break
		}
	}
	return result
}

// wordToBraceChars flattens a Word into []braceChar, tagging each rune
// with its QuoteContext.
func wordToBraceChars(w lexer.Word) []braceChar {
	var chars []braceChar
	for _, part := range w {
		for _, ch := range part.Text {
			chars = append(chars, braceChar{ch: ch, quote: part.Quote})
		}
	}
	return chars
}

// braceCharsToWord groups consecutive chars with the same QuoteContext
// back into WordParts.
func braceCharsToWord(chars []braceChar) lexer.Word {
	if len(chars) == 0 {
		return nil
	}

	var parts lexer.Word
	var buf strings.Builder
	curQuote := chars[0].quote

	for _, bc := range chars {
		if bc.quote != curQuote {
			if buf.Len() > 0 {
				parts = append(parts, lexer.WordPart{Text: buf.String(), Quote: curQuote})
				buf.Reset()
			}
			curQuote = bc.quote
		}
		buf.WriteRune(bc.ch)
	}

	if buf.Len() > 0 {
		parts = append(parts, lexer.WordPart{Text: buf.String(), Quote: curQuote})
	}

	return parts
}

// stringToBraceChars converts a plain string to braceChars with the given quote.
func stringToBraceChars(s string, quote lexer.QuoteContext) []braceChar {
	var chars []braceChar
	for _, ch := range s {
		chars = append(chars, braceChar{ch: ch, quote: quote})
	}
	return chars
}

// concatBraceChars concatenates multiple braceChar slices.
func concatBraceChars(parts ...[]braceChar) []braceChar {
	var result []braceChar
	for _, p := range parts {
		result = append(result, p...)
	}
	return result
}
