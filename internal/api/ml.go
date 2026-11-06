package api

// log_ml.go — ES log ML katmanı dilim 0 uçları (docs/audits/ml-audit.md).
// api.go BÜYÜMEZ: registerRoutesExtra ile kayıt.
//
//   GET /api/ml/events?since=24h&signal=&detector=&service=&stage= — her rol
//   GET /api/ml/scorecard                                   — her rol (30 g)
//   PUT /api/ml/{id}/verdict  {verdict, note?, detector, service} — editor+
//   GET /api/ml/{id}/replay                                 — her rol (kayıt + servisin diğer olayları)
//   POST /api/ml/backtest {signal, service, from, to, config?, overrides?} — editor+ (salt hesap)
//   GET /api/admin/ml                                       — admin
//   PUT /api/admin/ml                                       — admin (audit)
//
// Karar anomaly_verdicts'e 'ml:'+id anahtarıyla yazılır (mevcut altyapı);
// kind 'ml:<detector>' — eski anomali kararlarıyla parmak izi çakışmaz.
// Terfi (promotion) dilim 0'da yalnız AYAR: hiçbir dedektör satırı
// anomaly_events'e kopyalanmaz.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/ml"
)

func init() { registerRoutesExtra("ml", (*Server).registerMLRoutes) }

var mlReadRoles = []string{auth.RoleAdmin, auth.RoleEditor, auth.RoleViewer}

const (
	mlEventsTTL    = 30 * time.Second
	mlScorecardTTL = 60 * time.Second
	mlScoreWindow  = 30 * 24 * time.Hour
)

func (s *Server) registerMLRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ml/events", auth.RequireAnyRole(mlReadRoles, s.getMLEvents))
	mux.HandleFunc("GET /api/ml/scorecard", auth.RequireAnyRole(mlReadRoles, s.getMLScorecard))
	mux.HandleFunc("PUT /api/ml/{id}/verdict", auth.RequireAnyRole(editorRoles, s.putMLVerdict))
	mux.HandleFunc("GET /api/ml/{id}/replay", auth.RequireAnyRole(mlReadRoles, s.getMLReplay))
	mux.HandleFunc("POST /api/ml/backtest", auth.RequireAnyRole(editorRoles, s.postMLBacktest))
	mux.HandleFunc("GET /api/admin/ml", auth.RequireRole(auth.RoleAdmin, s.getMLConfig))
	mux.HandleFunc("PUT /api/admin/ml", auth.RequireRole(auth.RoleAdmin, s.putMLConfig))
}

// withMLStats — getSystemStats yanıtına `ml` bloğu (api.go tek satır).
func withMLStats(st *chstore.SystemStats) *chstore.SystemStats {
	if st != nil {
		st.ML = ml.Observability()
	}
	return st
}

// mlEventsKey — SAF: tüm girdiler anahtarda (since kovası, süzgeçler, özetin
// saat dilimi). v3 (v0.10.1843): satır `summary` taşır.
func mlEventsKey(sinceS int64, signal, detector, service, stage, tz string) string {
	return fmt.Sprintf("ml:events:v3:since=%d:sig=%s:d=%s:s=%s:st=%s:tz=%s", sinceS, signal, detector, service, stage, tz)
}

// mlEventRow — liste satırı: olay + sunucuda kurulan tek satır özet
// (ml.ExplainSummary; detay kartının özetiyle AYNI işlev).
type mlEventRow struct {
	chstore.MLEvent
	Summary string `json:"summary"`
}

// mlEventRows — SAF: olaylar → özetli satırlar.
func mlEventRows(items []chstore.MLEvent, cfg chstore.MLConfig, now time.Time, loc *time.Location) []mlEventRow {
	out := make([]mlEventRow, len(items))
	for i, e := range items {
		out[i] = mlEventRow{MLEvent: e, Summary: ml.ExplainSummary(ml.SummaryInput(e, cfg, now, loc))}
	}
	return out
}

func (s *Server) getMLEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since := 24 * time.Hour
	if d, err := time.ParseDuration(q.Get("since")); err == nil && d > 0 && d <= 30*24*time.Hour {
		since = d
	}
	signal, detector, service, stage := strings.TrimSpace(q.Get("signal")), strings.TrimSpace(q.Get("detector")), strings.TrimSpace(q.Get("service")), strings.TrimSpace(q.Get("stage"))
	if signal != "" && signal != chstore.MLSignalLog && signal != chstore.MLSignalTrace && signal != chstore.MLSignalMetric {
		signal = ""
	}
	loc := ml.OperatorLocation(s.store.AnomalySensitivityForDetectors())
	key := mlEventsKey(int64(since.Seconds()), signal, detector, service, stage, loc.String())
	s.serveCached(w, r, key, mlEventsTTL, func(ctx context.Context) (any, error) {
		items, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{
			Since: time.Now().Add(-since), Signal: signal, Detector: detector, Service: service, Stage: stage, Limit: chstore.MLListCap})
		if err != nil {
			return nil, err
		}
		rows := mlEventRows(items, s.store.GetMLConfig(ctx), time.Now(), loc)
		return map[string]any{"items": rows, "truncated": len(items) >= chstore.MLListCap}, nil
	})
}

func (s *Server) getMLScorecard(w http.ResponseWriter, r *http.Request) {
	s.serveCached(w, r, "ml:scorecard:v2:30d", mlScorecardTTL, func(ctx context.Context) (any, error) {
		rows, err := s.store.MLScorecard(ctx, time.Now().Add(-mlScoreWindow))
		if err != nil {
			return nil, err
		}
		if rows == nil {
			rows = []chstore.MLDetectorScore{}
		}
		cfg := s.store.GetMLConfig(ctx)
		signals := map[string]string{}
		for _, n := range chstore.MLDetectorNames {
			signals[n] = chstore.MLDetectorSignal(n)
		}
		// terfi kapısı: olay görmemiş dedektör de gerekçesiyle listelenir
		tz := s.store.AnomalySensitivityForDetectors().Calendar.Timezone
		byDet := map[string]chstore.MLDetectorScore{}
		for _, r := range rows {
			byDet[r.Detector] = r
		}
		gates := map[string]chstore.MLGate{}
		for _, n := range chstore.MLDetectorNames {
			gates[n] = cfg.PromotionGate(n, byDet[n], tz)
		}
		out := map[string]any{"windowDays": 30, "detectors": rows, "bySignal": chstore.MLSignalScores(rows),
			"promotion": cfg.Promotion, "gates": gates, "signals": signals}
		if ts, ok, _ := s.store.GetMLTickStatus(ctx); ok {
			out["tick"] = ts
		}
		if bs, ok, _ := s.store.GetMLBaselineStatus(ctx); ok { // v0.10.1814: günlük baseline işi
			out["baseline"] = bs
		}
		if bp, ok, _ := s.store.GetMLBackfillProgress(ctx, chstore.MLSignalLog); ok {
			out["backfill"] = []chstore.MLBackfillProgress{bp}
		}
		// v0.10.1829: log kalıbı maskesi sürümü + yeni-şablon (log_template_new,
		// rare_template) sessiz penceresi; yeni UI yok, durum yükünde görünür.
		if lm, ok, _ := s.store.GetLogMaskerState(ctx); ok {
			out["logMasker"] = lm
		}
		return out, nil
	})
}

// postMLBacktest — kalite çerçevesi backtest'i / what-if: kayıtlı ml_series
// üzerinde canlı dedektör kararını yeniden koşar, ortak etiket setiyle
// isabet/yakalama döner. Hiçbir şey YAZMAZ (audit yok; "bu ayarı uygula"
// admin PUT'undan geçer). config verilirse what-if (normalize edilir), yoksa
// kayıtlı ayar. Servis zorunlu, aralık ≤ 14 gün. Cache yok: POST + gövde.
func (s *Server) postMLBacktest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Signal  string            `json:"signal"`
		Service string            `json:"service"`
		From    int64             `json:"from"` // unix sn
		To      int64             `json:"to"`
		Config  *chstore.MLConfig `json:"config,omitempty"`
		// Overrides — what-if: kayıtlı ayarın üstüne yalnız bu eşikler
		// (editor tam blob'u göremez; GET /api/admin/ml admin-only).
		Overrides *struct {
			CriticalZ    *float64 `json:"criticalZ,omitempty"`
			DwellBuckets *int     `json:"dwellBuckets,omitempty"`
			ImpactMinRel *float64 `json:"impactMinRel,omitempty"`
		} `json:"overrides,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	body.Service = strings.TrimSpace(body.Service)
	if body.Service == "" || (body.Signal != chstore.MLSignalLog && body.Signal != chstore.MLSignalTrace && body.Signal != chstore.MLSignalMetric) {
		writeJSONError(w, http.StatusBadRequest, "signal (log|trace|metric) ve service zorunlu")
		return
	}
	to := time.Now()
	if body.To > 0 && body.To < to.Unix() {
		to = time.Unix(body.To, 0)
	}
	from := to.Add(-24 * time.Hour)
	if body.From > 0 && body.From < to.Unix() {
		from = time.Unix(body.From, 0)
	}
	cfg := s.store.GetMLConfig(r.Context())
	if body.Config != nil {
		cfg = chstore.NormalizeMLConfig(*body.Config)
	}
	if o := body.Overrides; o != nil {
		if o.CriticalZ != nil {
			cfg.CriticalZ = *o.CriticalZ
		}
		if o.DwellBuckets != nil {
			cfg.DwellBuckets = *o.DwellBuckets
		}
		if o.ImpactMinRel != nil {
			cfg.Quality.ImpactMinRel = *o.ImpactMinRel
		}
		cfg = chstore.NormalizeMLConfig(cfg)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := ml.Backtest(ctx, s.store, body.Signal, body.Service, from, to, cfg)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, res)
}

func (s *Server) putMLVerdict(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	var body struct {
		Verdict  string `json:"verdict"`
		Note     string `json:"note"`
		Detector string `json:"detector"`
		Service  string `json:"service"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	if id == "" || !chstore.ValidVerdict(body.Verdict) {
		writeJSONError(w, http.StatusBadRequest, "verdict 'anomaly' ya da 'not_anomaly' olmalı")
		return
	}
	kind := "ml:" + strings.TrimSpace(body.Detector)
	v := chstore.AnomalyVerdict{
		EventID: chstore.MLVerdictID(id), Fingerprint: verdictFingerprint(kind, id, body.Service),
		Kind: kind, Pattern: id, Service: body.Service,
		Verdict: body.Verdict, Note: truncRunes(strings.TrimSpace(body.Note), 500),
		CreatedBy: claimEmail(auth.FromContext(r.Context())), CreatedAt: time.Now().UnixNano(),
	}
	if err := s.store.UpsertAnomalyVerdict(r.Context(), v); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "ml:")
	details, _ := json.Marshal(map[string]string{"verdict": body.Verdict, "detector": body.Detector, "service": body.Service, "note": v.Note})
	s.audit(r, "ml.verdict", "ml_event", id, string(details))
	writeJSON(w, v)
}

func (s *Server) getMLConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.GetMLConfig(r.Context()))
}

func (s *Server) putMLConfig(w http.ResponseWriter, r *http.Request) {
	var c chstore.MLConfig
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	c = chstore.NormalizeMLConfig(c)
	before := s.store.GetMLConfig(r.Context())
	if err := s.store.SaveMLConfig(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	s.cacheInvalidatePrefix(r.Context(), "ml:")
	// audit: kim (claim), ne zaman (audit damgası), eski/yeni değer.
	details, _ := json.Marshal(map[string]any{"before": before, "after": c})
	s.audit(r, "ml.config", "settings", "ml", string(details))
	writeJSON(w, c)
}

// getMLReplay — olay anı kaydı (ml_event_snapshots) + aynı servisin ±2 sa
// içindeki diğer kayıtları + açıklama kartı (ml.BuildExplainCard, v0.10.1843).
// Kart canlı olay satırından (süre/durum güncel) + kayıttan (seri, bant,
// eşikler) + servis profilinden + son 24 sa yayınlarından kurulur. Kayıt
// yoksa (eski olay / TTL) kart yine döner, snapshot null; ikisi de yoksa 404.
// ES/rollup okuması YOK; yayınlar servis MV'sinden (GetServiceDeploys).
func (s *Server) getMLReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, "id zorunlu")
		return
	}
	loc := ml.OperatorLocation(s.store.AnomalySensitivityForDetectors())
	s.serveCached(w, r, mlReplayKey(id, loc.String()), mlReplayTTL, func(ctx context.Context) (any, error) {
		now := time.Now()
		snap, hasSnap, err := s.store.GetMLSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		evs, err := s.store.ListMLEvents(ctx, chstore.MLEventsFilter{ID: id, Since: now.Add(-mlEventTTLWindow), Limit: 1})
		if err != nil {
			return nil, err
		}
		src := ml.CardSources{Config: s.store.GetMLConfig(ctx), Now: now, Loc: loc, HrefOf: mlEventPath, DeployLookback: mlCardDeployLookback}
		var service string
		var anchor, last time.Time
		switch {
		case len(evs) > 0:
			src.Event = &evs[0]
			service, anchor, last = evs[0].Service, time.Unix(0, evs[0].StartedAt), time.Unix(0, evs[0].LastSeen)
		case hasSnap:
			service, anchor, last = snap.Service, time.Unix(0, snap.DetectedAt), time.Unix(0, snap.LastSeen)
		default:
			return nil, fmt.Errorf("%w: bu olay için kayıt yok", errNotFound)
		}
		var snapOut any // null: kayıt yok
		others := []chstore.MLSnapshot{}
		if hasSnap {
			src.Snapshot = &snap
			snapOut = snap
			anchor = time.Unix(0, snap.DetectedAt)
		}
		related, err := s.store.ListMLSnapshotsForService(ctx, service, anchor.Add(-2*time.Hour), anchor.Add(2*time.Hour))
		if err != nil {
			return nil, err
		}
		for _, x := range related {
			if x.EventID != id {
				others = append(others, x)
			}
		}
		src.Related = others
		// Yayın penceresi olaya bağlı (başlangıç − 24 sa … son görülme + 5 dk,
		// şimdiyi aşmaz): eski olayda "bugüne kadar" taraması olmasın.
		deployTo := last.Add(5 * time.Minute)
		if deployTo.After(now) || deployTo.Before(anchor) {
			deployTo = now
		}
		if ds, derr := s.store.GetServiceDeploys(ctx, service, anchor.Add(-mlCardDeployLookback), deployTo); derr == nil {
			src.Deploys, src.DeploysOK = ds, true
		}
		if row, ok, perr := s.store.MLLearnedOne(ctx, chstore.MLLearnedKindProfile, service); perr == nil && ok {
			src.Profile = ml.ServiceProfileFromRow(row).Profile
		}
		in, _ := ml.AssembleCardInput(src)
		return map[string]any{"snapshot": snapOut, "related": others, "card": ml.BuildExplainCard(in), "verdict": mlReplayVerdict(src.Event)}, nil
	})
}

const (
	mlReplayTTL          = 30 * time.Second
	mlEventTTLWindow     = 31 * 24 * time.Hour // ml_events TTL 30 g
	mlCardDeployLookback = 24 * time.Hour
)

// mlReplayVerdict — SAF: olay satırının etiketi (detay eylem kutusu viewer'a
// da gösterir); etiketsiz ya da satırsız olay → nil (JSON null).
func mlReplayVerdict(ev *chstore.MLEvent) map[string]any {
	if ev == nil || ev.Verdict == "" {
		return nil
	}
	return map[string]any{"verdict": ev.Verdict, "by": ev.VerdictBy, "at": ev.VerdictAt, "note": ev.Note}
}

// mlReplayKey — SAF: girdiler = olay id + kart saatlerinin dilimi. v2:
// yanıt `card` taşır, snapshot null olabilir.
func mlReplayKey(id, tz string) string {
	return fmt.Sprintf("ml:replay:v2:id=%s:tz=%s", id, tz)
}

// mlEventPath — SAF: ML olay detayı (FE lib/anomalyHref.ts mlEventHref ikizi).
func mlEventPath(id string) string {
	return "/anomalies/ml/" + url.PathEscape(id)
}
