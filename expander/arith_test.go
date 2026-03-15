package expander

import "testing"

func TestArithBasicOps(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"1 + 2", 3},
		{"10 - 3", 7},
		{"4 * 5", 20},
		{"10 / 3", 3},
		{"10 % 3", 1},
		{"0", 0},
		{"42", 42},
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithPrecedence(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"2 + 3 * 4", 14},     // * before +
		{"(2 + 3) * 4", 20},   // parens override
		{"10 - 2 - 3", 5},     // left associative
		{"1 + 2 * 3 + 4", 11}, // mixed
		{"8 / 2 / 2", 2},      // left associative division
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithUnary(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"-5", -5},
		{"+5", 5},
		{"- -5", 5},   // double negative (with space)
		{"-(-3)", 3},  // negation with parens
		{"!0", 1},     // logical not of 0
		{"!1", 0},     // logical not of non-zero
		{"!42", 0},    // logical not of non-zero
		{"~0", -1},    // bitwise complement
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithComparison(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"1 < 2", 1},
		{"2 < 1", 0},
		{"2 <= 2", 1},
		{"3 <= 2", 0},
		{"2 > 1", 1},
		{"1 > 2", 0},
		{"2 >= 2", 1},
		{"1 >= 2", 0},
		{"5 == 5", 1},
		{"5 == 6", 0},
		{"5 != 6", 1},
		{"5 != 5", 0},
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithLogical(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"1 && 1", 1},
		{"1 && 0", 0},
		{"0 && 1", 0},
		{"0 && 0", 0},
		{"1 || 0", 1},
		{"0 || 1", 1},
		{"0 || 0", 0},
		{"1 || 1", 1},
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithBitwise(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"5 & 3", 1},    // 101 & 011 = 001
		{"5 | 3", 7},    // 101 | 011 = 111
		{"5 ^ 3", 6},    // 101 ^ 011 = 110
		{"1 << 4", 16},  // shift left
		{"16 >> 2", 4},  // shift right
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithTernary(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr string
		want int64
	}{
		{"1 ? 10 : 20", 10},
		{"0 ? 10 : 20", 20},
		{"5 > 3 ? 100 : 200", 100},
		{"5 < 3 ? 100 : 200", 200},
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithVariables(t *testing.T) {
	vars := map[string]string{"x": "5", "y": "3", "empty": ""}
	lookup := func(name string) string { return vars[name] }

	tests := []struct {
		expr string
		want int64
	}{
		{"x", 5},
		{"x + y", 8},
		{"x * y", 15},
		{"$x + $y", 8},       // $-prefixed variables
		{"${x} + ${y}", 8},   // ${}-prefixed variables
		{"undefined", 0},     // undefined defaults to 0
		{"x + undefined", 5}, // undefined defaults to 0
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, nil)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
	}
}

func TestArithErrors(t *testing.T) {
	lookup := func(name string) string { return "" }

	tests := []struct {
		expr    string
		wantErr string
	}{
		{"1 / 0", "division by zero"},
		{"1 % 0", "division by zero"},
	}

	for _, tt := range tests {
		_, err := evalArith(tt.expr, lookup, nil)
		if err == nil {
			t.Errorf("evalArith(%q): expected error containing %q, got nil", tt.expr, tt.wantErr)
			continue
		}
	}
}

func TestArithNonIntegerVariable(t *testing.T) {
	vars := map[string]string{"s": "hello"}
	lookup := func(name string) string { return vars[name] }

	_, err := evalArith("s + 1", lookup, nil)
	if err == nil {
		t.Error("evalArith(\"s + 1\"): expected error for non-integer variable")
	}
}

func TestArithAssignment(t *testing.T) {
	vars := map[string]string{"x": "10", "y": "3"}
	lookup := func(name string) string { return vars[name] }
	setVar := func(name, value string) { vars[name] = value }

	tests := []struct {
		expr     string
		want     int64
		wantVars map[string]string
	}{
		{"x = 5", 5, map[string]string{"x": "5"}},
		{"x += 3", 8, map[string]string{"x": "8"}},
		{"x -= 2", 6, map[string]string{"x": "6"}},
		{"x *= 4", 24, map[string]string{"x": "24"}},
		{"x /= 6", 4, map[string]string{"x": "4"}},
		{"x %= 3", 1, map[string]string{"x": "1"}},
		{"z = 42", 42, map[string]string{"z": "42"}},
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup, setVar)
		if err != nil {
			t.Errorf("evalArith(%q): unexpected error: %v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("evalArith(%q) = %d, want %d", tt.expr, got, tt.want)
		}
		for k, v := range tt.wantVars {
			if vars[k] != v {
				t.Errorf("evalArith(%q): vars[%q] = %q, want %q", tt.expr, k, vars[k], v)
			}
		}
	}
}

func TestArithIncDec(t *testing.T) {
	vars := map[string]string{"x": "5"}
	lookup := func(name string) string { return vars[name] }
	setVar := func(name, value string) { vars[name] = value }

	// Pre-increment: ++x returns new value.
	got, err := evalArith("++x", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith(\"++x\"): %v", err)
	}
	if got != 6 {
		t.Errorf("++x = %d, want 6", got)
	}
	if vars["x"] != "6" {
		t.Errorf("after ++x: x = %q, want \"6\"", vars["x"])
	}

	// Pre-decrement: --x returns new value.
	got, err = evalArith("--x", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith(\"--x\"): %v", err)
	}
	if got != 5 {
		t.Errorf("--x = %d, want 5", got)
	}

	// Post-increment: x++ returns old value.
	got, err = evalArith("x++", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith(\"x++\"): %v", err)
	}
	if got != 5 {
		t.Errorf("x++ = %d, want 5 (old value)", got)
	}
	if vars["x"] != "6" {
		t.Errorf("after x++: x = %q, want \"6\"", vars["x"])
	}

	// Post-decrement: x-- returns old value.
	got, err = evalArith("x--", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith(\"x--\"): %v", err)
	}
	if got != 6 {
		t.Errorf("x-- = %d, want 6 (old value)", got)
	}
	if vars["x"] != "5" {
		t.Errorf("after x--: x = %q, want \"5\"", vars["x"])
	}
}

func TestArithAssignInExpr(t *testing.T) {
	vars := map[string]string{}
	lookup := func(name string) string { return vars[name] }
	setVar := func(name, value string) { vars[name] = value }

	// Assignment in a larger expression.
	got, err := evalArith("x = 3 + 4", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith: %v", err)
	}
	if got != 7 {
		t.Errorf("x = 3 + 4 → %d, want 7", got)
	}
	if vars["x"] != "7" {
		t.Errorf("x = %q, want \"7\"", vars["x"])
	}

	// Chained assignment (right-associative).
	got, err = evalArith("y = x = 10", lookup, setVar)
	if err != nil {
		t.Fatalf("evalArith: %v", err)
	}
	if got != 10 {
		t.Errorf("y = x = 10 → %d, want 10", got)
	}
	if vars["x"] != "10" || vars["y"] != "10" {
		t.Errorf("x=%q y=%q, want both \"10\"", vars["x"], vars["y"])
	}
}
