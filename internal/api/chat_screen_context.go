package api

import (
	"fmt"
	"strings"
	"time"

	agentctx "github.com/cilcenk/coremetry/internal/ai/agent/context"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// chat_screen_context.go — serbest tool döngüsüne EKRAN BAĞLAMI
// (v0.10.32, Copilot denetiminin #1 sıradaki sınırı).
//
// ── KUSUR ───────────────────────────────────────────────────────────────
//
// Dört kademenin ilk üçü ekran bağlamını alıyordu (servis, operation,
// aralık, env, trace); serbest tool döngüsü HİÇBİRİNİ almıyordu:
//
//	loopPrompt := withAddressee(addressee, copilot.SystemPromptChat())
//
// `req.Context.*` guided/drawer/RAG'e ve link kurucusuna gidiyor, ama
// döngünün prompt'una asla. Üstelik prompt kendi varsayılanını dayatıyor:
//
//	"Zaman penceresi: … Operatör aksini söylemedikçe 1800 (30 dk) kullan."
//
// Sonuç: ekranda `checkout-service` açık ve 6 saatlik pencere seçiliyken
// sorulan "hata oranı ne" sorusu FİLO GENELİNE ve 30 DAKİKAYA gidiyor.
// Cevap makul görünüyor, kaynağı doğru, sayılar gerçek — yalnız SORULAN
// ŞEY DEĞİL. Ve cevapta bunu belirten hiçbir şey yok.
//
// ⚠ Bu, kademelerin en kötü yerinde: guided'ın ıskaladığı sorular buraya
// düşüyor, yani en ZOR ve en serbest sorularda model en az bağlama sahip.
//
// ── NEDEN SUNUCUDA KURULUYOR ────────────────────────────────────────────
//
// Modelden bağlamı "çıkarmasını" istemek (ör. konuşmadan servis adı
// tahmin etmesi) küçük modelde güvenilmez ve uydurma yüzeyi açar. Önsöz
// deterministik: ne verildiyse o yazılıyor, verilmeyen alan HİÇ
// yazılmıyor. Boş bir alanı "(bilinmiyor)" diye yazmak modele
// doldurulacak bir boşluk sunardı.
//
// ── NEDEN "VARSAYILAN" ──────────────────────────────────────────────────
//
// Operatör tek servise bakarken pekâlâ "hangi servisler yavaş" diye
// filo sorusu sorabilir. Bağlam bir VARSAYILAN, kelepçe değil. v0.10.1324'e
// dek bu, BÜYÜK HARFLİ bir emirdi ("Soru AKSİNİ SÖYLEMEDİKÇE …"); kapsamı
// artık sunucu uyguladığı için yalnız olgu kalır (screenScopeLineTR).

// screenScopeLineTR — v0.10.1324: ekran önsözünün TEK kapsam satırı. Olgusal,
// emir değil; sayfa önsözü (agentctx.PreambleTR) artık kendi satırını yazmaz.
const screenScopeLineTR = "Bu kapsam varsayılandır; varsa açık sayfanın cluster/namespace/filtre boyutlarını sunucu uygulamaz."

// ChatScreenContext — istekle gelen ekran bağlamı.
type ChatScreenContext struct {
	Service   string
	Operation string
	Env       string
	RangeS    int64
	// AnchorTo / Anchored (v0.10.33) — pencerenin BİTİŞ anı ve onun
	// operatörün MUTLAK seçiminden mi geldiği. Anchored=false iken
	// AnchorTo yalnız "şimdi"dir ve ilan edilmez: göreli bir pencereyi
	// mutlakmış gibi yazmak yeni bir yanlış olurdu.
	AnchorTo time.Time
	Anchored bool
}

// Empty — ilan edilecek hiçbir şey yok mu.
func (c ChatScreenContext) Empty() bool {
	return strings.TrimSpace(c.Service) == "" &&
		strings.TrimSpace(c.Operation) == "" &&
		strings.TrimSpace(c.Env) == "" &&
		c.RangeS <= 0
}

// freeLoopScreenService — v0.10.944: trace çekmecesi odaklı span'in servisini
// YALNIZ page.service'te gönderir (context.service bilerek boş — guided
// yönlendirme değişmesin) ve PreambleTR(full=false) sayfa servisini yazmaz;
// düşüm olmadan "Bağlam" şeridinde görünen servis modele hiç ulaşmıyordu.
// Pin varsa boş servis "kapsamsız" demektir, ekrandan doldurulmaz
// (CopilotChat.tsx pinnedLegacy). SAF.
func freeLoopScreenService(ctxSvc string, page, pinned *agentctx.PageContext) string {
	if s := strings.TrimSpace(ctxSvc); s != "" {
		return s
	}
	if pinned != nil || page == nil {
		return ""
	}
	return strings.TrimSpace(page.Service)
}

// screenContextPreambleTR — döngü prompt'unun başına eklenen önsöz.
//
// Boş bağlamda BOŞ dize döner ve prompt'un kendi 1800 varsayılanı
// yürürlükte kalır — uydurma bir bağlam eklemek, hiç eklememekten kötü.
func screenContextPreambleTR(c ChatScreenContext) string {
	if c.Empty() {
		return ""
	}
	// v0.10.1274 — KISALDI: kapsamı artık SUNUCU araç argümanlarına koyuyor
	// (chat_tool_scope.go applyScreenContext); önsöz yalnız ne olduğunu ve
	// farklı kapsamın nasıl isteneceğini söyler (küçük model: az talimat).
	var b strings.Builder
	b.WriteString("EKRAN BAĞLAMI (operatör şu an buna bakıyor; sunucu bunu argüman vermediğin araçlara kendisi uygular):\n")
	if s := strings.TrimSpace(c.Service); s != "" {
		fmt.Fprintf(&b, "- servis: %s\n", s)
	}
	if o := strings.TrimSpace(c.Operation); o != "" {
		fmt.Fprintf(&b, "- operation: %s\n", o)
	}
	if e := strings.TrimSpace(c.Env); e != "" {
		fmt.Fprintf(&b, "- ortam: %s\n", e)
	}
	if c.RangeS > 0 {
		// ⚠ Prompt'taki "aksini söylemedikçe 1800" kuralını EZER (küçük model
		// çelişkide ilk gördüğünü izler).
		fmt.Fprintf(&b, "- zaman aralığı: %s (range_s=%d; 1800 YERİNE bu)\n", fmtRangeTR(c.RangeS), c.RangeS)
	}
	if c.Anchored {
		// ⚠ v0.10.33 — MUTLAK PENCERE: cevap "şimdi"yi değil o pencereyi anlatır.
		fmt.Fprintf(&b, "- ⚠ pencere GEÇMİŞE sabitlenmiş: %s'de BİTİYOR, "+
			"\"şimdi\" DEĞİL. Cevabı o ana göre yaz.\n",
			c.AnchorTo.UTC().Format("2006-01-02 15:04 UTC"))
	}
	// v0.10.1324 (operatör-bildirimi) — eski "Soru AKSİNİ SÖYLEMEDİKÇE …
	// argümanda açıkça yaz" emri kalktı: kapsamı v0.10.1274'ten beri SUNUCU
	// uyguluyor (chat_tool_scope.go) ve sayfa/sohbet önsözlerindeki iki ikiz
	// emirle birlikte gemma4 31b o cümleleri cevaba yankılayıp döngüye
	// giriyordu. Tek, kısa, OLGUSAL satır — sayfa önsözünün emri de buraya
	// katlandı (sayfa boyutları sunucuca uygulanmaz).
	b.WriteString(screenScopeLineTR + "\n\n")
	return b.String()
}

// screenContextChipTR — operatöre GÖRÜNEN kısa özet.
//
// Bağlam sessizce uygulanırsa operatör cevabın neden o kapsamda
// olduğunu bilemez — v0.9.1259'da env için şeffaflık eklenmişti, aralık
// ve servis için eklenmemişti. Çip o boşluğu kapatıyor.
func screenContextChipTR(c ChatScreenContext) string {
	if c.Empty() {
		return ""
	}
	var parts []string
	if s := strings.TrimSpace(c.Service); s != "" {
		parts = append(parts, s)
	}
	if o := strings.TrimSpace(c.Operation); o != "" {
		parts = append(parts, o)
	}
	if e := strings.TrimSpace(c.Env); e != "" {
		parts = append(parts, "env="+e)
	}
	if c.RangeS > 0 {
		if c.Anchored { // v0.10.2480 (E) — geçmişte biten pencere "son N" değil
			parts = append(parts, pastWindowLabelTR(c.RangeS, c.AnchorTo.UnixMilli(), time.Now(), tzdefault.Location()))
		} else {
			parts = append(parts, fmtRangeTR(c.RangeS))
		}
	}
	return "ekran bağlamı: " + strings.Join(parts, " · ")
}

// fmtRangeTR — saniyeyi okunur pencereye çevirir.
func fmtRangeTR(s int64) string {
	switch {
	case s >= 86400 && s%86400 == 0:
		return fmt.Sprintf("son %d gün", s/86400)
	case s >= 3600 && s%3600 == 0:
		return fmt.Sprintf("son %d saat", s/3600)
	case s >= 60:
		return fmt.Sprintf("son %d dakika", s/60)
	default:
		return fmt.Sprintf("son %d saniye", s)
	}
}
