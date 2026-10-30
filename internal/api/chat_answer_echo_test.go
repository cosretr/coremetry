package api

// v0.10.1679 — operatör-bildirimi (CoSRE sohbeti, servisin HTTP endpoint'i
// soruldu): cevap gövdesinde ham "// build_link result" metni göründü.
// Kök neden: v0.10.1297 yankı süzgeci (answerToolEchoRe) yalnız satırı TEK
// BAŞINA kaplayan yankıyı siliyordu; satır içinde, markdown sarımında
// (`…`, **…**, > , - ) ya da arkasından içerik gelen ("…result: /services/…")
// yankı olduğu gibi yayına gidiyordu. Ayrıca sentetik ön-getirme adları
// (trace_drill, cluster_compare, operation_exceptions) yankı kapısında yoktu.

import (
	"strings"
	"testing"
)

func TestSanitizeAnswerToolEchoVariants(t *testing.T) {
	tools := chatToolNameSet(map[string]int{"build_link": 0, "search_traces": 0})
	cases := []struct{ name, in, want string }{
		{"tek başına satır (eski davranış)", "A\n// build_link result\nB", "A\nB"},
		{"satır sonunda", "Detay için bağlantı aşağıda. // build_link result\nB", "Detay için bağlantı aşağıda.\nB"},
		{"ardından içerik", "A\n// build_link result: /services/orders-api/endpoints\nB", "A\n/services/orders-api/endpoints\nB"},
		{"satır içi kod", "A\n`// build_link result`\nB", "A\nB"},
		{"kalın", "A\n**// build_link result**\nB", "A\nB"},
		{"alıntı", "A\n> // build_link result\nB", "A\nB"},
		{"liste", "A\n- // build_link result\nB", "A\nB"},
		{"Türkçe ek", "A\n# search_traces sonucu:\nB", "A\nB"},
		{"sentetik ön-getirme adı", "A\n// trace_drill result\nB", "A\nB"},
		{"CRLF", "A\r\n// build_link result\r\nB", "A\r\nB"},
		{"katalog dışı ad kalır", "A // not result\nB", "A // not result\nB"},
		{"sözcük sınırı: resultant kalır", "A // build_link resultant\nB", "A // build_link resultant\nB"},
		{"düz yorum kalır", "// orders-api yavaş\nB", "// orders-api yavaş\nB"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sanitizeAnswerLinks(c.in, nil, "", tools)
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
			if c.want != c.in && strings.Contains(got, "_link result") {
				t.Errorf("yankı sızdı: %q", got)
			}
		})
	}
}
