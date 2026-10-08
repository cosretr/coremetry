package mcpclient

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// seed_test.go — v0.10.1132: COREMETRY_MCP_SEED_JSON tohumu. Sözleşme:
// operatörün kaydı asla ezilmez, silinen tohum geri gelmez, sır JSON'a
// girmez (tokenEnv yalnız COREMETRY_MCP_SEED_ önekli env'i okuyabilir).

func TestParseSeed(t *testing.T) {
	env := map[string]string{
		"COREMETRY_MCP_SEED_GRAFANA_TOKEN": "caller-tok",
		"COREMETRY_JWT_SECRET":             "must-not-leak",
	}
	getenv := func(k string) string { return env[k] }
	cases := []struct {
		name    string
		raw     string
		wantErr string
		check   func(t *testing.T, got []ServerConfig)
	}{
		{name: "empty", raw: "  ", check: func(t *testing.T, got []ServerConfig) {
			if got != nil {
				t.Fatalf("want nil, got %v", got)
			}
		}},
		{name: "array defaults", raw: `[{"name":"grafana","url":"http://gm.obs.svc:8000/mcp","allowTools":[" query_prometheus ",""]}]`,
			check: func(t *testing.T, got []ServerConfig) {
				if len(got) != 1 {
					t.Fatalf("len=%d", len(got))
				}
				g := got[0]
				if g.Transport != "http" || !g.Enabled || g.Token != "" {
					t.Fatalf("defaults wrong: %+v", g)
				}
				if len(g.AllowTools) != 1 || g.AllowTools[0] != "query_prometheus" {
					t.Fatalf("allowTools not trimmed: %v", g.AllowTools)
				}
			}},
		{name: "wrapped + tokenEnv + disabled", raw: `{"servers":[{"name":"grafana","url":"https://gm.example.test/mcp","tokenEnv":"COREMETRY_MCP_SEED_GRAFANA_TOKEN","enabled":false}]}`,
			check: func(t *testing.T, got []ServerConfig) {
				if got[0].Token != "caller-tok" || got[0].Enabled {
					t.Fatalf("got %+v", got[0])
				}
			}},
		{name: "tokenEnv outside prefix", raw: `[{"name":"x","url":"http://a/mcp","tokenEnv":"COREMETRY_JWT_SECRET"}]`, wantErr: "önekini"},
		{name: "tokenEnv empty", raw: `[{"name":"x","url":"http://a/mcp","tokenEnv":"COREMETRY_MCP_SEED_MISSING"}]`, wantErr: "boş"},
		{name: "stdio rejected", raw: `[{"name":"x","transport":"stdio","url":"http://a"}]`, wantErr: "yalnız http"},
		{name: "bad url", raw: `[{"name":"x","url":"gm:8000"}]`, wantErr: "URL"},
		{name: "empty name", raw: `[{"name":" __ ","url":"http://a"}]`, wantErr: "boş olamaz"},
		{name: "duplicate sanitized", raw: `[{"name":"Grafana","url":"http://a"},{"name":"grafana","url":"http://b"}]`, wantErr: "tekrar"},
		{name: "bad json", raw: `[{`, wantErr: SeedEnv},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSeed(tc.raw, getenv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			tc.check(t, got)
		})
	}
}

func TestPlanSeed(t *testing.T) {
	seed := ServerConfig{Name: "grafana", Transport: "http", URL: "http://gm/mcp", Enabled: true}
	operator := ServerConfig{Name: "Grafana", Transport: "http", URL: "http://operator-edited/mcp", Enabled: false}
	full := Settings{}
	for i := 0; i < MaxServers; i++ {
		full.Servers = append(full.Servers, ServerConfig{Name: fmt.Sprintf("s%d", i), Transport: "http", URL: "http://a"})
	}
	cases := []struct {
		name        string
		cur         Settings
		seeded      []string
		wantAdded   int
		wantServers int
		wantMarker  []string
		wantURL     string // URL of the "grafana" entry after planning ("" = absent)
	}{
		{name: "fresh install adds", wantAdded: 1, wantServers: 1, wantMarker: []string{"grafana"}, wantURL: "http://gm/mcp"},
		{name: "operator entry untouched", cur: Settings{Servers: []ServerConfig{operator}}, wantServers: 1, wantMarker: []string{"grafana"}, wantURL: "http://operator-edited/mcp"},
		{name: "deleted after seeding stays deleted", seeded: []string{"grafana"}, wantServers: 0, wantMarker: []string{"grafana"}},
		{name: "list full: skipped, not marked", cur: full, wantServers: MaxServers, wantMarker: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next, marker, added := PlanSeed(tc.cur, tc.seeded, []ServerConfig{seed})
			if len(added) != tc.wantAdded || len(next.Servers) != tc.wantServers {
				t.Fatalf("added=%v servers=%d", added, len(next.Servers))
			}
			if strings.Join(marker, ",") != strings.Join(tc.wantMarker, ",") {
				t.Fatalf("marker=%v want %v", marker, tc.wantMarker)
			}
			url := ""
			for _, sv := range next.Servers {
				if SanitizedName(sv.Name) == "grafana" {
					url = sv.URL
				}
			}
			if url != tc.wantURL {
				t.Fatalf("grafana url=%q want %q", url, tc.wantURL)
			}
		})
	}
}

type fakeSeedStore struct{ kv map[string][]byte }

func (f *fakeSeedStore) GetSetting(_ context.Context, k string) ([]byte, error) { return f.kv[k], nil }
func (f *fakeSeedStore) PutSetting(_ context.Context, k string, v []byte) error {
	f.kv[k] = v
	return nil
}
func (f *fakeSeedStore) GetMCPClientSettingsRaw(ctx context.Context) ([]byte, error) {
	return f.GetSetting(ctx, "mcp_client_servers")
}
func (f *fakeSeedStore) PutMCPClientSettingsRaw(ctx context.Context, raw []byte) error {
	return f.PutSetting(ctx, "mcp_client_servers", raw)
}

func TestApplySeedIdempotentAcrossBoots(t *testing.T) {
	ctx := context.Background()
	st := &fakeSeedStore{kv: map[string][]byte{}}
	raw := `[{"name":"grafana","url":"http://gm.obs.svc:8000/mcp"}]`

	svc := NewService()
	defer svc.Close()
	added, err := svc.ApplySeed(ctx, st, raw, nil)
	if err != nil || len(added) != 1 {
		t.Fatalf("boot1 added=%v err=%v", added, err)
	}
	if !svc.Configured() {
		t.Fatal("live config not updated")
	}

	// Boot 2: nothing to do.
	added, err = svc.ApplySeed(ctx, st, raw, nil)
	if err != nil || len(added) != 0 {
		t.Fatalf("boot2 added=%v err=%v", added, err)
	}

	// Operator deletes the entry; boot 3 must not bring it back.
	_ = st.PutMCPClientSettingsRaw(ctx, []byte(`{}`))
	added, err = svc.ApplySeed(ctx, st, raw, nil)
	if err != nil || len(added) != 0 {
		t.Fatalf("boot3 added=%v err=%v", added, err)
	}
	var cur Settings
	_ = json.Unmarshal(st.kv["mcp_client_servers"], &cur)
	if len(cur.Servers) != 0 {
		t.Fatalf("deleted entry resurrected: %+v", cur.Servers)
	}
}
