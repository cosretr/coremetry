package api

// chat_step_ids.go — v0.9.1229. ⚙ adım çipinin KİMLİĞİ ve KANITI.
//
// v0.9.1181 çipe "veriyi göster" affordance'ı verdi: `step` olayı bir
// `i` taşır, tool çalıştıktan sonra `step-result` aynı `i` ile kanıtı
// yollar, frontend ikisini bu sayıyla eşler (useChatThread). O iş
// YALNIZ serbest tool döngüsünde yapılmıştı.
//
// Guided yol — yani cevapların ÇOĞUNLUĞU — adımlarını
// map[string]string{"tool","args"} olarak yayınlıyordu: `i` yok,
// `step-result` hiç yok. Frontend `i` yoksa detayı DÜŞÜRÜYOR, yani
// guided'ın çipleri tıklanamayan ölü etiketlerdi. Operatör serbest
// döngüde kanıt zincirini açabiliyor, asıl cevap yolunda
// açamıyordu — bir APM'de en yanlış yerdeki boşluk.
//
// Kimlik ÜRETİMİ tek yerde ve TEK sayaçta olmalı, çünkü bir istekte
// birden çok yol adım yayınlayabiliyor: guided bağlam çipini basıp
// rotayı devredebilir (copilot_guided.go), çekmece yolu kendi çipini
// basar, sonra serbest döngü çalışır. İki ayrı sayaç aynı sayıyı iki
// kez üretirdi ve frontend `i` ile eşlediği için kanıt YANLIŞ çipe
// yapışırdı. Bu yüzden sayaç SSE emit sarmalayıcısında yaşıyor:
// akıştaki her `step` olayı, kim yayınlarsa yayınlasın, sıradaki
// numarayı alır.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/ai/agent/blocks"
	"github.com/cilcenk/coremetry/internal/mcp"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// withStepIDs — akıştaki her `step` olayına istek-boyunca tekil bir
// `i` damgalar. copilotChat'in SSE emit'ini bir kez sarar.
//
// map[string]any payload YERİNDE damgalanır: emitStepChip damgayı
// geri okuyup çağırana döndürüyor (eşli `step-result` bu kimliği
// ister). map[string]string payload (bağlam çipleri, çekmece yolu)
// yeni bir map'e kopyalanıp damgalanır — kimlikleri kimse geri
// okumuyor ama SAYI ATLAMAMALI: frontend çip şeridini `step`
// sırasıyla çiziyor ve detay dizisi yalnız `i` taşıyan olaylarla
// büyüyor; numarasız bir çip diziyi kaydırıp sonraki kanıtı yanlış
// çipe bindirirdi.
func withStepIDs(emit func(string, any)) func(string, any) {
	return withStepIDsFrom(emit, 0)
}

// withStepIDsFrom — agent-core S5: sayaç `base`ten başlar (onay devamı aynı
// v2 asistan mesajına yazar; kimlikler önceki akışınkilerle çakışmasın).
func withStepIDsFrom(emit func(string, any), base int) func(string, any) {
	n := base
	return func(kind string, payload any) {
		if kind == "step" {
			n++
			switch m := payload.(type) {
			case map[string]any:
				m["i"] = n
			case map[string]string:
				conv := make(map[string]any, len(m)+1)
				for k, v := range m {
					conv[k] = v
				}
				conv["i"] = n
				payload = conv
			}
		}
		emit(kind, payload)
	}
}

// emitStepChip — ⚙ çipini yayınlar ve sarmalayıcının verdiği kimliği
// döner. Çip tool ÇALIŞMADAN önce çıkar (ilerleme geri bildirimi),
// kanıt sonra ayrı bir olayla gelir.
//
// Sarmalanmamış bir emit'te (iç içe bundle çağrılarının no-op emit'i,
// testler) 0 döner ve eşli kanıt SESSİZCE yayınlanmaz — numarasız bir
// `step-result` frontend'de hiçbir çiple eşleşmez, en iyi ihtimalle
// gürültüdür.
func emitStepChip(emit func(string, any), tool, args string) int {
	return emitStepChipOrigin(emit, tool, args, "")
}

// emitStepChipOrigin — v0.10.161: çip kökeni. "guided" = sunucu ön-yüklemesi
// (model araç çağırmadı; copilot_guided.go), boş = modelin araç çağrısı.
// Frontend rozeti ("ön-yükleme") bu alandan okur — `delta` olayından
// çıkarım YAPMAZ (drawer katmanı da delta yayınlıyor; inceleme must-fix).
func emitStepChipOrigin(emit func(string, any), tool, args, origin string) int {
	m := map[string]any{"tool": tool, "args": args}
	if origin != "" {
		m["origin"] = origin
	}
	if p := stepPhaseTR(tool); p != "" {
		m["phase"] = p // v0.10.1273 — isteğe bağlı evre (chat_step_phase.go)
	}
	emit("step", m)
	if i, ok := m["i"].(int); ok {
		return i
	}
	return 0
}

// stepArgsJSONMax — argsJson tavanı: daha büyük argüman yalnız metin (args)
// olarak gider; v2 istemcisi yapısal görünümü atlar.
const stepArgsJSONMax = 8 << 10

// emitStepChipInput — v0.10.1594 (CoSRE v2 Faz 1, R6): emitStepChipOrigin +
// geçerli ve ≤8 KiB JSON argümanı `argsJson` olarak (uistream →
// tool-input-available.input.args). Yalnız serbest döngünün model çağrısı
// sitesinde kullanılır; v1 alanları (tool/args/origin/phase) aynen.
func emitStepChipInput(emit func(string, any), tool, args, origin string, input json.RawMessage) int {
	m := map[string]any{"tool": tool, "args": args}
	if origin != "" {
		m["origin"] = origin
	}
	if p := stepPhaseTR(tool); p != "" {
		m["phase"] = p
	}
	if len(input) > 0 && len(input) <= stepArgsJSONMax && json.Valid(input) {
		m["argsJson"] = input
	}
	emit("step", m)
	if i, ok := m["i"].(int); ok {
		return i
	}
	return 0
}

// emitStepEvidence — adımın KANITI: modelin gerçekten gördüğü metin,
// 4 KB tavanıyla kırpılmış ve kırpma İLAN EDİLEREK (clipStepPreview).
// `bytes` kırpılmamış gerçek boydur.
//
// İki durumda hiç yayınlanmaz:
//   - kimlik yoksa (yukarı bak),
//   - hata YOKKEN metin boşsa. Boş kanıt çipi düğmeye çevirirdi ve
//     açılan blok bomboş olurdu; ölü affordance (v0.9.592 dersi)
//     eksik affordance'tan kötüdür. Hata varsa metin daima dolu.
func emitStepEvidence(emit func(string, any), i int, tool, text string, err error) {
	if i <= 0 {
		return
	}
	ok := err == nil
	if err != nil {
		// v0.9.1234 — hata çipi de tool hatalarının ortak sözleşmesini
		// gösterir (mcp.ToolErrorJSON): sınıf + tekrar denenebilirlik +
		// Türkçe ipucu + KIRPILMIŞ ham metin. Öncesinde çipe ham sürücü
		// dökümü basılıyordu — 4 KB'lık önizlemenin tamamını tek bir
		// ClickHouse istisnası doldurabiliyordu ve operatörün okuduğu
		// şey "ne yapmalı"yı söylemiyordu. Guided yolunda bu metin
		// modele GİTMEZ (blok sessizce eksik kalır); yine de aynı
		// sözleşme, çünkü okuyan gözün sorusu aynı.
		text = mcp.ToolErrorJSON(err)
	} else if strings.TrimSpace(text) == "" {
		return
	}
	preview, truncated := clipStepPreview(text)
	ev := map[string]any{
		"i": i, "tool": tool, "ok": ok,
		"preview": preview, "truncated": truncated, "bytes": len(text),
	}
	// v0.10.944 — başarılı kanıtın kaynak durumu (chat_step_sources.go).
	if ok {
		// v0.10.944 — boş dilim de gider: "okundu, durum yok" (nil = denetlenemedi).
		if srcs := stepSourceStatuses(text); srcs != nil {
			ev["sources"] = srcs
		}
		// v0.10.1272 — kanıt zarflı araç (query_metrics/traces/logs) bu yoldan da
		// geçerse çip aynı kanıtı taşır (chat_evidence.go). v0.10.1275 — liste
		// varyantı: bileşik araçta `evidenceList` de yazılır.
		attachStepEvidenceList(ev, tool, text)
	}
	emit("step-result", ev)
}

// emitGuidedEvidenceResult — v0.10.1386 (CoSRE metric_chart): kanıt zarfı
// taşımayan guided adımın KENDİ kurduğu kanıtı. emitStepEvidence'in aynası
// (aynı step-result gövdesi), ama kanıt aracın JSON'undan çıkarılmaz — demet
// sorguyu kendisi koştu ve ChatEvidence'i doğrudan verir. Ref = "ev<i>"
// (attachStepEvidence kuralı); chart != nil ise aynı ref'le `ev_block`
// (withBlockSeq sıralayıcısı) yayınlanır: çip bloğu açar/kapatır. Kimlik yoksa
// (sarmalanmamış emit) hiçbir şey yayınlanmaz; dönen kanıtın Ref'i boştur.
func emitGuidedEvidenceResult(emit func(string, any), i int, tool, text string, ev mcptools.ChatEvidence, chart *mcptools.EvChartPayload) mcptools.ChatEvidence {
	if i <= 0 {
		return ev
	}
	ev.Ref = fmt.Sprintf("ev%d", i)
	preview, truncated := clipStepPreview(text)
	m := map[string]any{
		"i": i, "tool": tool, "ok": true,
		"preview": preview, "truncated": truncated, "bytes": len(text),
		"evidence": ev,
	}
	if ev.Href != "" {
		m["href"] = ev.Href
	}
	emit("step-result", m)
	if chart != nil && len(chart.Series) > 0 {
		p := *chart
		p.EvidenceRef = ev.Ref
		emit("ev_block", mcptools.EvidenceBlock{Type: blocks.TypeChart, Payload: p})
	}
	return ev
}

// withBlockSeq — v0.10.557 (CoSRE Faz 4c): guided demetlerin yapısal kanıtı TEK
// sıralayıcıdan geçsin. Demet `emit("evidence", payload)` der; sarmalayıcı bunu
// chat'in blockSeq'iyle `block{type:"evidence"}` olarak yayar — chart/link/action
// bloklarıyla aynı id/seq uzayı (iki sıralayıcı = FE'de id çakışması, blok
// birbirini ezer). seq nil ise olay olduğu gibi geçer (harici/hafif yollar).
func withBlockSeq(emit func(string, any), seq *blocks.Sequencer) func(string, any) {
	return func(kind string, payload any) {
		if kind == "evidence" && seq != nil {
			emit("block", seq.Next(blocks.TypeEvidence, payload))
			return
		}
		if kind == "trace_list" && seq != nil { // v0.10.688 — endpoint trace listesi (FE tablo)
			emit("block", seq.Next(blocks.TypeTraceList, payload))
			return
		}
		if kind == blocks.TypeServiceReport && seq != nil { // v0.10.1337 — servis raporu kartı
			emit("block", seq.Next(blocks.TypeServiceReport, payload))
			return
		}
		if kind == blocks.TypeTopology && seq != nil { // v0.10.1388 — topoloji grafiği (copilot_service_topology.go)
			emit("block", seq.Next(blocks.TypeTopology, payload))
			return
		}
		// v0.10.1407 — açıklama turunun kanıt kartları (chat_explain_registry.go).
		if (kind == blocks.TypeChartsSignals || kind == blocks.TypeLogSpike || kind == blocks.TypeMetricPanel) && seq != nil { // v0.10.1435 — metric_panel
			emit("block", seq.Next(kind, payload))
			return
		}
		// v0.10.1276 (CoSRE adım 7) — guided demetin bileşik araç bloğu
		// (mcptools.ToolResultBlocks çıktısı: chart/table/trace_list; yük
		// evidenceRef'iyle adım çipine bağlı). Serbest döngünün blok yoluyla AYNI
		// sıralayıcı — iki sıralayıcı FE'de id çakışması olurdu.
		if b, ok := payload.(mcptools.EvidenceBlock); ok && kind == "ev_block" && seq != nil {
			emit("block", seq.Next(b.Type, b.Payload))
			return
		}
		emit(kind, payload)
	}
}
