package uploadfile

import "testing"

func TestOriginalKeyConfinement(t *testing.T) {
	prefixes, err := normalizeOriginalPrefixes(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"tmp/uploads/admin/1/source.pdf", "staging/v1/tus/session/source.png"} {
		if err := validateOriginalKey(key, prefixes); err != nil {
			t.Fatalf("valid key %q: %v", key, err)
		}
	}
	for _, key := range []string{
		"", ".", "..", "../a", "/tmp/uploads/a", "https://host/a", "tmp/uploads/../a",
		"tmp//uploads/a", "tmp/uploads/./a", "tmp/uploads", "tmp/uploads2/a", "tmp/uploads/a\\b", "tmp/uploads/a\x00b",
		"media/v1/admin/1/main.pdf",
	} {
		if err := validateOriginalKey(key, prefixes); err == nil {
			t.Fatalf("unsafe key %q accepted", key)
		}
	}
}

func TestOriginalPrefixesAreCopiedAndValidated(t *testing.T) {
	input := []string{"custom/staging"}
	prefixes, err := normalizeOriginalPrefixes(input)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = "changed"
	if err := validateOriginalKey("custom/staging/a.pdf", prefixes); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"", ".", "..", "../private", "/tmp", "a/../b", "a/", "a\\b", "https://host"} {
		if _, err := normalizeOriginalPrefixes([]string{prefix}); err == nil {
			t.Fatalf("unsafe prefix %q accepted", prefix)
		}
	}
}
