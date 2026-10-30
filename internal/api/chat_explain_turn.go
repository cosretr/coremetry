package api

// chat_explain_turn.go — v0.10.1400 (Tek CoSRE). Operatör:
//   "Trace ve exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama
//    sanki ayrı drawer hissi, CoSRE bütünselliğine uymuyor."
//   "Explain trace'te çıkan loading ikonu güzel ama chat'ten 'bu trace'i
//    açıkla' deyince aynı yerde devam etmiyor, sanki yeni drawer açılıyor."
//
// Kök sebep: trace / exception ✨ açıklaması CoSRE çekmecesinin İÇİNDE ama
// sohbetin DIŞINDA çiziliyordu — `?ai=` öznesi çekmeceyi "açıklama kipine"
// alıyor (AIDrawerBody: CopilotExplain + ikinci useChatThread + ikinci
// composer), sohbetteki "<id> açıkla" da v0.10.460'tan beri `open` ile o
// kipe sıçrıyordu. İki thread, iki composer, iki bekleme göstergesi.
//
// Şimdi bir açıklama, sohbetin SIRADAN bir turu: istemci kullanıcı turunu
// ("Bu trace'i açıkla · 5b8efff7…") yapısal niyetle gönderir —
// context.explainAsk {kind: trace|exception, id, src} — ve bu kademe cevabı
// ✨ düğmesinin BİREBİR çekirdeğiyle üretir:
//   • trace     → buildTraceExplainInput + SystemPromptTrace, anahtar
//                 explainCacheKey(SystemPromptTrace(), in.User, "")
//                 (explainTraceClassicPrepared ile aynı), yüzey
//                 explain-trace:chat;
//   • exception → GetExceptionGroup + exceptionExplainInput (dağıtıcının
//                 prompt'u; `ora:` → Oracle), anahtar explainCacheKey(system,
//                 in.User, "") (copilotExplainException ile aynı), yüzey
//                 explain-exception:chat.
// Düğme ve sohbet aynı önbellek satırını paylaşır; isabet LLM çağırmaz.
// answer çerçevesi sohbetin olağan alanları + `explain` künyesi (istemci
// ExplainBody ile çizer, kanıtı sayfaya duyurur, takip bağlamını kurar).
//
// Kademe sırası: niyet AÇIK olduğu için metin eşlemesinden önce — kapsam,
// hafıza, wiki, operasyon ön-getirmesi ve guided'dan ÖNCE
// (TestChatExplainTurnTierIsFirst). explainAsk yokken akış bayt bayt eski.

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// chatExplainAsk — context.explainAsk: istemcinin yapısal açıklama niyeti.
// v0.10.1405 — tür kümesi kayıttan (chat_explain_registry.go: span, problem,
// incident, anomaly, service-health, runbook, log); `subject` = `?ai=` kodeği
// (aiSubject.ts formatAiParam) — kimliği tek alana sığmayan türlerde
// (span / service-health / log) ZORUNLU, basit türlerde isteğe bağlı (verilirse
// tür + kimlik eşleşmeli).
type chatExplainAsk struct {
	Kind    string `json:"kind"`              // trace | exception | span | problem | incident | anomaly | service-health | runbook | log | charts | log-spike
	ID      string `json:"id"`                // trace id (32-hex) | grup parmak izi | problem/olay/anomali kimliği | servis ("" = log-spike filo geneli)
	Subject string `json:"subject,omitempty"` // v0.10.1405 — kanonik `?ai=` kodeği
	Src     string `json:"src,omitempty"`     // button | nudge | deeplink | chip | starter (ölçüm; karar değil)
	// Code — v0.10.1406 (Tek CoSRE adım 3): "Kodu da incele" — ✨ düğmesinin kod
	// dalı (trace + span exception; Oracle grubunda ve diğer türlerde düşer).
	// Anahtar düğmenin kod anahtarı (codeExplainPrep) → tek önbellek satırı.
	Code bool `json:"code,omitempty"`
	// Fresh — v0.10.1406: "↻ Yeniden üret" — önbellek okunmaz, cevap yine
	// yazılır. YALNIZ istekte yaşar: arşive girmez (künyede alanı yok,
	// sanitizeChatExplain kurmaz), istemci kullanıcı turunda saklamaz.
	Fresh bool `json:"fresh,omitempty"`
	// Panel — v0.10.1435 (explain_metric_panel.go): metric_panel niyetinin
	// yapısal yükü (≤8 KiB). Kodek konum taşır, sorgu buradadır; dashboard
	// panelinde sunucu yalnız `vars`'a güvenir. Arşive girmez (künyede yok).
	Panel *metricPanelAsk `json:"panel,omitempty"`
}

const (
	explainAskTrace     = "trace"
	explainAskException = "exception"
	// chatExplainIDMax — kimlik tavanı (rune); arşiv sanitize'ı da aynı.
	chatExplainIDMax = 200
	// chatExplainEvidenceMax — künyedeki kanıt kimliği tavanı (her liste).
	chatExplainEvidenceMax = 50
	chatExplainSrcMax      = 32
	// chatExplainSubjectMax — v0.10.1405: `subject` kodeğinin tavanı (rune;
	// arşiv sanitize'ı da aynı).
	chatExplainSubjectMax = 400
)

// chatExplainMeta — answer.explain (ve arşivde aiChatMessage.explain): turun
// açıklama künyesi. İstemci ExplainBody'yi, kanıt satırlarını ve takip
// bağlamını bundan kurar.
type chatExplainMeta struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	// Subject — v0.10.1405: kanonik `?ai=` kodeği (span / service-health / log);
	// istemci takip bağlamını ve kanıtı bundan kurar. Basit türlerde boş.
	Subject string `json:"subject,omitempty"`
	Surface string `json:"surface,omitempty"`
	// Renderer — v0.10.1405: çizim kipi ("body" = ExplainBody; boş = body).
	// v0.10.1407 — "charts" / "log-spike": istemci cevabın kanıt bloğundan
	// (charts_signals / log_spike) kart çizer; blok yoksa ExplainBody'ye düşer.
	// v0.10.1444 — bloklar arşivde (cosre_interactions.blocks, chat_turn_blocks.go):
	// Geçmiş'ten açılan tur da kartı çizer; zarfsız eski tur ExplainBody'de kalır.
	Renderer string `json:"renderer,omitempty"`
	Label    string `json:"label,omitempty"` // exception: grup kodu / tipi; runbook: "RUNBOOK"; diğerleri: öznenin adı
	// Note — v0.10.1405: künye satırı (runbook: "Based on N past resolved…").
	Note string `json:"note,omitempty"`
	// Code — v0.10.1406: "Kodu da incele" turunun kod künyesi (✨ düğmesinin
	// `code` alanıyla aynı şekil: depo/dal/dosyalar ya da okunamama nedeni);
	// istemci CodeStatusNote + CodeSourceFooter çizer. İstemciden ASLA kabul
	// edilmez (sanitizeChatExplain kurmaz); v0.10.1444 — arşivden açılan turda
	// sunucu kendi blok zarfından doldurur (decorateTurnBlocks).
	Code             *codeContextPayload `json:"code,omitempty"`
	Cached           bool                `json:"cached,omitempty"`
	CachedAtMs       int64               `json:"cachedAtMs,omitempty"`
	EvidenceSpanIDs  []string            `json:"evidenceSpanIds,omitempty"`
	EvidenceTraceIDs []string            `json:"evidenceTraceIds,omitempty"`
	OracleRows       int                 `json:"oracleRows,omitempty"`
}

// normalizeExplainAsk — SAF: tür beyaz listesi + kimlik biçimi. trace 32-hex
// (küçük harfe indirilir); exception parmak izi 1..200 rune, yalnız
// [A-Za-z0-9:._-] (ora: önekli Oracle grupları dahil). ok=false → kademe
// düşer ve kullanıcı metni olağan yoldan cevaplanır.
//
// v0.10.1407 — charts / log-spike da kodek ister (log-spike'ta boş kimlik =
// filo geneli, meşru).
//
// v0.10.1405 — kayıttaki türler: problem / incident / anomaly / runbook
// kimliği parmak izi biçiminde (subject verilirse tür + kimlik eşleşmeli,
// normalize edilmiş niyette subject boş); span / service-health / log
// subject'i ZORUNLU (≤400 rune, parseAiSubject, tür eşleşmeli; boş id
// kodekten alınır, dolu id kodeğinkiyle aynı olmalı) ve kanonik biçime
// (formatAiSubject) yeniden yazılır.
func normalizeExplainAsk(a *chatExplainAsk) (chatExplainAsk, bool) {
	if a == nil {
		return chatExplainAsk{}, false
	}
	kind := strings.ToLower(strings.TrimSpace(a.Kind))
	id := strings.TrimSpace(a.ID)
	src := clampChatRunes(a.Src, chatExplainSrcMax)
	rawSubj := strings.TrimSpace(a.Subject)
	if utf8.RuneCountInString(rawSubj) > chatExplainSubjectMax {
		return chatExplainAsk{}, false
	}
	switch kind {
	case explainAskTrace:
		id = strings.ToLower(id)
		if !fuTraceRe.MatchString(id) {
			return chatExplainAsk{}, false
		}
	case explainAskException:
		if !explainFingerprintOK(id) {
			return chatExplainAsk{}, false
		}
	case explainAskProblem, explainAskIncident, explainAskAnomaly, explainAskRunbook:
		if !explainFingerprintOK(id) {
			return chatExplainAsk{}, false
		}
		if rawSubj != "" {
			if subj, ok := parseAiSubject(rawSubj); !ok || subj.Kind != kind || subj.ID != id {
				return chatExplainAsk{}, false
			}
		}
	case explainAskSpan, explainAskServiceHealth, explainAskLog, explainAskCharts, explainAskLogSpike:
		subj, ok := parseAiSubject(rawSubj)
		if !ok || subj.Kind != kind || (id != "" && id != subj.ID) {
			return chatExplainAsk{}, false
		}
		switch {
		case kind == explainAskSpan:
			if !explainFingerprintOK(subj.ID) || !explainFingerprintOK(subj.SpanID) {
				return chatExplainAsk{}, false
			}
		case kind == explainAskLogSpike && subj.ID == "":
			// v0.10.1407 — filo geneli log artışı (`log-spike:*:…`): kimlik boş meşru.
		case !explainNameOK(subj.ID):
			return chatExplainAsk{}, false
		}
		return chatExplainAsk{Kind: kind, ID: subj.ID, Subject: formatAiSubject(subj), Src: src, Fresh: a.Fresh}, true
	case explainAskMetricPanel:
		// v0.10.1435 — kodek ZORUNLU (konum + pencere + brush + seri); yük
		// isteğe bağlı (arşiv / derin link yüksüz meşru) ama verildiyse ≤8 KiB
		// ve biçimli olmalı, aksi niyet reddedilir.
		subj, ok := parseAiSubject(rawSubj)
		if !ok || subj.Kind != kind || (id != "" && id != subj.ID) {
			return chatExplainAsk{}, false
		}
		panel, ok := normalizeMetricPanelAsk(a.Panel)
		if !ok {
			return chatExplainAsk{}, false
		}
		return chatExplainAsk{Kind: kind, ID: subj.ID, Subject: formatAiSubject(subj), Src: src, Fresh: a.Fresh, Panel: panel}, true
	default:
		return chatExplainAsk{}, false
	}
	// v0.10.1406 — code yalnız kod-yetenekli öznede kalır (aksi sessizce düşer:
	// kodsuz açıklama dürüst bir düşüş); fresh her türde.
	return chatExplainAsk{Kind: kind, ID: id, Src: src, Code: a.Code && explainCodeCapable(kind, id), Fresh: a.Fresh}, true
}

// explainCodeCapable — SAF (v0.10.1406): "Kodu da incele"nin anlamlı olduğu
// özne — ✨ düğmesindeki codeCapable'ın aynası (CopilotExplain.tsx): trace ve
// span exception grubu. Oracle grubu (`ora:`) stack taşımaz.
func explainCodeCapable(kind, id string) bool {
	return kind == explainAskTrace || (kind == explainAskException && !chstore.IsOracleGroup(id))
}

// explainCodeChipLabel — sohbet içi kod çipinin metni (düğmedeki çip adı).
const explainCodeChipLabel = "Kodu da incele"

// explainCodeChip — SAF (v0.10.1406): açıklama turunun "Kodu da incele" takip
// çipi. Aynı niyet + code:true + src:"chip"; tur zaten kod turuysa, özne
// kod-yetenekli değilse (Oracle grubu, diğer türler) ya da kod entegrasyonu
// (DevOps) yapılandırılmamışsa çip YOK — sonucu baştan "kod entegrasyonu
// yapılandırılmamış" olan bir tık önermek ikinci bir LLM turu yakmaktır.
// (Derin link ?aicode=1 yine kod turu açar; dürüst durum CodeStatusNote'ta.)
func explainCodeChip(ask chatExplainAsk, codeConfigured bool) (chatFollowup, bool) {
	if ask.Code || !codeConfigured || !explainCodeCapable(ask.Kind, ask.ID) {
		return chatFollowup{}, false
	}
	a := chatExplainAsk{Kind: ask.Kind, ID: ask.ID, Subject: ask.Subject, Src: "chip", Code: true}
	return chatFollowup{Text: explainCodeChipLabel, Label: explainCodeChipLabel, Kind: "code", ExplainAsk: &a}, true
}

// explainCodeConfigured — kod entegrasyonu (DevOps) yapılandırılmış mı.
func (s *Server) explainCodeConfigured() bool {
	return s != nil && s.devops != nil && s.devops.Configured()
}

// withExplainCodeChip — answer çerçevesine kod çipini ekler (çip yoksa aynen).
func (s *Server) withExplainCodeChip(ans map[string]any, ask chatExplainAsk) map[string]any {
	if c, ok := explainCodeChip(ask, s.explainCodeConfigured()); ok {
		return withAnswerFollowups(ans, []chatFollowup{c})
	}
	return ans
}

// explainNameOK — SAF (v0.10.1405): servis adı biçimli kimlik — boş değil,
// ≤200 rune, kontrol karakteri yok.
func explainNameOK(id string) bool {
	if id == "" || utf8.RuneCountInString(id) > chatExplainIDMax {
		return false
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// explainAskSubject — SAF (v0.10.1405): normalize edilmiş niyetin öznesi.
func explainAskSubject(a chatExplainAsk) (aiSubject, bool) {
	if a.Subject != "" {
		return parseAiSubject(a.Subject)
	}
	return aiSubject{Kind: a.Kind, ID: a.ID}, a.Kind != "" && a.ID != ""
}

// explainFingerprintOK — SAF: grup parmak izinin kabul edilen biçimi.
func explainFingerprintOK(id string) bool {
	if id == "" || utf8.RuneCountInString(id) > chatExplainIDMax {
		return false
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ':', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// clampExplainIDs — SAF: kanıt kimliklerini künye tavanına indirir (boşlar düşer).
func clampExplainIDs(in []string) []string {
	out := make([]string, 0, min(len(in), chatExplainEvidenceMax))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || utf8.RuneCountInString(v) > chatExplainIDMax {
			continue
		}
		out = append(out, v)
		if len(out) >= chatExplainEvidenceMax {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// errExplainTurnNotFound — açıklanacak trace / grup bulunamadı (adım çipi).
var errExplainTurnNotFound = errors.New("bulunamadı")

// explainTurnNotFoundTR — SAF: bulunamayan öznenin dürüst, LLM'siz cevabı.
func explainTurnNotFoundTR(a chatExplainAsk) string {
	switch a.Kind {
	case explainAskTrace:
		return "Trace `" + a.ID + "` bulunamadı — ne Tempo'da ne ClickHouse'ta var (saklama süresi dolmuş ya da kimlik yanlış olabilir)."
	case explainAskException:
		return "Exception grubu `" + a.ID + "` bulunamadı — silinmiş ya da kimlik yanlış olabilir."
	case explainAskSpan: // v0.10.1405
		return "Span bulunamadı — trace `" + a.ID + "` ya da içindeki span artık yok (saklama süresi dolmuş ya da kimlik yanlış olabilir)."
	case explainAskLog: // v0.10.1405
		return "Log satırı yeniden okunamadı (saklama süresi dolmuş ya da kaynak değişmiş olabilir)."
	}
	if k := explainKindTR[a.Kind]; k != "" { // v0.10.1405
		return k + " `" + a.ID + "` bulunamadı — silinmiş ya da kimlik yanlış olabilir."
	}
	return "`" + a.ID + "` bulunamadı."
}

// chatExplainTurn — yapısal açıklama turu. handled=false → explainAsk geçersiz
// ya da copilot yok; çağıran olağan kademelere devam eder.
func (s *Server) chatExplainTurn(ctx context.Context, emit func(string, any), raw *chatExplainAsk, loc *time.Location) (handled, ok bool) {
	ask, valid := normalizeExplainAsk(raw)
	if !valid || s == nil || s.copilot == nil {
		return false, false
	}
	// v0.10.1405 — tür kaydı (chat_explain_registry.go): trace / exception
	// kendi turlarıyla (aşağıda, v0.10.1400 aynen), diğerleri düğmenin
	// kurucusuyla genel turdan.
	k, known := explainKindFor(ask.Kind)
	if !known {
		return false, false
	}
	if k.Turn != nil {
		return k.Turn(s, ctx, emit, ask, loc)
	}
	return s.chatExplainRegistryTurn(ctx, emit, k, ask, loc)
}

// chatExplainTraceTurn — ✨ Explain trace'in kodsuz klasik yolu, sohbet içinde.
func (s *Server) chatExplainTraceTurn(ctx context.Context, emit func(string, any), ask chatExplainAsk) (bool, bool) {
	n := emitGuidedStep(emit, "trace", `{"id":"`+ask.ID+`"}`)
	in, err := s.buildTraceExplainInput(ctx, ask.ID)
	if err != nil {
		if errors.Is(err, errExplainTraceNotFound) {
			emitGuidedStepResult(emit, n, "trace", "", errExplainTurnNotFound)
			emit("answer", map[string]any{"text": explainTurnNotFoundTR(ask), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
			return true, false
		}
		emitGuidedStepResult(emit, n, "trace", "", err)
		emit("error", map[string]string{"error": err.Error()})
		return true, false
	}
	emitGuidedStepResult(emit, n, "trace", in.User, nil)
	route := guidedRoute{Intent: guidedTraceByID, TraceID: ask.ID}
	return s.emitTraceExplainTurn(ctx, emit, ask, in, route, guidedAnswerLinks(route, noLinkWindow()))
}

// emitTraceExplainAnswer — v0.10.1400: trace açıklamasının sohbet cevabı.
// chatExplainTraceTurn ve guidedTraceExplain'in açıklama dalı paylaşır: aynı
// sistem/kullanıcı çifti, düğmenin anahtarı, aynı künye.
func (s *Server) emitTraceExplainAnswer(ctx context.Context, emit func(string, any), traceID string, in traceExplainInput, route guidedRoute, links []guidedAnswerLink) (bool, bool) {
	return s.emitTraceExplainTurn(ctx, emit, chatExplainAsk{Kind: explainAskTrace, ID: traceID}, in, route, links)
}

// emitTraceExplainTurn — v0.10.1406: emitTraceExplainAnswer'ın niyetli hâli.
// ask.Code → ✨ düğmesinin kod dalı (traceCodeExplainPrep, düğmeyle aynı
// anahtar; künyede `code`); ask.Fresh → önbellek okunmaz (Yeniden üret).
// Kodsuz turun cevabı "Kodu da incele" çipini taşır (explainCodeChip).
func (s *Server) emitTraceExplainTurn(ctx context.Context, emit func(string, any), ask chatExplainAsk, in traceExplainInput, route guidedRoute, links []guidedAnswerLink) (bool, bool) {
	traceID := ask.ID
	system := copilot.SystemPromptTrace()
	cacheKey := explainCacheKey(system, in.User, "") // explainTraceClassicPrepared ile AYNI formül
	var res explainCoreResult
	var ok bool
	var code *codeContextPayload
	if ask.Code {
		p := s.traceCodeExplainPrep(ctx, in)                                                                 // copilotExplainTrace'in kod dalıyla AYNI kurucu
		res, ok = s.streamTraceExplainCodeCore(ctx, emit, aisurface.ExplainTraceChat, p, in.User, ask.Fresh) // v0.10.1630 — dış bağımlılık satırı
		code = codePayload(p.CC, true)
	} else {
		res, ok = s.streamTraceExplainCore(ctx, emit, aisurface.ExplainTraceChat, system, in.User, cacheKey, ask.Fresh) // v0.10.1630
	}
	if !ok {
		return true, false
	}
	text := trimExplainText(res)
	links = append(links, guidedAnswerLink{Label: "Trace'i aç", Href: "/trace?id=" + url.QueryEscape(traceID)})
	links = append(links, s.answerRequestIDLinks(ctx, text, in.RootService)...)
	meta := chatExplainMeta{Kind: explainAskTrace, ID: traceID, Surface: aisurface.ExplainTraceChat,
		Cached: res.Cached, CachedAtMs: res.CachedAtMs,
		EvidenceSpanIDs: clampExplainIDs(in.Evidence), OracleRows: in.OracleRows, Code: code}
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"suggestions":     guidedSuggestions(route),
		"links":           dedupLinksByHref(links),
		"evidenceSpanIds": in.Evidence, // v0.10.453 sözleşmesi (eski istemci)
		"explain":         meta,
	}
	emit("answer", s.withExplainCodeChip(withExplainCached(ans, res), ask))
	return true, true
}

// chatExplainExceptionTurn — ✨ Explain root cause'un (kodsuz) yolu, sohbet içinde.
func (s *Server) chatExplainExceptionTurn(ctx context.Context, emit func(string, any), ask chatExplainAsk, loc *time.Location) (bool, bool) {
	args, _ := json.Marshal(map[string]any{"fingerprint": ask.ID})
	n := emitGuidedStep(emit, opExceptionExplainTool, string(args))
	ictx, cancel := context.WithTimeout(ctx, opExceptionExplainInputTimeout)
	g, in, system, gok := s.chatExceptionExplainInput(ictx, ask.ID, loc)
	cancel()
	if !gok {
		emitGuidedStepResult(emit, n, opExceptionExplainTool, "", errExplainTurnNotFound)
		emit("answer", map[string]any{"text": explainTurnNotFoundTR(ask), "exchangeId": copilot.MetaFromContext(ctx).ExchangeID})
		return true, false
	}
	emitGuidedStepResult(emit, n, opExceptionExplainTool, in.User, nil)
	// copilotExplainException ile AYNI anahtar (kod dalı kapalı).
	// v0.10.1406 — ask.Code (yalnız span grubu; normalizeExplainAsk Oracle'da
	// düşürür) → düğmenin kod dalı, AYNI kurucu (exceptionCodeExplainPrep).
	var res explainCoreResult
	var ok bool
	var code *codeContextPayload
	if ask.Code && in.Oracle == nil {
		p := s.exceptionCodeExplainPrep(ctx, g, in)
		res, ok = s.streamExplainCodeCore(ctx, emit, aisurface.ExplainExceptionChat, p, in.User, ask.Fresh)
		code = codePayload(p.CC, true)
	} else {
		res, ok = s.streamExplainCore(ctx, emit, aisurface.ExplainExceptionChat, system, in.User, explainCacheKey(system, in.User, ""), ask.Fresh)
	}
	if !ok {
		return true, false
	}
	text := trimExplainText(res)
	label := exceptionGroupLabel(g)
	links := []guidedAnswerLink{{Label: "Exceptions grubu " + clipRunes(label, exGroupOpRunes), Href: "/problems?exc=" + url.QueryEscape(g.Fingerprint)}}
	links = append(links, s.answerRequestIDLinks(ctx, text, g.Service)...)
	meta := chatExplainMeta{Kind: explainAskException, ID: g.Fingerprint, Surface: aisurface.ExplainExceptionChat,
		Label: clipRunes(label, exGroupOpRunes), Cached: res.Cached, CachedAtMs: res.CachedAtMs,
		EvidenceSpanIDs: clampExplainIDs(in.EvSpans), EvidenceTraceIDs: clampExplainIDs(in.EvTraces), Code: code}
	ans := map[string]any{
		"text": text, "exchangeId": res.Xid,
		"links":           dedupLinksByHref(links),
		"evidenceSpanIds": in.EvSpans,
		"explain":         meta,
	}
	emit("answer", s.withExplainCodeChip(withExplainCached(ans, res), ask))
	return true, true
}

// exceptionGroupLabel — SAF: grubun kısa adı (tip — Oracle grubunda hata
// kodu; yoksa parmak izi).
func exceptionGroupLabel(g *chstore.ExceptionGroup) string {
	if g == nil {
		return ""
	}
	if t := strings.TrimSpace(g.Type); t != "" {
		return t
	}
	return g.Fingerprint
}
