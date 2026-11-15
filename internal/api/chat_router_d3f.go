package api

// chat_router_d3f.go — v0.10.2197 (CoSRE router boşlukları D3-F,
// docs/cosre/router-gaps-audit.md F sınıfı). YALNIZ v2 (cosreV2Marked); v1
// yolu bu dosyanın hiçbir yardımcısını çağırmaz → bayt bayt eski.
//
// Operatör kararı (2026-11-15, iki açık soru):
//  1. Katalogda olmayan operasyon adı ("OP_NAME_REST operasyonu neden hata
//     veriyor?") filo `problems`'a (yanlış özne) düşüyordu. Artık cevap
//     MODELSİZ "operasyon bulunamadı" + en yakın ≤5 katalog adı; her öneri
//     sorunun kendisi, ad değiştirilmiş (tıklanınca aynı soru o adla).
//     Aday okuması sunucuda, SINIRLI: cosre_operation_catalog'da adın
//     parçalarını içeren ≤60 satır (CatalogCandidates, ön-getirmenin aynı
//     okuması) → nearNames ile ≤5. Tam katalog listesi asla çekilmez.
//  2. `DEP1080` biçimli iş kodu (`[A-Z]{2,5}\d{3,5}`) → guided
//     `business_code` rotası: business_code_summary_5m (FUNCTION_CODE /
//     CHANNEL_CODE, son 7 gün, nokta okuma) servis kırılımı + ilk/son
//     görülme; operasyonlar cosre_operation_catalog.function_codes'tan. Ham
//     span YOK. Bulunamazsa açıkça "bulunamadı" der. Cevap modelsiz.
//
// Rota yeni bir niyet değil (LLM niyet listesi değişmez): ask_service niyeti
// OpNotFound / BusinessCode alanıyla; /ai rota etiketi operation_not_found /
// business_code (guidedRouteLabel).

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	guidedOpNotFoundRoute   = "operation_not_found"
	guidedBusinessCodeRoute = "business_code"
	// opNearMax — "en yakın" öneri tavanı.
	opNearMax = 5
	// bizCodeLookbackS — iş kodu araması penceresi (MV TTL 30 gün; 7 gün nokta okuma).
	bizCodeLookbackS = 7 * 24 * 3600
	// bizCodeOpsMax — iş kodunu taşıyan katalog operasyonu tavanı.
	bizCodeOpsMax = 10
)

// bizCodeShapeRe — iş kodu biçimi (audit F: `[A-Z]{2,5}\d{3,5}`, "DEP1080").
var bizCodeShapeRe = regexp.MustCompile(`^[A-Z]{2,5}[0-9]{3,5}$`)

// bizCodeDeny — kod biçimine uyan ama iş kodu olmayan önekler.
var bizCodeDeny = []string{"HTTP", "ISO", "UTF", "RFC", "SHA"}

// businessCodeToken — SAF: mesajdaki ilk iş kodu biçimli belirteç.
func businessCodeToken(msg string) string {
	for _, t := range opTokens(msg) {
		if !bizCodeShapeRe.MatchString(t) {
			continue
		}
		deny := false
		for _, p := range bizCodeDeny {
			if strings.HasPrefix(t, p) {
				deny = true
				break
			}
		}
		if !deny {
			return t
		}
	}
	return ""
}

// opNameToken — SAF: mesajdaki ilk operasyon kodu biçimli belirteç
// (BÜYÜK_HARF_ALT_ÇİZGİ, opCodeShapeRe — ön-getirmenin Oracle kapısıyla aynı).
func opNameToken(msg string) string {
	for _, t := range opTokens(msg) {
		if opCodeShapeRe.MatchString(t) {
			return t
		}
	}
	return ""
}

// d3fShape — SAF: v2 hızlı yol kapısı — mesaj iş kodu ya da operasyon kodu taşıyor.
func d3fShape(msg string) bool {
	return businessCodeToken(msg) != "" || opNameToken(msg) != ""
}

// d3fGenericIntent — SAF: belirtecin öznesini ezmeyecek kadar genel rotalar
// (filo problems, netleştirme, varlık araması, rota yok).
func d3fGenericIntent(i guidedIntent) bool {
	switch i {
	case guidedNone, guidedProblems, guidedAskService, guidedFindEntity:
		return true
	}
	return false
}

// promoteD3F — SAF: v2'de servissiz genel rota + iş kodu → business_code;
// + katalog dışı operasyon kodu → operation_not_found. Ön-getirme katalogda
// eşleşen operasyonu guided'dan ÖNCE alır (copilot_chat.go: opPre != nil →
// guided yok), yani buraya gelen operasyon kodu katalogda eşleşmemiştir;
// demet yine katalogdan okur ve tam eşi görürse onu ilk öneri yapar.
func promoteD3F(r guidedRoute, raw string, v2 bool) guidedRoute {
	if !v2 || r.Service != "" || !d3fGenericIntent(r.Intent) {
		return r
	}
	if code := businessCodeToken(raw); code != "" {
		return guidedRoute{Intent: guidedAskService, BusinessCode: code, Env: r.Env}
	}
	if op := opNameToken(raw); op != "" {
		return guidedRoute{Intent: guidedAskService, OpNotFound: op, Env: r.Env}
	}
	return r
}

// d3fRouteLabel — SAF: /ai rota etiketi ("" = bu dosyanın rotası değil).
func d3fRouteLabel(r guidedRoute) string {
	switch {
	case r.Intent == guidedAskService && r.BusinessCode != "":
		return guidedBusinessCodeRoute
	case r.Intent == guidedAskService && r.OpNotFound != "":
		return guidedOpNotFoundRoute
	}
	return ""
}

// opNearSeam / bizCodeSeam — test dikişleri (prod'da nil → store).
var (
	opNearSeam  func(ctx context.Context, toks []string, limit int) ([]chstore.CatalogOperation, error)
	bizCodeSeam func(ctx context.Context, code string, from, to time.Time) (bizCodeLookup, error)
)

// bizCodeLookup — iş kodu okumasının iki kolu.
type bizCodeLookup struct {
	Seen []chstore.BusinessCodeSeenRow
	Ops  []chstore.CatalogOperation
}

// opNearQueryTokens — SAF: adın katalog aday okuması için parçaları
// ("OP_NAME_REST" → OP_NAME_REST, NAME, REST); ≥opTokenMin, ≤opTokenMax.
func opNearQueryTokens(op string) []string {
	out := []string{op}
	for _, p := range strings.Split(op, "_") {
		if len([]rune(p)) >= opTokenMin && len(out) < opTokenMax && !strings.EqualFold(p, op) {
			out = append(out, p)
		}
	}
	return out
}

// opNearNames — SAF: katalog adaylarından en yakın ≤max tekil ad.
func opNearNames(op string, cands []chstore.CatalogOperation, max int) []string {
	seen := map[string]bool{}
	names := make([]string, 0, len(cands))
	for _, c := range cands {
		if !seen[c.Operation] {
			seen[c.Operation] = true
			names = append(names, c.Operation)
		}
	}
	return nearNames(op, names, max)
}

// opNotFoundAnswerTR — SAF: modelsiz cevap + öneri çipleri (soru, ad değişmiş).
func opNotFoundAnswerTR(op, question string, near []string) (string, []string) {
	exact := len(near) > 0 && strings.EqualFold(near[0], op)
	sugg := make([]string, 0, len(near))
	for _, n := range near {
		if strings.Contains(question, op) {
			sugg = append(sugg, strings.Replace(question, op, n, 1))
		} else {
			sugg = append(sugg, n+" operasyonuna bak")
		}
	}
	switch {
	case exact:
		return fmt.Sprintf("%s operasyon kataloğunda var; aşağıdaki çiple aynı soruyu o operasyon için sorabilirsiniz.", near[0]), sugg
	case len(near) == 0:
		return fmt.Sprintf("Operasyon bulunamadı: %s operasyon kataloğunda (son 24 sa) yok ve yakın bir ad da bulunamadı. Adı kontrol edin ya da servis adıyla sorun.", op), nil
	}
	return fmt.Sprintf("Operasyon bulunamadı: %s operasyon kataloğunda (son 24 sa) yok. En yakın adlar: %s. Birini seçerseniz aynı soruyu o operasyon için sorarım.",
		op, strings.Join(near, ", ")), sugg
}

// bizCodeAnswerTR — SAF: iş kodu cevabı (modelsiz) + öneri çipleri.
func bizCodeAnswerTR(code string, l bizCodeLookup, loc *time.Location) (string, []string) {
	if loc == nil {
		loc = time.UTC
	}
	if len(l.Seen) == 0 && len(l.Ops) == 0 {
		return fmt.Sprintf("%s kodu son 7 günde bulunamadı: iş kodu özetinde (FUNCTION_CODE / CHANNEL_CODE) ve operasyon kataloğunda kaydı yok. "+
			"Log gövdeleri ve exception mesajları bu aramaya girmez.", code), nil
	}
	var b strings.Builder
	var sugg []string
	if len(l.Seen) > 0 {
		var calls, errs uint64
		first, last := l.Seen[0].FirstSeen, l.Seen[0].LastSeen
		kinds := map[string]bool{}
		for _, r := range l.Seen {
			calls += r.Calls
			errs += r.Errors
			kinds[r.Kind] = true
			if r.FirstSeen.Before(first) {
				first = r.FirstSeen
			}
			if r.LastSeen.After(last) {
				last = r.LastSeen
			}
		}
		kind := "FUNCTION_CODE"
		if kinds["channel_code"] && !kinds["function_code"] {
			kind = "CHANNEL_CODE"
		} else if kinds["channel_code"] {
			kind = "FUNCTION_CODE / CHANNEL_CODE"
		}
		fmt.Fprintf(&b, "%s (%s) son 7 günde %s span'de görüldü, %s hatalı · ilk görülme %s, son görülme %s.\n\nServisler:\n",
			code, kind, fmtThousands(calls), fmtThousands(errs), first.In(loc).Format("02.01 15:04"), last.In(loc).Format("02.01 15:04"))
		for _, r := range l.Seen {
			fmt.Fprintf(&b, "- %s: %s çağrı, %s hata\n", r.Service, fmtThousands(r.Calls), fmtThousands(r.Errors))
			if len(sugg) < 3 {
				sugg = append(sugg, r.Service+" sağlığı nasıl?")
			}
		}
	} else {
		fmt.Fprintf(&b, "%s kodu iş kodu özetinde son 7 günde yok; operasyon kataloğunda şu operasyonlarla kayıtlı.\n", code)
	}
	if len(l.Ops) > 0 {
		b.WriteString("\nOperasyonlar (katalog, son 24 sa):\n")
		for _, o := range l.Ops {
			fmt.Fprintf(&b, "- %s · %s: %s çağrı, %s hata\n", o.Operation, o.Service, fmtThousands(o.CallCount), fmtThousands(o.ErrorCount))
		}
		sugg = append([]string{l.Ops[0].Operation + " operasyonuna bak"}, sugg...)
	}
	return strings.TrimRight(b.String(), "\n"), sugg
}

// guidedD3FAnswer — runGuidedRoute kancası: iki rotanın modelsiz cevabı.
func (s *Server) guidedD3FAnswer(ctx context.Context, emit func(string, any), route guidedRoute, question string, anchorTo time.Time, loc *time.Location) (handled, ok bool) {
	switch d3fRouteLabel(route) {
	case guidedOpNotFoundRoute:
		toks := opNearQueryTokens(route.OpNotFound)
		n := emitGuidedStep(emit, "operation_catalog", fmt.Sprintf(`{"near":%q}`, route.OpNotFound))
		var cands []chstore.CatalogOperation
		var err error
		switch {
		case opNearSeam != nil:
			cands, err = opNearSeam(ctx, toks, opCandidateLimit)
		case s.store != nil:
			cands, err = s.opCatalogCandidates(ctx, toks, opCandidateLimit)
		}
		emitGuidedStepResult(emit, n, "operation_catalog", "", err)
		if err != nil {
			emit("answer", map[string]any{"text": "Operasyon kataloğu okunamadı; " + route.OpNotFound + " adını şu an doğrulayamıyorum."})
			return true, false
		}
		text, sugg := opNotFoundAnswerTR(route.OpNotFound, question, opNearNames(route.OpNotFound, cands, opNearMax))
		emit("answer", map[string]any{"text": text, "suggestions": sugg})
		return true, true
	case guidedBusinessCodeRoute:
		to := anchorTo
		if to.IsZero() {
			to = time.Now()
		}
		from := to.Add(-bizCodeLookbackS * time.Second)
		n := emitGuidedStep(emit, "resolve_business_code", fmt.Sprintf(`{"code":%q}`, route.BusinessCode))
		l, err := s.bizCodeRead(ctx, route.BusinessCode, from, to)
		emitGuidedStepResult(emit, n, "resolve_business_code", "", err)
		if err != nil {
			emit("answer", map[string]any{"text": route.BusinessCode + " kodu şu an aranamadı (iş kodu özeti okunamadı)."})
			return true, false
		}
		text, sugg := bizCodeAnswerTR(route.BusinessCode, l, loc)
		emit("answer", map[string]any{"text": text, "suggestions": sugg})
		return true, true
	}
	return false, false
}

// bizCodeRead — iki kol: MV servis kırılımı + katalog operasyonları.
func (s *Server) bizCodeRead(ctx context.Context, code string, from, to time.Time) (bizCodeLookup, error) {
	if bizCodeSeam != nil {
		return bizCodeSeam(ctx, code, from, to)
	}
	if s.store == nil {
		return bizCodeLookup{}, nil
	}
	seen, err := s.store.BusinessCodeSeen(ctx, code, from, to)
	if err != nil {
		return bizCodeLookup{}, err
	}
	ops, err := s.store.CatalogOperationsByFunctionCode(ctx, code, bizCodeOpsMax)
	if err != nil {
		return bizCodeLookup{}, err
	}
	return bizCodeLookup{Seen: seen, Ops: ops}, nil
}
