package mcpclient

import (
	"context"
	"strings"
	"testing"
	"time"
)

// v0.10.803 (dış skill denetimi M3, agents-mcp "scoped env vars") — stdio alt
// süreci Coremetry'nin ortamını MİRAS ALMAZ: yalnız allowlist + cfg.Env.

func TestStdioEnvPure(t *testing.T) {
	parent := []string{"PATH=/usr/bin", "HOME=/home/x", "COREMETRY_CH_PASSWORD=s3cret", "OPENAI_API_KEY=k", "TZ=UTC", "BROKEN"}
	got := stdioEnv(map[string]string{"MCP_ROOT": "/data", " ": "ignored", "PATH": "/opt/bin"}, parent)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"HOME=/home/x", "TZ=UTC", "MCP_ROOT=/data", "PATH=/opt/bin"} {
		if !strings.Contains(joined, want) {
			t.Errorf("eksik %q in %v", want, got)
		}
	}
	for _, leak := range []string{"COREMETRY_CH_PASSWORD", "OPENAI_API_KEY", "BROKEN", "PATH=/usr/bin"} {
		if strings.Contains(joined, leak) {
			t.Errorf("sızdı: %q in %v", leak, got)
		}
	}
	// Deterministik sıra.
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("sıralı değil: %v", got)
		}
	}
	if len(stdioEnv(nil, nil)) != 0 {
		t.Error("boş üst + boş cfg → boş")
	}
}

// Canlı: helper alt süreç üst sürecin sırrını GÖRMEZ, cfg.Env'i görür.
func TestStdioChildDoesNotInheritParentEnv(t *testing.T) {
	t.Setenv("COREMETRY_SECRET_PROBE", "leak")
	cfg := helperConfig(t) // helperEnv işareti cfg.Env'de — miras yok
	cfg.Env["MCP_PROBE"] = "given"
	tr, err := newStdioTransport(cfg)
	if err != nil {
		t.Fatalf("stdio başlatılamadı: %v", err)
	}
	defer tr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := func(name string) string {
		var out struct {
			Value string `json:"value"`
		}
		if err := tr.Call(ctx, "env", map[string]any{"name": name}, &out); err != nil {
			t.Fatalf("env %s: %v", name, err)
		}
		return out.Value
	}
	if v := probe("COREMETRY_SECRET_PROBE"); v != "" {
		t.Fatalf("üst sürecin sırrı alt sürece sızdı: %q", v)
	}
	if v := probe("MCP_PROBE"); v != "given" {
		t.Fatalf("cfg.Env alt sürece geçmedi: %q", v)
	}
	if v := probe("PATH"); v == "" {
		t.Fatal("PATH allowlist'te olmalı")
	}
}

func TestEnvKeysOf(t *testing.T) {
	if EnvKeysOf(nil) != nil {
		t.Error("nil → nil")
	}
	got := EnvKeysOf(map[string]string{"B": "2", "A": "1"})
	if len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("%v", got)
	}
	for _, k := range got {
		if strings.Contains(k, "1") || strings.Contains(k, "2") {
			t.Error("değer sızdı")
		}
	}
}
