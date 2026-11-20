package api

// chat_source_ask_test.go — v0.10.2381 (operatör, 2026-11-19: "kafası
// karışırsa kullanıcıya sorsun: wiki mi, envanter mi, yoksa telemetri mi").
// TEK kaynak sorusunun SAF çekirdeği. Sözleşme:
//   - seçenek kümesi yalnız bu kullanıcı / kurulum için VAR olan kaynaklar;
//     tek seçenek kalırsa soru yok;
//   - soru metni tek kurucudan (dört eski soru aynı cümleyi kurar);
//   - cevap yorumlayıcısı serbest metni (Türkçe ekler, yazım hataları)
//     sunulan seçeneklerden birine eşler; tanınmayan cevap "" (soru düşer);
//   - "kanıt yok + biçim belirsiz" sorusu DAR bir koşuldur (fazla sormak hata);
//   - seçim 30 dk hatırlanır, yalnız hâlâ sunulan bir kaynaksa uygulanır.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/copilot"
)

// sourceAskHeadTR — sorunun her seçenek kümesinde ortak başı (akış testleri
// seçenek kümesinden bağımsız bununla eşler).
const sourceAskHeadTR = "Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler)"

func TestSourceAskOptions(t *testing.T) {
	for _, c := range []struct {
		name string
		a    sourceAvail
		want string
	}{
		{"hepsi", sourceAvail{Wiki: true, Inventory: true, General: true}, "telemetri,wiki,excel,genel"},
		{"wiki kapalı", sourceAvail{Inventory: true, General: true}, "telemetri,excel,genel"},
		{"envanter yüklenmemiş", sourceAvail{Wiki: true, General: true}, "telemetri,wiki,genel"},
		{"genel sorular kapalı", sourceAvail{Wiki: true, Inventory: true}, "telemetri,wiki,excel"},
		{"yalnız telemetri", sourceAvail{}, "telemetri"},
		{"exceptions adayı", sourceAvail{Wiki: true, Exceptions: true}, "telemetri,wiki,exceptions"},
	} {
		if got := strings.Join(sourceAskOptions(c.a), ","); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

func TestSourceAskText(t *testing.T) {
	all := []string{srcTelemetry, srcWiki, srcExcel, srcGeneral}
	const full = "Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), wikide, envanterde; yoksa genel bilgi olarak mı cevaplayayım?"
	for _, c := range []struct {
		name string
		lead string
		opts []string
		want string
	}{
		{"dört seçenek", "", all, full},
		{"sıra girdiden bağımsız", "", []string{srcGeneral, srcExcel, srcWiki, srcTelemetry}, full},
		{"genel kapalı", "", []string{srcTelemetry, srcWiki, srcExcel},
			"Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), wikide, yoksa envanterde mi?"},
		{"iki kaynak", "", []string{srcTelemetry, srcWiki},
			"Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), yoksa wikide mi?"},
		{"tek kaynak + genel", "", []string{srcTelemetry, srcGeneral},
			"Bunu telemetride (servis sağlığı, hatalar, trace'ler) mi arayayım, yoksa genel bilgi olarak mı cevaplayayım?"},
		{"exceptions adayı", "", []string{srcTelemetry, srcExceptions},
			"Bunu nerede arayayım: telemetride (servis sağlığı, hatalar, trace'ler), yoksa Exceptions gruplarında mı?"},
		{"bağlam cümlesi öne", "Bu soru yüklü envanter tablosunda 3 satırla eşleşiyor.", all,
			"Bu soru yüklü envanter tablosunda 3 satırla eşleşiyor. " + full},
		{"tek seçenek → soru yok", "", []string{srcTelemetry}, ""},
		{"tek seçenek + bağlam → soru yok", "x", []string{srcGeneral}, ""},
		{"boş", "", nil, ""},
		{"bilinmeyen tür sayılmaz", "", []string{srcTelemetry, "nope"}, ""},
	} {
		if got := sourceAskText(c.lead, c.opts); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestSourceLeads(t *testing.T) {
	if got := sourceLeadRowsTR(3, []string{"payments-core", "srvpay01", "x"}); got != "Bu soru yüklü envanter tablosunda 3 satırla eşleşiyor (payments-core, srvpay01)." {
		t.Errorf("satır bağlamı: %q", got)
	}
	if got := sourceLeadRowsTR(1, nil); got != "Bu soru yüklü envanter tablosunda 1 satırla eşleşiyor." {
		t.Errorf("değersiz satır bağlamı: %q", got)
	}
	if got := sourceLeadEntityTR(bareEntityTeam, "Payments-Core"); got != "“Payments-Core” bir takım adı; yüklü envanterde de geçiyor." {
		t.Errorf("takım bağlamı: %q", got)
	}
	if got := sourceLeadEntityTR(bareEntityService, "order-api"); got != "“order-api” bir servis adı; yüklü envanterde de geçiyor." {
		t.Errorf("servis bağlamı: %q", got)
	}
	tie := sourceLeadTieTR([]srcCand{{Kind: srcExcel, Label: "16 satır"}, {Kind: srcWiki, Label: "Platform sayfası"}})
	if tie != "Bu soru için birden çok kaynakta güçlü kanıt buldum: **Yüklenen tablolar** (16 satır), **Wiki** (Platform sayfası)." {
		t.Errorf("başa baş bağlamı: %q", tie)
	}
}

// Kart etiketleri serbest metin olarak yazılsa da aynı seçeneğe düşer.
func TestSourceOptionLabelsClassifyToThemselves(t *testing.T) {
	all := []string{srcTelemetry, srcWiki, srcExcel, srcExceptions, srcGeneral}
	for _, k := range all {
		label := sourceOptionLabelTR(k)
		if label == "" {
			t.Errorf("%q etiketsiz", k)
		}
		if got := classifySourceChoice(label, all); got != k {
			t.Errorf("etiket %q → %q, beklenen %q", label, got, k)
		}
	}
}

func TestClassifySourceChoice(t *testing.T) {
	all := []string{srcTelemetry, srcWiki, srcExcel, srcGeneral}
	for _, c := range []struct {
		in   string
		opts []string
		want string
	}{
		// wiki
		{"wiki", all, srcWiki},
		{"Wiki", all, srcWiki},
		{"wikide ara", all, srcWiki},
		{"wiki'de bak", all, srcWiki},
		{"önce wiki lütfen", all, srcWiki},
		{"viki", all, srcWiki},
		{"vikiden bak", all, srcWiki},
		{"runbook", all, srcWiki},
		{"dokümanda", all, srcWiki},
		{"wiki dokümanları", all, srcWiki},
		// envanter
		{"envanter", all, srcExcel},
		{"envanterden bak", all, srcExcel},
		{"Envanterden göster", all, srcExcel},
		{"tabloda bak", all, srcExcel},
		{"excel", all, srcExcel},
		{"yüklü dokümanlarda ara", all, srcExcel},
		{"envaterden", all, srcExcel}, // yazım hatası (harf eksik)
		{"envantere bak", all, srcExcel},
		{"envnater", all, ""}, // iki hata: tanınmaz
		// telemetri
		{"telemetri", all, srcTelemetry},
		{"telemetriden", all, srcTelemetry},
		{"telemtri", all, srcTelemetry}, // yazım hatası
		{"coremetry", all, srcTelemetry},
		{"Coremetry'de", all, srcTelemetry},
		{"Coremetry verisinde", all, srcTelemetry},
		{"canlı veride ara", all, srcTelemetry},
		{"servislerde", all, srcTelemetry},
		{"takımın servislerini getir", all, srcTelemetry},
		{"servisin durumunu", all, srcTelemetry},
		// genel
		{"genel", all, srcGeneral},
		{"Genel bilgi", all, srcGeneral},
		{"genel bilgi olarak cevapla", all, srcGeneral},
		{"genel bilgiyle", all, srcGeneral},
		// exceptions yalnız sunulduysa; sunulmadıysa telemetriye düşer
		{"exceptions", []string{srcTelemetry, srcExceptions}, srcExceptions},
		{"exceptions", all, srcTelemetry},
		// sunulmayan kaynak: doküman sözcüğü kalan seçeneğe düşer, ad ise tanınmaz
		{"dokümanlarda", []string{srcTelemetry, srcExcel}, srcExcel},
		{"wiki", []string{srcTelemetry, srcExcel}, srcExcel},
		{"wiki", []string{srcTelemetry, srcGeneral}, ""},
		{"envanter", []string{srcTelemetry, srcWiki}, ""},
		{"genel", []string{srcTelemetry, srcWiki}, ""},
		// cevap değil
		{"namespace nasıl oluşturulur", all, ""},
		{"orders-api sağlığı nasıl?", all, ""},
		{"checkout sağlığı nasıl?", all, ""},
		{"hayır başka bir şey", all, ""},
		{"wiki ve coremetry", all, ""},
		{"takım dokümanı", all, ""}, // iki kaynak
		{"evet", all, ""},           // yalnız dolgu
		{"tamam", all, ""},
		{"örnek verir misin", all, ""},
		{"bunu wiki ve coremetry ikisinde de bir ara bakalım", all, ""},
		{"", all, ""},
		{"wiki", nil, ""},
	} {
		if got := classifySourceChoice(c.in, c.opts); got != c.want {
			t.Errorf("classifySourceChoice(%q, %v) = %q, beklenen %q", c.in, c.opts, got, c.want)
		}
	}
}

func TestRememberedSource(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(age time.Duration, kind string) *chatSourceChoice {
		return &chatSourceChoice{Kind: kind, At: now.Add(-age).UnixNano()}
	}
	all := []string{srcTelemetry, srcWiki, srcExcel, srcGeneral}
	for _, c := range []struct {
		name string
		ch   *chatSourceChoice
		opts []string
		want string
	}{
		{"taze", at(time.Minute, srcWiki), all, srcWiki},
		{"tam 30 dk", at(sourceChoiceTTL, srcWiki), all, srcWiki},
		{"30 dk + 1 ns", at(sourceChoiceTTL+time.Nanosecond, srcWiki), all, ""},
		{"saat kayması 30 sn ileri", at(-30*time.Second, srcExcel), all, srcExcel},
		{"saat kayması 2 dk ileri", at(-2*time.Minute, srcExcel), all, ""},
		{"artık sunulmayan kaynak", at(time.Minute, srcWiki), []string{srcTelemetry, srcGeneral}, ""},
		{"seçim yok", nil, all, ""},
		{"zamansız", &chatSourceChoice{Kind: srcWiki}, all, ""},
		{"türsüz", &chatSourceChoice{At: now.UnixNano()}, all, ""},
	} {
		if got := rememberedSource(c.ch, now, c.opts); got != c.want {
			t.Errorf("%s: %q, beklenen %q", c.name, got, c.want)
		}
	}
}

// Hatırlanan seçim, başka bir kaynak AÇIKÇA öndeyse (güçlü ve farklı) biter.
func TestSourceChoiceSurvivesWinner(t *testing.T) {
	for _, c := range []struct {
		choice, winner string
		score          float64
		want           bool
	}{
		{srcWiki, srcWiki, 0.9, true},
		{srcWiki, srcTelemetry, 0.9, false},
		{srcWiki, srcTelemetry, 0.5, true}, // zayıf sinyal "açıkça önde" değil
		{srcExcel, srcExceptions, 0.85, false},
		{srcTelemetry, srcExceptions, 0.85, true}, // Exceptions telemetrinin parçası
		{"", srcTelemetry, 0.9, true},
	} {
		if got := sourceChoiceSurvives(c.choice, c.winner, c.score); got != c.want {
			t.Errorf("seçim %q, kazanan %q (%.2f): %v, beklenen %v", c.choice, c.winner, c.score, got, c.want)
		}
	}
}

func TestSourceQuestionShape(t *testing.T) {
	for _, c := range []struct {
		q     string
		prior bool
		want  string
	}{
		// genel bilgi biçimi
		{"OpenTelemetry'de span link nedir?", false, shapeTelemetry}, // "span" telemetri sözlüğü: yine sorulmaz
		{"Kubernetes HPA nedir", false, shapeGeneral},
		{"java 25 kod örneği virtual thread kullansın", false, shapeTelemetry}, // "thread" kaynak sözlüğü: yine sorulmaz
		{"java 25 kod örneği ver", false, shapeGeneral},
		{"python ile palindrom fonksiyonu yaz", false, shapeGeneral},
		{"iki yaklaşımın farkı ne", false, shapeGeneral},
		{"what is a circuit breaker", false, shapeGeneral},
		// telemetri biçimi
		{"orders-api neden yavaş?", false, shapeTelemetry},
		{"checkout hata oranı", false, shapeTelemetry},
		{"son deploy ne zaman", false, shapeTelemetry},
		// selam / küçük sohbet / CoSRE'nin kendisi
		{"merhaba", false, shapeSmallTalk},
		{"teşekkürler tamam", false, shapeSmallTalk},
		{"sen kimsin", false, shapeSmallTalk},
		{"neler yapabilirsin", false, shapeSmallTalk},
		{"cosre hangi modeli kullanıyor", false, shapeSmallTalk},
		// tek sözcük / uzun / kod
		{"PlatformTeam", false, shapeShort},
		{"", false, shapeShort},
		{strings.Repeat("kelime ", 13), false, shapeLong},
		{"şunu düzelt\n```go\nx := 1\n```", false, shapeLong},
		// konuşmaya yaslanan devam sorusu (önceki tur varsa)
		{"peki ya diğeri", true, shapeFollowUp},
		{"bunun sorumlusu kim", true, shapeFollowUp},
		{"bunun sorumlusu kim", false, shapeUnclear}, // ilk turda yaslanacak konuşma yok
		// belirsiz: kısa, kuruma özgü görünen arama
		{"PlatformTeam kimde", false, shapeUnclear},
		{"checkout sorumlusu", false, shapeUnclear},
		{"yeni ortam bilgileri", false, shapeUnclear},
	} {
		if got := sourceQuestionShape(c.q, c.prior); got != c.want {
			t.Errorf("sourceQuestionShape(%q, prior=%v) = %q, beklenen %q", c.q, c.prior, got, c.want)
		}
	}
}

// Kanıtsız + biçimi belirsiz soru: yalnız bütün koşullar birlikteyse sorulur.
func TestShouldAskUnknownSource(t *testing.T) {
	ok := unknownSourceSignals{Allowed: true, DiscoveryRan: true}
	const q = "PlatformTeam kimde"
	if !shouldAskUnknownSource(q, ok) {
		t.Fatal("kanıt yok + biçim belirsiz + ana sohbet → sorulmalı")
	}
	with := func(f func(*unknownSourceSignals)) unknownSourceSignals { g := ok; f(&g); return g }
	for _, c := range []struct {
		name string
		q    string
		g    unknownSourceSignals
	}{
		{"panel / çekmece / trace bağlamı", q, with(func(g *unknownSourceSignals) { g.Allowed = false })},
		{"kaynak zorlanmış ya da adıyla söylenmiş", q, with(func(g *unknownSourceSignals) { g.Forced = true })},
		{"keşif koşmadı (ayar kapalı)", q, with(func(g *unknownSourceSignals) { g.DiscoveryRan = false })},
		{"bir kaynakta kanıt var", q, with(func(g *unknownSourceSignals) { g.Candidates = 1 })},
		{"sınıflandırıcı bir niyete oturttu", q, with(func(g *unknownSourceSignals) { g.ClassifierMatched = true })},
		{"off_topic vetosu (telemetri adı / kimlik)", q, with(func(g *unknownSourceSignals) { g.Vetoed = true })},
		{"konuşmanın öznesi var", q, with(func(g *unknownSourceSignals) { g.HasSubject = true })},
		{"genel bilgi biçimi", "Kubernetes HPA nedir", ok},
		{"açık genel soru", "OpenTelemetry'de span link nedir?", ok},
		{"telemetri biçimi", "orders-api neden yavaş?", ok},
		{"selam", "merhaba nasılsın", ok},
		{"CoSRE'nin kendisi", "neler yapabilirsin", ok},
		{"tek sözcük", "PlatformTeam", ok},
		{"uzun serbest metin", strings.Repeat("kelime ", 20), ok},
		{"devam sorusu", "peki ya diğeri", with(func(g *unknownSourceSignals) { g.HasPrior = true })},
	} {
		if shouldAskUnknownSource(c.q, c.g) {
			t.Errorf("%s: %q sorulmamalı", c.name, c.q)
		}
	}
}

// Kullanıcı kaynağı adıyla söylediyse soru sorulmaz.
func TestNamedSourceKind(t *testing.T) {
	for q, want := range map[string]string{
		"wikide orders deploy adımları":            srcWiki,
		"Atlas wikisine bak":                       srcWiki,
		"vikide nasıl anlatılmış":                  srcWiki,
		"envanterde PlatformTeam sunucuları":       srcExcel,
		"PlatformTeam envanterindeki sunucular":    srcExcel,
		"envanter'den checkout sunucularını getir": srcExcel,
		"excel'de PlatformTeam":                    srcExcel,
		"telemetride orders-api":                   srcTelemetry,
		"telemetriden bak checkout":                srcTelemetry,
		"telemetri'de checkout hataları":           srcTelemetry,
		// ad geçiyor ama kaynak GÖSTERİLMİYOR
		"envanter nedir":                  "",
		"excel export nasıl alınır":       "",
		"OpenTelemetry nedir":             "",
		"telemetri verisi nasıl toplanır": "",
		"orders tablosunda kilit var mı":  "",
		"checkout neden yavaş":            "",
		"":                                "",
	} {
		if got := namedSourceKind(q); got != want {
			t.Errorf("namedSourceKind(%q) = %q, beklenen %q", q, got, want)
		}
	}
}

// Genel cevabın devamı: telemetri sinyali, varlık ya da kaynak işareti yoksa
// genel sohbet sürer.
func TestGeneralFollowUpContinues(t *testing.T) {
	svcs, envs, teams := []string{"orders-api", "checkout"}, []string{"prod", "uat"}, []string{"PlatformTeam"}
	for q, want := range map[string]bool{
		"peki örnek verir misin":  true,
		"örnek verir misin":       true,
		"bunun farkı ne":          true,
		"daha kısa anlatır mısın": true,
		"aynısını Go ile yaz":     true,
		"neden böyle":             true,
		// telemetri sinyali
		"peki hata oranı ne":       false,
		"son deploy ne zaman":      false,
		"trace'lerde görünüyor mu": false,
		// varlık
		"checkout için de geçerli mi":      false,
		"prod ortamında nasıl":             false,
		"PlatformTeam bunu kullanıyor mu":  false,
		"4bf92f3577b34da6a3ce929d0e0e4736": false,
		// kaynak işareti
		"wikide var mı":                    false,
		"envanterde var mı":                false,
		"telemetride görünüyor mu":         false,
		"NullPointerException da böyle mi": false,
		"":                                 false,
	} {
		if got := generalFollowUpContinues(q, svcs, envs, teams); got != want {
			t.Errorf("generalFollowUpContinues(%q) = %v, beklenen %v", q, got, want)
		}
	}
}

// Çip komutunun "önceki soru"su kaynak seçimi cevabını ("wiki") atlar.
func TestPreviousUserQuestionSkipsSourceReply(t *testing.T) {
	msgs := []copilot.ChatMessage{
		{Role: "user", Text: "PlatformTeam kimde"},
		{Role: "assistant", Text: sourceAskText("", []string{srcTelemetry, srcWiki, srcGeneral})},
		{Role: "user", Text: "wiki"},
		{Role: "assistant", Text: "Wikide bulunamadı."},
		{Role: "user", Text: "/genel"},
	}
	if got := previousUserQuestion(msgs); got != "PlatformTeam kimde" {
		t.Fatalf("önceki soru: %q", got)
	}
	// Kaynak sorusunun ardından GELMEYEN kısa telemetri sorusu ("hata logları"
	// kaynak sözcüklerinden oluşur) soru sayılmaya devam eder.
	plain := []copilot.ChatMessage{
		{Role: "user", Text: "hata logları"},
		{Role: "assistant", Text: "Son 1 saatte 3 hata logu var."},
		{Role: "user", Text: "/wiki"},
	}
	if got := previousUserQuestion(plain); got != "hata logları" {
		t.Fatalf("kısa telemetri sorusu atlanmamalı: %q", got)
	}
	// Her seçenek kümesinin sorusu tanınır; başka metin tanınmaz.
	for _, opts := range [][]string{{srcTelemetry, srcWiki}, {srcTelemetry, srcGeneral}, {srcTelemetry, srcWiki, srcExcel, srcGeneral}, {srcTelemetry, srcExceptions}} {
		if !isSourceAskText(sourceAskText("Bağlam cümlesi.", opts)) {
			t.Errorf("kaynak sorusu tanınmadı: %v", opts)
		}
	}
	if isSourceAskText("Wikide bulunamadı.") || isSourceAskText("") {
		t.Error("kaynak sorusu olmayan metin tanındı")
	}
}

func TestSourceAltChips(t *testing.T) {
	got := sourceAltChips([]string{srcTelemetry, srcWiki, srcExcel, srcGeneral}, srcWiki, map[string]string{srcExcel: "16 satır"})
	if strings.Join(got, "|") != "/telemetri|/excel · 16 satır|/genel" {
		t.Fatalf("alternatif çipleri: %v", got)
	}
	if got := sourceAltChips([]string{srcWiki}, srcWiki, nil); len(got) != 0 {
		t.Fatalf("tek seçenek: %v", got)
	}
}

func TestPendingSourceOptions(t *testing.T) {
	// Eski sürümün yazdığı (seçeneksiz) bekleyen soru: iki seçenekli eski küme.
	if got := strings.Join(pendingSourceOptions(&chatPendingAsk{Kind: pendingKindSource}), ","); got != "telemetri,wiki" {
		t.Errorf("eski bekleyen soru: %q", got)
	}
	if got := strings.Join(pendingSourceOptions(&chatPendingAsk{Kind: pendingKindSource, Options: []string{srcExcel, srcGeneral}}), ","); got != "excel,genel" {
		t.Errorf("seçenekli bekleyen soru: %q", got)
	}
	if got := pendingSourceOptions(&chatPendingAsk{Kind: pendingKindTraceSynth}); got != nil {
		t.Errorf("başka tür: %v", got)
	}
}
