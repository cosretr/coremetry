package api

// chat_lookup_route.go — v0.10.2449 (operatör raporu 2026-11-22: prod CoSRE
// kullanıcıları çoğunlukla TRACE ve ENDPOINT soruyor, cevap alamayıp tekrar
// soruyor). Üç soru ailesi guided kademesinin hazır demetlerine (servis
// sağlığı, operasyon, istisna arama) savruluyordu; doğru cevap yeni
// araçlardaydı:
//   - endpoint_lookup — "bu servisin url'i / endpoint'i nedir", "<OP> exact
//     endpoint url", "<domain> bu domainin çözdüğü pod";
//   - trace_where — "timeout hangi poddan", "hangi mq'da uzamış", "pod
//     içinde geçen süre", "hata X'ten mi dönmüş", "external call var mı";
//   - repo_lookup — "reponun linki", "podun reposu".
//   - dependency_path (v0.10.2453) — "neden yavaş, zincir", "hangi downstream
//     yüzünden", "kim çağırıyor", "etki alanı", "bağımlılık zinciri".
//
// İki SAF parça, MODELDEN BAĞIMSIZ (Qwen / gemma aynı metni görür; model
// dalı yok):
//  1. lookupAskFor — soru metni (+ yapıştırılan log ipuçları) → araç + argüman.
//     Eşleşen soru guided kademesine GİRMEZ (copilotChatGuided başı), serbest
//     döngüye düşer.
//  2. lookupLoopPromptTR — döngü istemine tek ipucu bloğu: hangi araç, hangi
//     argümanlar (metinden çıkarılan değerler aynen), sayıların yalnız araç
//     çıktısından aktarılacağı. Eşleşme yoksa "" → istem bayt bayt eski.

import (
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// lookupAsk — sınıflandırma sonucu.
type lookupAsk struct {
	Tool  string
	Args  map[string]string
	Clues mcptools.LogClues
	Why   string // kısa gerekçe (adım etiketi / test)
}

var (
	lookupRepoStems     = []string{"repo", "repository", "github", "gitlab", "bitbucket"}
	lookupURLStems      = []string{"url", "adres", "domain", "hostname", "alan-adı"}
	lookupEndpointStems = []string{"endpoint", "uç-nokta"}
	lookupIdentityAsk   = []string{"nedir", "ne", "neler", "hangi", "hangisi", "exact", "what", "which", "ver", "göster", "listele", "söyle"}
	// lookupHealthStems — sağlık / gecikme / hata sorusu: endpoint ADI anılsa da
	// guided demetleri doğru yer (eski davranış korunur).
	lookupHealthStems = []string{"yavaş", "hata", "error", "p95", "p99", "gecik", "latency", "slow", "sağlı", "grafik", "trend", "artış", "neden", "niye"}
	lookupPodStems    = []string{"pod"}
	lookupQueueStems  = []string{"mq", "kuyruk", "queue", "topic", "ibmmq", "rabbit", "jms", "kafka", "mesaj"}
	lookupWhereVerbs  = []string{"timeout", "time-out", "düş", "gel", "geçen", "sürel", "süres", "sürede", "uzam", "uzun", "patla", "hata"}
	lookupFromSuffix  = map[string]bool{"den": true, "dan": true, "ten": true, "tan": true}
	lookupQSuffix     = map[string]bool{"mi": true, "mı": true, "mu": true, "mü": true}
)

// lookupDomainToken — SAF: mesajdaki ilk host biçimli belirteç (en az iki
// nokta, harf TLD; ör. api.example.com). Servis adları noktasızdır.
func lookupDomainToken(toks []string) string {
	for _, t := range toks {
		t = strings.Trim(t, ".")
		if strings.Count(t, ".") < 2 {
			continue
		}
		last := t[strings.LastIndexByte(t, '.')+1:]
		if len(last) < 2 || strings.IndexFunc(last, func(r rune) bool { return r < 'a' || r > 'z' }) >= 0 {
			continue
		}
		return t
	}
	return ""
}

// lookupTopicToken — SAF: "-topic" / ".topic" içeren ya da "topic" sözcüğünden
// hemen önce gelen belirteç (ör. payments-topic, "payments topic'inde").
func lookupTopicToken(toks []string) string {
	for i, t := range toks {
		if strings.Contains(t, "-topic") || strings.Contains(t, ".topic") || strings.HasPrefix(t, "topic-") || strings.HasPrefix(t, "topic.") {
			return t
		}
		if i > 0 && strings.HasPrefix(t, "topic") && !tokenHasPrefix(toks[i-1:i], "hangi", "bu", "şu", "which", "the", "kafka") {
			return toks[i-1]
		}
	}
	return ""
}

// lookupPeerToken — SAF: "X'den mi dönmüş" kalıbındaki X (apostrof belirteç
// sınırı: "osb", "den", "mi").
func lookupPeerToken(toks []string) string {
	for i := 0; i+2 < len(toks); i++ {
		if lookupFromSuffix[toks[i+1]] && lookupQSuffix[toks[i+2]] {
			return toks[i]
		}
	}
	return ""
}

// lookupDepPathDir — SAF (v0.10.2453): çok-atlamalı bağımlılık sorusu → yön
// (down | up | both; "" = değil). Dar tutuldu: "neden yavaşladı, hangi
// bağımlılık" / "yavaşlamasının sebebi hangi downstream" root_cause'ta,
// "bağımlılıkları neler" service_topology'de, "nereden çağrılıyor" guided'da
// KALIR (TestRoutingRegression komşu satırları). Zincir / yüzünden / kim
// çağırıyor / etki alanı sözcüğü şart.
func lookupDepPathDir(toks []string) string {
	down := tokenHasPrefix(toks, "zincir", "dependency-path") ||
		(tokenHasPrefix(toks, "yüzünden") && tokenHasPrefix(toks, "downstream", "bağımlılık", "dependency"))
	up := tokenHasPrefix(toks, "etki-alan", "blast-radius") ||
		(hasExact(toks, "kim", "kimler", "who") && tokenHasPrefix(toks, "çağır", "call"))
	switch {
	case down && up:
		return "both"
	case up:
		return "up"
	case down:
		return "down"
	}
	return ""
}

func hasExact(toks []string, words ...string) bool {
	for _, t := range toks {
		for _, w := range words {
			if t == w {
				return true
			}
		}
	}
	return false
}

// lookupAskFor — SAF: soru → araç. Boş Tool = eşleşme yok (akış aynen).
func lookupAskFor(question string) lookupAsk {
	q := strings.TrimSpace(question)
	if q == "" {
		return lookupAsk{}
	}
	norm := normalizeGuidedMsg(q)
	norm = strings.ReplaceAll(norm, "alan adı", "alan-adı")
	norm = strings.ReplaceAll(norm, "uç nokta", "uç-nokta")
	norm = strings.ReplaceAll(norm, "dış çağrı", "external-call")
	norm = strings.ReplaceAll(norm, "external call", "external-call")
	norm = strings.ReplaceAll(norm, "etki alanı", "etki-alanı")
	norm = strings.ReplaceAll(norm, "blast radius", "blast-radius")
	norm = strings.ReplaceAll(norm, "dependency path", "dependency-path")
	toks := guidedTokens(norm)
	var clues mcptools.LogClues
	pasted := mcptools.LooksLikePastedLog(q)
	if pasted {
		clues = mcptools.ExtractLogClues(q)
	} else if c := mcptools.ExtractLogClues(q); len(c.TraceIDs) > 0 {
		clues.TraceIDs = c.TraceIDs
	}
	ask := func(tool, why string, args map[string]string) lookupAsk {
		if args == nil {
			args = map[string]string{}
		}
		return lookupAsk{Tool: tool, Args: args, Clues: clues, Why: why}
	}
	health := tokenHasPrefix(toks, lookupHealthStems...)

	// 1) repo
	if tokenHasPrefix(toks, lookupRepoStems...) || (tokenHasPrefix(toks, "depo") && tokenHasPrefix(toks, "link", "kod", "kaynak")) {
		return ask(mcptools.RepoLookupName, "repo", nil)
	}
	// 2) trace-nerede
	peer := lookupPeerToken(toks)
	returned := tokenHasPrefix(toks, "dön", "geldi", "gelmiş", "geliyor")
	switch {
	case peer != "" && returned:
		args := map[string]string{"peer": peer}
		if len(clues.TraceIDs) > 0 {
			args["trace_id"] = clues.TraceIDs[0]
		}
		return ask(mcptools.TraceWhereName, "peer-origin", args)
	case hasExact(toks, "external-call", "external", "dış-çağrı") && tokenHasPrefix(toks, "var", "çağır", "call", "yapıyor", "mı", "mi"):
		args := map[string]string{}
		if len(clues.TraceIDs) > 0 {
			args["trace_id"] = clues.TraceIDs[0]
		}
		return ask(mcptools.TraceWhereName, "external-call", args)
	case tokenHasPrefix(toks, lookupQueueStems...) && len(clues.TraceIDs) > 0:
		return ask(mcptools.TraceWhereName, "queue-trace", map[string]string{"trace_id": clues.TraceIDs[0]})
	case tokenHasPrefix(toks, lookupQueueStems...) && (hasExact(toks, "hangi", "hangisi", "which", "kim", "who") ||
		tokenHasPrefix(toks, "uzam", "gecik", "lag", "tüket", "consum", "üret", "produc", "yayın", "publish")):
		args := map[string]string{}
		if t := lookupTopicToken(toks); t != "" {
			args["topic"] = t
		}
		return ask(mcptools.MessagingFlowName, "queue", args)
	case tokenHasPrefix(toks, "lag") && tokenHasPrefix(toks, "consumer", "tüketici"):
		return ask(mcptools.MessagingFlowName, "consumer-lag", nil)
	case lookupDepPathDir(toks) != "" && len(clues.TraceIDs) == 0: // trace id varsa tek trace sorusu
		return ask(mcptools.DependencyPathName, "dependency-path", map[string]string{"direction": lookupDepPathDir(toks)})
	case (!tokenHasPrefix(toks, "sebe", "neden", "niye", "yavaşlama") && tokenHasPrefix(toks, "downstream")) || (hasExact(toks, "kimi", "kimleri", "neleri", "neyi", "hangi", "hangilerini") && tokenHasPrefix(toks, "çağır")):
		return ask(mcptools.DownstreamCallsName, "downstream", nil)
	case tokenHasPrefix(toks, lookupPodStems...) && tokenHasPrefix(toks, lookupWhereVerbs...) && lookupDomainToken(toks) == "":
		args := map[string]string{}
		if len(clues.TraceIDs) > 0 {
			args["trace_id"] = clues.TraceIDs[0]
		} else if tokenHasPrefix(toks, "timeout", "time-out") {
			args["mode"] = "errors"
		}
		if tokenHasPrefix(toks, "geçen", "sürel", "süres", "sürede") && !tokenHasPrefix(toks, "timeout") {
			if _, ok := args["trace_id"]; !ok {
				args["mode"] = "slow"
			}
		}
		return ask(mcptools.TraceWhereName, "pod-where", args)
	case tokenHasPrefix(toks, "timeout", "time-out") && hasExact(toks, "nereden", "nerden", "hangi", "kaynağı", "where"):
		args := map[string]string{"mode": "errors"}
		if len(clues.TraceIDs) > 0 {
			args = map[string]string{"trace_id": clues.TraceIDs[0]}
		}
		return ask(mcptools.TraceWhereName, "timeout-origin", args)
	}
	// 3) endpoint / URL / domain
	domain := lookupDomainToken(toks)
	urlAsk := tokenHasPrefix(toks, lookupURLStems...)
	epAsk := tokenHasPrefix(toks, lookupEndpointStems...) && hasExact(toks, lookupIdentityAsk...)
	if (urlAsk || epAsk || (domain != "" && tokenHasPrefix(toks, "pod", "çöz", "servis", "cluster"))) && !health {
		args := map[string]string{}
		if domain != "" {
			args["domain"] = domain
		}
		if op := opNameToken(q); op != "" {
			args["operation"] = op
		}
		return ask(mcptools.EndpointLookupName, "endpoint", args)
	}
	// 4) yapıştırılan log + trace id, başka soru sözcüğü yok → trace_where.
	if pasted && len(clues.TraceIDs) > 0 {
		return ask(mcptools.TraceWhereName, "pasted-log", map[string]string{"trace_id": clues.TraceIDs[0]})
	}
	return lookupAsk{}
}

// lookupLoopPromptTR — SAF: döngü istemine eklenen ipucu bloğu ("" = yok).
func lookupLoopPromptTR(a lookupAsk) string {
	if a.Tool == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nYÖNLENDİRME İPUCU (sunucu, sorunun metninden çıkardı): bu soru için ÖNCE ")
	b.WriteString(a.Tool)
	b.WriteString("(")
	keys := make([]string, 0, len(a.Args))
	for k := range a.Args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k + "=" + a.Args[k])
	}
	b.WriteString(") çağır; servis adı soruda ya da bağlamda yoksa önce resolve_entity, o da belirsizse kullanıcıya sor.")
	if hint := lookupToolHintTR[a.Tool]; hint != "" {
		b.WriteString(" " + hint)
	}
	if !a.Clues.Empty() {
		b.WriteString("\nYapıştırılan metinden çıkarılanlar (aynen kullan, değiştirme):")
		writeClue := func(label string, vs []string) {
			if len(vs) > 0 {
				b.WriteString(" " + label + "=" + strings.Join(vs, ","))
				b.WriteString(";")
			}
		}
		writeClue("trace_id", a.Clues.TraceIDs)
		writeClue("span_id", a.Clues.SpanIDs)
		writeClue("host", a.Clues.Hosts)
		writeClue("url", a.Clues.URLs)
		writeClue("exception", a.Clues.Exceptions)
		if a.Clues.Timeout {
			b.WriteString(" zaman aşımı ifadesi var;")
		}
		if calls := mcptools.ClueToolCalls(a.Clues); len(calls) > 0 {
			b.WriteString(" uygun çağrılar: " + strings.Join(calls, ", ") + ".")
		}
	}
	b.WriteString("\nCevaptaki her sayı, pod, cluster, host, URL ve depo linki araç çıktısında olmalı; araç bulamadıysa bunu açıkça söyle, değer uydurma.")
	return b.String()
}

// lookupToolHintTR — aracın cevapta nasıl aktarılacağı (tek cümle).
var lookupToolHintTR = map[string]string{
	mcptools.EndpointLookupName:  "Cevapta gözlenen host + yol + fiili (observed) ve rotaları aktar; domain sorusunda served_by'daki servis ve cluster başına pod'ları listele.",
	mcptools.TraceWhereName:      "Cevapta error_origin / timeouts / messaging / self_by_service / external_calls alanlarından ilgili olanı pod ve cluster adlarıyla aktar.",
	mcptools.RepoLookupName:      "repo_url \"unknown\" ise depo linki UYDURMA; note alanındaki açma yolunu aktar.",
	mcptools.MessagingFlowName:   "Cevapta topic başına üretici ve tüketici servisleri, p95 ve hata %'sini aktar; lag yalnız lag[] doluysa, yoksa lag_status'u söyle.",
	mcptools.DownstreamCallsName: "Cevapta hedef başına (targets önce, yoksa topology) yavaş ve hatalı olanları değerleriyle aktar.",
	mcptools.DependencyPathName:  "Cevapta likely_source'u (yol, farklar, deploy, sample_trace_id) ve kötüleşen kenarları değerleriyle aktar; likely_source null ise kaynak ADLANDIRMA; etki alanında çağıranları ve caller_degraded_too'yu söyle.",
}
