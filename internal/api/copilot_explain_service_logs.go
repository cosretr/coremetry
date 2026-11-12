package api

// copilot_explain_service_logs.go — Log Search Faz 6 dilim 6.7 (audit §7
// "Servis hızlı özeti": "Ortak deterministik katman: desen özeti + önceki
// pencere farkı. … LLM yalnız anlatır."; §8 kabul: "Aynı fonksiyon iki yerde
// (test)").
//
//   POST /api/copilot/explain-service-logs?service=&env=&from=&to=
//
// Servis sayfasının Logs sekmesindeki "Log özeti": servisin penceredeki
// loglarının üç cümlelik durumu. Kanıt "Bu logları açıkla" (6.3) ile AYNI
// katmandan: buildLogDigest + renderLogDigestTR (logs_ai_digest.go) — iki
// yüzey aynı sayıyı aynı dille görür (copilot_explain_service_logs_test.go
// çiviler). Arama / seviye süzgeci bilerek YOK: özet servisin tüm loglarıdır.
//
// Login Briefing'in takım log satırı (copilot_team_logs.go) DEĞİŞMEDİ —
// mevcut yüzeyin davranışı; ortak katmana bağlanması operatör kararı.
// ES: yalnız tıkla, 2 `_search` (pencere + önceki pencere, servis indeksi).

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/cilcenk/coremetry/internal/copilot"
)

const serviceLogsPatLinks = 4

// serviceLogsSummaryQuery — SAF: uca gelen sorgudan yalnız servis özeti
// paramları (arama / seviye / trace süzgeci düşer; logsPatternsFilter'a gider).
func serviceLogsSummaryQuery(q url.Values) (url.Values, bool) {
	svc := strings.TrimSpace(q.Get("service"))
	if svc == "" {
		return nil, false
	}
	out := url.Values{"service": {svc}}
	for _, k := range []string{"env", "cluster", "from", "to"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			out.Set(k, v)
		}
	}
	return out, true
}

// renderServiceLogSummaryTR — SAF: servis başlığı + ortak kanıt bölümü.
func renderServiceLogSummaryTR(service string, d logDigest) string {
	return "SERVİS: " + oneLineSample(service, 120) + "\n" + renderLogDigestTR(d)
}

func (s *Server) copilotExplainServiceLogs(w http.ResponseWriter, r *http.Request) {
	if s.logs == nil {
		writeJSONError(w, http.StatusServiceUnavailable, errExplainLogsUnconfigured.Error())
		return
	}
	q, ok := serviceLogsSummaryQuery(r.URL.Query())
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "service gerekli")
		return
	}
	r = withRouteQueryTag(r)
	f, ok := s.logsPatternsFilter(w, r, q)
	if !ok {
		return
	}
	d := s.buildLogDigest(r.Context(), f)
	if d.unavailable() {
		if !d.slow() {
			writeErr(w, d.CurErr)
			return
		}
		writeJSON(w, map[string]any{"explanation": "", "degraded": true, "reason": "log backend slow/unreachable",
			"patterns": []logsExplainPatternLink{}})
		return
	}
	if len(d.Cur.Groups) == 0 {
		writeJSON(w, map[string]any{"explanation": "", "sampled": 0, "patterns": []logsExplainPatternLink{}})
		return
	}
	r, xid := withExchange(r)
	out, err := s.copilotExplain(r, copilot.SystemPromptServiceLogSummary(), renderServiceLogSummaryTR(f.Service, d))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]any{"explanation": out, "exchangeId": xid, "sampled": d.Cur.Sampled,
		"patterns": digestPatternLinks(d, serviceLogsPatLinks)})
}
