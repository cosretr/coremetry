package api

// chat_offtopic_general.go — v0.10.1458 (operatör kararı 2026-10-23: "Genel
// soruları açık bırakalım, anahtar da olabilir."). v0.10.819'un kararını
// (copilot_offtopic.go: konu dışı soru MODEL cevabı almaz) anahtarla tersine
// çevirir: system_settings `ai.generalQuestions.allow` (varsayılan AÇIK) iken
// sınıflandırıcının off_topic dediği ve sunucu vetosunun geçirdiği soru KISA bir
// genel bilgi cevabı alır; altında v0.10.819'un yol tarifi çipleri/bağlantıları
// aynen durur (kullanıcı CoSRE'nin ne yaptığını yine öğrenir).
//
// Sınırlar (neden böyle):
//   - Tek araçsız çağrı (copilotStreamSurface → StreamText): tool tanımı yok,
//     telemetri döngüsü yok, RAG/wiki içeriği yok — prompt yalnız soru, o yüzden
//     localOnly kapısı (kod/wiki → yerel model) burada söz konusu değil.
//   - Derin kip YOK sayılır (copilot.WithoutDeepAnswer): genel cevap kısa kalır,
//     bütçe profilin tabanı. Sabit max_tokens tavanı KONMAZ: düşünen modeller
//     (Qwen3, gpt-5 …) küçük tavanda boş cevap döndürür (modelcaps dersi);
//     kısalık istemden (SystemPromptOffTopicGeneral), bütçe profilden.
//   - Zararlı istek (offTopicIsHarmful) anahtar açıkken de MODELE GİTMEZ:
//     v0.10.819'un kesin sınırı bayt bayt.
//   - Model hata verir / boş dönerse v0.10.819 sınırı (exchangeId'siz) — cevap
//     hiçbir zaman kesilmez; akış ortasında kopan çağrının kısmi metni korunur
//     (chat-general emsali, copilot_intent.go).
//   - Satır aisurface.ChatOffTopicGeneral (RoleAnswer): kök exchange'i aynen
//     taşır → oylanabilir (chat-general gibi); KB'ye terfi edemez (NoKB).
//     Anahtar açıkken chat-offtopic işaret satırı YAZILMAZ — model satırı onun
//     yerini alır (aynı tür /ai'da kendi yüzeyiyle görünür).
//
// v0.10.1485 (operatör, prod ekranı 2026-10-23, Qwen profili: palindrom
// fonksiyonu sorusu — "Coremetry izlediği telemetri ile ilgili değil'i 2 defa
// söylüyor", "doğru cevabı versin üstte, uyarı en altta not diye düşsün"):
//   - Cevap ÜSTTE, tek not EN ALTTA (offTopicGeneralNoteTR, kısa, italik).
//     Not artık akışın başına basılmaz; `answer` olayı otoriter metindir.
//   - Model kendi kapsam notunu yazmasın (istem) + yazarsa deterministik
//     süzgeç (stripScopeDisclaimers) onu nerede olursa olsun düşürür — model
//     bağımsız (Gemma/Qwen), kod bloklarının içine dokunmaz.
//   - Biçim istemde: Markdown, dil etiketli çok satırlı kod bloğu, uzunluk
//     soruyla orantılı (kod/how-to sorusuna "2-3 cümle" tavanı yok).
//   - Kalabalık azaldı: "Şöyle sorabilirsin" listesi ve bağlantılar bu yolda
//     YOK; CoSRE'yi keşfettiren en çok 3 çip kalır. Anahtar KAPALI sınırı
//     (v0.10.819) bayt bayt aynı.

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// offTopicGeneralNoteTR — genel cevabın ALTINA sunucunun koyduğu TEK not:
// cevabın telemetriden gelmediği modele bırakılmaz (intentGeneralNoteTR
// emsali). v0.10.1485 — kısaldı ve üstten alta indi (operatör: "uyarı olacaksa
// en altta not diye düşsün, doğru cevabı versin üstte").
const offTopicGeneralNoteTR = "_Not: genel bilgi — Coremetry verilerine dayanmaz._"

// offTopicGeneralMaxChips — v0.10.1485: genel cevabın altında en çok bu kadar
// çip (servis sayfasındaysa servis soruları önce); liste + bağlantı yok.
const offTopicGeneralMaxChips = 3

// offTopicGeneralChips — SAF: v0.10.819 çiplerinin ilk offTopicGeneralMaxChips'i.
func offTopicGeneralChips(ctxService string) []string {
	c := offTopicChips(ctxService)
	if len(c) > offTopicGeneralMaxChips {
		c = c[:offTopicGeneralMaxChips]
	}
	return c
}

// offTopicUsesModel — SAF: konu dışı soru model cevabı alır mı? Anahtar açık
// VE istek zararlı değil.
func offTopicUsesModel(allow bool, question string) bool {
	return allow && !offTopicIsHarmful(normalizeGuidedMsg(question))
}

// offTopicGeneralEnabled — canlı karar: anahtar + zararlılık + model hazır.
func (s *Server) offTopicGeneralEnabled(question string) bool {
	if s == nil || s.copilot == nil || !s.copilot.Active() {
		return false
	}
	return offTopicUsesModel(chstore.CurrentAIGeneralQuestions().Allow, question)
}

// guidedOffTopicDispatch — runGuidedRoute'un off_topic dalı: anahtar açıksa
// kısa genel cevap, kapalıysa (ya da zararlı istek) v0.10.819 sınırı.
func (s *Server) guidedOffTopicDispatch(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, question, ctxService string) (bool, bool) {
	if s.offTopicGeneralEnabled(question) {
		return s.offTopicGeneralAnswer(ctx, emit, msgs, question, ctxService)
	}
	return s.guidedOffTopicAnswer(emit, question, ctxService)
}

// Önceki konuşmanın genel cevaba taşınan payı: en çok offTopicGeneralHistoryMsgs
// ileti, toplam offTopicGeneralHistoryRunes; tek kullanıcı iletisi (yapıştırılan
// kod) offTopicGeneralUserRunes, tek asistan iletisi offTopicGeneralAsstRunes.
const (
	offTopicGeneralQuestionRunes = 1500
	offTopicGeneralHistoryMsgs   = 6
	offTopicGeneralHistoryRunes  = 6000
	offTopicGeneralUserRunes     = 4000
	offTopicGeneralAsstRunes     = 1200
)

// offTopicGeneralPrompt — SAF: genel cevabın kullanıcı istemi. v0.10.2012
// (operatör, prod): kullanıcı bir turda kod yapıştırıp sonrakinde "buna junit
// testi yazar mısın" dedi; modele yalnız son soru gittiği için "kodu
// paylaşmadınız" cevabı çıktı. Önceki iletiler (en yeniden geriye, bütçe
// dolana dek) sorunun önüne konur; önceki ileti yoksa istem eskisi gibi
// yalnız sorudur.
func offTopicGeneralPrompt(msgs []copilot.ChatMessage, question string) string {
	q := clipRunes(question, offTopicGeneralQuestionRunes)
	end := len(msgs)
	if end > 0 && msgs[end-1].Role == "user" && strings.TrimSpace(msgs[end-1].Text) == strings.TrimSpace(question) {
		end-- // son ileti şimdiki sorunun kendisi
	}
	var prior []string
	budget := offTopicGeneralHistoryRunes
	for i := end - 1; i >= 0 && len(prior) < offTopicGeneralHistoryMsgs && budget > 0; i-- {
		t := strings.TrimSpace(msgs[i].Text)
		if t == "" {
			continue
		}
		who, limit := "Kullanıcı", offTopicGeneralUserRunes
		if msgs[i].Role != "user" {
			who, limit = "CoSRE", offTopicGeneralAsstRunes
		}
		if limit > budget {
			limit = budget
		}
		t = clipRunes(t, limit)
		budget -= utf8.RuneCountInString(t)
		prior = append(prior, "["+who+"]\n"+t)
	}
	if len(prior) == 0 {
		return q
	}
	for i, j := 0, len(prior)-1; i < j; i, j = i+1, j-1 {
		prior[i], prior[j] = prior[j], prior[i]
	}
	return "ÖNCEKİ KONUŞMA (kullanıcı \"bu\", \"buna\", \"şunu\" derse buradaki içeriği kasteder):\n\n" +
		strings.Join(prior, "\n\n") + "\n\nŞİMDİKİ SORU:\n" + q
}

// offTopicGeneralText — SAF: model çıktısı (+ hata) → ekrana giden metin.
// general=false ⇒ v0.10.819 sınırı (deterministik, oylanmaz).
// Akış ortasında kopan çağrının kısmi metni korunur (hata adım çipinde görünür).
func offTopicGeneralText(raw, question, ctxService string) (text string, general bool) {
	raw = strings.TrimSpace(stripScopeDisclaimers(raw))
	if raw == "" {
		return offTopicAnswerText(question, ctxService), false
	}
	return raw + "\n\n" + offTopicGeneralNoteTR, true
}

// ── v0.10.1485 — modelin kendi kapsam notunu düşüren süzgeç ─────────────

// scopeSubjectWords — not cümlesinin KONUSU (Coremetry / telemetri / "senin
// sistemin verisi"); scopeHedgeWords — notun YÜKLEMİ (değil / dayanmaz /
// genel bilgi …). İkisi aynı cümlede → kapsam notu. Normalize edilmiş
// (küçük harf) alt-dize; Türkçe + İngilizce.
var scopeSubjectWords = []string{"coremetry", "telemetri", "telemetry", "sisteminin verisi", "sisteminizin verisi", "your system's data"}
var scopeHedgeWords = []string{
	"değil", "degil", "dayanmaz", "dayanmıyor", "dayanmamaktadır", "gelmez", "gelmiyor", "gelmemektedir",
	"ilgisiz", "ilgili değil", "dışında", "genel bilgi", "genel python bilgi",
	"not based", "not related", "unrelated", "doesn't", "does not", "isn't", "is not", "general knowledge", "not from",
}

// scopeNoteMaxRunes — bundan uzun cümle not sayılmaz (asıl içerik taşıyordur).
const scopeNoteMaxRunes = 220

// isScopeDisclaimer — SAF: tek cümle/yan cümle modelin "bu Coremetry
// telemetrisinden değil" notu mu? Markdown vurgu işaretleri ve "Not:" öneki
// karar için soyulur.
func isScopeDisclaimer(seg string) bool {
	t := strings.Trim(strings.TrimSpace(seg), "_*>()[]:-–— ")
	if t == "" || utf8.RuneCountInString(t) > scopeNoteMaxRunes || strings.Contains(t, "`") {
		return false
	}
	n := normalizeGuidedMsg(t)
	n = strings.ReplaceAll(strings.ReplaceAll(n, "*", ""), "_", " ")
	subj, hedge := false, false
	for _, w := range scopeSubjectWords {
		if strings.Contains(n, w) {
			subj = true
			break
		}
	}
	for _, w := range scopeHedgeWords {
		if strings.Contains(n, w) {
			hedge = true
			break
		}
	}
	return subj && hedge
}

// splitScopeSegments — SAF: bir satırı cümle/yan cümle parçalarına böler;
// her parça kendi ayırıcısını (". ", "; ", " — " …) sonunda taşır, böylece
// tutulan parçalar birleşince satır harfiyen geri gelir. Satır içi `kod`
// içinde bölmez.
func splitScopeSegments(line string) []string {
	var out []string
	rs := []rune(line)
	start, inCode := 0, false
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		if r == '`' {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		end := -1
		switch {
		case (r == '.' || r == '!' || r == '?' || r == ';' || r == '…') && i+1 < len(rs) && rs[i+1] == ' ':
			end = i + 2
		case (r == '—' || r == '–') && i > 0 && rs[i-1] == ' ' && i+1 < len(rs) && rs[i+1] == ' ':
			end = i + 2
		}
		if end > 0 {
			out = append(out, string(rs[start:end]))
			start = end
			i = end - 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// stripScopeDisclaimers — SAF, v0.10.1485: modelin kendi kapsam notunu
// (Türkçe/İngilizce; başta, ortada ya da sonda) düşürür — tek not sunucunun.
// Çitli kod bloklarına dokunmaz. Baştaki parça düştüyse kalan satırın ilk
// harfi büyütülür (Türkçe kuralıyla: "aşağıdaki" → "Aşağıdaki").
func stripScopeDisclaimers(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inFence := false
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
			out = append(out, ln)
			continue
		}
		if inFence || strings.TrimSpace(ln) == "" {
			out = append(out, ln)
			continue
		}
		segs := splitScopeSegments(ln)
		var kept strings.Builder
		dropped, droppedLead := false, false
		for i, sg := range segs {
			if isScopeDisclaimer(sg) {
				dropped = true
				if kept.Len() == 0 && i == 0 {
					droppedLead = true
				}
				continue
			}
			if droppedLead && kept.Len() == 0 {
				sg = strings.TrimLeft(sg, " ")
			}
			kept.WriteString(sg)
		}
		if !dropped {
			out = append(out, ln)
			continue
		}
		k := strings.TrimRight(kept.String(), " ")
		if strings.TrimSpace(k) == "" {
			continue
		}
		if droppedLead {
			k = upperFirstTR(k)
		}
		out = append(out, k)
	}
	return collapseBlankLines(strings.Join(out, "\n"))
}

// upperFirstTR — SAF: ilk harfi Türkçe kuralıyla büyütür (i → İ).
func upperFirstTR(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError || !unicode.IsLower(r) {
		return s
	}
	return strings.ToUpperSpecial(unicode.TurkishCase, string(r)) + s[size:]
}

// collapseBlankLines — SAF: düşen satırların bıraktığı ardışık boş satırları
// teke indirir; baş/son boşluk kırpılır (çit içi boş satırlar korunur).
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inFence, blank := false, false
	for _, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "```") {
			inFence = !inFence
		}
		if !inFence && strings.TrimSpace(ln) == "" {
			if blank {
				continue
			}
			blank = true
		} else {
			blank = false
		}
		out = append(out, ln)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// offTopicGeneralAnswer — tek araçsız çağrı + çipler (düşüşte v0.10.819 sınırı
// çip ve bağlantılarıyla).
func (s *Server) offTopicGeneralAnswer(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, question, ctxService string) (bool, bool) {
	q := offTopicGeneralPrompt(msgs, question)
	const tool = "off_topic_general"
	args, _ := json.Marshal(map[string]string{"q": clipRunes(question, 120), "service": ctxService})
	n := emitGuidedStep(emit, tool, string(args))
	t0 := time.Now()
	raw, err := s.copilotStreamSurface(copilot.WithoutDeepAnswer(ctx), aisurface.ChatOffTopicGeneral,
		copilot.SystemPromptOffTopicGeneral(), q, func(d string) { emit("delta", map[string]string{"text": d}) })
	text, general := offTopicGeneralText(raw, question, ctxService)
	if err != nil {
		emitStepEvidence(emit, n, tool, "", err)
	} else if n > 0 {
		preview, trunc := clipStepPreview(strings.TrimSpace(raw))
		emit("step-result", map[string]any{
			"i": n, "tool": tool, "ok": true, "preview": preview, "truncated": trunc,
			"bytes": len(raw), "durationMs": time.Since(t0).Milliseconds(),
		})
	}
	ans := map[string]any{"text": text}
	if general {
		// v0.10.1485 — model cevabının altında liste/bağlantı yok, ≤3 çip.
		ans["suggestions"] = offTopicGeneralChips(ctxService)
	} else {
		ans["suggestions"] = offTopicChips(ctxService)
		ans["links"] = offTopicLinks(ctxService)
	}
	if general {
		// Model metni oylanabilir: exchangeId = sohbetin kendi alışverişi (satır
		// RoleAnswer, kök kimliği aynen taşır). Deterministik düşüşte YOK.
		ans["exchangeId"] = copilot.MetaFromContext(ctx).ExchangeID
	}
	emit("answer", ans)
	return true, true
}
