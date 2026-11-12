package api

// Log Search Faz 6 dilim 6.6 — doğal dilden log sorgusu: model çıktısının
// doğrulanması (SAF). Uç sorgu çalıştırmaz; burada yalnız çıktı kapısı.

import (
	"strings"
	"testing"
)

func TestFinishLogsNL(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantValid bool
		wantQuery string
		wantSev   int
		wantRange string
		wantErr   string
	}{
		{"tam", `{"query":"service.name:\"orders-api\" and (timeout or \"timed out\")","severity":"error","range":"1h","explain":"x"}`,
			true, `service.name:"orders-api" and (timeout or "timed out")`, 17, "1h", ""},
		{"gevezelik + çit", "İşte:\n```json\n{\"query\":\"level:error\",\"severity\":\"\",\"range\":\"\",\"explain\":\"\"}\n```", true, "level:error", 0, "", ""},
		{"bilinmeyen seviye ve aralık düşer", `{"query":"timeout","severity":"critical","range":"90m","explain":""}`, true, "timeout", 0, "", ""},
		{"yalnız seviye", `{"query":"","severity":"warn","range":"","explain":""}`, true, "", 13, "", ""},
		{"boş", `{"query":"","severity":"","range":"","explain":""}`, false, "", 0, "", "sorgu üretmedi"},
		{"ayrışmaz", `{"query":"level:error and (timeout","severity":"","range":"","explain":""}`, false, "level:error and (timeout", 0, "", "ayrışmadı"},
		{"JSON değil", `bilmiyorum`, false, "", 0, "", "JSON"},
		{"çok uzun", `{"query":"` + strings.Repeat("a", logsNLQueryMax+1) + `","severity":"","range":"","explain":""}`, false, "", 0, "", "çok uzun"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := finishLogsNL(c.raw)
			if got.Valid != c.wantValid || got.SeverityMin != c.wantSev || got.Range != c.wantRange {
				t.Fatalf("got %+v", got)
			}
			if c.wantQuery != "" && got.Query != c.wantQuery {
				t.Errorf("query %q", got.Query)
			}
			if c.wantErr != "" && !strings.Contains(got.Error, c.wantErr) {
				t.Errorf("error %q, %q bekleniyordu", got.Error, c.wantErr)
			}
			if c.wantValid && got.Error != "" {
				t.Errorf("geçerli sonuçta hata: %q", got.Error)
			}
		})
	}
}

func TestCleanLogsNLFields(t *testing.T) {
	in := []string{" message ", "service.name", "message", "", "bad field", `q"x`, strings.Repeat("f", logsNLFieldLen+1)}
	for i := 0; i < logsNLFieldsMax+10; i++ {
		in = append(in, "f"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	out := cleanLogsNLFields(in)
	if len(out) != logsNLFieldsMax {
		t.Fatalf("len %d", len(out))
	}
	seen := map[string]bool{}
	for _, f := range out {
		if seen[f] || strings.ContainsAny(f, " \"") {
			t.Fatalf("geçersiz / tekrar: %q", f)
		}
		seen[f] = true
	}
	if !seen["message"] || !seen["service.name"] {
		t.Fatalf("baştaki alanlar kalmalı: %v", out[:3])
	}
	if s := renderLogsNLInput("hata", nil); !strings.Contains(s, "ALANLAR: bilinmiyor") {
		t.Fatalf("alan yok satırı: %q", s)
	}
}

func TestLogsNLQuerySchemaEnums(t *testing.T) {
	sch := logsNLQuerySchema()
	props, _ := sch["properties"].(map[string]any)
	sev, _ := props["severity"].(map[string]any)
	enum, _ := sev["enum"].([]string)
	if len(enum) != len(logsNLSeverity)+1 || enum[0] != "" {
		t.Fatalf("severity enum %v", enum)
	}
}
