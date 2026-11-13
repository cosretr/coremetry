package chstore

// ml_status.go — ML son tik durumu (v0.10.1764). Süreç-içi sayaçlar sre
// pod'unda kalır; karne api pod'undan okunur → lider her tik sonunda küçük
// bir blob yazar (system_settings `ml_tick_status`). "0 olay" ile
// "çalışmıyor" buradan ayrılır; VM bekleme (backoff) durumu buradan görünür.

import (
	"context"
	"encoding/json"
)

const mlTickStatusKey = "ml_tick_status"

// MLBackoffEntry — beklemedeki VM ailesi.
type MLBackoffEntry struct {
	Family    string `json:"family"`
	Until     int64  `json:"until"` // unix sn
	Failures  int    `json:"failures"`
	LastError string `json:"lastError,omitempty"`
}

// MLSignalTick — sinyal başına son tik özeti (v0.10.1767 doldurur).
type MLSignalTick struct {
	Ran        bool   `json:"ran"`
	Services   int    `json:"services"`
	SourceMs   int64  `json:"sourceMs"`
	Candidates int    `json:"candidates"`
	Suppressed int    `json:"suppressed"`
	Written    int    `json:"written"`
	Partial    int    `json:"partial"`
	BatchCut   int    `json:"batchCut,omitempty"` // v0.10.1811: batch kuralının kestiği aday
	Error      string `json:"error,omitempty"`
	// Unmeasured — v0.10.2063: dedektör → "ölçülemedi" gerekçesi (kaynak /
	// etiket yok; sağlıklı DEĞİL, anomali DEĞİL). Ör. dependency_error_rate
	// error_type etiketi olmayan VM'de.
	Unmeasured map[string]string `json:"unmeasured,omitempty"`
}

// MLKindTick — v0.10.1812 (audit §10 dilim 1.1, §13 D3): bir sinyal türünün
// (log | trace | metric) son tikteki ölçümü. Dedektör fazı; tik sonrası
// zenginleştirme/replay sorguları yalnız tik toplamında (MLTickStatus.ES…).
type MLKindTick struct {
	Services   int   `json:"services"`   // seri yazılan ∪ aday üreten servis
	Series     int   `json:"series"`     // bu tik ml_series'e yazılan ayrı (servis, part)
	SeriesRows int   `json:"seriesRows"` // ml_series satırı
	ES         int   `json:"es"`
	VM         int   `json:"vm"`
	CH         int   `json:"ch"`
	Dropped    int   `json:"dropped"` // ES servis terms tavanı dışında kalan servis (yalnız log)
	DurationMs int64 `json:"durationMs"`
}

// MLTickStatus — son tik.
type MLTickStatus struct {
	At         int64 `json:"at"` // unix sn
	DurationMs int64 `json:"durationMs"`
	Events     int64 `json:"events"`
	// ContribQueries — v0.10.2037: bu tikte suçlu seri için atılan kırılım
	// sorgusu (yalnız yeni açılan / tepesi büyüyen metrik olayları).
	ContribQueries int64 `json:"contribQueries,omitempty"`
	// LogSvcUnmatched / LogSvcViaPod — v0.10.2045: log ML olayının ES servis
	// adı katalogda yoktu ve pod'dan çözülemedi (yazılmadı) / pod'dan çözüldü.
	LogSvcUnmatched int64                   `json:"logSvcUnmatched,omitempty"`
	LogSvcViaPod    int64                   `json:"logSvcViaPod,omitempty"`
	Steps           map[string]int64        `json:"steps,omitempty"` // adım → ms
	Signals         map[string]MLSignalTick `json:"signals,omitempty"`
	Backoff         []MLBackoffEntry        `json:"backoff"`
	Partial         int64                   `json:"partial"`            // bu tikte kısmi ES sonucu alan dedektör
	Docs            int64                   `json:"docs"`               // log RED penceresinde toplanan belge
	BatchCut        int64                   `json:"batchCut,omitempty"` // v0.10.1811: batch kuralının kestiği aday
	// v0.10.1812 — sinyal türü başına ölçüm + tik toplam sorgu sayıları.
	Kinds     map[string]MLKindTick `json:"kinds,omitempty"`
	ES        int64                 `json:"es"`
	VM        int64                 `json:"vm"`
	CH        int64                 `json:"ch"`
	LastError string                `json:"lastError,omitempty"`
	// v0.10.2015 (anomali tekilleştirme D3) — gölge grup terfisi: bu tikte
	// karar verilen grup ve kapıdan geçseydi Problem "açardı" sayısı.
	ShadowGroups    int64 `json:"shadowGroups,omitempty"`
	ShadowWouldOpen int64 `json:"shadowWouldOpen,omitempty"`
	// v0.10.2058 — Davis tek-olay kolu (yalnız gölge): tek sinyalli grubun
	// güçlü + uzun kötüleşmesi "tek sinyal: Problem açardı" sayısı.
	ShadowSingleWouldOpen int64 `json:"shadowSingleWouldOpen,omitempty"`
	// ShadowSvcWouldOpen / ShadowSvcP1 — v0.10.2089: servis düzeyi gölge
	// kapısı (anomaly-auto:svc:*) — "Problem açardı" servis sayısı + P1 olanlar.
	ShadowSvcWouldOpen int64 `json:"shadowSvcWouldOpen,omitempty"`
	ShadowSvcP1        int64 `json:"shadowSvcP1,omitempty"`
	// v0.10.2052 (D4) — canlı kip: anahtar açık mı (izleyici de görür) ve
	// bu tikte açılan / tazelenen / kapanan grup Problem'i sayısı.
	GroupLive      bool  `json:"groupLive,omitempty"`
	GroupOpened    int64 `json:"groupOpened,omitempty"`
	GroupRefreshed int64 `json:"groupRefreshed,omitempty"`
	GroupResolved  int64 `json:"groupResolved,omitempty"`
	// v0.10.2019 — metrik ailesi başına otomatik seçilen kaynak (saatte bir
	// tespit; ayar DEĞİL, Settings › ML'de salt-okunur) ve tikin Thanos ölçümü.
	MetricSources map[string]MLMetricSource `json:"metricSources,omitempty"`
	Thanos        *MLThanosTick             `json:"thanos,omitempty"`
}

// MLMetricSource — v0.10.2019: bir metrik ailesinin kaynağı. Source:
// "vm" | "thanos" | "none" (ölçüldü, iki kaynakta da yok) | "" (ölçülemedi).
type MLMetricSource struct {
	Source    string   `json:"source"`
	Clusters  []string `json:"clusters,omitempty"` // Thanos cluster adları
	CheckedAt int64    `json:"checkedAt,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// MLThanosTick — v0.10.2019: tikin Thanos okuması (VM sayacından ayrı).
type MLThanosTick struct {
	Queries   int64    `json:"queries"`
	Ms        int64    `json:"ms"`                  // sorgu sürelerinin toplamı
	MaxMs     int64    `json:"maxMs"`               // en yavaş sorgu
	Errors    int64    `json:"errors"`              // hata / kısmi / kesik sonuç
	BudgetCut int64    `json:"budgetCut,omitempty"` // cluster tavanı yüzünden sorulmayan ifade
	Unmatched int64    `json:"unmatched,omitempty"` // servise eşlenemeyen pod
	Failed    []string `json:"failed,omitempty"`    // bu tikte yanıt vermeyen cluster'lar
}

func (s *Store) GetMLTickStatus(ctx context.Context) (MLTickStatus, bool, error) {
	raw, err := s.GetSetting(ctx, mlTickStatusKey)
	if err != nil || len(raw) == 0 {
		return MLTickStatus{}, false, err
	}
	var t MLTickStatus
	if err := json.Unmarshal(raw, &t); err != nil {
		return MLTickStatus{}, false, nil
	}
	return t, true, nil
}

func (s *Store) SaveMLTickStatus(ctx context.Context, t MLTickStatus) error {
	if t.Backoff == nil {
		t.Backoff = []MLBackoffEntry{}
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, mlTickStatusKey, raw)
}
