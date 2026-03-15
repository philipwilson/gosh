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
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		{"--5", 5},    // double negative
		{"-(-3)", 3},  // negation with parens
		{"!0", 1},     // logical not of 0
		{"!1", 0},     // logical not of non-zero
		{"!42", 0},    // logical not of non-zero
		{"~0", -1},    // bitwise complement
	}

	for _, tt := range tests {
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		got, err := evalArith(tt.expr, lookup)
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
		_, err := evalArith(tt.expr, lookup)
		if err == nil {
			t.Errorf("evalArith(%q): expected error containing %q, got nil", tt.expr, tt.wantErr)
			continue
		}
	}
}

func TestArithNonIntegerVariable(t *testing.T) {
	vars := map[string]string{"s": "hello"}
	lookup := func(name string) string { return vars[name] }

	_, err := evalArith("s + 1", lookup)
	if err == nil {
		t.Error("evalArith(\"s + 1\"): expected error for non-integer variable")
	}
}
