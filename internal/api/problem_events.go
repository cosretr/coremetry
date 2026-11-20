package api

// problem_events.go — v0.10.2163 (Davis tarzı tek Problem D4, spec
// docs/plans/spec-davis-style-problem-merge.md):
//
//	GET /api/problems/{id}/events
//
// Bir Problem'in OLAY ÇİZELGESİ: servis başına tek Problem'e (D1–D3) katılan
// üyeler, zaman sırasıyla. Üye = Problem'e bağlı ml_events satırı — eski
// dedektörün üye olayı (yaygın yavaşlama / metrik anomalisi / anomali
// terfisi) ya da ML'in canlı damgalı olayı. Sahip eski bir aileyse kendi
// sinyali de çizelgede bir satırdır (o sinyal ml_events'te değildir).
// Üyesi olmayan Problem'de liste BOŞ döner: birleşme yok, çizilecek bir şey
// yok (detay sayfası satırı gizler).
//
// Neden ayrı uç, /api/problems/{id} gövdesinde değil: liste ve tekil okuma
// aynı satır şeklini paylaşıyor (ProblemDetail her alanı çiziyor); çizelge
// yalnız detay açıkken istenir (aç-üzerine-getir) ve kanıt gövdesi okur —
// 200 satırlık listeye taşınmaz. Listenin ihtiyacı olan tek sayı
// (eventCount) hafif okumayla zenginleştirmede gelir.
//
// Rol kapısı YOK — salt-okunur, viewer GÖRÜR (kardeş /affected duruşu).
// serveCached 15 sn (Problem okumasıyla aynı tazelik); anahtar tek girdiyi,
// kimliği taşır. {id} "P-…" görüntü kimliğini de kabul eder. Yazma ucu yok.
// api.go BÜYÜMEZ: route defteri.

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("problem-events", (*Server).registerProblemEventRoutes) }

func (s *Server) registerProblemEventRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/problems/{id}/events", s.getProblemEvents)
}

// problemEventRow — çizelgenin tek satırı.
type problemEventRow struct {
	Detector string `json:"detector"`
	// RuleID — üyenin kendi kuralı (eski dedektör); ML olayında boş.
	RuleID string `json:"ruleId,omitempty"`
	// EventID — ML olayının kimliği (detay /anomalies/ml/<id>); üye olayında
	// ve sahibin kendi satırında boş.
	EventID   string  `json:"eventId,omitempty"`
	StartedAt int64   `json:"startedAt"` // unix ns
	LastSeen  int64   `json:"lastSeen"`  // unix ns
	Observed  float64 `json:"observed"`
	Baseline  float64 `json:"baseline"`
	Direction string  `json:"direction"` // up | down
	Live      bool    `json:"live"`
	// Own — sahibin KENDİ sinyali (Problem'i açan dedektör).
	Own bool `json:"own,omitempty"`
	// Record — v0.10.2380: servis hata oranı olayının kaydı (başarılı /
	// başarısız istek grafiği + rakamlar; ml.RequestsRecordView). Yalnız EN
	// YENİ uygun olayın satırında — gövde tek kayıtla sınırlı. Kayıt bu
	// okumada zaten geliyor (ListMLEvents); ek sorgu / ek istek yok.
	Record *ml.RecordView `json:"record,omitempty"`
}

// problemEventsKey — SAF: cache anahtarı (tek girdi: kimlik).
// :v2: — v0.10.2380 satıra `record` eklendi (gövde şekli değişti).
func problemEventsKey(id string) string { return "problem:events:v2:" + id }

// buildProblemEventRows — SAF: Problem + bağlı olaylar → çizelge (başlangıç,
// eşitse sahip önce, sonra dedektör adı). evs boşsa çizelge boş (sahibin
// kendi satırı tek başına gösterilmez). Sahibin ölçüsü bir üyeden geldiyse
// (öncelik yükseltmesi) kendi satırı değer taşımaz — yanlış oran yazılmaz.
func buildProblemEventRows(p chstore.Problem, evs []chstore.MLEvent, nowNs int64) []problemEventRow {
	rows := []problemEventRow{}
	if len(evs) == 0 {
		return rows
	}
	if det := chstore.ProblemOwnDetector(p); det != "" {
		own := problemEventRow{Detector: det, RuleID: p.RuleID, StartedAt: p.StartedAt, LastSeen: nowNs, Direction: "up",
			Live: p.Status != "resolved", Own: true}
		if p.ResolvedAt != nil {
			own.LastSeen = *p.ResolvedAt
		}
		if chstore.ProblemMeasureIsOwn(p) {
			own.Observed, own.Baseline = p.Value, p.Threshold
			if chstore.ProblemBelowRule(p) {
				own.Direction = "down"
			}
		}
		rows = append(rows, own)
	}
	recAt, recSeen := -1, int64(0) // en yeni istek-görünümü kaydı olan satır
	var recView *ml.RecordView
	for i := range evs {
		e := evs[i]
		r := problemEventRow{Detector: e.Detector, StartedAt: e.StartedAt, LastSeen: e.LastSeen, Observed: e.Observed,
			Baseline: e.Baseline, Direction: e.Direction, Live: chstore.MLEventOpen(e, nowNs)}
		if ev, ok := chstore.MemberEvidenceOf(e); ok {
			r.RuleID = ev.RuleID
		} else {
			r.EventID = e.ID
			if rv := ml.RequestsRecordView(&e); rv != nil && (recAt < 0 || e.LastSeen > recSeen) {
				recAt, recSeen, recView = len(rows), e.LastSeen, rv
			}
		}
		rows = append(rows, r)
	}
	if recAt >= 0 {
		rows[recAt].Record = recView
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].StartedAt != rows[j].StartedAt {
			return rows[i].StartedAt < rows[j].StartedAt
		}
		if rows[i].Own != rows[j].Own {
			return rows[i].Own
		}
		return rows[i].Detector < rows[j].Detector
	})
	return rows
}

// problemEventDetectors — SAF: çizelgedeki farklı dedektör sayısı.
func problemEventDetectors(rows []problemEventRow) int {
	seen := map[string]bool{}
	for _, r := range rows {
		seen[r.Detector] = true
	}
	return len(seen)
}

func (s *Server) getProblemEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "problem id required")
		return
	}
	s.serveCached(w, r, problemEventsKey(id), 15*time.Second, func(ctx context.Context) (any, error) {
		var p *chstore.Problem
		var err error
		if chstore.IsProblemDisplayID(id) {
			p, err = s.store.GetProblemByDisplayID(ctx, id)
		} else {
			p, err = s.store.GetProblem(ctx, id)
		}
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, notFoundf("problem %q", id)
		}
		evs, err := s.store.ProblemEvents(ctx, *p)
		if err != nil {
			return nil, err
		}
		rows := buildProblemEventRows(*p, evs, time.Now().UnixNano())
		return map[string]any{"problemId": p.ID, "items": rows, "total": len(rows), "detectors": problemEventDetectors(rows)}, nil
	})
}
