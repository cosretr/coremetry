package argocd

import (
	"fmt"
	"testing"
)

// v0.10.1013 — keşfedilen instance'ların kendiliğinden kaydı: yalnız EKLER,
// aday kuralı elle "Tümünü ekle" ile aynı, mevcut instance'a dokunmaz, tavanı
// aşmaz; anahtar entegrasyon kapalıyken saklanmaz.

func autoRegBlob() Settings {
	return Settings{
		Enabled: true, AutoRegister: AutoRegisterSettings{Enabled: true},
		Hubs: []Hub{{ClusterID: "c-aaaa0001"}, {ClusterID: "c-bbbb0002"}},
		Instances: []Instance{
			{ID: "team-a-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-a-prod", MetricsJob: "team-a-prod-metrics", Enabled: true, TokenRef: "env:A"},
			{ID: "team-b-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-b-prod", Enabled: false},
		},
	}
}

func TestAutoRegisterActive(t *testing.T) {
	s := autoRegBlob()
	if !AutoRegisterActive(s) {
		t.Fatal("açık + anahtar + hub → etkin")
	}
	off := s
	off.AutoRegister.Enabled = false
	noInt := s
	noInt.Enabled = false
	noHub := s
	noHub.Hubs = nil
	for name, c := range map[string]Settings{"anahtar kapalı": off, "entegrasyon kapalı": noInt, "hub yok": noHub, "varsayılan": DefaultSettings()} {
		if AutoRegisterActive(c) {
			t.Errorf("%s: etkin olmamalı", name)
		}
	}
}

func TestMergeAutoRegistered(t *testing.T) {
	cur := autoRegBlob()
	cands := []Candidate{
		// yeni, namespace'i belli → eklenir
		{ID: "team-c-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-c-prod", MetricsJob: "team-c-prod-metrics"},
		// durum C → appsAnyNamespace
		{ID: "team-d-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-d-prod", NamespaceCase: "C"},
		// ikinci hub'da aynı namespace → kimliği farklı, eklenir
		{ID: "team-a-prod-2", HubClusterID: "c-bbbb0002", HubNamespace: "team-a-prod"},
		// zaten kayıtlı (keşif eşledi) → atlanır
		{ID: "team-a-prod", HubClusterID: "c-aaaa0001", HubNamespace: "team-a-prod", ConfiguredID: "team-a-prod"},
		// namespace'i bilinmiyor (durum B, çok namespace) → atlanır
		{ID: "legacy-metrics", HubClusterID: "c-aaaa0001", MetricsJob: "legacy-metrics"},
		// hatalı aday → atlanır
		{ID: "broken", HubClusterID: "c-aaaa0001", HubNamespace: "broken", Error: "sorgu düştü"},
		// (hub, namespace) yuvası dolu ama keşif eşlememiş (iş adı farklı) → atlanır
		{ID: "team-b-prod-x", HubClusterID: "c-aaaa0001", HubNamespace: "team-b-prod", MetricsJob: "other-job"},
		// kimlik dolu (yarış: aynı kimlik bu arada alınmış) → atlanır
		{ID: "team-c-prod", HubClusterID: "c-bbbb0002", HubNamespace: "team-zzz"},
		// hub listede değil → atlanır
		{ID: "ghost", HubClusterID: "c-gone", HubNamespace: "ghost"},
	}
	out, res := MergeAutoRegistered(cur, cands)
	if len(res.Added) != 3 || res.SkippedOther != 4 || res.SkippedTaken != 2 || res.SkippedFull != 0 {
		t.Fatalf("döküm: eklenen %d, uygun değil %d, dolu yuva %d, tavan %d", len(res.Added), res.SkippedOther, res.SkippedTaken, res.SkippedFull)
	}
	if len(out.Instances) != 5 {
		t.Fatalf("instance sayısı: %d", len(out.Instances))
	}
	// Mevcutlar AYNEN (alan, sıra, etkinlik, tokenRef).
	if out.Instances[0] != cur.Instances[0] || out.Instances[1] != cur.Instances[1] {
		t.Fatalf("mevcut instance değişmemeli: %+v", out.Instances[:2])
	}
	c := out.Instances[2]
	if c.ID != "team-c-prod" || !c.Enabled || !c.Discovered || c.AppsAnyNamespace || c.TokenRef != "" || c.APIURL != "" || c.Name != "" || c.MetricsJob != "team-c-prod-metrics" {
		t.Errorf("eklenen satır elle eklenenle aynı şekilde olmalı: %+v", c)
	}
	if d := out.Instances[3]; !d.AppsAnyNamespace {
		t.Errorf("durum C → appsAnyNamespace: %+v", d)
	}
	if e := out.Instances[4]; e.ID != "team-a-prod-2" || e.HubClusterID != "c-bbbb0002" {
		t.Errorf("ikinci hub: %+v", e)
	}
	// Girdi blobu değişmez (dilim paylaşılmaz).
	if len(cur.Instances) != 2 {
		t.Fatal("girdi blobu değişti")
	}
	// Sonuç PUT ile aynı doğrulamadan geçer.
	if _, err := Validate(out, testClusters); err != nil {
		t.Fatalf("birleşen blob geçerli olmalı: %v", err)
	}
	// Uygun aday yoksa blob aynı.
	same, res2 := MergeAutoRegistered(cur, cands[3:6])
	if len(res2.Added) != 0 || len(same.Instances) != 2 {
		t.Fatalf("aday yokken ekleme olmamalı: %+v", res2)
	}
}

func TestMergeAutoRegisteredRespectsCap(t *testing.T) {
	cur := Settings{Enabled: true, Hubs: []Hub{{ClusterID: "c-aaaa0001"}}}
	for i := 0; i < maxInstances-2; i++ {
		cur.Instances = append(cur.Instances, Instance{ID: fmt.Sprintf("i-%d", i), HubClusterID: "c-aaaa0001", HubNamespace: fmt.Sprintf("ns-%d", i)})
	}
	var cands []Candidate
	for i := 0; i < 5; i++ {
		cands = append(cands, Candidate{ID: fmt.Sprintf("new-%d", i), HubClusterID: "c-aaaa0001", HubNamespace: fmt.Sprintf("new-ns-%d", i)})
	}
	out, res := MergeAutoRegistered(cur, cands)
	if len(out.Instances) != maxInstances || len(res.Added) != 2 || res.SkippedFull != 3 {
		t.Fatalf("tavan: %d instance, eklenen %d, tavan dışı %d", len(out.Instances), len(res.Added), res.SkippedFull)
	}
}

// Anahtar entegrasyona bağlı: kapalı entegrasyonda açık anahtar saklanmaz
// (yeniden açılış otomatik yazımı sessizce başlatmasın).
func TestValidateAutoRegisterFollowsEnabled(t *testing.T) {
	s := validBlob()
	s.AutoRegister.Enabled = true
	out, err := Validate(s, testClusters)
	if err != nil || !out.AutoRegister.Enabled {
		t.Fatalf("açık entegrasyonda anahtar korunmalı: %v %+v", err, out.AutoRegister)
	}
	s.Enabled = false
	s.Hubs, s.Instances, s.Pins = nil, nil, nil
	out, err = Validate(s, testClusters)
	if err != nil || out.AutoRegister.Enabled {
		t.Fatalf("kapalı entegrasyonda anahtar kapanmalı: %v %+v", err, out.AutoRegister)
	}
}
