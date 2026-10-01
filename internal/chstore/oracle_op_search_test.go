package chstore

import (
	"strings"
	"testing"
)

// v0.10.1002 — Oracle operasyon adı araması (oracle_op_search.go): sorgu zaman
// sınırlı + LIMIT + max_execution_time, metin ve kaynak kimlikleri bind-arg;
// fonksiyon kodu kaynağa göre error_code ya da attribute. İfade yerel
// ClickHouse'ta doğrulandı (iki kaynak, dolgulu kod, trace'siz satır).

func TestOracleOpSearchSQL(t *testing.T) {
	attrOnly, mixed := oracleOpSearchSQL(0), oracleOpSearchSQL(2)
	for name, q := range map[string]string{"attr": attrOnly, "mixed": mixed} {
		for _, w := range []string{
			"FROM oracle_error_log", "WHERE time >= ? AND time < ? AND operation_code != ''",
			"positionCaseInsensitiveUTF8(operation_code, ?) > 0", "argMaxIf(trace_id, time, trace_id != '')",
			"GROUP BY operation_code", "LIMIT ?", "SETTINGS max_execution_time = 5",
		} {
			if !strings.Contains(q, w) {
				t.Errorf("%s: SQL %q içermeli", name, w)
			}
		}
		if strings.Contains(q, "FINAL") {
			t.Errorf("%s: arama FINAL taşımamalı (yaklaşık sayı yeter)", name)
		}
	}
	if strings.Contains(attrOnly, "source_id IN") || strings.Count(attrOnly, "?") != 5 {
		t.Errorf("attribute yolu: 5 yer tutucu, IN yok:\n%s", attrOnly)
	}
	if !strings.Contains(mixed, "if(source_id IN (?,?), trimBoth(error_code), ") || strings.Count(mixed, "?") != 7 {
		t.Errorf("karışık yol: kaynak başına yer tutucu:\n%s", mixed)
	}
}

func TestClampOracleOpSearch(t *testing.T) {
	for in, want := range map[int]int{0: 6, -3: 6, 6: 6, 20: 20, 21: 20, 500: 20} {
		if got := clampOracleOpSearch(in); got != want {
			t.Errorf("limit %d → %d, istenen %d", in, got, want)
		}
	}
}

// Süzgeç anahtarı harf duyarlı: link, probe'un DOĞRULADIĞI yazımla kurulur.
func TestPromotedAttrSpelling(t *testing.T) {
	prev := promotedColsPtr.Load()
	t.Cleanup(func() { promotedColsPtr.Store(prev) })
	lower := map[string]string{"function_code": "attr_function_code", "channel_code": "attr_channel_code"}
	promotedColsPtr.Store(&lower)
	if got := PromotedAttrSpelling("FUNCTION_CODE", "function_code"); got != "function_code" {
		t.Errorf("küçük harf kayıtlı: %q", got)
	}
	both := map[string]string{"FUNCTION_CODE": "attr_function_code", "function_code": "attr_function_code"}
	promotedColsPtr.Store(&both)
	if got := PromotedAttrSpelling("FUNCTION_CODE", "function_code"); got != "FUNCTION_CODE" {
		t.Errorf("ikisi de kayıtlı → ilk aday: %q", got)
	}
	none := map[string]string{}
	promotedColsPtr.Store(&none)
	if got := PromotedAttrSpelling("FUNCTION_CODE", "function_code"); got != "" {
		t.Errorf("kayıt yok → boş: %q", got)
	}
}
