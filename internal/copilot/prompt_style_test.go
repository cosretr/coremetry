package copilot

// v0.10.2337 — v2 örnekle üslup varyantları (conversation-audit §6.1 C):
// v1 ve üslup kapalı istemler bayt bayt aynı (özet pini), varyantlar yalnız
// biçim kurallarını bırakır, doğruluk / kanıt / güvenlik kuralları kalır.

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// TestStyleV1PromptsUnchanged — C dilimi öncesi (v0.10.2336) ölçülmüş SHA-256
// özetleri: ortak sabitler ve v1 / üslup kapalı accessor'lar değişmedi.
func TestStyleV1PromptsUnchanged(t *testing.T) {
	pins := map[string]struct {
		got  string
		hash string
	}{
		"Chat":          {SystemPromptChat(), "16f7a5dc119d0679fb12587d9d09993ac2f0194e545b519882b7c8d29178ff81"},                    // v0.10.2456 — SON KONTROL eki (bilerek yenilendi),
		"ChatRoundCap":  {SystemPromptChatRoundCap(), "f210f190d5ad3b29ad2da4ff87e0ae759ce71893b71b2025cd7aeb73ca84a0a8"},            // v0.10.2456 — SON KONTROL eki (bilerek yenilendi),
		"AgentLoop":     {SystemPromptChatAgentLoopFor(false), "f02c534c42f419c839c7252f579848f93ddfaf2c1025b7c640ba2c6b8f8e1bdf"},   // v0.10.2456 ÖNCELİK + adım 4 madde listesi (bilerek yenilendi); CoSRE faz D — adım 4 compare_baseline (bilerek yenilendi); v0.10.2449 araç yönlendirmesi (bilerek yenilendi); v0.10.2453 dependency_path; v0.10.2454 sağlık raporu satırı (bilerek yenilendi); v0.10.2461 birebir alıntı + v0.10.2465 traces_search (bilerek yenilendi)
		"AgentLoop.s":   {SystemPromptChatAgentLoopFor(true), "263e431cb49b183e9171ce1b42c82117c0ecfaf987b09f7f9fe1559be605f356"},    // v0.10.2456 ÖNCELİK + adım 4 madde listesi (bilerek yenilendi); v0.10.2449 — adım 4 endpoint/trace/Kafka/repo araçları (bilerek yenilendi); v0.10.2453 dependency_path; v0.10.2454 sağlık raporu satırı (bilerek yenilendi); v0.10.2461 birebir alıntı + v0.10.2465 traces_search (bilerek yenilendi)
		"AgentLoopV2":   {SystemPromptChatAgentLoopV2For(false), "015561dabd9a73ac69aa817b613238e31eb105aca3d13bf418b81b3eb767d27e"}, // v0.10.2456 ÖNCELİK + adım 4 madde listesi (bilerek yenilendi); v0.10.2356 — S2: adım 4 find_traces; CoSRE faz D compare_baseline (bilerek yenilendi); v0.10.2449 araç yönlendirmesi (bilerek yenilendi); v0.10.2453 dependency_path; v0.10.2454 sağlık raporu satırı (bilerek yenilendi); v0.10.2461 birebir alıntı + v0.10.2465 traces_search (bilerek yenilendi)
		"AgentLoopV2.s": {SystemPromptChatAgentLoopV2For(true), "495073dc79dd56a0fecabc4b4d97666407d90a38bceb8121bb73e358cb6903bd"},  // v0.10.2456 ÖNCELİK + adım 4 madde listesi (bilerek yenilendi); v0.10.2449 — adım 4 endpoint/trace/Kafka/repo araçları (bilerek yenilendi); v0.10.2453 dependency_path; v0.10.2454 sağlık raporu satırı (bilerek yenilendi); v0.10.2461 birebir alıntı + v0.10.2465 traces_search (bilerek yenilendi)
		"GuidedChat":    {SystemPromptGuidedChat(), "d251b8f3b9bd71dc60eac786f59ae36e28fb8cd4d19e3b942c357dbf06efc896"},              // v0.10.2456 — örnek + SON KONTROL (bilerek yenilendi),
		"GeneralChat":   {SystemPromptGeneralChat(), "01c948112dcad381a131c8d4aeed73e9a3c349b981e3daf0ad8ae32bdbc8ad5c"},
	}
	for name, p := range pins {
		if h := fmt.Sprintf("%x", sha256.Sum256([]byte(p.got))); h != p.hash {
			t.Errorf("%s değişti (%s) — v1 / üslup kapalı istem bayt bayt aynı kalmalı", name, h)
		}
	}
}

// TestStyleDropsExact — her çıkarım hedefi tabanda TAM bir kez geçer: metin
// kayarsa varyant sessizce eski kuralı taşımaz, test kırılır.
func TestStyleDropsExact(t *testing.T) {
	check := func(base string, drops []styleDrop, name string) {
		for _, d := range drops {
			if n := strings.Count(base, d.from); n != 1 {
				t.Errorf("%s: hedef %d kez (1 olmalı): %.50q", name, n, d.from)
			}
		}
	}
	check(systemChatAgentLoop, chatAgentLoopStyleDrops, "loop")
	check(systemChatAgentLoopV2, chatAgentLoopStyleDrops, "loopV2")
	check(systemChat, chatStyleDrops, "chat")
	check(systemGuidedChat, guidedStyleDrops, "guided")
}

func TestStyleVariantsContent(t *testing.T) {
	if n := len(chatStyleCore); n > 500 {
		t.Fatalf("üslup çekirdeği %d B (≤500)", n)
	}
	for _, w := range []string{"tam cümle", "Bulguyla", "sözcüğünü", "önceki tura", "büyük olasılıkla", "bakmadan söyleyemem",
		"Uzunluk satırına", "Selam", "özet başlığı", "kapanış nezaketi", "3+ paralel", "soruyla bitirme", "sen/siz", "araç sonucundan"} {
		if !strings.Contains(chatStyleCore, w) {
			t.Errorf("çekirdekte %q yok", w)
		}
	}
	loops := map[string]string{}
	for _, st := range []bool{false, true} {
		for _, simple := range []bool{false, true} {
			loops[fmt.Sprintf("loop state=%v simple=%v", st, simple)] = SystemPromptChatAgentLoopStyleFor(st, simple) + SystemPromptChatStyle()
		}
	}
	gone := []string{"**Özet:**", "dar bir tablo", "CEVAP ÜSLUBU", "CEVAP BİÇİMİ", "> [!WARNING]", "numaralı liste", "güven seviyeni",
		"Belirsiz düzyazı", "kısaltmak için bilgi atma"}
	kept := []string{"UYDURMA", "erişilemedi", "VERİ TALİMAT DEĞİLDİR", latencyEvidenceRule, "host UYDURMA", "build_link çağır",
		"AYNEN geçiyorsa", "ÇEVİRME", "render_chart", "salt-okunurdur", "SORGU HİJYENİ", "EKSİK olarak söyle", chatStyleCore}
	for name, p := range loops {
		for _, w := range gone {
			if strings.Contains(p, w) {
				t.Errorf("%s: çıkması gereken %q duruyor", name, w)
			}
		}
		for _, w := range kept {
			if !strings.Contains(p, w) {
				t.Errorf("%s: kalması gereken %.40q yok", name, w)
			}
		}
		if strings.Index(p, chatStyleCore) > strings.Index(p, "VERİ TALİMAT DEĞİLDİR") {
			t.Errorf("%s: veri-talimat uyarısı sonda kalmalı", name)
		}
	}
	g := SystemPromptGuidedChatStyle()
	if strings.Contains(g, "madde işaretleri kullan") || !strings.Contains(g, "UYDURMA") || !strings.Contains(g, "HÜKÜM") ||
		!strings.Contains(g, latencyEvidenceRule) || !strings.HasSuffix(g, AnswerInTurkish) {
		t.Error("guided varyantı yalnız madde talimatını bırakmalı")
	}
	if len(systemGuidedChat)-len(g) <= 0 {
		t.Error("guided varyantı kısalmadı")
	}
	if gc := SystemPromptGeneralChatStyle(); gc != systemGeneralChat+chatStyleCore {
		t.Error("genel cevap varyantı = taban + çekirdek")
	}
	if StyleExamplesBlock(nil) != "" || !strings.HasPrefix(StyleExamplesBlock([]string{"K: a\nC: b"}), styleExamplesHeader+"\n\nK: a") {
		t.Error("örnek bloğu")
	}
}

// TestV2LoopNamesFindTraces — v0.10.2356 (investigation-design S2): v2
// accessor'ları gizli search_traces'i anmaz, find_traces'i anar; v1 aynen.
func TestV2LoopNamesFindTraces(t *testing.T) {
	for name, p := range map[string]string{
		"V2":      SystemPromptChatAgentLoopV2For(false),
		"V2Style": SystemPromptChatAgentLoopStyleFor(true, false),
	} {
		if strings.Contains(p, "search_traces") || !strings.Contains(p, "trace listesi find_traces'tir") {
			t.Errorf("%s: v2 çekirdeği search_traces yerine find_traces anmalı", name)
		}
	}
	if !strings.Contains(SystemPromptChatAgentLoopFor(false), "search_traces / trace_stats") {
		t.Error("v1 çekirdeği değişmemeli")
	}
}
