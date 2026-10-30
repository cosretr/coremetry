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
