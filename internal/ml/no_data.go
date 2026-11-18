package ml

import (
	"encoding/json"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ── Veri gelmedi (v0.10.2359) ─────────────────────────────────────────────
// Koşu "görülmedi" iki ayrı şeydir: seri değerlendirildi ve bant içindeydi
// (kaçırma sayılır, episodeCloseMisses sonra "banda döndü" kapanır) ya da
// bu tik o seriyi taşıyan dedektör ailesi HİÇ veri değerlendiremedi (hata,
// kısmi ES, bütçe/backoff atlaması, boş kova). İkincisi kaçırma sayılmaz:
// koşu açık tutulur; noDataHoldMax boyunca veri dönmezse "veri gelmedi"
// gerekçesiyle kapanır (kanıt endReason=no_data, kayıt EndReason) ve ML
// Problem'i "kendiliğinden kapandı" yoluna girmez (bayat süpürme kapatır).

// noDataHoldMax — veri yokken açık tutma sınırı. 3 sa: VM backoff (dakikalar),
// ES/CH kesintisi ve saatlik hacim değerlendirmesini rahat örter; rejim
// bastırması (RegimeHours, 6 sa) ve koşu gc'sinden (24 sa) kısa, böylece
// izlenemeyen olay bir vardiya boyunca "sürüyor" görünmez.
const noDataHoldMax = 3 * time.Hour

// EndReasonNoData — kanıt `endReason` / kayıt EndReason değeri.
const EndReasonNoData = "no_data"

// famCoverage — bir dedektör ailesinin (Detector.Name) bu tikteki durumu.
type famCoverage struct{ blind bool }

// tickCoverage — SAF girdi: olay dedektörü → aile, aile → bu tik kör mü.
// Bilinmeyen aile (hiç olay üretmemiş, kapalı) = bugünkü davranış.
type tickCoverage struct {
	classOf map[string]string
	fam     map[string]famCoverage
}

// noData — bu tikte bu dedektörün serisi değerlendirilemedi mi.
func (c tickCoverage) noData(detector string) bool {
	cls, ok := c.classOf[detector]
	if !ok {
		return false
	}
	return c.fam[cls].blind
}

// familyBlind — SAF: dedektör çağrısı veri değerlendiremedi mi. err: kaynak
// hatası / kısmi ES / bütçe atlaması; skipped: VM backoff atlaması;
// countsSeries: aile seri sayar (önceki tiklerde >0) ve bu tik 0 = boş kova.
func familyBlind(err error, skipped, countsSeries bool, seriesDelta int64) bool {
	return err != nil || skipped || (countsSeries && seriesDelta == 0)
}

// NoDataOf — SAF: kanıttaki veri-yok durumu. since: tutmanın başı (unix sn,
// 0 = yok); ended: olay "veri gelmedi" gerekçesiyle kapandı.
func NoDataOf(evidence string) (since int64, ended bool) {
	if evidence == "" {
		return 0, false
	}
	var m struct {
		Since  int64  `json:"noDataSince"`
		Reason string `json:"endReason"`
	}
	if json.Unmarshal([]byte(evidence), &m) != nil {
		return 0, false
	}
	return m.Since, m.Reason == EndReasonNoData
}

// noDataOpen — SAF: kapanmamış ve veri-yok tutması sınır içinde (kart/liste
// "açık" kuralı; son görülme donmuş olsa da).
func noDataOpen(stage string, sinceSec int64, now time.Time) bool {
	return stage != chstore.MLStageClosed && sinceSec > 0 && now.Sub(time.Unix(sinceSec, 0)) <= noDataHoldMax
}

// stampRecordEnd — kaydı olan kapanış satırına EndReason yazar (kayıt yoksa aynen).
func stampRecordEnd(ev chstore.MLEvent) chstore.MLEvent {
	if ev.Record == "" {
		return ev
	}
	var rec EventRecord
	if json.Unmarshal([]byte(ev.Record), &rec) != nil {
		return ev
	}
	rec.EndReason = EndReasonNoData
	if b, err := json.Marshal(rec); err == nil {
		ev.Record = string(b)
	}
	return ev
}

// dropNoDataResolves — SAF: "veri gelmedi" ile biten servislerin ML Problem
// kapanışları planından çıkar (birleşen ikiz hariç). Satır tazelenmez;
// evaluator'ın bayat süpürmesi "source silent" diye kapatır — "anomali
// bitti / kendiliğinden kapandı" yazılmaz.
func dropNoDataResolves(resolve []chstore.Problem, merged, silent map[string]bool) []chstore.Problem {
	if len(silent) == 0 {
		return resolve
	}
	out := resolve[:0:0]
	for _, p := range resolve {
		if !merged[p.ID] && silent[p.Service] {
			continue
		}
		out = append(out, p)
	}
	return out
}
