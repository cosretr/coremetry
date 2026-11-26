package ml

// lag_pattern.go — v0.10.2476 (operatör kararı): consumer lag günlük desen
// kuralının kaydedici tarafı. Kural SAF (chstore.LagDailyPatternDays, karar
// group_gate.go lagDailyPattern); burası yalnız geçmişi okur.
//
// OKUMA: tik başına en çok BİR ml_events okuması (chstore.MLLagEpisodeStarts)
// ve yalnız belleğinde olmayan açık lag bölümleri için. Bölümün geçmişi
// başlangıcıyla sabittir (önceki 7 gün) → bölüm başına bir kez okunur, bölüm
// ayrılınca bellekten düşer. Okuma hatasında bellek korunur (bilinen desen
// satırı bir tik okunamadı diye P1'e dönmez); hiç bilinmeyen bölüm o tik
// desensiz (v0.10.2396 önceliği) karar alır.

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// lagHistMemo — bölüm anahtarı (lagEpisodeKey) → serinin önceki başlangıçları.
type lagHistMemo map[string][]int64

// lagEpisodeKey — SAF: bölüm kimliği + başlangıç (aynı kimlik yeni bölümde
// yeni başlangıçla gelir).
func lagEpisodeKey(e chstore.MLEvent) string {
	return e.ID + "@" + time.Unix(0, e.StartedAt).UTC().Format(time.RFC3339Nano)
}

// lagHistoryNeeds — SAF: geçmişi bilinmeyen consumer lag bölümleri (servis,
// kod listeleri ve okuma aralığı). evs: bu tikin + açık koşuların olayları.
func lagHistoryNeeds(evs []chstore.MLEvent, memo lagHistMemo) (missing []chstore.MLEvent, services, codes []string, since, until time.Time) {
	svcSet, codeSet := map[string]bool{}, map[string]bool{}
	for _, e := range evs {
		if _, ok := lagEventTopic(e); !ok || e.StartedAt <= 0 {
			continue
		}
		if _, ok := memo[lagEpisodeKey(e)]; ok {
			continue
		}
		missing = append(missing, e)
		svcSet[e.Service], codeSet[e.Code] = true, true
		st := time.Unix(0, e.StartedAt)
		from := st.Add(-time.Duration(chstore.LagPatternWindowDays)*24*time.Hour - chstore.LagPatternHourSlack - time.Hour)
		if since.IsZero() || from.Before(since) {
			since = from
		}
		if st.After(until) {
			until = st
		}
	}
	return missing, sortedKeys(svcSet), sortedKeys(codeSet), since, until
}

// lagHistoryInput — SAF: belleği açık olaylara göre budar ve kapı girdisini
// kurar (seri anahtarı → başlangıçlar; aynı serinin bölümleri birleşir).
func lagHistoryInput(evs []chstore.MLEvent, memo lagHistMemo) map[string][]int64 {
	live := map[string]bool{}
	out := map[string][]int64{}
	for _, e := range evs {
		k := lagEpisodeKey(e)
		starts, ok := memo[k]
		if !ok {
			continue
		}
		live[k] = true
		sk := chstore.MLSeriesKey(e.Service, e.Code)
		out[sk] = append(out[sk], starts...)
	}
	for k := range memo {
		if !live[k] {
			delete(memo, k)
		}
	}
	return out
}

// lagHistory — kapının LagHistory girdisi; kural kapalıyken nil.
func (r *Recorder) lagHistory(ctx context.Context, cfg chstore.MLConfig, evs []chstore.MLEvent) map[string][]int64 {
	if !cfg.LagDailyPatternOn() || r.store == nil {
		r.lagHist = nil
		return nil
	}
	if r.lagHist == nil {
		r.lagHist = lagHistMemo{}
	}
	missing, svcs, codes, since, until := lagHistoryNeeds(evs, r.lagHist)
	if len(missing) > 0 {
		got, err := r.store.MLLagEpisodeStarts(ctx, svcs, codes, since, until)
		if err != nil {
			log.Printf("[ml] lag daily pattern history: %v", err)
		} else {
			for _, e := range missing {
				starts := append([]int64(nil), got[chstore.MLSeriesKey(e.Service, e.Code)]...)
				sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
				r.lagHist[lagEpisodeKey(e)] = starts
			}
		}
	}
	return lagHistoryInput(evs, r.lagHist)
}
