package config

import "testing"

func TestProcessingModeResolve(t *testing.T) {
	for _, mode := range []ProcessingMode{"", ProcessingOriginalOnly, ProcessingMediaResizer} {
		resolved, err := mode.Resolve()
		if err != nil {
			t.Fatal(err)
		}
		want := mode
		if want == "" {
			want = ProcessingOriginalOnly
		}
		if resolved != want {
			t.Fatalf("mode %q resolved to %q, want %q", mode, resolved, want)
		}
	}
	for _, mode := range []ProcessingMode{"auto", "disabled", "ORIGINAL_ONLY", " original_only", "resizer"} {
		if _, err := mode.Resolve(); err == nil {
			t.Fatalf("invalid mode %q accepted", mode)
		}
	}
}
