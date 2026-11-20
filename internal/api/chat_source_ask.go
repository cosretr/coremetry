package api

// chat_source_ask.go — v0.10.2381 TEK KAYNAK SORUSU (operatör, 2026-11-19:
// "CoSRE'de free format da chat yapabilsinler; bu arada eğer kafası karışırsa
// da kullanıcıya sorsun: wiki mi, envanter mi, yoksa telemetri mi diye
// sorabilir"; 2026-11-18, sessiz yanlış yönlendirmenin ardından: "Kullanıcıya
// sorabilir bu tarz durumlarda wiki, envanter ya da genel bilgi mi diye").
//
// Kök sorun: "hangi kaynaktan?" sorusu dört ayrı yerde dört ayrı cümleyle ve
// farklı seçenek kümeleriyle soruluyordu (chat_clarify.go iki seçenek;
// chat_source_discovery.go başa baş; chat_clarify_entity.go envanter / varlık
// ve envanter / wiki / genel; copilot_guided.go "servis için mi, genel bilgi
// mi"). Bunların dışındaki her durumda CoSRE kaynağı sormadan seçiyordu.
//
// Bu dosya tek kurucu + tek cevap yorumlayıcısı + tek hatırlama kuralıdır:
//
//	A. SORU — sourceAskText: yalnız bu kullanıcı / kurulum için VAR olan
//	   kaynaklar (sourceAvail → sourceAskOptions; sıra sourceAskOrder).
//	   Telemetri her zaman vardır; wiki bağlı + oturumlu kullanıcı + yerel
//	   profil; envanter = yüklenen tablolar (en az bir yüklü belge); genel =
//	   ai.generalQuestions.allow açık ve istek zararlı değil. İkiden az
//	   seçenek → soru YOK. Düz metin (2026-10-29 kararı: "soru cevap
//	   şeklinde, frontier model gibi"); v2'de aynı seçenekler soru kartında
//	   (clarifyOption), bekleyen soru saklanamayan yolda eğik komut çipleri.
//	B. NE ZAMAN — dört eski tetik aynen (başa baş keşif, tablo eşleşmesi,
//	   guided'ın almadığı telemetri adlı soru, servis adayı + genel biçim) ve
//	   YENİ: hiçbir kaynakta kanıt yok + sınıflandırıcı bir niyete oturtamadı +
//	   soru ne genel ne telemetri biçiminde (shouldAskUnknownSource — dar).
//	C. HATIRLAMA — cevap ChatContext.SourceChoice'a yazılır (bekleyen sorunun
//	   durduğu yer; yeni tablo / ayar yok). Aynı soruyu doğuracak sonraki tur
//	   30 dk boyunca sormadan o kaynağa gider, diğer seçenekler cevabın
//	   altında çip olur. Kullanıcı başka kaynağı adıyla söylerse ya da başka
//	   bir kaynak açıkça öndeyse seçim silinir.
//
// 👍/👎 ve örtük sinyaller burada OKUNMAZ (vote_runtime_guard_test.go).
// Kayıt: soru turu cosre-turn:clarify/<rota>; cevap ve hatırlanan seçim
// clarify_reply adımı (args: kind=source, reply=<kaynak>, remembered). Şema
// değişikliği yok.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/wiki"
)

const (
	// sourceChoiceTTL — hatırlanan kaynak seçiminin ömrü (seçim anından; her
	// uygulamada UZAMAZ — sınırlı kalsın).
	sourceChoiceTTL = 30 * time.Minute
	// generalFollowTTL — genel cevabın "devam sorusu" penceresi.
	generalFollowTTL = 30 * time.Minute
	// Kanıtsız soru koşulunun sözcük aralığı: tek sözcük bugünkü akışta kalır,
	// uzun serbest metin sohbettir (arama değil).
	sourceAskUnknownMinTokens = 2
	sourceAskUnknownMaxTokens = 12
)

// sourceAskOrder — soru metninde, kartta ve çiplerde seçenek sırası.
var sourceAskOrder = []string{srcTelemetry, srcWiki, srcExcel, srcExceptions, srcGeneral}

// sourceAvail — bu kullanıcı / kurulum / soru için var olan kaynaklar.
// Telemetri her zaman vardır. Exceptions yalnız keşif o soruda grup bulduysa.
type sourceAvail struct {
	Wiki, Inventory, General, Exceptions bool
}

// sourceAskOptions — SAF: sunulacak kaynak türleri (sourceAskOrder sırasıyla).
func sourceAskOptions(a sourceAvail) []string {
	out := []string{srcTelemetry}
	if a.Wiki {
		out = append(out, srcWiki)
	}
	if a.Inventory {
		out = append(out, srcExcel)
	}
	if a.Exceptions {
		out = append(out, srcExceptions)
	}
	if a.General {
		out = append(out, srcGeneral)
	}
	return out
}

// orderedSourceKinds — SAF: bilinen türler, sourceAskOrder sırasıyla, tekrarsız.
func orderedSourceKinds(opts []string) []string {
	var out []string
	for _, k := range sourceAskOrder {
		if containsStr(opts, k) {
			out = append(out, k)
		}
	}
	return out
}

// sourceAskPhrases — kaynağın soru içindeki hâli (bulunma durumu) ve soru eki.
var sourceAskPhrases = map[string]struct{ loc, particle string }{
	srcTelemetry:  {"telemetride (servis sağlığı, hatalar, trace'ler)", "mi"},
	srcWiki:       {"wikide", "mi"},
	srcExcel:      {"envanterde", "mi"},
	srcExceptions: {"Exceptions gruplarında", "mı"},
}

// sourceAskText — SAF: TEK kaynak sorusunun deterministik metni (LLM yok).
// lead: bağlam cümlesi ("" = yok). İkiden az seçenek → "" (soru sorulmaz).
func sourceAskText(lead string, opts []string) string {
	opts = orderedSourceKinds(opts)
	if len(opts) < 2 {
		return ""
	}
	var locs []string
	last, general := "", false
	for _, k := range opts {
		if k == srcGeneral {
			general = true
			continue
		}
		locs = append(locs, sourceAskPhrases[k].loc)
		last = sourceAskPhrases[k].particle
	}
	var q string
	switch {
	case general && len(locs) == 1:
		q = "Bunu " + locs[0] + " " + last + " arayayım, yoksa genel bilgi olarak mı cevaplayayım?"
	case general:
		q = "Bunu nerede arayayım: " + strings.Join(locs, ", ") + "; yoksa genel bilgi olarak mı cevaplayayım?"
	default:
		n := len(locs)
		q = "Bunu nerede arayayım: " + strings.Join(locs[:n-1], ", ") + ", yoksa " + locs[n-1] + " " + last + "?"
	}
	if lead = strings.TrimSpace(lead); lead != "" {
		return lead + " " + q
	}
	return q
}

// isSourceAskText — SAF: asistan metni kaynak sorusu mu (sourceAskText'in iki
// kalıbından biri). Çip komutu "önceki soru"yu ararken kaynak sorusuna
// verilen cevabı ("wiki") atlamak için; kısa telemetri sorusu ("hata
// logları") kaynak sorusunun ardından gelmediği sürece soru sayılır.
func isSourceAskText(text string) bool {
	return strings.Contains(text, "Bunu nerede arayayım: ") ||
		strings.Contains(text, " arayayım, yoksa genel bilgi olarak mı cevaplayayım?")
}

// sourceLeadRowsTR — SAF: tablo eşleşmesinin bağlam cümlesi.
func sourceLeadRowsTR(n int, vals []string) string {
	what := ""
	if len(vals) > 0 {
		v := vals
		if len(v) > 2 {
			v = v[:2]
		}
		what = " (" + strings.Join(v, ", ") + ")"
	}
	return fmt.Sprintf("Bu soru yüklü envanter tablosunda %d satırla eşleşiyor%s.", n, what)
}

// sourceLeadEntityTR — SAF: çıplak varlık adının bağlam cümlesi.
func sourceLeadEntityTR(kind, name string) string {
	what := "takım"
	if kind == bareEntityService {
		what = "servis"
	}
	return "“" + name + "” bir " + what + " adı; yüklü envanterde de geçiyor."
}

// sourceLeadTieTR — SAF: başa baş keşfin bağlam cümlesi.
func sourceLeadTieTR(tie []srcCand) string {
	names := make([]string, 0, len(tie))
	for _, c := range tie {
		n := "**" + sourceKindTR(c.Kind) + "**"
		if c.Label != "" {
			n += " (" + c.Label + ")"
		}
		names = append(names, n)
	}
	return "Bu soru için birden çok kaynakta güçlü kanıt buldum: " + strings.Join(names, ", ") + "."
}

// sourceLeadServiceTR — SAF: sınıflandırıcı servis isteyen bir niyet seçti ama
// soruda telemetri işareti yok (v0.10.2347 bulgusu).
func sourceLeadServiceTR(candidates []string) string {
	if len(candidates) == 0 {
		return ""
	}
	return "Soruda telemetri işareti yok; bir servis adını andırıyor olabilir (adaylar: " + strings.Join(candidates, ", ") + ")."
}

// sourceOptionLabelTR — SAF: seçeneğin kart / düğme etiketi. Etiket serbest
// metin olarak yazılsa da aynı seçeneğe düşer (test pinler).
func sourceOptionLabelTR(kind string) string {
	switch kind {
	case srcTelemetry:
		return "Telemetri"
	case srcWiki:
		return "Wiki"
	case srcExcel:
		return "Envanter"
	case srcExceptions:
		return "Exceptions"
	case srcGeneral:
		return "Genel bilgi"
	}
	return ""
}

// sourceAltChips — SAF: seçilen kaynak dışındaki seçenekler eğik komut çipi
// olarak (çip önceki soruyu o kaynakla yeniden sorar). chosen "" → hepsi.
func sourceAltChips(opts []string, chosen string, labels map[string]string) []string {
	var out []string
	for _, k := range orderedSourceKinds(opts) {
		if k == chosen || len(out) >= discoverMaxChips {
			continue
		}
		out = append(out, sourceChip(srcCand{Kind: k, Label: labels[k]}))
	}
	return out
}

// ── cevap yorumlayıcısı ─────────────────────────────────────────────────────

// sourceChoiceStems — katlanmış (memFold) sözcük önekleri → kaynak tercihleri.
// Birden çok tür: sözcük iki kaynağa da yakışır ("doküman"); sunulan
// seçenekler ve cevaptaki diğer sözcükler hangisi olduğunu belirler.
var sourceChoiceStems = []struct {
	stem  string
	kinds []string
}{
	{"wiki", []string{srcWiki, srcExcel}}, {"viki", []string{srcWiki, srcExcel}},
	{"wikki", []string{srcWiki, srcExcel}}, {"vikki", []string{srcWiki, srcExcel}},
	{"runbook", []string{srcWiki}}, {"dokumantasyon", []string{srcWiki}}, {"documentation", []string{srcWiki}},
	{"dokuman", []string{srcWiki, srcExcel}}, {"belge", []string{srcWiki, srcExcel}},
	{"docs", []string{srcWiki, srcExcel}}, {"doc", []string{srcWiki, srcExcel}},
	{"envanter", []string{srcExcel}}, {"inventory", []string{srcExcel}}, {"tablo", []string{srcExcel}},
	{"excel", []string{srcExcel}}, {"csv", []string{srcExcel}}, {"dosya", []string{srcExcel}},
	{"yuklu", []string{srcExcel}}, {"yuklen", []string{srcExcel}}, {"rag", []string{srcExcel, srcWiki}},
	{"telemetri", []string{srcTelemetry}}, {"telemetry", []string{srcTelemetry}}, {"coremetry", []string{srcTelemetry}},
	{"veri", []string{srcTelemetry}}, {"canli", []string{srcTelemetry}}, {"servis", []string{srcTelemetry}},
	{"service", []string{srcTelemetry}}, {"trace", []string{srcTelemetry}}, {"metrik", []string{srcTelemetry}},
	{"log", []string{srcTelemetry}}, {"hata", []string{srcTelemetry}}, {"takim", []string{srcTelemetry}},
	{"ekip", []string{srcTelemetry}}, {"team", []string{srcTelemetry}}, {"durum", []string{srcTelemetry}},
	{"saglik", []string{srcTelemetry}},
	{"exception", []string{srcExceptions, srcTelemetry}}, {"istisna", []string{srcExceptions, srcTelemetry}},
	{"genel", []string{srcGeneral}}, {"general", []string{srcGeneral}}, {"model", []string{srcGeneral}}, {"llm", []string{srcGeneral}},
}

// sourceChoiceTypoNames — yazım hatası toleransı yalnız uzun kaynak adlarında
// (kısa adların bilinen yazımları yukarıdaki listede).
var sourceChoiceTypoNames = []struct{ name, kind string }{
	{"envanter", srcExcel}, {"telemetri", srcTelemetry}, {"coremetry", srcTelemetry},
}

// sourceChoiceFillers — cevapta kaynak adının yanında geçebilen dolgu sözcükleri.
var sourceChoiceFillers = func() map[string]bool {
	m := map[string]bool{}
	for k := range sourceReplyFillers {
		m[k] = true
	}
	for _, k := range []string{"getir", "getirin", "goster", "gosterin", "olsun", "bakar", "bakalim", "arayalim", "aransin",
		"cevapla", "cevaplayin", "sadece", "yalniz", "direkt", "bana", "sen", "burada", "buradan", "icinden",
		"den", "dan", "ten", "tan", "ye", "ya", "e", "a", "te", "ta", "from", "use",
		"bilgi", "bilgisi", "bilgiyle", "bilgisiyle", "olarak", "ile"} {
		m[k] = true
	}
	return m
}()

// sourceStemKinds — SAF: sözcüğün kaynak tercihleri (en uzun önek kazanır);
// uzun adlarda tek harflik yazım hatası tolere edilir. nil = kaynak sözcüğü değil.
func sourceStemKinds(tok string) []string {
	best, kinds := 0, []string(nil)
	for _, st := range sourceChoiceStems {
		if len(st.stem) > best && strings.HasPrefix(tok, st.stem) {
			best, kinds = len(st.stem), st.kinds
		}
	}
	if kinds != nil {
		return kinds
	}
	for _, c := range sourceChoiceTypoNames {
		for _, d := range []int{0, -1, 1} { // aynı boy, eksik harf, fazla harf
			n := len(c.name) + d
			if n > len(tok) {
				continue
			}
			if levenshtein(tok[:n], c.name) <= 1 {
				return []string{c.kind}
			}
		}
	}
	return nil
}

// classifySourceChoice — SAF: kaynak sorusuna verilen serbest metin cevabın
// kaynağı; "" = cevap değil. 1–6 sözcük; her sözcük bir kaynak sözcüğü ya da
// dolgudur; kaynak sözcüklerinin SUNULAN seçeneklerde ortak tek bir karşılığı
// olmalı ("wiki ve coremetry" → "", "evet" → "").
func classifySourceChoice(text string, opts []string) string {
	toks := memTokens(text)
	if len(toks) == 0 || len(toks) > 6 || len(opts) == 0 {
		return ""
	}
	var cand []string
	hit := false
	for _, t := range toks {
		kinds := sourceStemKinds(t.f)
		if kinds == nil {
			if sourceChoiceFillers[t.f] {
				continue
			}
			return ""
		}
		var offered []string
		for _, k := range kinds {
			if containsStr(opts, k) {
				offered = append(offered, k)
			}
		}
		if !hit {
			cand, hit = offered, true
			continue
		}
		var both []string
		for _, k := range cand {
			if containsStr(offered, k) {
				both = append(both, k)
			}
		}
		cand = both
	}
	if len(cand) == 0 {
		return ""
	}
	return cand[0]
}

// pendingSourceOptions — SAF: bekleyen kaynak sorusunun seçenekleri. Seçeneksiz
// soru bu sürümden önce yazılmıştır (iki seçenekli eski küme).
func pendingSourceOptions(p *chatPendingAsk) []string {
	if p == nil || p.Kind != pendingKindSource {
		return nil
	}
	if len(p.Options) == 0 {
		return []string{srcTelemetry, srcWiki}
	}
	return p.Options
}

// ── hatırlanan seçim ────────────────────────────────────────────────────────

// chatSourceChoice — kullanıcının kaynak sorusuna verdiği son cevap.
type chatSourceChoice struct {
	Kind string `json:"kind"`
	At   int64  `json:"at"` // unix ns
}

// rememberedSource — SAF: hatırlanan seçim bu soruda uygulanır mı. Süresi
// içinde VE hâlâ sunulan bir kaynaksa türü, değilse "". 1 dk ileri saat toleransı.
func rememberedSource(c *chatSourceChoice, now time.Time, opts []string) string {
	if c == nil || c.Kind == "" || c.At <= 0 || !containsStr(opts, c.Kind) {
		return ""
	}
	age := now.Sub(time.Unix(0, c.At))
	if age < -time.Minute || age > sourceChoiceTTL {
		return ""
	}
	return c.Kind
}

// sourceChoiceSurvives — SAF: keşif tek bir kaynağı kazanan seçtiğinde
// hatırlanan seçim yaşar mı. Farklı VE güçlü (≥ discoverStrongMin) kazanan
// "açıkça önde"dir → seçim biter. Exceptions telemetrinin parçasıdır.
func sourceChoiceSurvives(choice, winner string, score float64) bool {
	if choice == "" || score < discoverStrongMin {
		return true
	}
	fam := func(k string) string {
		if k == srcExceptions {
			return srcTelemetry
		}
		return k
	}
	return fam(choice) == fam(winner)
}

// rememberSource / dropSourceChoice — hatırlanan seçimi yazar / siler. nil-güvenli.
func (st *chatContextState) rememberSource(kind string, now time.Time) {
	if st == nil || kind == "" {
		return
	}
	st.ctx.SourceChoice, st.dirty = &chatSourceChoice{Kind: kind, At: now.UnixNano()}, true
}

func (st *chatContextState) dropSourceChoice() {
	if st == nil || st.ctx.SourceChoice == nil {
		return
	}
	st.ctx.SourceChoice, st.dirty = nil, true
}

// dropSourceChoiceUnless — kullanıcı bir kaynağı adıyla söyledi: hatırlanan
// seçim başka bir kaynaksa biter.
func (st *chatContextState) dropSourceChoiceUnless(kind string) {
	if st != nil && st.ctx.SourceChoice != nil && st.ctx.SourceChoice.Kind != kind {
		st.dropSourceChoice()
	}
}

// noteSourceCommand — kaynak komutu (/wiki /excel /telemetri /exceptions /genel;
// çip tıkı da budur). Bekleyen kaynak sorusu varsa komut onun CEVABIDIR: soru
// kapanır ve seçim hatırlanır. Yoksa kullanıcı bu tur için bir kaynak adı
// söylemiştir: farklı bir hatırlanan seçim biter.
func noteSourceCommand(ctx context.Context, kind string) {
	st := chatContextFromCtx(ctx)
	if st == nil || kind == "" {
		return
	}
	now := time.Now()
	if p := st.ctx.Pending; p != nil && p.Kind == pendingKindSource {
		st.ctx.Pending, st.dirty = nil, true
		if pendingAskValid(p, now) && containsStr(pendingSourceOptions(p), kind) {
			st.rememberSource(kind, now)
			return
		}
	}
	st.dropSourceChoiceUnless(kind)
}

// ── kullanıcı kaynağı adıyla söyledi ────────────────────────────────────────

// namedSourceSuffixes — "telemetri" / "excel" yalnız bu hâl ekleriyle kaynak
// GÖSTERİR ("telemetride", "excel'den"); yalın ya da başka ekli hâli konudur
// ("telemetri verisi nasıl toplanır", "excel export").
var namedSourceSuffixes = map[string]bool{"de": true, "den": true, "ye": true, "e": true, "da": true, "dan": true,
	"sinde": true, "sinden": true}

// namedSourceKind — SAF: soru kaynağı ADIYLA gösteriyor mu ("wikide",
// "envanterde", "PlatformTeam envanterindeki", "telemetride"). "" = hayır.
// Wiki için wikiNamedCue (v0.10.2345) aynen. "tablo" BİLEREK yok: veritabanı
// tablosu telemetri sorusudur.
func namedSourceKind(q string) string {
	if wikiNamedCue(q) {
		return srcWiki
	}
	toks := memTokens(q)
	for i, t := range toks {
		next := ""
		if i+1 < len(toks) { // kesme imiyle ayrılmış ek: "excel'de"
			if sep := q[t.end:toks[i+1].start]; sep == "'" || sep == "’" || sep == "`" {
				next = toks[i+1].f
			}
		}
		switch {
		case strings.HasPrefix(t.f, "envanter") && (len(t.f) > len("envanter") || namedSourceSuffixes[next]):
			return srcExcel
		case t.f == "excel" && namedSourceSuffixes[next], t.f == "excelde", t.f == "excelden":
			return srcExcel
		case t.f == "telemetri" && namedSourceSuffixes[next],
			strings.HasPrefix(t.f, "telemetri") && namedSourceSuffixes[t.f[len("telemetri"):]]:
			return srcTelemetry
		}
	}
	return ""
}

// applyNamedSource — kullanıcı kaynağı adıyla söylediyse soru sorulmaz:
// "telemetride …" telemetriyi zorlar (doküman / keşif kademeleri atlanır),
// "envanterde …" envanteri onaylar (satır kademesi sormadan cevaplar; keşif ve
// tablo sorusu çekilir). Farklı bir hatırlanan seçim biter. Yalnız ana sohbet
// (panel / çekmece / trace bağlamı aynen).
func applyNamedSource(ctx context.Context, cst *chatContextState, q string, skipDocs bool) (context.Context, bool) {
	if !sourceTurnFrom(ctx).open() {
		return ctx, skipDocs
	}
	kind := namedSourceKind(q)
	if kind == "" {
		return ctx, skipDocs
	}
	cst.dropSourceChoiceUnless(kind)
	switch kind {
	case srcTelemetry:
		if forcedChatSource(ctx) == "" {
			ctx = withForcedChatSource(ctx, srcTelemetry)
		}
		return ctx, true
	case srcExcel:
		if !skipDocs {
			ctx = withDocsConfirmed(ctx)
		}
	}
	return ctx, skipDocs
}

// ── "kanıt yok + biçim belirsiz" kararı ─────────────────────────────────────

const (
	shapeGeneral   = "general"
	shapeTelemetry = "telemetry"
	shapeSmallTalk = "smalltalk"
	shapeShort     = "short"
	shapeLong      = "long"
	shapeFollowUp  = "followup"
	shapeUnclear   = ""
)

var (
	// sourceGeneralPhrases / sourceGeneralStems — genel bilgi sorusunun açık
	// biçimleri (normalizeGuidedMsg küçük harf; kökler önek).
	sourceGeneralPhrases = []string{"nedir", "ne demek", "ne anlama", "ne işe yarar", "kimdir", "what is", "what are",
		"how to", "how do", "how does", "explain", "difference"}
	sourceGeneralStems = []string{"açıkla", "anlat", "özetle", "örnek", "example", "fark", "karşılaştır", "kıyasla",
		"yaz", "write", "çevir", "translate", "öner", "tavsiye", "tanım", "define", "kod", "code", "fonksiyon", "function",
		"script", "algoritma", "nasıl", "how", "why", "neden", "niye", "niçin"}
	// sourceSelfPhrases — CoSRE'nin kendisine dair sorular.
	sourceSelfPhrases = []string{"sen kimsin", "kimsin", "adın ne", "neler yapabilirsin", "ne yapabilirsin", "ne yapabiliyorsun",
		"nasıl kullan", "hangi model", "who are you", "what can you do", "yardım", "help"}
	sourceSelfStems = []string{"cosre", "coremetry"}
	// sourceFollowUpWords — konuşmaya yaslanan sözcükler (tam eşleşme).
	sourceFollowUpWords = map[string]bool{"bu": true, "bunu": true, "buna": true, "bunun": true, "bunlar": true, "bunları": true,
		"şu": true, "şunu": true, "şunun": true, "o": true, "onu": true, "onun": true, "onlar": true, "peki": true, "ya": true,
		"diğer": true, "diğeri": true, "başka": true, "devam": true, "aynı": true, "aynısı": true, "aynısını": true,
		"yukarıdaki": true, "önceki": true, "tekrar": true, "ayrıca": true, "this": true, "that": true, "it": true,
		"more": true, "again": true}
)

// sourceQuestionShape — SAF: sorunun biçimi. shapeUnclear ("") yalnız kısa,
// selam / kendine dair / genel / telemetri / devam biçimlerinden HİÇBİRİNE
// oturmayan soru içindir. hasPrior: konuşmada önceki bir tur var mı.
func sourceQuestionShape(q string, hasPrior bool) string {
	if strings.Contains(q, "```") || strings.Count(strings.TrimSpace(q), "\n") > 0 {
		return shapeLong // yapıştırılan kod / çok satırlı metin sohbettir
	}
	norm := normalizeGuidedMsg(strings.TrimSpace(q))
	toks := guidedTokens(norm)
	if len(toks) > 0 && classIsChat(toks) {
		return shapeSmallTalk
	}
	for _, p := range sourceSelfPhrases {
		if strings.Contains(norm, p) {
			return shapeSmallTalk
		}
	}
	if tokenHasPrefix(toks, sourceSelfStems...) {
		return shapeSmallTalk
	}
	if len(toks) < sourceAskUnknownMinTokens {
		return shapeShort
	}
	if len(toks) > sourceAskUnknownMaxTokens {
		return shapeLong
	}
	if hasGuidedSignal(norm) || hasTelemetryVocab(norm, toks) || extractTraceID(norm) != "" ||
		extractSpanID(norm) != "" || hasStructuredRequestID(norm) || exceptionTermOf(q) != "" {
		return shapeTelemetry
	}
	for _, p := range sourceGeneralPhrases {
		if strings.Contains(norm, p) {
			return shapeGeneral
		}
	}
	if hasHowToShape(toks) || tokenHasPrefix(toks, sourceGeneralStems...) {
		return shapeGeneral
	}
	if hasPrior {
		for _, t := range toks {
			if sourceFollowUpWords[t] {
				return shapeFollowUp
			}
		}
	}
	return shapeUnclear
}

// unknownSourceSignals — "kanıt yok" sorusunun turdan gelen girdileri.
type unknownSourceSignals struct {
	Allowed           bool // ana sohbet (panel / çekmece / trace bağlamı ve tek-tur değerlendirme değil)
	Forced            bool // kaynak komutla / adıyla seçilmiş
	DiscoveryRan      bool // kaynak keşfi bu turda koştu
	Candidates        int  // keşfin kanıt bulduğu kaynak sayısı
	ClassifierMatched bool // niyet sınıflandırıcısı bir niyete (off_topic dahil) oturttu
	Vetoed            bool // off_topic vetosu (varlık adı / kimlik / telemetri sözcüğü)
	HasSubject        bool // konuşma durumunda özne var
	HasPrior          bool // konuşmada önceki tur var
}

// shouldAskUnknownSource — SAF: hiçbir kaynakta kanıt yokken kaynak sorulur mu.
// Yalnız: ana sohbet + kaynak seçilmemiş + keşif koştu ve hiçbir kaynakta kanıt
// bulamadı + sınıflandırıcı soruyu hiçbir niyete oturtamadı (veto da yok) +
// konuşmanın öznesi yok + soru biçimi belirsiz. Fazla sormak hata sayılır:
// her koşulun olumsuz testi chat_source_ask_test.go'da.
func shouldAskUnknownSource(q string, g unknownSourceSignals) bool {
	if !g.Allowed || g.Forced || !g.DiscoveryRan || g.Candidates > 0 || g.ClassifierMatched || g.Vetoed || g.HasSubject {
		return false
	}
	return sourceQuestionShape(q, g.HasPrior) == shapeUnclear
}

// ── genel sohbetin devamı ───────────────────────────────────────────────────

// generalFollowUpContinues — SAF: genel cevabın ardından gelen soru genel
// sohbeti sürdürür mü. Telemetri sinyali, canlı varlık (servis / ortam / takım
// adı, kimlik) ya da kaynak işareti taşıyan soru sürdürmez (olağan yönlendirme).
// Telemetri / varlık yarısı off_topic vetosunun AYNI kuralıdır (offTopicVeto).
func generalFollowUpContinues(q string, services, envs, teams []string) bool {
	if strings.TrimSpace(q) == "" || namedSourceKind(q) != "" || exceptionTermOf(q) != "" {
		return false
	}
	_, vetoed := offTopicVeto(q, services, envs, teams)
	return !vetoed
}

// noteGeneral / takeGeneral — son cevabın genel bilgi cevabı olduğu işareti.
// takeGeneral okur ve SİLER (bekleyen soru gibi: yalnız HEMEN sonraki serbest
// metin turu devam sayılır). nil-güvenli.
func (st *chatContextState) noteGeneral(now time.Time) {
	if st == nil {
		return
	}
	st.ctx.GeneralAt, st.dirty = now.UnixNano(), true
}

func (st *chatContextState) takeGeneral(now time.Time) bool {
	if st == nil || st.ctx.GeneralAt == 0 {
		return false
	}
	at := st.ctx.GeneralAt
	st.ctx.GeneralAt, st.dirty = 0, true
	age := now.Sub(time.Unix(0, at))
	return age >= -time.Minute && age <= generalFollowTTL
}

type prevGeneralKey struct{}

func withPrevGeneral(ctx context.Context) context.Context {
	return context.WithValue(ctx, prevGeneralKey{}, true)
}

func prevGeneral(ctx context.Context) bool {
	v, _ := ctx.Value(prevGeneralKey{}).(bool)
	return v
}

// chatGeneralFollowTier — genel sohbetin devamı: önceki tur genel bilgi
// cevabıysa ve bu soru telemetri sinyali / varlık / kaynak işareti taşımıyorsa
// cevap yine genel yoldan, konuşma geçmişiyle (offTopicGeneralAnswer). Doküman /
// keşif / wiki kademelerinden SONRA koşar: bir kaynak açıkça öndeyse o cevaplar.
// Kapı kapalıysa false ve akış bayt bayt eski.
func (s *Server) chatGeneralFollowTier(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, tc wikiTierContext) (handled, ok bool) {
	if !prevGeneral(ctx) || !wikiTierAllowed(tc) || !sourceTurnFrom(ctx).open() || forcedChatSource(ctx) != "" || docsConfirmed(ctx) {
		return false, false
	}
	q := strings.TrimSpace(lastUserText(msgs))
	if q == "" || !s.offTopicGeneralEnabled(q) {
		return false, false
	}
	if !generalFollowUpContinues(q, s.guidedServiceNames(ctx), s.guidedEnvNames(ctx), s.guidedTeamNames(ctx)) {
		return false, false
	}
	emitGuidedContextStep(emit, "genel sohbet devamı")
	chatExchangeFrom(ctx).noteRoute(string(guidedOffTopic))
	return s.offTopicGeneralAnswer(ctx, emit, msgs, q, "")
}

// ── tur durumu ──────────────────────────────────────────────────────────────

type sourceTurnKey struct{}

// sourceTurnState — bu turun kaynak sorusu durumu (ctx'te; tek goroutine yazar).
type sourceTurnState struct {
	allowed      bool // ana sohbet: durumlu soru + hatırlama açık
	discoveryRan bool
	candidates   int
	resolved     bool // bu turda kaynak soruldu ya da hatırlanan seçim uygulandı
	asked        bool // bu tur kaynak sorusuyla bitti (etkileşim kademesi clarify)
}

// withSourceTurn — turun durumunu ctx'e koyar. allowed=false (panel / çekmece /
// trace bağlamı, tek-tur değerlendirme, onay devamı): yeni soru koşulu ve
// hatırlama KAPALI; bugün zaten çipli soran yollar aynı cümleyi çiple sorar.
func withSourceTurn(ctx context.Context, allowed bool) context.Context {
	return context.WithValue(ctx, sourceTurnKey{}, &sourceTurnState{allowed: allowed})
}

func sourceTurnFrom(ctx context.Context) *sourceTurnState {
	st, _ := ctx.Value(sourceTurnKey{}).(*sourceTurnState)
	return st
}

func (st *sourceTurnState) open() bool { return st != nil && st.allowed }

// sourceAsked — bu tur kaynak sorusuyla mı bitti. Soru başka bir kademenin
// içinden basıldıysa (satır kademesi, doküman adımı, niyet) tur yine clarify
// olarak kaydedilir.
func sourceAsked(ctx context.Context) bool {
	st := sourceTurnFrom(ctx)
	return st != nil && st.asked
}

func (st *sourceTurnState) noteDiscovery(candidates int) {
	if st != nil {
		st.discoveryRan, st.candidates = true, candidates
	}
}

// ── kullanılabilir kaynaklar ────────────────────────────────────────────────

// inventoryExistsSeam — test dikişi: yüklü belge var mı (üretimde nil).
var inventoryExistsSeam func(ctx context.Context) bool

// inventoryExists — en az bir yüklenen belge indekste mi. Yalnız soru
// sorulmak üzereyken okunur (sıcak yol değil); okunamazsa envanter SUNULMAZ.
func (s *Server) inventoryExists(ctx context.Context, w *wiki.Service) bool {
	if inventoryExistsSeam != nil {
		return inventoryExistsSeam(ctx)
	}
	if w == nil {
		return false
	}
	pctx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
	defer cancel()
	paths, err := w.LocalDocPaths(pctx)
	return err == nil && len(paths) > 0
}

// sourceAvail — bu çağıran / kurulum / soru için var olan kaynaklar. haveRows:
// soru zaten tabloda eşleşti (envanter kesin var; indeks okunmaz).
func (s *Server) sourceAvail(ctx context.Context, question string, haveRows bool) sourceAvail {
	w := wikiKB()
	docs := w != nil && w.Enabled() && sourceCodeCallerAllowed(auth.FromContext(ctx))
	return sourceAvail{
		Wiki:      docs && s.localOnlyAvailable(ctx),
		Inventory: haveRows || (docs && s.inventoryExists(ctx, w)),
		General:   s.offTopicGeneralEnabled(question),
	}
}

// ── sor ya da hatırlananı uygula ────────────────────────────────────────────

// sourceAskSite — bir soru yerinin girdisi.
type sourceAskSite struct {
	Route    string            // etkileşim rotası ("" = yazılmaz)
	Question string            // özgün soru
	Lead     string            // bağlam cümlesi
	Entity   string            // çıplak varlık türü (kayıt için)
	Avail    sourceAvail       // var olan kaynaklar
	Labels   map[string]string // çip etiketi (tür → kanıt: "16 satır")
	// NeedMemory — bekleyen soru saklanamıyorsa (kullanıcısız / önbelleksiz /
	// ana sohbet dışı) HİÇ sorulmaz: akış aynen. false: aynı cümle eğik komut
	// çipleriyle sorulur (bugün zaten çipli soran yollar).
	NeedMemory bool
	// Stateless — durumlu yol hiç denenmez (satır kademesinin çipli sorusu).
	Stateless bool
}

// sourceAskResult — Asked: soru basıldı (tur biter). Reuse: hatırlanan seçim
// uygulanacak (çağıran routeSourceChoice ile yönlendirir). İkisi de boş: bu
// yer sormadı, akış aynen.
type sourceAskResult struct {
	Asked bool
	Reuse string
	Opts  []string
}

// sourceAskOrReuse — bütün soru yerlerinin ortak kuyruğu.
func (s *Server) sourceAskOrReuse(ctx context.Context, emit func(string, any), cst *chatContextState, site sourceAskSite) sourceAskResult {
	turn := sourceTurnFrom(ctx)
	opts := sourceAskOptions(site.Avail)
	text := sourceAskText(site.Lead, opts)
	if text == "" {
		return sourceAskResult{}
	}
	stateful := !site.Stateless && turn.open() && s.canRemember(cst)
	if !site.Stateless && (forcedChatSource(ctx) != "" || (turn != nil && turn.resolved)) {
		return sourceAskResult{}
	}
	if !stateful && site.NeedMemory {
		return sourceAskResult{}
	}
	if turn != nil {
		turn.resolved = true
	}
	q := strings.TrimSpace(site.Question)
	if stateful {
		now := time.Now()
		if kind := rememberedSource(cst.ctx.SourceChoice, now, opts); kind != "" {
			emitRememberedSourceStep(emit, kind, q)
			return sourceAskResult{Reuse: kind, Opts: opts}
		}
		cst.setPending(&chatPendingAsk{Kind: pendingKindSource, Question: clipRunes(q, chatPendingQuestionMax),
			Options: opts, Entity: site.Entity, AskedAt: now.UnixNano()})
		chatExchangeFrom(ctx).noteRoute(site.Route)
		emit("answer", map[string]any{"text": text, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
		emitSourceQuestion(ctx, emit, opts, text) // yalnız v2 kartı
		turn.asked = true
		return sourceAskResult{Asked: true, Opts: opts}
	}
	if turn != nil {
		turn.asked = true
	}
	chatExchangeFrom(ctx).noteRoute(site.Route)
	emit("answer", map[string]any{"text": text, "suggestions": sourceAltChips(opts, "", site.Labels),
		"sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
	return sourceAskResult{Asked: true, Opts: opts}
}

// emitRememberedSourceStep — hatırlanan seçim uygulanırken "Nasıl cevapladım"
// adımı (clarify_reply; args'ta remembered).
func emitRememberedSourceStep(emit func(string, any), kind, question string) {
	args, _ := json.Marshal(map[string]string{"kind": pendingKindSource, "reply": kind, "remembered": "true"})
	n := emitGuidedStep(emit, clarifyReplyTool, string(args))
	emitGuidedStepResult(emit, n, clarifyReplyTool, "soru: "+clipRunes(question, 200)+
		"\nhatırlanan kaynak seçimi: "+kind+" — bu konuşmada az önce seçildi, yeniden sorulmadı; "+
		"başka kaynak için cevabın altındaki seçenekleri kullan", nil)
}

// sourceRoute — seçilen kaynağa yönlendirmenin sonucu. handled=false: zincir
// ctx / msgs / emit ile sürer (telemetri).
type sourceRoute struct {
	ctx      context.Context
	msgs     []copilot.ChatMessage
	emit     func(string, any)
	handled  bool
	ok       bool
	tier     string // aisurface.Tier* adı
	skipDocs bool
}

// plainSourceAnswer — LLM'siz düz metin kapanış.
func plainSourceAnswer(emit func(string, any), text string) {
	emit("answer", map[string]any{"text": text, "sources": []any{}, "links": []guidedAnswerLink{}, "allowedLinks": []string{}})
}

// routeSourceChoice — ÖZGÜN soruyu seçilen kaynaktan cevaplar (cevap turu ve
// hatırlanan seçim ortak). alts: diğer seçeneklerin çipleri (cevabın altına).
func (s *Server) routeSourceChoice(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, kind, question string, alts []string) sourceRoute {
	out := sourceRoute{ctx: ctx, msgs: replaceLastUserText(msgs, question), emit: emit}
	ew := emitWithAltSources(emit, alts)
	switch kind {
	case srcWiki:
		out.ok = s.wikiForcedAnswer(ctx, ew, out.msgs, question)
		out.tier, out.handled = aisurface.TierWiki, true
	case srcTelemetry:
		out.ctx = withRecordsQuestion(withForcedChatSource(ctx, srcTelemetry), question)
		out.emit, out.skipDocs = ew, true
	case srcExcel:
		out.handled, out.ok = true, true
		rows, vals := s.docRowsFind(ctx, question, true)
		if len(rows) == 0 {
			rows, vals = s.docRowsFind(ctx, question, false)
		}
		if len(rows) == 0 {
			plainSourceAnswer(ew, docOrEntityNoRowsTR(question))
			out.tier = aisurface.TierClarify
			break
		}
		s.docRowsEmit(withRecordsQuestion(ctx, question), emit, rows, vals, alts)
		out.tier = aisurface.TierDocRows
	case srcExceptions:
		_, _, out.ok = s.sourceForcedAnswer(ctx, ew, srcExceptions, question, out.msgs)
		out.tier, out.handled = aisurface.TierDiscovery, true
	case srcGeneral:
		out.handled, out.ok = true, true
		if !s.offTopicGeneralEnabled(question) {
			plainSourceAnswer(ew, generalUnavailableTR)
			out.tier = aisurface.TierClarify
			break
		}
		chatExchangeFrom(ctx).noteRoute(string(guidedOffTopic))
		_, out.ok = s.offTopicGeneralAnswer(ctx, ew, generalChoiceMsgs(msgs, question), question, "")
		out.tier = aisurface.TierIntent
	}
	return out
}

// generalUnavailableTR — genel cevap seçildi ama verilemiyor.
const generalUnavailableTR = "Genel bilgi cevabı şu an kullanılamıyor (CoSRE genel soru ayarı kapalı ya da model yanıt vermedi)."

// generalChoiceMsgs — SAF: genel cevabın konuşma geçmişi. Sondaki "özgün soru →
// kaynak sorusu → cevap" üçlüsü geçmişe girmez (soru zaten şimdiki sorudur);
// öncesi aynen, sona özgün soru.
func generalChoiceMsgs(msgs []copilot.ChatMessage, question string) []copilot.ChatMessage {
	end := len(msgs)
	if end > 0 && msgs[end-1].Role == "user" {
		end-- // kaynak cevabı ("genel") ya da hatırlanan seçimde sorunun kendisi
	}
	if end >= 2 && msgs[end-1].Role != "user" && msgs[end-2].Role == "user" &&
		strings.TrimSpace(msgs[end-2].Text) == strings.TrimSpace(question) {
		end -= 2
	}
	out := append([]copilot.ChatMessage(nil), msgs[:end]...)
	return append(out, copilot.ChatMessage{Role: "user", Text: question})
}

// finishSourceAsk — soru yerinin sonucu → kademe sonucu. Soru basıldıysa tur
// clarify ile biter; hatırlanan seçim varsa özgün soru o kaynaktan cevaplanır;
// hiçbiri yoksa akış aynen.
func (s *Server) finishSourceAsk(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, question string, labels map[string]string, res sourceAskResult) sourceRoute {
	switch {
	case res.Asked:
		return sourceRoute{ctx: ctx, msgs: msgs, emit: emit, handled: true, ok: true, tier: aisurface.TierClarify}
	case res.Reuse != "":
		return s.routeSourceChoice(ctx, emit, msgs, res.Reuse, question, sourceAltChips(res.Opts, res.Reuse, labels))
	}
	return sourceRoute{ctx: ctx, msgs: msgs, emit: emit}
}

// tierNamed — kaynak yönlendirmesinin kademesi. Adlar copilotChat'in literal
// kademe adlarıyla aynı küme (aisurface.KnownTier); bilinmeyen ad clarify.
func (c *chatSpan) tierNamed(name string, ok bool) {
	switch name {
	case aisurface.TierWiki:
		c.tier("wiki", ok)
	case aisurface.TierDocRows:
		c.tier("doc-rows", ok)
	case aisurface.TierDiscovery:
		c.tier("discovery", ok)
	case aisurface.TierIntent:
		c.tier("intent", ok)
	default:
		c.tier("clarify", ok)
	}
}

// ── niyet kademesinin iki soru yeri ─────────────────────────────────────────

// intentSourceAsk — niyet kademesinden kaynak sorusu. done=true: tur bitti
// (soru basıldı ya da hatırlanan kaynak cevapladı). done=false: çağıranın
// akışı emitOut ile sürer (hatırlanan telemetri seçiminde diğer kaynak çipleri
// cevabın altına eklenir; aksi hâlde emit aynen).
func (s *Server) intentSourceAsk(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, site sourceAskSite) (handled, ok, done bool, emitOut func(string, any)) {
	site.Avail = s.sourceAvail(ctx, site.Question, false)
	res := s.sourceAskOrReuse(ctx, emit, chatContextFromCtx(ctx), site)
	r := s.finishSourceAsk(ctx, emit, msgs, site.Question, nil, res)
	if r.handled {
		return true, r.ok, true, emit
	}
	return false, false, false, r.emit
}

// unknownSourceAsk — YENİ koşul: sınıflandırıcı soruyu hiçbir niyete
// oturtamadı ve hiçbir kaynakta kanıt yok (shouldAskUnknownSource).
func (s *Server) unknownSourceAsk(ctx context.Context, emit func(string, any), msgs []copilot.ChatMessage, question string, vetoed bool) (handled, ok, done bool, emitOut func(string, any)) {
	turn := sourceTurnFrom(ctx)
	if !turn.open() {
		return false, false, false, emit
	}
	g := unknownSourceSignals{Allowed: true, Forced: forcedChatSource(ctx) != "" || docsConfirmed(ctx),
		DiscoveryRan: turn.discoveryRan, Candidates: turn.candidates, Vetoed: vetoed, HasPrior: hasPriorTurn(msgs)}
	if st := chatContextFromCtx(ctx); st != nil && chatStateOn(ctx) {
		g.HasSubject = stateHasSubject(st.ctx)
	}
	if !shouldAskUnknownSource(question, g) {
		return false, false, false, emit
	}
	return s.intentSourceAsk(ctx, emit, msgs, sourceAskSite{Route: "source_unknown_ask", Question: question, NeedMemory: true})
}

// hasPriorTurn — SAF: son kullanıcı mesajından önce bir asistan cevabı var mı.
func hasPriorTurn(msgs []copilot.ChatMessage) bool {
	for _, m := range msgs {
		if m.Role != "user" && strings.TrimSpace(m.Text) != "" {
			return true
		}
	}
	return false
}
