package expander

import (
	"fmt"
	"gosh/lexer"
	"os"
	"strconv"
	"strings"
	"unicode"
)

// activeSetVar holds the current SetFunc during Expand, used by expandParam
// for ${var:=word} assignment. Set by Expand, cleared on return.
var activeSetVar SetFunc

// expandVarsInWord expands $VAR references in a word, respecting quoting.
// For Unquoted parts, expansion results are marked Expanded (subject to
// word splitting). For DoubleQuoted parts, results keep DoubleQuoted context.
// isSet may be nil (nounset checking disabled).
func expandVarsInWord(w lexer.Word, lookup LookupFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) lexer.Word {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	var result lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			result = append(result, part)
			continue
		}
		if part.Quote == lexer.Unquoted {
			// Produce structured parts: literal text stays Unquoted,
			// expansion results are marked Expanded for word splitting.
			result = append(result, expandDollarParts(part.Text, lookup, isSet, assocFn)...)
		} else {
			// DoubleQuoted (or Expanded from prior phase) — expand
			// variables but keep the quoting context.
			expanded := expandDollar(part.Text, lookup, isSet, assocFn)
			result = append(result, lexer.WordPart{
				Text:  expanded,
				Quote: part.Quote,
			})
		}
	}

	return result
}

// expandVarsInWordMulti is like expandVarsInWord but can return multiple
// words when a DoubleQuoted part contains "${arr[@]}" or "$@". Each array
// element becomes a separate word, with surrounding text attached to the
// first and last elements.
func expandVarsInWordMulti(w lexer.Word, lookup LookupFunc, lookupArray LookupArrayFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) []lexer.Word {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	if lookupArray == nil {
		return []lexer.Word{expandVarsInWord(w, lookup, isSet, assocFn)}
	}

	// Check if any DoubleQuoted part contains an array-@ expansion.
	hasArrayAt := false
	for _, part := range w {
		if part.Quote == lexer.DoubleQuoted && containsArrayAt(part.Text) {
			hasArrayAt = true
			break
		}
	}
	if !hasArrayAt {
		return []lexer.Word{expandVarsInWord(w, lookup, isSet, assocFn)}
	}

	// Process parts, splitting on array-@ expansions in DoubleQuoted context.
	// We build words by accumulating parts until we hit a "${arr[@]}" or "$@",
	// then split into multiple words.
	var result []lexer.Word
	var cur lexer.Word

	for _, part := range w {
		if part.Quote == lexer.SingleQuoted {
			cur = append(cur, part)
			continue
		}
		if part.Quote == lexer.Unquoted {
			cur = append(cur, expandDollarParts(part.Text, lookup, isSet, assocFn)...)
			continue
		}
		if part.Quote != lexer.DoubleQuoted || !containsArrayAt(part.Text) {
			expanded := expandDollar(part.Text, lookup, isSet, assocFn)
			cur = append(cur, lexer.WordPart{Text: expanded, Quote: part.Quote})
			continue
		}

		// DoubleQuoted part with ${arr[@]} or $@ — split into elements.
		elements := expandDollarMulti(part.Text, lookup, lookupArray, isSet, assocFn)
		if len(elements) == 0 {
			// Empty array — the word should be removed entirely
			// (unless there are other non-empty parts).
			continue
		}

		// First element attaches to accumulated prefix.
		cur = append(cur, lexer.WordPart{Text: elements[0], Quote: lexer.DoubleQuoted})

		if len(elements) == 1 {
			continue
		}

		// Emit prefix + first element as a word.
		result = append(result, cur)

		// Middle elements each become their own word.
		for _, elem := range elements[1 : len(elements)-1] {
			result = append(result, lexer.Word{{Text: elem, Quote: lexer.DoubleQuoted}})
		}

		// Last element starts a new accumulator for suffix.
		cur = lexer.Word{{Text: elements[len(elements)-1], Quote: lexer.DoubleQuoted}}
	}

	if len(cur) > 0 {
		result = append(result, cur)
	}

	// If result is empty (empty array with no other text), return nothing.
	if len(result) == 0 {
		return nil
	}

	return result
}

// containsArrayAt returns true if text contains $@ or ${...[@]}.
func containsArrayAt(text string) bool {
	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '$' {
			continue
		}
		i++
		if i >= len(runes) {
			break
		}
		if runes[i] == '@' {
			return true
		}
		if runes[i] == '{' {
			// Look for [@] before }.
			j := i + 1
			for j < len(runes) && runes[j] != '}' {
				j++
			}
			if j < len(runes) {
				content := string(runes[i+1 : j])
				// Match arr[@] and !arr[@] (key enumeration).
				if strings.HasSuffix(content, "[@]") {
					return true
				}
			}
		}
	}
	return false
}

// expandDollarMulti expands a DoubleQuoted text that may contain $@ or
// ${arr[@]}, returning separate elements for array-@ expansions. Non-array
// parts are concatenated into adjacent elements.
func expandDollarMulti(text string, lookup LookupFunc, lookupArray LookupArrayFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) []string {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	runes := []rune(text)
	var elements []string
	var buf strings.Builder
	hadEmptyArray := false
	i := 0

	for i < len(runes) {
		if runes[i] != '$' {
			buf.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $
		if i >= len(runes) {
			buf.WriteRune('$')
			break
		}

		// Check for $@ (positional params array).
		if runes[i] == '@' {
			i++
			elems, ok := lookupArray("@")
			if ok && len(elems) == 0 {
				hadEmptyArray = true
				continue
			}
			if ok && len(elems) > 0 {
				// First element merges with prefix.
				buf.WriteString(elems[0])
				elements = appendBuf(&buf, elements)
				// Middle elements.
				for _, e := range elems[1 : len(elems)-1] {
					elements = append(elements, e)
				}
				// Last element starts next buffer.
				if len(elems) > 1 {
					buf.WriteString(elems[len(elems)-1])
				}
			} else if !ok {
				buf.WriteString(lookup("@"))
			}
			continue
		}

		// Check for ${...[@]}.
		if runes[i] == '{' {
			start := i + 1
			j := start
			for j < len(runes) && runes[j] != '}' {
				j++
			}
			if j < len(runes) {
				content := string(runes[start:j])
				i = j + 1

				// Check for !name[@] pattern (key enumeration).
				if strings.HasPrefix(content, "!") && strings.HasSuffix(content, "[@]") {
					arrName := content[1 : len(content)-3]
					elems, ok := lookupArray("!" + arrName + "[@]")
					if ok && len(elems) > 0 {
						buf.WriteString(elems[0])
						elements = appendBuf(&buf, elements)
						for _, e := range elems[1 : len(elems)-1] {
							elements = append(elements, e)
						}
						if len(elems) > 1 {
							buf.WriteString(elems[len(elems)-1])
						}
					} else if ok {
						hadEmptyArray = true
					}
					continue
				}

				// Check for array[@] pattern.
				if idx := strings.Index(content, "[@]"); idx >= 0 && idx+3 == len(content) {
					arrName := content[:idx]
					elems, ok := lookupArray(arrName + "[@]")
					if ok && len(elems) > 0 {
						buf.WriteString(elems[0])
						elements = appendBuf(&buf, elements)
						for _, e := range elems[1 : len(elems)-1] {
							elements = append(elements, e)
						}
						if len(elems) > 1 {
							buf.WriteString(elems[len(elems)-1])
						}
					} else if !ok {
						buf.WriteString(expandParam(content, lookup, isSet, assocFn))
					} else {
						// ok && len(elems)==0: empty array
						hadEmptyArray = true
					}
					continue
				}

				buf.WriteString(expandParam(content, lookup, isSet, assocFn))
				continue
			}
			// Unclosed brace.
			buf.WriteString("${")
			buf.WriteString(string(runes[start:]))
			break
		}

		// Regular $VAR expansion.
		switch {
		case runes[i] == '?':
			buf.WriteString(lookup("?"))
			i++
		case runes[i] == '$':
			buf.WriteString(lookup("$"))
			i++
		case runes[i] == '!':
			buf.WriteString(lookup("!"))
			i++
		case runes[i] == '#':
			buf.WriteString(lookup("#"))
			i++
		case runes[i] == '*':
			buf.WriteString(lookup("*"))
			i++
		case runes[i] >= '0' && runes[i] <= '9':
			buf.WriteString(lookup(string(runes[i])))
			i++
		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			buf.WriteString(lookup(string(runes[start:i])))
		default:
			buf.WriteRune('$')
		}
	}

	// Flush remaining buffer.
	if buf.Len() > 0 {
		elements = append(elements, buf.String())
	} else if len(elements) == 0 && !hadEmptyArray {
		elements = append(elements, "")
	}
	return elements
}

// appendBuf flushes a string builder as an element and returns the updated slice.
func appendBuf(buf *strings.Builder, elements []string) []string {
	elements = append(elements, buf.String())
	buf.Reset()
	return elements
}

// expandDollarParts is like expandDollar but returns structured parts:
// literal text is marked Unquoted, expansion results are marked Expanded.
// This preserves the boundary between literal and expanded text so that
// word splitting can act only on expanded portions.
func expandDollarParts(text string, lookup LookupFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) []lexer.WordPart {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	if !strings.ContainsRune(text, '$') {
		return []lexer.WordPart{{Text: text, Quote: lexer.Unquoted}}
	}

	runes := []rune(text)
	var parts []lexer.WordPart
	var literal strings.Builder

	flushLiteral := func() {
		if literal.Len() > 0 {
			parts = append(parts, lexer.WordPart{Text: literal.String(), Quote: lexer.Unquoted})
			literal.Reset()
		}
	}

	i := 0
	for i < len(runes) {
		if runes[i] != '$' {
			literal.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $

		if i >= len(runes) {
			literal.WriteRune('$')
			break
		}

		var varName string
		consumed := true
		switch {
		case runes[i] == '{':
			i++ // skip {
			start := i
			for i < len(runes) && runes[i] != '}' {
				i++
			}
			if i >= len(runes) {
				literal.WriteString("${")
				literal.WriteString(string(runes[start:]))
				consumed = false
			} else {
				content := string(runes[start:i])
				i++ // skip }
				flushLiteral()
				parts = append(parts, lexer.WordPart{Text: expandParam(content, lookup, isSet, assocFn), Quote: lexer.Expanded})
				consumed = false // already handled
			}
		case runes[i] == '?':
			varName = "?"
			i++
		case runes[i] == '$':
			varName = "$"
			i++
		case runes[i] == '!':
			varName = "!"
			i++
		case runes[i] == '#':
			varName = "#"
			i++
		case runes[i] == '@' || runes[i] == '*':
			varName = string(runes[i])
			i++
		case runes[i] >= '0' && runes[i] <= '9':
			varName = string(runes[i])
			i++
		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			varName = string(runes[start:i])
		default:
			literal.WriteRune('$')
			consumed = false
		}

		if consumed {
			flushLiteral()
			parts = append(parts, lexer.WordPart{Text: lookup(varName), Quote: lexer.Expanded})
		}
	}

	flushLiteral()
	return parts
}

// expandParam handles ${...} parameter expansion. It supports:
//
//	${var}           simple lookup
//	${#var}          string length
//	${var:-word}     default value (if unset or empty)
//	${var-word}      default value (if unset)
//	${var:+word}     alternative value (if set and non-empty)
//	${var+word}      alternative value (if set)
//	${var:=word}     assign default (if unset or empty) — limited: no SetFunc
//	${var=word}      assign default (if unset) — limited: no SetFunc
//	${var:?word}     error if unset or empty
//	${var?word}      error if unset
//	${var#pattern}   remove shortest prefix match
//	${var##pattern}  remove longest prefix match
//	${var%pattern}   remove shortest suffix match
//	${var%%pattern}  remove longest suffix match
func expandParam(content string, lookup LookupFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) string {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	// ${#var} — string length / array length.
	if len(content) > 1 && content[0] == '#' {
		rest := content[1:]
		if isValidVarRef(rest) {
			// For ${#arr[@]} / ${#arr[*]}, use #arr[@] convention
			// to get element count from lookup.
			if idx := strings.IndexByte(rest, '['); idx >= 0 {
				sub := rest[idx+1:]
				if strings.HasSuffix(sub, "]") {
					sub = sub[:len(sub)-1]
				}
				if sub == "@" || sub == "*" {
					return lookup("#" + rest)
				}
			}
			return strconv.Itoa(len([]rune(lookup(rest))))
		}
	}

	// ${!var} — indirect expansion or key enumeration.
	if len(content) > 1 && content[0] == '!' {
		rest := content[1:]
		if isValidVarRef(rest) {
			// ${!arr[@]} / ${!arr[*]} — key enumeration.
			if strings.HasSuffix(rest, "[@]") || strings.HasSuffix(rest, "[*]") {
				return lookup("!" + rest)
			}
			intermediate := lookup(rest)
			if intermediate == "" {
				return ""
			}
			return lookup(intermediate)
		}
	}

	name, op, word := parseParamOp(content)

	// Evaluate arithmetic subscripts in array references.
	name = evalArraySubscript(name, lookup, assocFn)

	if op == "" {
		return lookup(name)
	}

	// For operators that provide defaults/alternatives, check isSet first
	// to avoid triggering nounset errors when a default is available.
	switch op {
	case ":-", "-", ":+", "+", ":=", "=", ":?", "?":
		var varSet bool
		var value string
		if isSet != nil {
			// Use isSet to avoid triggering nounset for unset vars.
			varSet = isSet(name)
			if varSet {
				value = lookup(name)
			}
		} else {
			// No isSet callback — call lookup directly (no nounset protection).
			// Treat non-empty value as "set" (matches old behavior).
			value = lookup(name)
			varSet = true // assume set; colon variants still check value==""
		}
		// Expand variables in the word part.
		word = expandDollar(word, lookup, isSet, assocFn)

		switch op {
		case ":-":
			// Use default if unset or empty.
			if !varSet || value == "" {
				return word
			}
			return value
		case "-":
			// Use default if unset.
			if !varSet {
				return word
			}
			return value
		case ":+":
			// Use alternative if set and non-empty.
			if varSet && value != "" {
				return word
			}
			return ""
		case "+":
			// Use alternative if set.
			if varSet {
				return word
			}
			return ""
		case ":=":
			if !varSet || value == "" {
				if activeSetVar != nil {
					activeSetVar(name, word)
				}
				return word
			}
			return value
		case "=":
			if !varSet {
				if activeSetVar != nil {
					activeSetVar(name, word)
				}
				return word
			}
			return value
		case ":?":
			if !varSet || value == "" {
				msg := word
				if msg == "" {
					msg = "parameter null or not set"
				}
				fmt.Fprintf(os.Stderr, "gosh: %s: %s\n", name, msg)
				return ""
			}
			return value
		case "?":
			if !varSet {
				msg := word
				if msg == "" {
					msg = "parameter null or not set"
				}
				fmt.Fprintf(os.Stderr, "gosh: %s: %s\n", name, msg)
				return ""
			}
			return value
		}
	}

	value := lookup(name)
	// Expand variables in the word part.
	word = expandDollar(word, lookup, isSet, assocFn)

	switch op {
	case "#":
		return removePrefix(value, word, false)
	case "##":
		return removePrefix(value, word, true)
	case "%":
		return removeSuffix(value, word, false)
	case "%%":
		return removeSuffix(value, word, true)
	case "/":
		pat, rep := splitSlashPattern(word)
		return substitutePattern(value, pat, rep, false)
	case "//":
		pat, rep := splitSlashPattern(word)
		return substitutePattern(value, pat, rep, true)
	case ":":
		return substringExtract(value, word)
	case "^":
		if len(value) == 0 {
			return ""
		}
		r := []rune(value)
		if word == "" || patternMatch(word, string(r[0])) {
			r[0] = unicode.ToUpper(r[0])
		}
		return string(r)
	case "^^":
		if word == "" {
			return strings.ToUpper(value)
		}
		r := []rune(value)
		for i, ch := range r {
			if patternMatch(word, string(ch)) {
				r[i] = unicode.ToUpper(ch)
			}
		}
		return string(r)
	case ",":
		if len(value) == 0 {
			return ""
		}
		r := []rune(value)
		if word == "" || patternMatch(word, string(r[0])) {
			r[0] = unicode.ToLower(r[0])
		}
		return string(r)
	case ",,":
		if word == "" {
			return strings.ToLower(value)
		}
		r := []rune(value)
		for i, ch := range r {
			if patternMatch(word, string(ch)) {
				r[i] = unicode.ToLower(ch)
			}
		}
		return string(r)
	}

	return value
}

// parseParamOp extracts the variable name, operator, and word from
// the content between ${ and }. Returns (name, op, word) where op
// is "" for a simple ${var} lookup. Array subscripts like arr[0] or
// arr[@] are included in the name.
func parseParamOp(content string) (name, op, word string) {
	runes := []rune(content)
	i := 0

	// Special single-character variable names.
	if len(runes) > 0 {
		switch runes[0] {
		case '?', '$', '@', '*':
			i = 1
		default:
			if runes[0] >= '0' && runes[0] <= '9' {
				i = 1
			} else if isNameStart(runes[0]) {
				for i < len(runes) && isNameCont(runes[i]) {
					i++
				}
			} else {
				return content, "", ""
			}
		}
	}

	// Consume array subscript [expr] if present.
	if i < len(runes) && runes[i] == '[' {
		depth := 1
		i++ // skip [
		for i < len(runes) && depth > 0 {
			if runes[i] == '[' {
				depth++
			} else if runes[i] == ']' {
				depth--
			}
			i++
		}
	}

	name = string(runes[:i])
	rest := string(runes[i:])

	// Check for two-character operators first, then single-character.
	for _, candidate := range []string{"%%", "##", "//", ":-", ":+", ":=", ":?", "^^", ",,"} {
		if strings.HasPrefix(rest, candidate) {
			return name, candidate, rest[len(candidate):]
		}
	}
	for _, candidate := range []string{"%", "#", "/", "-", "+", "=", "?", "^", ","} {
		if strings.HasPrefix(rest, candidate) {
			return name, candidate, rest[len(candidate):]
		}
	}

	// Substring extraction: ${var:offset} or ${var:offset:length}
	if strings.HasPrefix(rest, ":") {
		return name, ":", rest[1:]
	}

	// No operator — could be a simple name or unrecognized content.
	if rest == "" {
		return name, "", ""
	}
	return content, "", ""
}

// isValidVarName returns true if s is a valid variable name
// (used to distinguish ${#var} from ${#} with an operator).
func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	runes := []rune(s)
	if len(runes) == 1 {
		switch runes[0] {
		case '?', '$', '#', '@', '*':
			return true
		}
		if runes[0] >= '0' && runes[0] <= '9' {
			return true
		}
	}
	if !isNameStart(runes[0]) {
		return false
	}
	for _, r := range runes[1:] {
		if !isNameCont(r) {
			return false
		}
	}
	return true
}

// isValidVarRef returns true if s is a valid variable name or an
// array reference like arr[0] or arr[@].
func isValidVarRef(s string) bool {
	if isValidVarName(s) {
		return true
	}
	// Check for arr[subscript] pattern.
	idx := strings.IndexByte(s, '[')
	if idx <= 0 || !strings.HasSuffix(s, "]") {
		return false
	}
	return isValidVarName(s[:idx])
}

// evalArraySubscript evaluates arithmetic in array subscripts.
// For "arr[expr]", it expands $vars in expr and evaluates it as arithmetic.
// For "arr[@]" and "arr[*]", the subscript is left as-is.
// For non-array names, returns the name unchanged.
func evalArraySubscript(name string, lookup LookupFunc, isAssoc ...IsAssocFunc) string {
	idx := strings.IndexByte(name, '[')
	if idx < 0 || !strings.HasSuffix(name, "]") {
		return name
	}
	arrName := name[:idx]
	subscript := name[idx+1 : len(name)-1]
	if subscript == "@" || subscript == "*" {
		return name
	}
	// Expand $vars in the subscript.
	subscript = expandDollar(subscript, lookup, nil)
	// Associative arrays: use expanded subscript as string key (no arithmetic).
	if len(isAssoc) > 0 && isAssoc[0] != nil && isAssoc[0](arrName) {
		return arrName + "[" + subscript + "]"
	}
	// Evaluate as arithmetic for indexed arrays.
	val, err := EvalArith(subscript, lookup, nil)
	if err != nil {
		return name
	}
	return arrName + "[" + strconv.FormatInt(val, 10) + "]"
}

// expandDollar scans text for $VAR, ${VAR}, $?, $$ and replaces
// them with values from the lookup function.
func expandDollar(text string, lookup LookupFunc, isSet IsSetFunc, isAssoc ...IsAssocFunc) string {
	var assocFn IsAssocFunc
	if len(isAssoc) > 0 {
		assocFn = isAssoc[0]
	}
	if !strings.ContainsRune(text, '$') {
		return text
	}

	runes := []rune(text)
	var result strings.Builder
	result.Grow(len(text))

	i := 0
	for i < len(runes) {
		if runes[i] != '$' {
			result.WriteRune(runes[i])
			i++
			continue
		}

		i++ // skip $

		if i >= len(runes) {
			result.WriteRune('$')
			break
		}

		switch {
		case runes[i] == '{':
			i++ // skip {
			start := i
			for i < len(runes) && runes[i] != '}' {
				i++
			}
			if i >= len(runes) {
				result.WriteString("${")
				result.WriteString(string(runes[start:]))
				break
			}
			content := string(runes[start:i])
			i++ // skip }
			result.WriteString(expandParam(content, lookup, isSet, assocFn))

		case runes[i] == '?':
			result.WriteString(lookup("?"))
			i++

		case runes[i] == '$':
			result.WriteString(lookup("$"))
			i++

		case runes[i] == '!':
			result.WriteString(lookup("!"))
			i++

		case runes[i] == '#':
			result.WriteString(lookup("#"))
			i++

		case runes[i] == '@' || runes[i] == '*':
			result.WriteString(lookup(string(runes[i])))
			i++

		case runes[i] >= '0' && runes[i] <= '9':
			result.WriteString(lookup(string(runes[i])))
			i++

		case isNameStart(runes[i]):
			start := i
			for i < len(runes) && isNameCont(runes[i]) {
				i++
			}
			name := string(runes[start:i])
			result.WriteString(lookup(name))

		default:
			result.WriteRune('$')
		}
	}

	return result.String()
}
