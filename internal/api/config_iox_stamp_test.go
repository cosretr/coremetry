package api

// config_iox_stamp_test.go — v0.10.969 — config import "replace" kipinde
// eski yedek canlıya geçmiyordu.
//
// Kök neden: importConfig system_settings satırlarını AYNEN yazar ve reload
// sinyali yayınlar; ama rollouts (applyLoaded), argocd (isStale) ve
// entity_layer (applyLoaded) servisleri blobun İÇİNDEKİ updatedAt canlı
// ayarınkinden küçükse yüklemeyi ATLAR (eski pod'un bayat blobu yeni admin
// PUT'unu ezmesin diye). Eski bir yedeğin updatedAt'i tanım gereği eskidir:
// satır CH'de kazanır (version = now), bellek onu reddeder — ayar ancak
// restart'ta (bellek varsayılana dönünce) uygulanıyordu.
//
// Düzeltme: replace kipinde bu anahtarların blobuna içe aktarma anı
// damgalanır (version ile AYNI an). Testler saf seam'e (importRowValues)
// ve servislerin DIŞA AÇIK yükleme yoluna karşı yazıldı; CH gerekmez.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/rollout"
)

// systemSettingsCols — store.go DDL'indeki system_settings kolon sırası
// (system.columns ORDER BY position ile aynı).
var systemSettingsCols = []configColumn{
	{"key", "String"},
	{"value", "String"},
	{"updated_at", "DateTime64(9)"},
	{"updated_by", "String"},
	{"version", "UInt64"},
}

// settingsExportRow — exportConfig'in ürettiği satır şekli (UseNumber ile
// çözülmüş: version json.Number).
func settingsExportRow(key, value string, version uint64) map[string]any {
	return map[string]any{
		"key":        key,
		"value":      value,
		"updated_at": "2026-01-02T03:04:05.000000006Z",
		"updated_by": "admin-synthetic",
		"version":    json.Number(uintStr(version)),
	}
}

func uintStr(v uint64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const (
	backupStamp = int64(1_767_323_045_000_000_000) // 2026-01-02 — yedeğin gömülü updatedAt'i
	importNow   = uint64(1_790_000_000_000_000_000)
)

// Replace kipinde korumalı anahtarın blobu import anıyla damgalanır; öteki
// her bayt aynen kalır, version = now.
func TestImportRowValuesStampsGuardedSettingsOnReplace(t *testing.T) {
	cases := []struct {
		key, in, want string
	}{
		{
			key:  "rollouts",
			in:   `{"enabled":false,"interval":"120s","bucket":"10m","updatedAt":1767323045000000000}`,
			want: `{"enabled":false,"interval":"120s","bucket":"10m","updatedAt":1790000000000000000}`,
		},
		{
			// iç içe nesnedeki updatedAt'e DOKUNULMAZ; girinti korunur
			key:  "argocd",
			in:   "{\n  \"enabled\": true,\n  \"instances\": [{\"id\": \"hub-a\", \"updatedAt\": 5}],\n  \"updatedAt\": 1767323045000000000\n}",
			want: "{\n  \"enabled\": true,\n  \"instances\": [{\"id\": \"hub-a\", \"updatedAt\": 5}],\n  \"updatedAt\": 1790000000000000000\n}",
		},
		{
			// omitempty: sıfır damgalı blobda alan yok → sona eklenir
			key:  "entity_layer",
			in:   `{"enabled":true,"syncInterval":"60s"}`,
			want: `{"enabled":true,"syncInterval":"60s","updatedAt":1790000000000000000}`,
		},
	}
	for _, c := range cases {
		vals, err := importRowValues("system_settings", systemSettingsCols, settingsExportRow(c.key, c.in, 42), true, importNow)
		if err != nil {
			t.Fatalf("%s: %v", c.key, err)
		}
		if got := vals[1]; got != c.want {
			t.Errorf("%s: value\n got %q\nwant %q\n(eski yedeğin updatedAt'i kalırsa canlı servis onu bayat sayıp atlar — restart'a dek uygulanmaz)", c.key, got, c.want)
		}
		if vals[0] != c.key {
			t.Errorf("%s: key kolonu değişti: %v", c.key, vals[0])
		}
		if vals[4] != importNow {
			t.Errorf("%s: replace kipinde version = now beklenir, got %v", c.key, vals[4])
		}
		if vals[3] != "admin-synthetic" {
			t.Errorf("%s: updated_by kolonu değişti: %v", c.key, vals[3])
		}
	}
}

// Merge kipi bugünkü sözleşmesini korur: blob da version da AYNEN.
func TestImportRowValuesMergeKeepsSettingsVerbatim(t *testing.T) {
	in := `{"enabled":false,"interval":"120s","updatedAt":1767323045000000000}`
	for _, key := range []string{"rollouts", "argocd", "entity_layer"} {
		vals, err := importRowValues("system_settings", systemSettingsCols, settingsExportRow(key, in, 42), false, importNow)
		if err != nil {
			t.Fatal(err)
		}
		if vals[1] != in {
			t.Errorf("%s: merge kipinde blob değişmemeli, got %q", key, vals[1])
		}
		if vals[4] != uint64(42) {
			t.Errorf("%s: merge kipinde version dosyadaki kalmalı, got %v", key, vals[4])
		}
	}
}

// updatedAt koruması OLMAYAN anahtarlar ve başka tablolar replace'te de
// aynen yazılır (yalnız version bump'ı).
func TestImportRowValuesLeavesUnguardedRowsVerbatim(t *testing.T) {
	in := `{"model":"synthetic-model","updatedAt":1767323045000000000}`
	vals, err := importRowValues("system_settings", systemSettingsCols, settingsExportRow("copilot", in, 42), true, importNow)
	if err != nil {
		t.Fatal(err)
	}
	if vals[1] != in {
		t.Errorf("korumasız anahtar damgalanmamalı, got %q", vals[1])
	}
	// Aynı kolon adları başka bir tabloda: dokunulmaz.
	vals, err = importRowValues("saved_views", systemSettingsCols, settingsExportRow("rollouts", in, 42), true, importNow)
	if err != nil {
		t.Fatal(err)
	}
	if vals[1] != in {
		t.Errorf("system_settings dışı tablo damgalanmamalı, got %q", vals[1])
	}
	// JSON nesnesi olmayan / bozuk blob: aynen (import bozuk veriyi onarmaz,
	// ama onu daha da bozmaz).
	for _, bad := range []string{`not-json`, `[1,2]`, `{"enabled":`, ``} {
		vals, err = importRowValues("system_settings", systemSettingsCols, settingsExportRow("rollouts", bad, 42), true, importNow)
		if err != nil {
			t.Fatal(err)
		}
		if vals[1] != bad {
			t.Errorf("geçersiz blob %q aynen yazılmalı, got %q", bad, vals[1])
		}
	}
}

// ── uçtan uca: servislerin DIŞA AÇIK yükleme yolu ────────────────────────

type fakeRolloutSettingsStore struct{ raw []byte }

func (f *fakeRolloutSettingsStore) GetRolloutSettingsRaw(context.Context) ([]byte, error) {
	return f.raw, nil
}
func (f *fakeRolloutSettingsStore) PutRolloutSettingsRaw(_ context.Context, raw []byte) error {
	f.raw = raw
	return nil
}

type fakeEntitySettingsStore struct{ raw []byte }

func (f *fakeEntitySettingsStore) GetEntitySettingsRaw(context.Context) ([]byte, error) {
	return f.raw, nil
}
func (f *fakeEntitySettingsStore) PutEntitySettingsRaw(_ context.Context, raw []byte) error {
	f.raw = raw
	return nil
}

// importedSettingsValue — replace kipinin CH'ye yazacağı value dizesi.
func importedSettingsValue(t *testing.T, key string, blob []byte, now uint64) string {
	t.Helper()
	vals, err := importRowValues("system_settings", systemSettingsCols, settingsExportRow(key, string(blob), 42), true, now)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := vals[1].(string)
	if !ok {
		t.Fatalf("value kolonu string değil: %T", vals[1])
	}
	return s
}

func TestImportedOldRolloutsBackupAppliesWithoutRestart(t *testing.T) {
	ctx := context.Background()
	store := &fakeRolloutSettingsStore{}
	svc := rollout.NewSettingsService()
	live := rollout.DefaultSettings()
	live.Enabled, live.Interval = true, "60s"
	if err := svc.SavePersisted(ctx, store, live); err != nil { // canlı damga ≈ şimdi
		t.Fatal(err)
	}

	backup := rollout.DefaultSettings()
	backup.Enabled, backup.Interval = false, "120s"
	backup.UpdatedAt = svc.Current().UpdatedAt - int64(time.Hour) // yedek canlıdan ESKİ
	blob, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}

	// Öncül (hatanın kendisi): blob aynen yazılırsa koruma onu atlar.
	store.raw = blob
	if err := svc.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if svc.Current().Interval != "60s" {
		t.Fatalf("öncül bozuldu: koruma eski blobu zaten alıyor (Interval=%q)", svc.Current().Interval)
	}

	now := uint64(time.Now().Add(time.Second).UnixNano())
	store.raw = []byte(importedSettingsValue(t, rollout.SettingsKey, blob, now))
	if err := svc.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	got := svc.Current()
	if got.Interval != "120s" || got.Enabled {
		t.Errorf("replace import sonrası yedek canlıya geçmedi: enabled=%v interval=%q (yedek: false/120s)", got.Enabled, got.Interval)
	}
	if got.UpdatedAt != int64(now) {
		t.Errorf("UpdatedAt = %d, import anı %d beklenir", got.UpdatedAt, now)
	}
}

func TestImportedOldEntityBackupAppliesWithoutRestart(t *testing.T) {
	ctx := context.Background()
	store := &fakeEntitySettingsStore{}
	svc := entity.NewSettingsService()
	live := entity.DefaultSettings()
	live.Enabled, live.SyncInterval = true, "60s"
	if err := svc.SavePersisted(ctx, store, live); err != nil {
		t.Fatal(err)
	}
	backup := entity.DefaultSettings()
	backup.Enabled, backup.SyncInterval = false, "5m"
	backup.UpdatedAt = svc.Current().UpdatedAt - int64(time.Hour)
	blob, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}

	store.raw = blob
	if err := svc.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if svc.Current().SyncInterval != "60s" {
		t.Fatalf("öncül bozuldu: koruma eski blobu zaten alıyor")
	}

	now := uint64(time.Now().Add(time.Second).UnixNano())
	store.raw = []byte(importedSettingsValue(t, entity.SettingsKey, blob, now))
	if err := svc.LoadPersisted(ctx, store); err != nil {
		t.Fatal(err)
	}
	if got := svc.Current(); got.SyncInterval != "5m" || got.Enabled || got.UpdatedAt != int64(now) {
		t.Errorf("replace import sonrası yedek canlıya geçmedi: %+v", got)
	}
}

// argocd'nin koruması (isStale) paket-içi; sözleşmesi saf karşılaştırma
// (loaded.UpdatedAt < cur.UpdatedAt). Damganın blobun KENDİ alanına, kendi
// tipinde çözüldüğü ve öteki alanların korunduğu yeterli kanıt.
func TestImportedArgoCDBackupCarriesImportStamp(t *testing.T) {
	backup := argocd.Settings{Enabled: true, UpdatedAt: backupStamp}
	blob, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	var got argocd.Settings
	if err := json.Unmarshal([]byte(importedSettingsValue(t, argocd.SettingsKey, blob, importNow)), &got); err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt != int64(importNow) {
		t.Errorf("argocd UpdatedAt = %d, import anı %d beklenir (isStale eski yedeği atlardı)", got.UpdatedAt, importNow)
	}
	if !got.Enabled {
		t.Error("damgalama öteki alanları bozdu (enabled düştü)")
	}
}

// ── stampJSONInt: bayt koruyan tarayıcı ─────────────────────────────────

func TestStampJSONInt(t *testing.T) {
	const n = int64(1790000000000000000)
	ok := []struct{ name, in, want string }{
		{"değiştir", `{"a":1,"updatedAt":5}`, `{"a":1,"updatedAt":1790000000000000000}`},
		{"ilk üye", `{"updatedAt":5,"a":"x"}`, `{"updatedAt":1790000000000000000,"a":"x"}`},
		{"yok → sona ekle", `{"a":1}`, `{"a":1,"updatedAt":1790000000000000000}`},
		{"boş nesne", `{}`, `{"updatedAt":1790000000000000000}`},
		{"boşluklu boş nesne", `{ }`, `{ "updatedAt":1790000000000000000}`},
		{"boşluklar korunur", `{ "updatedAt" : 12 , "a" : null }`, `{ "updatedAt" : 1790000000000000000 , "a" : null }`},
		{"sondaki satır sonu korunur", "{\"a\":1}\n", "{\"a\":1,\"updatedAt\":1790000000000000000}\n"},
		{"null değer", `{"updatedAt":null}`, `{"updatedAt":1790000000000000000}`},
		{"iç içe dokunulmaz", `{"x":{"updatedAt":1},"y":[{"updatedAt":2}]}`, `{"x":{"updatedAt":1},"y":[{"updatedAt":2}],"updatedAt":1790000000000000000}`},
		{"dizedeki sahte anahtar", `{"note":"a \"updatedAt\":1 }","b":false}`, `{"note":"a \"updatedAt\":1 }","b":false,"updatedAt":1790000000000000000}`},
		{"kaçışlı anahtar", `{"updatedAt":3}`, `{"updatedAt":1790000000000000000}`},
		{"büyük harf (json alan eşleşmesi)", `{"UpdatedAt":3}`, `{"UpdatedAt":1790000000000000000}`},
		{"yinelenen anahtarların hepsi", `{"updatedAt":1,"a":2,"updatedAt":3}`, `{"updatedAt":1790000000000000000,"a":2,"updatedAt":1790000000000000000}`},
		{"değer dize/nesne", `{"updatedAt":"x","z":{"k":[1,{"q":"}"}]}}`, `{"updatedAt":1790000000000000000,"z":{"k":[1,{"q":"}"}]}}`},
		{"unicode değer", `{"ad":"çğış","updatedAt":-1}`, `{"ad":"çğış","updatedAt":1790000000000000000}`},
	}
	for _, c := range ok {
		got, done := stampJSONInt(c.in, "updatedAt", n)
		if !done || got != c.want {
			t.Errorf("%s: stampJSONInt(%q)\n got %q ok=%v\nwant %q", c.name, c.in, got, done, c.want)
			continue
		}
		var probe struct {
			UpdatedAt int64 `json:"updatedAt"`
		}
		if err := json.Unmarshal([]byte(got), &probe); err != nil || probe.UpdatedAt != n {
			t.Errorf("%s: çıktı çözülünce updatedAt=%d err=%v", c.name, probe.UpdatedAt, err)
		}
	}
	for _, bad := range []string{``, `null`, `[1,2]`, `"s"`, `12`, `{"a":`, `{"a":1}x`, `not-json`} {
		if got, done := stampJSONInt(bad, "updatedAt", n); done || got != bad {
			t.Errorf("stampJSONInt(%q) = %q ok=%v; nesne olmayan/bozuk girdi aynen ve ok=false dönmeli", bad, got, done)
		}
	}
}

// ── kapsam: updatedAt koruması taşıyan HER ayar blobu damgalanıyor mu ────

// Yeni bir ayar servisi LoadPersisted'ına "gömülü updatedAt eskiyse atla"
// koruması eklerse, replace import o anahtarda da eski yedeği sessizce
// yutar. Tarama internal/<paket>/*.go'da korumayı (X.UpdatedAt <|>= Y.UpdatedAt)
// ve LoadPersisted'ı birlikte taşıyan paketleri bulur; paketin SettingsKey
// sabiti settingsImportStampKeys'te olmalı.
func TestSettingsImportStampCoversEveryUpdatedAtGuard(t *testing.T) {
	guard := regexp.MustCompile(`\b\w+(?:\.\w+)*\.UpdatedAt\s*(?:<|>=|<=|>)\s*\w+(?:\.\w+)*\.UpdatedAt\b`)
	keyRe := regexp.MustCompile(`(?m)^\s*const\s+SettingsKey\s*=\s*"([^"]+)"`)
	files, err := filepath.Glob("../*/*.go")
	if err != nil {
		t.Fatal(err)
	}
	type pkgInfo struct {
		guarded, loads bool
		key            string
		guardFile      string
	}
	pkgs := map[string]*pkgInfo{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := stripGoComments(string(raw))
		dir := filepath.Base(filepath.Dir(f))
		p := pkgs[dir]
		if p == nil {
			p = &pkgInfo{}
			pkgs[dir] = p
		}
		if guard.MatchString(src) {
			p.guarded, p.guardFile = true, f
		}
		if strings.Contains(src, ") LoadPersisted(") {
			p.loads = true
		}
		if m := keyRe.FindStringSubmatch(src); m != nil {
			p.key = m[1]
		}
	}
	found := 0
	for dir, p := range pkgs {
		if !p.guarded || !p.loads {
			continue
		}
		found++
		if p.key == "" {
			t.Errorf("%s: LoadPersisted updatedAt koruması taşıyor (%s) ama paket `const SettingsKey = \"…\"` bildirmiyor — "+
				"system_settings anahtarını settingsImportStampKeys'e elle ekle ve bu taramaya anahtarın yerini öğret", dir, p.guardFile)
			continue
		}
		if !settingsImportStampKeys[p.key] {
			t.Errorf("%s: %q anahtarı updatedAt korumalı (%s) ama settingsImportStampKeys'te yok — replace import eski yedeği "+
				"restart'a dek uygulamaz (v0.10.969)", dir, p.key, p.guardFile)
		}
	}
	// Kapsam tabanı: tarama hiçbir şey bulmazsa hiçbir şey kanıtlamadan geçer.
	if found < 3 {
		t.Fatalf("yalnız %d korumalı ayar paketi bulundu (rollout, argocd, entity beklenir) — tarama kapsamını yitirdi", found)
	}
	// Ters yön: listede korumasız anahtar kalmasın (koruma kalkarsa damga
	// gereksiz bayt değişikliğidir).
	guardedKeys := map[string]bool{}
	for _, p := range pkgs {
		if p.guarded && p.loads && p.key != "" {
			guardedKeys[p.key] = true
		}
	}
	for k := range settingsImportStampKeys {
		if !guardedKeys[k] {
			t.Errorf("settingsImportStampKeys'te %q var ama taramada bu anahtarın korumalı yükleyicisi bulunmadı", k)
		}
	}
}

// Damgalanan her blobun servisi import sonrası sinyal almalı — yoksa yedek
// ancak 30 s tazelemesinde canlıya geçer. entities v0.10.969'te eklendi.
func TestConfigImportReloadsEveryStampedService(t *testing.T) {
	topic := map[string]string{ // system_settings anahtarı → reload konusu (cache.go reloadConfigOnSignal)
		rollout.SettingsKey: "rollouts",
		argocd.SettingsKey:  "argocd",
		entity.SettingsKey:  "entities",
	}
	have := map[string]bool{}
	for _, s := range configImportReloadTopics {
		have[s] = true
	}
	for key := range settingsImportStampKeys {
		tp, ok := topic[key]
		if !ok {
			t.Errorf("%q damgalanıyor ama reload konusu bu testte eşlenmemiş — eşle ve configImportReloadTopics'e ekle", key)
			continue
		}
		if !have[tp] {
			t.Errorf("%q (anahtar %q) import sonrası yayınlanmıyor — damgalı yedek peer pod'lara 30 s gecikmeyle ulaşır", tp, key)
		}
	}
}

// configImportReloadTopics bir döngü değişkeniyle yayınlanır; literal
// publishConfigReload taraması (config_reload_test.go) onu görmez. Aynı
// sözleşme burada: her konunun reloadConfigOnSignal'da case'i olmalı, yoksa
// sinyal default:'a düşer (v0.9.237).
func TestConfigImportReloadTopicsHaveListeners(t *testing.T) {
	raw, err := os.ReadFile("cache.go")
	if err != nil {
		t.Fatal(err)
	}
	src := stripGoComments(string(raw))
	i := strings.Index(src, "func (s *Server) reloadConfigOnSignal")
	if i < 0 {
		t.Fatal("reloadConfigOnSignal cache.go'da bulunamadı")
	}
	body := src[i:]
	if len(configImportReloadTopics) < 8 {
		t.Fatalf("configImportReloadTopics yalnız %d konu — liste budanmış görünüyor", len(configImportReloadTopics))
	}
	for _, tp := range configImportReloadTopics {
		if !regexp.MustCompile(`case[^:\n]*"` + regexp.QuoteMeta(tp) + `"`).MatchString(body) {
			t.Errorf("import %q yayınlıyor ama reloadConfigOnSignal'da case yok — peer pod'lar 30 s poll'u bekler", tp)
		}
	}
}
