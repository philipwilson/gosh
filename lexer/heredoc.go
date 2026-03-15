package lexer

import (
	"fmt"
	"strings"
)

// ResolveHeredocs finds TOKEN_HEREDOC tokens in the stream and fills
// in their Body field by reading lines from lineReader. The lineReader
// callback returns the next line (without trailing newline) and true,
// or ("", false) on EOF.
//
// Multiple heredocs on a single command line have their bodies read
// in order of appearance.
func ResolveHeredocs(tokens []Token, lineReader func() (string, bool)) error {
	var pending []*HeredocInfo
	for i := range tokens {
		if tokens[i].Type == TOKEN_HEREDOC && tokens[i].Heredoc != nil {
			pending = append(pending, tokens[i].Heredoc)
		}
	}

	for _, hd := range pending {
		var bodyLines []string
		for {
			line, ok := lineReader()
			if !ok {
				return fmt.Errorf("here-document delimited by '%s' not found", hd.Delim)
			}
			checkLine := line
			if hd.StripTabs {
				checkLine = strings.TrimLeft(line, "\t")
			}
			if checkLine == hd.Delim {
				break
			}
			if hd.StripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			bodyLines = append(bodyLines, line)
		}

		body := strings.Join(bodyLines, "\n")
		if len(bodyLines) > 0 {
			body += "\n"
		}

		if hd.Expand {
			parts, err := LexHeredocBody(body)
			if err != nil {
				return err
			}
			hd.Body = parts
		} else {
			hd.Body = Word{{Text: body, Quote: SingleQuoted}}
		}
	}

	return nil
}

// HasHeredocs returns true if any TOKEN_HEREDOC tokens exist in the stream.
func HasHeredocs(tokens []Token) bool {
	for _, tok := range tokens {
		if tok.Type == TOKEN_HEREDOC {
			return true
		}
	}
	return false
}
