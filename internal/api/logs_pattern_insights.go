package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/logstore"
)

// logs_pattern_insights.go — v0.10.1896 (Log Search Faz 4 dilim 4.2; audit
// §3.2). /logs Desenler sekmesinin iki ek Elasticsearch sorusu:
//
//   - eğilim çizgisi: ilk 20 desen, TEK `_msearch` (size:0 + date_histogram);
//   - "ilk kez görüldü": önceki pencere örneklemesinde OLMAYAN desenlerden
//     ilk 20'si için pencereden geriye 7 gün varlık yoklaması, TEK `_msearch`
//     (size:0, terminate_after:1).
//
// ES YÜKÜ (1877 olayı kuralı): iki yol `system_settings['logs_search']
// .patternInsights` arkasında, varsayılan KAPALI. Kapalıyken `insights=1`
// yok sayılır — bu iki istek HİÇ gitmez (TestPatternInsights_OffSendsNothing).
// Açıkken de yalnız Desenler sekmesi açılınca (istemci yalnız orada ister) ve
// sonuç /api/logs/patterns ile aynı serveCached gövdesine (30 sn) girer.
// Servis seçili değilse yoklama YAPILMAZ: tüm `app-*` üzerinde 7 günlük
// yoklama 1877 sınıfı bir yüktür; durum "no-service" ("ölçülemedi").

const (
	patternFirstSeenLookback = 7 * 24 * time.Hour
	patternInsightsBudget    = 4 * time.Second
)

// patternInsightsOn — SAF: istek + ayar kararı. Kapalı ayar isteği ezer.
func patternInsightsOn(want, setting bool) bool { return want && setting }

// patternInsightsKeySuffix — önbellek anahtarına giren etkin bayrak (kapalı
// ayarla açık ayarın gövdeleri karışmasın; v0.5.187 sınıfı).
func patternInsightsKeySuffix(on bool) string {
	if !on {
		return ""
	}
	return ":ins=1"
}

// patternInsightTop — SAF: aranabilir sorgusu olan ilk n grubun indeksleri
// (gruplar sayıya göre azalan sırada gelir).
func patternInsightTop(groups []logstore.SignatureGroup, n int, onlyNew bool) []int {
	var out []int
	for i, g := range groups {
		if len(out) >= n {
			break
		}
		if g.Query == "" || (onlyNew && !g.New) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// patternFirstSeenScope — SAF: yoklama yapılabilir mi? Tek servis şart
// (çoklu servis / takım / kapsam kümesi ve servissiz pencere → hayır).
func patternFirstSeenScope(f logstore.Filter) bool {
	svc := strings.TrimSpace(f.Service)
	return svc != "" && !strings.Contains(svc, ",") && len(f.Services) == 0 && len(f.TeamValues) == 0
}

// existStatus — SAF: yoklama sonuçlarının zarf durumu.
func existStatus(st []logstore.ExistState) string {
	for _, s := range st {
		if s == logstore.ExistUnknown {
			return "partial"
		}
	}
	return "ok"
}

func firstSeenLabel(s logstore.ExistState) string {
	switch s {
	case logstore.ExistAbsent:
		return "first"
	case logstore.ExistFound:
		return "seen"
	}
	return "unknown"
}

// attachPatternInsights — res'e eğilim + ilk görülme ekler (yerinde). Hata
// asla sonucu düşürmez: durum zarfa yazılır, desen listesi aynen döner.
func (s *Server) attachPatternInsights(ctx context.Context, f logstore.Filter, res *logstore.PatternsResult, baseline bool) {
	if res == nil {
		return
	}
	meta := &logstore.PatternInsightsMeta{
		FromNs: f.From.UnixNano(), ToNs: f.To.UnixNano(), LookbackDays: int(patternFirstSeenLookback / (24 * time.Hour)),
		BucketSec: logstore.PatternTrendBucketSec(f.To.Sub(f.From)),
	}
	res.Insights = meta
	ins, ok := logstore.Unwrap(s.logs).(logstore.PatternInsighter)
	if !ok {
		meta.Trend, meta.FirstSeen = "unsupported", "unsupported"
		return
	}
	ictx, cancel := context.WithTimeout(ctx, patternInsightsBudget)
	defer cancel()

	trendIdx := patternInsightTop(res.Groups, logstore.PatternInsightsMax, false)
	var newIdx []int
	switch {
	case !baseline || res.Baseline == nil || res.Baseline.Degraded:
		meta.FirstSeen = "no-baseline"
	case !patternFirstSeenScope(f):
		meta.FirstSeen = "no-service"
	default:
		newIdx = patternInsightTop(res.Groups, logstore.PatternInsightsMax, true)
		if len(newIdx) == 0 {
			meta.FirstSeen = "none"
		}
	}

	var wg sync.WaitGroup
	var trends []logstore.PatternTrend
	var terr error
	if len(trendIdx) > 0 {
		qs := make([]string, len(trendIdx))
		for j, i := range trendIdx {
			qs[j] = res.Groups[i].Query
		}
		wg.Add(1)
		go func() { defer wg.Done(); trends, terr = ins.PatternTrends(ictx, f, qs, meta.BucketSec) }()
	} else {
		meta.Trend = "ok"
	}
	var exist []logstore.ExistState
	var eerr error
	if len(newIdx) > 0 {
		probes := make([]logstore.PatternProbe, len(newIdx))
		for j, i := range newIdx {
			probes[j] = logstore.PatternProbe{Query: res.Groups[i].Query, From: f.From.Add(-patternFirstSeenLookback), To: f.From}
		}
		wg.Add(1)
		go func() { defer wg.Done(); exist, eerr = ins.PatternsExist(ictx, f, probes) }()
	}
	wg.Wait()

	if len(trendIdx) > 0 {
		if terr != nil {
			log.Printf("[logs] pattern trends (svc=%q): %v", f.Service, terr)
			meta.Trend = "error"
		} else {
			meta.Trend = "ok"
			for j, i := range trendIdx {
				if j >= len(trends) {
					break
				}
				if trends[j].Failed {
					meta.Trend = "partial"
					continue
				}
				res.Groups[i].Trend, res.Groups[i].TrendPartial = trends[j].Points, trends[j].Partial
				if trends[j].Partial {
					meta.Trend = "partial"
				}
			}
		}
	}
	if len(newIdx) > 0 {
		if eerr != nil {
			log.Printf("[logs] pattern first-seen (svc=%q): %v", f.Service, eerr)
			meta.FirstSeen = "error"
			for _, i := range newIdx {
				res.Groups[i].FirstSeen7d = "unknown"
			}
		} else {
			meta.FirstSeen = existStatus(exist)
			for j, i := range newIdx {
				st := logstore.ExistUnknown
				if j < len(exist) {
					st = exist[j]
				}
				res.Groups[i].FirstSeen7d = firstSeenLabel(st)
			}
		}
	}
}
