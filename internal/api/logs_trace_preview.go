package api

// logs_trace_preview.go — v0.10.1881 (Log Search Faz 3 dilim 3.4; audit §4
// "Çip + önizleme", "Toplu önizleme okuması", "Örneklenmiş tespiti").
//
//	POST /api/logs/trace-preview   {items:[{id, tsNs}]}  → {traces:{<id>:{state,…}}, window}
//
// /logs sayfasındaki trace çiplerinin özeti — sayfa başına TEK ClickHouse
// sorgusu (chstore.TracePreviews: trace_summary_5m, dar pencere, LIMIT 200,
// max_execution_time 3). ES isteği YOK. Rol kapısı yok (salt-okunur; viewer da
// görür). serveCached 30 sn; anahtar normalize kimliklerin SIRALI FNV özeti +
// 5 dk'ya hizalı pencere (v0.5.187 — uzunluk değil içerik).
//
// Beş durum (audit §4 tablosu):
//
//	found        MV satırı var → ✓ / ✕ + süre
//	pending      satır yok, log 2 dk'dan yeni → "trace henüz yazılmadı"
//	sampled      satır yok, log 2 dk'dan eski, sorgu BAŞARILI → "örneklenmiş"
//	unrecognized kimlik hex32 / tireli UUID / 16-hex değil → çip yok
//	error        CH hatası / zaman aşımı → "önizleme alınamadı" (ASLA "örneklenmiş")
//
// Pencere tavanı 6 saat (en yeni logdan geriye). Log zamanı pencerenin dışında
// kalan kimlik SORGULANMAZ ve yanıtta YER ALMAZ (çip düz kalır): bakılmamış bir
// trace'e "örneklenmiş" denmez, geniş aralığa saçılmış bir örnek de ClickHouse'ta
// günlerce MV taratmaz (trace_id IN zaman tabanını daraltmaz — maliyet pencere
// genişliğiyle büyür).
//
// Neden POST: 200 kimlik GET sorgu dizisine sığmaz. api.go BÜYÜMEZ: rota bu
// dosyada, kayıt init()'te.

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("logs_trace_preview", (*Server).registerLogsTracePreviewRoutes) }

func (s *Server) registerLogsTracePreviewRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/logs/trace-preview", s.postLogsTracePreview)
}

const (
	tracePreviewTTL       = 30 * time.Second
	tracePreviewPad       = 15 * time.Minute // audit §4: log zamanları ∓ 15 dk
	tracePreviewBucket    = 5 * time.Minute
	tracePreviewMaxWindow = 6 * time.Hour
	tracePreviewPending   = 2 * time.Minute
	tracePreviewMaxBody   = 64 << 10
	tracePreviewTimeout   = 3 * time.Second
)

const (
	tpFound        = "found"
	tpPending      = "pending"
	tpSampled      = "sampled"
	tpUnrecognized = "unrecognized"
	tpError        = "error"
)

type tracePreviewItem struct {
	ID   string `json:"id"`
	TsNs int64  `json:"tsNs"`
}

type tracePreview struct {
	State       string  `json:"state"`
	RootService string  `json:"rootService,omitempty"`
	RootName    string  `json:"rootName,omitempty"`
	StartNs     int64   `json:"startNs,omitempty"`
	DurationMs  float64 `json:"durationMs,omitempty"`
	SpanCount   uint64  `json:"spanCount,omitempty"`
	ErrorCount  uint64  `json:"errorCount,omitempty"`
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != ""
}

// normalizeTraceID — SAF: log'daki yazım → CH'deki 32-hex (küçük harf; tireli
// UUID tiresiz; 16-hex sola sıfırla — trace_fallback.go ile aynı tolerans).
func normalizeTraceID(raw string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(raw))
	t = strings.TrimPrefix(t, "0x")
	if len(t) == 36 && strings.Count(t, "-") == 4 {
		t = strings.ReplaceAll(t, "-", "")
	}
	switch {
	case len(t) == 32 && isHex(t):
		return t, true
	case len(t) == 16 && isHex(t):
		return strings.Repeat("0", 16) + t, true
	}
	return "", false
}

// tracePreviewWindow — SAF: log zamanlarının en erken / en geç ∓ 15 dk, 5 dk
// kovaya hizalı (alt tabana, üst tavana); en çok 6 saat (en yeniden geriye).
func tracePreviewWindow(minNs, maxNs int64) (time.Time, time.Time) {
	lo := time.Unix(0, minNs).Add(-tracePreviewPad).Truncate(tracePreviewBucket)
	hi := time.Unix(0, maxNs).Add(tracePreviewPad)
	if t := hi.Truncate(tracePreviewBucket); !t.Equal(hi) {
		hi = t.Add(tracePreviewBucket)
	}
	if hi.Sub(lo) > tracePreviewMaxWindow {
		lo = hi.Add(-tracePreviewMaxWindow)
	}
	return lo.UTC(), hi.UTC()
}

// tracePreviewMissingState — SAF: satır yokken durum (sorgu başarılıydı).
func tracePreviewMissingState(logTsNs int64, now time.Time) string {
	if now.Sub(time.Unix(0, logTsNs)) < tracePreviewPending {
		return tpPending
	}
	return tpSampled
}

type tracePreviewPlan struct {
	norm   map[string]string // istemcinin yazımı → normalize kimlik
	latest map[string]int64  // normalize kimlik → en yeni log zamanı
	ids    []string          // sıralı normalize kimlikler (≤ tavan)
	raws   []string          // sıralı istemci yazımları (önbellek anahtarı — yanıt bu yazımlarla anahtarlı)
	minNs  int64
	maxNs  int64
}

// planTracePreview — SAF: girdi → normalize küme (tekil, sıralı, tavanlı).
func planTracePreview(items []tracePreviewItem) tracePreviewPlan {
	p := tracePreviewPlan{norm: map[string]string{}, latest: map[string]int64{}}
	for _, it := range items {
		raw := strings.TrimSpace(it.ID)
		if raw == "" {
			continue
		}
		n, ok := normalizeTraceID(raw)
		if !ok {
			p.norm[raw] = ""
			continue
		}
		if _, seen := p.latest[n]; !seen && len(p.latest) >= chstore.TracePreviewMaxIDs {
			continue
		}
		p.norm[raw] = n
		if it.TsNs > p.latest[n] {
			p.latest[n] = it.TsNs
		}
		if it.TsNs > 0 && (p.minNs == 0 || it.TsNs < p.minNs) {
			p.minNs = it.TsNs
		}
		if it.TsNs > p.maxNs {
			p.maxNs = it.TsNs
		}
	}
	for id := range p.latest {
		p.ids = append(p.ids, id)
	}
	sort.Strings(p.ids)
	for raw := range p.norm {
		p.raws = append(p.raws, raw)
	}
	sort.Strings(p.raws)
	return p
}

// tracePreviewKey — SAF: istemci yazımlarının SIRALI özeti (yanıt bu yazımlarla
// anahtarlı; aynı kimliğin farklı yazımı ayrı girdi) + hizalı pencere.
func tracePreviewKey(raws []string, lo, hi time.Time) string {
	return "logs-trace-preview:v1:" + fnvStr(append([]string{lo.Format(time.RFC3339), hi.Format(time.RFC3339), "\x02"}, raws...)...)
}

// tracePreviewCovered — SAF: kimliğin kendi ∓15 dk'sı sorgu penceresinin içinde
// mi (pencere 6 saatte kırpıldıysa eski loglar dışarıda kalır).
func tracePreviewCovered(logTsNs int64, lo time.Time) bool {
	return !time.Unix(0, logTsNs).Add(-tracePreviewPad).Before(lo)
}

// coveredIDs — SAF: pencerenin kapsadığı normalize kimlikler (sıra korunur).
func (p tracePreviewPlan) coveredIDs(lo time.Time) []string {
	out := make([]string, 0, len(p.ids))
	for _, id := range p.ids {
		if tracePreviewCovered(p.latest[id], lo) {
			out = append(out, id)
		}
	}
	return out
}

// buildTracePreviews — SAF: plan + sorgu sonucu → istemci yazımı başına durum.
// Pencerenin kapsamadığı kimlik çıktıda YOK (bakılmadı — durum uydurulmaz).
func buildTracePreviews(p tracePreviewPlan, lo time.Time, found map[string]chstore.TraceAggregateStub, qerr error, now time.Time) map[string]tracePreview {
	out := make(map[string]tracePreview, len(p.norm))
	for raw, n := range p.norm {
		if n == "" {
			out[raw] = tracePreview{State: tpUnrecognized}
			continue
		}
		if !tracePreviewCovered(p.latest[n], lo) {
			continue
		}
		if qerr != nil {
			out[raw] = tracePreview{State: tpError}
			continue
		}
		if st, ok := found[n]; ok {
			out[raw] = tracePreview{State: tpFound, RootService: st.RootService, RootName: st.RootName,
				StartNs: st.StartTimeNs, DurationMs: st.DurationMs, SpanCount: st.SpanCount, ErrorCount: st.ErrorCount}
			continue
		}
		out[raw] = tracePreview{State: tracePreviewMissingState(p.latest[n], now)}
	}
	return out
}

func (s *Server) postLogsTracePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Items []tracePreviewItem `json:"items"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, tracePreviewMaxBody)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	plan := planTracePreview(in.Items)
	if len(plan.ids) == 0 {
		writeJSON(w, map[string]any{"traces": buildTracePreviews(plan, time.Time{}, nil, nil, time.Now())})
		return
	}
	lo, hi := tracePreviewWindow(plan.minNs, plan.maxNs)
	s.serveCached(w, r, tracePreviewKey(plan.raws, lo, hi), tracePreviewTTL, func(ctx context.Context) (any, error) {
		var found map[string]chstore.TraceAggregateStub
		var qerr error
		if s.store == nil {
			qerr = errUpstream
		} else {
			tctx, cancel := context.WithTimeout(ctx, tracePreviewTimeout)
			found, qerr = s.store.TracePreviews(tctx, plan.coveredIDs(lo), lo, hi)
			cancel()
		}
		return map[string]any{
			"traces": buildTracePreviews(plan, lo, found, qerr, time.Now()),
			"window": map[string]int64{"fromNs": lo.UnixNano(), "toNs": hi.UnixNano()},
		}, nil
	})
}
