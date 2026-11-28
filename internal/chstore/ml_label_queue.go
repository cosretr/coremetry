package chstore

// ml_label_queue.go — v0.10.2484 (precision programı B.3/2): Sinyaller
// sekmesinin "Bu hafta etiketle (10)" kuyruğu. Son 7 günün ETİKETSİZ ML
// olaylarından deterministik, tabakalı 10 örnek. Etiket olay sayfasındaki
// mevcut "Gerçek anomali / Sorun değil" düğmesiyle verilir; kuyruktan açılan
// sayfada oy origin='queue' ile yazılır ve karne kuyruk etiketlerini
// organiklerden ayrı sayar (etiket yanlılığı önlemi).
//
// SALT RAPOR: kuyruk hiçbir tespit / kapı / bildirim kararına girmez.

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"time"
)

// MLLabelQueueSize — haftalık örnek boyu.
const MLLabelQueueSize = 10

// MLLabelQueueWindow — aday penceresi (son 7 gün).
const MLLabelQueueWindow = 7 * 24 * time.Hour

// mlLabelQueueCap — aday okuması tavanı. ml_events dedektör başına günlük
// tavanlı (DailyEventCap 200): 7 gün × ~27 dedektör ≈ 38 bin satır.
const mlLabelQueueCap = 60000

// MLLabelQueueItem — kuyruk adayı / örneği.
type MLLabelQueueItem struct {
	ID        string `json:"id"`
	Detector  string `json:"detector"`
	Service   string `json:"service"`
	StartedAt int64  `json:"startedAt"` // unix ns
	Labelled  bool   `json:"-"`         // oyu var (örneğe girmez)
}

// MLISOWeek — SAF: "2026-W48" (UTC). Örnekleyicinin haftalık tohumu.
func MLISOWeek(t time.Time) string {
	y, w := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", y, w)
}

// MLISOWeekStart — SAF: t'nin ISO haftasının pazartesi 00:00 UTC'si.
func MLISOWeekStart(t time.Time) time.Time {
	t = t.UTC()
	wd := (int(t.Weekday()) + 6) % 7 // pazartesi = 0
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return d.AddDate(0, 0, -wd)
}

// mlLabelQueueEligible — SAF: dedektör kuyruğa girebilir mi. Muaf (etki
// kuralı, metric_kafka), akış dışı (telemetri hacmi, yük / yapı) ve üye
// dedektörleri girmez — bunlar precision kuralına girmez, etiketleri boşa gider.
func mlLabelQueueEligible(det string) bool {
	return det != "" && !MLPrecisionExempt(det) && !MLOffFeedDetector(det) && !IsMLMemberDetector(det)
}

// mlLabelQueueKey — SAF: fnv64a(olay id | ISO hafta) + fmix64 sonlandırıcı.
// Neden sonlandırıcı: FNV-1a'da yalnız SON baytlar değişen girdiler (hafta
// "W48" → "W49") çarpımı yalnız ±asal kadar kaydırır; sıra büyük ölçüde
// korunur ve örnek haftadan haftaya neredeyse aynı kalırdı (tablo testi
// yakaladı). murmur3 fmix64 tam çığ etkisi verir.
func mlLabelQueueKey(id, week string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id + "|" + week))
	k := h.Sum64()
	k ^= k >> 33
	k *= 0xff51afd7ed558ccd
	k ^= k >> 33
	k *= 0xc4ceb9fe1a85ec53
	k ^= k >> 33
	return k
}

// MLLabelQueueSample — SAF, deterministik: adaylardan en çok `size` örnek.
//
// Kural:
//  1. Etiketli, muaf ve akış dışı (gürültü görünümü) adaylar elenir.
//  2. Dedektör sırası: kurala girebilen etiketi n < 20 olanlar önce
//     (`labelledN`, karnenin n'i), kendi içinde 7 günlük aday hacmi azalan,
//     eşitlikte ad.
//  3. Her dedektörün adayları fnv(id | hafta) artan sıralanır.
//  4. Tur tur: her turda sıradaki her dedektörden bir örnek (en az birer
//     örnek önce en hacimli n < 20 dedektörlere), `size` dolana dek.
//
// Aynı hafta + aynı aday kümesi → aynı örnek; hafta değişince sıra değişir.
func MLLabelQueueSample(cands []MLLabelQueueItem, week string, size int, labelledN map[string]int64) []MLLabelQueueItem {
	if size <= 0 {
		return []MLLabelQueueItem{}
	}
	type keyed struct {
		it  MLLabelQueueItem
		key uint64
	}
	by := map[string][]keyed{}
	for _, c := range cands {
		if c.Labelled || c.ID == "" || !mlLabelQueueEligible(c.Detector) {
			continue
		}
		by[c.Detector] = append(by[c.Detector], keyed{c, mlLabelQueueKey(c.ID, week)})
	}
	dets := make([]string, 0, len(by))
	for d, ks := range by {
		sort.Slice(ks, func(i, j int) bool {
			if ks[i].key != ks[j].key {
				return ks[i].key < ks[j].key
			}
			return ks[i].it.ID < ks[j].it.ID
		})
		dets = append(dets, d)
	}
	needs := func(d string) bool { return labelledN[d] < MLPrecisionMinLabels }
	sort.Slice(dets, func(i, j int) bool {
		a, b := dets[i], dets[j]
		if needs(a) != needs(b) {
			return needs(a)
		}
		if len(by[a]) != len(by[b]) {
			return len(by[a]) > len(by[b])
		}
		return a < b
	})
	out := []MLLabelQueueItem{}
	for round := 0; len(out) < size; round++ {
		took := false
		for _, d := range dets {
			if len(out) >= size {
				break
			}
			if round < len(by[d]) {
				out = append(out, by[d][round].it)
				took = true
			}
		}
		if !took {
			break
		}
	}
	return out
}

// mlLabelQueueSQL — SAF: son 7 günün aday ML olayları (hafif kolonlar).
// Muaf / akış dışı / üye dedektörler ve gürültü kapısında kalan (held) ya da
// geçersiz kayıtlar SQL'de elenir; etiketli işareti bayrak olarak döner
// (örnekleyici eler). Zaman sınırlı + LIMIT + max_execution_time.
func mlLabelQueueSQL(recCols bool) string {
	excl := "'" + strings.Join(append(append(append([]string{}, MLMemberDetectors...), mlPrecisionExemptDetectors...), MLLoadStructureDetectors...), "', '") + "'"
	held := ""
	if recCols {
		held = " AND publish_state NOT IN ('held', '" + mlPublishInvalid + "')"
	}
	return `
		SELECT id, toString(detector), toString(service), started_at,
		       id GLOBAL IN (SELECT substring(event_id, 4) FROM anomaly_verdicts FINAL
		              WHERE startsWith(event_id, 'ml:') AND created_at >= ?) AS labelled
		FROM ml_events FINAL
		WHERE started_at >= ? AND detector NOT IN (` + excl + `)
		  AND NOT ` + mlPlatformCond(recCols) + held + `
		LIMIT ` + fmt.Sprint(mlLabelQueueCap) + `
		SETTINGS max_execution_time = 10`
}

// MLLabelQueueCandidates — son `since`'den beri aday ML olayları (etiketli
// bayraklı). truncated = tavana değdi.
func (s *Store) MLLabelQueueCandidates(ctx context.Context, since time.Time) ([]MLLabelQueueItem, bool, error) {
	rows, err := s.conn.Query(ctx, mlLabelQueueSQL(s.hasMLRecordCols.Load()), chDateTime64Arg(since), chDateTime64Arg(since))
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []MLLabelQueueItem
	for rows.Next() {
		var it MLLabelQueueItem
		var st time.Time
		var lab uint8
		if err := rows.Scan(&it.ID, &it.Detector, &it.Service, &st, &lab); err != nil {
			return nil, false, err
		}
		it.StartedAt, it.Labelled = st.UnixNano(), lab == 1
		out = append(out, it)
	}
	return out, len(out) >= mlLabelQueueCap, rows.Err()
}

// MLQueueLabelsSince — `since`'den beri kuyruktan verilmiş oy sayısı (bu
// haftanın kalan kuyruk boyu = 10 − bu). Kolon yoksa 0.
func (s *Store) MLQueueLabelsSince(ctx context.Context, since time.Time) (int, error) {
	if !s.hasVerdictOriginCol.Load() {
		return 0, nil
	}
	var n uint64
	err := s.conn.QueryRow(ctx, `
		SELECT count() FROM anomaly_verdicts FINAL
		WHERE startsWith(event_id, 'ml:') AND origin = ? AND created_at >= ?
		SETTINGS max_execution_time = 5`, VerdictOriginQueue, chDateTime64Arg(since)).Scan(&n)
	return int(n), err
}
