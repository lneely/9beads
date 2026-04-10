package p9

import (
	"testing"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantArgs []string
		wantErr  bool
	}{
		{
			name:     "empty input",
			input:    "",
			wantArgs: nil,
		},
		{
			name:     "whitespace only",
			input:    "   ",
			wantArgs: nil,
		},
		{
			name:     "single word",
			input:    "claim",
			wantArgs: []string{"claim"},
		},
		{
			name:     "two words",
			input:    "claim bd-123",
			wantArgs: []string{"claim", "bd-123"},
		},
		{
			name:     "multiple words",
			input:    "new title description parent",
			wantArgs: []string{"new", "title", "description", "parent"},
		},
		{
			name:     "single quoted arg",
			input:    "new 'my title'",
			wantArgs: []string{"new", "my title"},
		},
		{
			name:     "double quoted arg",
			input:    `new "my title"`,
			wantArgs: []string{"new", "my title"},
		},
		{
			name:     "mixed quoted and unquoted",
			input:    "new 'my title' description bd-parent",
			wantArgs: []string{"new", "my title", "description", "bd-parent"},
		},
		{
			name:     "empty quoted string",
			input:    "comment bd-123 ''",
			wantArgs: []string{"comment", "bd-123", ""},
		},
		{
			name:     "multiple spaces",
			input:    "claim    bd-123",
			wantArgs: []string{"claim", "bd-123"},
		},
		{
			name:     "tabs and spaces",
			input:    "claim\t\tbd-123",
			wantArgs: []string{"claim", "bd-123"},
		},
		{
			name:     "quoted arg with special chars",
			input:    "comment bd-123 'Work in progress: 50% done'",
			wantArgs: []string{"comment", "bd-123", "Work in progress: 50% done"},
		},
		{
			name:     "backslash escape in double quotes",
			input:    `new "title with \"quotes\""`,
			wantArgs: []string{"new", `title with "quotes"`},
		},
		{
			name:     "unicode escape",
			input:    `new "hello \u0041"`,
			wantArgs: []string{"new", "hello A"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := ParseArgs(tt.input)

			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if len(args) != len(tt.wantArgs) {
				t.Errorf("args length = %d, want %d\nargs = %v\nwant = %v",
					len(args), len(tt.wantArgs), args, tt.wantArgs)
				return
			}

			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}
