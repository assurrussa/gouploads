package uploadfilejob

import "testing"

func TestTerminalAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		attempt     int
		maxAttempts int
		hasMetadata bool
		want        bool
	}{
		{name: "missing metadata", attempt: 30, maxAttempts: 30, hasMetadata: false},
		{name: "invalid maximum", attempt: 1, maxAttempts: 0, hasMetadata: true},
		{name: "transient attempt", attempt: 29, maxAttempts: 30, hasMetadata: true},
		{name: "terminal attempt", attempt: 30, maxAttempts: 30, hasMetadata: true, want: true},
		{name: "attempt beyond maximum", attempt: 31, maxAttempts: 30, hasMetadata: true, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := terminalAttempt(tt.attempt, tt.maxAttempts, tt.hasMetadata); got != tt.want {
				t.Fatalf("terminalAttempt() = %v, want %v", got, tt.want)
			}
		})
	}
}
