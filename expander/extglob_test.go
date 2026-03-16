package expander

import "testing"

func TestHasExtglob(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"foo", false},
		{"*.txt", false},
		{"@(a|b)", true},
		{"?(a)", true},
		{"*(a)", true},
		{"+(a)", true},
		{"!(a)", true},
		{"foo@(a|b)bar", true},
		{`\@(a)`, false}, // escaped
	}
	for _, tt := range tests {
		got := HasExtglob(tt.pattern)
		if got != tt.want {
			t.Errorf("HasExtglob(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

func TestExtglobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		s       string
		want    bool
	}{
		// @(pat) — exactly one
		{"@(foo|bar)", "foo", true},
		{"@(foo|bar)", "bar", true},
		{"@(foo|bar)", "baz", false},
		{"@(foo|bar)", "", false},

		// ?(pat) — zero or one
		{"?(foo)", "", true},
		{"?(foo)", "foo", true},
		{"?(foo)", "foofoo", false},
		{"?(a|b)", "a", true},
		{"?(a|b)", "b", true},
		{"?(a|b)", "", true},
		{"?(a|b)", "c", false},

		// *(pat) — zero or more
		{"*(a)", "", true},
		{"*(a)", "a", true},
		{"*(a)", "aaa", true},
		{"*(a)", "b", false},
		{"*(ab)", "ababab", true},
		{"*(ab)", "aba", false},
		{"*(a|b)", "abba", true},

		// +(pat) — one or more
		{"+(a)", "a", true},
		{"+(a)", "aaa", true},
		{"+(a)", "", false},
		{"+(a|b)", "abba", true},
		{"+(a|b)", "", false},

		// !(pat) — negation
		{"!(foo)", "bar", true},
		{"!(foo)", "foo", false},
		{"!(foo)", "", true},
		{"!(foo)", "foobar", true},
		{"!(a|b)", "c", true},
		{"!(a|b)", "a", false},
		{"!(a|b)", "b", false},

		// Mixed with regular glob
		{"*.@(c|h)", "foo.c", true},
		{"*.@(c|h)", "foo.h", true},
		{"*.@(c|h)", "foo.o", false},
		{"@(*.txt|*.go)", "foo.txt", true},
		{"@(*.txt|*.go)", "foo.go", true},
		{"@(*.txt|*.go)", "foo.rs", false},

		// Prefix/suffix with extglob
		{"f@(oo|aa)bar", "foobar", true},
		{"f@(oo|aa)bar", "faabar", true},
		{"f@(oo|aa)bar", "fxxbar", false},

		// Nested extglob
		{"+(?(a)b)", "ab", true},
		{"+(?(a)b)", "b", true},
		{"+(?(a)b)", "abab", true},

		// !(pattern) with glob inside
		{"*.!(txt)", "foo.go", true},
		{"*.!(txt)", "foo.txt", false},
		{"*.!(txt)", "foo.c", true},

		// Character class inside extglob
		{"@([0-9])", "5", true},
		{"@([0-9])", "a", false},
		{"+([0-9])", "123", true},
		{"+([0-9])", "12a", false},
	}
	for _, tt := range tests {
		got := ExtglobMatch(tt.pattern, tt.s)
		if got != tt.want {
			t.Errorf("ExtglobMatch(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func TestExtglobMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		s       string
		want    bool
	}{
		// * should not match /
		{"*.@(c|h)", "dir/foo.c", false},
		{"@(a|b)", "a", true},
	}
	for _, tt := range tests {
		got := ExtglobMatchPath(tt.pattern, tt.s)
		if got != tt.want {
			t.Errorf("ExtglobMatchPath(%q, %q) = %v, want %v", tt.pattern, tt.s, got, tt.want)
		}
	}
}

func TestBroadenExtglob(t *testing.T) {
	tests := []struct {
		pattern string
		want    string
	}{
		{"@(a|b)", "*"},
		{"*.@(c|h)", "*.*"},
		{"foo+(x)bar", "foo*bar"},
		{"no_extglob", "no_extglob"},
	}
	for _, tt := range tests {
		got := broadenExtglob(tt.pattern)
		if got != tt.want {
			t.Errorf("broadenExtglob(%q) = %q, want %q", tt.pattern, got, tt.want)
		}
	}
}
