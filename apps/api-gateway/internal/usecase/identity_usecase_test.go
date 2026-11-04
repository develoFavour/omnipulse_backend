package usecase

import (
	"context"
	"strings"
	"testing"
)

func TestSplitFullName(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantFirst string
		wantLast  string
	}{
		{name: "empty", input: "", wantFirst: "", wantLast: ""},
		{name: "whitespace only", input: "   ", wantFirst: "", wantLast: ""},
		{name: "single name", input: "Favour", wantFirst: "Favour", wantLast: ""},
		{name: "first and last", input: "Favour Opia", wantFirst: "Favour", wantLast: "Opia"},
		{name: "extra whitespace", input: "  Mary   Jane  Watson ", wantFirst: "Mary", wantLast: "Jane Watson"},
		{
			name:      "long first name is capped at the column limit",
			input:     strings.Repeat("a", 120),
			wantFirst: strings.Repeat("a", maxUserNameLength),
			wantLast:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, last := splitFullName(tc.input)
			if first != tc.wantFirst || last != tc.wantLast {
				t.Fatalf("splitFullName(%q) = (%q, %q), want (%q, %q)", tc.input, first, last, tc.wantFirst, tc.wantLast)
			}
		})
	}
}

func TestUpdateUserNameRejectsInvalidInput(t *testing.T) {
	uc := &IdentityUseCase{}

	if err := uc.UpdateUserName(context.Background(), "", "Favour", "Opia"); err == nil {
		t.Fatal("expected an error for an empty user ID")
	}
	if err := uc.UpdateUserName(context.Background(), "user_123", "   ", "Opia"); err == nil {
		t.Fatal("expected an error for an empty first name")
	}
}
