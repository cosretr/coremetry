package api

import (
	"strings"
	"testing"
	"time"
)

// v0.8.385 — env-separation Phase 2: /api/services + /api/endpoints
// consume the global ?env= picker. These tests pin the api-layer half
// of the slice:
//
//   • servicesUseMV — a non-empty env (like cluster before it,
//     v0.5.372) disqualifies the service_summary_5m fast path; the
//     read falls to the bounded raw-spans branch (KARAR KAYDI:
//     cluster-parity raw-fallback, NO MV changes);
//   • both list cache keys carry env (hash-ALL-inputs, the v0.5.187
//     class) — without it an env-filtered response would cross-poison
//     the unfiltered one inside the same 30s bucket.

func TestServicesUseMV_EnvDisqualifies(t *testing.T) {
	cases := []struct {
		name    string
		window  time.Duration
		cluster string
		env     string
		envMV   bool // v0.10.882 — service_env_summary_5m pencereyi kapsıyor
		want    bool
	}{
		{"wide window, no filters — MV", time.Hour, "", "", false, true},
		{"env set, env MV kapsamıyor — raw", time.Hour, "", "uat", false, false},
		{"cluster set, env MV kapsamıyor — raw", time.Hour, "prod-eu", "", false, false},
		{"both set, env MV kapsamıyor — raw", time.Hour, "prod-eu", "uat", false, false},
		{"env set, env MV KAPSIYOR — MV (v0.10.882)", time.Hour, "", "uat", true, true},
		{"both set, env MV KAPSIYOR — MV", time.Hour, "prod-eu", "uat", true, true},
		{"sub-5m window — raw even unfiltered", 2 * time.Minute, "", "", false, false},
		{"sub-5m window — raw even with env MV", 2 * time.Minute, "", "uat", true, false},
		{"exactly 5m — MV", 5 * time.Minute, "", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := servicesUseMV(tc.window, tc.cluster, tc.env, tc.envMV); got != tc.want {
				t.Fatalf("servicesUseMV(%v, %q, %q, %v) = %v, want %v",
					tc.window, tc.cluster, tc.env, tc.envMV, got, tc.want)
			}
		})
	}
}

func TestServicesListKey_CarriesEnv(t *testing.T) {
	key := func(env string) string {
		return servicesListKey(false, 50, 0, "b", "", "spanCount", "desc", "", "", "", env, "", false)
	}
	uat, prep, all := key("uat"), key("prep"), key("")
	if uat == prep || uat == all || prep == all {
		t.Fatalf("distinct envs must produce distinct keys: uat=%q prep=%q all=%q", uat, prep, all)
	}
	if !strings.Contains(uat, "env=uat") {
		t.Fatalf("key must carry the env value; got %q", uat)
	}
	// Same inputs → same key (stability half of the v0.5.187 contract).
	if key("uat") != uat {
		t.Fatal("servicesListKey must be deterministic")
	}
}

// v0.9.1039 — env(a): the /service Operations + bundle reads narrow by the
// global env picker, so their cache keys must hash env or an env-scoped
// response cross-poisons the all-env one inside the shared TTL (v0.5.187).
func TestSvcOpsCacheKey_CarriesEnv(t *testing.T) {
	key := func(env string) string {
		return svcOpsCacheKey("checkout", "1h", "", "", false, false, env)
	}
	uat, prep, all := key("uat"), key("prep"), key("")
	if uat == prep || uat == all || prep == all {
		t.Fatalf("distinct envs must produce distinct keys: uat=%q prep=%q all=%q", uat, prep, all)
	}
	if !strings.Contains(uat, "env=uat") {
		t.Fatalf("key must carry the env value; got %q", uat)
	}
	if key("uat") != uat {
		t.Fatal("svcOpsCacheKey must be deterministic")
	}
}

func TestEndpointsListKey_CarriesEnv(t *testing.T) {
	key := func(env string) string {
		return endpointsListKey("b", "", "", "", env, 500, false, false, "calls", "desc", "http")
	}
	uat, prep, all := key("uat"), key("prep"), key("")
	if uat == prep || uat == all || prep == all {
		t.Fatalf("distinct envs must produce distinct keys: uat=%q prep=%q all=%q", uat, prep, all)
	}
	if !strings.Contains(uat, "env=uat") {
		t.Fatalf("key must carry the env value; got %q", uat)
	}
	if key("uat") != uat {
		t.Fatal("endpointsListKey must be deterministic")
	}
}
