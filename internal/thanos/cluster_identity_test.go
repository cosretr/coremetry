package thanos

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// v0.10.128 — K8s entity katmanı AŞAMA 3 adım 2: Remote Cluster kaydı
// entity hiyerarşisinin KÖKÜ olur (docs/plans/entity-layer-design-2026-08-28.md §1.1).
//
// Sözleşme:
//   - ID opak ve DEĞİŞMEZ; boşsa Name'den türetilir ("c-" + fnv64a(Name)
//     ilk 8 hex) — Name sonradan değişse de türetilmiş id kayıtta kalır
//     (BackfillClusterIDs bir kez yazar).
//   - Thanos etiketi: ad BOŞ = matcher YOK = eski davranış (cluster başına
//     URL modeli bozulmaz). Ad doluysa değer boşken Name kullanılır.
//   - Span cluster değeri boşsa Name (bugünkü join anahtarı).

func TestDerivedClusterIDIsStableAndOpaque(t *testing.T) {
	a := derivedClusterID("ocp-prod-1")
	b := derivedClusterID("ocp-prod-1")
	c := derivedClusterID("ocp-prod-2")
	if a != b {
		t.Fatalf("türetilmiş id kararlı olmalı: %s != %s", a, b)
	}
	if a == c {
		t.Fatalf("farklı adlar farklı id vermeli: %s", a)
	}
	if !regexp.MustCompile(`^c-[0-9a-f]{8}$`).MatchString(a) {
		t.Fatalf("id biçimi c-<8 hex> olmalı, alınan %q", a)
	}
	if derivedClusterID("") != "" {
		t.Fatal("boş ad boş id vermeli (kayıt zaten reddedilir)")
	}
}

func TestClusterConfigEffectiveValues(t *testing.T) {
	base := ClusterConfig{Name: "ocp-prod-1", URL: "https://thanos", Enabled: true}
	if got := base.EffectiveID(); got != derivedClusterID("ocp-prod-1") {
		t.Fatalf("ID boşken türetilmiş id dönmeli, alınan %q", got)
	}
	withID := base
	withID.ID = "c-deadbeef"
	if got := withID.EffectiveID(); got != "c-deadbeef" {
		t.Fatalf("kayıtlı ID kazanmalı, alınan %q", got)
	}
	// Thanos etiketi: ad boş → matcher yok.
	if name, val := base.EffectiveThanosLabel(); name != "" || val != "" {
		t.Fatalf("etiket adı boşken matcher olmamalı, alınan %q=%q", name, val)
	}
	lab := base
	lab.ThanosLabelName = "cluster"
	if name, val := lab.EffectiveThanosLabel(); name != "cluster" || val != "ocp-prod-1" {
		t.Fatalf("ad doluyken değer Name'e düşmeli, alınan %q=%q", name, val)
	}
	lab.ThanosLabelValue = "prod-1"
	if _, val := lab.EffectiveThanosLabel(); val != "prod-1" {
		t.Fatalf("açık değer kazanmalı, alınan %q", val)
	}
	if got := base.SpanClusterKey(); got != "ocp-prod-1" {
		t.Fatalf("span cluster değeri boşken Name, alınan %q", got)
	}
	sp := base
	sp.SpanClusterValue = "ocp-prod-1-spans"
	if got := sp.SpanClusterKey(); got != "ocp-prod-1-spans" {
		t.Fatalf("açık span değeri kazanmalı, alınan %q", got)
	}
}

func TestBackfillClusterIDs(t *testing.T) {
	cfg := Settings{Clusters: []ClusterConfig{
		{Name: "a", URL: "https://a", Enabled: true},
		{Name: "b", URL: "https://b", ID: "c-00000001"},
		{Name: "", URL: "https://x"}, // adı boş kayıt: id üretilmez, dokunulmaz
	}}
	out, changed := BackfillClusterIDs(cfg)
	if !changed {
		t.Fatal("boş id doldurulduğunda changed=true olmalı")
	}
	if out.Clusters[0].ID != derivedClusterID("a") {
		t.Fatalf("a için türetilmiş id beklenir, alınan %q", out.Clusters[0].ID)
	}
	if out.Clusters[1].ID != "c-00000001" {
		t.Fatalf("mevcut id korunmalı, alınan %q", out.Clusters[1].ID)
	}
	if out.Clusters[2].ID != "" {
		t.Fatalf("adsız kayda id yazılmamalı, alınan %q", out.Clusters[2].ID)
	}
	// Girdi DEĞİŞTİRİLMEZ (tam-blob yazım çağıranın işi).
	if cfg.Clusters[0].ID != "" {
		t.Fatal("BackfillClusterIDs girdiyi yerinde değiştirmemeli")
	}
	// İkinci koşum: değişiklik yok.
	if _, again := BackfillClusterIDs(out); again {
		t.Fatal("dolu kayıtta ikinci koşum changed=false olmalı")
	}
}

func TestClusterByIDAndName(t *testing.T) {
	s := &Service{}
	s.Configure(Settings{Clusters: []ClusterConfig{
		{Name: "a", URL: "https://a", Enabled: true, ID: "c-aaaaaaaa"},
		{Name: "b", URL: "https://b", Enabled: false, ID: "c-bbbbbbbb"},
	}})
	if c, ok := s.ClusterByID("c-aaaaaaaa"); !ok || c.Name != "a" {
		t.Fatalf("id ile bulunmalı, alınan %+v %v", c, ok)
	}
	if _, ok := s.ClusterByID("c-bbbbbbbb"); ok {
		t.Fatal("kapalı kayıt id ile de dönmemeli (ClusterByName ile aynı sözleşme)")
	}
	// Geriye uyumluluk: ?cluster= eski URL'lerde Name taşır.
	if c, ok := s.ClusterByRef("a"); !ok || c.Name != "a" {
		t.Fatalf("ad ile de çözülmeli, alınan %+v %v", c, ok)
	}
	if c, ok := s.ClusterByRef("c-aaaaaaaa"); !ok || c.Name != "a" {
		t.Fatalf("id ile de çözülmeli, alınan %+v %v", c, ok)
	}
}

// PUT sözleşmesi: ID sunucu sahipli. İstemci gelen kayıt için (a) gönderdiği
// ID saklı bir kayda aitse o kayıt yeniden adlandırılıyordur → id VE token
// korunur; (b) aynı ad saklıysa saklı id (+ boş token yerine saklı token);
// (c) yeni kayıt → Name'den türetilmiş id (istemcinin uydurduğu id
// alınmaz — sunucu sahipli). Etiket adı geçersizse hata.
func TestReconcileClusterSettings(t *testing.T) {
	cur := Settings{Clusters: []ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "a", URL: "https://a", Token: "tok-a", Enabled: true},
		{ID: "c-bbbbbbbb", Name: "b", URL: "https://b", Token: "tok-b", Enabled: true},
	}}
	in := Settings{Clusters: []ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "a-renamed", URL: "https://a", Enabled: true},              // (a) yeniden adlandırma
		{Name: "b", URL: "https://b", Token: "", Enabled: true, ThanosLabelName: "cluster"}, // (b) aynı ad
		{ID: "c-uydurma1", Name: "n", URL: "https://n", Token: "tok-n", Enabled: true},      // (c) yeni
	}}
	out, err := ReconcileClusterSettings(in, cur)
	if err != nil {
		t.Fatal(err)
	}
	if out.Clusters[0].ID != "c-aaaaaaaa" || out.Clusters[0].Token != "tok-a" {
		t.Fatalf("yeniden adlandırma id+token korumalı: %+v", out.Clusters[0])
	}
	if out.Clusters[1].ID != "c-bbbbbbbb" || out.Clusters[1].Token != "tok-b" || out.Clusters[1].ThanosLabelName != "cluster" {
		t.Fatalf("aynı ad saklı id+token almalı, alanlar korunmalı: %+v", out.Clusters[1])
	}
	if out.Clusters[2].ID != derivedClusterID("n") || out.Clusters[2].Token != "tok-n" {
		t.Fatalf("yeni kayıt türetilmiş id almalı (istemci id'si alınmaz): %+v", out.Clusters[2])
	}
	bad := Settings{Clusters: []ClusterConfig{{Name: "x", URL: "https://x", ThanosLabelName: "not a label"}}}
	if _, err := ReconcileClusterSettings(bad, cur); err == nil {
		t.Fatal("geçersiz etiket adı reddedilmeli")
	}
	if ValidThanosLabelName("") != true || ValidThanosLabelName("cluster_id") != true || ValidThanosLabelName("9x") != false {
		t.Fatal("etiket adı doğrulaması: boş ok, cluster_id ok, 9x hayır")
	}
}

// v0.10.139 — otomatik eşleme brief'i: çoklu span değeri + teklik.
func TestSpanClusterKeysAndUniqueness(t *testing.T) {
	c := ClusterConfig{Name: "prod-eu", SpanClusterValue: "prod-eu-west", SpanClusterValues: []string{" prod-eu-west ", "prod-eu", ""}}
	if got := c.SpanClusterKeys(); len(got) != 2 || got[0] != "prod-eu-west" || got[1] != "prod-eu" {
		t.Fatalf("keys tekil+boşsuz olmalı: %v", got)
	}
	if !c.MatchesSpanCluster("prod-eu") || c.MatchesSpanCluster("prod-us") {
		t.Fatal("MatchesSpanCluster")
	}
	if got := (ClusterConfig{Name: "solo"}).SpanClusterKeys(); len(got) != 1 || got[0] != "solo" {
		t.Fatalf("değer yoksa Name: %v", got)
	}
	// Teklik: aynı değer iki kayıtta → hata + bağlı kayıt adı.
	in := Settings{Clusters: []ClusterConfig{
		{Name: "prod-eu", URL: "http://a", SpanClusterValues: []string{"prod-eu-west"}},
		{Name: "prod-us", URL: "http://b", SpanClusterValues: []string{"prod-us-east", "prod-eu-west"}},
	}}
	if _, err := ReconcileClusterSettings(in, Settings{}); err == nil || !strings.Contains(err.Error(), `"prod-eu"`) || !strings.Contains(err.Error(), "prod-eu-west") {
		t.Fatalf("çakışma reddedilmeli ve bağlı kaydı söylemeli: %v", err)
	}
	// Aynı etiket çifti iki kayıtta → hata.
	in2 := Settings{Clusters: []ClusterConfig{
		{Name: "a", URL: "http://q", ThanosLabelName: "cluster", ThanosLabelValue: "x"},
		{Name: "b", URL: "http://q", ThanosLabelName: "cluster", ThanosLabelValue: "x"},
	}}
	if _, err := ReconcileClusterSettings(in2, Settings{}); err == nil || !strings.Contains(err.Error(), "cluster=") {
		t.Fatalf("etiket çakışması reddedilmeli: %v", err)
	}
	// Geçerli çoklu değer: liste kanonikleşir, SpanClusterValue ilk eleman.
	ok := Settings{Clusters: []ClusterConfig{{Name: "prod-eu", URL: "http://a", SpanClusterValues: []string{"v2", "v1", "v2"}}}}
	out, err := ReconcileClusterSettings(ok, Settings{})
	if err != nil || out.Clusters[0].SpanClusterValue != "v2" || len(out.Clusters[0].SpanClusterValues) != 2 {
		t.Fatalf("kanonik liste: %+v %v", out.Clusters[0], err)
	}
	// Auto alanları korunur; etiket elle değişince manual'a düşer.
	cur := Settings{Clusters: []ClusterConfig{{ID: "c-1", Name: "prod-eu", URL: "http://a", ThanosLabelName: "cluster", ThanosLabelValue: "eu", ThanosLabelSource: "auto", ThanosLabelDetectedAt: 5}}}
	keep, _ := ReconcileClusterSettings(Settings{Clusters: []ClusterConfig{{ID: "c-1", Name: "prod-eu", URL: "http://a", ThanosLabelName: "cluster", ThanosLabelValue: "eu"}}}, cur)
	if keep.Clusters[0].ThanosLabelSource != "auto" || keep.Clusters[0].ThanosLabelDetectedAt != 5 {
		t.Fatalf("auto alanları korunmalı: %+v", keep.Clusters[0])
	}
	man, _ := ReconcileClusterSettings(Settings{Clusters: []ClusterConfig{{ID: "c-1", Name: "prod-eu", URL: "http://a", ThanosLabelName: "cluster", ThanosLabelValue: "eu2"}}}, cur)
	if man.Clusters[0].ThanosLabelSource != "manual" {
		t.Fatalf("elle değişen etiket manual olmalı: %+v", man.Clusters[0])
	}
}

// İnceleme (v0.10.139): Snapshot/form yalnız AÇIK değerleri gösterir; Name
// yedeği listeye yazılmaz — yeniden kaydetmek adı kalıcı değere çevirmez;
// değer boşken ad değişimi etkin matcher'ı değiştirir → auto düşer.
func TestExplicitValuesAndRenameKeepImplicitName(t *testing.T) {
	c := ClusterConfig{Name: "prod-eu"}
	if got := c.ExplicitSpanClusterValues(); len(got) != 0 {
		t.Fatalf("açık değer yok: %v", got)
	}
	out, err := ReconcileClusterSettings(Settings{Clusters: []ClusterConfig{{Name: "prod-eu", URL: "http://a"}}}, Settings{})
	if err != nil || out.Clusters[0].SpanClusterValue != "" || out.Clusters[0].SpanClusterValues != nil {
		t.Fatalf("Name yedeği listeye yazılmamalı: %+v %v", out.Clusters[0], err)
	}
	if got := out.Clusters[0].SpanClusterKeys(); len(got) != 1 || got[0] != "prod-eu" {
		t.Fatalf("etkin anahtar Name: %v", got)
	}
	cur := Settings{Clusters: []ClusterConfig{{ID: "c-1", Name: "prod-eu", URL: "http://a", ThanosLabelName: "cluster", ThanosLabelSource: "auto", ThanosLabelDetectedAt: 5}}}
	ren, _ := ReconcileClusterSettings(Settings{Clusters: []ClusterConfig{{ID: "c-1", Name: "prod-eu-2", URL: "http://a", ThanosLabelName: "cluster"}}}, cur)
	if ren.Clusters[0].ThanosLabelSource != "manual" {
		t.Fatalf("değer boşken ad değişimi matcher'ı değiştirir → manual: %+v", ren.Clusters[0])
	}
}

// v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2; karar 7):
// Remote Cluster kaydı `apiServerUrls` (liste, normalise), `argoSuffix`
// (tek, cluster'lar arası tekil, büyük/küçük harf duyarsız) ve `pairGroup`
// (serbest metin) taşır. P1'de hiçbir okuyucu yok; sözleşme yalnız YAZIMIN
// bu alanları kaybetmemesi:
//   - Eski istemci gövdesi (alan anahtarları YOK) saklı değerleri SİLMEZ —
//     id ile yeniden adlandırmada da, ada göre eşleşmede de taşınır.
//   - Yeni istemci `apiServerUrls` anahtarını HER ZAMAN gönderir (boşsa []);
//     anahtar varsa gövde yetkilidir: [] + "" alanları temizler.
//   - Reconcile idempotent: PUT'un otomatik algılama kancası ikinci kez
//     Reconcile çağırır; temizlenen alan geri GELMEZ.
//   - Diğer blob yazıcıları (detect, assign-span-cluster) saklı kaydı kopyalar;
//     alanlar aynen kalır.

const (
	rfURLA = "https://api.cluster-a.example.invalid:6443"
	rfURLB = "https://api.cluster-b.example.invalid:6443"
)

func rfCur() Settings {
	return Settings{Clusters: []ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "cluster-a", URL: "http://thanos-a.example.invalid", Token: "tok-a", Enabled: true,
			APIServerURLs: []string{rfURLA}, ArgoSuffix: "ca", PairGroup: "pair-1"},
		{ID: "c-bbbbbbbb", Name: "cluster-b", URL: "http://thanos-b.example.invalid", Enabled: true,
			APIServerURLs: []string{rfURLB}, ArgoSuffix: "cb", PairGroup: "pair-1"},
	}}
}

func rfAssertKept(t *testing.T, what string, got ClusterConfig, want ClusterConfig) {
	t.Helper()
	if strings.Join(got.APIServerURLs, ",") != strings.Join(want.APIServerURLs, ",") ||
		got.ArgoSuffix != want.ArgoSuffix || got.PairGroup != want.PairGroup {
		t.Fatalf("%s: rollout alanları korunmalı — alınan urls=%v suffix=%q pair=%q, beklenen urls=%v suffix=%q pair=%q",
			what, got.APIServerURLs, got.ArgoSuffix, got.PairGroup, want.APIServerURLs, want.ArgoSuffix, want.PairGroup)
	}
}

func TestReconcileCarriesRolloutFieldsForward(t *testing.T) {
	cur := rfCur()

	// (1) Eski istemci: gövdede üç anahtar da YOK (v0.10.956 öncesi form ya da
	// önbellekte kalmış eski bundle). Biri id ile yeniden adlandırılıyor, öteki
	// yalnız adla eşleşiyor.
	oldBody := `{"clusters":[
		{"id":"c-aaaaaaaa","name":"cluster-a-renamed","url":"http://thanos-a.example.invalid","enabled":true},
		{"name":"cluster-b","url":"http://thanos-b.example.invalid","enabled":true}
	]}`
	var in Settings
	if err := json.Unmarshal([]byte(oldBody), &in); err != nil {
		t.Fatal(err)
	}
	out, err := ReconcileClusterSettings(in, cur)
	if err != nil {
		t.Fatal(err)
	}
	rfAssertKept(t, "eski istemci + id ile yeniden adlandırma", out.Clusters[0], cur.Clusters[0])
	rfAssertKept(t, "eski istemci + ad eşleşmesi", out.Clusters[1], cur.Clusters[1])
	if out.Clusters[0].Token != "tok-a" || out.Clusters[0].ID != "c-aaaaaaaa" {
		t.Fatalf("mevcut id/token birleştirmesi bozulmamalı: %+v", out.Clusters[0])
	}

	// (2) Yeni istemci AÇIKÇA temizliyor: apiServerUrls [] + boş metinler.
	clearBody := `{"clusters":[
		{"id":"c-aaaaaaaa","name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true,"apiServerUrls":[],"argoSuffix":"","pairGroup":""},
		{"id":"c-bbbbbbbb","name":"cluster-b","url":"http://thanos-b.example.invalid","enabled":true,"apiServerUrls":["` + rfURLB + `"],"argoSuffix":"cb","pairGroup":"pair-1"}
	]}`
	var clr Settings
	if err := json.Unmarshal([]byte(clearBody), &clr); err != nil {
		t.Fatal(err)
	}
	cleared, err := ReconcileClusterSettings(clr, cur)
	if err != nil {
		t.Fatal(err)
	}
	if c := cleared.Clusters[0]; len(c.APIServerURLs) != 0 || c.ArgoSuffix != "" || c.PairGroup != "" {
		t.Fatalf("açık temizleme uygulanmalı: %+v", c)
	}
	// (3) İdempotent: PUT kancası (autoDetectNewClusterLabels) sonucu yeniden
	// Reconcile eder — temizlenen alan saklı değerden GERİ GELMEMELİ.
	again, err := ReconcileClusterSettings(cleared, cur)
	if err != nil {
		t.Fatal(err)
	}
	if c := again.Clusters[0]; len(c.APIServerURLs) != 0 || c.ArgoSuffix != "" || c.PairGroup != "" {
		t.Fatalf("ikinci Reconcile temizlenen alanı geri getirmemeli: %+v", c)
	}
	rfAssertKept(t, "ikinci Reconcile, dokunulmayan kayıt", again.Clusters[1], cur.Clusters[1])
	// Blob biçimi: boş liste/metin yazılmaz (omitempty) — eski pod'un blobu aynı kalır.
	raw, _ := json.Marshal(again.Clusters[0])
	for _, k := range []string{"apiServerUrls", "argoSuffix", "pairGroup"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Fatalf("boş %s blob'a yazılmamalı: %s", k, raw)
		}
	}

	// (4) Kısmi betik gövdesi: yalnız argoSuffix değişiyor, liste anahtarı yok →
	// metin uygulanır, liste + pairGroup saklı değerden taşınır.
	part := Settings{Clusters: []ClusterConfig{
		{ID: "c-aaaaaaaa", Name: "cluster-a", URL: "http://thanos-a.example.invalid", Enabled: true, ArgoSuffix: "ca2"},
		cur.Clusters[1],
	}}
	po, err := ReconcileClusterSettings(part, cur)
	if err != nil {
		t.Fatal(err)
	}
	if c := po.Clusters[0]; c.ArgoSuffix != "ca2" || strings.Join(c.APIServerURLs, ",") != rfURLA || c.PairGroup != "pair-1" {
		t.Fatalf("kısmi gövde: suffix uygulanmalı, liste+pair taşınmalı: %+v", c)
	}

	// (5) Yeni değerler normalise edilir: harf, sondaki /, varsayılan port,
	// boş satır ve normalize sonrası tekrar atılır; metinler kırpılır.
	set := Settings{Clusters: []ClusterConfig{{
		Name: "cluster-c", URL: "http://thanos-c.example.invalid", Enabled: true,
		APIServerURLs: []string{" HTTPS://API.Cluster-C.example.invalid/ ", "", "https://api.cluster-c.example.invalid:6443", "https://API-int.cluster-c.example.invalid"},
		ArgoSuffix:    "  cc ", PairGroup: "  pair 2 ",
	}}}
	so, err := ReconcileClusterSettings(set, cur)
	if err != nil {
		t.Fatal(err)
	}
	c := so.Clusters[0]
	if strings.Join(c.APIServerURLs, ",") != "https://api.cluster-c.example.invalid:6443,https://api-int.cluster-c.example.invalid:6443" ||
		c.ArgoSuffix != "cc" || c.PairGroup != "pair 2" {
		t.Fatalf("normalise + tekilleştirme + kırpma: %+v", c)
	}

	// (6) Diğer blob yazıcıları: detect (ApplyDetection) ve assign-span-cluster
	// (saklı kaydı kopyalayıp span değeri ekler) → Reconcile; alanlar kalır.
	det, err := ApplyDetection(cur.Clusters[0], Detection{Label: "cluster", Value: "cluster-a", Series: 3}, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	rfAssertKept(t, "ApplyDetection", det, cur.Clusters[0])
	dn := Settings{Clusters: append([]ClusterConfig(nil), cur.Clusters...)}
	dn.Clusters[0] = det
	do, err := ReconcileClusterSettings(dn, cur)
	if err != nil {
		t.Fatal(err)
	}
	rfAssertKept(t, "detect → Reconcile", do.Clusters[0], cur.Clusters[0])
	an := Settings{Clusters: append([]ClusterConfig(nil), cur.Clusters...)}
	an.Clusters[1].SpanClusterValues = []string{"cluster-b", "prod-cluster-b"}
	ao, err := ReconcileClusterSettings(an, cur)
	if err != nil {
		t.Fatal(err)
	}
	rfAssertKept(t, "assign-span-cluster → Reconcile", ao.Clusters[1], cur.Clusters[1])
}

// Karışık sürüm: yeni pod'un yazdığı blob JSON'dan aynen geri okunur; eski
// pod'un (alan bilmeyen) blobu hatasız sıfır değerlerle yüklenir.
func TestRolloutFieldsBlobRoundTrip(t *testing.T) {
	raw, err := json.Marshal(rfCur())
	if err != nil {
		t.Fatal(err)
	}
	var back Settings
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	for i := range back.Clusters {
		rfAssertKept(t, "blob gidiş-dönüş", back.Clusters[i], rfCur().Clusters[i])
	}
	var old Settings
	if err := json.Unmarshal([]byte(`{"clusters":[{"id":"c-aaaaaaaa","name":"cluster-a","url":"http://thanos-a.example.invalid","enabled":true}]}`), &old); err != nil {
		t.Fatal(err)
	}
	if c := old.Clusters[0]; c.APIServerURLs != nil || c.ArgoSuffix != "" || c.PairGroup != "" {
		t.Fatalf("eski blob sıfır değerlerle yüklenmeli: %+v", c)
	}
}

func TestRolloutFieldsValidationAndUniqueness(t *testing.T) {
	mk := func(cs ...ClusterConfig) Settings { return Settings{Clusters: cs} }
	a := func(mut func(*ClusterConfig)) ClusterConfig {
		c := ClusterConfig{Name: "cluster-a", URL: "http://thanos-a.example.invalid", Enabled: true}
		mut(&c)
		return c
	}
	b := func(mut func(*ClusterConfig)) ClusterConfig {
		c := ClusterConfig{Name: "cluster-b", URL: "http://thanos-b.example.invalid", Enabled: true}
		mut(&c)
		return c
	}
	long := strings.Repeat("x", 64)
	cases := []struct {
		name    string
		in      Settings
		wantErr []string // hepsi hata metninde geçmeli; nil = hata beklenmez
	}{
		{"geçersiz URL kayıt adı ve konumla", mk(a(func(c *ClusterConfig) { c.APIServerURLs = []string{rfURLA, "ftp://x.example.invalid"} })),
			[]string{"cluster-a", "apiServerUrls", "#2"}},
		{"userinfo parolası yankılanmaz", mk(a(func(c *ClusterConfig) { c.APIServerURLs = []string{"https://u:hunter2@api.cluster-a.example.invalid"} })),
			[]string{"cluster-a", "apiServerUrls"}},
		{"aynı URL farklı yazımla iki kayıtta", mk(
			a(func(c *ClusterConfig) { c.APIServerURLs = []string{rfURLA} }),
			b(func(c *ClusterConfig) { c.APIServerURLs = []string{"HTTPS://api.cluster-a.example.invalid/"} })),
			[]string{rfURLA, `"cluster-a"`}},
		{"argoSuffix büyük/küçük harf duyarsız tekil", mk(
			a(func(c *ClusterConfig) { c.ArgoSuffix = "ca" }),
			b(func(c *ClusterConfig) { c.ArgoSuffix = "CA" })),
			[]string{"argoSuffix", `"cluster-a"`}},
		// v0.10.956 — iki hub (audit §5.6): küme-içi adres HİÇBİR kayda
		// yazılmaz — her yazımı (…svc, :443, :6443, .cluster.local, sondaki /)
		// reddedilir; eşleme onu instance'ın hub'ına çözer (argocd hubs[]).
		{"küme-içi adres tek kayıtta da reddedilir", mk(
			a(func(c *ClusterConfig) { c.APIServerURLs = []string{InClusterAPIServerURL} })),
			[]string{"cluster-a", "apiServerUrls", "hub"}},
		{"küme-içi adres :443 yazımıyla reddedilir", mk(
			a(func(c *ClusterConfig) { c.APIServerURLs = []string{rfURLA, "https://kubernetes.default.svc:443"} })),
			[]string{"cluster-a", "hub"}},
		{"küme-içi adres FQDN yazımıyla reddedilir", mk(
			b(func(c *ClusterConfig) { c.APIServerURLs = []string{"https://kubernetes.default.svc.cluster.local/"} })),
			[]string{"cluster-b", "hub"}},
		{"iki hub'ın dış API adresleri ayrı kayıtlarda serbest", mk(
			a(func(c *ClusterConfig) { c.APIServerURLs = []string{rfURLA} }),
			b(func(c *ClusterConfig) { c.APIServerURLs = []string{rfURLB} })),
			nil},
		{"kapalı kayıt da tekillikte sayılır", mk(
			a(func(c *ClusterConfig) { c.ArgoSuffix = "ca" }),
			b(func(c *ClusterConfig) { c.ArgoSuffix = "ca"; c.Enabled = false })),
			[]string{"argoSuffix"}},
		{"argoSuffix boşluk içeremez", mk(a(func(c *ClusterConfig) { c.ArgoSuffix = "c a" })), []string{"cluster-a", "argoSuffix"}},
		{"argoSuffix tire ile başlayamaz", mk(a(func(c *ClusterConfig) { c.ArgoSuffix = "-ca" })), []string{"argoSuffix"}},
		{"argoSuffix en çok 63", mk(a(func(c *ClusterConfig) { c.ArgoSuffix = long })), []string{"argoSuffix"}},
		{"argoSuffix 63 karakter geçerli", mk(a(func(c *ClusterConfig) { c.ArgoSuffix = long[:63] })), nil},
		{"pairGroup en çok 64 karakter", mk(a(func(c *ClusterConfig) { c.PairGroup = long + "y" })), []string{"pairGroup"}},
		{"pairGroup kontrol karakteri içeremez", mk(a(func(c *ClusterConfig) { c.PairGroup = "pair\n1" })), []string{"pairGroup"}},
		{"pairGroup serbest metin (paylaşımlı)", mk(
			a(func(c *ClusterConfig) { c.PairGroup = "Aktif-aktif çift 1" }),
			b(func(c *ClusterConfig) { c.PairGroup = "Aktif-aktif çift 1" })),
			nil},
		{"apiServerUrls en çok 16", mk(a(func(c *ClusterConfig) {
			for i := 0; i < 17; i++ {
				c.APIServerURLs = append(c.APIServerURLs, "https://api"+strconv.Itoa(i)+".cluster-a.example.invalid")
			}
		})), []string{"apiServerUrls", "16"}},
	}
	for _, tc := range cases {
		_, err := ReconcileClusterSettings(tc.in, Settings{})
		if tc.wantErr == nil {
			if err != nil {
				t.Fatalf("%s: hata beklenmiyordu: %v", tc.name, err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: reddedilmeliydi", tc.name)
		}
		for _, w := range tc.wantErr {
			if !strings.Contains(err.Error(), w) {
				t.Fatalf("%s: hata %q içermeli, alınan %v", tc.name, w, err)
			}
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Fatalf("%s: hata metni parolayı sızdırmamalı: %v", tc.name, err)
		}
	}
}

// Snapshot (admin GET) üç alanı taşır; apiServerUrls HER ZAMAN dizi (boşsa
// []), böylece GET → PUT gidiş-dönüşü yetkili gövde sayılır ve temizleme
// betikten de yapılabilir.
func TestSnapshotCarriesRolloutFields(t *testing.T) {
	s := &Service{}
	cur := rfCur()
	cur.Clusters = append(cur.Clusters, ClusterConfig{ID: "c-cccccccc", Name: "cluster-c", URL: "http://thanos-c.example.invalid"})
	s.Configure(cur)
	snap := s.Snapshot()
	if len(snap.Clusters) != 3 {
		t.Fatalf("3 kayıt beklenir: %+v", snap.Clusters)
	}
	a := snap.Clusters[0]
	if strings.Join(a.APIServerURLs, ",") != rfURLA || a.ArgoSuffix != "ca" || a.PairGroup != "pair-1" {
		t.Fatalf("snapshot alanları taşımalı: %+v", a)
	}
	if snap.Clusters[2].APIServerURLs == nil {
		t.Fatal("alan yoksa da apiServerUrls boş dizi olmalı (nil değil)")
	}
	raw, _ := json.Marshal(snap.Clusters[2])
	if !strings.Contains(string(raw), `"apiServerUrls":[]`) {
		t.Fatalf("snapshot JSON'u boş listeyi [] olarak taşımalı: %s", raw)
	}
	// Snapshot üzerinden değişiklik saklı kaydı bozmaz (kopya).
	snap.Clusters[0].APIServerURLs[0] = "mutated"
	if s.CurrentSettings().Clusters[0].APIServerURLs[0] != rfURLA {
		t.Fatal("snapshot saklı listeyi paylaşmamalı (kopya olmalı)")
	}
}
