package uistream

// Part — tek UI Message Stream parçası. map: json.Marshal anahtarları sıralar,
// altın baytlar deterministik olur.
type Part map[string]any

// KnownKinds — çeviricinin eşlediği v1 olay türleri. internal/api'deki
// TestUIStreamCoversEmittedKinds, sohbet yazıcısına ulaşabilen her türün bu
// kümede olduğunu pinler; bilinmeyen tür data-unknown'a sarılır (düşmez).
var KnownKinds = map[string]bool{
	"step": true, "step-result": true, "delta": true, "answer": true,
	"block": true, "error": true, "code-lock": true, "done": true,
	// araç seti dilim 1 — döngü çıkışı (data-stop); v1 yazıcısı düşürür (api chat_sink.go).
	"stop": true,
	// araç seti dilim 2 — kullanıcı kapsamı (data-scope); v1 yazıcısı düşürür.
	"scope": true,
	// v0.10.1737 — v2 seçici kartı (data-pick; kind=endpoint); v1 yazıcısı düşürür.
	"pick": true,
	// v0.10.2100 (experience-audit D1) — kapalı netleştirme sorusu
	// (data-question); v1 yazıcısı düşürür, v1 bugünkü düz metni alır.
	"question": true,
	// v0.10.2325 (conversation-audit §6.1 A) — konuşma durumu (data-state):
	// turun çözülmüş kapsamı, defter değerleri yok; v1 yazıcısı düşürür.
	"state": true,
	// v0.10.2333 (conversation-audit §6.1 F1, karar 11) — araç turunun ara
	// durum cümlesi (data-status, transient): mesaja ve arşive girmez; v1
	// yazıcısı düşürür.
	"status": true,
	// v0.10.2353 (interaction-audit §6 I5) — onaylı aksiyon kartı (data-action,
	// yalnız sunucu kuralı önerir) ve onay ucunun sonucu (data-action-result;
	// akışta değil, POST /api/cosre/actions/confirm cevabı ActionResultPart ile
	// aynı biçimde döner). v1 yazıcısı düşürür.
	"action": true, "action-result": true,
	// v0.10.2363 (interaction-audit §6 I1) — adım sözleşmesi: araçlı adımın tool
	// parçasının YANINDA eşli data-step (id s<i>; aynı id öncekinin yerine
	// geçer: running → done | failed | skipped). v1 yazıcısı düşürür.
	"step-state": true,
	// v0.10.2364 (interaction-audit §6 I3) — sonraki adım çipleri
	// (data-suggestions {exchangeId, chips[{id, label, rule, intent{tool,args},
	// token}]}); açıkken data-followups'ın yerine geçer. v1 yazıcısı düşürür.
	"suggestions": true,
}

// SuggestionsPart — data-suggestions parçası (alışveriş başına bir).
func SuggestionsPart(m map[string]any) Part {
	return Part{"type": "data-suggestions", "id": "suggestions", "data": m}
}

// StepStates / StepSignals — data-step sözleşmesinin kapalı kümeleri (api chat_step_contract.go).
var (
	StepStates  = []string{"running", "done", "failed", "skipped"}
	StepSignals = []string{"metric", "trace", "log", "knowledge"}
)

// StepStatePart — sözleşme parçası: data-step, id = adımın kimliği (s<i>).
func StepStatePart(m map[string]any) Part {
	id, _ := m["id"].(string)
	return Part{"type": "data-step", "id": id, "data": m}
}

// ActionKinds — data-action `kind` kapalı kümesi (api cosre_actions.go; prod'a
// dokunan tür — restart / rollback / scale — YOK, test pinler).
var ActionKinds = []string{"share_link", "problem_note"}

// ActionResultStatuses — data-action-result `status` kapalı kümesi.
var ActionResultStatuses = []string{"done", "needs_save"}

// ActionPart — data-action parçası: kart başına kararlı id (act-<kind>).
func ActionPart(m map[string]any) Part {
	kind, _ := m["kind"].(string)
	return Part{"type": "data-action", "id": "act-" + kind, "data": m}
}

// ActionResultPart — data-action-result parçası (id = kartın id'si + "-result").
func ActionResultPart(m map[string]any) Part {
	kind, _ := m["kind"].(string)
	return Part{"type": "data-action-result", "id": "act-" + kind + "-result", "data": m}
}

// QuestionKinds — data-question `kind` kapalı kümesi (api chat_question.go;
// ilk üçü chat_clarify Pending türleriyle aynı adlar; entity_pick bekleyen
// soru yazmaz — seçenek etiketi kendi başına yönlenen servis adıdır).
var QuestionKinds = []string{"source", "doc_or_entity", "trace_synth", "entity_pick", "slot"} // v0.10.2139 entity_pick (api chat_bare_entity.go); v0.10.2332 slot (api chat_answer_slot.go)

// QuestionMaxOptions — data-question seçenek tavanı (kapalı küçük soru;
// katalogdan çok öğeli seçim data-pick'tir).
const QuestionMaxOptions = 3

// QuestionSlotMaxOptions — v0.10.2332 (conversation-audit §6.1 B): `slot`
// sorusunun (eksik tek alan, ör. hangi servis) seçenek tavanı.
const QuestionSlotMaxOptions = 4

// QuestionSourceMaxOptions — v0.10.2381: `source` sorusunun seçenek tavanı
// (telemetri, wiki, envanter, exceptions, genel bilgi — yalnız var olanlar).
const QuestionSourceMaxOptions = 5

// QuestionMaxOptionsFor — türün seçenek tavanı.
func QuestionMaxOptionsFor(kind string) int {
	switch kind {
	case "slot":
		return QuestionSlotMaxOptions
	case "source":
		return QuestionSourceMaxOptions
	}
	return QuestionMaxOptions
}

// StopReasons — data-stop `reason` kapalı kümesi (api chat_stop_event.go
// üretir; protocol.ts STOP_REASONS ile eşit, test pinler).
var StopReasons = []string{"stop", "round_cap", "call_cap", "cancelled", "deadline", "approval_pending", "error", "token_budget"}

// ScopeReasons — data-scope `reason` kapalı kümesi (api chat_user_scope.go
// üretir; protocol.ts SCOPE_REASONS ile eşit, test pinler). "team" = kapsamlı;
// gerisi unscoped:true (tools-audit Q1/Q2 — sessiz genişleme yok, rozet var).
var ScopeReasons = []string{"team", "no_identity", "no_team", "no_services", "disabled", "escape", "error"}

// ReasonApprovalRequired — agent-core S4: `step-result.reason` onay bekleyen
// çağrı (api skipReasonApproval / Executor KindApproval ile aynı dizgi; api
// testi eşitliği pinler). Böyle bir sonuç data-approval'a çevrilir.
const ReasonApprovalRequired = "approval_required"

// BlockPartName — blok tipinin data parçası adı.
func BlockPartName(blockType string) string { return "data-block-" + blockType }
