package chstore

import (
	"context"
	"sync"
	"time"
)

// prior_coverage.go — compare=prior okumalarının KAYNAK KAPSAMA kapısı
// (v0.10.1025 inceleme düzeltmesi R4).
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// MV'ler geriye dolmaz: kuruldukları andan itibaren yazılırlar. Taze bir
// kurulumda, bir telemetri temizliğinden sonra ya da bir göç MV'yi yeniden
// yarattığında, prior penceresi MV'nin ilk kovasından ÖNCE başlayabilir.
// O prior EKSİK sayar; current ona karşı FAZLA görünür ve bir pencere boyu
// boyunca her sayaç karosu sahte bir KÖTÜLEŞME (kırmızı ↑) basar. TTL ufku
// (PriorReadable) bunu yakalamaz — ufuk "silinmiş mi"yi sorar, "hiç yazıldı
// mı"yı değil.
//
// ── KARAR ───────────────────────────────────────────────────────────────
//
// MV kaynakları: ilk kova ÖLÇÜLÜR (EnvSummaryCovers deseni,
// service_env_summary.go: `min(time_bucket), count()`, 60 s önbellek, boş
// tablo → kapsamıyor). Prior yalnız ilk kova pFrom'dan KESİNLİKLE önceyse
// okunur: ilk kova MV'nin kurulduğu (ya da verinin başladığı) anı içerir ve
// büyük olasılıkla YARIM doludur, o kova prior'un ilki olamaz. Probe
// düşerse (zaman aşımı dahil) kapı KAPALI — kıyas çizilmez, yanıltmaz.
//
// Ham spans (yalnız /databases env süzgeci): sınırsız `min(time)` burada
// iki kuralı birden çiğnerdi — spans sorgusu zaman-sınırlı WHERE + LIMIT
// taşımalı (CLAUDE.md sert kısıtı) ve milyar satırlık bir tabloda sütun
// taraması 5 s tavanını aşabilir (metricresolve.go v0.10.520 ölçümü,
// spanmetrics_1m: 7 s). Soru bu yüzden VARLIK sorusuna çevrildi: pFrom'dan
// önceki 24 saatte HERHANGİ bir span var mı (`LIMIT 1`, ilk eşleşmede
// biter, gün bölümleriyle budanır). Varsa kaynak pFrom'da canlıydı.
// Bedeli: o 24 saatte hiç trafik yoksa kıyas çizilmez (kapalı kalır).

// priorSource — kapsaması ölçülen MV kaynağı. Tablo adı SQL'e sabitten
// girer; dışarıdan dizge alınmaz.
type priorSource int

const (
	priorSrcDBSummary        priorSource = iota // /databases listesi (MV yolu)
	priorSrcDBCallerSummary                     // /database detayı
	priorSrcMsgSummary                          // /messaging listesi
	priorSrcMsgCallerSummary                    // /messaging listesi (üretim/tüketim ayrımı)
	priorSourceCount
)

// priorSourceProbeSQL — MV başına ilk-kova probu.
var priorSourceProbeSQL = [priorSourceCount]string{
	priorSrcDBSummary:        "SELECT min(time_bucket), count() FROM db_summary_5m SETTINGS max_execution_time = 5",
	priorSrcDBCallerSummary:  "SELECT min(time_bucket), count() FROM db_caller_summary_5m SETTINGS max_execution_time = 5",
	priorSrcMsgSummary:       "SELECT min(time_bucket), count() FROM messaging_summary_5m SETTINGS max_execution_time = 5",
	priorSrcMsgCallerSummary: "SELECT min(time_bucket), count() FROM messaging_caller_summary_5m SETTINGS max_execution_time = 5",
}

// spansLiveBeforeSQL — ham yolun varlık probu: [pFrom − 24 sa, pFrom)
// aralığında en az bir span var mı.
const spansLiveBeforeSQL = `
	SELECT count() FROM (
		SELECT 1 FROM spans
		WHERE time >= ? AND time < ?
		LIMIT 1
	)
	SETTINGS max_execution_time = 5`

const priorCoverageProbeTTL = 60 * time.Second

type coverageProbe struct {
	mu  sync.Mutex
	at  time.Time
	min time.Time
	ok  bool
}

// priorSourceCovers — SAF: ilk kova pFrom'dan KESİNLİKLE önce mi? ok=false
// (probe hatası / boş tablo) ya da sıfır zaman → hayır.
func priorSourceCovers(first time.Time, ok bool, pFrom time.Time) bool {
	return ok && !first.IsZero() && first.Before(pFrom)
}

// sourceCovers — MV kaynağı [pFrom, …) aralığını baştan beri taşıyor mu.
// Probe hatası 60 s önbelleklenir (her istekte yeniden denenmesin) —
// çağıranın iptal ettiği bağlam HARİÇ: o, kaynağın değil isteğin hâli.
func (s *Store) sourceCovers(ctx context.Context, src priorSource, pFrom time.Time) bool {
	if s == nil || src < 0 || src >= priorSourceCount {
		return false
	}
	p := &s.priorCoverage[src]
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.at.IsZero() || time.Since(p.at) > priorCoverageProbeTTL {
		var first time.Time
		var n uint64
		err := s.conn.QueryRow(ctx, priorSourceProbeSQL[src]).Scan(&first, &n)
		if err != nil && ctx.Err() != nil {
			return false
		}
		p.min, p.ok, p.at = first, err == nil && n > 0, time.Now()
	}
	return priorSourceCovers(p.min, p.ok, pFrom)
}

// spansLiveBefore — ham spans pFrom'dan önceki 24 saatte canlı mıydı.
// Hata → hayır (kapı kapalı). s.conn: MV probuyla aynı bağlantı (dosya
// okuma havuzu listesinde değil; tek satırlık bir varlık sorusu).
func (s *Store) spansLiveBefore(ctx context.Context, pFrom time.Time) bool {
	if s == nil {
		return false
	}
	var n uint64
	if err := s.conn.QueryRow(ctx, spansLiveBeforeSQL, pFrom.Add(-24*time.Hour), pFrom).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// DBListPriorReadable — /databases ?compare=prior: prior okunmalı mı?
// İki kapı: saklama ufku (horizonDays — zarfın SpanHorizonDays'i: MV yolunda
// 90, env/ham yolda span saklaması; 0 = bilinmiyor → okunmaz) ve kaynağın
// ileriye dönük kapsaması (MV yolunda db_summary_5m, ham yolda spans).
func (s *Store) DBListPriorReadable(ctx context.Context, pFrom, pTo, now time.Time, raw bool, horizonDays int) bool {
	if !PriorReadable(pFrom, pTo, now, horizonDays) {
		return false
	}
	if raw {
		return s.spansLiveBefore(ctx, pFrom)
	}
	return s.sourceCovers(ctx, priorSrcDBSummary, pFrom)
}

// MessagingPriorReadable — /messaging ?compare=prior: prior okunmalı mı?
// Prior rollup İKİ MV okur (messaging_summary_5m sayaçlar + quantile'lar,
// messaging_caller_summary_5m üretim/tüketim ayrımı); ikisi de kapsamalı.
func (s *Store) MessagingPriorReadable(ctx context.Context, pFrom, pTo, now time.Time) bool {
	return PriorReadable(pFrom, pTo, now, msgMVHorizonDays) &&
		s.sourceCovers(ctx, priorSrcMsgSummary, pFrom) &&
		s.sourceCovers(ctx, priorSrcMsgCallerSummary, pFrom)
}
