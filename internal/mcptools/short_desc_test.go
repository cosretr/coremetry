package mcptools

// v0.9.1230 (AI perf) — KOMPAKT katalog görünümünün yapısal kapısı.
//
// Kayıt defteri TEK (mcptools.ToolList) ama iki tüketicisi var ve bağlam
// bütçeleri zıt:
//
//   - Dış MCP istemcisi (Claude Desktop, frontier model) kataloğu bir kez
//     okur ve uzun İngilizce sözleşmeden kazanç sağlar.
//   - Yerel gemma4 AYNI kataloğu HER TURDA yutar. Ölçüm (v0.9.1230):
//     33 tool = 24.268 B açıklama + 17.672 B şema = 41.940 B, serbest
//     döngü 5 tura kadar → tek bir soruda ~200 KB katalog.
//
// Çözüm ikinci bir liste DEĞİL, aynı literalde ikinci bir ALAN
// (mcp.Tool.ShortDescription). İkinci liste açılsaydı sürüklenme
// kaçınılmazdı; alan olduğunda yeni tool'un yazarı iki görünümü yan yana
// görür — ve bu dosya boş bırakmasına izin vermez.
//
// Kapı üç yönlü: (a) her tool'un kompakt metni VAR, (b) kompakt metin
// gerçekten KOMPAKT (yoksa alan tören olur), (c) MCP yüzeyi DEĞİŞMEDİ.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// shortDescMaxBytes — tek bir kompakt açıklamanın tavanı.
//
// Amaç "kısa yaz" ricası değil, TAM METNİN yapıştırılmasını engellemek:
// katalogdaki en kısa tam açıklama 224 B, en uzunu 1814 B. 400 B tavanı
// iki-üç cümlelik bir sözleşmeye rahat yeter, bir paragrafa yetmez.
const shortDescMaxBytes = 400

// shortDescMinBytes — alt sınır. "Servisleri listeler" gibi bir metin
// alanı DOLDURUR ama modele hiçbir ayrım öğretmez; kompakt görünümün
// işi maliyeti düşürmek, kararı kötüleştirmek değil.
const shortDescMinBytes = 60

// shortCatalogMaxBytes — TOPLAM kompakt katalog bütçesi.
//
// Diyetin kendisi buradan ölçülür: tam açıklamalar 24.268 B idi. 9 KB
// tavanı bugünkü toplamın (~5.9 KB) üstünde makul bir pay bırakır ama
// katalog sessizce eski ağırlığına dönemez — 33 tool'un her biri 400 B
// tavanına dayansaydı 13 KB ederdi, yani tool başına tavan TEK BAŞINA
// yetmiyor.
// v0.10.809 — 57 tool (product_guide, 74 B): 9.07 KB; tavan 9.1 KB (tool
// başına ~160 B ortalama korunuyor; yeni tool eklerken uzunları kırp).
// v0.10.1271 — 71 tool: query_metrics / query_traces / query_logs (CoSRE adım 2,
// küçük modelin yapısal giriş yolu; 145 + 127 + 122 = 394 B) → ~9.46 KB; tavan
// 9.5 KB. Bilinçli artış: eski üçlü (query_metric / search_traces / search_logs)
// MCP sözleşmesi olarak kalıyor; sohbetten çıkarılmaları ayrı bir karar.
// v0.10.1275 — 72 tool: pivot_from_trace (CoSRE adım 6, trace → pod → JVM → log
// tek çağrıda; ölçülen +133 B → 9570 B) → tavan 9.65 KB (+150 B, 50 B'a yukarı).
// v0.10.1276 — 73 tool: service_health_sweep (CoSRE adım 7, servisin pod /
// operasyon / fonksiyon taraması + sorunluların logları tek çağrıda; ölçülen
// +126 B → 9696 B) → tavan 9.8 KB (+150 B, 50 B'a yukarı).
// v0.10.1284 — 74 tool: explain_log_spike (AI SRE Faz 1a, log artışını ne
// açıklıyor — tepe + önceki pencereyle pod/seviye/desen payı tek çağrıda;
// ölçülen +150 B → 9846 B) → tavan 9.95 KB (+150 B, 50 B'a yukarı).
// CoSRE faz D — 75 tool: compare_baseline ("ne değişti / neden yavaşladı", yalnız-MV
// taban kıyası + grafik bloğu; ölçülen +174 B → 10117 B) → tavan 10.25 KB (50 B'a yukarı).
// v0.10.2449 — 80 tool: endpoint_lookup / trace_where / repo_lookup / messaging_flow / downstream_calls (ölçülen +575 B → 10692 B) → tavan 10.75 KB.
// v0.10.2453 — 81 tool: dependency_path (ölçülen +119 B → 10811 B) → tavan 10.85 KB.
// v0.10.2454 — get_service_health raporu +32 B → 10843 B.
// v0.10.2455 (operatör 2026-11-22: "limiti bilinçli artıralım") — tavan
// 12 KB: ~1.2 KB (~300 token) pay, sonraki 6–8 araç için. Küçük model
// bağlamı ve tur başı token maliyeti nedeniyle tavan kalır; her yeni araç
// ölçülen B'ı bu yoruma yazar.
const shortCatalogMaxBytes = 12000

// shortCatalogSimpleMaxBytes — v0.10.1302: simple araç seti varyantının
// (küçük model; simpleHiddenTools gizli, ⟦full|simple⟧ işaretleri simple
// tarafıyla) kompakt toplamı. Ölçülen: 9344 B (full 9854 B; dört gizli
// aracın metni −~510 B, işaretlerin simple tarafı +~0) → tavan 9.5 KB (+150 B,
// 50 B'a yukarı; full'un kuralı). Simple < full ayrıca zorlanır.
// CoSRE faz D — compare_baseline +174 B → 9607 B; tavan 9.7 KB.
// v0.10.2449 — beş araç +575 B → 10182 B; tavan 10.25 KB.
// v0.10.2453 — dependency_path +119 B → 10301 B; tavan 10.35 KB.
// v0.10.2454 — +32 B → 10333 B. v0.10.2455 (operatör) — tavan 11.5 KB
// (full ile aynı ~1.2 KB pay; simple < full kuralı sürer).
const shortCatalogSimpleMaxBytes = 11500

// shortCatalogConditionalMaxBytes — v0.10.1050: KOŞULLU sohbet araçlarının
// (chatOffered; bugün yalnız read_source_code, DevOps bağlıyken) kompakt
// toplamının AYRI tavanı. Koşulsuz katalog 9.1 KB'ta kalır (her kurulumun her
// turu); koşullu araç yalnız bağımlılığı yapılandırılmış kurulumda ödenir ve
// bedeli burada açıkça sayılır: read_source_code 271 B → tavan 300 B.
// v0.10.1053 — özne-nötr metin (trace ya da exception takibi; model kapsam
// dışı bir servise çağrı harcamasın) 321 B → tavan 330 B.
// v0.10.1122 — + search_wiki (~180 B) + read_wiki_page (~140 B) → 622 B, tavan 650 B
// (yalnız wiki bilgisi açık + DevOps bağlı kurulumda ödenir).
// v0.10.1133 — + resolve_oracle_operation (~170 B; yalnız etkin Oracle kaynağı olan
// kurulumda ödenir) → ~795 B, tavan 820 B. Koşulsuz katalog 9.1 KB'ta kaldı.
const shortCatalogConditionalMaxBytes = 820

// TAMLIK — her tool'un kompakt metni olmalı. Yeni bir tool kompakt
// açıklamasız gemiye giremez (mcp.Tool.ChatDescription() tam metne
// düşerdi: güvenli ama pahalı, ve sessiz).
//
// v0.10.1302 — araç seti varyantları: kompakt metin ⟦full|simple⟧ işareti
// taşıyabilir; boy kuralları İŞLENMİŞ metne (iki varyant) uygulanır ve bütçe
// varyant başına sayılır (full: ChatToolList, simple: eksi simpleHiddenTools).
func TestEveryToolHasShortDescription(t *testing.T) {
	tools := ToolList(Deps{})
	if len(tools) < 30 {
		t.Fatalf("katalog beklenmedik biçimde küçük (%d) — test yanlış şeyi ölçüyor olabilir", len(tools))
	}
	totalShort, totalSimple, totalFull, conditional := 0, 0, 0, 0
	for _, tl := range tools {
		full, simple := tl.ChatDescriptionFor(false), tl.ChatDescriptionFor(true)
		// v0.10.993 — BÜTÇE sohbetin her tur ödediği katalogdur: dış-yalnız
		// araçlar (externalOnlyTools; ChatToolList'te yok) toplama girmez.
		// Alan kuralları (dolu, tavan/taban, Türkçe) onlar için de geçerli.
		// v0.10.1050 — koşullu araçlar (chatOffered) ayrı tavanda sayılır.
		switch {
		case externalOnlyTools[tl.Name]:
		case !chatOffered(Deps{}, tl.Name):
			conditional += len(full)
		default:
			totalShort += len(full)
			totalFull += len(tl.Description)
			if !simpleHiddenTools[tl.Name] {
				totalSimple += len(simple)
			}
		}
		if tl.ShortDescription == "" {
			t.Errorf("%s: ShortDescription BOŞ — sohbet yolu bu tool için her tur tam metni ödüyor", tl.Name)
			continue
		}
		for variant, txt := range map[string]string{"full": full, "simple": simple} {
			if n := len(txt); n > shortDescMaxBytes {
				t.Errorf("%s (%s): kompakt açıklama %d B (tavan %d) — tam metin yapıştırılmış olabilir", tl.Name, variant, n, shortDescMaxBytes)
			}
			if n := len(txt); n < shortDescMinBytes {
				t.Errorf("%s (%s): kompakt açıklama %d B (taban %d) — hangi soruyu cevapladığını ve komşusundan farkını söylemeli", tl.Name, variant, n, shortDescMinBytes)
			}
			if len(txt) >= len(tl.Description) {
				t.Errorf("%s (%s): kompakt açıklama tam metinden kısa DEĞİL (%d ≥ %d)", tl.Name, variant, len(txt), len(tl.Description))
			}
			if !utf8.ValidString(txt) {
				t.Errorf("%s (%s): kompakt açıklama geçerli UTF-8 değil", tl.Name, variant)
			}
		}
		// ChatDescription() kompakt metni SEÇMELİ — alan var ama
		// okunmuyorsa kazanç sıfır. v0.10.1302: işaretler çözülmüş full hâli.
		if got := tl.ChatDescription(); got != full || got != mcp.RenderToolsetMarkers(tl.ShortDescription, false) {
			t.Errorf("%s: ChatDescription() kompakt metnin full hâlini döndürmüyor", tl.Name)
		}
	}
	if totalShort > shortCatalogMaxBytes {
		t.Errorf("kompakt katalog (full) %d B — tavan %d B (diyet eriyor)", totalShort, shortCatalogMaxBytes)
	}
	if totalSimple > shortCatalogSimpleMaxBytes {
		t.Errorf("kompakt katalog (simple) %d B — tavan %d B", totalSimple, shortCatalogSimpleMaxBytes)
	}
	if totalSimple >= totalShort {
		t.Errorf("simple katalog (%d B) full'dan (%d B) küçük değil — gizleme kazanç getirmiyor", totalSimple, totalShort)
	}
	if conditional > shortCatalogConditionalMaxBytes {
		t.Errorf("koşullu araçların kompakt toplamı %d B — tavan %d B", conditional, shortCatalogConditionalMaxBytes)
	}
	// Diyetin ANLAMLI olması: kompakt görünüm tam metnin yarısından
	// küçük kalmalı, yoksa her tur ödenen bedel aynı sınıfta kalır.
	if totalShort*2 >= totalFull {
		t.Errorf("kompakt katalog (%d B) tam kataloğun (%d B) yarısından küçük değil", totalShort, totalFull)
	}
	t.Logf("katalog: %d tool, tam %d B → kompakt full %d B / simple %d B (+ koşullu %d B)", len(tools), totalFull, totalShort, totalSimple, conditional)
}

// ChatDescription() BOŞKEN tam metne düşer — güvenli yön. Tamlık kapısı
// yukarıda; burada yalnız yedek dalın doğru davrandığı pinleniyor.
func TestChatDescriptionFallsBackToFull(t *testing.T) {
	full := mcp.Tool{Name: "x", Description: "tam metin"}
	if got := full.ChatDescription(); got != "tam metin" {
		t.Fatalf("boş ShortDescription tam metne düşmeli, %q döndü", got)
	}
	short := mcp.Tool{Name: "x", Description: "tam metin", ShortDescription: "kısa"}
	if got := short.ChatDescription(); got != "kısa" {
		t.Fatalf("dolu ShortDescription seçilmeli, %q döndü", got)
	}
}

// MCP YÜZEYİ DEĞİŞMEDİ — dış istemci tools/list'te TAM İngilizce
// sözleşmeyi görmeye devam eder. Kompakt metin protokole SIZMAMALI:
// sızarsa Claude Desktop sınıfı bir istemci maliyet şerhlerini,
// dürüstlük negatiflerini ve "şunu uydurma" kurallarını kaybeder.
func TestMCPToolsListServesFullDescriptions(t *testing.T) {
	srv := mcp.New("coremetry-test", "v0.0.0-test")
	tools := ToolList(Deps{})
	for _, tl := range tools {
		srv.RegisterTool(tl)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/mcp", srv.HandleStreamable)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	resp, err := http.Post(ts.URL+"/api/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	defer resp.Body.Close()
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Result.Tools) != len(tools) {
		t.Fatalf("tools/list %d tool döndü, kayıt defterinde %d var", len(out.Result.Tools), len(tools))
	}
	byName := map[string]mcp.Tool{}
	for _, tl := range tools {
		byName[tl.Name] = tl
	}
	for _, got := range out.Result.Tools {
		want, ok := byName[got.Name]
		if !ok {
			t.Errorf("tools/list bilinmeyen tool döndü: %s", got.Name)
			continue
		}
		if got.Description != want.Description {
			t.Errorf("%s: tools/list TAM açıklamayı servis etmiyor", got.Name)
		}
		if got.Description == want.ShortDescription {
			t.Errorf("%s: kompakt metin MCP protokolüne SIZDI — dış istemci sözleşmeyi kaybeder", got.Name)
		}
	}
}

// Kompakt metinler ÜRÜNÜN dilinde (Türkçe) yazılır — sohbet yüzeyi
// Türkçe ve modele iki dilli katalog vermek küçük modelde ek yük.
// Kapı kaba ama ısırır: her metin en az bir Türkçe'ye özgü harf ya da
// Türkçe bir bağlaç taşımalı.
// v0.10.1302 — iki varyant da (⟦full|simple⟧ işlenmiş) Türkçe olmalı.
func TestShortDescriptionsAreTurkish(t *testing.T) {
	markers := []string{"ı", "ş", "ğ", "ü", "ö", "ç", "İ", " ile ", " için ", " ve "}
	for _, tl := range ToolList(Deps{}) {
		for _, simple := range []bool{false, true} {
			txt := tl.ChatDescriptionFor(simple)
			hit := false
			for _, m := range markers {
				if strings.Contains(txt, m) {
					hit = true
					break
				}
			}
			if !hit {
				t.Errorf("%s (simple=%v): kompakt açıklama Türkçe görünmüyor: %q", tl.Name, simple, txt)
			}
		}
	}
}
