package api

// cosre_briefing_test.go — CoSRE Login Briefing Faz 1 saf dikişleri (nötr fikstür).

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/cosre/briefing"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func TestBriefingTeamsFrom(t *testing.T) {
	ta := chstore.TeamAliases{Aliases: map[string]string{"Pay Team": "payments"}}
	got := briefingTeamsFrom([]chstore.User{
		{Email: "b@x", Team: "Payments"},
		{Email: "a@x", Team: "pay team"},
		{Email: "c@x", Team: "Orders"},
		{Email: "d@x", Team: "Ghost", Disabled: true},
		{Email: "e@x", Team: ""},
	}, ta)
	if len(got) != 2 || got[0].Key != "orders" || got[1].Key != "payments" || got[1].Label != "pay team" {
		t.Fatalf("%+v", got)
	}
}

func TestBriefingTrimServicesQ12(t *testing.T) {
	var svcs []string
	for i := 0; i < mcptools.MaxTeamServices+7; i++ {
		svcs = append(svcs, "svc")
	}
	kept, trimmed := briefingTrimServices(svcs)
	if len(kept) != mcptools.MaxTeamServices || trimmed != 7 {
		t.Fatalf("%d %d", len(kept), trimmed)
	}
}

func TestBriefingWindowFullBuckets(t *testing.T) {
	now := time.Date(2026, 10, 31, 10, 7, 30, 0, time.UTC)
	from, to := briefingWindow(now, 10800)
	if !to.Equal(time.Date(2026, 10, 31, 10, 5, 0, 0, time.UTC)) || to.Sub(from) != 3*time.Hour {
		t.Fatalf("%v %v", from, to)
	}
}

func TestRolloutCandidatesAndPreviousVersion(t *testing.T) {
	from := time.Unix(1000, 0)
	deps := []chstore.RecentDeployEntry{
		{Service: "orders-api", Version: "1.1", FirstSeenNs: 2000e9},
		{Service: "orders-api", Version: "1.2", FirstSeenNs: 3000e9},
		{Service: "payments-core", Version: "2.0", FirstSeenNs: 2500e9},
		{Service: "billing-api", Version: "9", FirstSeenNs: 4000e9},    // kapsam dışı
		{Service: "payments-core", Version: "1.9", FirstSeenNs: 500e9}, // pencere dışı
	}
	got := rolloutCandidates(deps, []string{"orders-api", "payments-core"}, from, 3)
	if len(got) != 2 || got[0].Version != "1.2" || got[1].Service != "payments-core" {
		t.Fatalf("%+v", got)
	}
	if len(rolloutCandidates(deps, []string{"orders-api", "payments-core"}, from, 1)) != 1 {
		t.Fatal("tavan")
	}
	prev, at, ok := previousVersion([]chstore.Deploy{{Version: "1.1", TimeUnixNs: 1}, {Version: "1.2", TimeUnixNs: 2}}, "1.2")
	if !ok || prev != "1.1" || at != 2 {
		t.Fatalf("%q %d %v", prev, at, ok)
	}
	if _, _, ok := previousVersion(nil, "1.2"); ok {
		t.Fatal("bulunmayan sürüm")
	}
}

func TestBriefingProblemRowsDisplayID(t *testing.T) {
	rows := briefingProblemRows([]mcptools.InboxRow{
		{ID: "problem:abc", Kind: "problem", Service: "orders-api", Priority: "P1"},
		{ID: "exception:f", Kind: "exception", Service: "orders-api"},
	})
	if rows[0].DisplayID != chstore.ProblemDisplayID("abc") || rows[1].DisplayID != "exception:f" {
		t.Fatalf("%+v", rows)
	}
}

func TestBriefingScopeResponse(t *testing.T) {
	if briefingScopeResponse(userScope{Reason: scopeReasonTeam, Team: "acme", Services: []string{"orders-api"}}, "", 600) != nil {
		t.Fatal("kapsamlı kullanıcı kartsız cevap almamalı")
	}
	for _, reason := range []string{scopeReasonNoTeam, scopeReasonNoServices, scopeReasonNoIdentity, scopeReasonDisabled} {
		r := briefingScopeResponse(userScope{Unscoped: true, Reason: reason}, "prod", 600)
		if r == nil || r.Status != briefingStatusUnscoped || r.Line != briefing.UnresolvedTeamLine || r.Briefing != nil {
			t.Errorf("%s: %+v", reason, r)
		}
	}
	if r := briefingScopeResponse(userScope{Unscoped: true, Reason: scopeReasonError}, "", 600); r.Status != briefingStatusUnavailable || r.Line != "" {
		t.Errorf("okuma hatası tek satır değil: %+v", r)
	}
}

func TestBriefingGetKeyHashesAllInputs(t *testing.T) {
	cfg := briefing.DefaultConfig().Normalize()
	now := time.Unix(1_800_000_000, 0)
	base := briefingGetKey("acme", []string{"orders-api"}, "prod", cfg, now)
	cfg2 := cfg
	cfg2.TTLS = 900
	cfg3 := cfg
	cfg3.WindowS = 3600
	for name, k := range map[string]string{
		"ttl":     briefingGetKey("acme", []string{"orders-api"}, "prod", cfg2, now),
		"pencere": briefingGetKey("acme", []string{"orders-api"}, "prod", cfg3, now),
		"env":     briefingGetKey("acme", []string{"orders-api"}, "", cfg, now),
		"küme":    briefingGetKey("acme", []string{"payments-core"}, "prod", cfg, now),
	} {
		if k == base {
			t.Errorf("%s anahtarı değiştirmedi", name)
		}
	}
	if !strings.HasPrefix(base, "briefing:v1:") {
		t.Fatal(base)
	}
	if !briefingEnvProduced(cfg, "prod") || !briefingEnvProduced(cfg, "") || briefingEnvProduced(cfg, "stage") {
		t.Fatal("env üretim kümesi")
	}
}
