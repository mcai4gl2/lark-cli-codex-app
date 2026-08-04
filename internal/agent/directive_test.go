package agent

import "testing"

func TestParseBackendDirective(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantBackend string
		wantRest    string
		wantOK      bool
	}{
		{"grok with rest", "/grok explain this", "grok", "explain this", true},
		{"codex", "/codex fix the bug", "codex", "fix the bug", true},
		{"agy alias", "/antigravity do it", "agy", "do it", true},
		{"case insensitive", "/Grok Hello", "grok", "Hello", true},
		{"directive only", "/grok", "grok", "", true},
		{"directive only whitespace rest", "/grok   ", "grok", "", true},
		{"unrecognized slash word", "/something-unrelated please", "", "/something-unrelated please", false},
		{"path like text", "/tmp/file.txt", "", "/tmp/file.txt", false},
		{"no slash", "grok explain", "", "grok explain", false},
		{"slash mid message", "please /grok this", "", "please /grok this", false},
		{"empty", "", "", "", false},
		{"leading spaces then directive", "  /agy hello", "agy", "hello", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend, rest, ok := ParseBackendDirective(tc.in)
			if ok != tc.wantOK || backend != tc.wantBackend || rest != tc.wantRest {
				t.Fatalf("ParseBackendDirective(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, backend, rest, ok, tc.wantBackend, tc.wantRest, tc.wantOK)
			}
		})
	}
}
