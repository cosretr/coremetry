package anomaly

// red_vm_source.go — v0.10.1478: VM RED okuyucusu (lider tikinde, dedektörün
// tik goroutine'i + span okumalarıyla PARALEL). Saf parçalar red_vm_query.go /
// red_vm_store.go / red_vm_select.go'da; burası yalnız G/Ç + bütçe + sayaç.
//
// Tik başına iş (sırasıyla, hepsi tek bütçe bağlamında — vmTickBudget):
//  1. aile keşfi (vmMetaTTL'de bir): etiket kümesi → servis etiketi (LabelMap /
//     service_name / keşif), durum etiketleri, ortam kapsamı (redSourceEnv dolu
//     ise resolveHTTPEnvScope kuralı). Kapsam değişirse ailenin kovaları atılır.
//  2. durum-etiketli servis kümesi (vmStatusTTL'de bir, aile başına TEK anlık
//     sorgu): etiketsiz servisin hata oranı VM'den okunmaz.
//  3. eksik kovalar: gerekli küme (ardışık 24 sa ∪ mevsimsel slot ∪ davranış
//     haftanın-saati) − bellek → koşular → aynı uzunlukta koşular tek sorguda
//     (ofset birleşimi) → en yeni önce, tik başına ≤ vmMaxQueriesPerTick sorgu,
//     sorgu başına vmQueryTimeout, vmConcurrency paralel. Hata veren (aile, tür)
//     vmKindBackoff boyunca denenmez (o tür span'e düşer).
//
// Ölçüm: REDSourceObservability() (/api/settings/anomaly-red-source/status) +
// değiştikçe bir log satırı.

import (
	"context"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

// REDVM — okuyucunun VM'den istedikleri (*vmetrics.Service karşılar; anomaly
// → vmetrics yalnız SAF yardımcılar için import edilir, okuma bu arayüzden).
type REDVM interface {
	Configured() bool
	MetricLabelMap() vmetrics.LabelMap
	MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error)
	MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error)
	QueryPromQLRange(ctx context.Context, query string, from, to time.Time, stepSeconds, maxDataPoints int) ([]chstore.SpanMetricSeries, error)
	QueryPromQLInstant(ctx context.Context, query string, at time.Time) ([]chstore.SpanMetricSeries, error)
}

// Bütçe + tazelik sabitleri (DECISIONS v0.10.1478).
const (
	// vmIngestLag — kova kapanışından sonra beklenen pay: OTel collector
	// dışa aktarımı (≈60 sn) + VM -search.latencyOffset (30 sn). Kova bu
	// paydan ÖNCE okunsaydı son örnek eksik kalır ve kalıcı olarak bellekte
	// yanlış kalırdı. Bedel: VM kaynağında "son kova" span'e göre en çok
	// 2 dk daha eski.
	vmIngestLag         = 2 * time.Minute
	vmTickBudget        = 20 * time.Second
	vmQueryTimeout      = 10 * time.Second
	vmMaxQueriesPerTick = 24
	vmConcurrency       = 4
	vmMaxPoints         = 1000
	vmMetaTTL           = 10 * time.Minute
	vmMetaStale         = time.Hour // keşif okunamazsa eski kapsam bu kadar geçerli
	vmStatusTTL         = 30 * time.Minute
	vmKindBackoff       = 5 * time.Minute
	vmLabelLookback     = 24 * time.Hour
	vmEnvValuesLimit    = 1000
)

// vmEnvLabelDefault — api/http_env_scope.go httpEnvLabelDefault ile aynı.
const vmEnvLabelDefault = "deployment_environment_name"

// vmEnvLabelCandidates — SAF: api httpEnvLabelCandidates'in ikizi —
// LabelMap.Env doluysa YALNIZ o (operatörün açık kararı); değilse varsayılan +
// kalan rol adayları.
func vmEnvLabelCandidates(lm vmetrics.LabelMap) []string {
	if l := lm.Get(vmetrics.RoleEnv); l != "" {
		return []string{l}
	}
	out := []string{vmEnvLabelDefault}
	for _, c := range vmetrics.RoleCandidates(vmetrics.RoleEnv) {
		if c != vmEnvLabelDefault {
			out = append(out, c)
		}
	}
	return out
}

// vmFamMeta — ailenin keşif durumu.
type vmFamMeta struct {
	at        time.Time
	ok        bool   // kapsam kullanılabilir
	present   bool   // metrik VM'de var (son 24 sa)
	reason    string // ok=false gerekçesi
	scope     vmQueryScope
	scopeKey  string
	statusAt  time.Time
	statusSvc map[string]bool // nil = bilinmiyor
}

type vmREDBox struct{ vm REDVM }

// vmREDSource — lider belleği + kablo (atomik: main.go dedektörü VM
// servisinden önce kuruyor; heap kaynağı emsali).
type vmREDSource struct {
	wire    atomic.Pointer[vmREDBox]
	store   *vmREDStore
	meta    []vmFamMeta
	backoff map[[2]int]time.Time
	// runLen — (aile, tür) başına koşu uzunluğu: hata/zaman aşımında yarıya
	// iner (en az 1 kova), başarıda iki katına çıkar (en çok vmMaxRunBuckets).
	// Filo geneli kova histogramı ağırsa ısınma daha küçük dilimlerle sürer.
	runLen  map[[2]int]int
	envKey  string
	lastLog string

	// v0.10.1791 (Davis F1.3) — ENDPOINT kipi. routes=true olan ikinci bir
	// kaynak (Detector.redEndpoints) aynı kova belleği / planlama / bütçe
	// mantığını (svc, route) anahtarıyla koşar: yalnız HTTP ailesi, rota
	// etiketi zorunlu, servis başına üst-N rota seçimi (sel) ve tik başına
	// sorgu tavanı queryCap (servis yolundan kalan pay). routes=false iken
	// bu alanlar okunmaz — servis yolu bayt bayt eski.
	routes    bool
	topN      int
	maxSeries int
	queryCap  int // 0 = vmMaxQueriesPerTick
	sel       vmEndpointSel
}

// vmEndpointSel — endpoint kipinin seçim durumu.
type vmEndpointSel struct {
	at     time.Time
	anchor int64
	topN   int
	keys   map[string]bool
}

func newVMREDSource() *vmREDSource {
	return &vmREDSource{store: newVMREDStore(), meta: make([]vmFamMeta, len(vmREDFamilies)), backoff: map[[2]int]time.Time{}, runLen: map[[2]int]int{}}
}

// newVMEndpointSource — v0.10.1791: endpoint kipindeki kaynak.
func newVMEndpointSource() *vmREDSource {
	s := newVMREDSource()
	s.routes = true
	return s
}

// SetREDSource — main.go: anomDet.SetREDSource(vmSvc). nil = VM yok.
func (d *Detector) SetREDSource(vm REDVM) {
	if d.red == nil {
		return
	}
	if vm == nil {
		d.red.wire.Store(nil)
		if d.redEndpoints != nil {
			d.redEndpoints.wire.Store(nil)
		}
		return
	}
	d.red.wire.Store(&vmREDBox{vm: vm})
	if d.redEndpoints != nil { // v0.10.1791 — endpoint özneleri aynı VM'i okur
		d.redEndpoints.wire.Store(&vmREDBox{vm: vm})
	}
}

// vmPlanned — planlanmış tek sorgu.
type vmPlanned struct {
	fam   int
	kind  vmREDKind
	group vmQueryGroup
	query string
}

// prepare — tikin VM görünümü (asla nil). Bütün hatalar görünüme gerekçe
// olarak yazılır; dedektör o servislerde span'e düşer.
func (s *vmREDSource) prepare(ctx context.Context, now time.Time, sens chstore.AnomalySensitivityConfig, days, neighbor int) *vmREDView {
	start := time.Now()
	v := &vmREDView{mode: sens.REDSourceMode(), store: s.store}
	defer func() { s.publish(v, time.Since(start)) }()
	if v.mode == chstore.AnomalyREDSourceSpan {
		v.reason = redReasonSpanSetting
		return v
	}
	box := s.wire.Load()
	if box == nil || box.vm == nil || !box.vm.Configured() {
		v.reason = redReasonNotConfigured
		return v
	}
	vm := box.vm
	v.configured = true
	vnow := now.Add(-vmIngestLag)
	v.upper = lastCompleteBucketStart(vnow).Unix()
	v.vnow = vnow
	v.consec = vmConsecutiveBuckets(vnow)
	// v0.10.1791 — mevsimsel dilim (seasonalTimezone, karar 3): CH yoluyla aynı.
	sloc := seasonalLocation(sens.SeasonalTimezone)
	v.seasonal, v.seasonalClass = vmSeasonalBucketsCal(vnow, days, neighbor, sens.Calendar, sloc) // v0.10.1483 — takvim yedek sınıfı
	v.seasonalDayKey = seasonalDayKey(sloc)
	if sens.Behavior.IsEnabled() && !s.routes { // endpoint kipinde davranış motoru yok
		v.behavior = vmBehaviorBuckets(vnow)
	}
	req := vmRequiredSet(v.consec, v.seasonal, v.behavior)
	s.store.retain(req)

	bctx, cancel := context.WithTimeout(ctx, vmTickBudget)
	defer cancel()
	env := sens.REDSourceEnvScope()
	lm := vm.MetricLabelMap().Normalized()
	if key := env + "|" + lm.Service + "|" + lm.Env; key != s.envKey {
		// Kapsam girdisi değişti: keşif hemen yenilensin.
		for i := range s.meta {
			s.meta[i].at = time.Time{}
		}
		s.envKey = key
	}
	for fi := range vmREDFamilies {
		if s.routes && fi != 0 {
			continue // v0.10.1791 — rota yalnız HTTP ailesinde (karar 2)
		}
		s.refreshMeta(bctx, vm, fi, lm, env, vnow)
	}
	if s.routes {
		// v0.10.1791 — üst-N rota seçimi; tavan aşılırsa tür bu tik atlanır
		// (okuma yok, bellek korunur).
		q, n := s.refreshSelection(bctx, vm, vnow)
		v.queries += q
		v.selected = n
		if n < 0 {
			v.reason = redReasonNoSelection
			return v
		}
		if n > s.maxSeries {
			v.reason = subjectSkipSeriesCap
			return v
		}
	}
	required := make([]int64, 0, len(req))
	for b := range req {
		required = append(required, b)
	}
	sort.Slice(required, func(i, j int) bool { return required[i] > required[j] })

	plan := s.plan(required, vnow)
	var execQ int
	execQ, v.queryErrs, v.bucketsFetched, v.lastErr = s.execute(bctx, vm, plan, vnow)
	v.queries += execQ
	for fi := range vmREDFamilies {
		v.queries += s.refreshStatus(bctx, vm, fi, vnow)
	}
	v.meta = append([]vmFamMeta(nil), s.meta...)
	for fi, f := range vmREDFamilies {
		if !s.meta[fi].ok {
			continue // kullanılamayan aile hazır değil ve bekleyen sayılmaz
		}
		for k := vmREDKind(0); k < vmKindN; k++ {
			if _, ok := vmREDExpr(f, k, s.meta[fi].scope, 0); !ok {
				continue // ölçülemeyen tür (durum etiketi yok)
			}
			if until, ok := s.backoff[[2]int{fi, int(k)}]; ok && vnow.Before(until) {
				v.failed[fi][k] = true
			}
			v.readyConsec[fi][k] = len(s.store.missing(fi, k, v.consec)) == 0
			v.readyDet[fi][k] = v.readyConsec[fi][k] && len(s.store.missing(fi, k, v.seasonal)) == 0
			v.readyBeh[fi][k] = len(s.store.missing(fi, k, v.behavior)) == 0
			v.pending += len(s.store.missing(fi, k, required))
		}
	}
	return v
}

// refreshMeta — ailenin kapsam keşfi (vmMetaTTL). Okuma hatasında eski
// kapsam vmMetaStale boyunca korunur (okunamadı ≠ yok); daha eskiyse kullanılmaz.
func (s *vmREDSource) refreshMeta(ctx context.Context, vm REDVM, fi int, lm vmetrics.LabelMap, env string, vnow time.Time) {
	m := &s.meta[fi]
	if !m.at.IsZero() && vnow.Sub(m.at) < vmMetaTTL {
		return
	}
	f := vmREDFamilies[fi]
	from := vnow.Add(-vmLabelLookback)
	names, _, err := vm.MetricLabelNamesIn(ctx, f.Probe, from, vnow)
	if err != nil {
		if m.ok && vnow.Sub(m.at) < vmMetaStale {
			return // eski kapsam geçerli; bir sonraki tik yeniden dener
		}
		*m = vmFamMeta{at: vnow, reason: redReasonVMError + ": etiket okuması"}
		return
	}
	next := vmFamMeta{at: vnow, statusAt: m.statusAt, statusSvc: m.statusSvc}
	if len(names) == 0 {
		next.reason = redReasonNoFamily
		s.setMeta(fi, next)
		return
	}
	next.present = true
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	sr := vmetrics.ResolveLabelRole(vmetrics.RoleService, lm, set, true)
	if !sr.Applied() || !set[sr.Label] {
		next.reason = "servis etiketi yok (service_name / LabelMap)"
		s.setMeta(fi, next)
		return
	}
	sc := vmQueryScope{SvcLabel: sr.Label}
	for _, l := range f.StatusLabels {
		if set[l] {
			sc.StatusLabels = append(sc.StatusLabels, l)
		}
	}
	if s.routes {
		// v0.10.1791 — rota etiketi yoksa endpoint tabanı kurulamaz; servisler
		// servis düzeyinde kalır.
		for _, l := range vmRouteLabels {
			if set[l] {
				sc.RouteLabel = l
				break
			}
		}
		if sc.RouteLabel == "" {
			next.reason = redReasonNoRoute
			s.setMeta(fi, next)
			return
		}
	}
	if env != "" {
		matcher, reason, err := s.resolveEnv(ctx, vm, f, lm, env, from, vnow)
		if err != nil {
			if m.ok && vnow.Sub(m.at) < vmMetaStale {
				return
			}
			next.reason = redReasonVMError + ": ortam etiketi okuması"
			s.setMeta(fi, next)
			return
		}
		if reason != "" {
			next.reason = reason
			s.setMeta(fi, next)
			return
		}
		sc.EnvMatcher = matcher
	}
	next.ok, next.scope = true, sc
	next.scopeKey = sc.SvcLabel + "|" + strings.Join(sc.StatusLabels, ",") + "|" + sc.EnvMatcher
	if sc.RouteLabel != "" {
		next.scopeKey += "|" + sc.RouteLabel
		if next.scopeKey == m.scopeKey {
			next.scope.Select = m.scope.Select // seçim kapsamla birlikte yaşar
		}
	}
	s.setMeta(fi, next)
}

// setMeta — kapsam anahtarı değiştiyse ailenin kovaları + durum kümesi atılır.
func (s *vmREDSource) setMeta(fi int, next vmFamMeta) {
	if next.scopeKey != s.meta[fi].scopeKey {
		s.store.flushFamily(fi)
		next.statusAt, next.statusSvc = time.Time{}, nil
		if s.routes {
			s.sel = vmEndpointSel{} // kapsam değişti: seçim yeniden
		}
	}
	s.meta[fi] = next
}

// resolveEnv — resolveHTTPEnvScope kuralı (etiket seçimi + toleranslı eşleme).
// reason dolu = çözülemedi (span yedeği), err = okunamadı.
func (s *vmREDSource) resolveEnv(ctx context.Context, vm REDVM, f vmREDFamily, lm vmetrics.LabelMap, env string, from, to time.Time) (string, string, error) {
	cands := vmEnvLabelCandidates(lm)
	label, vals := cands[0], []string(nil)
	for i, l := range cands {
		got, _, err := vm.MetricLabelValuesIn(ctx, f.Probe, l, from, to, vmEnvValuesLimit)
		if err != nil {
			return "", "", err
		}
		if i == 0 {
			vals = got
		}
		if len(got) > 0 {
			label, vals = l, got
			break
		}
	}
	m := vmetrics.MatchEnvValue(env, vals)
	if m.Tier == vmetrics.EnvTierNone || len(m.Values) == 0 {
		return "", fmt.Sprintf("ortam %q eşleşmedi (%s)", env, label), nil
	}
	matcher := vmetrics.EnvMatcher(label, m.Values)
	if len(matcher) > vmMaxEnvMatcherLen {
		return "", fmt.Sprintf("ortam %q çok fazla değere eşleşti (%d)", env, len(m.Values)), nil
	}
	return matcher, "", nil
}

// refreshStatus — durum-etiketli servis kümesi (vmStatusTTL). Döner: sorgu sayısı.
func (s *vmREDSource) refreshStatus(ctx context.Context, vm REDVM, fi int, vnow time.Time) int {
	m := &s.meta[fi]
	if !m.ok || len(m.scope.StatusLabels) == 0 {
		return 0
	}
	if m.statusSvc != nil && vnow.Sub(m.statusAt) < vmStatusTTL {
		return 0
	}
	q, ok := vmStatusServicesQuery(vmREDFamilies[fi], m.scope)
	if !ok {
		return 0
	}
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	ser, err := vm.QueryPromQLInstant(qctx, q, vnow)
	if err != nil {
		if m.statusSvc != nil && vnow.Sub(m.statusAt) < vmMetaStale {
			return 1
		}
		m.statusSvc = nil
		return 1
	}
	set := make(map[string]bool, len(ser))
	for _, sr := range ser {
		if svc, _, ok := vmParseGroupKey(sr.GroupKey, m.scope.SvcLabel); ok {
			set[svc] = true
		}
	}
	m.statusSvc, m.statusAt = set, vnow
	return 1
}

// plan — eksik kovalardan sorgu planı (en yeni önce, count > err > p99).
func (s *vmREDSource) plan(required []int64, vnow time.Time) []vmPlanned {
	var out []vmPlanned
	for fi, f := range vmREDFamilies {
		m := s.meta[fi]
		if !m.ok {
			continue
		}
		for k := vmREDKind(0); k < vmKindN; k++ {
			if until, ok := s.backoff[[2]int{fi, int(k)}]; ok && vnow.Before(until) {
				continue
			}
			if _, ok := vmREDExpr(f, k, m.scope, 0); !ok {
				continue // ör. durum etiketi yok → hata türü ölçülemez
			}
			miss := s.store.missing(fi, k, required)
			for _, g := range vmGroupRuns(vmRuns(miss, s.runLenFor(fi, k)), vmMaxOffsetsPerQuery) {
				build := func(offs []int64) string { q, _ := vmREDQuery(f, k, m.scope, offs); return q }
				for _, sg := range vmSplitForLen(g, build) {
					q := build(sg.Offsets)
					if q == "" || len(q) > vmMaxQueryLen {
						continue
					}
					out = append(out, vmPlanned{fam: fi, kind: k, group: sg, query: q})
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].group.RefStart != out[j].group.RefStart {
			return out[i].group.RefStart > out[j].group.RefStart
		}
		return out[i].kind < out[j].kind
	})
	limit := vmMaxQueriesPerTick
	if s.queryCap > 0 { // v0.10.1791 — endpoint kipi: servis yolundan kalan pay
		limit = s.queryCap
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// refreshSelection — v0.10.1791 endpoint kipi: servis başına üst-N rota
// (vmEndpointSelectExpr, vmStatusTTL'de bir). Küme DEĞİŞİRSE çapa yeni anı
// alır, ifadeler yeni seçime geçer ve ailenin kovaları atılır (yeni rotanın
// geçmişi yeniden okunur — eski kovalar yalnız eski kümeyi taşır). Küme aynıysa
// çapa korunur (sorgu metni ve bellek değişmez). Döner: sorgu sayısı, seçilen
// seri sayısı (-1 = seçim yok/okunamadı).
func (s *vmREDSource) refreshSelection(ctx context.Context, vm REDVM, vnow time.Time) (int, int) {
	m := &s.meta[0]
	if !m.ok || m.scope.RouteLabel == "" {
		return 0, -1
	}
	if s.sel.keys != nil && s.sel.topN == s.topN && m.scope.Select != "" && vnow.Sub(s.sel.at) < vmStatusTTL {
		return 0, len(s.sel.keys)
	}
	base := m.scope
	base.Select = ""
	anchor := vnow.Unix()
	qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
	defer cancel()
	ser, err := vm.QueryPromQLInstant(qctx, vmEndpointSelectExpr(vmREDFamilies[0], base, s.topN, anchor), vnow)
	if err != nil {
		if s.sel.keys != nil && m.scope.Select != "" && vnow.Sub(s.sel.at) < vmMetaStale {
			return 1, len(s.sel.keys) // eski seçim geçerli
		}
		s.sel, m.scope.Select = vmEndpointSel{}, ""
		return 1, -1
	}
	keys := make(map[string]bool, len(ser))
	for _, sr := range ser {
		if k, _, ok := vmParseGroupKeyRoute(sr.GroupKey, base.SvcLabel, base.RouteLabel); ok {
			keys[k] = true
		}
	}
	if !sameKeySet(keys, s.sel.keys) || s.sel.topN != s.topN || m.scope.Select == "" {
		s.sel.anchor = anchor
		m.scope.Select = vmEndpointSelectExpr(vmREDFamilies[0], base, s.topN, anchor)
		s.store.flushFamily(0)
	}
	s.sel.keys, s.sel.at, s.sel.topN = keys, vnow, s.topN
	return 1, len(keys)
}

// sameKeySet — SAF: iki küme eşit mi (nil = boş değil, "yok").
func sameKeySet(a, b map[string]bool) bool {
	if a == nil || b == nil || len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// runLenFor — (aile, tür) koşu uzunluğu (varsayılan vmMaxRunBuckets).
func (s *vmREDSource) runLenFor(fi int, k vmREDKind) int {
	if n, ok := s.runLen[[2]int{fi, int(k)}]; ok && n >= 1 {
		return n
	}
	return vmMaxRunBuckets
}

// vmNextRunLen — SAF: hata → yarı (≥1), başarı → iki kat (≤ vmMaxRunBuckets).
func vmNextRunLen(cur int, ok bool) int {
	if cur < 1 {
		cur = vmMaxRunBuckets
	}
	if !ok {
		if cur/2 < 1 {
			return 1
		}
		return cur / 2
	}
	if cur*2 > vmMaxRunBuckets {
		return vmMaxRunBuckets
	}
	return cur * 2
}

type vmResult struct {
	p   vmPlanned
	ser []chstore.SpanMetricSeries
	err error
}

// execute — planı sınırlı paralellikle koşar, sonuçları TEK goroutine'de
// belleğe yazar. Döner: sorgu sayısı, hatalı sorgu, okunan kova, son hata.
func (s *vmREDSource) execute(ctx context.Context, vm REDVM, plan []vmPlanned, vnow time.Time) (int, int, int, string) {
	if len(plan) == 0 {
		return 0, 0, 0, ""
	}
	res := make([]vmResult, len(plan))
	sem := make(chan struct{}, vmConcurrency)
	var wg sync.WaitGroup
	for i, p := range plan {
		wg.Add(1)
		go func(i int, p vmPlanned) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			from := time.Unix(p.group.RefStart, 0)
			to := from.Add(time.Duration(p.group.Len*vmStepS) * time.Second)
			qctx, cancel := context.WithTimeout(ctx, vmQueryTimeout)
			defer cancel()
			ser, err := vm.QueryPromQLRange(qctx, p.query, from, to, vmStepS, vmMaxPoints)
			res[i] = vmResult{p: p, ser: ser, err: err}
		}(i, p)
	}
	wg.Wait()
	errs, fetched := 0, 0
	lastErr := ""
	for _, r := range res {
		if r.err != nil {
			errs++
			lastErr = fmt.Sprintf("%s/%s: %v", vmREDFamilies[r.p.fam].ID, r.p.kind, r.err)
			key := [2]int{r.p.fam, int(r.p.kind)}
			s.backoff[key] = vnow.Add(vmKindBackoff)
			s.runLen[key] = vmNextRunLen(s.runLenFor(r.p.fam, r.p.kind), false)
			continue
		}
		key := [2]int{r.p.fam, int(r.p.kind)}
		s.runLen[key] = vmNextRunLen(s.runLenFor(r.p.fam, r.p.kind), true)
		applyVMResult(s.store, r.p, r.ser, s.meta[r.p.fam].scope)
		fetched += r.p.group.Len * len(r.p.group.Offsets)
	}
	return len(plan), errs, fetched, lastErr
}

// applyVMResult — SAF-ımsı (yalnız bellek): sorgu sonucunu kovalara yazar ve
// sorgulanan kovaları "okundu" işaretler. Pencere dışı nokta yazılmaz.
//
// v0.10.1791 — scope: endpoint kipinde (RouteLabel dolu) anahtar servis +
// ayraç + rota (endpointStoreKey); servis kipinde servis adı (eski davranış).
func applyVMResult(st *vmREDStore, p vmPlanned, ser []chstore.SpanMetricSeries, sc vmQueryScope) {
	want := map[int64]bool{}
	var buckets []int64
	for _, off := range p.group.Offsets {
		for i := 0; i < p.group.Len; i++ {
			b := p.group.RefStart - off + int64(i*vmStepS)
			want[b] = true
			buckets = append(buckets, b)
		}
	}
	for _, sr := range ser {
		svc, off, ok := vmParseGroupKey(sr.GroupKey, sc.SvcLabel)
		if sc.RouteLabel != "" {
			svc, off, ok = vmParseGroupKeyRoute(sr.GroupKey, sc.SvcLabel, sc.RouteLabel)
		}
		if !ok {
			continue
		}
		for _, pt := range sr.Points {
			b := pt.Time/int64(time.Second) - off
			if !want[b] || math.IsNaN(pt.Value) {
				continue
			}
			st.put(p.fam, p.kind, b, svc, pt.Value)
		}
	}
	st.markHave(p.fam, p.kind, buckets)
}

// ── Ölçüm ──────────────────────────────────────────────────────────────────

// REDSourceFamilyStats — ailenin durumu.
type REDSourceFamilyStats struct {
	ID             string `json:"id"`
	Present        bool   `json:"present"`
	Usable         bool   `json:"usable"`
	Reason         string `json:"reason,omitempty"`
	ServiceLabel   string `json:"serviceLabel,omitempty"`
	EnvMatcher     string `json:"envMatcher,omitempty"`
	StatusLabels   string `json:"statusLabels,omitempty"`
	StatusServices int    `json:"statusServices"`
	ReadyDetector  string `json:"readyDetector"` // "count,err,p99" hazır türler
	ReadyBehavior  string `json:"readyBehavior"`
}

// REDSourceStats — /api/settings/anomaly-red-source/status gövdesi.
type REDSourceStats struct {
	Mode           string                 `json:"mode"`
	Configured     bool                   `json:"configured"`
	Reason         string                 `json:"reason,omitempty"`
	LastUnix       int64                  `json:"lastUnix"`
	LastMs         int64                  `json:"lastMs"`
	LastQueries    int                    `json:"lastQueries"`
	LastQueryErrs  int                    `json:"lastQueryErrors"`
	LastFetched    int                    `json:"lastBucketsFetched"`
	PendingBuckets int                    `json:"pendingBuckets"`
	Frames         int                    `json:"frames"`
	Services       int                    `json:"services"`
	Dropped        int                    `json:"droppedServices,omitempty"`
	LastError      string                 `json:"lastError,omitempty"`
	Families       []REDSourceFamilyStats `json:"families"`
	// Sources — son tikte (metrik → kaynak → servis sayısı): "vm" | "span" |
	// "hold" (VM biliyor, bu tik karar yok — span'e düşülmedi).
	Sources map[string]map[string]int `json:"sources,omitempty"`
	// Fallbacks — son tikte VM KULLANILMAYAN çiftlerin gerekçeleri (metrik →
	// gerekçe → sayı + ≤ 5 örnek servis). "VM yapılandırılmış ama servis X
	// span'e düştü" sorusunun cevabı; servis bazında ayrıntı explain ucunda.
	Fallbacks map[string]map[string]REDFallback `json:"fallbacks,omitempty"`
	// VMOnlyServices — span MV'sinde olmayıp yalnız VM'den değerlendirilen
	// servis sayısı (son tik).
	VMOnlyServices int `json:"vmOnlyServices"`
}

// REDFallback — bir gerekçenin sayımı + örnekler.
type REDFallback struct {
	Count    int      `json:"count"`
	Services []string `json:"services,omitempty"`
}

// redFallbackSamples — gerekçe başına örnek servis tavanı.
const redFallbackSamples = 5

var redObs atomic.Pointer[REDSourceStats]

// REDSourceObservability — son tikin ölçümü (lider olmayan pod'da boş = doğru cevap).
func REDSourceObservability() REDSourceStats {
	if p := redObs.Load(); p != nil {
		return *p
	}
	return REDSourceStats{}
}

func kindsReady(r [vmKindN]bool) string {
	var out []string
	for k := vmREDKind(0); k < vmKindN; k++ {
		if r[k] {
			out = append(out, k.String())
		}
	}
	return strings.Join(out, ",")
}

// publish — tik ölçümünü yayınlar + DEĞİŞİNCE (ya da sorgu koştuysa) log.
func (s *vmREDSource) publish(v *vmREDView, took time.Duration) {
	if s.routes {
		return // v0.10.1791 — endpoint kipi kendi ölçümünü yazar (SubjectObservability)
	}
	st := REDSourceStats{Mode: v.mode, Configured: v.configured, Reason: v.reason, LastUnix: time.Now().Unix(), LastMs: took.Milliseconds(),
		LastQueries: v.queries, LastQueryErrs: v.queryErrs, LastFetched: v.bucketsFetched, PendingBuckets: v.pending,
		Frames: len(s.store.frames), Services: len(s.store.names), Dropped: s.store.dropped, LastError: v.lastErr}
	for fi, f := range vmREDFamilies {
		m := s.meta[fi]
		fs := REDSourceFamilyStats{ID: f.ID, Present: m.present, Usable: m.ok, Reason: m.reason, ServiceLabel: m.scope.SvcLabel,
			EnvMatcher: m.scope.EnvMatcher, StatusLabels: strings.Join(m.scope.StatusLabels, ","), StatusServices: len(m.statusSvc),
			ReadyDetector: kindsReady(v.readyDet[fi]), ReadyBehavior: kindsReady(v.readyBeh[fi])}
		st.Families = append(st.Families, fs)
	}
	if prev := redObs.Load(); prev != nil && prev.Sources != nil {
		st.Sources = prev.Sources // tikin kaynak sayımı overlay'den sonra yazılır (recordSources)
		st.Fallbacks, st.VMOnlyServices = prev.Fallbacks, prev.VMOnlyServices
	}
	redObs.Store(&st)
	line := fmt.Sprintf("mode=%s configured=%v reason=%q pending=%d frames=%d svc=%d", st.Mode, st.Configured, st.Reason, st.PendingBuckets, st.Frames, st.Services)
	for _, fs := range st.Families {
		line += fmt.Sprintf(" | %s usable=%v %q det=%s beh=%s", fs.ID, fs.Usable, fs.Reason, fs.ReadyDetector, fs.ReadyBehavior)
	}
	if v.queries > 0 || line != s.lastLog {
		log.Printf("[anomaly] RED kaynağı: %d VM sorgusu (%d hata) %d ms, %d kova okundu · %s", v.queries, v.queryErrs, st.LastMs, v.bucketsFetched, line)
		s.lastLog = line
	}
}

// recordSources — overlay sonrası kaynak sayımı (metrik → kaynak → servis) +
// span'e düşüş / bekletme gerekçeleri.
func recordSources(used map[string]redSourceUsed, vmOnly int) {
	counts, fb := redSourceCounts(used)
	prev := REDSourceObservability()
	prev.Sources, prev.Fallbacks, prev.VMOnlyServices = counts, fb, vmOnly
	redObs.Store(&prev)
}

// redSourceCounts — SAF: sayım + gerekçeler (örnekler sıralı, ≤ redFallbackSamples).
func redSourceCounts(used map[string]redSourceUsed) (map[string]map[string]int, map[string]map[string]REDFallback) {
	counts := map[string]map[string]int{}
	fb := map[string]map[string]REDFallback{}
	keys := make([]string, 0, len(used))
	for k := range used {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		u := used[k]
		i := strings.LastIndexByte(k, '|')
		if i < 0 {
			continue
		}
		m := k[i+1:]
		if counts[m] == nil {
			counts[m] = map[string]int{}
		}
		src := redSourceSpan
		switch {
		case u.VM:
			src = redSourceVM
		case u.Hold:
			src = "hold"
		}
		counts[m][src]++
		if u.VM || u.Reason == "" || u.Reason == redReasonNotConfigured || u.Reason == redReasonSpanSetting {
			continue
		}
		if fb[m] == nil {
			fb[m] = map[string]REDFallback{}
		}
		reason := u.Reason
		if u.Hold {
			reason = "hold: " + reason
		}
		f := fb[m][reason]
		f.Count++
		if len(f.Services) < redFallbackSamples {
			f.Services = append(f.Services, k[:i])
		}
		fb[m][reason] = f
	}
	return counts, fb
}
