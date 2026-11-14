package api

// chat_operation_why.go — v0.10.2137 (CoSRE router boşlukları D4,
// docs/cosre/router-gaps-audit.md E sınıfı §4). YALNIZ v2 (cosreV2Marked);
// v1 yolu bu dosyanın hiçbir yardımcısını çağırmaz → bayt bayt eski.
//
// Operasyon / endpoint ön-getirmesi (chat_operation_prefetch.go) RED + önceki
// eşit pencere, hatalı-önce trace'ler, Oracle, Exceptions ve en yavaş trace
// kırılımını DETERMİNİSTİK topluyor, sonra serbest döngüyü tohumluyordu. Neden
// / yavaşlama / "en uzun süren kısımlar" sorusunda döngü kanıtı yeniden
// toplamaya çalışıyor ve prod'da boşluğa düşüyordu. v2'de bu soru şekli artık
// guided ile aynı yoldan TEK araçsız anlatım çağrısıyla cevaplanır
// (runGuidedRoute, yüzey chat-guided): ön-getirme paketi + kırılım + küme
// kıyası + servisin deploy geçmişi. Yeni anlatım biçimi yok — klasik kök
// neden anlatıcısı (guidedNarrationPrompt).
//
// Ayrıca (karar 3) v2'de ön-getirme penceresi SORUDAN okunur ("son 6 saat",
// "son 1 hafta"); RED 7 güne kadar MV'den, trace örnekleri / kırılım en çok
// son 24 sa (opTarget.traceMaxS).
//
// Kapsam dışı: hata-neden sorusu (errorAsk — Exceptions açıklaması kendi
// kademesinde) ve v2 rota incelemesi (rtPlan: servis + route → grafikli
// inceleme, operatörün onayladığı deneyim) bu kademeye girmez.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// guidedOperationRootCauseRoute — /ai etkileşim satırındaki alt rota adı.
const guidedOperationRootCauseRoute = "operation_root_cause"

// opWhyNarrationAsk — SAF: ön-getirme sorusu neden / gecikme / gecikme
// büyümesi / "en uzun süren" şeklinde mi. Tek liste yok; mevcut üç sinyal
// (hasWhySignal, hasLatencyAsk, hasLatencyGrowthSignal) okunur.
func opWhyNarrationAsk(raw string) bool {
	norm := normalizeGuidedMsg(raw)
	return hasWhySignal(guidedTokens(norm)) || hasLatencyAsk(norm) || hasLatencyGrowthSignal(norm)
}

// opWhyTierWanted — SAF: v2 ön-getirme anlatım kademesi bu turu alır mı.
// Hata-neden sorusu (Exceptions açıklaması) ve v2 rota incelemesi hariç.
func opWhyTierWanted(v2 bool, p *opPrefetch, routePlanned bool, raw string) bool {
	return v2 && p != nil && !p.errorAsk && !routePlanned && opWhyNarrationAsk(raw)
}

// hasWeekLookback — SAF: "hafta/week" geriye bakışı ("son 1 hafta",
// "haftadır"); belirli bir hafta ("geçen/önceki hafta") DEĞİL.
func hasWeekLookback(toks []string) bool {
	if !tokenHasPrefix(toks, "hafta", "week") {
		return false
	}
	for i := 0; i+1 < len(toks); i++ {
		if (toks[i] == "geçen" || toks[i] == "önceki" || toks[i] == "last" || toks[i] == "previous") &&
			tokenHasPrefix(toks[i+1:i+2], "hafta", "week") {
			return false
		}
	}
	return true
}

// v2OpPrefetchRangeS — SAF: ön-getirme penceresi. v2'de soruda açık pencere
// varsa o (guidedRangeSExplicit, ≤24 sa), "hafta" geriye bakışı 7 gün; yoksa
// ekranın penceresi. v1 aynen ekran.
func v2OpPrefetchRangeS(question string, screenS int64, v2 bool) int64 {
	if !v2 {
		return screenS
	}
	norm := normalizeGuidedMsg(question)
	if v, explicit := guidedRangeSExplicit(norm); explicit && v > 0 {
		return v
	}
	if hasWeekLookback(guidedTokens(norm)) {
		return guidedWeekRangeS
	}
	return screenS
}

// opTraceFrom — SAF: trace örneklerinin (ve kırılımın) başlangıcı. traceMaxS
// 0 ya da pencere içinde → from aynen.
func opTraceFrom(from, to time.Time, traceMaxS int64) time.Time {
	if traceMaxS <= 0 {
		return from
	}
	if lo := to.Add(-time.Duration(traceMaxS) * time.Second); from.Before(lo) {
		return lo
	}
	return from
}

// opWhyEvidenceHeader — kanıtın başı: yön sorudan değil sayılardan.
const opWhyEvidenceHeader = "Soru bir operasyon/endpoint için neden ya da gecikme soruyor. Aşağıdaki kanıt sunucunun ön-getirmesidir. " +
	"Değişimin yönünü (arttı/azaldı/aynı) YALNIZ sayılardan oku; sorudaki iddiayı doğrulamadan tekrarlama. " +
	"Kanıtta olmayan bir nedeni UYDURMA; okunamayan kaynağı okunamadı diye söyle.\n\n"

// opWhyEvidence — SAF: anlatım kanıtı (özet + kırılım + küme kıyası + deploy).
func opWhyEvidence(p *opPrefetch, deploy string) string {
	var b strings.Builder
	b.WriteString(opWhyEvidenceHeader)
	b.WriteString(p.summary())
	if p.Drill != "" {
		b.WriteString("\n" + p.Drill)
	}
	if p.clusterText != "" {
		b.WriteString("\n" + p.clusterText)
	}
	if deploy != "" {
		b.WriteString("\n" + deploy)
	}
	return b.String()
}

// opWhySources — SAF: künye.
func opWhySources(p *opPrefetch, deploySrc string) string {
	src := "operasyon ön-getirmesi (RED: operation_summary_5m / spanmetrics_1m, önceki eşit pencere; trace araması"
	if p.Drill != "" {
		src += "; en yavaş trace kırılımı"
	}
	src += ")"
	if deploySrc != "" {
		src += "; " + deploySrc
	}
	return src
}

// guidedOpWhyBundle — runGuidedRoute'un operation_root_cause demeti: paket
// zaten getirildi (adımlar yayında); yalnız servis tekse deploy geçmişi okunur
// (≤24 sa; GetServiceDeploys).
func (s *Server) guidedOpWhyBundle(ctx context.Context, emit func(string, any), r guidedRoute, rangeS int64, anchorTo time.Time) (string, string, error) {
	p := r.opWhy
	deploy, deploySrc := "", ""
	if r.Service != "" {
		dr := rangeS
		if dr > guidedDrillMaxRangeS {
			dr = guidedDrillMaxRangeS
		}
		ev, src, err := s.guidedDeployBundle(ctx, emit, r.Service, r.Env, dr, anchorTo)
		if err != nil {
			deploy = fmt.Sprintf("Deploy geçmişi OKUNAMADI (%v) — deploy kaynaklı neden bilinmiyor.\n", err)
		} else {
			deploy, deploySrc = ev, src
		}
	}
	return opWhyEvidence(p, deploy), opWhySources(p, deploySrc), nil
}

// chatOperationWhyTier — v2: ön-getirme + neden/gecikme sorusu → tek anlatım.
// handled=false → akış aşağıda aynen (döngü tohumu).
func (s *Server) chatOperationWhyTier(ctx context.Context, emit func(string, any), p *opPrefetch, msgs []copilot.ChatMessage, explain, ctxService string, loc *time.Location) (handled, ok bool) {
	svc := ""
	if len(p.Services) == 1 {
		svc = p.Services[0]
	}
	route := guidedRoute{Intent: guidedRootCause, Service: svc, opWhy: p}
	return s.runGuidedRoute(ctx, emit, route, p.RangeS, lastUserText(msgs), msgs, explain, ctxService, p.Operation, "", p.To, loc)
}
