package rollout

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/settingsdur"
)

// settings.go — ROLLOUTS özellik bayrağı + vidalar (system_settings["rollouts"]).
// v0.10.199 (Faz 2). Şablon internal/entity/settings.go: tek JSON blob, boot
// LoadPersisted, çok-pod 30 s poll, admin PUT → SavePersisted + canlı swap
// (uçlar v0.10.200). Varsayılan KAPALI: reconciler koşmaz; uçlar 404.
// Eşik gerekçeleri docs/audits/rollouts-audit.md §11; kelepçeler BAĞLI
// (inceleme: histerezis × kova ≥ 10 dk, kova saat böleni — CH ızgarası).

const SettingsKey = "rollouts"

type Settings struct {
	Enabled    bool   `json:"enabled"`
	Interval   string `json:"interval,omitempty"`   // reconciler tiki (60s)
	Bucket     string `json:"bucket,omitempty"`     // karar kovası (5m; düşük trafikte 10m) — 1m/5m/10m/15m/30m
	Threshold  int64  `json:"threshold,omitempty"`  // kovada aktif sayılmak için span (10)
	Hysteresis int    `json:"hysteresis,omitempty"` // GİRİŞ: ardışık kova (2)
	// ExitHysteresis — ÇIKIŞ: "çekildi" için ardışık inaktif kova (6 = 30 dk):
	// gece dalması / pod restart olay değildir (inceleme 2. tur).
	ExitHysteresis int    `json:"exitHysteresis,omitempty"`
	OverlapMax     string `json:"overlapMax,omitempty"` // çok-revizyonlu notu eşiği (30m)
	Lookback       string `json:"lookback,omitempty"`   // etkinlik penceresi (6h)
	// WeakSignal — "yeni revizyon son kovada eşik altında kaldı" notu (audit §11 vidası; nil = açık).
	WeakSignal *bool `json:"weakSignal,omitempty"`
	// StalledMin — Faz 5 (KSM): ready < desired bu süreden uzun → stalled (10m). Bugün yalnız saklanır/kelepçelenir.
	StalledMin string `json:"stalledMin,omitempty"`

	// v0.10.957 — Rollouts v2 P1.5 vidaları (docs/rollouts/v2-audit.md §10.7).
	// EKLEMELİ: v1 alanları ve Resolved aynen; v2 değerleri ResolvedV2'de.
	// P1'de bunları OKUYAN YOK (KSM dedektörü P2.1–P2.2; kaynak anahtarı
	// P2.3). Eski bloblar (alan yok) v2 varsayılanlarıyla yüklenir.
	//
	// Source — /api/rollouts* okuma kaynağı: "v1" (workload_rollouts,
	// varsayılan) | "v2" (rollout_events; P2.3'te bağlanır).
	Source string `json:"source,omitempty"`
	// DetectorIntervalS — KSM dedektör tiki, saniye (karar 8: 30; 10–300).
	DetectorIntervalS int `json:"detectorIntervalS,omitempty"`
	// StuckAfter — progressing → stuck zamanlayıcısı (§4.5; "10m", 2m–6h).
	StuckAfter string `json:"stuckAfter,omitempty"`
	// IgnoreScale — yalnız ölçek / yalnız annotation nesil artışı olay
	// değildir (§4.4 adım 4); nil = true.
	IgnoreScale *bool `json:"ignoreScale,omitempty"`
	// Kinds — izlenen iş yükü türleri (rollout_events.workload_kind yazımı;
	// büyük/küçük harf duyarsız girilir): Deployment, StatefulSet, DaemonSet.
	Kinds []string `json:"kinds,omitempty"`
	// InitialEvents — bootstrap'tan sonra İLK kez görülen iş yükü için
	// change_type='initial' olayı (karar 10); ilk-ever koşu yalnız baseline.
	// nil = true.
	InitialEvents *bool `json:"initialEvents,omitempty"`
	// ObservedGenWaitTicks — nesil artışından sonra observed_generation ≥
	// generation için en çok kaç tik beklenir (§4.4 adım 2; 10 = 30 s tikte
	// 5 dk: 1 dk'lık CMO scrape'i + controller gecikmesine geniş pay; 1–120).
	ObservedGenWaitTicks int `json:"observedGenWaitTicks,omitempty"`
	// IncarnationAbsentTicks — K: iş yükü ≥ K ardışık TAM okumada yoksa
	// yeniden görünüşü yeni incarnation'dır (§4.3; 3; 1–60).
	IncarnationAbsentTicks int `json:"incarnationAbsentTicks,omitempty"`
	// KnownRevisionsMax — incarnation başına akılda tutulan revizyon sınırı
	// (§10.3.2; 32). revisionHistoryLimit'i (vars. 10) AŞMALI ki GC'lenmiş
	// RS'nin geri dönüşü ROLLBACK görünsün (§4.4 durum c) → taban 11, tavan 256.
	KnownRevisionsMax int `json:"knownRevisionsMax,omitempty"`

	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// v0.10.957 — v2 kaynak değerleri.
const (
	SourceV1 = "v1"
	SourceV2 = "v2"
)

// v2Kinds — v0.10.957 — kanonik tür yazımı ve SIRASI (ResolvedV2 bu sırayla döner).
// Rollout (Argo Rollouts, karar 11) ve DeploymentConfig (karar 12) onaysız
// — P1'de kabul edilmez.
var v2Kinds = []string{"Deployment", "StatefulSet", "DaemonSet"}

// V2Resolved — v0.10.957 — v2 vidalarının UYGULANAN değerleri. Resolved'dan
// ayrı: Resolved karşılaştırılabilir kalsın (settings_test.go `!=`) ve v1
// reconciler'ın girdisi değişmesin.
type V2Resolved struct {
	Source                 string
	DetectorInterval       time.Duration
	StuckAfter             time.Duration
	IgnoreScale            bool
	Kinds                  []string
	InitialEvents          bool
	ObservedGenWaitTicks   int
	IncarnationAbsentTicks int
	KnownRevisionsMax      int
}

// clampInt — v0.10.957 — ≤0 → def (ayarsız / geçersiz), sonra [lo, hi].
func clampInt(v, def, lo, hi int) int {
	if v <= 0 {
		v = def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// canonicalV2Kind — v0.10.957 — büyük/küçük harf duyarsız eşleşme.
func canonicalV2Kind(k string) (string, bool) {
	k = strings.TrimSpace(k)
	for _, c := range v2Kinds {
		if strings.EqualFold(k, c) {
			return c, true
		}
	}
	return "", false
}

// ResolvedV2 — v0.10.957 — v2 kelepçeleri (tik 10–300 s, stuckAfter 2m–6h,
// observedGenWaitTicks 1–120, incarnationAbsentTicks 1–60, knownRevisionsMax
// 11–256); bilinmeyen source → v1; kinds kanonik/tekil/kanonik sırada,
// geçerli tür yoksa varsayılan üçlü; nil bool'lar → true.
func (s Settings) ResolvedV2() V2Resolved {
	d := DefaultSettings()
	r := V2Resolved{
		Source:                 SourceV1,
		DetectorInterval:       time.Duration(clampInt(s.DetectorIntervalS, d.DetectorIntervalS, 10, 300)) * time.Second,
		StuckAfter:             settingsdur.Clamp(settingsdur.Parse(s.StuckAfter, settingsdur.Parse(d.StuckAfter, 10*time.Minute)), 2*time.Minute, 6*time.Hour),
		IgnoreScale:            s.IgnoreScale == nil || *s.IgnoreScale,
		InitialEvents:          s.InitialEvents == nil || *s.InitialEvents,
		ObservedGenWaitTicks:   clampInt(s.ObservedGenWaitTicks, d.ObservedGenWaitTicks, 1, 120),
		IncarnationAbsentTicks: clampInt(s.IncarnationAbsentTicks, d.IncarnationAbsentTicks, 1, 60),
		KnownRevisionsMax:      clampInt(s.KnownRevisionsMax, d.KnownRevisionsMax, 11, 256),
	}
	if strings.EqualFold(strings.TrimSpace(s.Source), SourceV2) {
		r.Source = SourceV2
	}
	want := map[string]bool{}
	for _, k := range s.Kinds {
		if c, ok := canonicalV2Kind(k); ok {
			want[c] = true
		}
	}
	for _, c := range v2Kinds {
		if len(want) == 0 || want[c] {
			r.Kinds = append(r.Kinds, c)
		}
	}
	return r
}

type Resolved struct {
	Enabled        bool
	Interval       time.Duration
	Bucket         time.Duration
	Threshold      int64
	Hysteresis     int
	ExitHysteresis int
	OverlapMax     time.Duration
	Lookback       time.Duration
	WeakSignal     bool
	StalledMin     time.Duration
}

func DefaultSettings() Settings {
	t := true
	return Settings{Enabled: false, Interval: "60s", Bucket: "5m", Threshold: 10, Hysteresis: 2, ExitHysteresis: 6, OverlapMax: "30m", Lookback: "6h", StalledMin: "10m",
		// v0.10.957 — v2 varsayılanları (GET `defaults`; ResolvedV2 buradan okur).
		Source: SourceV1, DetectorIntervalS: 30, StuckAfter: "10m", IgnoreScale: &t, Kinds: append([]string(nil), v2Kinds...),
		InitialEvents: &t, ObservedGenWaitTicks: 10, IncarnationAbsentTicks: 3, KnownRevisionsMax: 32}
}

// bucketAllowed — saat bölenleri: Go AlignBucket ile CH toStartOfInterval aynı
// ızgarada kalsın (7 dk gibi bir kova iki tarafta farklı hizalanır).
var bucketAllowed = []time.Duration{time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 30 * time.Minute}

// snapBucket — izinli listede en büyük ≤ d (d < 1m → 1m).
func snapBucket(d time.Duration) time.Duration {
	best := bucketAllowed[0]
	for _, a := range bucketAllowed {
		if a <= d {
			best = a
		}
	}
	return best
}

// Resolved — kelepçeler: interval 30s..15m, bucket {1,5,10,15,30}m, threshold
// 1..1e6, hysteresis 2..12 VE hysteresis×bucket ≥ 10 dk (1 dk kovada 2 kova =
// 2 dk'lık karar çok kırılgan), exitHysteresis hysteresis..36 VE ×bucket ≥ 30 dk,
// overlapMax 5m..6h ve ≤ lookback/2, lookback 1h..48h ve ≥ 4×exitHysteresis×bucket
// ve lookback/bucket ≤ 576 kova, stalledMin 2m..2h.
func (s Settings) Resolved() Resolved {
	d := DefaultSettings()
	r := Resolved{
		Enabled:        s.Enabled,
		Interval:       settingsdur.Clamp(settingsdur.Parse(s.Interval, settingsdur.Parse(d.Interval, time.Minute)), 30*time.Second, 15*time.Minute),
		Bucket:         snapBucket(settingsdur.Parse(s.Bucket, settingsdur.Parse(d.Bucket, 5*time.Minute))),
		Threshold:      s.Threshold,
		Hysteresis:     s.Hysteresis,
		ExitHysteresis: s.ExitHysteresis,
		OverlapMax:     settingsdur.Clamp(settingsdur.Parse(s.OverlapMax, settingsdur.Parse(d.OverlapMax, 30*time.Minute)), 5*time.Minute, 6*time.Hour),
		Lookback:       settingsdur.Clamp(settingsdur.Parse(s.Lookback, settingsdur.Parse(d.Lookback, 6*time.Hour)), time.Hour, 48*time.Hour),
		WeakSignal:     s.WeakSignal == nil || *s.WeakSignal,
		StalledMin:     settingsdur.Clamp(settingsdur.Parse(s.StalledMin, settingsdur.Parse(d.StalledMin, 10*time.Minute)), 2*time.Minute, 2*time.Hour),
	}
	if r.Threshold <= 0 {
		r.Threshold = d.Threshold
	}
	if r.Threshold > 1_000_000 {
		r.Threshold = 1_000_000
	}
	if r.Hysteresis < 2 {
		r.Hysteresis = d.Hysteresis
	}
	if r.Hysteresis > 12 {
		r.Hysteresis = 12
	}
	if minH := int(math.Ceil(float64(10*time.Minute) / float64(r.Bucket))); r.Hysteresis < minH {
		r.Hysteresis = minH
	}
	if r.ExitHysteresis <= 0 {
		r.ExitHysteresis = d.ExitHysteresis
	}
	if r.ExitHysteresis < r.Hysteresis {
		r.ExitHysteresis = r.Hysteresis
	}
	if r.ExitHysteresis > 36 {
		r.ExitHysteresis = 36
	}
	if minEH := int(math.Ceil(float64(30*time.Minute) / float64(r.Bucket))); r.ExitHysteresis < minEH {
		r.ExitHysteresis = minEH
	}
	// lookback ≥ 4·EH·B ama tavan 48 sa: tavana sığmıyorsa EH düşer (pencere büyümez)
	if maxEH := int(48 * time.Hour / (4 * r.Bucket)); r.ExitHysteresis > maxEH {
		r.ExitHysteresis = maxEH
	}
	if minLB := 4 * time.Duration(r.ExitHysteresis) * r.Bucket; r.Lookback < minLB {
		r.Lookback = minLB
	}
	// seri başına kova sayısı ≤ 576 (48 sa / 5 dk): 1 dk kovayla 48 sa = 2880
	// kova × iş yükü × revizyon etkinlik tavanını (500k) geçerli ayarla aşardı
	if n := int(r.Lookback / r.Bucket); n > 576 {
		r.Lookback = 576 * r.Bucket
	}
	if r.OverlapMax >= r.Lookback/2 {
		r.OverlapMax = r.Lookback / 2
	}
	return r
}

// Config — saf çekirdeğin girdisi (reconcile.go).
func (r Resolved) Config() Config {
	return Config{Bucket: r.Bucket, Threshold: r.Threshold, Hysteresis: r.Hysteresis, ExitHysteresis: r.ExitHysteresis, OverlapMax: r.OverlapMax, WeakSignal: r.WeakSignal, StalledMin: r.StalledMin}
}

// ValidateSettings — PUT kapısı: anlaşılmaz girdi 400 olsun (kelepçe yine
// okumada — Resolved; operatör girdiğini geri görür, uygulananı resolved'da).
func ValidateSettings(s Settings) error {
	for name, v := range map[string]string{"interval": s.Interval, "bucket": s.Bucket, "overlapMax": s.OverlapMax, "lookback": s.Lookback, "stalledMin": s.StalledMin} {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if settingsdur.Parse(v, 0) == 0 {
			return fmt.Errorf("%s anlaşılamadı: %q (örn. \"30s\", \"5m\", \"6h\", \"2d\")", name, v)
		}
	}
	if s.Threshold < 0 {
		return fmt.Errorf("threshold negatif olamaz: %d", s.Threshold)
	}
	if s.Hysteresis < 0 || s.ExitHysteresis < 0 {
		return fmt.Errorf("histerezis negatif olamaz")
	}
	// v0.10.957 — v2 vidaları: aynı duruş (anlaşılmaz/negatif 400, aralık
	// dışı okumada kelepçelenir — GET `resolved` uygulananı gösterir).
	if v := strings.TrimSpace(s.StuckAfter); v != "" && settingsdur.Parse(v, 0) == 0 {
		return fmt.Errorf("stuckAfter anlaşılamadı: %q (örn. \"10m\", \"1h\")", s.StuckAfter)
	}
	if v := strings.TrimSpace(s.Source); v != "" && !strings.EqualFold(v, SourceV1) && !strings.EqualFold(v, SourceV2) {
		return fmt.Errorf("source %q geçersiz: %q ya da %q", s.Source, SourceV1, SourceV2)
	}
	for name, v := range map[string]int{"detectorIntervalS": s.DetectorIntervalS, "observedGenWaitTicks": s.ObservedGenWaitTicks,
		"incarnationAbsentTicks": s.IncarnationAbsentTicks, "knownRevisionsMax": s.KnownRevisionsMax} {
		if v < 0 {
			return fmt.Errorf("%s negatif olamaz: %d", name, v)
		}
	}
	for i, k := range s.Kinds {
		if _, ok := canonicalV2Kind(k); !ok {
			return fmt.Errorf("kinds[%d] %q geçersiz: %s", i, k, strings.Join(v2Kinds, " | "))
		}
	}
	return nil
}

// SettingsStore — v0.10.957 — system_settings["rollouts"]'ın dar yüzü
// (*chstore.Store karşılar). Dışa açıldı — api katmanı PUT'u sahte depoyla
// gidiş-dönüş test edebilsin (rollouts.go rolloutSettingsStoreOf).
type SettingsStore = settingsStore

type settingsStore interface {
	GetRolloutSettingsRaw(ctx context.Context) ([]byte, error)
	PutRolloutSettingsRaw(ctx context.Context, raw []byte) error
}

type SettingsService struct {
	mu  sync.RWMutex
	cfg Settings
}

func NewSettingsService() *SettingsService { return &SettingsService{cfg: DefaultSettings()} }

func (s *SettingsService) Current() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *SettingsService) Resolved() Resolved { return s.Current().Resolved() }

// ResolvedV2 — v0.10.957 — v2 vidalarının uygulanan değerleri.
func (s *SettingsService) ResolvedV2() V2Resolved { return s.Current().ResolvedV2() }

func (s *SettingsService) Configure(cfg Settings) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// applyLoaded — SAF: kalıcı blob canlı ayardan YENİ ya da eşitse alınır
// (eski pod'un bayat blobu daha yeni admin PUT'unu ezmesin).
func applyLoaded(cur, loaded Settings) Settings {
	if loaded.UpdatedAt >= cur.UpdatedAt {
		return loaded
	}
	return cur
}

func (s *SettingsService) LoadPersisted(ctx context.Context, store settingsStore) error {
	if s == nil || store == nil {
		return nil
	}
	raw, err := store.GetRolloutSettingsRaw(ctx)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return nil
	}
	var cfg Settings
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return fmt.Errorf("rollout settings decode: %w", err)
	}
	s.mu.Lock()
	s.cfg = applyLoaded(s.cfg, cfg)
	s.mu.Unlock()
	return nil
}

func (s *SettingsService) SavePersisted(ctx context.Context, store settingsStore, cfg Settings) error {
	cfg.UpdatedAt = time.Now().UnixNano()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := store.PutRolloutSettingsRaw(ctx, raw); err != nil {
		return err
	}
	s.Configure(cfg)
	return nil
}

func (s *SettingsService) StartConfigRefresh(ctx context.Context, store settingsStore, interval time.Duration) {
	if s == nil || store == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.LoadPersisted(ctx, store); err != nil {
				log.Printf("[rollout] ayar tazeleme: %v", err)
			}
		}
	}
}
