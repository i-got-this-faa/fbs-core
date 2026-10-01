package sharelink

import (
	"strings"
	"testing"
)

func TestGenerateCodeIsBase62AndUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for range 1000 {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode() error = %v", err)
		}
		if len(code) != GeneratedCodeLength {
			t.Fatalf("len(code) = %d, want %d", len(code), GeneratedCodeLength)
		}
		if strings.Trim(code, codeAlphabet) != "" {
			t.Fatalf("code %q contains non-base62 characters", code)
		}
		if ValidateAlias(code) != nil {
			t.Fatalf("generated code %q is not a valid alias", code)
		}
		if _, dup := seen[code]; dup {
			t.Fatalf("duplicate code %q", code)
		}
		seen[code] = struct{}{}
	}
}

func TestValidateAlias(t *testing.T) {
	tests := []struct {
		alias string
		valid bool
	}{
		{"abc", true},
		{"vacation-2026", true},
		{"My_Clip", true},
		{"ab", false},
		{"-abc", false},
		{"_abc", false},
		{"has space", false},
		{"slash/inside", false},
		{"dot.mp4", false},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"", false},
	}
	for _, tt := range tests {
		err := ValidateAlias(tt.alias)
		if (err == nil) != tt.valid {
			t.Errorf("ValidateAlias(%q) error = %v, want valid=%v", tt.alias, err, tt.valid)
		}
	}
}

func TestPath(t *testing.T) {
	if got := Path("abc123"); got != "/s/abc123" {
		t.Fatalf("Path() = %q, want /s/abc123", got)
	}
}
