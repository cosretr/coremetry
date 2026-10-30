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
}

// ReasonApprovalRequired — agent-core S4: `step-result.reason` onay bekleyen
// çağrı (api skipReasonApproval / Executor KindApproval ile aynı dizgi; api
// testi eşitliği pinler). Böyle bir sonuç data-approval'a çevrilir.
const ReasonApprovalRequired = "approval_required"

// BlockPartName — blok tipinin data parçası adı.
func BlockPartName(blockType string) string { return "data-block-" + blockType }
