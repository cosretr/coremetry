package api

// ml_evidence.go — v0.10.1873, öğrenme döngüsü Faz 2 parti B (audit §7.1
// "Kanıt" satırları; mockup görünüm 2–4): ML olay detayının sinyale göre
// kanıt bölümleri. api.go BÜYÜMEZ: registerRoutesExtra ile kayıt.
//
//   GET /api/ml/{id}/evidence/{kind} — her rol (salt okuma)
//     endpoints (trace) — spanmetrics MV (GetEndpointsMV, SkipStatus: ham
//                         spans yan okuması YOK): olay penceresi + önceki
//                         eşit pencere = 2 CH (MV).
//     patterns  (log)   — logstore imza grupları (ES ya da CH logstore,
//                         örneklem 500, tek sayfa): olay penceresi + önceki
//                         eşit pencere = 2 logstore çağrısı. FE yalnız bölüm
//                         AÇILINCA ister.
//     metrics   (metric)— dedektörün aynı servis için izlediği diğer metrik
//                         ifadeleri: ml_series (VM'den zaten çekilmiş seri)
//                         = 1 CH; VM'e yeni sorgu YOK.
//   Her çağrıda olay satırı (ml_events, yoksa olay anı kaydı) = +1 CH.
//
// Pencere ml.EvidenceWindow (başlangıç … en çok 1 sa; en az 15 dk). Saf
// kurulum internal/ml/explain_evidence.go (cümleler orada). serveCached 60 sn;
// anahtar id + tür + kart saat dilimi (tüm girdiler). Sinyal türü uymayan
// istek boş liste döner (yanlış bölüm sessizce başka veri göstermez).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml-evidence", (*Server).registerMLEvidenceRoutes) }

func (s *Server) registerMLEvidenceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/{id}/evidence/{kind}", auth.RequireAnyRole(mlReadRoles, s.getMLEvidence))
}

const (
	mlEvidenceTTL          = 60 * time.Second
	mlEvidenceBudget       = 10 * time.Second
	mlEvidenceSample       = 500
	mlEvidenceEndpointRows = 200 // iki pencerede de: olayın endpoint'i az çağrılı olsa da satırda kalsın (tablo ilk 10)
)

// mlEvidenceKinds — tür → olayın sinyali.
var mlEvidenceKinds = map[string]string{"endpoints": chstore.MLSignalTrace, "patterns": chstore.MLSignalLog, "metrics": chstore.MLSignalMetric}

// mlEvidenceKey — SAF: tüm girdiler (olay id, tür, kart saat dilimi).
func mlEvidenceKey(id, kind, tz string) string {
	return fmt.Sprintf("ml:evidence:v1:id=%q:kind=%s:tz=%s", id, kind, tz)
}

func (s *Server) getMLEvidence(w http.ResponseWriter, r *http.Request) {
	id, kind := strings.TrimSpace(r.PathValue("id")), r.PathValue("kind")
	sig, ok := mlEvidenceKinds[kind]
	if id == "" || !ok {
		writeJSONError(w, http.StatusBadRequest, "kind: endpoints | patterns | metrics")
		return
	}
	loc := ml.OperatorLocation(s.store.AnomalySensitivityForDetectors())
	s.serveCached(w, r, mlEvidenceKey(id, kind, loc.String()), mlEvidenceTTL, func(ctx context.Context) (any, error) {
		now := time.Now()
		cfg := s.store.GetMLConfig(ctx)
		in, found, err := s.mlEvidenceInput(ctx, id, cfg, now, loc)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, fmt.Errorf("%w: bu olay için kayıt yok", errNotFound)
		}
		if in.Signal != sig {
			return map[string]any{"rows": []any{}}, nil
		}
		from, to, prevFrom := ml.EvidenceWindow(in)
		win := map[string]any{"from": from.UnixNano(), "to": to.UnixNano(), "prevFrom": prevFrom.UnixNano()}
		switch kind {
		case "endpoints":
			cur, err := s.store.GetEndpointsMV(ctx, chstore.EndpointsQuery{From: from, To: to, Service: in.Service, Limit: mlEvidenceEndpointRows, Sort: "calls", Dir: "desc", SkipStatus: true})
			if err != nil {
				return nil, err
			}
			prev, perr := s.store.GetEndpointsMV(ctx, chstore.EndpointsQuery{From: prevFrom, To: from, Service: in.Service, Limit: mlEvidenceEndpointRows, Sort: "calls", Dir: "desc", SkipStatus: true})
			if perr != nil {
				prev = nil // önceki pencere okunamadı → "önce" sütunu boş (yokluk ≠ sıfır)
			}
			win["rows"], win["prevOK"] = ml.EndpointEvidence(in, cur, prev, ml.EvidenceRange(in)), perr == nil
			return win, nil
		case "patterns":
			return s.mlPatternEvidence(ctx, in, from, to, prevFrom, win)
		default:
			cctx, cancel := context.WithTimeout(ctx, mlEvidenceBudget)
			defer cancel()
			wf, wt := ml.CourseWindow(in)
			series, err := s.store.MLSeriesRange(cctx, chstore.MLSignalMetric, in.Service, wf, wt, cfg.BucketSec)
			if err != nil {
				return nil, err
			}
			win["rows"] = ml.MetricNeighbors(in, series, cfg)
			return win, nil
		}
	})
}

// mlEvidenceInput — olay satırı (ml_events) önce; TTL'le düştüyse olay anı kaydı.
func (s *Server) mlEvidenceInput(ctx context.Context, id string, cfg chstore.MLConfig, now time.Time, loc *time.Location) (ml.CardInput, bool, error) {
	evs, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{ID: id, Since: now.Add(-mlEventTTLWindow), Limit: 1})
	if err != nil {
		return ml.CardInput{}, false, err
	}
	if len(evs) > 0 {
		return ml.SummaryInput(evs[0], cfg, now, loc), true, nil
	}
	snap, ok, err := s.store.GetMLSnapshot(ctx, id)
	if err != nil || !ok {
		return ml.CardInput{}, false, err
	}
	return ml.CardInputFromSnapshot(snap, now, loc), true, nil
}

// mlPatternEvidence — iki logstore çağrısı (pencere + önceki eşit pencere),
// /api/logs/patterns?baseline=1 ile aynı birleştirme. Yavaş backend 200 +
// degraded (gövde boş), gerçek hata 5xx.
func (s *Server) mlPatternEvidence(ctx context.Context, in ml.CardInput, from, to, prevFrom time.Time, win map[string]any) (any, error) {
	f := logstore.Filter{Service: in.Service, From: from, To: to}
	if in.Part == "_error" {
		f.SeverityMin = 17
	}
	tctx, cancel := context.WithTimeout(ctx, mlEvidenceBudget)
	defer cancel()
	res, err := logstore.GroupBySignatureN(tctx, s.logs, f, logstore.PatternsMaxGroups, mlEvidenceSample)
	if err != nil {
		if mapped := logstore.MapBackendSlow(err, tctx, ctx); errors.Is(mapped, logstore.ErrBackendSlow) {
			log.Printf("[ml] evidence patterns degraded (backend=%s, svc=%q): %v", s.logs.Backend(), in.Service, err)
			win["rows"], win["degraded"] = []any{}, true
			return win, nil
		}
		return nil, err
	}
	bf := f
	bf.From, bf.To = prevFrom, from
	bctx, bcancel := context.WithTimeout(ctx, mlEvidenceBudget)
	base, berr := logstore.GroupBySignatureN(bctx, s.logs, bf, logstore.PatternsMaxGroups, mlEvidenceSample)
	bcancel()
	if berr == nil {
		base = logstore.AlignPatternBaseline(res, base)
		logstore.JoinPatternBaseline(res, base)
	}
	win["rows"] = ml.PatternEvidence(in, res.Groups, berr == nil, ml.EvidenceRange(in))
	win["sampled"], win["cap"], win["baseOK"] = res.Sampled, mlEvidenceSample, berr == nil
	return win, nil
}
