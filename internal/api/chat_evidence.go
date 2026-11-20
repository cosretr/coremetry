package api

// chat_evidence.go — v0.10.1272 (CoSRE adım 3/8): kanıt çipleri.
//
// query_metrics / query_traces / query_logs sonuçlarındaki `evidence`
// (kaynak · sorgu · pencere · env/cluster · satır · süre) sohbet olaylarına
// YAPISAL olarak taşınır:
//
//   - `step-result.evidence` — çipin kendi kanıtı (+ "Explore'da aç" href'i);
//     adımda başka köprü yoksa `href` de bu olur (eski istemci "Üründe aç"
//     bağlantısını görür).
//   - `block` (v0.10.1272 operatör eki: "chat içerisine de getirebilirsin") —
//     aracın zaten sınırlı verisinden chart / trace_list / table bloğu
//     (mcptools.ToolResultBlock); yük `evidenceRef` = kanıtın `ref`'i, çip
//     bloğu açar/kapatır. Ek sorgu yok.
//   - `answer.evidence[]` — alışveriş boyunca toplanan kanıt listesi; cevap
//     balonu çipleri bundan çizer, arşive de bu liste yazılır (geri yüklenen
//     cevap çiplerini korur).
//
// Geriye uyum: iki alan da İSTEĞE BAĞLI ve yalnız kanıt varsa yazılır — kanıt
// zarfı taşımayan araçların step-result / answer gövdesi bayt-bayt aynı.
// href mcptools.EvidenceHref'ten (tek saf, tablo testli eşleme); burada URL
// kurulmaz.

import (
	"fmt"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// chatEvidenceMax — cevap başına kanıt çipi tavanı (çağrı bütçesi 6–12; Derin
// kipte bile şerit taşmasın). v0.10.1275 — 8 → 12: bileşik araç (pivot_from_trace)
// tek çağrıda ≤6 hop çipi taşır; iki atomik + bir pivot sığsın.
const chatEvidenceMax = 12

// attachStepEvidenceList — v0.10.1275 (CoSRE adım 6): çok-kanıtlı zarf.
// Atomik araç → tek kalem (v0.10.2397 öncesi attachStepEvidence ile aynı gövde); bileşik araç →
// hop başına kalem. Ref'ler adım kimliğinden: ilk "ev<i>", sonrakiler
// "ev<i>.<k>". GERİYE UYUM: `evidence` hep İLK kalem (eski istemci tek çip
// görür); `evidenceList` YALNIZ >1 kalemde yazılır. Saf (yalnız stepEv).
func attachStepEvidenceList(stepEv map[string]any, tool, content string) []mcptools.ChatEvidence {
	evs, ok := mcptools.ToolResultEvidenceList(tool, content)
	if !ok || len(evs) == 0 {
		return nil
	}
	if i, has := eventInt(stepEv["i"]); has && i > 0 {
		for k := range evs {
			if k == 0 {
				evs[k].Ref = fmt.Sprintf("ev%d", i)
			} else {
				evs[k].Ref = fmt.Sprintf("ev%d.%d", i, k)
			}
		}
	}
	stepEv["evidence"] = evs[0]
	if len(evs) > 1 {
		stepEv["evidenceList"] = evs
	}
	if _, set := stepEv["href"]; !set && evs[0].Href != "" {
		stepEv["href"] = evs[0].Href
	}
	return evs
}

// withEvidenceListScope — v0.10.1275: ekran-kapsamı künyesi listenin her
// kalemine; step-result gövdesi de güncellenir. Boş künye → dokunulmaz. Saf.
func withEvidenceListScope(stepEv map[string]any, evs []mcptools.ChatEvidence, sa mcptools.ScopeApplied) []mcptools.ChatEvidence {
	if sa.Empty() || len(evs) == 0 {
		return evs
	}
	for k := range evs {
		c := sa
		evs[k].CtxApplied = &c
	}
	stepEv["evidence"] = evs[0]
	if len(evs) > 1 {
		stepEv["evidenceList"] = evs
	}
	return evs
}

// mergeChatEvidence — çağrı sırasıyla biriken kanıt; tavan chatEvidenceMax. Saf.
func mergeChatEvidence(acc []mcptools.ChatEvidence, ev mcptools.ChatEvidence) []mcptools.ChatEvidence {
	if len(acc) >= chatEvidenceMax {
		return acc
	}
	return append(acc, ev)
}

// withAnswerEvidence — `answer` gövdesine kanıt listesi; liste boşsa alan
// YAZILMAZ (eski cevap şekli aynen). Saf.
func withAnswerEvidence(ans map[string]any, evs []mcptools.ChatEvidence) map[string]any {
	if len(evs) > 0 {
		ans["evidence"] = evs
	}
	return ans
}
