package api

// chat_state_resolve.go — v0.10.2324 (conversation-audit §6.1 A): konuşma
// durumunun DETERMİNİSTİK çözücüsü. Her v2 turunun başında, kademe
// zincirinden ÖNCE koşar (chat_state.go beginChatStateTurn); model çağrısı
// yoktur. SAF: girdi önceki durum + soru + saat + canlı kataloglar.
//
// Tanıdıkları:
//   - göreli zaman (Türkçe): "bugün", "dün", "geçen hafta", "bu hafta",
//     "dün aynı saatte" / "dün bu saatte", gün parçası ("sabah", "öğleden
//     sonra", "akşam", "gece", "dün gece"; "dün sabah" gibi birleşik),
//     "son N dakika/saat/gün" ve açık saat aralığı ("09:00-11:00", "9 ile 11
//     arası"; abs_window.go aynı ayrıştırıcı). Çıplak "dün" önceki pencere
//     bugünün bir dilimiyse AYNI dilimi bir gün geri kaydırır ("bu sabah" →
//     "dün sabah"); değilse dünün tamamı.
//   - ortam: canlı ortam kataloğunda tek anma (findEnvMentions).
//   - servis: varlık çözücüsü (mcptools.ResolveEntities, tam ad jetonu) — ad
//     eşlemesi burada yeniden yazılmaz.
//   - kanal kodu / fonksiyon kodu / problem kimliği: yalnız açık önekle
//     ("kanal kodu X1", "fonksiyon 4711", "problem P-42") ve rakam ya da büyük
//     harf taşıyan değerle.
//   - hitap: kullanıcının kendi yazımından (siz eki > sen eki > önceki >
//     varsayılan "sen").
//   - bekleyen öneri (offer): çıplak kabul ("bak", "evet", "devam", "olur",
//     "tamam", …) ve taze öneri → öneri yürütülür.
//
// Bilerek TANIMADIKLARI: tarihli göreli ifadeler ("geçen salı", "3 gün önce
// sabah"), "dün"den başka gün kaydırma ("önceki gün"), birden çok pencere
// (kıyas guided'ın window_compare'inde kalır), bulanık servis adı (tek tam ad
// jetonu değilse çözücü aday listesine gider, durum DEĞİŞMEZ), takım adı.
//
// Devralma sınırı (yanlış devralma riski, audit §6.1 A): açık ifade kazanır;
// yeni açık servis eskisinin yerine geçer (operasyon ve trace düşer);
// devralma yalnız kısa devam sorusunda (followUpMaxRunes, D5 kuralı) ve filo
// ifadesi yokken; öneri yalnız ÇIPLAK kabulde ve ömrü içinde yürür, başka her
// soru öneriyi siler; TTL'i geçmiş durum hiç okunmaz.

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	// chatStateTTL — kapsam alanlarının ömrü (UpdatedAt'ten). Redis anahtarı
	// 24 s yaşar; bundan eski durum devralınmaz (D5 alanları dahil).
	chatStateTTL = 2 * time.Hour
	// chatOfferTTL — önerinin ömrü: "bak" bundan sonra öneriyi yürütmez.
	chatOfferTTL = 15 * time.Minute
)

// Hitap değerleri.
const (
	addressSen = "sen"
	addressSiz = "siz"
)

// stateWindow — çözülen pencere. ToMs 0 = şimdiye göreli.
type stateWindow struct {
	RangeS int64
	ToMs   int64
	Label  string
}

// stateResolveIn — çözücü girdisi.
type stateResolveIn struct {
	Question string
	Prev     ChatContext
	Now      time.Time
	Loc      *time.Location
	Services []string
	Envs     []string
	// HasPrior — konuşmada önceki kullanıcı turu var (ilk tur devralmaz; D5).
	HasPrior bool
}

// stateResolveOut — çözücü çıktısı. Next = bu turun durumu (kalıcı alanlar
// dahil); bayraklar bu turun ne söylediğini ve neyin devralınacağını taşır.
type stateResolveOut struct {
	Next           ChatContext
	Stale          bool // önceki durum TTL dışıydı — yok sayıldı
	ServiceSet     bool
	EnvSet         bool
	WindowSet      bool
	InheritService bool
	InheritEnv     bool
	InheritWindow  bool
	Offer          *chatOffer // kabul edilen öneri (nil = yok)
}

// freshChatState — SAF: TTL'i geçmiş durumun kapsamı düşer (kalıcı işaretler
// kalır). UpdatedAt 0 = yaşı bilinmiyor (Redis TTL'i yönetir) → taze.
func freshChatState(c ChatContext, now time.Time) (ChatContext, bool) {
	if c.UpdatedAt <= 0 || now.Sub(time.Unix(0, c.UpdatedAt)) <= chatStateTTL {
		return c, false
	}
	return ChatContext{CodeSticky: c.CodeSticky, Pending: c.Pending, Address: c.Address}, true
}

// resolveChatState — SAF çözücü.
func resolveChatState(in stateResolveIn) stateResolveOut {
	if in.Loc == nil {
		in.Loc = time.UTC
	}
	prev, stale := freshChatState(in.Prev, in.Now)
	out := stateResolveOut{Next: prev, Stale: stale}
	norm := normalizeGuidedMsg(strings.TrimSpace(in.Question))
	toks := guidedTokens(norm)
	out.Next.Address = resolveAddress(toks, prev.Address)

	// Öneri: çıplak kabul + taze öneri → yürüt; başka her soru öneriyi siler.
	if o := prev.Offer; o != nil && in.HasPrior && isOfferAccept(norm) && offerFresh(*o, in.Now) {
		acc := *o
		out.Offer = &acc
		out.Next.Offer = nil
		out.InheritService = prev.Service != ""
		out.InheritEnv = prev.Env != ""
		out.InheritWindow = prev.RangeExplicit && prev.RangeS > 0
		return out
	}
	out.Next.Offer = nil

	if w, ok := resolveTurnWindow(norm, toks, prev, in.Now, in.Loc); ok {
		out.Next.RangeS, out.Next.WinToMs, out.Next.WinLabel, out.Next.RangeExplicit = w.RangeS, w.ToMs, w.Label, true
		out.WindowSet = true
	}
	if ms := findEnvMentions(norm, in.Envs); len(ms) == 1 && len(ms[0].Values) == 1 {
		out.Next.Env = ms[0].Values[0]
		out.EnvSet = true
	}
	if svcs := explicitStateServices(in.Question, in.Services); len(svcs) > 0 {
		if svcs[0] != prev.Service {
			out.Next.Operation, out.Next.Trace = "", "" // eski servisin öznesi yeni servise taşınmaz
		}
		out.Next.Service = svcs[0]
		out.Next.Services = nil
		if len(svcs) > 1 {
			out.Next.Services = svcs
		}
		out.ServiceSet = true
	}
	if v := codeAfter(chanCodeRe, in.Question); v != "" {
		out.Next.ChannelCode = v
	}
	if v := codeAfter(funcCodeRe, in.Question); v != "" {
		out.Next.FunctionCode = v
	}
	if v := codeAfter(problemRefRe, in.Question); v != "" && chatProblemIDRe.MatchString(v) {
		out.Next.Problem = v
	}
	follow := in.HasPrior && utf8.RuneCountInString(norm) <= followUpMaxRunes && !wantsFleetScope(toks)
	out.InheritService = follow && !out.ServiceSet && prev.Service != ""
	out.InheritEnv = follow && !out.EnvSet && prev.Env != ""
	out.InheritWindow = follow && !out.WindowSet && prev.RangeExplicit && prev.RangeS > 0
	return out
}

// ── öneri ─────────────────────────────────────────────────────────────────

// offerAcceptPhrases — çıplak kabul (normalize, noktalama atılmış). Kapalı
// liste: "evet ama hataları" gibi içerik taşıyan mesaj öneri DEĞİLDİR.
var offerAcceptPhrases = map[string]bool{
	"bak": true, "bakalım": true, "bakalim": true, "bakın": true, "bakin": true,
	"bakar mısın": true, "bakar misin": true, "bakar mısınız": true, "bakar misiniz": true,
	"bakabilir misin": true, "bakabilir misiniz": true,
	"evet": true, "evet bak": true, "evet bakın": true, "evet lütfen": true, "lütfen": true, "lütfen bak": true,
	"devam": true, "devam et": true, "devam edin": true, "olur": true, "olur bak": true,
	"tamam": true, "tamam bak": true, "hadi": true, "hadi bak": true, "peki bak": true,
	"göster": true, "göster bakalım": true, "getir": true, "yap": true, "ok": true, "okey": true,
}

func isOfferAccept(norm string) bool {
	t := strings.Join(strings.Fields(strings.TrimFunc(norm, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})), " ")
	return offerAcceptPhrases[t]
}

func offerFresh(o chatOffer, now time.Time) bool {
	return o.At > 0 && now.Sub(time.UnixMilli(o.At)) <= chatOfferTTL && offerKinds[o.Kind] != ""
}

// ── pencere ───────────────────────────────────────────────────────────────

// dayPart — gün dilimi [başlangıç, bitiş) saat; bitiş 24'ü geçebilir.
type dayPart struct {
	from, to   int
	today, ago string // etiket: bugün / dün
}

var dayParts = map[string]dayPart{
	"sabah":   {6, 12, "bu sabah", "dün sabah"},
	"öğleden": {12, 18, "bugün öğleden sonra", "dün öğleden sonra"},
	"akşam":   {18, 24, "bu akşam", "dün akşam"},
	"gece":    {0, 6, "bu gece", "dün gece"},
}

// dayPartOf — tokenlerdeki gün dilimi anahtarı ("" = yok).
func dayPartOf(toks []string) string {
	for _, t := range toks {
		switch {
		case strings.HasPrefix(t, "sabah"):
			return "sabah"
		case strings.HasPrefix(t, "öğleden"), strings.HasPrefix(t, "ögleden"), t == "öğlen", t == "öğleden":
			return "öğleden"
		case strings.HasPrefix(t, "akşam"), strings.HasPrefix(t, "aksam"):
			return "akşam"
		case t == "gece", t == "geceki", t == "gecesi":
			return "gece"
		}
	}
	return ""
}

func hasTok(toks []string, words ...string) bool {
	for _, t := range toks {
		for _, w := range words {
			if t == w {
				return true
			}
		}
	}
	return false
}

func dayStart(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, loc)
}

// windowFrom — SAF: [from, to) → stateWindow; gelecek uç şimdiye kırpılır.
// relative: pencere şimdiye göreli tutulur (ToMs 0).
func windowFrom(from, to, now time.Time, label string, relative bool) (stateWindow, bool) {
	if to.After(now) {
		to = now
	}
	if !to.After(from) {
		return stateWindow{}, false
	}
	w := stateWindow{RangeS: int64(to.Sub(from) / time.Second), Label: label}
	if w.RangeS < 60 {
		w.RangeS = 60
	}
	if !relative {
		w.ToMs = to.UnixMilli()
	}
	return w, true
}

func clockSpan(from, to time.Time, loc *time.Location) string {
	return from.In(loc).Format("15:04") + "–" + to.In(loc).Format("15:04")
}

// resolveTurnWindow — SAF: sorunun açık penceresi.
func resolveTurnWindow(norm string, toks []string, prev ChatContext, now time.Time, loc *time.Location) (stateWindow, bool) {
	today := dayStart(now, loc)
	yesterday := hasTok(toks, "dün", "dünkü", "dunku", "dun")
	if yesterday && (strings.Contains(norm, "aynı saat") || strings.Contains(norm, "ayni saat") || strings.Contains(norm, "bu saat")) {
		length := time.Duration(prev.RangeS) * time.Second
		if length <= 0 {
			length = time.Hour
		}
		end := now
		if prev.WinToMs > 0 {
			end = time.UnixMilli(prev.WinToMs)
		}
		end = end.Add(-24 * time.Hour)
		return windowFrom(end.Add(-length), end, now, "dün aynı saatte ("+clockSpan(end.Add(-length), end, loc)+")", false)
	}
	if strings.Contains(norm, "geçen hafta") || strings.Contains(norm, "gecen hafta") {
		monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		return windowFrom(monday.AddDate(0, 0, -7), monday, now, "geçen hafta", false)
	}
	if strings.Contains(norm, "bu hafta") {
		monday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
		return windowFrom(monday, now, now, "bu hafta", true)
	}
	if wins, ok := extractAbsoluteWindows(norm, now, loc); ok && len(wins) == 1 && looksLikeAbsoluteWindow(norm) {
		w := wins[0]
		if yesterday && !w.From.Before(today) {
			w.From, w.To = w.From.Add(-24*time.Hour), w.To.Add(-24*time.Hour)
		}
		return windowFrom(w.From, w.To, now, absWindowLabel(w, loc), false)
	}
	if key := dayPartOf(toks); key != "" {
		p := dayParts[key]
		base, label := today, p.today
		if yesterday {
			base, label = today.AddDate(0, 0, -1), p.ago
		}
		from, to := base.Add(time.Duration(p.from)*time.Hour), base.Add(time.Duration(p.to)*time.Hour)
		if key == "gece" && yesterday { // "dün gece" = dün 22:00 → bugün 06:00
			from, to = today.Add(-2*time.Hour), today.Add(6*time.Hour)
		}
		if !yesterday && from.After(now) { // gece 03'te "sabah" → dünün sabahı
			from, to, label = from.AddDate(0, 0, -1), to.AddDate(0, 0, -1), p.ago
		}
		return windowFrom(from, to, now, label+" ("+clockSpan(from, to, loc)+")", false)
	}
	if yesterday {
		// Çıplak "dün": önceki pencere bugünün bir dilimiyse aynı dilim dün.
		if prev.WinToMs > 0 && prev.RangeS > 0 && prev.RangeS <= 12*3600 {
			pTo := time.UnixMilli(prev.WinToMs)
			pFrom := pTo.Add(-time.Duration(prev.RangeS) * time.Second)
			if !pFrom.Before(today) {
				from, to := pFrom.Add(-24*time.Hour), pTo.Add(-24*time.Hour)
				label := "dün"
				for _, p := range dayParts {
					if strings.HasPrefix(prev.WinLabel, p.today) {
						label = p.ago
					}
				}
				return windowFrom(from, to, now, label+" ("+clockSpan(from, to, loc)+")", false)
			}
		}
		return windowFrom(today.AddDate(0, 0, -1), today, now, "dün", false)
	}
	if hasTok(toks, "bugün", "bugun", "bugünkü") {
		return windowFrom(today, now, now, "bugün", true)
	}
	if m := guidedRangeRe.FindStringSubmatch(norm); m != nil {
		if rs, ok := guidedRangeSExplicit(norm); ok && rs > 0 {
			return stateWindow{RangeS: rs, Label: fmtRangeTR(rs)}, true
		}
	}
	return stateWindow{}, false
}

// ── varlık ve kodlar ──────────────────────────────────────────────────────

// explicitStateServices — soruda TAM adla anılan servisler, varlık
// çözücüsünden (mcptools.ResolveEntities; jeton tam eşi). Bulanık aday
// durumu değiştirmez.
func explicitStateServices(question string, services []string) []string {
	if len(services) == 0 {
		return nil
	}
	var out []string
	for _, c := range mcptools.ResolveEntities(question, mcptools.EntityCatalogIndex{Services: services}, 5) {
		if c.Kind == "service" && c.Score >= 100 && c.Match == "exact" {
			out = append(out, c.Name)
		}
	}
	return out
}

var (
	chanCodeRe   = regexp.MustCompile(`(?i)\bkanal(?:\s+kodu)?\s*[:=]?\s*([A-Za-z0-9_-]{1,16})`)
	funcCodeRe   = regexp.MustCompile(`(?i)\bfonksiyon(?:\s+kodu)?\s*[:=]?\s*([A-Za-z0-9_-]{1,16})`)
	problemRefRe = regexp.MustCompile(`(?i)\bproblem(?:\s+(?:id|no|kimliği))?\s*[:#=]?\s*([A-Za-z0-9][A-Za-z0-9:_.-]{1,79})`)
)

// codeAfter — SAF: önekten sonraki değer; rakam ya da (≥2 harfli) tamamen
// büyük harf taşımıyorsa yok ("kanal kodu nedir" kod değildir).
func codeAfter(re *regexp.Regexp, q string) string {
	m := re.FindStringSubmatch(q)
	if m == nil {
		return ""
	}
	v := m[1]
	hasDigit, allUpper := false, len(v) >= 2
	for _, r := range v {
		if unicode.IsDigit(r) {
			hasDigit = true
		}
		if unicode.IsLetter(r) && !unicode.IsUpper(r) {
			allUpper = false
		}
	}
	if !hasDigit && !allUpper {
		return ""
	}
	return v
}

// ── hitap ─────────────────────────────────────────────────────────────────

var sizSuffixes = []string{"mısınız", "misiniz", "musunuz", "müsünüz", "ınız", "iniz", "unuz", "ünüz", "yınız", "yiniz"}
var senSuffixes = []string{"mısın", "misin", "musun", "müsün"}

// resolveAddress — SAF: kullanıcının yazımından hitap. Siz işareti sen
// işaretinden önce okunur; işaret yoksa önceki, o da yoksa "sen".
func resolveAddress(toks []string, prev string) string {
	siz, sen := false, false
	for _, t := range toks {
		switch t {
		case "siz", "size", "sizin", "sizde", "sizden", "bakın", "bakin", "lütfen":
			if t != "lütfen" {
				siz = true
			}
			continue
		case "sen", "sana", "senin", "sende", "senden", "bak", "göster", "getir":
			sen = true
			continue
		}
		for _, s := range sizSuffixes {
			if strings.HasSuffix(t, s) && utf8.RuneCountInString(t) > utf8.RuneCountInString(s)+1 {
				siz = true
			}
		}
		for _, s := range senSuffixes {
			if strings.HasSuffix(t, s) {
				sen = true
			}
		}
	}
	switch {
	case siz:
		return addressSiz
	case sen:
		return addressSen
	case prev != "":
		return prev
	}
	return addressSen
}
