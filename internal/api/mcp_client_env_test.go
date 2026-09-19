package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/mcpclient"
)

// v0.10.803 (dış skill denetimi M3) — stdio Env: PUT birleştirmesi token
// sözleşmesini anahtar-başına uygular; snapshot/audit değer taşımaz.

func TestMergeEnvPure(t *testing.T) {
	stored := map[string]string{"API_KEY": "saklı", "OLD": "eski"}
	cases := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{"nil girdi → nil", nil, nil},
		{"boş girdi → nil", map[string]string{}, nil},
		{"sentinel saklıyı korur", map[string]string{"API_KEY": secretKept}, map[string]string{"API_KEY": "saklı"}},
		{"sentinel ama saklı yok → anahtar düşer", map[string]string{"NEW": secretKept}, nil},
		{"yeni değer değiştirir", map[string]string{"API_KEY": "yeni"}, map[string]string{"API_KEY": "yeni"}},
		{"boş değer anahtarı düşürür", map[string]string{"API_KEY": ""}, nil},
		{"formda olmayan saklı anahtar düşer", map[string]string{"API_KEY": secretKept}, map[string]string{"API_KEY": "saklı"}},
		{"anahtar kırpılır, boş anahtar atlanır", map[string]string{" X ": "1", "  ": "2"}, map[string]string{"X": "1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mergeEnv(tc.in, stored)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s=%q, want %q", k, got[k], v)
				}
			}
			if _, ok := got["OLD"]; ok {
				t.Error("formda olmayan saklı anahtar düşmeliydi")
			}
		})
	}
}

func TestMergeMCPServers_EnvMerge(t *testing.T) {
	stored := mcpCur(mcpclient.ServerConfig{
		Name: "fs", Transport: "stdio", Command: "/opt/mcp/fs", Enabled: true,
		Env: map[string]string{"MCP_ROOT": "/data", "API_KEY": "saklı"},
	})
	in := mcpServerInput{Name: "fs", Transport: "stdio", Command: "/opt/mcp/fs", Enabled: true,
		Env: map[string]string{"MCP_ROOT": "/mnt", "API_KEY": secretKept}}
	out, bad := mergeMCPServers([]mcpServerInput{in}, stored)
	if bad != "" {
		t.Fatalf("beklenmeyen 400: %s", bad)
	}
	env := out.Servers[0].Env
	if env["MCP_ROOT"] != "/mnt" || env["API_KEY"] != "saklı" {
		t.Errorf("env birleşmedi: %v", env)
	}
	// Snapshot yalnız anahtar; değer asla.
	snap := mcpclient.EnvKeysOf(env)
	if len(snap) != 2 || snap[0] != "API_KEY" || snap[1] != "MCP_ROOT" {
		t.Errorf("EnvKeys sıralı anahtar listesi olmalı: %v", snap)
	}
	raw, _ := json.Marshal(mcpclient.ServerSnapshot{Name: "fs", EnvKeys: snap})
	if strings.Contains(string(raw), "saklı") || strings.Contains(string(raw), "/mnt") {
		t.Errorf("snapshot değer sızdırdı: %s", raw)
	}
}
