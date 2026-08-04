package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestResolveRegisteredBackendsAndAliases(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"codex", "codex", true},
		{" CODEX ", "codex", true},
		{"agy", "agy", true},
		{"antigravity", "agy", true},
		{"antigravity-cli", "agy", true},
		{"", "", false},
		{"unknown-value", "", false},
		{"claude", "", false},
		{"grok", "", false}, // registered in Task 3
	}
	for _, tc := range cases {
		b, ok := Resolve(tc.in)
		if ok != tc.ok {
			t.Fatalf("Resolve(%q) ok=%v, want %v", tc.in, ok, tc.ok)
		}
		if !tc.ok {
			continue
		}
		if b.Name() != tc.want {
			t.Fatalf("Resolve(%q).Name() = %q, want %q", tc.in, b.Name(), tc.want)
		}
	}
}

func TestRegisteredBackendNamesSorted(t *testing.T) {
	got := RegisteredBackendNames()
	want := []string{"agy", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredBackendNames() = %#v, want %#v", got, want)
	}
}

func TestBackendLabel(t *testing.T) {
	if got := backendLabel("codex"); got != "本地 Codex 执行代理" {
		t.Fatalf("codex label = %q", got)
	}
	if got := backendLabel("agy"); got != "本地 Antigravity/agy 执行代理" {
		t.Fatalf("agy label = %q", got)
	}
	if got := backendLabel("antigravity"); got != "本地 Antigravity/agy 执行代理" {
		t.Fatalf("alias label = %q", got)
	}
	if got := backendLabel("nope"); got != "本地执行代理" {
		t.Fatalf("unknown label = %q", got)
	}
}

func TestResolveBackendFromConfig(t *testing.T) {
	b, err := resolveBackend(Config{Backend: "agy"})
	if err != nil || b.Name() != "agy" {
		t.Fatalf("agy: b=%v err=%v", b, err)
	}
	b, err = resolveBackend(Config{Backend: ""})
	if err != nil || b.Name() != "codex" {
		t.Fatalf("empty default: b=%v err=%v", b, err)
	}
	_, err = resolveBackend(Config{Backend: "typo-name"})
	if err == nil {
		t.Fatal("expected error for unregistered default backend")
	}
	if !strings.Contains(err.Error(), "typo-name") {
		t.Fatalf("error = %v, want typo-name mentioned", err)
	}
}

func TestResolveBackendBinary(t *testing.T) {
	cfg := Config{Backend: "codex", Binary: "", CodexBinary: "custom-codex"}
	backend := testBackend{name: "codex", defaultBinary: "codex"}
	if got := resolveBackendBinary(cfg, backend); got != "custom-codex" {
		t.Fatalf("binary = %q, want custom-codex", got)
	}

	cfg = Config{Backend: "agy"}
	backend = testBackend{name: "agy", defaultBinary: "agy"}
	if got := resolveBackendBinary(cfg, backend); got != "agy" {
		t.Fatalf("binary = %q, want agy", got)
	}

	cfg = Config{Backend: "agy", Binary: " /opt/bin/agy "}
	if got := resolveBackendBinary(cfg, backend); got != "/opt/bin/agy" {
		t.Fatalf("binary = %q, want /opt/bin/agy", got)
	}
}

type testBackend struct {
	name          string
	defaultBinary string
}

func (b testBackend) Name() string { return b.name }

func (b testBackend) DefaultBinary() string { return b.defaultBinary }

func (b testBackend) Execute(context.Context, BackendRequest) (BackendResult, error) {
	return BackendResult{}, nil
}
