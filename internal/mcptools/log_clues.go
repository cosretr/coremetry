package mcptools

// log_clues.go — v0.10.2449 (operatör raporu 2026-11-22: "bir log attım,
// external call var mı"). Yapıştırılan log / stack parçasından trace id,
// span id, URL, host, exception sınıfı ve zaman aşımı işaretini çıkaran SAF
// çıkarıcı. Ağ / store yok; sonuç araç argümanı olur (trace_where trace_id,
// endpoint_lookup domain / url). Çıkarılan değer UYDURULMAZ: metinde yoksa
// alan boş kalır.

import (
	"regexp"
	"sort"
	"strings"
)

// LogClues — çıkarılanlar (her liste tekil, metindeki sırayla, tavanlı).
type LogClues struct {
	TraceIDs   []string `json:"trace_ids,omitempty"`
	SpanIDs    []string `json:"span_ids,omitempty"`
	URLs       []string `json:"urls,omitempty"`
	Hosts      []string `json:"hosts,omitempty"`
	Exceptions []string `json:"exceptions,omitempty"`
	Timeout    bool     `json:"timeout,omitempty"`
}

// Empty — hiçbir ipucu yok.
func (c LogClues) Empty() bool {
	return len(c.TraceIDs) == 0 && len(c.SpanIDs) == 0 && len(c.URLs) == 0 && len(c.Hosts) == 0 && len(c.Exceptions) == 0 && !c.Timeout
}

const (
	clueIDsMax   = 3
	clueURLsMax  = 5
	clueExcMax   = 3
	clueURLRunes = 300
)

var (
	// traceparent: 00-<32 hex trace>-<16 hex span>-<2 hex flags>
	clueTraceparentRe = regexp.MustCompile(`\b[0-9a-f]{2}-([0-9a-f]{32})-([0-9a-f]{16})-[0-9a-f]{2}\b`)
	clueTraceIDRe     = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])([0-9a-f]{32})(?:[^0-9a-z]|$)`)
	clueSpanKeyRe     = regexp.MustCompile(`(?i)span[_.\-]?id["']?\s*[:=]\s*["']?([0-9a-f]{16})\b`)
	clueURLRe         = regexp.MustCompile(`(?i)\b(?:https?|wss?|grpcs?)://[^\s"'<>\]\[)(,;]+`)
	// Çıplak host: en az iki etiket + harf TLD. Ardından ".<harf>" gelirse
	// (java.net.SocketTimeoutException) paket adıdır, host değil.
	clueHostRe    = regexp.MustCompile(`(?i)\b(?:[a-z0-9](?:[a-z0-9\-]{0,61}[a-z0-9])?\.)+(?:com|net|org|io|local|internal|lan|corp|intra|test|example|tr|svc|cluster\.local)\b`)
	clueExcRe     = regexp.MustCompile(`\b(?:[a-z][a-z0-9_]*\.)+[A-Z][A-Za-z0-9_$]*(?:Exception|Error|Fault)\b|\b[A-Z][A-Za-z0-9_]*(?:Exception|Error|Fault)\b`)
	clueTimeoutRe = regexp.MustCompile(`(?i)time[d]?\s?out|zaman aşımı|deadline exceeded`)
)

// clueHostSkip — host biçimli ama host olmayan Java/Kotlin paket kökleri
// (yalnız "paket.alt.Sınıf" sonrası düşmezse yakalanan iki etiketli
// adlar: "java.net" gibi).
var clueHostSkip = map[string]bool{"java.net": true, "javax.net": true, "sun.net": true, "jdk.net": true}

// ExtractLogClues — SAF: metinden ipuçları.
func ExtractLogClues(text string) LogClues {
	var c LogClues
	add := func(list *[]string, v string, max int) {
		if v == "" || len(*list) >= max {
			return
		}
		for _, x := range *list {
			if x == v {
				return
			}
		}
		*list = append(*list, v)
	}
	lower := strings.ToLower(text)
	for _, m := range clueTraceparentRe.FindAllStringSubmatch(lower, -1) {
		add(&c.TraceIDs, m[1], clueIDsMax)
		add(&c.SpanIDs, m[2], clueIDsMax)
	}
	for _, m := range clueTraceIDRe.FindAllStringSubmatch(lower, -1) {
		if strings.Trim(m[1], "0") == "" {
			continue // tümü sıfır geçersiz trace id
		}
		add(&c.TraceIDs, m[1], clueIDsMax)
	}
	for _, m := range clueSpanKeyRe.FindAllStringSubmatch(lower, -1) {
		add(&c.SpanIDs, m[1], clueIDsMax)
	}
	for _, u := range clueURLRe.FindAllString(text, -1) {
		u = strings.TrimRight(u, ".:")
		if r := []rune(u); len(r) > clueURLRunes {
			u = string(r[:clueURLRunes])
		}
		add(&c.URLs, u, clueURLsMax)
		add(&c.Hosts, urlHost(u), clueURLsMax)
	}
	for _, loc := range clueHostRe.FindAllStringIndex(text, -1) {
		h := strings.ToLower(text[loc[0]:loc[1]])
		if clueHostSkip[h] {
			continue
		}
		if rest := text[loc[1]:]; len(rest) >= 2 && rest[0] == '.' && isASCIILetter(rest[1]) {
			continue // paket adı (java.net.X)
		}
		if loc[0] > 0 && text[loc[0]-1] == '.' {
			continue // daha uzun bir noktalı adın kuyruğu
		}
		add(&c.Hosts, h, clueURLsMax)
	}
	for _, e := range clueExcRe.FindAllString(text, -1) {
		add(&c.Exceptions, e, clueExcMax)
	}
	c.Timeout = clueTimeoutRe.MatchString(text)
	return c
}

func isASCIILetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

// urlHost — SAF: şemalı URL'den host (port ve userinfo atılır, küçük harf).
func urlHost(u string) string {
	i := strings.Index(u, "://")
	if i < 0 {
		return ""
	}
	rest := u[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	if j := strings.LastIndexByte(rest, '@'); j >= 0 {
		rest = rest[j+1:]
	}
	if j := strings.LastIndexByte(rest, ':'); j >= 0 && !strings.Contains(rest[j:], "]") {
		rest = rest[:j]
	}
	return strings.ToLower(rest)
}

// LooksLikePastedLog — SAF: mesaj bir log / stack yapıştırması mı (çok
// satırlı ya da log damgası / stack işareti taşıyan uzun metin).
func LooksLikePastedLog(text string) bool {
	t := strings.TrimSpace(text)
	if len(t) < 60 {
		return false
	}
	markers := 0
	for _, m := range []string{"\tat ", "\n\tat ", "    at ", "Caused by:", " ERROR ", " WARN ", "Exception", "traceId", "trace_id", "spanId", "span_id", "traceparent", "level="} {
		if strings.Contains(t, m) {
			markers++
		}
	}
	if logStampRe.MatchString(t) {
		markers++
	}
	lines := strings.Count(t, "\n") + 1
	return markers >= 2 || (lines >= 3 && markers >= 1)
}

var logStampRe = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}`)

// ClueToolCalls — SAF: ipuçlarından önerilen araç çağrıları (ad → argüman
// özeti), deterministik sıra. trace id → trace_where; host / URL →
// endpoint_lookup. Boş ipucu → nil.
func ClueToolCalls(c LogClues) []string {
	var out []string
	for _, id := range c.TraceIDs {
		out = append(out, TraceWhereName+"(trace_id="+id+")")
	}
	hosts := append([]string(nil), c.Hosts...)
	sort.Strings(hosts)
	for i, h := range hosts {
		if i >= 2 {
			break
		}
		out = append(out, EndpointLookupName+"(domain="+h+")")
	}
	return out
}
