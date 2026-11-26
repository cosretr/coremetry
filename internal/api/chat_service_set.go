package api

// chat_service_set.go — v0.10.2471 (operator-reported 2026-11-26): servis adı
// kalıbından çözülen KÜMENİN kademesi, genel bakışı ve konuşma kapsamı.
// Ayrıştırma / çözüm kararı SAF ve chat_service_pattern.go'da.
//
// Akış (copilot_chat.go — netleştirme cevabından SONRA, doküman kademeleri /
// kaynak sorusu / endpoint kartı / guided'dan ÖNCE; v1 ve v2):
//
//	kalıp yok + etkin küme yok → hiçbir okuma, akış bayt bayt eski
//	0 eşleşme   → LLM'siz "yok" + en yakın adlar (kademe clarify, rota
//	              service_pattern_none); yumuşak kalıpta tur sahiplenilmez
//	1 eşleşme   → kalıp deyişi servis adıyla değişir, telemetri zorlanır, zincir
//	              o tek servis yazılmış gibi sürer
//	2..N        → küme konuşma kapsamı olur (ChatContext.ServiceSet) ve
//	              "incele / nasıl / sorun var mı / hangisi en yavaş" → guided
//	              service_set genel bakışı; log / trace / deploy / neden sorusu
//	              v2'de küme kapsamıyla serbest döngüye (v1'de genel bakış)
//	N > tavan   → hata, sonra trafik sırasıyla ilk servicePatternCap; kalan sayı
//	              cevapta, daraltma istenir
//
// Genel bakış (guidedServiceSetBundle) MEVCUT sınırlı okuyucuları kullanır,
// servis başına fan-out YOK:
//
//	metrik RED  — buildServicesREDMetric (Services listesinin örneklenmemiş
//	              okuyucusu; v0.10.2435 aile önceliği, tesisat rotaları dışlı)
//	span RED    — mcptools.ReadTeamServicesREDEnv (tek MV okuması; metrikte
//	              olmayan servis için, "örneklenmiş" etiketiyle)
//	problemler  — readTeamSummary (açık problem P1/P2/P3 + servis kırılımı,
//	              exception grupları; takım özetinin okuması)
//	anomaliler  — ListAnomalyFeed (Services IN, yalnız aktif, LIMIT)
//
// Her bölüm ok | none | missing | unread: okunamayan "OKUNAMADI"dır, "yok"
// değildir; değer asla tahmin edilmez. Sayılı tablo ve kapanış sorusu SUNUCU
// EKİdir (route.AnswerTail) — model yalnız kısa özet yazar.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// guidedServiceSet — servis adı kalıbından çözülen kümenin genel bakışı.
// Router-içi (chatServicePatternTier kurar); sınıflandırıcı listesinde yok.
const guidedServiceSet guidedIntent = "service_set"

// servicePatternNoneRoute — eşleşme yok cevabının /ai rota etiketi.
const servicePatternNoneRoute = "service_pattern_none"

// scopeReasonPattern — data-scope nedeni: kapsam bir servis adı kalıbı
// (uistream.ScopeReasons; kapsamlı, unscoped=false).
const scopeReasonPattern = "pattern"

const (
	// serviceSetListMax — kanıtta ve tabloda adıyla gösterilen servis tavanı.
	serviceSetListMax = 20
	// serviceSetExcMax — özetteki exception grubu satırı.
	serviceSetExcMax    = 3
	serviceSetREDTTL    = 30 * time.Second
	serviceSetSpanWait  = 5 * time.Second
	serviceSetAnomWait  = 5 * time.Second
	serviceSetAnomLimit = 200
	serviceSetRankWait  = 6 * time.Second
)

// Bölüm durumları (get_service_health raporuyla aynı sözlük).
const (
	setSecOK      = "ok"
	setSecNone    = "none"
	setSecMissing = "missing"
	setSecUnread  = "unread"
)

// servicePatternReq — kademenin istek girdileri.
type servicePatternReq struct {
	Msgs     []copilot.ChatMessage
	RangeS   int64
	AnchorTo time.Time
	Loc      *time.Location
	// Off — kademe bu turda koşmaz: çekmece / trace / açıklama bağlamı, yapısal
	// @-kapsam ya da /komut, kod turu, onay devamı, tek-tur değerlendirme.
	Off bool
}

// servicePatternTurn — kademenin sonucu. handled=false → zincir sürer
// (msgs / ctx güncellenmiş olabilir; toLoop → guided ve orta kademeler atlanır).
type servicePatternTurn struct {
	handled, ok bool
	tier        string
	ctx         context.Context
	msgs        []copilot.ChatMessage
	toLoop      bool
}

// servicePatternScope — SAF: kümenin kullanıcı kapsamı biçimi (v2 döngüsü +
// başlık rozeti). Takım alanı BOŞ: `team` argümanı enjekte edilmez.
func servicePatternScope(set *chatServiceSet) userScope {
	return userScope{Reason: scopeReasonPattern, Pattern: set.Label,
		Services: append([]string(nil), set.Services...), Trimmed: set.leftOut()}
}

// activeServicePatternScope — v2 döngüsü: etkin küme takım kapsamının YERİNE
// geçer (kullanıcının bu konuşmadaki açık seçimi; `ai.userScope.enabled`
// kapalıyken de). @-kapsam / komut kaçışında (escape) bu tur uygulanmaz.
func activeServicePatternScope(us userScope, cst *chatContextState, escape bool, now time.Time) userScope {
	if cst == nil || escape {
		return us
	}
	set := freshServiceSet(cst.ctx, now)
	if set == nil {
		return us
	}
	return servicePatternScope(set)
}

// serviceSetRangeS — SAF: sorunun açık penceresi > ekran penceresi > 30 dk.
func serviceSetRangeS(q string, ctxRangeS int64) (int64, bool) {
	rangeS, explicit := guidedRangeSExplicit(normalizeGuidedMsg(q))
	if !explicit && ctxRangeS > 0 {
		rangeS = snapRangeS(ctxRangeS)
	}
	return rangeS, explicit
}

// forceTelemetry — küme / tek servis turu doküman kademelerine girmez.
func forceTelemetry(ctx context.Context) context.Context {
	if forcedChatSource(ctx) != "" {
		return ctx
	}
	return withForcedChatSource(ctx, srcTelemetry)
}

// chatServicePatternTier — dosya başı. Kalıp ve etkin küme yoksa hiçbir şey
// okumaz ve hiçbir şeye dokunmaz.
func (s *Server) chatServicePatternTier(ctx context.Context, emit func(string, any), rq servicePatternReq, cst *chatContextState) servicePatternTurn {
	out := servicePatternTurn{ctx: ctx, msgs: rq.Msgs}
	q := strings.TrimSpace(lastUserText(rq.Msgs))
	if rq.Off || q == "" {
		return out
	}
	now := time.Now()
	p, ok := parseServicePattern(q)
	if !ok {
		return s.serviceSetFollowTier(ctx, emit, rq, cst, q, now, out)
	}
	names := s.guidedServiceNames(ctx)
	if len(names) == 0 {
		return out // katalog okunamadı: tahmin yok, akış aynen
	}
	matched := matchServicePattern(p, names)
	if len(matched) == 0 && !p.Explicit {
		return out // yumuşak kalıp katalogda yok: servis kümesi değil
	}
	rangeS, explicit := serviceSetRangeS(q, rq.RangeS)
	var rank map[string]svcRank
	rankSrc := ""
	if len(matched) > servicePatternCap {
		to := rq.AnchorTo
		if to.IsZero() {
			to = now
		}
		rank, rankSrc = s.serviceSetRank(ctx, matched, to.Add(-time.Duration(rangeS)*time.Second), to)
	}
	plan := planServiceSet(p, matched, names, rank, rankSrc, servicePatternCap)
	switch plan.Outcome {
	case setOutcomeNone:
		chatExchangeFrom(ctx).noteRoute(servicePatternNoneRoute)
		text, chips := servicePatternNoneTR(p, len(names), plan.Near)
		ans := map[string]any{"text": text}
		if len(chips) > 0 {
			ans["suggestions"] = chips
		}
		emit("answer", ans)
		out.handled, out.ok, out.tier = true, true, aisurface.TierClarify
		return out
	case setOutcomeSingle:
		svc := plan.Services[0]
		emit("step", map[string]string{"label": "kalıp «" + p.Label() + "» → tek servis: " + svc})
		cst.noteServiceSet(nil)
		out.msgs = replaceLastUserText(rq.Msgs, rewriteSinglePattern(q, p, svc))
		out.ctx = forceTelemetry(ctx)
		return out
	}
	set := &chatServiceSet{Kind: p.Kind, Pattern: p.Pattern, Label: p.Label(), Services: plan.Services,
		Total: plan.Total, RankedBy: plan.RankedBy, At: now.UnixNano()}
	cst.noteServiceSet(set)
	label := fmt.Sprintf("kalıp «%s» → %d servis", set.Label, set.Total)
	if n := set.leftOut(); n > 0 {
		label += fmt.Sprintf(" (ilk %d incelenir, %d dışarıda)", len(set.Services), n)
	}
	emit("step", map[string]string{"label": label})
	rest := strings.TrimSpace(q[:p.Start] + " " + q[p.End:])
	norm := normalizeGuidedMsg(rest)
	toks := guidedTokens(norm)
	if cosreV2Marked(ctx) && serviceSetOtherAsk(toks) {
		out.toLoop, out.ctx = true, forceTelemetry(ctx)
		return out
	}
	return s.runServiceSet(ctx, emit, rq, set, serviceSetFocus(norm, toks), q, rest, rangeS, explicit, out)
}

// serviceSetFollowTier — kalıp taşımayan tur: etkin kümenin bakımı (ömür,
// açık bırakma, konu değişimi) ve devam sorusu.
func (s *Server) serviceSetFollowTier(ctx context.Context, emit func(string, any), rq servicePatternReq, cst *chatContextState, q string, now time.Time, out servicePatternTurn) servicePatternTurn {
	if cst == nil || cst.ctx.ServiceSet == nil {
		return out
	}
	set := freshServiceSet(cst.ctx, now)
	if set == nil {
		cst.noteServiceSet(nil) // ömrü doldu
		return out
	}
	if clearsServiceSet(normalizeGuidedMsg(q)) {
		cst.noteServiceSet(nil)
		emit("step", map[string]string{"label": "kapsam: «" + set.Label + "» servis kümesi bırakıldı"})
		return out
	}
	named := explicitStateServices(q, s.guidedServiceNames(ctx))
	for _, n := range named {
		if !set.has(n) { // küme dışı servis adlandırıldı: konu değişti
			cst.noteServiceSet(nil)
			return out
		}
	}
	lastSet := cst.ctx.LastRoute != nil && cst.ctx.LastRoute.Intent == guidedServiceSet
	focus, ok := serviceSetFollowUp(q, lastSet, len(named) > 0)
	if !ok {
		return out
	}
	fresh := *set
	fresh.At = now.UnixNano()
	cst.noteServiceSet(&fresh)
	emit("step", map[string]string{"label": fmt.Sprintf("bağlam (önceki turdan): «%s» · %d servis", fresh.Label, fresh.Total)})
	rangeS, explicit := serviceSetRangeS(q, rq.RangeS)
	return s.runServiceSet(ctx, emit, rq, &fresh, focus, q, q, rangeS, explicit, out)
}

// runServiceSet — küme genel bakışını guided yoldan koşar (prefetch → anlatım
// → sunucu eki). Demet okunamazsa v2'de küme kapsamıyla serbest döngüye bırakır.
func (s *Server) runServiceSet(ctx context.Context, emit func(string, any), rq servicePatternReq, set *chatServiceSet, focus, q, rest string, rangeS int64, explicit bool, out servicePatternTurn) servicePatternTurn {
	route := guidedRoute{Intent: guidedServiceSet, Family: append([]string(nil), set.Services...),
		SetLabel: set.Label, SetKind: set.Kind, SetTotal: set.Total, SetRankedBy: set.RankedBy, SetFocus: focus}
	if env := extractEnvEntity(normalizeGuidedMsg(rest), s.guidedEnvNames(ctx)); env != "" {
		route.Env = env
	}
	if cosreV2Marked(ctx) { // başlık rozeti: "Kapsam: harvester-* · N servis"
		emit(chatScopeEventKind, servicePatternScope(set).event())
	}
	handled, ok := s.runGuidedRoute(ctx, emit, route, rangeS, q, rq.Msgs, "", "", "", "", rq.AnchorTo, rq.Loc)
	if !handled {
		out.toLoop, out.ctx = cosreV2Marked(ctx), forceTelemetry(ctx)
		return out
	}
	s.noteChatContextRoute(ctx, route, rangeS, explicit)
	out.handled, out.ok, out.tier = true, ok, aisurface.TierGuided
	return out
}

// serviceSetRank — tavanı aşan kalıbın sıralama girdisi (pencere toplamı):
// VM kaynağında filo metrik sıralaması (Services sayfasının 60 sn önbellekli
// okuması; yeni sorgu biçimi yok), değilse tek span MV okuması. İkisi de
// okunamazsa nil → çağıran alfabetik keser ve bunu söyler.
func (s *Server) serviceSetRank(ctx context.Context, matched []string, from, to time.Time) (map[string]svcRank, string) {
	names := append([]string(nil), matched...)
	sort.Strings(names)
	if len(names) > servicePatternRankMax {
		names = names[:servicePatternRankMax]
	}
	c, cancel := context.WithTimeout(ctx, serviceSetRankWait)
	defer cancel()
	if vm, ok := s.metricSource().(vmMetricSource); ok {
		if r, _, err := s.servicesMetricRankingCached(c, vm, "errorRate", "", "", from, to, false); err == nil && len(r.Services) > 0 {
			out := make(map[string]svcRank, len(names))
			for _, n := range names {
				e, ok := r.Services[n]
				if !ok {
					continue
				}
				rk := svcRank{Requests: e.Requests}
				if e.Value != nil {
					rk.Errors = e.Requests * *e.Value / 100
				}
				out[n] = rk
			}
			if len(out) > 0 {
				return out, "metric"
			}
		}
	}
	if s.store == nil {
		return nil, ""
	}
	rows, err := mcptools.ReadTeamServicesRED(c, s.mcpDeps(), names, from, to)
	if err != nil || len(rows) == 0 {
		return nil, ""
	}
	out := make(map[string]svcRank, len(rows))
	for _, r := range rows {
		out[r.Name] = svcRank{Errors: float64(r.ErrorCount), Requests: float64(r.SpanCount)}
	}
	return out, "span"
}

// ── okuma ──────────────────────────────────────────────────────────────

// serviceSetData — dört okumanın ham sonucu.
type serviceSetData struct {
	MetricStatus   string
	Metric         map[string]serviceREDMetricRow
	SpanOK         bool
	SpanEnvApplied bool
	Span           []chstore.ServiceSummary
	Summary        teamSummary
	AnomalyOK      bool
	Anomalies      map[string]int
}

// serviceSetREDKey — SAF (hash-all-inputs): sıralı servis kümesi + ortam +
// pencere kovası + etiket haritası.
func serviceSetREDKey(sorted []string, env string, from, to time.Time, svcLabel, envLabel, clusterLabel string) string {
	parts := append([]string{env, cacheBucket(from, to), svcLabel, envLabel, clusterLabel}, sorted...)
	return "copilot:svcset:red:v1:" + fnvStr(parts...)
}

// readServiceSet — dört sınırlı okuma paralel; her biri kendi zaman aşımıyla
// soft-fail (okunamayan bölüm diğerlerini düşürmez).
func (s *Server) readServiceSet(ctx context.Context, label string, names []string, env string, from, to time.Time) serviceSetData {
	d := serviceSetData{MetricStatus: setSecMissing}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	var wg sync.WaitGroup
	if vm, ok := s.metricSource().(vmMetricSource); ok {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lm := vm.MetricLabelMap().Normalized()
			key := serviceSetREDKey(sorted, env, from, to, lm.Service, lm.Env, lm.Cluster)
			var resp servicesREDMetricResponse
			if !s.metricChartCacheGet(ctx, key, &resp) || resp.Services == nil {
				resp = s.buildServicesREDMetric(ctx, vm, sorted, env, "", from, to, false)
				if len(resp.Notes) == 0 { // kısmi / hatalı okuma önbelleğe girmez
					s.metricChartCacheSet(ctx, key, resp, serviceSetREDTTL)
				}
			}
			switch {
			case len(resp.Services) > 0:
				d.MetricStatus, d.Metric = setSecOK, resp.Services
			case len(resp.Notes) > 0:
				d.MetricStatus = setSecUnread
			default:
				d.MetricStatus = setSecNone
			}
		}()
	}
	if s.store != nil {
		wg.Add(2)
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, serviceSetSpanWait)
			defer cancel()
			rows, applied, err := mcptools.ReadTeamServicesREDEnv(c, s.mcpDeps(), sorted, env, from, to)
			if err == nil {
				d.SpanOK, d.SpanEnvApplied, d.Span = true, applied, rows
			}
		}()
		go func() {
			defer wg.Done()
			c, cancel := context.WithTimeout(ctx, serviceSetAnomWait)
			defer cancel()
			evs, err := s.store.ListAnomalyFeed(c, chstore.ListAnomalyEventsFilter{
				Services: sorted, ActiveOnly: true, Limit: serviceSetAnomLimit, MaxExecSec: int(serviceSetAnomWait / time.Second)})
			if err != nil {
				return
			}
			counts := map[string]int{}
			for _, e := range evs {
				if svc := strings.TrimSpace(e.Service); svc != "" {
					counts[svc]++
				}
			}
			d.AnomalyOK, d.Anomalies = true, counts
		}()
		d.Summary = s.readTeamSummary(ctx, "svcset:"+label, sorted, env, from, to)
	}
	wg.Wait()
	return d
}

// ── montaj (SAF) ───────────────────────────────────────────────────────

// serviceSetInput — assembleServiceSet girdisi.
type serviceSetInput struct {
	Label, Kind string
	Names       []string // kümedeki servisler (tavanlı)
	Total       int      // kalıba uyan servis sayısı
	RankedBy    string   // tavan aşıldıysa sıra kaynağı
	Focus       string
	RangeS      int64
	Env         string
	Data        serviceSetData
}

// serviceSetRow — bir servisin satırı. Source: "metric" | "span" | "".
type serviceSetRow struct {
	Name      string
	Source    string
	Family    string  // metrik ailesi (http, grpc_stable, spanmetrics…)
	RatePerS  float64 // metrik: istek/sn
	Spans     uint64  // span: saklanan span sayısı
	ErrPct    float64
	HasErr    bool
	Errors    float64
	P99Ms     float64
	HasP99    bool
	Problems  int
	P1        int
	Anomalies int
}

func (r serviceSetRow) troubled() bool { return (r.HasErr && r.ErrPct > 0) || r.Problems > 0 }

// serviceSetView — genel bakışın saf görünümü.
type serviceSetView struct {
	Label, Kind   string
	Total         int
	LeftOut       int
	RankedBy      string
	Focus         string
	RangeS        int64
	Env           string
	EnvApplied    bool
	Rows          []serviceSetRow // odağa göre sıralı (errors odağında yalnız sorunlular)
	All           int             // kümedeki servis sayısı (süzgeçten önce)
	MetricStatus  string
	SpanStatus    string
	ProblemStatus string
	AnomalyStatus string
	MetricN       int
	SpanN         int
	SilentN       int
	Summary       teamSummary
}

func sourceRank(src string) int {
	switch src {
	case "metric":
		return 0
	case "span":
		return 1
	}
	return 2
}

// assembleServiceSet — SAF: okumalar → satırlar + bölüm durumları + sıra.
// Metrik (örneklenmemiş) satırı span satırını ezer; span yalnız metrikte
// olmayan servis için ve "örneklenmiş" olarak. Varsayılan sıra: sorunlu
// (hata > 0 ya da açık problem) önce, sonra kaynak (metrik, span, verisiz),
// hata oranı, hata sayısı, açık problem, trafik, ad.
func assembleServiceSet(in serviceSetInput) serviceSetView {
	d := in.Data
	v := serviceSetView{Label: in.Label, Kind: in.Kind, Total: in.Total, RankedBy: in.RankedBy, Focus: in.Focus,
		RangeS: in.RangeS, Env: in.Env, EnvApplied: d.SpanEnvApplied, All: len(in.Names),
		MetricStatus: d.MetricStatus, Summary: d.Summary}
	if v.Total < v.All {
		v.Total = v.All
	}
	v.LeftOut = v.Total - v.All
	if v.MetricStatus == "" {
		v.MetricStatus = setSecMissing
	}
	span := map[string]chstore.ServiceSummary{}
	for _, r := range d.Span {
		span[r.Name] = r
	}
	rows := make([]serviceSetRow, 0, len(in.Names))
	for _, n := range in.Names {
		row := serviceSetRow{Name: n}
		if m, ok := d.Metric[n]; ok && d.MetricStatus == setSecOK {
			row.Source, row.Family, row.RatePerS = "metric", m.Source, m.RatePerS
			if m.ErrorRate != nil {
				row.HasErr, row.ErrPct, row.Errors = true, *m.ErrorRate, m.Requests**m.ErrorRate/100
			}
			if m.P99Ms != nil {
				row.HasP99, row.P99Ms = true, *m.P99Ms
			}
			v.MetricN++
		} else if sp, ok := span[n]; ok && d.SpanOK && sp.SpanCount > 0 {
			row.Source, row.Spans = "span", sp.SpanCount
			row.HasErr, row.ErrPct, row.Errors = true, sp.ErrorRate, float64(sp.ErrorCount)
			row.HasP99, row.P99Ms = true, sp.P99Ms
			v.SpanN++
		} else {
			v.SilentN++
		}
		if d.Summary.ProblemsOK {
			row.Problems, row.P1 = d.Summary.OpenBySvc[n], d.Summary.P1BySvc[n]
		}
		if d.AnomalyOK {
			row.Anomalies = d.Anomalies[n]
		}
		rows = append(rows, row)
	}
	switch {
	case !d.SpanOK:
		v.SpanStatus = setSecUnread
	case len(d.Span) == 0:
		v.SpanStatus = setSecNone
	default:
		v.SpanStatus = setSecOK
	}
	switch {
	case !d.Summary.ProblemsOK:
		v.ProblemStatus = setSecUnread
	case d.Summary.P1+d.Summary.P2+d.Summary.P3 == 0:
		v.ProblemStatus = setSecNone
	default:
		v.ProblemStatus = setSecOK
	}
	switch {
	case !d.AnomalyOK:
		v.AnomalyStatus = setSecUnread
	case len(d.Anomalies) == 0:
		v.AnomalyStatus = setSecNone
	default:
		v.AnomalyStatus = setSecOK
	}
	byDefault := func(a, b serviceSetRow) bool {
		if a.troubled() != b.troubled() {
			return a.troubled()
		}
		if sa, sb := sourceRank(a.Source), sourceRank(b.Source); sa != sb {
			return sa < sb
		}
		if a.ErrPct != b.ErrPct {
			return a.ErrPct > b.ErrPct
		}
		if a.Errors != b.Errors {
			return a.Errors > b.Errors
		}
		if a.Problems != b.Problems {
			return a.Problems > b.Problems
		}
		if a.RatePerS != b.RatePerS {
			return a.RatePerS > b.RatePerS
		}
		if a.Spans != b.Spans {
			return a.Spans > b.Spans
		}
		return a.Name < b.Name
	}
	less := byDefault
	switch in.Focus {
	case setFocusSlow: // p99 azalan (kaynak etiketi satırda); p99'u olmayan sona
		less = func(a, b serviceSetRow) bool {
			if a.HasP99 != b.HasP99 {
				return a.HasP99
			}
			if a.P99Ms != b.P99Ms {
				return a.P99Ms > b.P99Ms
			}
			return a.Name < b.Name
		}
	case setFocusTraffic: // istek/sn (metrik) önce; span sayısı istek hızı değildir
		less = func(a, b serviceSetRow) bool {
			if sa, sb := sourceRank(a.Source), sourceRank(b.Source); sa != sb {
				return sa < sb
			}
			if a.RatePerS != b.RatePerS {
				return a.RatePerS > b.RatePerS
			}
			if a.Spans != b.Spans {
				return a.Spans > b.Spans
			}
			return a.Name < b.Name
		}
	case setFocusProblems:
		less = func(a, b serviceSetRow) bool {
			if a.P1 != b.P1 {
				return a.P1 > b.P1
			}
			if a.Problems != b.Problems {
				return a.Problems > b.Problems
			}
			return byDefault(a, b)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return less(rows[i], rows[j]) })
	if in.Focus == setFocusErrors {
		kept := rows[:0:0]
		for _, r := range rows {
			if r.troubled() {
				kept = append(kept, r)
			}
		}
		rows = kept
	}
	v.Rows = rows
	return v
}

// usable — en az bir RED kaynağı okundu (ikisi de okunamadıysa demet düşer).
func (v serviceSetView) usable() bool {
	return v.MetricStatus == setSecOK || v.MetricStatus == setSecNone || v.SpanStatus != setSecUnread
}

// names — çip / link sırası (en çok max).
func (v serviceSetView) names(max int) []string {
	out := make([]string, 0, max)
	for _, r := range v.Rows {
		if len(out) >= max {
			break
		}
		out = append(out, r.Name)
	}
	return out
}

func setSecTR(status, missing string) string {
	switch status {
	case setSecOK:
		return "okundu"
	case setSecNone:
		return "yok (okundu, boş)"
	case setSecMissing:
		return "ölçülmüyor (" + missing + ")"
	}
	return "OKUNAMADI"
}

func setFocusTR(focus string) string {
	switch focus {
	case setFocusSlow:
		return "en yavaş (p99 azalan)"
	case setFocusErrors:
		return "yalnız hata veren ya da açık problemi olan servisler"
	case setFocusTraffic:
		return "en yoğun (istek/sn azalan)"
	case setFocusProblems:
		return "açık problemi olanlar önce"
	}
	return "sorunlu olanlar önce, hata oranına göre"
}

func setRankedByTR(by string) string {
	switch by {
	case "metric":
		return "örneklenmemiş metrik"
	case "span":
		return "örneklenmiş span"
	}
	return "sıra okunamadı — alfabetik"
}

// serviceSetLineTR — SAF: bir servisin kanıt satırı.
func serviceSetLineTR(r serviceSetRow) string {
	var b strings.Builder
	switch r.Source {
	case "metric":
		fmt.Fprintf(&b, "- %s [metrik · %s]: %.2f istek/sn", r.Name, r.Family, r.RatePerS)
		if r.HasErr {
			fmt.Fprintf(&b, ", hata %%%.2f", r.ErrPct)
		} else {
			b.WriteString(", hata oranı ölçülmüyor (durum etiketi yok)")
		}
		if r.HasP99 {
			fmt.Fprintf(&b, ", p99 %.0f ms", r.P99Ms)
		}
	case "span":
		fmt.Fprintf(&b, "- %s [span · ÖRNEKLENMİŞ, gerçek oran değil]: %s span, saklanan span'lerde hata payı %%%.2f (%.0f hata), p99 %.0f ms",
			r.Name, fmtThousands(r.Spans), r.ErrPct, r.Errors, r.P99Ms)
	default:
		fmt.Fprintf(&b, "- %s: bu pencerede metrik ve span verisi yok", r.Name)
	}
	if r.Problems > 0 {
		fmt.Fprintf(&b, " · açık problem %d", r.Problems)
		if r.P1 > 0 {
			fmt.Fprintf(&b, " (P1 %d)", r.P1)
		}
	}
	if r.Anomalies > 0 {
		fmt.Fprintf(&b, " · aktif anomali %d", r.Anomalies)
	}
	return b.String()
}

// renderServiceSetEvidenceTR — SAF: modelin kanıt bloğu. Her sayı bir
// okuyucudan; okunamayan bölüm adıyla söylenir.
func renderServiceSetEvidenceTR(v serviceSetView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "SERVİS KÜMESİ — adı «%s» kalıbına uyan servisler: %d servis eşleşti; pencere son %s.\n", v.Label, v.Total, fmtAgoTR(v.RangeS))
	if v.LeftOut > 0 {
		fmt.Fprintf(&b, "TAVAN: hata, sonra trafik sırasına göre (%s) ilk %d servis incelendi, %d servis DIŞARIDA kaldı. Bunu söyle ve kalıbı daraltmasını öner.\n",
			setRankedByTR(v.RankedBy), v.All, v.LeftOut)
	}
	fmt.Fprintf(&b, "KAYNAKLAR: metrik RED (örneklenmemiş) = %s; span RED (örneklenmiş) = %s; açık problemler = %s; aktif anomaliler = %s.\n",
		setSecTR(v.MetricStatus, "metrik kaynağı VictoriaMetrics değil"), setSecTR(v.SpanStatus, ""), setSecTR(v.ProblemStatus, ""), setSecTR(v.AnomalyStatus, ""))
	if v.Env != "" && !v.EnvApplied && v.SpanN > 0 {
		fmt.Fprintf(&b, "Not: span değerleri tüm ortamların toplamı; %q ortam daraltması span satırlarına UYGULANMADI.\n", v.Env)
	}
	fmt.Fprintf(&b, "Kaynak dağılımı: %d servis metrikten, %d servis örneklenmiş span'den, %d serviste bu pencerede veri yok.\n", v.MetricN, v.SpanN, v.SilentN)
	fmt.Fprintf(&b, "Sıra: %s.\n", setFocusTR(v.Focus))
	if len(v.Rows) == 0 {
		if v.Focus == setFocusErrors {
			b.WriteString("Kümede hata veren ya da açık problemi olan servis yok (okunan kaynaklara göre).\n")
		}
	}
	for i, r := range v.Rows {
		if i >= serviceSetListMax {
			fmt.Fprintf(&b, "… ve %d servis daha (bu sırada daha geride).\n", len(v.Rows)-serviceSetListMax)
			break
		}
		b.WriteString(serviceSetLineTR(r) + "\n")
	}
	switch v.ProblemStatus {
	case setSecOK:
		fmt.Fprintf(&b, "Açık problemler (küme toplamı): P1 %d · P2 %d · P3 %d", v.Summary.P1, v.Summary.P2, v.Summary.P3)
		if v.Summary.ProblemsCapped {
			b.WriteString(" (tarama tavanında — en az)")
		}
		b.WriteString(".\n")
	case setSecNone:
		b.WriteString("Açık problem yok (okundu).\n")
	default:
		b.WriteString("Açık problemler OKUNAMADI — \"problem yok\" DEME.\n")
	}
	if v.AnomalyStatus == setSecUnread {
		b.WriteString("Aktif anomaliler OKUNAMADI — \"anomali yok\" DEME.\n")
	}
	if v.Summary.ExceptionsOK {
		for i, x := range v.Summary.Exceptions {
			if i >= serviceSetExcMax {
				break
			}
			fmt.Fprintf(&b, "Exception grubu: %s — %s (%s kez)\n", x.Service, clipRunes(oneLine(x.Type+" "+x.Message), 100), fmtThousands(x.Occurrences))
		}
	}
	b.WriteString("KURAL: 2-4 cümlelik kısa özet yaz. Sıradaki İLK servisi adı ve sayısıyla söyle; hiçbir serviste hata ve açık problem yoksa bunu AÇIKÇA söyle ve kimseyi işaretleme. " +
		"Örneklenmiş span değerini gerçek oran gibi sunma, \"örneklenmiş\" de. OKUNAMADI ya da ölçülmüyor denen bölüm için \"yok\" deme. " +
		"Servis tablosu ve kapanış sorusu cevabın ALTINA sunucu tarafından ekleniyor — tabloyu TEKRAR YAZMA, kapanış sorusu SORMA. " +
		"Listede olmayan servis adı ya da sayı UYDURMA.\n")
	return b.String()
}

// renderServiceSetTailTR — SAF: sunucu eki (tablo + dürüstlük notları +
// kapanış sorusu). Başında "\n\n" (anlatımdan ayrılır).
func renderServiceSetTailTR(v serviceSetView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n**«%s» servisleri** — %d servis, %s (son %s):\n\n", mdCell(v.Label), v.Total, setFocusTR(v.Focus), fmtAgoTR(v.RangeS))
	shown := 0
	if len(v.Rows) > 0 {
		b.WriteString("| Servis | Trafik | Hata % | p99 | Kaynak | Açık problem | Anomali |\n| --- | ---: | ---: | ---: | --- | ---: | ---: |\n")
		for _, r := range v.Rows {
			if shown >= serviceSetListMax {
				break
			}
			traffic, errc, p99, src := "—", "—", "—", "veri yok"
			switch r.Source {
			case "metric":
				traffic, src = fmt.Sprintf("%.2f istek/sn", r.RatePerS), "metrik"
			case "span":
				traffic, src = fmtThousands(r.Spans)+" span", "span (örneklenmiş)"
			}
			if r.HasErr {
				errc = fmt.Sprintf("%%%.2f", r.ErrPct)
			}
			if r.HasP99 {
				p99 = fmt.Sprintf("%.0f ms", r.P99Ms)
			}
			prob, anom := "—", "—"
			if v.ProblemStatus != setSecUnread {
				prob = fmt.Sprintf("%d", r.Problems)
				if r.P1 > 0 {
					prob += fmt.Sprintf(" (P1 %d)", r.P1)
				}
			}
			if v.AnomalyStatus != setSecUnread {
				anom = fmt.Sprintf("%d", r.Anomalies)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n", mdCell(r.Name), traffic, errc, p99, src, prob, anom)
			shown++
		}
		b.WriteString("\n")
	} else if v.Focus == setFocusErrors {
		b.WriteString("Bu kümede hata veren ya da açık problemi olan servis yok.\n\n")
	}
	if more := len(v.Rows) - shown; more > 0 {
		fmt.Fprintf(&b, "+%d servis daha (bu sırada daha geride).\n", more)
	}
	if v.LeftOut > 0 {
		fmt.Fprintf(&b, "_Kalıba %d servis uyuyor; hata, sonra trafik sırasına göre (%s) ilk %d incelendi, %d servis dışarıda kaldı. Kalıbı daraltırsan hepsini görebilirim._\n",
			v.Total, setRankedByTR(v.RankedBy), v.All, v.LeftOut)
	}
	var notes []string
	switch v.MetricStatus {
	case setSecMissing:
		notes = append(notes, "örneklenmemiş metrik RED ölçülmüyor (metrik kaynağı VictoriaMetrics değil) — değerler örneklenmiş span'den")
	case setSecUnread:
		notes = append(notes, "metrik RED okunamadı — değerler örneklenmiş span'den")
	}
	if v.SpanStatus == setSecUnread {
		notes = append(notes, "span RED okunamadı")
	}
	if v.ProblemStatus == setSecUnread {
		notes = append(notes, "açık problemler okunamadı (okunamadı ≠ yok)")
	}
	if v.AnomalyStatus == setSecUnread {
		notes = append(notes, "aktif anomaliler okunamadı (okunamadı ≠ yok)")
	}
	if v.SilentN > 0 {
		notes = append(notes, fmt.Sprintf("%d servis bu pencerede metrik ya da span üretmedi", v.SilentN))
	}
	if len(notes) > 0 {
		b.WriteString("_Not: " + strings.Join(notes, "; ") + "._\n")
	}
	b.WriteString("\n**" + teamOverviewClosingQ + "** Alttaki çiplerden birini seç ya da servis adını yaz. " +
		"Bu küme konuşmanın kapsamı olarak kaldı: «hangisi en yavaş», «hata verenleri göster» diyebilirsin; bırakmak için «tüm servisler» yaz.")
	return b.String()
}

// serviceSetLinks — SAF: Servisler sayfası ad süzgeciyle (alt-dize; önek /
// sonek kalıbında üst küme) + sıradaki ilk servis.
func serviceSetLinks(route guidedRoute) []guidedAnswerLink {
	core := strings.Trim(strings.ReplaceAll(route.SetLabel, "*", " "), " ")
	if i := strings.IndexByte(core, ' '); i >= 0 { // glob: yalnız ilk parça süzgeç olur
		core = core[:i]
	}
	links := []guidedAnswerLink{{Label: "Servisler", Href: "/services"}}
	if core != "" {
		links[0] = guidedAnswerLink{Label: "Servisler · " + route.SetLabel, Href: "/services?q=" + url.QueryEscape(core)}
	}
	if len(route.TeamServices) > 0 {
		svc := route.TeamServices[0]
		links = append(links, guidedAnswerLink{Label: svc, Href: "/service?name=" + url.QueryEscape(svc)})
	}
	return links
}

// guidedServiceSetBundle — küme genel bakışının demeti (dosya başı).
func (s *Server) guidedServiceSetBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	names := route.Family
	if len(names) == 0 {
		return "", "", errors.New("service_set: küme boş")
	}
	nRes := emitGuidedStep(emit, "resolve_service_pattern", fmt.Sprintf(`{"pattern":%s,"matched":%d}`, jsonStr(route.SetLabel), route.SetTotal))
	emitGuidedStepResult(emit, nRes, "resolve_service_pattern",
		fmt.Sprintf("«%s» kalıbına uyan servisler (%d; incelenen %d):\n- %s\n", route.SetLabel, route.SetTotal, len(names), strings.Join(names, "\n- ")), nil)
	nRead := emitGuidedStep(emit, "service_set_overview", fmt.Sprintf(`{"services":%d}`, len(names)))
	v := assembleServiceSet(serviceSetInput{Label: route.SetLabel, Kind: route.SetKind, Names: names, Total: route.SetTotal,
		RankedBy: route.SetRankedBy, Focus: route.SetFocus, RangeS: rangeS, Env: route.Env,
		Data: s.readServiceSet(ctx, route.SetLabel, names, route.Env, from, to)})
	if !v.usable() {
		err := errors.New("service_set: metrik ve span RED okunamadı")
		emitGuidedStepResult(emit, nRead, "service_set_overview", "", err)
		return "", "", err
	}
	ev := renderServiceSetEvidenceTR(v)
	emitGuidedStepResult(emit, nRead, "service_set_overview", ev, nil)
	route.AnswerTail = renderServiceSetTailTR(v)
	route.TeamServices = v.names(teamServicesMaxRows)
	if len(route.TeamServices) == 0 { // "hata verenler" boş: çipler yine kümeyi adlandırsın
		route.TeamServices = append([]string(nil), names[:min(len(names), teamServicesMaxRows)]...)
	}
	route.TeamFollowups = teamServicePickChips(route.TeamServices, route.Env)
	src := fmt.Sprintf("servis kümesi «%s» (%d servis) — metrik RED %s, span RED %s, açık problemler %s, anomaliler %s (son %s)",
		route.SetLabel, v.Total, setSecTR(v.MetricStatus, "VM yok"), setSecTR(v.SpanStatus, ""), setSecTR(v.ProblemStatus, ""), setSecTR(v.AnomalyStatus, ""), fmtAgoTR(rangeS))
	return ev + serviceSetTailNoteTR(v), src, nil
}

// serviceSetTailNoteTR — SAF: modele, altına ne ekleneceğini söyleyen kısa not.
func serviceSetTailNoteTR(v serviceSetView) string {
	shown := len(v.Rows)
	if shown > serviceSetListMax {
		shown = serviceSetListMax
	}
	return fmt.Sprintf("SUNUCU EKİ (cevabın ALTINA deterministik eklenir, sen YAZMA): %d servislik tablo (kümede %d servis) ve kapanış sorusu %q.\n",
		shown, v.All, teamOverviewClosingQ)
}
