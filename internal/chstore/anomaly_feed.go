package chstore

// anomaly_feed.go — v0.10.2080 (anomali tekilleştirme, okuyucu taşıması):
// eski anomaly_events okuyucularının ML olaylarını da görmesi için TEK
// sunucu tarafı erişici.
//
// Neden: /anomalies birleşik listesi (ml.BuildUnified) iki kaynağı
// birleştiriyor, ama servis şeridi, CoSRE, MCP, deploy raporu gibi
// yüzeyler yalnız anomaly_events okuyordu; ML olayları (ve v0.10.2069'dan
// beri eski log_template_new'in ML karşılığı new_template) orada yoktu.
// Bu erişici aynı yüzeylere ML olaylarını AYNI satır şekliyle
// (AnomalyEvent, kind "ml:<dedektör>") ekler.
//
// Tekilleştirme kuralı TEK: MLSupersededLegacyIDs (birleşik liste de onu
// çağırır) — ML new_template olayı eski satırın kimliğini kanıtında taşır
// (evidence.legacyId), eski satır düşer, kalıp bir kez görünür. Açık/bitti
// kuralı TEK: MLEventOpen (birleşik listenin "üye açık" kuralı).
//
// Maliyet: çağrı başına bir ek CH okuması (ml_events FINAL, last_seen
// WHERE + LIMIT + max_execution_time 5). Elasticsearch çağrısı YOK.

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"sort"
	"time"
)

// MLAnomalyKindPrefix — ML olayının AnomalyEvent satırındaki tür öneki
// ("ml:<dedektör>"). Kimlik ML olay kimliğidir; ayrıntı sayfası
// /anomalies/ml/<id>, kararı ML karar ucu (MLVerdictID).
const MLAnomalyKindPrefix = "ml:"

// mlNewTemplateDetector — eski log_template_new'in ML karşılığı.
const mlNewTemplateDetector = "new_template"

// feedExecSec — erişicinin eski tablo okumasında çağıran tavan vermediyse
// kullanılan max_execution_time.
const feedExecSec = 10

// IsMLAnomalyKind — SAF: satır ML olayından mı geldi.
func IsMLAnomalyKind(kind string) bool {
	return len(kind) > len(MLAnomalyKindPrefix) && kind[:len(MLAnomalyKindPrefix)] == MLAnomalyKindPrefix
}

// MLEventOpen — SAF: ML olayı sürüyor mu (kapalı damgası yok VE son
// görülme 4 kovadan taze). Birleşik liste ve erişici aynı kuralı okur.
func MLEventOpen(e MLEvent, nowNs int64) bool {
	if e.Stage == MLStageClosed {
		return false
	}
	w := int64(e.WindowSec)
	if w <= 0 {
		w = 300
	}
	return nowNs-e.LastSeen <= 4*w*int64(time.Second)
}

// mlFeedEvidence — erişicinin okuduğu kanıt alanları.
type mlFeedEvidence struct {
	LegacyID string `json:"legacyId"`
	LogQuery string `json:"logQuery"`
	Sample   string `json:"sample"`
	Template string `json:"template"`
}

func parseMLFeedEvidence(raw string) mlFeedEvidence {
	var ev mlFeedEvidence
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &ev)
	}
	return ev
}

// MLSupersededLegacyIDs — SAF (v0.10.2069 kuralı, tek gövde): ML'e taşınan
// eski olayların kimlikleri. Eski satırı düşürür, ML satırını değil: ML
// olayı gruplara / ≥ 2 sinyal Problem kuralına giren kayıttır. ML olayı
// okumada yoksa eski satır aynen görünür.
func MLSupersededLegacyIDs(ml []MLEvent) map[string]bool {
	var out map[string]bool
	for _, e := range ml {
		if e.Detector != mlNewTemplateDetector {
			continue
		}
		if id := parseMLFeedEvidence(e.Evidence).LegacyID; id != "" {
			if out == nil {
				out = map[string]bool{}
			}
			out[id] = true
		}
	}
	return out
}

// MLEventAsAnomaly — SAF: ML olayı → eski satır şekli. Desen: kod (log
// kalıbı / hata kodu) yoksa seviye / pod, o da yoksa dedektör adı. Sayı
// gözlenen değer. Durum
// MLEventOpen'dan; karar ML kararından (ListMLEvents ekler).
func MLEventAsAnomaly(e MLEvent, nowNs int64) AnomalyEvent {
	ev := parseMLFeedEvidence(e.Evidence)
	pattern := e.Code
	for _, alt := range []string{ev.Template, e.Level, e.Pod, e.Detector} {
		if pattern != "" {
			break
		}
		pattern = alt
	}
	status := "cleared"
	if MLEventOpen(e, nowNs) {
		status = "active"
	}
	dir := ""
	if e.Direction == AnomalyDirectionUp || e.Direction == AnomalyDirectionDown {
		dir = e.Direction
	}
	// Oran = gözlenen / taban (eski satırın "normalin N katı" anlamı; ML
	// skoru z'dir, oran sütununa yazılmaz). Taban yoksa sayım olayı: oran 0,
	// taban türü "none" (eski log_template_new ile aynı yorum).
	ratio, baseKind := CountOnlyRatio, AnomalyBaselineNone
	if e.Baseline > 0 {
		ratio, baseKind = e.Observed/e.Baseline, ""
	}
	return AnomalyEvent{
		ID: e.ID, Kind: MLAnomalyKindPrefix + e.Detector, Pattern: pattern, Service: e.Service,
		StartedAt: e.StartedAt, LastSeen: e.LastSeen, PeakRatio: ratio, CurrentRatio: ratio,
		CurrentCount: uint64(max(e.Observed, 0)), Sample: ev.Sample, Direction: dir,
		BaselineValue: e.Baseline, BaselineKind: baseKind, LogQuery: ev.LogQuery, Status: status,
		Verdict: e.Verdict, VerdictBy: e.VerdictBy, VerdictAt: e.VerdictAt,
	}
}

// MergeAnomalyFeed — SAF: eski satırlar + ML olayları → tek liste. Eski
// satır ML olayıyla taşındıysa düşer; ML satırları süzgecin SQL'de
// uygulanamayan kısmından (aktif, başlangıç penceresi, susturma) Go'da
// geçer. Sıra ListAnomalyEvents ile aynı: aktif önce, sonra son görülme
// (yeni önce), eşitlikte kimlik. Tavan f.Limit.
func MergeAnomalyFeed(legacy []AnomalyEvent, ml []MLEvent, f ListAnomalyEventsFilter, nowNs int64) []AnomalyEvent {
	sup := MLSupersededLegacyIDs(ml)
	// Emekli kopya yolunun (ml/promote.go, v0.10.2067'de silindi) eski
	// "ml:" satırları ML olayıyla AYNI kimliği taşır: ikisi birden gelirse
	// ML satırı kalır.
	for _, m := range ml {
		if sup == nil {
			sup = map[string]bool{}
		}
		sup[m.ID] = true
	}
	out := make([]AnomalyEvent, 0, len(legacy)+len(ml))
	for _, e := range legacy {
		if !sup[e.ID] {
			out = append(out, e)
		}
	}
	for _, m := range ml {
		a := MLEventAsAnomaly(m, nowNs)
		switch {
		case f.ActiveOnly && a.Status != "active":
			continue
		case f.FromNs > 0 && a.StartedAt < f.FromNs:
			continue
		case f.ToNs > 0 && a.StartedAt >= f.ToNs:
			continue
		case slices.Contains(f.ExcludeIDs, a.ID):
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		ai, aj := out[i].Status == "active", out[j].Status == "active"
		if ai != aj {
			return ai
		}
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].ID < out[j].ID
	})
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out
}

// mlFeedFilter — SAF: eski süzgeç → ML okuma süzgeci. ok=false: ML okuması
// gereksiz (tür süzgeci eski bir tür, ya da boş servis kümesi = hiçbir şey).
func mlFeedFilter(f ListAnomalyEventsFilter, now time.Time) (MLEventsFilter, bool) {
	if f.Kind != "" && !IsMLAnomalyKind(f.Kind) {
		return MLEventsFilter{}, false
	}
	if f.Services != nil && len(f.Services) == 0 {
		return MLEventsFilter{}, false
	}
	since := f.SinceNs
	if since == 0 {
		since = now.Add(-24 * time.Hour).UnixNano()
	}
	// last_seen ≥ started_at: başlangıç penceresinin alt sınırı last_seen
	// için de geçerli bir alt sınırdır (okuma daralır, sonuç değişmez).
	since = max(since, f.FromNs)
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	mf := MLEventsFilter{Since: time.Unix(0, since), Limit: min(limit, MLUnifiedCap), Services: f.Services}
	if IsMLAnomalyKind(f.Kind) {
		mf.Detector = f.Kind[len(MLAnomalyKindPrefix):]
	}
	return mf, true
}

// ListAnomalyFeed — eski olaylar + ML olayları, aynı süzgeçle (SQL'de
// uygulanamayan kısmı Go'da). Eski okuma hatası hata döner (bugünkü
// davranış); ML okuma hatası loglanır ve yalnız eski satırlar döner —
// ML kesintisi yüzeyi boşaltmaz.
func (s *Store) ListAnomalyFeed(ctx context.Context, f ListAnomalyEventsFilter) ([]AnomalyEvent, error) {
	if f.MaxExecSec == 0 {
		f.MaxExecSec = feedExecSec
	}
	legacy, err := s.ListAnomalyEvents(ctx, f)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	mf, ok := mlFeedFilter(f, now)
	if !ok {
		return legacy, nil
	}
	ml, err := s.ListMLEvents(ctx, mf)
	if err != nil {
		log.Printf("[anomaly-feed] ML olayları okunamadı (yalnız eski satırlar): %v", err)
		return legacy, nil
	}
	return MergeAnomalyFeed(legacy, ml, f, now.UnixNano()), nil
}
