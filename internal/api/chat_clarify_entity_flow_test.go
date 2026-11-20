package api

// chat_clarify_entity_flow_test.go — v0.10.1576 (operator-reported, prod
// ekran görüntüsü): "Takımımın servisleri nasıl?" → CoSRE takım sordu →
// kullanıcı listedeki takım adını yazdı → cevap YÜKLENEN TABLOLARDAN geldi
// ("Yüklenen tablolarda … 200 satır bulundu"), takımın servisleri değil.
// Operatör: "Bu durumda ragda aramasın, yüklü dokümanlarda arayayım mı diye
// sormasını beklerim." Çıplak bilinen varlık adı (takım / servis) + tabloda
// eşleşme → doküman kademeleri kendiliğinden cevaplamaz, düz metin soru sorar.
//
// v0.10.2381 — soru TEK kaynak sorusu (chat_source_ask.go): önünde bağlam
// cümlesi ("“X” bir takım adı; yüklü envanterde de geçiyor." / "… N satırla
// eşleşiyor."), ardından var olan kaynaklar. Bekleyen soru türü `source`,
// cevap sınıfı kaynak türü (telemetri / excel / wiki / genel).

import (
	"context"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/ai/aisurface"
	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	// entAsk / entRowsAsk — kaynak sorusunun bağlam cümleleri (v0.10.2381).
	entAsk     = "“Payments-Core” bir takım adı; yüklü envanterde de geçiyor."
	entRowsAsk = "yüklü envanter tablosunda"
	entTeam    = "Payments-Core"
	entTeamAsk = "Takımımın servisleri nasıl?"
	entTeamQ   = "Hesabına takım atanmamış. Hangi takımdasın? Uygulama takımları: Payments-Core, Ledger"
)

// newEntityDocRig — clarifyRig + takım kataloğu + takım adını taşıyan tablo.
func newEntityDocRig(t *testing.T) *clarifyRig {
	t.Helper()
	rg := newClarifyRig(t, nil)
	rg.mc.mu.Lock()
	rg.mc.m["copilot:guided:teamcat:v2"] = []byte(`[{"team":"Payments-Core","services":3,"owner":3},{"team":"Ledger","services":2,"owner":2}]`)
	rg.mc.m["copilot:guided:svcnames"] = []byte(`["checkout","payments","order-api"]`)
	rg.mc.mu.Unlock()
	table := "ekip_envanteri · satır 2 — Takım: Payments-Core · Sunucu: srvpay01 · Ortam: PROD\n" +
		"ekip_envanteri · satır 3 — Takım: Payments-Core · Sunucu: srvpay02 · Ortam: UAT\n" +
		"ekip_envanteri · satır 4 — Takım: Ledger · Sunucu: srvled01 · Ortam: PROD\n" +
		"ekip_envanteri · satır 5 — Uygulama: order-api · Sunucu: srvord01 · Ortam: PROD\n"
	prev := docRowChunksSeam
	docRowChunksSeam = func(_ context.Context, needle string) ([]chstore.LocalDocChunk, error) {
		if !strings.Contains(strings.ToLower(table), strings.ToLower(needle)) {
			return nil, nil
		}
		return []chstore.LocalDocChunk{{Title: "ekip_envanteri", Text: table}}, nil
	}
	t.Cleanup(func() { docRowChunksSeam = prev })
	return rg
}

// TestBareTeamNameAsksDocsOrTeam — ekran görüntüsünün kendisi: takım sorusunun
// cevabı olarak yazılan çıplak takım adı tablo cevabına KAÇMAZ, soru sorulur.
func TestBareTeamNameAsksDocsOrTeam(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam)
	r := lastRow(t, rg.rows())
	got := lastAnswerText(fr)
	if r.Tier != aisurface.TierClarify || !strings.HasPrefix(got, entAsk+" "+sourceAskHeadTR) || !strings.Contains(got, "envanterde") {
		t.Fatalf("çıplak takım adı soru sormalı: tier=%q surface=%q cevap=%q", r.Tier, r.Surface, got)
	}
	if strings.Contains(got, "srvpay01") {
		t.Fatalf("tablo cevabı basılmamalı: %q", got)
	}
}

// TestBareTeamNameAskShape — soru LLM'siz, çipsiz, exchangeId'siz; bekleyen
// kaynak sorusu özgün adı, varlık türünü ve sunulan kaynakları taşır.
func TestBareTeamNameAskShape(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam)
	r := lastRow(t, rg.rows())
	if r.Surface != "cosre-turn:clarify/doc_or_entity_ask" {
		t.Fatalf("surface=%q", r.Surface)
	}
	p := rg.pending(t)
	if p == nil || p.Kind != pendingKindSource || p.Question != entTeam || p.Entity != bareEntityTeam ||
		!containsStr(p.Options, srcTelemetry) || !containsStr(p.Options, srcExcel) {
		t.Fatalf("bekleyen soru: %+v", p)
	}
	if got := lastAnswerText(fr); got != sourceAskText(entAsk, p.Options) {
		t.Fatalf("soru metni: %q", got)
	}
	a := framesOf(fr, "answer")[0]
	if a["suggestions"] != nil || a["exchangeId"] != nil {
		t.Fatalf("çip / exchangeId yok: %v", a)
	}
	if n := len(rg.llm.calls()); n != 0 || r.ModelCalls != 0 {
		t.Fatalf("soru LLM'siz olmalı: %d çağrı", n)
	}
}

// TestBareTeamNameReplyDocs — "dokümanlarda" → ÖZGÜN ad tablo satırlarında.
func TestBareTeamNameReplyDocs(t *testing.T) {
	rg := newEntityDocRig(t)
	rg.post(t, entTeamAsk, entTeamQ, entTeam)
	ask := entAsk
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, "yüklü dokümanlarda ara")
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay01") {
		t.Fatalf("doküman seçildi: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if m := replyTool(r); m["kind"] != pendingKindSource || m["reply"] != srcExcel {
		t.Fatalf("clarify_reply kaydı: %v", m)
	}
	if p := rg.pending(t); p != nil {
		t.Fatalf("bekleyen soru silinmeli: %+v", p)
	}
}

// TestBareTeamNameReplyEntity — "takımın servisleri" → guided team_services
// ÖZGÜN adla; doküman kademeleri atlanır, soru yeniden sorulmaz.
func TestBareTeamNameReplyEntity(t *testing.T) {
	for _, reply := range []string{"takımın servislerini getir", "takım", "Coremetry'de"} {
		t.Run(reply, func(t *testing.T) {
			rg := newEntityDocRig(t)
			rg.post(t, entTeamAsk, entTeamQ, entTeam)
			ask := entAsk
			fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, reply)
			r := lastRow(t, rg.rows())
			if r.Tier != aisurface.TierGuided || len(*rg.routes) == 0 || (*rg.routes)[len(*rg.routes)-1] != string(guidedTeamServices) {
				t.Fatalf("takım seçildi: tier=%q rotalar=%v cevap=%q", r.Tier, *rg.routes, lastAnswerText(fr))
			}
			if strings.Contains(lastAnswerText(fr), "srvpay01") {
				t.Fatalf("tablo cevabı gelmemeli: %q", lastAnswerText(fr))
			}
			if m := replyTool(r); m["reply"] != srcTelemetry {
				t.Fatalf("clarify_reply kaydı: %v", m)
			}
			if p := rg.pending(t); p != nil {
				t.Fatalf("yeniden sorulmamalı: %+v", p)
			}
		})
	}
}

// TestBareTeamNameReplyUnrelatedDropped — ilgisiz cevap: soru düşer (ekranda
// adım yok, v0.10.1575), tur yeni soru gibi.
func TestBareTeamNameReplyUnrelatedDropped(t *testing.T) {
	rg := newEntityDocRig(t)
	rg.post(t, entTeamAsk, entTeamQ, entTeam)
	ask := entAsk
	fr := rg.post(t, entTeamAsk, entTeamQ, entTeam, ask, "checkout sağlığı nasıl?")
	for _, f := range fr {
		if f.event == "step" && f.data["tool"] == clarifyReplyTool {
			t.Fatalf("düşen soru adım çizmemeli: %v", f.data)
		}
	}
	r := lastRow(t, rg.rows())
	if r.Tier != aisurface.TierGuided || len(*rg.routes) == 0 || (*rg.routes)[0] != string(guidedServiceHealth) {
		t.Fatalf("yeni soru guided'a: tier=%q rotalar=%v", r.Tier, *rg.routes)
	}
	if m := replyTool(r); m["reply"] != clarifyReplyDropped {
		t.Fatalf("dropped: %v", m)
	}
}

// TestBareServiceNameAsks — servis varyantı.
func TestBareServiceNameAsks(t *testing.T) {
	rg := newEntityDocRig(t)
	fr := rg.post(t, "order-api")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify || !strings.HasPrefix(lastAnswerText(fr), sourceLeadEntityTR(bareEntityService, "order-api")+" "+sourceAskHeadTR) {
		t.Fatalf("servis sorusu: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
}

// TestDocQuestionNotBareEntityUnchanged — v0.10.1942 (operator-reported:
// "envanterde direkt cevap dönmesin önce sorsun"): çıplak varlık OLMAYAN
// doküman sorusu da önce sorulur; "envanter" cevabı satırları getirir.
// Tabloda eşleşmesi olmayan çıplak takım adı soru sormadan guided'a gider.
func TestDocQuestionNotBareEntityUnchanged(t *testing.T) {
	rg := newEntityDocRig(t)
	const q = "Payments-Core sunucuları hangi ortamda"
	fr := rg.post(t, q)
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierClarify || strings.Contains(lastAnswerText(fr), "srvpay02") ||
		!strings.Contains(lastAnswerText(fr), entRowsAsk) {
		t.Fatalf("doküman sorusu önce sorulmalı: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}
	if p := rg.pending(t); p == nil || p.Kind != pendingKindSource || p.Entity != "" {
		t.Fatalf("bekleyen soru beklenir: %+v", p)
	}
	fr = rg.post(t, q, lastAnswerText(fr), "envanterden göster")
	if r := lastRow(t, rg.rows()); r.Tier != aisurface.TierDocRows || !strings.Contains(lastAnswerText(fr), "srvpay02") {
		t.Fatalf("envanter seçilince tablo: tier=%q cevap=%q", r.Tier, lastAnswerText(fr))
	}

	rg2 := newEntityDocRig(t)
	docRowChunksSeam = func(context.Context, string) ([]chstore.LocalDocChunk, error) { return nil, nil }
	rg2.post(t, entTeamAsk, entTeamQ, entTeam)
	if r := lastRow(t, rg2.rows()); r.Tier != aisurface.TierGuided || len(*rg2.routes) == 0 || (*rg2.routes)[0] != string(guidedTeamServices) {
		t.Fatalf("tabloda yoksa doğrudan takım: tier=%q rotalar=%v", r.Tier, *rg2.routes)
	}
}

// TestBareEntityOf — SAF: çıplak varlık tanıma.
// TestWikiNamedQuestionSkipsInventoryAsk — v0.10.2345 (operatör bulgusu: "wiki'de
// dediğimde anlamıyor"): sözcükleri envanter tablosuyla eşleşen ama wiki'yi
// ADIYLA anan soru envanter eşleşmesi sorusuna ve tablo satırlarına
// takılmaz; aynı sorunun wiki'siz hâli eskisi gibi önce sorar.
func TestWikiNamedQuestionSkipsInventoryAsk(t *testing.T) {
	for _, q := range []string{
		"Payments-Core sunucuları nasıl tanımlanır, Atlas wikisine bak",
		"Payments-Core sunucuları nasıl tanımlanır. Atlas wikisi ne bak", // operatörün yazımı
		"wikide Payments-Core sunucuları nasıl tanımlanıyor",
	} {
		rg := newEntityDocRig(t)
		fr := rg.post(t, q)
		ans := lastAnswerText(fr)
		if strings.Contains(ans, entRowsAsk) || strings.Contains(ans, "srvpay02") {
			t.Fatalf("%q: wiki adıyla anılan soru envanter sorusuna / satırlarına gitmemeli: %q", q, ans)
		}
		if p := rg.pending(t); p != nil && p.Kind == pendingKindSource {
			t.Fatalf("%q: bekleyen envanter sorusu olmamalı: %+v", q, p)
		}
	}
	rg := newEntityDocRig(t)
	if fr := rg.post(t, "Payments-Core sunucuları hangi ortamda"); !strings.Contains(lastAnswerText(fr), entRowsAsk) {
		t.Fatalf("wiki anılmayan doküman sorusu eskisi gibi önce sormalı: %q", lastAnswerText(fr))
	}
}

func TestBareEntityOf(t *testing.T) {
	teams := []string{"Payments-Core", "Ledger"}
	svcs := []string{"checkout", "order-api"}
	for _, c := range []struct{ in, kind, name string }{
		{"Payments-Core", bareEntityTeam, "Payments-Core"},
		{"payments-core?", bareEntityTeam, "Payments-Core"},
		{" Ledger ", bareEntityTeam, "Ledger"},
		{"order-api", bareEntityService, "order-api"},
		{"order-api.", bareEntityService, "order-api"},
		{"Payments-Core sunucuları", "", ""},
		{"order-api hataları", "", ""},
		{"Payments-Core sunucuları hangi ortamda", "", ""},
		{"PlatformTeam", "", ""},
		{"", "", ""},
	} {
		k, n := bareEntityOf(c.in, teams, svcs)
		if k != c.kind || n != c.name {
			t.Errorf("bareEntityOf(%q) = %q %q, want %q %q", c.in, k, n, c.kind, c.name)
		}
	}
}

// TestClassifyDocOrEntityReply — SAF: cevap sınıfları; "evet" tek başına sınıf değil.
// v0.10.2381 — yorumlayıcı tek (classifySourceChoice); bu tablo envanter /
// telemetri seçenekli sorunun eski sözleşmesini korur.
func TestClassifyDocOrEntityReply(t *testing.T) {
	opts := []string{srcTelemetry, srcExcel}
	for _, c := range []struct{ in, want string }{
		{"dokümanlarda", srcExcel},
		{"yüklü dokümanlarda ara", srcExcel},
		{"tabloda bak", srcExcel},
		{"excel", srcExcel},
		{"takımın servislerini getir", srcTelemetry},
		{"servisin durumunu", srcTelemetry},
		{"Coremetry'de", srcTelemetry},
		{"evet", ""},
		{"tamam", ""},
		{"takım dokümanı", ""},
		{"checkout sağlığı nasıl?", ""},
	} {
		if got := classifySourceChoice(c.in, opts); got != c.want {
			t.Errorf("classifySourceChoice(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIsServiceHealthAsk — v0.10.1942 (operator-reported): servis sağlığı
// sorusu yüklü envantere gitmez. Nasıl-yapılır ve envanteri açıkça anan soru
// sağlık sorusu değildir.
func TestIsServiceHealthAsk(t *testing.T) {
	cases := map[string]bool{
		"Payments Core takımının servisleri nasıl?":    true,
		"servislerim nasıl":                            true,
		"takımımın servisleri nasıl?":                  true,
		"orders-api servisinin durumu":                 true,
		"Payments ekibinde sorun var mı":               true,
		"orders-api servisi nasıl kurulur":             false, // nasıl-yapılır
		"Payments Core envanterindeki servisler nasıl": false, // envanteri açıkça anıyor
		"Payments-Core sunucuları hangi ortamda":       false,
		"Payments-Core sunucularının IP adresleri":     false,
		"Payments-Core":                                false,
	}
	for q, want := range cases {
		if got := isServiceHealthAsk(q); got != want {
			t.Errorf("isServiceHealthAsk(%q) = %v, beklenen %v", q, got, want)
		}
	}
}

// TestDocsAskChips — v0.10.1942: bellek kapalı yolda çipli soru; /excel aynı
// soruyu envanterden, /telemetri canlı veriden cevaplar.
// v0.10.2381 — cümle TEK kaynak sorusu; çipler soru sırasıyla (telemetri,
// wiki, envanter), keşfin diğer adayları etiketleriyle.
func TestDocsAskChips(t *testing.T) {
	var got map[string]any
	(&Server{}).docRowsAsk(context.Background(), func(ev string, v any) { got = v.(map[string]any) }, "payments-core sunucuları", 3,
		[]string{"payments-core", "srvpay01", "x"}, map[string]string{srcTelemetry: "canlı veri", srcWiki: "Runbook"})
	txt, _ := got["text"].(string)
	if !strings.Contains(txt, "3 satırla") || !strings.Contains(txt, "payments-core, srvpay01") || strings.Contains(txt, ", x") {
		t.Fatalf("metin: %q", txt)
	}
	if want := sourceAskText(sourceLeadRowsTR(3, []string{"payments-core", "srvpay01", "x"}), []string{srcTelemetry, srcWiki, srcExcel}); txt != want {
		t.Fatalf("metin tek kaynak sorusu olmalı:\n got %q\nwant %q", txt, want)
	}
	chips, _ := got["suggestions"].([]string)
	if len(chips) != 3 || chips[0] != "/telemetri · canlı veri" || chips[1] != "/wiki · Runbook" || chips[2] != "/excel · 3 satır" {
		t.Fatalf("çipler: %v", chips)
	}
}
