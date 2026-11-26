package api

// v0.10.2480 (F, operatör-bildirimi, prod v0.10.2478): doğrulayıcı aynı turun
// araç sonuçlarından gelen değerlere "(doğrulanmamış değer)" yazıyordu —
// ters tırnaklı "alan: değer" (`peakRatio:9.11`), modelin kısalttığı hata
// grubu kimliği ve "Tip: mesaj" biçimli hata alıntısı. Meşru biçimler
// doğrulanır; kanıtın HİÇBİR yerinde olmayan değer yine uyuşmazdır.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/copilot"
)

const shapesEvidence = `{"anomalies":[{"peakRatio":9.1123,"currentRatio":4.2}],"value":18213,` +
	`"groups":[{"fingerprint":"a1b2c3d4e5f60718","type":"System.Data.SqlClient.SqlException",` +
	`"message":"Error converting data type nvarchar to numeric."}]}`

func TestVerifyLegitimateShapes(t *testing.T) {
	ev := verifyEvidence{Texts: []string{shapesEvidence}}
	cases := []struct {
		name, answer string
		ok           bool // tüm iddialar doğrulandı / türetildi
	}{
		{"oran düz", "peakRatio: 9.11 oldu", true},
		{"oran ters tırnaklı alan:değer", "`peakRatio:9.11`", true},
		{"oran ters tırnaklı boşluklu", "`peakRatio: 9.11`", true},
		{"oran virgüllü", "tabanın 9,11 katı", true},
		{"binlik virgül", "değer 18,213 oldu", true},
		{"binlik nokta", "değer 18.213 oldu", true},
		{"yuvarlanmış", "yaklaşık 18 bin", true},
		{"tam kimlik", "kimlik `a1b2c3d4e5f60718`", true},
		{"kısaltılmış kimlik", "kimlik `a1b2c3d4`", true},
		{"büyük harf kimlik", "kimlik `A1B2C3D4E5F6`", true},
		{"alıntı", `hata "Error converting data type nvarchar to numeric"`, true},
		{"alıntı tip önekli", `hata "SqlException: Error converting data type nvarchar to numeric."`, true},
		{"alıntı tam tip önekli", `hata "System.Data.SqlClient.SqlException: Error converting data type nvarchar to numeric."`, true},
		{"alıntı boşluk/harf", `hata "Error  converting data type NVARCHAR to numeric."`, true},
		// Gevşetilmeyenler: kanıtın hiçbir yerinde olmayan değerler.
		{"uydurma oran", "`peakRatio:7.4`", false},
		{"uydurma kimlik", "kimlik `ffee0099aabb`", false},
		{"kimliğin ORTASI önek değil", "kimlik `c3d4e5f6`", false},
		{"kısa kimlik parçası", "kimlik `a1b2c3`", false},
		{"uydurma alıntı", `hata "Connection refused by upstream host"`, false},
		{"uydurma tip + gerçek mesaj", `hata "TimeoutException: Error converting data type nvarchar to numeric."`, false},
	}
	for _, c := range cases {
		r := verifyAnswer(verifyTierLoop, c.answer, ev, nil)
		mm := r.mismatches()
		if c.ok && len(mm) > 0 {
			t.Errorf("%s: meşru değer uyuşmaz sayıldı: %+v", c.name, mm)
		}
		if !c.ok && len(mm) == 0 {
			t.Errorf("%s: kanıtta olmayan değer kaçtı: %+v", c.name, r.Claims)
		}
	}
}

// Tohum çiftleri (guided_prefetch, saved_investigation) kanıttır — taslak
// denetimi (draftPasses) da görür, final de.
func TestVerifySeededPairsAreEvidence(t *testing.T) {
	args, _ := json.Marshal(map[string]string{"problemId": "p1"})
	seed := []copilot.ChatMessage{
		{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: problemRecordToolID, Name: problemRecordTool, Input: args}}},
		{Role: "user", ToolResults: []copilot.ToolResult{{CallID: problemRecordToolID, Name: problemRecordTool, Content: "KAYITLI PROBLEM ARAŞTIRMASI\n```text\n- tüketici gecikmesi 18213 kayıt, tabanın 9,1 katı\n```"}}},
		{Role: "assistant", ToolCalls: []copilot.ToolCall{{ID: "g", Name: "guided_prefetch", Input: json.RawMessage(`{}`)}}},
		{Role: "user", ToolResults: []copilot.ToolResult{{CallID: "g", Name: "guided_prefetch", Content: `{"p99_ms":2950}`}}},
	}
	v := &answerVerifyTurn{}
	v.addConversation(seededPairs(append([]copilot.ChatMessage{{Role: "user", Text: "neden oluyor bu"}}, seed...), 1))
	if !v.draftPasses(verifyTierLoop, "Gecikme 18.213 kayıt, tabanın 9,1 katı; p99 2.950 ms.") {
		t.Fatal("tohum değerleri kanıt sayılmadı")
	}
	if v.draftPasses(verifyTierLoop, "Gecikme 55.000 kayıt.") {
		t.Fatal("kanıtta olmayan değer geçti")
	}
}

// Sunum: kısa işaret + TEK dipnot satırı; satır içinde cümle tekrarı yok.
func TestVerifyFootnotePresentation(t *testing.T) {
	ans := "Oran 7,4; kimlik `ffee0099aabb`; hata \"Connection refused by upstream host\"."
	mm := verifyAnswer(verifyTierLoop, ans, verifyEvidence{Texts: []string{shapesEvidence}}, nil).mismatches()
	if len(mm) != 3 {
		t.Fatalf("3 uyuşmazlık bekleniyordu: %+v", mm)
	}
	got := seamWithEnforceNote(seamAnnotate(ans, mm), mm)
	if strings.Contains(got, "doğrulanmamış değer") {
		t.Fatal("eski satır içi cümle")
	}
	if strings.Count(got, verifyUnverifiedNote) != 3+1 { // 3 işaret + dipnot başı
		t.Fatalf("işaret sayısı: %q", got)
	}
	lines := strings.Split(got, "\n")
	foot := lines[len(lines)-1]
	if !strings.HasPrefix(foot, verifyFootnotePrefix) || !strings.Contains(foot, "«7,4»") || !strings.Contains(foot, "«ffee0099aabb»") ||
		strings.Count(got, verifyFootnotePrefix) != 1 {
		t.Fatalf("dipnot: %q", got)
	}
	// Uzun alıntı dipnotta kısaltılır; ≤6 değer + "+N".
	var many []verifyClaim
	for _, s := range []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", strings.Repeat("uzun ", 30)} {
		many = append(many, verifyClaim{Text: s})
	}
	f := verifyFootnoteTR(many)
	if !strings.Contains(f, " +2") || strings.Contains(f, strings.Repeat("uzun ", 12)) {
		t.Fatalf("dipnot sınırı: %q", f)
	}
}
