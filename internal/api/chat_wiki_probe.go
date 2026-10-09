package api

// chat_wiki_probe.go — v0.10.1143: İÇERİK tabanlı wiki yoklaması.
//
// Operatör (prod): /cosre'de "Sık karşılaşılan cache refresh hataları
// nelerdir?" → cevap telemetriden geldi ("paylaşılan veriler içerisinde cache
// refresh hatalarına dair bilgi bulunmamaktadır… Kaynak: açık problemler +
// triage önceliği + kök-neden hipotezleri (canlı)"); wiki'de başlığı tam da
// "Cache Refresh Akışı" olan sayfa vardı. Kök neden:
//
//   - chat_wiki_tier.go wikiChatAnswer: soru wikiQuestionCue'ya göre ne güçlü
//     ne zayıf işaret taşıyordu ("nelerdir" işaret değil) → kademe sessizce
//     çekiliyordu;
//   - copilot_guided.go routeGuidedIntentOpts: servis adı yok + "hata"
//     (hasErrorSignal) → son dal `case hasErrorSignal(toks)` filo geneli
//     guidedProblems → runGuidedRoute → guidedProblemsBundle; wiki hiç
//     aranmıyordu.
//
// Karar İÇERİKTEN verilir, kalıp sözcükten DEĞİL (kalıp listesi büyütülmez):
// işaretsiz soruda tek YEREL lexical arama (wiki.LiveOff — ADO'ya istek yok,
// embed yok) koşar ve en iyi parça sorunun nadir terimlerinin çoğunu
// (idf-ağırlıklı) taşıyor VE terimlerden biri sayfa başlığı/başlık yolunda
// geçiyorsa (wikiProbeStrong) cevap wiki'den gelir; altına guided'ın bu
// soruya seçeceği rota "Telemetri için:" çipi olarak eklenir. Kapı geçmezse
// HİÇBİR olay basılmaz, akış bayt bayt eski yolda.
//
// Ters yön: guidedProblems servis adsız soruda SIFIR problem döndürürse
// (markFleetZeroMatch — eşleşmeyi üreten kod işaretler, dize eşleştirme yok)
// aynı yoklamanın kapıyı geçen sayfası cevabın sonuna yalnız bağlantı olarak
// eklenir: "Wiki'de ilgili olabilir: <başlık>" (anlatım yok).
//
// Maliyet: yoklama alışveriş başına EN ÇOK bir kez arar (sonuç ctx'teki
// durumda saklanır; ters yön aynı sonucu kullanır). Genel telemetri ve soru
// kalıbı sözcükleri ayıklandıktan sonra < wikiProbeMinTerms içerik terimi
// kalan soru (ör. "açık problemler nelerdir", "hatalar") HİÇ aramaz.
// Kapı kademeyle aynı: API token'ı yok, panel/çekmece bağlamı yok, wiki
// kapalı → hiçbir şey değişmez.

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// wikiProbeCoverage — en iyi parçanın idf-ağırlıklı terim kapsamı tabanı.
	// 0.7: iki içerik terimli soruda İKİSİ de parçada olmalı (yalnız biri
	// ≈0.5 verir); üç terimde eksik terim ancak diğerlerinden belirgin YAYGIN
	// ise (düşük idf) geçer. Yani "sorunun nadir kavramlarının çoğu tek
	// parçada" — RAG'ın 0.5 skor tabanından sıkı, çünkü burada soru wiki'yi
	// işaret etmiyor ve yanlış pozitif bir telemetri sorusunu kaçırır.
	wikiProbeCoverage = 0.7
	// wikiProbeMinTerms — ayıklama sonrası en az içerik terimi. Tek terimde
	// kapsam terim geçtiği an 1.0'dır (ölçüt anlamsızlaşır: "kafka hataları"
	// Kafka sayfasına kaçardı); iki ayrı nadir terim gerçek bir konu ifadesi.
	wikiProbeMinTerms = 2
	// wikiProbeTimeout — yoklamanın tavanı: wiki CH okumalarının kendi
	// max_execution_time'ı (aday sorgusu 5 sn) ile aynı; sohbetin önünde durur.
	wikiProbeTimeout = 5 * time.Second
	// wikiProbeZeroHintPrefix — ters yön bağlantı satırı.
	wikiProbeZeroHintPrefix = "Wiki'de ilgili olabilir: "
)

// wikiProbeGenericExact / wikiProbeGenericPrefixes — kapsamaya SAYILMAYAN
// sözcükler (katlanmış; TEK YER). Genel telemetri sözcükleri her telemetri
// sorusunda geçer — sayılsalar her "hata" sorusu wiki'ye kaçardı; soru
// kalıbı niteleyicileri ("sık karşılaşılan", "yaygın", "bilinen") içerik
// değil istek biçimidir — sayılsalar (korpusta nadir → yüksek idf) gerçek
// konuyu taşıyan sayfanın kapsamını tabanın altına iterdi. Stopword'ler
// zaten wiki.QueryTerms'te düşer.
var (
	wikiProbeGenericExact = map[string]bool{
		"err": true, "acik": true, "open": true, "aktif": true, "active": true, "current": true,
		"gc": true, "jvm": true, "cpu": true, "ram": true, "rps": true, "qps": true, "tps": true,
		"p50": true, "p75": true, "p90": true, "p95": true, "p99": true, "p999": true,
		"5xx": true, "4xx": true, "500": true, "slo": true, "sla": true, "apdex": true,
		"son": true, "genel": true, "common": true, "known": true, "typical": true, "frequent": true,
	}
	wikiProbeGenericPrefixes = []string{
		// hata / başarısızlık
		"hata", "error", "exception", "istisna", "fail", "basarisiz", "ariza", "timeout",
		// özne / kayıt türleri
		"servis", "service", "problem", "sorun", "alarm", "alert", "incident",
		"trace", "span", "log", "metrik", "metric", "istek", "request", "cagri",
		// sağlık / performans
		"yavas", "slow", "gecikme", "latency", "performan", "saglik", "health", "durum", "status",
		"oran", "rate", "trafik", "traffic", "throughput", "bellek", "memory", "heap",
		"deploy", "rollout", "release", "surum", "pod",
		// soru kalıbı niteleyicileri
		"sik", "karsilas", "yaygin", "bilinen", "tipik", "olasi", "issue",
	}
)

// wikiProbeGenericWord — SAF: katlanmış terim genel sözcük mü.
func wikiProbeGenericWord(t string) bool {
	if wikiProbeGenericExact[t] {
		return true
	}
	for _, p := range wikiProbeGenericPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// wikiProbeTerms — SAF: yoklamanın içerik terimleri (wiki.QueryTerms, genel
// sözcükler hariç).
func wikiProbeTerms(question string) []string {
	var out []string
	for _, t := range wiki.QueryTerms(question) {
		if !wikiProbeGenericWord(t) {
			out = append(out, t)
		}
	}
	return out
}

// wikiProbeTimeWords — açık zaman/güncellik çapası (katlanmış, tam sözcük).
var wikiProbeTimeWords = map[string]bool{
	"bugun": true, "bugunku": true, "bugunun": true, "dun": true, "dunku": true, "dunden": true,
	"today": true, "yesterday": true, "tonight": true, "simdi": true, "simdiki": true, "anlik": true,
	"now": true, "currently": true, "son": true, "gece": true, "sabah": true, "aksam": true,
	"saat": true, "saatte": true, "saatlik": true, "dakika": true, "dakikada": true, "hafta": true,
	"haftaki": true, "week": true, "hour": true, "hours": true, "minute": true, "minutes": true,
}

// wikiProbeThresholdRe — sayısal eşik: birimli sayı (250ms, %5, 3 sn), karşılaştırma
// (> 500) ya da sayı + üzeri/üstü/altı/fazla/az.
var wikiProbeThresholdRe = regexp.MustCompile(`(?:\d+(?:[.,]\d+)?\s*(?:ms|sn|saniye|sec|s|%|rps|qps)(?:[^a-z0-9]|$))|(?:%\s*\d)|(?:[<>]=?\s*\d)|(?:\d+\s+(?:uzeri|ustu|alti|fazla|az)\b)`)

// wikiProbeTelemetryAnchor — SAF: soru açık bir telemetri çapası taşıyor mu
// (zaman penceresi, trace/span/istek/problem kimliği, sayısal eşik). Çapalı
// soru canlı veriyi soruyor: yoklama hiç koşmaz.
func wikiProbeTelemetryAnchor(question string) bool {
	norm := normalizeGuidedMsg(question)
	if looksLikeAbsoluteWindow(question) || guidedRangeRe.MatchString(norm) ||
		extractTraceID(norm) != "" || extractSpanID(norm) != "" || hasStructuredRequestID(norm) {
		return true
	}
	folded := wiki.Fold(question)
	words := strings.FieldsFunc(folded, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-'
	})
	for i, w := range words {
		if wikiProbeTimeWords[w] || chstore.IsProblemDisplayID(w) {
			return true
		}
		if w == "su" && i+1 < len(words) && strings.HasPrefix(words[i+1], "an") && len(words[i+1]) <= 5 {
			return true // "şu an", "şu anda", "şu anki"
		}
	}
	return wikiProbeThresholdRe.MatchString(folded)
}

// wikiProbeStrong — SAF kapı: en iyi isabet YEREL, kapsamı ≥ wikiProbeCoverage
// ve eşleşen terimlerden biri başlık/başlık yolunda.
func wikiProbeStrong(hits []wiki.Hit) bool {
	if len(hits) == 0 {
		return false
	}
	h := hits[0]
	return !h.Live && h.TermCoverage >= wikiProbeCoverage && h.HeadMatch
}

// wikiProbeState — alışveriş başına yoklama durumu (ctx'te).
type wikiProbeState struct {
	w        *wiki.Service
	question string
	mu       sync.Mutex
	done     bool
	hits     []wiki.Hit // kapıyı geçtiyse seçilmiş isabetler, yoksa nil
	zero     atomic.Bool
}

type wikiProbeKey struct{}

func wikiProbeFromCtx(ctx context.Context) *wikiProbeState {
	st, _ := ctx.Value(wikiProbeKey{}).(*wikiProbeState)
	return st
}

// armWikiProbe — yoklama durumunu kurar (ters yönün de kapısı). Kapı kapalıysa
// ctx AYNEN döner.
func (s *Server) armWikiProbe(ctx context.Context, msgs []copilot.ChatMessage, tc wikiTierContext) context.Context {
	if !wikiTierAllowed(tc) {
		return ctx
	}
	w := wikiKB()
	if w == nil || !w.Enabled() || !sourceCodeCallerAllowed(auth.FromContext(ctx)) {
		return ctx
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if q == "" {
		return ctx
	}
	return context.WithValue(ctx, wikiProbeKey{}, &wikiProbeState{w: w, question: q})
}

// wikiProbeEligible — yoklamanın ön koşulları (sorgusuz): çapa yok, yeterli
// içerik terimi var, servis adı + telemetri sinyali yok.
func (s *Server) wikiProbeEligible(ctx context.Context, question string) bool {
	if wikiProbeTelemetryAnchor(question) || len(wikiProbeTerms(question)) < wikiProbeMinTerms {
		return false
	}
	return !s.wikiWeakCueVetoed(ctx, question)
}

// wikiProbeRun — TEK yerel lexical arama + kapı. Canlı ADO yok (LiveOff),
// embed yok (NoSemantic). Kapıyı geçerse seçilmiş isabetler, yoksa nil.
func wikiProbeRun(ctx context.Context, w *wiki.Service, question string) []wiki.Hit {
	terms := wikiProbeTerms(question)
	if len(terms) < wikiProbeMinTerms {
		return nil
	}
	pctx, cancel := context.WithTimeout(ctx, wikiProbeTimeout)
	defer cancel()
	res, err := w.SearchWith(pctx, strings.Join(terms, " "), "", wiki.SearchOptions{
		Limit: wikiTierSearchLimit, PerPage: wikiTierPerPage, Live: wiki.LiveOff, NoSemantic: true,
	})
	if err != nil || !wikiProbeStrong(res.Hits) {
		return nil
	}
	return wikiTierSelect(res.Hits)
}

// wikiProbe — alışveriş başına en çok bir yoklama (durum varsa sonucu saklar).
func (s *Server) wikiProbe(ctx context.Context, w *wiki.Service, question string) []wiki.Hit {
	st := wikiProbeFromCtx(ctx)
	if st == nil || st.question != question {
		if !s.wikiProbeEligible(ctx, question) {
			return nil
		}
		return wikiProbeRun(ctx, w, question)
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.done {
		st.done = true
		if s.wikiProbeEligible(ctx, question) {
			st.hits = wikiProbeRun(ctx, st.w, question)
		}
	}
	return st.hits
}

// wikiProbeAnswer — işaretsiz soruda içerik yoklaması. Kapı geçmezse HİÇBİR
// olay basılmaz (handled=false); geçerse kademenin anlatım yolu AYNEN
// (çok-sayfa okuma + wiki_select) ve guided rotasının "Telemetri için:" çipi.
func (s *Server) wikiProbeAnswer(ctx context.Context, emit func(string, any), w *wiki.Service, msgs []copilot.ChatMessage, question string) (handled, ok bool) {
	hits := s.wikiProbe(ctx, w, question)
	if len(hits) == 0 {
		return false, false
	}
	emit("step", map[string]string{"label": "kurum wiki'si"})
	ans, used, err := s.wikiNarratedAnswer(ctx, w, question, wikiPriorTurns(msgs), hits, emit)
	if err != nil {
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	if text, chips, links, ok := s.wikiProbeTelemetryChip(ctx, question); ok {
		withTelemetryChips(ans, text, chips, links)
	}
	if text, _ := ans["text"].(string); !wikiDeclined(text) {
		s.rememberWikiAnswer(ctx, text, wikiPageRefs(used))
	}
	emit("answer", ans)
	return true, true
}

// wikiProbeRouteChip — SAF: servis adsız filo rotasının tek tıklık telemetri
// çipi (çipin kendisi aynı rotaya gider ve yoklamayı tetiklemez — testli).
func wikiProbeRouteChip(r guidedRoute) (text, chip string, ok bool) {
	if r.Service != "" || r.Team != "" || len(r.Family) > 0 {
		return "", "", false
	}
	switch r.Intent {
	case guidedProblems:
		return "Açık problemler (filo geneli, canlı)", "Açık problemleri göster", true
	case guidedSlowTraces:
		return "En yavaş trace'ler (canlı)", "En yavaş trace'leri göster", true
	case guidedLogErrors:
		return "Hata logları (canlı)", "Son 1 saatteki log hataları?", true
	case guidedDeployImpact:
		return "Son deploy'lar (canlı)", "Son deploy'ları göster", true
	}
	return "", "", false
}

// wikiProbeTelemetryChip — guided router'ın bu soruya seçeceği rota (rota
// ÇALIŞTIRILMAZ: veri okuması / model çağrısı yok; yalnız önbellekli ad
// listeleri). Netleştirme rotası guidedDisambigProbe'un aynısı; filo
// rotaları wikiProbeRouteChip.
func (s *Server) wikiProbeTelemetryChip(ctx context.Context, question string) (text string, chips []string, links []guidedAnswerLink, ok bool) {
	if t, c, l, dok := s.guidedDisambigProbe(ctx, question); dok {
		return t, c, l, true
	}
	if !hasGuidedSignal(normalizeGuidedMsg(question)) {
		return "", nil, nil, false
	}
	svcs, envs, teams := wikiProbeNames(s, ctx, true)
	route := routeGuidedIntent(question, svcs, envs, teams, "")
	label, chip, rok := wikiProbeRouteChip(route)
	if !rok {
		return "", nil, nil, false
	}
	return label, []string{chip}, guidedAnswerLinks(route, noLinkWindow()), true
}

// markFleetZeroMatch — guidedProblems servis adsız soruda SIFIR problem
// döndürdüğünde onu üreten kod çağırır; yoklama kurulmamışsa etkisiz.
func markFleetZeroMatch(ctx context.Context) {
	if st := wikiProbeFromCtx(ctx); st != nil {
		st.zero.Store(true)
	}
}

// wikiZeroMatchHint — ters yön: işaretliyse ve yoklama kapıyı geçen bir sayfa
// verdiyse cevap metnine bağlantı satırı ve çip eklenir (anlatım yok).
// İşaret yoksa text/links AYNEN döner.
func (s *Server) wikiZeroMatchHint(ctx context.Context, question, text string, links []guidedAnswerLink) (string, []guidedAnswerLink) {
	st := wikiProbeFromCtx(ctx)
	if st == nil || !st.zero.Load() {
		return text, links
	}
	hits := s.wikiProbe(ctx, st.w, question)
	if len(hits) == 0 {
		return text, links
	}
	top := hits[0]
	text += "\n\n" + wikiProbeZeroHintPrefix + cleanWikiTitle(top.Title)
	return text, append(links, ragWikiLinks([]wiki.Hit{top})...)
}
