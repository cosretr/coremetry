package api

// rollout_row_services_test.go — v0.10.1420 — operatör (/rollouts):
// "Deployment Workload basınca service'e girsin, şu an bulamıyor."
//
// NE ÇİVİLİYOR:
//   - workload → servis eşlemesi: eşleşme (ad = workload kazanır), belirsiz
//     (birden çok aday → deterministik alfabetik ilk, hepsi `services`te),
//     yok (alan boş → FE entity yedeğine düşer).
//   - Görünen TÜM satırlar için TEK toplu arama (N+1 yok), demetler tekil,
//     span-cluster değerleri birleşir; arama hatası satırları bozmaz.
//   - JSON: alanlar yalnız doluyken (v1 gövde eşliği korunur).
//   - v0.10.2232 (S2): satır → kanonik wl: id → resolver (serves).

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout"
)

func TestPickRolloutService(t *testing.T) {
	for _, c := range []struct {
		name, workload string
		svcs           []string
		want           string
	}{
		{"eşleşme: tek servis", "orders-api", []string{"orders-api"}, "orders-api"},
		{"eşleşme: ad = workload kazanır (alfabetik ilk değil)", "orders-api", []string{"billing", "orders-api", "orders-worker"}, "orders-api"},
		{"eşleşme: harf duyarsız", "Orders-API", []string{"audit", "orders-api"}, "orders-api"},
		{"tek aday, farklı ad", "orders-deploy", []string{"orders-api"}, "orders-api"},
		{"belirsiz: deterministik alfabetik ilk", "orders", []string{"orders-worker", "orders-api"}, "orders-api"},
		{"yok", "orders-api", nil, ""},
	} {
		if got := pickRolloutService(c.workload, c.svcs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAttachRolloutServicesBatchesOnce(t *testing.T) {
	t0 := time.Now().UTC().Truncate(time.Minute)
	row := func(cid, ns, wl, kind string) chstore.RolloutRow {
		return chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: cid, Namespace: ns, Workload: wl, Kind: kind, StartedAt: t0}}
	}
	rows := []chstore.RolloutRow{
		row("c-1", "team-a", "orders-api", "Deployment"),           // serves kenarı
		row("c-1", "team-a", "orders-api", "Deployment"),           // aynı id (tekilleşir)
		row("c-1", "team-a", "checkout", "Deployment"),             // iki servis
		row("c-1", "team-a", "batch-job", "CronJob"),               // kenar yok
		row("c-unmapped", "team-a", "orders-api", "Deployment"),    // cluster çözülmedi
		row("c-1", "team-a", "orders-api-7d9f8c6b5", "ReplicaSet"), // kanonik: Deployment orders-api
	}
	cidOf := func(id string) string {
		if id == "c-1" {
			return "c-1"
		}
		return ""
	}
	m := map[string][]string{
		"wl:c-1/team-a/Deployment/orders-api": {"orders-api"},
		"wl:c-1/team-a/Deployment/checkout":   {"checkout-web", "checkout-api"},
	}
	calls := 0
	var gotIDs []string
	lookup := func(_ context.Context, ids []string, at time.Time) (map[string][]string, error) {
		calls++
		gotIDs = ids
		if !at.IsZero() {
			t.Fatalf("liste \"şimdi\" sorar, at=%v", at)
		}
		return m, nil
	}
	if err := attachRolloutServicesWith(context.Background(), rows, cidOf, lookup); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || len(gotIDs) != 3 {
		t.Fatalf("tek toplu çağrı, 3 tekil id: calls=%d ids=%v", calls, gotIDs)
	}
	type want struct {
		svc  string
		svcs []string
	}
	for i, w := range []want{
		{"orders-api", []string{"orders-api"}},
		{"orders-api", []string{"orders-api"}},
		{"checkout-api", []string{"checkout-api", "checkout-web"}},
		{"", nil},
		{"", nil},
		{"orders-api", []string{"orders-api"}},
	} {
		if rows[i].Service != w.svc || !reflect.DeepEqual(rows[i].Services, w.svcs) {
			t.Errorf("satır %d (%s): service=%q services=%v, want %q %v", i, rows[i].Workload, rows[i].Service, rows[i].Services, w.svc, w.svcs)
		}
	}
	calls = 0
	_ = attachRolloutServicesWith(context.Background(), []chstore.RolloutRow{row("c-unmapped", "team-a", "x", "")}, cidOf, lookup)
	if calls != 0 {
		t.Fatal("çözülen cluster yokken çağrı atılmamalı")
	}
	fresh := []chstore.RolloutRow{row("c-1", "team-a", "orders-api", "Deployment")}
	err := attachRolloutServicesWith(context.Background(), fresh, cidOf, func(context.Context, []string, time.Time) (map[string][]string, error) {
		return nil, errors.New("ch down")
	})
	if err == nil || fresh[0].Service != "" || fresh[0].Services != nil {
		t.Fatalf("hata yolunda alan basılmamalı: err=%v %+v", err, fresh[0])
	}
}

func TestRolloutRowJSONServicesOnlyWhenSet(t *testing.T) {
	r := chstore.RolloutRow{Rollout: rollout.Rollout{ClusterID: "c-1", Namespace: "team-a", Workload: "orders-api"}}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), `"service`) {
		t.Fatalf("boş alanlar JSON'a girmemeli: %s", b)
	}
	r.Service, r.Services = "orders-api", []string{"orders-api", "orders-worker"}
	b, _ = json.Marshal(r)
	if !strings.Contains(string(b), `"service":"orders-api"`) || !strings.Contains(string(b), `"services":["orders-api","orders-worker"]`) {
		t.Fatalf("dolu alanlar JSON'da yok: %s", b)
	}
}
