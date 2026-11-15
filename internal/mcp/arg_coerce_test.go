package mcp

import (
	"encoding/json"
	"testing"
)

func TestCoerceNumericArgs(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"service": map[string]any{"type": "string"},
		"range_s": map[string]any{"type": "integer", "minimum": 0},
		"limit":   map[string]any{"type": []any{"integer", "null"}},
		"ratio":   map[string]any{"type": "number"},
		"id":      map[string]any{"type": []any{"string", "integer"}},
	}}
	cases := []struct {
		in, want string
		changed  bool
	}{
		{`{"service":"pay","range_s":"3600","limit":" 40 "}`, `{"limit":40,"range_s":3600,"service":"pay"}`, true},
		{`{"ratio":"0.5"}`, `{"ratio":0.5}`, true},
		{`{"range_s":"12.5"}`, `{"range_s":"12.5"}`, false}, // tam sayı değil → doğrulama söyler
		{`{"range_s":"saat"}`, `{"range_s":"saat"}`, false},
		{`{"service":"123"}`, `{"service":"123"}`, false}, // string alanı dokunulmaz
		{`{"id":"42"}`, `{"id":"42"}`, false},             // string kabul eden alan
		{`{"range_s":3600}`, `{"range_s":3600}`, false},
		{``, ``, false},
	}
	for _, c := range cases {
		got, changed := CoerceNumericArgs(schema, json.RawMessage(c.in))
		if string(got) != c.want || changed != c.changed {
			t.Errorf("%s → %s (%v), beklenen %s (%v)", c.in, got, changed, c.want, c.changed)
		}
		if changed && len(ValidateArgs(schema, got)) != 0 {
			t.Errorf("%s: düzeltilmiş argüman doğrulamadan geçmeli: %v", c.in, ValidateArgs(schema, got))
		}
	}
}
