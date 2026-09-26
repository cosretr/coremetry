package rollout

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// settings_test.go — v0.10.199 kelepçe + kalıcılık sözleşmesi (settings.go başlığı).

func res(interval, bucket time.Duration, thr int64, h, eh int, overlap, lookback time.Duration, weak bool, stalled time.Duration) Resolved {
	return Resolved{Interval: interval, Bucket: bucket, Threshold: thr, Hysteresis: h, ExitHysteresis: eh, OverlapMax: overlap, Lookback: lookback, WeakSignal: weak, StalledMin: stalled}
}

func TestResolvedClamps(t *testing.T) {
	f := false
	m, h := time.Minute, time.Hour
	cases := []struct {
		name string
		in   Settings
		want Resolved
	}{
		{"boş → varsayılanlar", Settings{}, res(m, 5*m, 10, 2, 6, 30*m, 6*h, true, 10*m)},
		{"alt sınırlar", Settings{Interval: "1s", Bucket: "10s", Threshold: -3, Hysteresis: 1, ExitHysteresis: 1, OverlapMax: "1s", Lookback: "1m", StalledMin: "1s"},
			res(30*time.Second, m, 10, 10, 30, 5*m, 2*h, true, 2*m)},
		{"üst sınırlar", Settings{Interval: "2h", Bucket: "3h", Threshold: 5_000_000, Hysteresis: 99, ExitHysteresis: 99, OverlapMax: "3d", Lookback: "9d", StalledMin: "1d"},
			res(15*m, 30*m, 1_000_000, 12, 24, 6*h, 48*h, true, 2*h)}, // EH 36 → 24: 4·EH·30m ≤ 48 sa
		{"kova izinli listeye aşağı oturur (7m → 5m)", Settings{Bucket: "7m"}, res(m, 5*m, 10, 2, 6, 30*m, 6*h, true, 10*m)},
		{"1 dk kova → H≥10, EH≥30, lookback ≥ 4·30·1m = 2 sa", Settings{Bucket: "1m"}, res(m, m, 10, 10, 30, 30*m, 6*h, true, 10*m)},
		{"1 dk kova, 48 sa lookback → 576 kova = 9 sa 36 dk", Settings{Bucket: "1m", Lookback: "48h"}, res(m, m, 10, 10, 30, 30*m, 576*m, true, 10*m)},
		{"10 dk kova, H=1 → taban 2 (bağ düşürmez), EH 3", Settings{Bucket: "10m", Hysteresis: 1, ExitHysteresis: 1}, res(m, 10*m, 10, 2, 3, 30*m, 6*h, true, 10*m)},
		{"15 dk kova, H=1 → 2, EH → 2", Settings{Bucket: "15m", Hysteresis: 1, ExitHysteresis: 2}, res(m, 15*m, 10, 2, 2, 30*m, 6*h, true, 10*m)},
		{"30 dk kova, EH=36 → lookback tavanı 48 sa EH'yi 24'e çeker", Settings{Bucket: "30m", ExitHysteresis: 36, Lookback: "1h"}, res(m, 30*m, 10, 2, 24, 30*m, 48*h, true, 10*m)},
		{"EH ayarsız (0) → varsayılan 6", Settings{Hysteresis: 3}, res(m, 5*m, 10, 3, 6, 30*m, 6*h, true, 10*m)},
		{"EH < H → H'ye çekilir", Settings{Hysteresis: 8, ExitHysteresis: 3}, res(m, 5*m, 10, 8, 8, 30*m, 6*h, true, 10*m)},
		{"overlap ≥ lookback/2 → lookback/2", Settings{OverlapMax: "5h", Lookback: "6h"}, res(m, 5*m, 10, 2, 6, 3*h, 6*h, true, 10*m)},
		{"zayıf sinyal kapatılabilir; 'd' birimi", Settings{WeakSignal: &f, Lookback: "1d"}, res(m, 5*m, 10, 2, 6, 30*m, 24*h, false, 10*m)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.in.Resolved()
			got.Enabled = false
			if got != tc.want {
				t.Fatalf("\n got %+v\nwant %+v", got, tc.want)
			}
			if time.Duration(got.Hysteresis)*got.Bucket < 10*time.Minute || time.Duration(got.ExitHysteresis)*got.Bucket < 30*time.Minute ||
				got.ExitHysteresis < got.Hysteresis || got.Lookback < 4*time.Duration(got.ExitHysteresis)*got.Bucket || got.OverlapMax > got.Lookback/2 ||
				int(got.Lookback/got.Bucket) > 576 {
				t.Fatalf("bağlı kelepçe bozuk: %+v", got)
			}
			c := got.Config()
			if c.Bucket != got.Bucket || c.Hysteresis != got.Hysteresis || c.ExitHysteresis != got.ExitHysteresis || c.WeakSignal != got.WeakSignal || c.Threshold != got.Threshold || c.OverlapMax != got.OverlapMax {
				t.Fatalf("Config eşlemesi: %+v vs %+v", c, got)
			}
		})
	}
}

func TestWeakSignalFalsePersists(t *testing.T) {
	f := false
	st := &fakeSettingsStore{}
	s := NewSettingsService()
	cfg := DefaultSettings()
	cfg.WeakSignal = &f
	if err := s.SavePersisted(context.Background(), st, cfg); err != nil {
		t.Fatal(err)
	}
	o := NewSettingsService()
	if err := o.LoadPersisted(context.Background(), st); err != nil || o.Resolved().WeakSignal {
		t.Fatalf("WeakSignal=false blobda kalmalı: %s %+v", st.raw, o.Resolved())
	}
}

func TestValidateSettings(t *testing.T) {
	if err := ValidateSettings(Settings{}); err != nil {
		t.Fatalf("boş ayar geçerli: %v", err)
	}
	if err := ValidateSettings(Settings{Interval: "30s", Bucket: "5m", Lookback: "2d", StalledMin: "10m"}); err != nil {
		t.Fatalf("geçerli süreler: %v", err)
	}
	for _, bad := range []Settings{{Interval: "banana"}, {Bucket: "5 dakika"}, {OverlapMax: "-3m"}, {Lookback: "0s"}, {Threshold: -1}, {Hysteresis: -5}} {
		if ValidateSettings(bad) == nil {
			t.Fatalf("anlaşılmaz girdi 400 olmalı: %+v", bad)
		}
	}
}

func TestApplyLoadedKeepsNewer(t *testing.T) {
	cur := Settings{Enabled: true, UpdatedAt: 20}
	if got := applyLoaded(cur, Settings{Enabled: false, UpdatedAt: 10}); got.Enabled != true {
		t.Fatal("bayat blob canlı ayarı ezmemeli")
	}
	if got := applyLoaded(cur, Settings{Enabled: false, UpdatedAt: 30}); got.Enabled != false {
		t.Fatal("yeni blob alınmalı")
	}
	if got := applyLoaded(Settings{}, Settings{Enabled: true}); got.Enabled != true {
		t.Fatal("damgasız ilk yükleme (0 ≥ 0) alınmalı")
	}
}

type fakeSettingsStore struct {
	raw    []byte
	getErr error
	putErr error
	puts   int
}

func (f *fakeSettingsStore) GetRolloutSettingsRaw(context.Context) ([]byte, error) {
	return f.raw, f.getErr
}
func (f *fakeSettingsStore) PutRolloutSettingsRaw(_ context.Context, raw []byte) error {
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.raw = raw
	return nil
}

func TestPersistence(t *testing.T) {
	ctx := context.Background()
	s := NewSettingsService()
	st := &fakeSettingsStore{}
	if err := s.LoadPersisted(ctx, st); err != nil || s.Current().Enabled {
		t.Fatalf("boş blob varsayılanı korur: %v %+v", err, s.Current())
	}
	st.raw = []byte("{not json")
	if err := s.LoadPersisted(ctx, st); err == nil || s.Current().Interval != "60s" {
		t.Fatalf("bozuk JSON hata verir, ayarı EZMEZ: %v %+v", err, s.Current())
	}
	st.getErr = errors.New("ch down")
	if err := s.LoadPersisted(ctx, st); err == nil {
		t.Fatal("store hatası döner")
	}
	st.getErr = nil
	cfg := DefaultSettings()
	cfg.Enabled, cfg.Interval = true, "2m"
	if err := s.SavePersisted(ctx, st, cfg); err != nil || st.puts != 1 || !s.Current().Enabled || s.Current().UpdatedAt == 0 {
		t.Fatalf("kaydet: %v %+v", err, s.Current())
	}
	// ikinci pod aynı blobu okur → aynı ayar
	other := NewSettingsService()
	if err := other.LoadPersisted(ctx, st); err != nil || other.Current().Interval != "2m" || !other.Resolved().Enabled {
		t.Fatalf("öteki pod blobu almalı: %v %+v", err, other.Current())
	}
	// bayat blob (eski damga) canlı ayarı ezmez
	s.Configure(Settings{Enabled: false, UpdatedAt: s.Current().UpdatedAt + 1})
	if err := s.LoadPersisted(ctx, st); err != nil || s.Current().Enabled {
		t.Fatalf("bayat blob ezmemeli: %v %+v", err, s.Current())
	}
	st.putErr = errors.New("ro")
	if err := s.SavePersisted(ctx, st, cfg); err == nil || s.Current().Enabled {
		t.Fatal("put hatasında canlı ayar değişmez")
	}
	if (*SettingsService)(nil).LoadPersisted(ctx, st) != nil || s.LoadPersisted(ctx, nil) != nil {
		t.Fatal("nil güvenli")
	}
}

// ── v0.10.957 — Rollouts v2 P1.5 vidaları (docs/rollouts/v2-audit.md §10.7,
// §4.3, §4.4, §10.3.2; karar 8 ve 10). Eklemeli: v1 alanları ve Resolved
// DOKUNULMADI (TestResolvedClamps aynen geçer); v2 değerleri ResolvedV2'de.
// P1'de bunları OKUYAN YOK — yalnız saklanır, kelepçelenir, GET'te görünür.

func TestResolvedV2Defaults(t *testing.T) {
	want := V2Resolved{Source: SourceV1, DetectorInterval: 30 * time.Second, StuckAfter: 10 * time.Minute, IgnoreScale: true,
		Kinds: []string{"Deployment", "StatefulSet", "DaemonSet"}, InitialEvents: true,
		ObservedGenWaitTicks: 10, IncarnationAbsentTicks: 3, KnownRevisionsMax: 32}
	for name, in := range map[string]Settings{"boş blob": {}, "DefaultSettings": DefaultSettings()} {
		if got := in.ResolvedV2(); !v2Equal(got, want) {
			t.Fatalf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
	// v2 varsayılanları v1 çözümünü değiştirmez (DefaultSettings'e eklendiler).
	if DefaultSettings().Resolved() != (Settings{}).Resolved() {
		t.Fatal("DefaultSettings v1 Resolved'ı değiştirdi")
	}
}

// v2Equal — V2Resolved dilim taşır (== yok); Kinds sırası da sözleşme.
func v2Equal(a, b V2Resolved) bool { return reflect.DeepEqual(a, b) }

func TestResolvedV2Clamps(t *testing.T) {
	f, tr := false, true
	d := DefaultSettings().ResolvedV2()
	mod := func(fn func(*V2Resolved)) V2Resolved {
		r := d
		r.Kinds = append([]string(nil), d.Kinds...)
		fn(&r)
		return r
	}
	cases := []struct {
		name string
		in   Settings
		want V2Resolved
	}{
		{"source v2 (büyük harf/boşluk)", Settings{Source: " V2 "}, mod(func(r *V2Resolved) { r.Source = SourceV2 })},
		{"bilinmeyen source → v1", Settings{Source: "v3"}, d},
		{"tik alt sınır 10 s", Settings{DetectorIntervalS: 3}, mod(func(r *V2Resolved) { r.DetectorInterval = 10 * time.Second })},
		{"tik üst sınır 300 s", Settings{DetectorIntervalS: 900}, mod(func(r *V2Resolved) { r.DetectorInterval = 300 * time.Second })},
		{"negatif tik → varsayılan", Settings{DetectorIntervalS: -1}, d},
		{"stuckAfter 'd' birimi → tavan 6 sa", Settings{StuckAfter: "2d"}, mod(func(r *V2Resolved) { r.StuckAfter = 6 * time.Hour })},
		{"stuckAfter 30s → taban 2 dk", Settings{StuckAfter: "30s"}, mod(func(r *V2Resolved) { r.StuckAfter = 2 * time.Minute })},
		{"stuckAfter 90m", Settings{StuckAfter: "90m"}, mod(func(r *V2Resolved) { r.StuckAfter = 90 * time.Minute })},
		{"stuckAfter çöp → varsayılan", Settings{StuckAfter: "banana"}, d},
		{"ignoreScale=false korunur", Settings{IgnoreScale: &f}, mod(func(r *V2Resolved) { r.IgnoreScale = false })},
		{"initialEvents=false korunur", Settings{InitialEvents: &f, IgnoreScale: &tr}, mod(func(r *V2Resolved) { r.InitialEvents = false })},
		{"kinds kanonik + tekil + sıralı (Deployment, StatefulSet, DaemonSet)",
			Settings{Kinds: []string{"daemonset", " Deployment ", "DAEMONSET"}},
			mod(func(r *V2Resolved) { r.Kinds = []string{"Deployment", "DaemonSet"} })},
		{"bilinmeyen tür atlanır; hiç geçerli yoksa varsayılan", Settings{Kinds: []string{"CronJob"}}, d},
		{"boş kinds listesi → varsayılan", Settings{Kinds: []string{}}, d},
		{"observedGenWaitTicks 1..120", Settings{ObservedGenWaitTicks: 500}, mod(func(r *V2Resolved) { r.ObservedGenWaitTicks = 120 })},
		{"incarnationAbsentTicks 1..60", Settings{IncarnationAbsentTicks: 99}, mod(func(r *V2Resolved) { r.IncarnationAbsentTicks = 60 })},
		// §4.4 durum (c): known_revisions revisionHistoryLimit'i (10) aşmalı → taban 11.
		{"knownRevisionsMax tabanı 11", Settings{KnownRevisionsMax: 5}, mod(func(r *V2Resolved) { r.KnownRevisionsMax = 11 })},
		{"knownRevisionsMax tavanı 256", Settings{KnownRevisionsMax: 1000}, mod(func(r *V2Resolved) { r.KnownRevisionsMax = 256 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.ResolvedV2(); !v2Equal(got, tc.want) {
				t.Fatalf("\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
	// Kinds dilimi çağrılar arasında paylaşılmaz.
	a := Settings{}.ResolvedV2()
	a.Kinds[0] = "X"
	if (Settings{}).ResolvedV2().Kinds[0] != "Deployment" {
		t.Fatal("varsayılan kinds dilimi paylaşılıyor")
	}
}

func TestValidateSettingsV2(t *testing.T) {
	good := Settings{Source: "v2", DetectorIntervalS: 30, StuckAfter: "10m", Kinds: []string{"deployment", "StatefulSet"},
		ObservedGenWaitTicks: 10, IncarnationAbsentTicks: 3, KnownRevisionsMax: 32}
	if err := ValidateSettings(good); err != nil {
		t.Fatalf("geçerli v2 vidaları: %v", err)
	}
	for _, s := range []Settings{{Source: "v1"}, {Source: " V2 "}, {Kinds: []string{" DaemonSet "}}} {
		if err := ValidateSettings(s); err != nil {
			t.Fatalf("geçerli: %+v %v", s, err)
		}
	}
	for field, bad := range map[string]Settings{
		"source":                 {Source: "v3"},
		"stuckAfter":             {StuckAfter: "10 dakika"},
		"detectorIntervalS":      {DetectorIntervalS: -1},
		"observedGenWaitTicks":   {ObservedGenWaitTicks: -1},
		"incarnationAbsentTicks": {IncarnationAbsentTicks: -2},
		"knownRevisionsMax":      {KnownRevisionsMax: -3},
		"kinds[1]":               {Kinds: []string{"Deployment", "CronJob"}},
	} {
		err := ValidateSettings(bad)
		if err == nil || !strings.Contains(err.Error(), field) {
			t.Fatalf("%s 400 olmalı ve alanı adlandırmalı: %v", field, err)
		}
	}
}

// GET/PUT kalıcılık gidiş-dönüşü (SavePersisted → blob → öteki pod) ve eski
// (v1-only) blobun v2 varsayılanlarıyla yüklenmesi.
func TestV2FieldsRoundTripAndOldBlob(t *testing.T) {
	ctx := context.Background()
	f := false
	st := &fakeSettingsStore{}
	s := NewSettingsService()
	cfg := DefaultSettings()
	cfg.Enabled = true
	cfg.Source, cfg.DetectorIntervalS, cfg.StuckAfter = "v2", 45, "20m"
	cfg.IgnoreScale, cfg.InitialEvents = &f, &f
	cfg.Kinds = []string{"Deployment"}
	cfg.ObservedGenWaitTicks, cfg.IncarnationAbsentTicks, cfg.KnownRevisionsMax = 6, 4, 64
	if err := s.SavePersisted(ctx, st, cfg); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{`"source":"v2"`, `"detectorIntervalS":45`, `"stuckAfter":"20m"`, `"ignoreScale":false`, `"kinds":["Deployment"]`,
		`"initialEvents":false`, `"observedGenWaitTicks":6`, `"incarnationAbsentTicks":4`, `"knownRevisionsMax":64`} {
		if !strings.Contains(string(st.raw), k) {
			t.Fatalf("blob %s taşımalı: %s", k, st.raw)
		}
	}
	other := NewSettingsService()
	if err := other.LoadPersisted(ctx, st); err != nil {
		t.Fatal(err)
	}
	want := V2Resolved{Source: SourceV2, DetectorInterval: 45 * time.Second, StuckAfter: 20 * time.Minute, IgnoreScale: false,
		Kinds: []string{"Deployment"}, InitialEvents: false, ObservedGenWaitTicks: 6, IncarnationAbsentTicks: 4, KnownRevisionsMax: 64}
	if got := other.ResolvedV2(); !v2Equal(got, want) {
		t.Fatalf("öteki pod:\n got %+v\nwant %+v", got, want)
	}
	// v1 alanları gidiş-dönüşte bozulmadı.
	if other.Resolved() != s.Resolved() || !other.Resolved().Enabled {
		t.Fatalf("v1 çözümü değişti: %+v vs %+v", other.Resolved(), s.Resolved())
	}

	// v0.10.199–956 blobu: v2 alanı YOK → v2 varsayılanları.
	old := &fakeSettingsStore{raw: []byte(`{"enabled":true,"interval":"60s","bucket":"5m","threshold":10,"hysteresis":2,"exitHysteresis":6,"overlapMax":"30m","lookback":"6h","stalledMin":"10m","updatedAt":5}`)}
	o := NewSettingsService()
	if err := o.LoadPersisted(ctx, old); err != nil {
		t.Fatal(err)
	}
	if got := o.ResolvedV2(); !v2Equal(got, DefaultSettings().ResolvedV2()) || !o.Resolved().Enabled {
		t.Fatalf("eski blob v2 varsayılanlarıyla yüklenmeli: %+v", got)
	}
}
