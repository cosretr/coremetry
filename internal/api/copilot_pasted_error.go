package api

// copilot_pasted_error.go — v0.10.819 (prod kullanıcı soruları, 2026-09-19):
// operatör sohbete bir hata/stack trace metni YAPIŞTIRIYOR ("Server Error in
// '/' Application. Could not load file or assembly 'log4net, Version=…,
// PublicKeyToken=669e0ddf0bb1aa2a'…", "Caused by: java.lang.NullPointer-
// Exception", Python traceback). Eskiden: .NET PublicKeyToken'ı 16-hex span
// sanılıyordu ("Span … BULUNAMADI"), token yoksa "error/exception" sözcüğü
// filo-geneli problemlere çekiyordu, "not"/"file" gibi sözcükler tek-önek
// servis çözümüne takılıyordu; metni ARAYAN hiçbir yol yoktu.
//
// Şimdi: ŞEKİL dedektörü (sözcük değil — "checkout hata alıyor mu?" burayı
// tetiklemez) + terim merdiveni → mevcut log_field rotası (message alanında
// terim geçen loglar; LLM'siz yönlendirme, kanıt paketi aynı) + cevaba Inbox
// exception grubu bağlantısı (ExceptionTerm). Servis yalnız sınırlı TAM ad
// eşleşmesinden (extractServiceFullName); ortam daraltması YOK (yapıştırılan
// metindeki "test"/"prod" sözcüğü ortam adı değildir).
//
// SIRA (routeGuidedIntent): 32-hex trace id ve yapılandırılmış istek kimliği
// önce (somut çapa), bu kontrol 16-hex span'den ÖNCE.

import (
	"regexp"
	"strings"
)

// pastedHardShapeRes — ŞEKİL: tek başına yeter ve soru cümlesinde OLAMAZ
// (stack frame, cause zinciri, traceback, assembly kimliği, Go panic dökümü).
var pastedHardShapeRes = []*regexp.Regexp{
	regexp.MustCompile(`(?m)^\s*at\s+[\w$]+(\.[\w$<>\[\]]+)+\(`),                      // Java / .NET stack frame (noktalı ad)
	regexp.MustCompile(`(?m)^\s*Caused by:\s`),                                        // Java cause chain
	regexp.MustCompile(`Traceback \(most recent call last\)`),                         // Python
	regexp.MustCompile(`File "[^"]+", line \d+`),                                      // Python frame
	regexp.MustCompile(`Version=\d+(\.\d+){1,3}[^\n]*PublicKeyToken=[0-9a-fA-F]{16}`), // .NET assembly identity
	regexp.MustCompile(`(?m)^goroutine \d+ \[`),                                       // Go panic dump
	regexp.MustCompile(`(?m)^panic: `),
}

// pastedStrongRes — şekil + nitelikli exception türü (tek satırda yeter; soru
// cümlesi kuralı routePastedError'da: "?" ile biten tek satır + yalnız tür → değil).
var pastedStrongRes = append(append([]*regexp.Regexp{}, pastedHardShapeRes...),
	regexp.MustCompile(`\b([A-Za-z][\w$]*\.)+[A-Z][\w$]*(Exception|Error|Throwable)\b`), // FQCN exception type
)

// pastedWeakRes — tek başına yetmez: 2 zayıf, ya da 1 zayıf + çok satır / uzun.
// Düzyazı kalıpları ("Unhandled exception", "Could not load file or assembly",
// "Server Error in '") BURADA: tek satırlık bir soru içinde geçebilirler
// ("Unhandled exception alan servisler hangileri?" — inceleme 2026-09-19).
var pastedWeakRes = []*regexp.Regexp{
	regexp.MustCompile(`\b[A-Z]\w+(Exception|Error)\b`),
	regexp.MustCompile(`(?i)\b(Exception Details|Source Error|Stack ?Trace|Inner ?Exception)\s*:`),
	regexp.MustCompile(`\bORA-\d{5}\b`),
	regexp.MustCompile(`(?i)Server Error in '`),
	regexp.MustCompile(`(?i)Could not load file or assembly`),
	regexp.MustCompile(`(?i)Unhandled exception`),
}

// pastedTypeNameRe — tür-şekilli terim (merdiven basamakları kendi çıktısını
// doğrular; geçersizse bir sonraki basamağa geçilir).
var pastedTypeNameRe = regexp.MustCompile(`^[A-Za-z_][\w$]{2,}$`)

// pastedErrorishRe — ilk-satır yedeği yalnız hata sözcüğü taşıyan satırı kabul eder.
var pastedErrorishRe = regexp.MustCompile(`(?i)(exception|error|fail|fatal|panic|timeout|refused|denied|not found|cannot|could not|unhandled|ORA-)`)

// looksLikePastedError — SAF, ham metin (harf kasası anlamlı).
func looksLikePastedError(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	for _, re := range pastedStrongRes {
		if re.MatchString(raw) {
			return true
		}
	}
	weak := 0
	for _, re := range pastedWeakRes {
		if re.MatchString(raw) {
			weak++
		}
	}
	if weak >= 2 {
		return true
	}
	return weak == 1 && (strings.Contains(raw, "\n") || len([]rune(raw)) >= 160)
}

var (
	pastedAssemblyLoadRe = regexp.MustCompile(`(?i)Could not load file or assembly '([^',]+)`)
	pastedAssemblyIDRe   = regexp.MustCompile(`([\w.]+),\s*Version=\d+(\.\d+){1,3}`)
	pastedOraRe          = regexp.MustCompile(`\bORA-\d{5}\b`)
	pastedCausedByRe     = regexp.MustCompile(`(?m)^\s*Caused by:\s*([\w$.]+)`)
	pastedExcTypeRe      = regexp.MustCompile(`\b(?:[A-Za-z][\w$]*\.)*([A-Z][\w$]*(?:Exception|Error|Throwable))\b`)
	pastedPanicRe        = regexp.MustCompile(`(?m)^panic: ([^\n]{3,80})`)
)

// pastedErrorTerm — SAF: yapıştırılan metinden ARANACAK terim (merdiven):
// 1. yüklenemeyen assembly adı ("log4net"), 2. assembly kimliğinin adı,
// 3. ORA-NNNNN, 4. SON "Caused by" türünün kısa adı (kök neden), 5. ilk
// exception türünün kısa adı, 6. Go panic mesajı, 7. ilk hata sözcüklü satır
// (≤80; soru cümlesi değil). Her basamak kendi çıktısını doğrular, geçersizse
// bir sonrakine geçer (inceleme: "Caused by: 2 errors" merdiveni kesiyordu).
// typed = 1-6 (tür/kod şekilli; Inbox exception bağlantısı yalnız o zaman).
// "" = terim yok (rota düşer).
func pastedErrorTerm(raw string) (term string, typed bool) {
	if m := pastedAssemblyLoadRe.FindStringSubmatch(raw); m != nil {
		if t := strings.TrimSpace(m[1]); len(t) >= 3 {
			return t, true
		}
	}
	if m := pastedAssemblyIDRe.FindStringSubmatch(raw); m != nil {
		if t := strings.TrimSpace(m[1]); len(t) >= 3 {
			return t, true
		}
	}
	if m := pastedOraRe.FindString(raw); m != "" {
		return m, true
	}
	if all := pastedCausedByRe.FindAllStringSubmatch(raw, -1); len(all) > 0 {
		if t := simpleTypeName(all[len(all)-1][1]); pastedTypeNameRe.MatchString(t) {
			return t, true
		}
	}
	if m := pastedExcTypeRe.FindStringSubmatch(raw); m != nil && pastedTypeNameRe.MatchString(m[1]) {
		return m[1], true
	}
	if m := pastedPanicRe.FindStringSubmatch(raw); m != nil {
		if t := strings.TrimSpace(m[1]); len(t) >= 3 {
			return t, true
		}
	}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "server error in") {
			continue
		}
		if strings.HasSuffix(line, "?") || !pastedErrorishRe.MatchString(line) {
			return "", false // soru cümlesi ya da hata sözcüğü yok: aranacak terim değil
		}
		r := []rune(line)
		if len(r) > 80 {
			r = r[:80]
		}
		return strings.TrimRight(string(r), " .:;,"), false
	}
	return "", false
}

// pastedIsQuestion — SAF: "?" ile biten tek satır, sert şekil (frame/traceback/
// assembly/panic) yok → hakkında SORU sorulan bir tür adı, yapıştırma değil
// ("com.shop.TimeoutException kaç kez oldu?").
func pastedIsQuestion(raw string) bool {
	raw = strings.TrimSpace(raw)
	if strings.Contains(raw, "\n") || !strings.HasSuffix(raw, "?") {
		return false
	}
	for _, re := range pastedHardShapeRes {
		if re.MatchString(raw) {
			return false
		}
	}
	return true
}

func simpleTypeName(fq string) string {
	fq = strings.TrimRight(fq, ".:")
	if i := strings.LastIndex(fq, "."); i >= 0 {
		fq = fq[i+1:]
	}
	return fq
}

// routePastedError — SAF: dedektör + terim → log_field rotası (message alanında
// terim GEÇEN loglar). ok=false → router sürer. ÇEKİLİR: operatör açık bir log
// alanı / trace araması istediyse (o dallar router'da daha sonra gelir; tür adı
// geçiyor diye alan/kip EZİLMEZ) ve tür adı hakkında soru cümlesiyse.
func routePastedError(raw, msg string, toks []string, services []string) (guidedRoute, bool) {
	if !looksLikePastedError(raw) || pastedIsQuestion(raw) {
		return guidedRoute{}, false
	}
	if _, _, _, lfOK := extractLogFieldQuery(raw, toks); lfOK {
		return guidedRoute{}, false
	}
	if _, _, tsOK := extractTraceSearch(raw, toks); tsOK {
		return guidedRoute{}, false
	}
	term, typed := pastedErrorTerm(raw)
	if n := len([]rune(term)); n < 3 || n > 256 {
		return guidedRoute{}, false
	}
	r := guidedRoute{
		Intent:      guidedLogField,
		Service:     extractServiceFullName(msg, services),
		LogField:    "message",
		LogValue:    term,
		LogContains: true,
	}
	if typed {
		r.ExceptionTerm = term
	}
	return r, true
}
