package mcptools

// evidence_link.go — v0.10.1272 (CoSRE adım 3/8): kanıt çipi + "Explore'da aç".
//
// query_metrics / query_traces / query_logs her sonuçta `evidence` taşır
// (cosre_query.go). Sohbet bu kanıtı çip olarak çizer; çipe tıklamak AYNI
// sorguyu AYNI pencerede (+ env/cluster) ürünün ilgili sayfasında açmalı.
//
// TEK SAF FONKSİYON: EvidenceHref(tool, evidence, request) → href. Mevcut
// üreticiler yeniden kullanılır (TracesDeepLink, buildLink); elle URL
// birleştirme yok, kodlama url.Values'tan. Girdi aracın KENDİ sonucudur
// (model metni değil): `request` sunucunun normalize ettiği argümanlar,
// `evidence.window` gerçekten okunan pencere (kapı daraltması dâhil).
//
// EŞLEME (kaynak → sayfa):
//
//	query_traces  · ClickHouse            → /traces (service, name=operation
//	                                        süzgeci, hasError, minMs, sort, env, range)
//	query_logs    · Elasticsearch|CH      → /logs (q, service, severity 13|17,
//	                                        traceId, env, cluster, range)
//	query_metrics · VictoriaMetrics RED   → /service Overview, src=metric (Overview
//	                                        METRİK kipi = aracın okuduğu okuyucunun ta kendisi;
//	                                        kaydedilen MetricsQL birden çok ifade —
//	                                        hiçbir sayfa ham MetricsQL'i URL'den almıyor)
//	query_metrics · ClickHouse span RED   → /service Overview, src=span
//	query_metrics · jvm_* (VM|CH)         → /service Infrastructure (runtime bölümü)
//	query_metrics · Thanos pod_*          → /service Infrastructure (servis varsa),
//	                                        yoksa /clusters?cluster=<ad>&section=pods.
//
// v0.10.1306 — PromQL EDİTÖRÜ önce gelir: kanıt `promql` türündeyse ve sorgu
// metni elimizdeyse (VictoriaMetrics ya da cluster'ı bilinen Thanos), çip
// /metrics?editor=1&tab=promql&promql=…&src=vm|thanos[&cluster=]&range=…
// açar — operatör aracın koştuğu SORGUNUN KENDİSİNİ görür ve düzenler
// (promqlEditorHref). VM kanıtında Query birden çok ifadenin "\n" ile
// birleşimi (query_metrics.go qmRunApp) → ilk boş olmayan satır. Thanos
// kanıtında Query zaten cluster matcher'lı (ScopedQuery); konsol
// EffectiveQuery'yi yeniden uygular ve enjeksiyon idempotent
// (internal/thanos/cluster_matcher.go). Sorgu yok, Thanos'ta cluster yok ya
// da sorgu 8 KiB'ı (maxPromQLQueryLen) aşıyor → yukarıdaki eski eşleme.
//
// Pencere daima mutlak `range=custom:<fromMs>-<toMs>` (build_link sözleşmesi).
// Eşleme kurulamazsa (servis yok, pencere bozuk) "" — çip yine çizilir,
// yalnız tıklanmaz (uydurma hedef yok).

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ChatEvidence — sohbet olaylarına (step-result.evidence, answer.evidence[])
// giden kanıt: aracın evidence alanı + hangi araç + "Explore'da aç" href'i.
type ChatEvidence struct {
	Tool       string         `json:"tool"`
	Source     string         `json:"source"`
	Query      string         `json:"query"`
	QueryKind  string         `json:"queryKind"`
	Window     EvidenceWindow `json:"window"`
	Env        string         `json:"env,omitempty"`
	Cluster    string         `json:"cluster,omitempty"`
	Rows       *int           `json:"rows,omitempty"`
	Series     *int           `json:"series,omitempty"`
	DurationMs int64          `json:"durationMs"`
	Href       string         `json:"href,omitempty"`
	// Ref — çipin sohbet içi bloğuna bağı (blok yükündeki evidenceRef aynı
	// değer; sunucu adım kimliğinden kurar: "ev<i>"). Boş = bloksuz kanıt.
	Ref string `json:"ref,omitempty"`
	// CtxApplied (v0.10.1274) — sunucunun bu çağrıya ekrandan koyduğu kapsam
	// (env/servis/pencere/trace); boşsa yazılmaz. api attach eder.
	CtxApplied *ScopeApplied `json:"ctxApplied,omitempty"`
	// Hop (v0.10.1275, CoSRE adım 6) — bileşik aracın (pivot_from_trace)
	// zincir halkası: trace|pods|jvm|logs|op_baseline. Atomik araçta boş
	// (omitempty — eski çip şekli aynen); FE "Trace › Pod" etiketini bundan kurar.
	Hop string `json:"hop,omitempty"`
}

// evidenceTools — kanıt zarfı taşıyan araçlar (adım 2'nin üçü).
// dilim 3b — endpoint_metrics (v2): VM PromQL kanıtı; href page_url'den (EvidenceHref "").
var evidenceTools = map[string]bool{queryMetricsName: true, queryTracesName: true, queryLogsName: true, EndpointMetricsName: true}

// compositeEvidenceTools — v0.10.1275: hop başına kanıt LİSTESİ (`evidences`)
// taşıyan bileşik araçlar.
// v0.10.1276 — service_health_sweep (bölüm başına kanıt: red · ops ·
// functions · pods · jvm · logs).
// v0.10.1284 — explain_log_spike (histogram · fields · patterns · samples).
var compositeEvidenceTools = map[string]bool{PivotFromTraceName: true, ServiceHealthSweepName: true, ExplainLogSpikeName: true}

// compositeEvidenceMax — bileşik aracın çip tavanı (hop sayısının üstü).
const compositeEvidenceMax = 6

// IsEvidenceTool — aracın sonucu kanıt zarfı taşır mı (atomik ya da bileşik).
func IsEvidenceTool(tool string) bool { return evidenceTools[tool] || compositeEvidenceTools[tool] }

// IsCompositeEvidenceTool — v0.10.1275: araç bileşik mi (sohbet ilerleme
// kancasını yalnız bunlara koyar).
func IsCompositeEvidenceTool(tool string) bool { return compositeEvidenceTools[tool] }

// ToolResultEvidenceList — v0.10.1275: aracın HAM sonucundan kanıt LİSTESİ.
// Atomik query_* → tek kalemli liste (ToolResultEvidence ile bayt-aynı kalem);
// bileşik → `evidences` sırasıyla, tavan compositeEvidenceMax, her kalemin
// href'i kendi eşlemesinden (EvidenceHref(As, …, Request)). Kanıt yok → false.
// SAF.
func ToolResultEvidenceList(tool, content string) ([]ChatEvidence, bool) {
	if evidenceTools[tool] {
		ev, ok := ToolResultEvidence(tool, content)
		if !ok {
			return nil, false
		}
		return []ChatEvidence{ev}, true
	}
	if !compositeEvidenceTools[tool] {
		return nil, false
	}
	var r struct {
		Tool      string             `json:"tool"`
		Evidences []cosreHopEvidence `json:"evidences"`
	}
	if err := json.Unmarshal([]byte(content), &r); err != nil || (r.Tool != "" && r.Tool != tool) {
		return nil, false
	}
	out := make([]ChatEvidence, 0, min(len(r.Evidences), compositeEvidenceMax))
	for _, h := range r.Evidences {
		if len(out) >= compositeEvidenceMax {
			break
		}
		if h.Source == "" {
			continue
		}
		ev := h.cosreEvidence
		out = append(out, ChatEvidence{
			Tool: tool, Source: ev.Source, Query: ev.Query, QueryKind: ev.QueryKind, Window: ev.Window,
			Env: ev.Env, Cluster: ev.Cluster, Rows: ev.Rows, Series: ev.Series, DurationMs: ev.DurationMs,
			Href: EvidenceHref(h.As, ev, h.Request), Hop: h.Hop,
		})
	}
	return out, len(out) > 0
}

// ToolResultEvidence — aracın HAM JSON sonucundan kanıt + href. Kanıt zarfı
// taşımayan araç, çözülemeyen içerik ya da kanıtsız sonuç → ok=false. Saf.
func ToolResultEvidence(tool, content string) (ChatEvidence, bool) {
	if !evidenceTools[tool] {
		return ChatEvidence{}, false
	}
	var r struct {
		Tool     string         `json:"tool"`
		Request  map[string]any `json:"request"`
		Evidence *cosreEvidence `json:"evidence"`
	}
	if err := json.Unmarshal([]byte(content), &r); err != nil || r.Evidence == nil || r.Evidence.Source == "" {
		return ChatEvidence{}, false
	}
	if r.Tool != "" && r.Tool != tool {
		return ChatEvidence{}, false
	}
	ev := *r.Evidence
	return ChatEvidence{
		Tool: tool, Source: ev.Source, Query: ev.Query, QueryKind: ev.QueryKind, Window: ev.Window,
		Env: ev.Env, Cluster: ev.Cluster, Rows: ev.Rows, Series: ev.Series, DurationMs: ev.DurationMs,
		Href: EvidenceHref(tool, ev, r.Request),
	}, true
}

// EvidenceHref — kanıt → ürün href'i (kök-göreli). SAF, tablo testli
// (evidence_link_test.go). Kurulamazsa "".
func EvidenceHref(tool string, ev cosreEvidence, req map[string]any) string {
	w := ev.Window
	if w.FromMs <= 0 || w.ToMs <= w.FromMs {
		return ""
	}
	from, to := time.UnixMilli(w.FromMs).UTC(), time.UnixMilli(w.ToMs).UTC()
	rangeParam := fmt.Sprintf("custom:%d-%d", w.FromMs, w.ToMs)
	service := reqString(req, "service")
	switch tool {
	case queryTracesName:
		_, coded := reqFilterExpr(req, qtCodeFilterKey)
		if service == "" && !coded { // v0.10.1302 — servissiz kod süzgeci de bağlanır
			return ""
		}
		f := chstore.TraceFilter{
			Service:  service,
			HasError: reqString(req, "status") == qtStatusError,
			MinMs:    reqNumber(req, "min_duration_ms"),
			Env:      ev.Env,
			Sort:     "time",
		}
		if reqString(req, "sort_by") == qtSortSlowest {
			f.Sort = "duration"
		}
		if cf, ok := reqFilterExpr(req, qtCodeFilterKey); ok {
			// v0.10.1297 — operasyon adı fonksiyon koduna çevrildi: link de kodla.
			f.Filters = []chstore.FilterExpr{cf}
		} else if op := reqString(req, "operation"); op != "" {
			// query_traces'in uyguladığı süzgecin AYNISI (name = operation).
			f.Filters = []chstore.FilterExpr{{Key: "name", Op: "=", Values: []string{op}}}
		}
		return TracesDeepLink(f, from, to)
	case queryLogsName:
		traceID := reqString(req, "trace_id")
		if service == "" && traceID == "" {
			return ""
		}
		sev := 0
		switch reqString(req, "severity") {
		case qlSeverityWarn:
			sev = qlOTelWarnFloor
		case qlSeverityError:
			sev = qlOTelErrorFloor
		}
		// /logs `cluster` ham logstore süzgecidir (api_logs.go Filter.Cluster) —
		// query_logs'un okuduğu değerle aynı; Remote Cluster kimliği DEĞİL.
		return evidenceBuildLink(buildLinkArgs{
			Page: "logs", Query: reqString(req, "text"), Service: service, Severity: sev,
			TraceID: traceID, Env: ev.Env,
		}, ev.Cluster, rangeParam)
	case "trace":
		// v0.10.1275 — bileşik kanıtın trace halkası: Trace sayfası, aynı pencere.
		tid := reqString(req, "trace_id")
		if tid == "" {
			return ""
		}
		return evidenceBuildLink(buildLinkArgs{Page: "trace", TraceID: tid}, "", rangeParam)
	case "pod":
		// v0.10.1275 — pod halkası: Pod sayfası (cluster = Remote Cluster kimliği).
		pod := reqString(req, "pod")
		if pod == "" {
			return ""
		}
		return evidenceBuildLink(buildLinkArgs{Page: "pod", Pod: pod, Namespace: reqString(req, "namespace"), Service: service},
			reqString(req, "cluster"), rangeParam)
	case queryMetricsName:
		// v0.10.1306 — sorgunun kendisi PromQL editöründe (servis dallarından önce).
		if href := promqlEditorHref(ev, rangeParam); href != "" {
			return href
		}
		family := reqString(req, "family")
		if ev.Source == evSourceThanos {
			if service == "" {
				if ev.Cluster == "" {
					return ""
				}
				// Clusters sayfası ?cluster= değerini ClusterByRef ile çözer (id ya da ad).
				return evidenceBuildLink(buildLinkArgs{Page: "clusters", Tab: "pods"}, ev.Cluster, rangeParam)
			}
			return evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "infra", Env: ev.Env}, "", rangeParam)
		}
		if service == "" {
			return ""
		}
		if qmKindOf(family) == qmRuntime {
			return evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "infra", Env: ev.Env}, "", rangeParam)
		}
		src := "span"
		if ev.Source == evSourceVM {
			src = "metric"
		}
		return withQueryParam(evidenceBuildLink(buildLinkArgs{Page: "service", Service: service, Tab: "overview", Env: ev.Env}, "", rangeParam), "src", src)
	}
	return ""
}

// promqlEditorMaxLen — internal/api maxPromQLQueryLen'in aynası: hem
// /api/metrics/promql (VM) hem konsol /api/promql/query_range 8 KiB'ı aşan
// sorguyu reddeder; reddedilecek bir sorguyu editöre taşımak yerine eski
// eşleme (v0.10.1306).
const promqlEditorMaxLen = 8192

// promqlEditorHref — v0.10.1306, SAF: promql kanıtı → /metrics PromQL editörü.
// VM → src=vm, Query'nin ilk boş olmayan satırı; Thanos → src=thanos +
// cluster (ev.Cluster, ad — konsol ClusterByRef ile id ya da ad çözer),
// cluster yoksa "". Boş / 8 KiB üstü sorgu ya da başka kaynak → "" (çağıran
// eski eşlemeye düşer). Kodlama url.Values'tan; elle birleştirme yok.
func promqlEditorHref(ev cosreEvidence, rangeParam string) string {
	if ev.QueryKind != evKindPromQL {
		return ""
	}
	var src, q, cluster string
	switch ev.Source {
	case evSourceVM:
		src, q = "vm", firstNonEmptyLine(ev.Query)
	case evSourceThanos:
		cluster = strings.TrimSpace(ev.Cluster)
		if cluster == "" {
			return ""
		}
		src, q = "thanos", strings.TrimSpace(ev.Query)
	default:
		return ""
	}
	if q == "" || len(q) > promqlEditorMaxLen {
		return ""
	}
	v := url.Values{}
	v.Set("editor", "1")
	v.Set("tab", "promql")
	v.Set("promql", q)
	v.Set("src", src)
	if cluster != "" {
		v.Set("cluster", cluster)
	}
	v.Set("range", rangeParam)
	return "/metrics?" + v.Encode()
}

// PromQLEditorHref — v0.10.1386: promqlEditorHref'in dış kapısı (CoSRE
// metric_chart kanıt çipi + "Metrics'te aç"). src "vm" | "thanos" (Thanos
// cluster ister); pencere unix ms, mutlak `range=custom:<from>-<to>`. Bozuk
// pencere / bilinmeyen kaynak / boş ya da 8 KiB üstü sorgu → "". SAF.
func PromQLEditorHref(query, src, cluster string, fromMs, toMs int64) string {
	if fromMs <= 0 || toMs <= fromMs {
		return ""
	}
	ev := cosreEvidence{QueryKind: evKindPromQL, Query: query, Cluster: cluster}
	switch src {
	case "vm":
		ev.Source = evSourceVM
	case "thanos":
		ev.Source = evSourceThanos
	default:
		return ""
	}
	return promqlEditorHref(ev, fmt.Sprintf("custom:%d-%d", fromMs, toMs))
}

// firstNonEmptyLine — ilk boş olmayan satır (kırpılmış); yoksa "".
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}

// evidenceBuildLink — buildLink; hata = "" (kanıt çipi tıklanmaz, uydurulmaz).
func evidenceBuildLink(a buildLinkArgs, clusterID, rangeParam string) string {
	href, err := buildLink(a, clusterID, nil, rangeParam)
	if err != nil {
		return ""
	}
	return href
}

// withQueryParam — üretilmiş href'e bir parametre ekler (url.Values ile yeniden
// kodlar; buildLink'in kapsamadığı sayfa paramı için — Overview ?src=).
func withQueryParam(href, k, v string) string {
	if href == "" || v == "" {
		return href
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	q := u.Query()
	q.Set(k, v)
	u.RawQuery = q.Encode()
	return u.String()
}

// reqString — normalize edilmiş istekten dize (JSON'dan gelen sayı da metne).
// reqFilterExpr — v0.10.1297, SAF: istekteki süzgeç (FilterExpr ya da JSON'dan
// çözülmüş map); anahtar/değer boşsa ya da süzgeç geçersizse ok=false.
func reqFilterExpr(req map[string]any, k string) (chstore.FilterExpr, bool) {
	v, ok := req[k]
	if !ok || v == nil {
		return chstore.FilterExpr{}, false
	}
	var f chstore.FilterExpr
	if fe, isFE := v.(chstore.FilterExpr); isFE {
		f = fe
	} else {
		b, err := json.Marshal(v)
		if err != nil || json.Unmarshal(b, &f) != nil {
			return chstore.FilterExpr{}, false
		}
	}
	if f.Key == "" || len(f.Values) == 0 || chstore.ValidateFilters([]chstore.FilterExpr{f}) != nil {
		return chstore.FilterExpr{}, false
	}
	return f, true
}

func reqString(req map[string]any, k string) string {
	switch v := req[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return fmt.Sprintf("%g", v)
	}
	return ""
}

// reqNumber — normalize edilmiş istekten sayı (yoksa 0).
func reqNumber(req map[string]any, k string) float64 {
	if v, ok := req[k].(float64); ok && v > 0 {
		return v
	}
	return 0
}
