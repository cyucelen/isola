package expand

import "testing"

func TestBraces(t *testing.T) {
	vars := map[string]string{
		"ISOLA_BRANCH_SLUG": "feature-auth",
		"DB":                "myapp_x",
	}
	mapping := func(name string) string { return vars[name] }

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no reference", "plain", "plain"},
		{"single var", "myapp_${ISOLA_BRANCH_SLUG}", "myapp_feature-auth"},
		{"multiple vars", "${DB}-${ISOLA_BRANCH_SLUG}", "myapp_x-feature-auth"},
		{"unknown var expands empty", "a${NOPE}b", "ab"},
		{"bare dollar left literal", "p$ssw0rd", "p$ssw0rd"},
		{"dollar without brace literal", "cost is $5", "cost is $5"},
		{"unclosed brace left as-is", "a${UNCLOSED", "a${UNCLOSED"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Braces(tt.in, mapping); got != tt.want {
				t.Errorf("Braces(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
