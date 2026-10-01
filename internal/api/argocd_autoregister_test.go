package api

// v0.10.1013 — Argo CD keşfedilen instance'ların kendiliğinden kaydı: tur,
// elle keşifle AYNI probe'u koşar ve sonucu taze bloba YALNIZ ekler. Anahtar
// kapalıyken ve elle keşif koşarken hiçbir istek / yazım yok; mevcut instance
// (tokenRef dahil) değişmez; ikinci tur yeniden yazmaz; pasif hub'a istek gitmez.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
)

func argoAutoRegSeed(t *testing.T, e *argoTestEnv, mut func(*argocd.Settings)) argocd.Settings {
	t.Helper()
	hub := argoClusterID(argoHubName)
	cfg := argocd.Settings{
		Enabled: true, AutoRegister: argocd.AutoRegisterSettings{Enabled: true},
		Hubs: []argocd.Hub{{ClusterID: hub}},
		Instances: []argocd.Instance{{ID: "prod-a", HubClusterID: hub, HubNamespace: "team-a-prod",
			MetricsJob: "team-a-prod-metrics", Enabled: false, TokenRef: "env:ARGO_PROD_A", Name: "Prod A"}},
	}
	if mut != nil {
		mut(&cfg)
	}
	if err := e.svc.SavePersisted(context.Background(), e.store, cfg); err != nil {
		t.Fatal(err)
	}
	return e.svc.Current()
}

func (e *argoTestEnv) reqCount() int {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	return len(e.fake.reqs)
}

func TestArgoCDAutoRegisterTick(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	before := argoAutoRegSeed(t, e, nil)
	puts := e.store.puts

	if n := e.s.ArgoCDAutoRegisterTick(context.Background()); n != 1 {
		t.Fatalf("yeni aday team-b-uat eklenmeli: %d", n)
	}
	cur := e.svc.Current()
	if len(cur.Instances) != 2 || e.store.puts != puts+1 || cur.UpdatedAt <= before.UpdatedAt {
		t.Fatalf("yazım: %d instance, %d put, updatedAt %d → %d", len(cur.Instances), e.store.puts-puts, before.UpdatedAt, cur.UpdatedAt)
	}
	// Mevcut instance AYNEN (devre dışı kalır, tokenRef ve ad korunur).
	if got := cur.Instances[0]; got != before.Instances[0] {
		t.Errorf("mevcut instance değişmemeli:\n got %+v\nwant %+v", got, before.Instances[0])
	}
	add := cur.Instances[1]
	if add.ID != "team-b-uat" || add.HubNamespace != "team-b-uat" || add.MetricsJob != "team-b-uat-metrics" || !add.Enabled || !add.Discovered || add.TokenRef != "" {
		t.Errorf("eklenen instance: %+v", add)
	}
	// Kalıcı blob da aynı (peer'lar bunu yükler).
	var stored argocd.Settings
	if err := json.Unmarshal(e.store.rows[argocd.SettingsKey], &stored); err != nil || len(stored.Instances) != 2 || !stored.AutoRegister.Enabled {
		t.Fatalf("kalıcı blob: %v %+v", err, stored)
	}
	// Denetim: sistem aktörü, eklenen kimlik.
	as := e.audits()
	if len(as) != 1 || as[0].Action != argocdAutoRegisterAction || as[0].ActorID != "system" || as[0].TargetID != argocd.SettingsKey {
		t.Fatalf("denetim: %+v", as)
	}
	var det struct {
		Added int      `json:"added"`
		IDs   []string `json:"ids"`
	}
	if err := json.Unmarshal([]byte(as[0].Details), &det); err != nil || det.Added != 1 || len(det.IDs) != 1 || det.IDs[0] != "team-b-uat" {
		t.Fatalf("denetim gövdesi: %v %s", err, as[0].Details)
	}

	// İkinci tur: aday artık kayıtlı → yazım yok.
	if n := e.s.ArgoCDAutoRegisterTick(context.Background()); n != 0 || e.store.puts != puts+1 || len(e.audits()) != 0 {
		t.Fatalf("ikinci tur yeniden yazmamalı: n=%d put=%d", n, e.store.puts-puts)
	}
}

func TestArgoCDAutoRegisterOffDoesNothing(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	for name, mut := range map[string]func(*argocd.Settings){
		"anahtar kapalı":     func(s *argocd.Settings) { s.AutoRegister.Enabled = false },
		"entegrasyon kapalı": func(s *argocd.Settings) { s.Enabled = false },
	} {
		argoAutoRegSeed(t, e, mut)
		puts, reqs := e.store.puts, e.reqCount()
		if n := e.s.ArgoCDAutoRegisterTick(context.Background()); n != 0 || e.store.puts != puts || e.reqCount() != reqs {
			t.Errorf("%s: istek / yazım olmamalı (n=%d, +%d put, +%d istek)", name, n, e.store.puts-puts, e.reqCount()-reqs)
		}
	}
	if len(e.audits()) != 0 {
		t.Error("kapalıyken denetim satırı olmamalı")
	}
}

func TestArgoCDAutoRegisterSkipsWhenBusyOrPassive(t *testing.T) {
	e := newArgoTestEnv(t)
	seedArgoFake(e.fake)
	argoAutoRegSeed(t, e, nil)
	puts, reqs := e.store.puts, e.reqCount()

	// Elle keşif koşuyor → tur atlanır, bayrak ELLE keşfin elinde kalır.
	argocdDiscoverBusy.Store(true)
	if n := e.s.ArgoCDAutoRegisterTick(context.Background()); n != 0 || e.reqCount() != reqs || e.store.puts != puts {
		t.Fatalf("meşgulken tur atlanmalı: n=%d", n)
	}
	if !argocdDiscoverBusy.Load() {
		t.Fatal("tur, başkasının meşguliyet bayrağını bırakmamalı")
	}
	argocdDiscoverBusy.Store(false)

	// Tek hub PASİF (Remote Cluster kaydı devre dışı) → istek gönderilmez.
	off := argoClusterID("cluster-off")
	cfg := e.svc.Current()
	cfg.Hubs = []argocd.Hub{{ClusterID: off}}
	cfg.Instances = nil
	e.svc.Configure(cfg)
	if n := e.s.ArgoCDAutoRegisterTick(context.Background()); n != 0 || e.reqCount() != reqs {
		t.Fatalf("pasif hub'a istek gitmemeli: n=%d +%d istek", n, e.reqCount()-reqs)
	}
	if argocdDiscoverBusy.Load() {
		t.Fatal("tur bitince meşguliyet bayrağı bırakılmalı")
	}
}

func TestArgoCDAutoRegisterDueAndDetails(t *testing.T) {
	now := time.Date(2026, 10, 1, 15, 0, 0, 0, time.UTC)
	if !argocdAutoRegisterDue(time.Time{}, now) || argocdAutoRegisterDue(now.Add(-10*time.Minute), now) || !argocdAutoRegisterDue(now.Add(-30*time.Minute), now) {
		t.Error("ilk tur hemen; sonrası 30 dk'da bir")
	}
	var res argocd.AutoRegisterResult
	for i := 0; i < argocdAutoRegisterAuditIDs+7; i++ {
		res.Added = append(res.Added, argocd.Instance{ID: "i"})
	}
	res.SkippedFull = 3
	var det struct {
		Added        int      `json:"added"`
		IDs          []string `json:"ids"`
		IDsTruncated bool     `json:"idsTruncated"`
		SkippedFull  int      `json:"skippedFull"`
		HubsProbed   int      `json:"hubsProbed"`
	}
	if err := json.Unmarshal([]byte(argocdAutoRegisterDetails(res, 2, 1, 42)), &det); err != nil {
		t.Fatal(err)
	}
	if det.Added != argocdAutoRegisterAuditIDs+7 || len(det.IDs) != argocdAutoRegisterAuditIDs || !det.IDsTruncated || det.SkippedFull != 3 || det.HubsProbed != 2 {
		t.Errorf("denetim gövdesi: %+v", det)
	}
}
