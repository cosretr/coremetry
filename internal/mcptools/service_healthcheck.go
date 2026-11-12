package mcptools

// service_healthcheck.go — v0.10.2039 (servis sağlık kontrolü, dilim 1;
// spec docs/plans/spec-service-healthcheck-runbook.md, operatör onayı
// 2026-11-12).
//
// Operatör: "Servis genel kontrollerini yapan bir runbook olsun; pod
// metriklerini, cluster sağlıklarını, datasource metriklerini kontrol edip
// sorun olup olmadığını söylesin. … Service healthcheck gibi olsun."
//
// TEK çekirdek, dört tüketici: GET /api/services/{name}/healthcheck (servis
// sayfası düğmesi + /runbooks yerleşik satırı), CoSRE niyeti ve MCP aracı.
// Hepsi ReadServiceHealthcheck'i çağırır.
//
// Neden service_health_sweep'in TAMAMI değil: tarama operasyon / fonksiyon /
// JVM / ham-span gecikme doğrulaması / grafik serileri ve evre-2 log okuması
// (Elasticsearch) da koşar — sağlık kontrolünün kataloğunda olmayan yük.
// Burada taramanın OKUYUCULARI (sweepReaders) ve bölüm kuralları (k8s bekleme
// sebepleri, deploy başarısızlık sözlüğü) yeniden kullanılır, yalnız
// katalogdaki kontroller koşar. Varsayılan koşuda ES YOK.
//
// Kontrol (dilim 1)            kaynak                                   sorgu
//
//	1 hata oranı + p95           service_summary_5m (şimdiki + önceki)    2 CH
//	2 açık problem               Inbox çekirdeği (servis şeridi kümesi)   1 (paylaşımlı, önbellekli)
//	3 süren anomali              ml_events FINAL (+ karar zenginleştirme) 2 CH
//	4 son deploy                 ListDeploymentsWindow                    ≤3 CH
//	5 exception P1/P2            2 ile AYNI okuma                         0
//	6-8 pod restart/bekleyen/kaynak  Thanos PodMetrics + restart artışı + bekleme sebebi   13 / cluster
//	   (+ servisin cluster'ları: span cluster kırılımı                    1 CH)
//	9 hazır replika (v0.10.2042) kube-state Deployment/StatefulSet       1 / cluster
//	10 cluster sağlığı (v0.10.2042) node Ready + firing ALERTS           2 / cluster
//	   (9-10 pod okumasının ardından, aynı cluster tavanında paralel; yalnız pod'u olan cluster)
//
// Hüküm KURALLARDAN çıkar (model yok). Durum: ok (İyi) / warn (Dikkat) /
// crit (Sorun) / unknown (Ölçülemedi). Ölçülemedi ASLA İyi sayılmaz.
// Bütçe: toplam 15 sn (taramanınki), her kaynak kendi tavanında paralel;
// süresi dolan kontrol "zaman aşımı" ile Ölçülemedi olur, koşu kısmi.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// ServiceHealthcheckName — tüketicilerin ortak adı (MCP aracı, kanıt).
const ServiceHealthcheckName = "service_healthcheck"

// Durumlar.
const (
	HCOK      = "ok"
	HCWarn    = "warn"
	HCCrit    = "crit"
	HCUnknown = "unknown"
)

// Kontrol kimlikleri (katalog sırası = tablo sırası).
const (
	HCCheckRED          = "red"
	HCCheckProblems     = "problems"
	HCCheckAnomalies    = "anomalies"
	HCCheckDeploys      = "deploys"
	HCCheckExceptions   = "exceptions"
	HCCheckPodRestarts  = "pod_restarts"
	HCCheckPodWaiting   = "pod_waiting"
	HCCheckPodResources = "pod_resources"
	HCCheckReplicas     = "replicas" // v0.10.2042
	HCCheckCluster      = "cluster"  // v0.10.2042
)

// HCCatalogVersion — önbellek anahtarına girer: kontrol kümesi / eşikler
// değişince eski gövde servis edilmez.
const HCCatalogVersion = "hc-v2"

// Eşikler (spec kontrol kataloğu; ayar YOK — operatör "ayarda vida değil
// varsayılan" dedi).
const (
	hcErrWarnPct      = 2.0
	hcErrCritPct      = 5.0
	hcErrMinCount     = 5  // bundan az hatalı span oranı hükme taşımaz (düşük trafik gürültüsü)
	hcLatMinSpans     = 20 // p95 kıyası için iki pencerede de en az bu kadar span
	hcLatWarnRatio    = 1.5
	hcLatCritRatio    = 2.0
	hcLatMinDeltaMs   = 50 // 4 → 8 ms "2×" bir sorun değildir
	hcMemWarnPct      = 80.0
	hcMemCritPct      = 95.0
	hcCPUWarnPct      = 90.0
	hcRestartCrit     = 3
	hcDeployRecent    = 30 * time.Minute
	hcDeployLookback  = 2 * time.Hour
	hcAnomalyOngoing  = 10 * time.Minute
	hcAnomalyReadMax  = 50
	hcBudget          = 15 * time.Second
	hcSeenRunes       = 200
	hcErrRunes        = 120
	hcShortListMax    = 3
	hcDefaultWindow   = "15m"
	hcMaxPodClusters  = sweepAllClusterMax
	hcPodNamesInSeen  = 2
	hcTitlesInSeenMax = 2
)

// HealthcheckRequest — normalize girdi.
type HealthcheckRequest struct {
	Service string
	Env     string // yalnız RED'e uygulanır (diğer kaynaklar ortam boyutu taşımaz)
	Window  string // 15m | 1h | 6h
}

// HealthcheckRow — tablonun bir satırı.
type HealthcheckRow struct {
	Check     string `json:"check"`
	Title     string `json:"title"`
	Subject   string `json:"subject,omitempty"` // cluster / datasource
	State     string `json:"state"`
	Seen      string `json:"seen"`
	Link      string `json:"link,omitempty"`
	LinkLabel string `json:"linkLabel,omitempty"`
	Evidence  string `json:"evidence,omitempty"` // PromQL / SQL (kapalı ayrıntı)
	Short     string `json:"short,omitempty"`    // özet cümlesindeki kısa ad (yalnız sapan satır)
}

// HealthcheckCounts — durum sayıları.
type HealthcheckCounts struct {
	OK      int `json:"ok"`
	Warn    int `json:"warn"`
	Crit    int `json:"crit"`
	Unknown int `json:"unknown"`
}

// HealthcheckResult — koşunun tamamı.
type HealthcheckResult struct {
	Service    string            `json:"service"`
	Env        string            `json:"env,omitempty"`
	Window     string            `json:"window"`
	Verdict    string            `json:"verdict"`
	Sentence   string            `json:"sentence"`
	Rows       []HealthcheckRow  `json:"rows"`
	Counts     HealthcheckCounts `json:"counts"`
	MeasuredAt int64             `json:"measuredAt"` // unix ms
	Partial    bool              `json:"partial"`
	DurationMs int64             `json:"durationMs"`
}

// HCNormalizeWindow — SAF: bilinmeyen / boş → 15m.
func HCNormalizeWindow(w string) string {
	switch w = strings.TrimSpace(w); w {
	case "15m", "1h", "6h":
		return w
	}
	return hcDefaultWindow
}

// ── okuyucular ──────────────────────────────────────────────────────────

type hcReaders struct {
	sweepReaders
	// mlEvents — servisin son ML olayları (karar zenginleştirmeli). nil = yok.
	mlEvents func(ctx context.Context, service string, since time.Time) ([]chstore.MLEvent, error)
}

func hcReadersFrom(d Deps) hcReaders {
	r := hcReaders{sweepReaders: sweepReadersFrom(d)}
	if st := d.Store; st != nil {
		r.mlEvents = func(ctx context.Context, service string, since time.Time) ([]chstore.MLEvent, error) {
			return st.ListMLEvents(ctx, chstore.MLEventsFilter{Service: service, Since: since, Limit: hcAnomalyReadMax})
		}
	}
	return r
}

// hcCaps — kaynak başına tavan (test dikişi).
type hcCaps struct {
	red, inbox, anomalies, deploys, pods time.Duration
}

var hcDefaultCaps = hcCaps{red: sweepCapRED, inbox: sweepCapProblems, anomalies: 3 * time.Second,
	deploys: sweepCapDeploys, pods: sweepCapPods + 2*time.Second}

// ReadServiceHealthcheck — koşunun tamamı. Hata yalnız çağıran iptal
// ettiğinde ya da servis boşken döner; her okuma arızası satır durumuna
// (Ölçülemedi) çevrilir.
func ReadServiceHealthcheck(ctx context.Context, d Deps, req HealthcheckRequest) (HealthcheckResult, error) {
	return runServiceHealthcheck(ctx, hcReadersFrom(d), req, hcDefaultCaps, hcBudget)
}

// ErrHealthcheckNoService — servis adı boş.
var ErrHealthcheckNoService = errors.New("service gerekli")

func runServiceHealthcheck(ctx context.Context, r hcReaders, req HealthcheckRequest, caps hcCaps, budget time.Duration) (HealthcheckResult, error) {
	elapsed := stopwatch()
	req.Service = strings.TrimSpace(req.Service)
	req.Env = strings.TrimSpace(req.Env)
	req.Window = HCNormalizeWindow(req.Window)
	if req.Service == "" {
		return HealthcheckResult{}, ErrHealthcheckNoService
	}
	now := wallNow().UTC()
	from := now.Add(-time.Duration(cosreWindowSeconds[req.Window]) * time.Second)
	deadline := now.Add(budget)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}

	type job struct {
		name string
		cap  time.Duration
		run  func(context.Context) []HealthcheckRow
		// timeout — tavan dolduğunda satırlar (kontroller Ölçülemedi).
		timeout func() []HealthcheckRow
	}
	svc := req.Service
	unk := func(check string, seen string) func() []HealthcheckRow {
		return func() []HealthcheckRow { return []HealthcheckRow{hcUnknownRow(svc, check, "", seen)} }
	}
	jobs := []job{
		{"red", caps.red, func(c context.Context) []HealthcheckRow {
			if r.windowRED == nil {
				return []HealthcheckRow{hcUnknownRow(svc, HCCheckRED, "", "ClickHouse okuyucusu bağlı değil")}
			}
			cur, err := r.windowRED(c, svc, req.Env, from, now)
			var prior chstore.ServiceWindowRED
			priorOK := false
			if err == nil {
				if p, perr := r.windowRED(c, svc, req.Env, from.Add(-now.Sub(from)), from); perr == nil {
					prior, priorOK = p, true
				}
			}
			row := hcClassifyRED(svc, cur, prior, priorOK, err)
			row.Evidence = inlineSQL(chstore.ServiceWindowREDSQL(req.Env != ""), sweepREDArgs(svc, req.Env, from, now)...)
			return []HealthcheckRow{row}
		}, unk(HCCheckRED, "zaman aşımı — okunamadı")},
		{"inbox", caps.inbox, func(c context.Context) []HealthcheckRow {
			if r.openItems == nil {
				err := errors.New("inbox okuyucusu bağlı değil")
				return []HealthcheckRow{hcClassifyProblems(svc, InboxOpenResult{}, err), hcClassifyExceptions(svc, InboxOpenResult{}, err)}
			}
			oi, err := r.openItems(c, ServiceAttentionQuery(svc))
			return []HealthcheckRow{hcClassifyProblems(svc, oi, err), hcClassifyExceptions(svc, oi, err)}
		}, func() []HealthcheckRow {
			return []HealthcheckRow{hcUnknownRow(svc, HCCheckProblems, "", "zaman aşımı — okunamadı"),
				hcUnknownRow(svc, HCCheckExceptions, "", "zaman aşımı — okunamadı")}
		}},
		{"anomalies", caps.anomalies, func(c context.Context) []HealthcheckRow {
			if r.mlEvents == nil {
				return []HealthcheckRow{hcUnknownRow(svc, HCCheckAnomalies, "", "anomali okuyucusu bağlı değil")}
			}
			evs, err := r.mlEvents(c, svc, now.Add(-hcAnomalyOngoing))
			return []HealthcheckRow{hcClassifyAnomalies(svc, evs, now, err)}
		}, unk(HCCheckAnomalies, "zaman aşımı — okunamadı")},
		{"deploys", caps.deploys, func(c context.Context) []HealthcheckRow {
			if r.deploys == nil {
				return []HealthcheckRow{hcUnknownRow(svc, HCCheckDeploys, "", "deploy okuyucusu bağlı değil")}
			}
			rows, _, err := r.deploys(c, svc, now.Add(-hcDeployLookback), now)
			return []HealthcheckRow{hcClassifyDeploys(svc, rows, now, err)}
		}, unk(HCCheckDeploys, "zaman aşımı — okunamadı")},
		{"pods", caps.pods, func(c context.Context) []HealthcheckRow {
			return hcRunPods(c, r, svc, from, now)
		}, func() []HealthcheckRow {
			return hcClassifyPodsOpts(svc, ServicePodsResult{Thanos: true}, context.DeadlineExceeded, false, hcAllPodChecks)
		}},
	}

	outs := make([][]HealthcheckRow, len(jobs))
	got := make([]bool, len(jobs))
	type done struct {
		i    int
		rows []HealthcheckRow
	}
	ch := make(chan done, len(jobs))
	pending := 0
	for i, j := range jobs {
		dl := hopDeadline(time.Until(deadline), j.cap)
		if dl <= 0 {
			continue
		}
		pending++
		go func(i int, j job, dl time.Duration) {
			hctx, cancel := context.WithTimeout(ctx, dl)
			defer cancel()
			ch <- done{i, j.run(hctx)}
		}(i, j, dl)
	}
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
wait:
	for pending > 0 {
		select {
		case dn := <-ch:
			outs[dn.i], got[dn.i] = dn.rows, true
			pending--
		case <-timer.C:
			break wait
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.Canceled) {
				return HealthcheckResult{}, ctx.Err()
			}
			break wait
		}
	}
	partial := false
	for i, j := range jobs {
		if !got[i] {
			outs[i] = j.timeout()
			partial = true
		}
	}
	rows := hcOrderRows(outs)
	for i := range rows {
		rows[i].Seen = truncateRunes(rows[i].Seen, hcSeenRunes)
		if rows[i].State == HCUnknown && strings.Contains(rows[i].Seen, "zaman aşımı") {
			partial = true
		}
	}
	verdict, sentence, counts := hcSummarize(svc, rows)
	return HealthcheckResult{Service: svc, Env: req.Env, Window: req.Window, Verdict: verdict, Sentence: sentence,
		Rows: rows, Counts: counts, MeasuredAt: now.UnixMilli(), Partial: partial, DurationMs: elapsed()}, nil
}

// hcCheckOrder — katalog sırası.
var hcCheckOrder = map[string]int{
	HCCheckRED: 1, HCCheckProblems: 2, HCCheckAnomalies: 3, HCCheckDeploys: 4, HCCheckExceptions: 5,
	HCCheckPodRestarts: 6, HCCheckPodWaiting: 7, HCCheckPodResources: 8,
	HCCheckReplicas: 9, HCCheckCluster: 10,
}

// hcOrderRows — SAF: işlerin satırları → katalog sırası (aynı kontrolde
// iş içi sıra korunur: cluster sırası).
func hcOrderRows(outs [][]HealthcheckRow) []HealthcheckRow {
	var rows []HealthcheckRow
	for _, o := range outs {
		rows = append(rows, o...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return hcCheckOrder[rows[i].Check] < hcCheckOrder[rows[j].Check] })
	if rows == nil {
		rows = []HealthcheckRow{}
	}
	return rows
}

// ── satır kurucular (SAF) ───────────────────────────────────────────────

var hcTitles = map[string]string{
	HCCheckRED:          "Hata oranı ve gecikme",
	HCCheckProblems:     "Açık problem",
	HCCheckAnomalies:    "Süren anomali",
	HCCheckDeploys:      "Son deploy",
	HCCheckExceptions:   "Exception (P1/P2)",
	HCCheckPodRestarts:  "Pod restart ve OOM",
	HCCheckPodWaiting:   "Bekleyen pod",
	HCCheckPodResources: "Pod CPU / bellek limiti",
	HCCheckReplicas:     "Hazır replika",
	HCCheckCluster:      "Cluster sağlığı",
}

func hcServiceHref(service, tab string) string {
	h := "/service?name=" + url.QueryEscape(service)
	if tab != "" {
		h += "&tab=" + tab
	}
	return h
}

// hcBase — kontrolün sabit alanları (başlık + varsayılan bağlantı).
func hcBase(service, check, subject string) HealthcheckRow {
	row := HealthcheckRow{Check: check, Title: hcTitles[check], Subject: subject}
	switch check {
	case HCCheckRED:
		row.Link, row.LinkLabel = hcServiceHref(service, ""), "Servis özeti"
	case HCCheckProblems, HCCheckExceptions:
		row.Link, row.LinkLabel = ServiceInboxHref(service), "Inbox"
	case HCCheckAnomalies:
		row.Link, row.LinkLabel = "/anomalies?service="+url.QueryEscape(service), "Anomaliler"
	case HCCheckDeploys:
		row.Link, row.LinkLabel = "/rollouts", "Rollouts"
	case HCCheckPodRestarts, HCCheckPodWaiting, HCCheckPodResources, HCCheckReplicas:
		row.Link, row.LinkLabel = hcServiceHref(service, "infra"), "Infrastructure"
	case HCCheckCluster:
		row.Link, row.LinkLabel = "/clusters", "Clusters"
	}
	return row
}

func hcUnknownRow(service, check, subject, seen string) HealthcheckRow {
	row := hcBase(service, check, subject)
	row.State, row.Seen = HCUnknown, seen
	return row
}

// hcErrSeen — SAF: okuma hatası → "Ne görüldü" metni.
func hcErrSeen(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || cosreClassify(err) == cosreErrTimeout {
		return "zaman aşımı — okunamadı"
	}
	return "okunamadı: " + truncateRunes(err.Error(), hcErrRunes)
}

// hcWorse — SAF: iki durumun kötüsü (crit > warn > ok; unknown en az).
func hcWorse(a, b string) string {
	rank := map[string]int{HCCrit: 3, HCWarn: 2, HCOK: 1, HCUnknown: 0}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// hcClassifyRED — kontrol 1. Hata oranı ≥%2 Dikkat / ≥%5 Sorun (en az 5
// hatalı span); p95 önceki eş pencerenin 1.5× Dikkat / 2× Sorun (iki pencerede
// ≥20 span ve ≥50 ms mutlak artış). Yük (istek hızı) hükme girmez — batch
// servislerinde yük artışı sorun sayılmaz kuralı böylece kendiliğinden tutar.
func hcClassifyRED(service string, cur, prior chstore.ServiceWindowRED, priorOK bool, err error) HealthcheckRow {
	row := hcBase(service, HCCheckRED, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	if cur.Spans == 0 {
		row.State, row.Seen = HCUnknown, "bu pencerede servisten span yok"
		return row
	}
	er := 100 * float64(cur.Errors) / float64(cur.Spans)
	row.State = HCOK
	parts := []string{fmt.Sprintf("hata oranı %%%.2f (%d / %d span)", er, cur.Errors, cur.Spans)}
	var shorts []string
	if cur.Errors >= hcErrMinCount {
		switch {
		case er >= hcErrCritPct:
			row.State = HCCrit
			shorts = append(shorts, fmt.Sprintf("hata oranı %%%.1f", er))
		case er >= hcErrWarnPct:
			row.State = HCWarn
			shorts = append(shorts, fmt.Sprintf("hata oranı %%%.1f", er))
		}
	}
	lat := fmt.Sprintf("p95 %.0f ms", cur.P95Ms)
	switch {
	case !priorOK:
		lat += " (önceki pencere okunamadı — kıyas yok)"
	case prior.Spans < hcLatMinSpans || cur.Spans < hcLatMinSpans || prior.P95Ms <= 0:
		lat += " (önceki pencerede yeterli trafik yok — kıyas yok)"
	default:
		ratio := cur.P95Ms / prior.P95Ms
		lat += fmt.Sprintf(" (önceki pencere %.0f ms)", prior.P95Ms)
		if cur.P95Ms-prior.P95Ms >= hcLatMinDeltaMs && ratio >= hcLatWarnRatio {
			lat = fmt.Sprintf("p95 %.0f ms, önceki pencerenin %.1f×'i (%.0f ms)", cur.P95Ms, ratio, prior.P95Ms)
			st := HCWarn
			if ratio >= hcLatCritRatio {
				st = HCCrit
			}
			row.State = hcWorse(row.State, st)
			shorts = append(shorts, fmt.Sprintf("p95 %.1f× arttı", ratio))
		}
	}
	row.Seen = strings.Join(append(parts, lat), ", ")
	row.Short = strings.Join(shorts, ", ")
	return row
}

// hcPrioRank — "P1" → 1 … bilinmeyen → 4.
func hcPrioRank(p string) int {
	switch strings.ToUpper(strings.TrimSpace(p)) {
	case "P1":
		return 1
	case "P2":
		return 2
	case "P3":
		return 3
	}
	return 4
}

func hcInboxPick(oi InboxOpenResult, kinds ...string) []InboxRow {
	var out []InboxRow
	for _, it := range oi.Rows {
		for _, k := range kinds {
			if it.Kind == k {
				out = append(out, it)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return hcPrioRank(out[i].Priority) < hcPrioRank(out[j].Priority) })
	return out
}

func hcInboxSeen(rows []InboxRow, noun string, truncated bool) string {
	n := fmt.Sprint(len(rows))
	if truncated {
		n += "+"
	}
	parts := make([]string, 0, hcTitlesInSeenMax)
	for i, it := range rows {
		if i >= hcTitlesInSeenMax {
			break
		}
		p := strings.TrimSpace(it.Priority + " " + truncateRunes(it.Title, 70))
		parts = append(parts, p)
	}
	return n + " açık " + noun + ": " + strings.Join(parts, "; ")
}

// hcClassifyProblems — kontrol 2 (problem + incident). P1/P2 Sorun, P3 Dikkat.
func hcClassifyProblems(service string, oi InboxOpenResult, err error) HealthcheckRow {
	row := hcBase(service, HCCheckProblems, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	rows := hcInboxPick(oi, "problem", "incident")
	if len(rows) == 0 {
		row.State, row.Seen = HCOK, "açık problem yok"
		return row
	}
	row.Seen = hcInboxSeen(rows, "problem", oi.Truncated)
	if rows[0].PageURL != "" {
		row.Link, row.LinkLabel = rows[0].PageURL, "Problem"
	}
	if hcPrioRank(rows[0].Priority) <= 2 {
		row.State, row.Short = HCCrit, fmt.Sprintf("%s açık problem", strings.ToUpper(rows[0].Priority))
		return row
	}
	row.State, row.Short = HCWarn, fmt.Sprintf("%d açık P3 problem", len(rows))
	return row
}

// hcClassifyExceptions — kontrol 5 (exception + httperror). P1 Sorun, P2
// Dikkat; yalnız P3 → İyi (izlemede, sayısı yazılır).
func hcClassifyExceptions(service string, oi InboxOpenResult, err error) HealthcheckRow {
	row := hcBase(service, HCCheckExceptions, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	rows := hcInboxPick(oi, "exception", "httperror")
	if len(rows) == 0 {
		row.State, row.Seen = HCOK, "açık P1/P2 exception yok"
		return row
	}
	if rows[0].PageURL != "" {
		row.Link, row.LinkLabel = rows[0].PageURL, "Exception"
	}
	switch hcPrioRank(rows[0].Priority) {
	case 1:
		row.State, row.Short = HCCrit, "P1 exception"
	case 2:
		row.State, row.Short = HCWarn, "P2 exception"
	default:
		row.State = HCOK
		row.Seen = fmt.Sprintf("açık P1/P2 exception yok; %d P3 izlemede", len(rows))
		return row
	}
	row.Seen = hcInboxSeen(rows, "exception", oi.Truncated)
	return row
}

// hcClassifyAnomalies — kontrol 3. Süren = kapanmamış, kronik değil, son
// 10 dk'da görülmüş, operatör "anomali değil" dememiş. Problem'e terfi etmiş
// olay Sorun, gölge olay Dikkat.
func hcClassifyAnomalies(service string, evs []chstore.MLEvent, now time.Time, err error) HealthcheckRow {
	row := hcBase(service, HCCheckAnomalies, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	var live []chstore.MLEvent
	for _, e := range evs {
		if e.Stage == chstore.MLStageClosed || e.Stage == chstore.MLStageChronic || e.Verdict == chstore.VerdictNotAnomaly {
			continue
		}
		if time.Unix(0, e.LastSeen).Before(now.Add(-hcAnomalyOngoing)) {
			continue
		}
		live = append(live, e)
	}
	if len(live) == 0 {
		row.State, row.Seen = HCOK, "süren anomali olayı yok"
		return row
	}
	sort.SliceStable(live, func(i, j int) bool {
		pi, pj := live[i].Stage == chstore.MLStagePromote, live[j].Stage == chstore.MLStagePromote
		if pi != pj {
			return pi
		}
		return live[i].Score > live[j].Score
	})
	names := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, e := range live {
		n := e.Detector
		if e.Signal != "" {
			n += " (" + e.Signal + ")"
		}
		if !seen[n] && len(names) < 3 {
			seen[n] = true
			names = append(names, n)
		}
	}
	since := now.Sub(time.Unix(0, live[0].StartedAt)).Round(time.Minute)
	row.Seen = fmt.Sprintf("%d süren olay: %s; en eskisi %s önce başladı", len(live), strings.Join(names, ", "), hcDurTR(since))
	row.Link = "/anomalies/ml/" + url.PathEscape(live[0].ID)
	row.LinkLabel = "Anomali"
	if live[0].Stage == chstore.MLStagePromote {
		row.State, row.Short = HCCrit, "süren anomali ("+live[0].Detector+")"
		return row
	}
	row.State, row.Short = HCWarn, "süren anomali ("+live[0].Detector+")"
	return row
}

// hcDurTR — SAF: "12 dk" / "2 sa 5 dk".
func hcDurTR(d time.Duration) string {
	m := int(math.Round(d.Minutes()))
	if m < 60 {
		return fmt.Sprintf("%d dk", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%d sa", m/60)
	}
	return fmt.Sprintf("%d sa %d dk", m/60, m%60)
}

// hcClassifyDeploys — kontrol 4. Son 30 dk'da deploy → Dikkat (bilgi);
// son 2 saatte başarısız / takılmış rollout → Sorun (taramanın
// deployFailedStatus sözlüğü).
func hcClassifyDeploys(service string, rows []ChangeRow, now time.Time, err error) HealthcheckRow {
	row := hcBase(service, HCCheckDeploys, "")
	if err != nil {
		row.State, row.Seen = HCUnknown, hcErrSeen(err)
		return row
	}
	desc := func(r ChangeRow) string {
		s := hcDurTR(now.Sub(time.Unix(0, r.TimeUnixNs))) + " önce " + deploySourceTR(r.Source)
		ver := r.Version
		if r.PrevVersion != "" && ver != "" {
			ver += "←" + r.PrevVersion
		}
		if ver != "" {
			s += " (" + ver + ")"
		}
		return s
	}
	for _, r := range rows {
		if r.Source == "rollout" && deployFailedStatus(r.Status) {
			row.State = HCCrit
			row.Seen = desc(r) + ", durum " + r.Status
			row.Short = "rollout " + r.Status
			if r.Workload != "" {
				row.Link = "/rollouts?workload=" + url.QueryEscape(r.Workload)
			}
			return row
		}
	}
	for _, r := range rows {
		if now.Sub(time.Unix(0, r.TimeUnixNs)) <= hcDeployRecent {
			row.State = HCWarn
			row.Seen = desc(r)
			row.Short = "son 30 dk'da deploy"
			if r.Workload != "" {
				row.Link = "/rollouts?workload=" + url.QueryEscape(r.Workload)
			}
			return row
		}
	}
	row.State = HCOK
	row.Seen = "son 30 dk'da deploy yok"
	if len(rows) > 0 {
		row.Seen += "; en sonuncusu " + desc(rows[0])
	}
	return row
}

// ── pod'lar (kontrol 6-8) ──────────────────────────────────────────────

func hcRunPods(ctx context.Context, r hcReaders, service string, from, to time.Time) []HealthcheckRow {
	var all []ClusterRef
	if r.clusters != nil {
		all = r.clusters()
	}
	if r.servicePods == nil || len(all) == 0 {
		return hcClassifyPodsOpts(service, ServicePodsResult{}, nil, true, hcAllPodChecks)
	}
	refs, _ := sweepPodClusterRefs(ctx, r.sweepReaders, SweepRequest{Service: service, From: from, To: to}, all)
	if len(refs) > hcMaxPodClusters {
		refs = refs[:hcMaxPodClusters]
	}
	res, err := r.servicePods(ctx, service, refs, from, to, ServicePodsOpts{WaitingReasons: true, Replicas: hcAllPodChecks.replicas, ClusterHealth: hcAllPodChecks.cluster})
	return hcClassifyPodsOpts(service, res, err, false, hcAllPodChecks)
}

// hcPodChecks — pod okumasından türeyen ek cluster kontrolleri.
type hcPodChecks struct{ replicas, cluster bool }

// hcAllPodChecks — koşunun açtığı ek kontroller (dilim 2: 9 + 10).
var hcAllPodChecks = hcPodChecks{replicas: true, cluster: true}

// hcPodCheckIDs — SAF: açık kontrollerin katalog sırası.
func hcPodCheckIDs(o hcPodChecks) []string {
	ids := []string{HCCheckPodRestarts, HCCheckPodWaiting, HCCheckPodResources}
	if o.replicas {
		ids = append(ids, HCCheckReplicas)
	}
	if o.cluster {
		ids = append(ids, HCCheckCluster)
	}
	return ids
}

// hcClassifyPods — dilim-1 kümesi (6-8).
func hcClassifyPods(service string, res ServicePodsResult, err error, skipped bool) []HealthcheckRow {
	return hcClassifyPodsOpts(service, res, err, skipped, hcPodChecks{})
}

// hcClassifyPodsOpts — SAF: pod okuması → cluster başına kontrol satırları,
// sıra: kontrol önce (restart ×N, bekleyen ×N, …), cluster sonra. Pod'u
// olmayan cluster düşer (servis orada koşmuyor); hiçbirinde pod yoksa her
// kontrol için tek Ölçülemedi satırı.
func hcClassifyPodsOpts(service string, res ServicePodsResult, err error, skipped bool, o hcPodChecks) []HealthcheckRow {
	ids := hcPodCheckIDs(o)
	allUnknown := func(subject, seen string) []HealthcheckRow {
		out := make([]HealthcheckRow, 0, len(ids))
		for _, id := range ids {
			out = append(out, hcUnknownRow(service, id, subject, seen))
		}
		return out
	}
	switch {
	case skipped || (!res.Thanos && err == nil):
		return allUnknown("", "Thanos Remote Cluster yapılandırılmamış — pod / cluster metrikleri okunamaz")
	case err != nil:
		return allUnknown("", "pod'lar "+hcErrSeen(err))
	}
	var per [][]HealthcheckRow
	empty := 0
	for _, cl := range res.Clusters {
		name := firstNonEmpty(cl.Name, cl.ID)
		if cl.Err != "" {
			seen := "okunamadı: " + truncateRunes(cl.Err, hcErrRunes)
			if strings.Contains(strings.ToLower(cl.Err), "deadline") || strings.Contains(strings.ToLower(cl.Err), "timeout") {
				seen = "zaman aşımı — okunamadı"
			}
			per = append(per, allUnknown(name, seen))
			continue
		}
		if len(cl.Rows) == 0 {
			empty++
			continue
		}
		rows := []HealthcheckRow{hcPodRestarts(service, name, cl), hcPodWaiting(service, name, cl), hcPodResources(service, name, cl)}
		if o.replicas {
			rows = append(rows, hcReplicas(service, name, cl))
		}
		if o.cluster {
			rows = append(rows, hcClusterHealth(service, name, cl))
		}
		per = append(per, rows)
	}
	if len(per) == 0 {
		seen := "Thanos'ta bu servisin pod'u bulunamadı"
		if res.PodRegex != "" {
			seen += " (kalıp " + res.PodRegex + ")"
		}
		if empty == 0 {
			seen = "taranacak cluster yok"
		}
		return allUnknown("", seen)
	}
	out := make([]HealthcheckRow, 0, len(ids)*len(per))
	for k := range ids {
		for _, rows := range per {
			out = append(out, rows[k])
		}
	}
	return out
}

func hcPodNames(pods []string) string {
	sort.Strings(pods)
	if len(pods) > hcPodNamesInSeen {
		return strings.Join(pods[:hcPodNamesInSeen], ", ") + fmt.Sprintf(" +%d", len(pods)-hcPodNamesInSeen)
	}
	return strings.Join(pods, ", ")
}

func hcPodRestarts(service, cluster string, cl ServicePodsCluster) HealthcheckRow {
	row := hcBase(service, HCCheckPodRestarts, cluster)
	row.Evidence = cl.Query
	if !cl.RestartsOK {
		row.State, row.Seen = HCUnknown, fmt.Sprintf("%d pod; penceredeki restart artışı okunamadı", len(cl.Rows))
		return row
	}
	total := 0.0
	maxR := 0.0
	var restarted, oom []string
	for _, p := range cl.Rows {
		v := math.Round(cl.Restarts[p.Namespace+"\x00"+p.Pod])
		if v <= 0 {
			continue
		}
		total += v
		maxR = math.Max(maxR, v)
		restarted = append(restarted, p.Pod)
		if strings.EqualFold(p.LastTermReason, "OOMKilled") {
			oom = append(oom, p.Pod)
		}
	}
	switch {
	case total == 0:
		row.State, row.Seen = HCOK, fmt.Sprintf("%d pod, pencerede restart yok", len(cl.Rows))
	case len(oom) > 0:
		row.State, row.Short = HCCrit, "OOMKilled"
		row.Seen = fmt.Sprintf("%d pod'da %.0f restart; OOMKilled: %s", len(restarted), total, hcPodNames(oom))
	case maxR >= hcRestartCrit:
		row.State, row.Short = HCCrit, "pod restart"
		row.Seen = fmt.Sprintf("%d pod'da %.0f restart (en çok %.0f): %s", len(restarted), total, maxR, hcPodNames(restarted))
	default:
		row.State, row.Short = HCWarn, "pod restart"
		row.Seen = fmt.Sprintf("%d pod'da %.0f restart: %s", len(restarted), total, hcPodNames(restarted))
	}
	return row
}

func hcPodWaiting(service, cluster string, cl ServicePodsCluster) HealthcheckRow {
	row := hcBase(service, HCCheckPodWaiting, cluster)
	crit := map[string][]string{}
	warn := map[string][]string{}
	for _, p := range cl.Rows {
		if cl.WaitingOK {
			if w := strings.TrimSpace(cl.Waiting[p.Namespace+"\x00"+p.Pod]); w != "" {
				if k8sCritWaiting[w] {
					crit[w] = append(crit[w], p.Pod)
				} else if w != "ContainerCreating" && w != "PodInitializing" {
					warn[w] = append(warn[w], p.Pod)
				}
			}
		}
		switch p.Phase {
		case "Pending", "Failed", "Unknown":
			warn[p.Phase] = append(warn[p.Phase], p.Pod)
		}
	}
	describe := func(m map[string][]string) (string, string) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%s: %d pod (%s)", k, len(m[k]), hcPodNames(m[k])))
		}
		return strings.Join(parts, "; "), keys[0]
	}
	switch {
	case len(crit) > 0:
		row.State = HCCrit
		row.Seen, row.Short = describe(crit)
	case len(warn) > 0:
		row.State = HCWarn
		row.Seen, row.Short = describe(warn)
	case !cl.WaitingOK:
		row.State, row.Seen = HCUnknown, fmt.Sprintf("%d pod; bekleme sebebi okunamadı (faz temiz)", len(cl.Rows))
	default:
		row.State, row.Seen = HCOK, fmt.Sprintf("%d pod, bekleyen ya da Pending pod yok", len(cl.Rows))
	}
	return row
}

func hcPodResources(service, cluster string, cl ServicePodsCluster) HealthcheckRow {
	row := hcBase(service, HCCheckPodResources, cluster)
	var maxMem, maxCPU float64
	var memPod, cpuPod string
	limited := false
	for _, p := range cl.Rows {
		if p.MemPct > 0 || p.CPUPct > 0 {
			limited = true
		}
		if p.MemPct > maxMem {
			maxMem, memPod = p.MemPct, p.Pod
		}
		if p.CPUPct > maxCPU {
			maxCPU, cpuPod = p.CPUPct, p.Pod
		}
	}
	if !limited {
		row.State, row.Seen = HCUnknown, fmt.Sprintf("%d pod; CPU/bellek limiti tanımlı değil ya da okunamadı — oran hesaplanamadı", len(cl.Rows))
		return row
	}
	row.State = HCOK
	row.Seen = fmt.Sprintf("en yüksek bellek limitin %%%.0f'i, CPU %%%.0f'i", maxMem, maxCPU)
	switch {
	case maxMem >= hcMemCritPct:
		row.State, row.Short = HCCrit, fmt.Sprintf("bellek limitin %%%.0f'i", maxMem)
		row.Seen = fmt.Sprintf("%s: bellek limitin %%%.0f'i (OOM yakın); CPU en çok %%%.0f", memPod, maxMem, maxCPU)
	case maxMem >= hcMemWarnPct:
		row.State, row.Short = HCWarn, fmt.Sprintf("bellek limitin %%%.0f'i", maxMem)
		row.Seen = fmt.Sprintf("%s: bellek limitin %%%.0f'i; CPU en çok %%%.0f", memPod, maxMem, maxCPU)
	}
	if maxCPU >= hcCPUWarnPct {
		row.State = hcWorse(row.State, HCWarn)
		if row.Short == "" {
			row.Short = fmt.Sprintf("CPU limitin %%%.0f'i", maxCPU)
			row.Seen = fmt.Sprintf("%s: CPU limitin %%%.0f'i (kısılma olası); bellek en çok %%%.0f", cpuPod, maxCPU, maxMem)
		}
	}
	return row
}

// ── özet (SAF) ──────────────────────────────────────────────────────────

// hcSummarize — satırlar → hüküm + tek cümle + sayımlar. Hüküm en kötü
// ölçülen durum; hiçbir satır ölçülemediyse unknown. Ölçülemeyen satır
// cümlede AYRICA söylenir — "sağlıklı görünüyor" yalnız her şey ölçüldüyse.
func hcSummarize(service string, rows []HealthcheckRow) (string, string, HealthcheckCounts) {
	var c HealthcheckCounts
	var crit, warn []string
	label := func(r HealthcheckRow) string {
		s := firstNonEmpty(r.Short, strings.ToLower(r.Title))
		if r.Subject != "" && !strings.Contains(s, r.Subject) {
			s += " (" + r.Subject + ")"
		}
		return s
	}
	for _, r := range rows {
		switch r.State {
		case HCCrit:
			c.Crit++
			crit = append(crit, label(r))
		case HCWarn:
			c.Warn++
			warn = append(warn, label(r))
		case HCOK:
			c.OK++
		default:
			c.Unknown++
		}
	}
	list := func(xs []string, noun string) string {
		if len(xs) <= hcShortListMax {
			return strings.Join(xs, ", ")
		}
		return strings.Join(xs[:hcShortListMax], ", ") + fmt.Sprintf(" ve %d %s daha", len(xs)-hcShortListMax, noun)
	}
	unknownTail := ""
	if c.Unknown > 0 {
		unknownTail = fmt.Sprintf("%d kontrol ölçülemedi", c.Unknown)
	}
	switch {
	case c.Crit > 0:
		s := fmt.Sprintf("%s: %d sorun var: %s.", service, c.Crit, list(crit, "sorun"))
		var extra []string
		if c.Warn > 0 {
			extra = append(extra, fmt.Sprintf("Ayrıca %d noktaya dikkat", c.Warn))
		}
		if unknownTail != "" {
			extra = append(extra, unknownTail)
		}
		if len(extra) > 0 {
			s += " " + strings.Join(extra, "; ") + "."
		}
		return HCCrit, s, c
	case c.Warn > 0:
		s := fmt.Sprintf("%s: sorun yok, %d noktaya dikkat: %s", service, c.Warn, list(warn, "nokta"))
		if unknownTail != "" {
			s += "; " + unknownTail
		}
		return HCWarn, s + ".", c
	case c.OK == 0:
		return HCUnknown, service + " ölçülemedi: kontrollerin hiçbiri okunamadı.", c
	case c.Unknown > 0:
		return HCOK, fmt.Sprintf("%s: ölçülen kontrollerde sorun yok; %s.", service, unknownTail), c
	}
	return HCOK, service + " sağlıklı görünüyor.", c
}
