package api

// trace_health.go — v0.10.757 "Trace hattı sağlığı" paneli (trace bütünlüğü
// denetimi 2026-09-17; operatör onayı: sihirbaz değil panel, önce pod-içi).
//
//	GET /api/admin/clickhouse/trace-health?range_s=3600[&raw=1]  (admin; serveCached 30 s)
//
// raw=1 (v0.10.823) İSTEĞE BAĞLIDIR ve öyle kalmalı: "agregat için ham
// spans okumak bug"tır, panelin VARSAYILAN sayısı service_summary_5m'den
// gelmeye devam eder. Ham sayım bir agregat değil, MV sayısının
// DOĞRULAMASIDIR — MV kaybı ile replika ayrışmasını ayırt eder (test
// kümesi, 2026-09-19: mutabakat %25.8, sebebi kayıp değil, shard'ın
// replikalarının birbirini replike etmemesiydi). Pencere MV sayısıyla
// AYNI (traceHealthWindow) ve önbellek anahtarı bayrağı taşır.
//
// Üç kart, bölüm başına YUMUŞAK hata (bir CH sorgusu düşerse öteki kartlar
// gelir, `errors` alanı hangisinin düştüğünü söyler):
//   - kayıp: BU POD'un ingest sayaçları (accepted/dropped/write_failed/
//     kuyruk) + v0.10.754 reject sayaçları + dönüştürücü degrade'leri +
//     dağıtık spool durumu, yanında CH'de saklanan span/5 dk (service_
//     summary_5m). Mutabakat pod-içi: çok-podlu ingest'te toplam için ayrı
//     tablo gerekir (Faz B, ayrı dilim) — cevap `pod.host` ile hangi podun
//     saydığını söyler.
//   - kapsama: kök tanımı, MV gap günleri, son 5 dk kök oranı (strict /
//     entry) — mevcut TraceRootCoverage, kısa pencere.
//   - ad kalitesi: çıplak fiil adlı span payı, boş ad, ayrık ad, servis
//     başına ayrık ad ilk 10 (24 sa).
//   - filo (v0.10.767, Faz B): ingest_ledger'dan TÜM ingest podlarının
//     kabul/düşürme/yazma-hatası toplamı ve CH'de saklananla mutabakat —
//     YERLEŞMİŞ pencerede [from, now-10dk): span zamanı ≠ kabul zamanı
//     (collector batch + geç span), son dakikalar iki tarafta da sayılmaz.
// api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/otlp"
)

func init() { registerRoutesExtra("trace-health", (*Server).registerTraceHealthRoutes) }

func (s *Server) registerTraceHealthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/clickhouse/trace-health", auth.RequireRole(auth.RoleAdmin, s.getTraceHealth))
}

// traceHealthRange — SAF: 300..86400 sn, varsayılan 3600.
func traceHealthRange(raw string) int {
	r := parseInt(raw, 3600)
	if r < 300 {
		return 300
	}
	if r > 86400 {
		return 86400
	}
	return r
}

type traceHealthPod struct {
	Host string `json:"host"`
	// IngestRole (v0.10.760) — bu pod OTLP alıyor mu (COREMETRY_MODE ingest/all).
	// api-rolü podda sayaçlar hep sıfırdır; "kayıp yok" demek yanıltırdı
	// (prod ekranı: coremetry-api podu). Sayaçlar ingest podlarındadır.
	IngestRole  bool              `json:"ingestRole"`
	Accepted    int64             `json:"accepted"`
	Dropped     int64             `json:"dropped"`
	WriteFailed int64             `json:"writeFailed"`
	Queued      int               `json:"queued"`
	Capacity    int               `json:"capacity"`
	Rejects     map[string]uint64 `json:"rejects"`
	Degrades    map[string]uint64 `json:"degrades"`
}

type traceHealthCoverage struct {
	Def           string   `json:"def"`
	GapDays       []string `json:"gapDays"`
	RangeS        int      `json:"rangeS"`
	Source        string   `json:"source,omitempty"`
	Traces        uint64   `json:"traces"`
	WithRoot      uint64   `json:"withRoot"`
	WithEntryRoot uint64   `json:"withEntryRoot"`
}

// traceHealthFleet (v0.10.767) — filo mutabakatı. Toplamlar ve
// StoredSettled [SettledFrom, SettledTo) üzerinden; Empty = defterde satır
// yok (tablo henüz yok / ingest podları eski sürüm), Detail sebebini söyler.
type traceHealthFleet struct {
	Pods          []chstore.IngestFleetPod    `json:"pods"`
	Buckets       []chstore.IngestFleetBucket `json:"buckets"`
	Accepted      uint64                      `json:"accepted"`
	Dropped       uint64                      `json:"dropped"`
	WriteFailed   uint64                      `json:"writeFailed"`
	StoredSettled uint64                      `json:"storedSettled"`
	StoredKnown   bool                        `json:"storedKnown"`
	SettledFrom   int64                       `json:"settledFrom"`
	SettledTo     int64                       `json:"settledTo"`
	// v0.10.770 — oran yalnız defterin kapsadığı kovalardan: CoveredFrom =
	// max(SettledFrom, ilk örneğin 5 dk'ya YUKARI yuvarlanmışı); AcceptedSettled
	// ve StoredSettled [CoveredFrom, SettledTo) üzerinden. Accepted (tüm
	// pencere) pod tablosuyla tutarlı kalsın diye ayrı.
	CoveredFrom     int64  `json:"coveredFrom"`
	AcceptedSettled uint64 `json:"acceptedSettled"`
	Empty           bool   `json:"empty"`
	Detail          string `json:"detail,omitempty"`
}

type traceHealthResponse struct {
	GeneratedAt   int64                        `json:"generatedAt"`
	RangeS        int                          `json:"rangeS"`
	Pod           traceHealthPod               `json:"pod"`
	Spool         *chstore.DistributionQueue   `json:"spool,omitempty"`
	SpoolDegraded bool                         `json:"spoolDegraded"`
	SpoolDetail   string                       `json:"spoolDetail,omitempty"`
	Stored        []chstore.StoredSpanBucket   `json:"stored"`
	StoredTotal   uint64                       `json:"storedTotal"`
	Fleet         traceHealthFleet             `json:"fleet"`
	Coverage      traceHealthCoverage          `json:"coverage"`
	Names         chstore.OperationNameQuality `json:"names"`
	// Raw — v0.10.823, yalnız raw=1 istendiğinde. Yokluğu "ham sayım
	// istenmedi" demektir; sorgu düşerse errors["raw"] dolar.
	Raw    *chstore.RawSpanCount `json:"raw,omitempty"`
	Errors map[string]string     `json:"errors,omitempty"`
}

// traceHealthWindow — SAF: MV sayımının da ham sayımın da gördüğü pencere
// [from, to). Alt uç 5 dk kovasına hizalı (MV kovası bölünemez), üst uç
// now. İki sayı FARKLI pencereden okunursa fark "kayıp" gibi görünürdü —
// bu yüzden tek yerde üretilir.
func traceHealthWindow(now time.Time, rangeS int) (from, to time.Time) {
	return now.Add(-time.Duration(rangeS) * time.Second).Truncate(5 * time.Minute), now
}

// sumStored — SAF.
func sumStored(b []chstore.StoredSpanBucket) uint64 {
	var n uint64
	for _, x := range b {
		n += x.Spans
	}
	return n
}

// traceHealthSettleS — yerleşme payı: son 10 dk iki tarafta da sayılmaz.
const traceHealthSettleS = 600

// settledWindow — SAF: [from, now-settle), ikisi de 5 dk'ya hizalı; pencere
// paydan kısaysa boş (from == to).
func settledWindow(now time.Time, rangeS int) (from, to time.Time) {
	from = now.Add(-time.Duration(rangeS) * time.Second).Truncate(5 * time.Minute)
	to = now.Add(-traceHealthSettleS * time.Second).Truncate(5 * time.Minute)
	if !to.After(from) {
		return from, from
	}
	return from, to
}

// sumStoredIn — SAF: kova başlangıcı [from, to) içindeki saklanan toplam.
func sumStoredIn(b []chstore.StoredSpanBucket, from, to time.Time) uint64 {
	var n uint64
	for _, x := range b {
		if t := time.Unix(0, x.TimeNs); !t.Before(from) && t.Before(to) {
			n += x.Spans
		}
	}
	return n
}

// ledgerCoveredFrom — SAF: defterin ilk örneği pencere başından sonraysa
// oran o örneğin 5 dk'ya YUKARI yuvarlanmış kovasından başlar (ilk kısmi
// kova iki tarafta da dışarıda); örnek yoksa pencere başı.
func ledgerCoveredFrom(settledFrom time.Time, firstSampleNs int64) time.Time {
	if firstSampleNs <= 0 {
		return settledFrom
	}
	first := time.Unix(0, firstSampleNs)
	c := first.Truncate(5 * time.Minute)
	if !c.Equal(first) {
		c = c.Add(5 * time.Minute)
	}
	if c.After(settledFrom) {
		return c
	}
	return settledFrom
}

// sumAcceptedIn — SAF: 5 dk kova başlangıcı [from, to) içindeki filo kabulü.
func sumAcceptedIn(b []chstore.IngestFleetBucket, from, to time.Time) uint64 {
	var n uint64
	for _, x := range b {
		if t := time.Unix(0, x.TimeNs); !t.Before(from) && t.Before(to) {
			n += x.Accepted
		}
	}
	return n
}

// ledgerMissing — SAF: tablo yok hatası (küme kipinde CREATE ON CLUSTER
// kuyruktayken ya da eski sürüm). Hata değil, "defter yok" durumu.
func ledgerMissing(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "UNKNOWN_TABLE") || strings.Contains(m, "Code: 60") || strings.Contains(m, "doesn't exist")
}

func (s *Server) getTraceHealth(w http.ResponseWriter, r *http.Request) {
	rangeS := traceHealthRange(r.URL.Query().Get("range_s"))
	// v0.10.823 — anahtar TÜM girdileri taşır: raw bayrağı cevabın şeklini
	// değiştirir, anahtara girmezse ham sayım isteyen ile istemeyen aynı
	// kaydı paylaşır (v0.5.187 sınıfı çapraz zehirlenme).
	raw := parseBoolParam(r.URL.Query().Get("raw"))
	key := "admin:trace-health:range=" + strconv.Itoa(rangeS) + ":raw=" + strconv.FormatBool(raw)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		now := time.Now()
		host, _ := os.Hostname()
		resp := traceHealthResponse{
			GeneratedAt: now.UnixNano(), RangeS: rangeS,
			Errors: map[string]string{},
			Pod: traceHealthPod{
				Host:       host,
				IngestRole: !s.roleIngestOff,
				Rejects:    otlp.IngestRejectCounts(),
				Degrades:   otlp.ConvertDegradeCounts(),
			},
			Stored: []chstore.StoredSpanBucket{},
		}
		if s.ing != nil && s.ing.Spans != nil {
			resp.Pod.Accepted = s.ing.Spans.Accepted()
			resp.Pod.Dropped = s.ing.Spans.Dropped()
			resp.Pod.WriteFailed = s.ing.Spans.WriteFailed()
			resp.Pod.Queued, resp.Pod.Capacity = s.ing.Spans.QueueLen(), s.ing.Spans.Capacity()
		}
		resp.Spool, resp.SpoolDegraded, resp.SpoolDetail = s.distributionBacklog()

		from, to := traceHealthWindow(now, rangeS)
		if b, err := s.store.StoredSpanBuckets(ctx, from, to); err != nil {
			resp.Errors["stored"] = err.Error()
		} else {
			resp.Stored, resp.StoredTotal = b, sumStored(b)
		}

		// v0.10.823 — ham sayım AYNI pencerede, yalnız istendiğinde.
		if raw {
			if rc, err := s.store.RawSpanCounts(ctx, from, to); err != nil {
				resp.Errors["raw"] = err.Error()
			} else {
				resp.Raw = &rc
			}
		}

		// v0.10.767 (Faz B) — filo mutabakatı, yerleşmiş pencere.
		sf, st := settledWindow(now, rangeS)
		resp.Fleet = traceHealthFleet{
			Pods: []chstore.IngestFleetPod{}, Buckets: []chstore.IngestFleetBucket{},
			SettledFrom: sf.UnixNano(), SettledTo: st.UnixNano(), CoveredFrom: sf.UnixNano(),
			StoredKnown: resp.Errors["stored"] == "",
		}
		if fl, err := s.store.IngestLedgerFleet(ctx, "spans", sf, st, now); err != nil {
			if ledgerMissing(err) {
				resp.Fleet.Empty, resp.Fleet.Detail = true, "ingest_ledger tablosu yok — küme DDL kuyruğu bekleniyor ya da ingest podları v0.10.767'den eski"
			} else {
				resp.Errors["fleet"] = err.Error()
			}
		} else {
			resp.Fleet.Pods, resp.Fleet.Buckets = fl.Pods, fl.Buckets
			resp.Fleet.Accepted, resp.Fleet.Dropped, resp.Fleet.WriteFailed = fl.Accepted, fl.Dropped, fl.WriteFailed
			cf := ledgerCoveredFrom(sf, fl.FirstSampleNs)
			if cf.After(st) {
				cf = st
			}
			resp.Fleet.CoveredFrom = cf.UnixNano()
			resp.Fleet.StoredSettled = sumStoredIn(resp.Stored, cf, st)
			resp.Fleet.AcceptedSettled = sumAcceptedIn(fl.Buckets, cf, st)
			if len(fl.Pods) == 0 {
				resp.Fleet.Empty, resp.Fleet.Detail = true, "defterde satır yok — ingest podları v0.10.767+ mı, pencere yerleşme payından (10 dk) uzun mu?"
			}
		}

		resp.Coverage = traceHealthCoverage{Def: string(s.store.TraceRootDef()), GapDays: s.store.TraceMVGapDayList(ctx), RangeS: 300}
		if rows, source, err := s.store.TraceRootCoverage(ctx, 300); err != nil {
			resp.Errors["coverage"] = err.Error()
		} else {
			resp.Coverage.Source = source
			resp.Coverage.Traces, resp.Coverage.WithRoot = rootCoverageTotals(rows)
			resp.Coverage.WithEntryRoot = rootCoverageEntryRoot(rows)
		}

		if q, err := s.store.OperationNameQuality(ctx, now.Add(-24*time.Hour)); err != nil {
			resp.Errors["names"] = err.Error()
		} else {
			resp.Names = q
		}
		if len(resp.Errors) == 0 {
			resp.Errors = nil
		}
		return resp, nil
	})
}
