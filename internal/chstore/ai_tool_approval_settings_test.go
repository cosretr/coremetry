package chstore

// ai_tool_approval_settings_test.go — agent-core S4: `ai.toolApproval`
// varsayılanı KAPALI; normalize zaman aşımını her sınırda kıstırır, onaylayan
// rolü editor|admin'e indirir, araç listesini kanonikleştirir.

import (
	"reflect"
	"testing"
)

func TestDefaultAIToolApprovalIsOff(t *testing.T) {
	d := DefaultAIToolApproval()
	if d.Enabled || d.ExternalAll || len(d.Tools) != 0 || d.TimeoutS != 300 || d.ApproverMinRole != "editor" {
		t.Fatalf("varsayılan: %+v", d)
	}
	if got, ok := parseAIToolApproval(nil); !ok || !reflect.DeepEqual(got, d) {
		t.Fatalf("boş blob: %+v", got)
	}
	if _, ok := parseAIToolApproval([]byte("{")); ok {
		t.Fatal("bozuk blob ok=true")
	}
	if CurrentAIToolApproval().Enabled {
		t.Fatal("yüklenmemiş süreçte onay açık")
	}
}

func TestNormalizeAIToolApproval(t *testing.T) {
	cases := []struct {
		name string
		in   AIToolApproval
		want AIToolApproval
	}{
		{"timeout 0 → varsayılan", AIToolApproval{}, AIToolApproval{Tools: []string{}, TimeoutS: 300, ApproverMinRole: "editor"}},
		{"timeout alt sınır", AIToolApproval{TimeoutS: 5}, AIToolApproval{Tools: []string{}, TimeoutS: 30, ApproverMinRole: "editor"}},
		{"timeout negatif", AIToolApproval{TimeoutS: -1}, AIToolApproval{Tools: []string{}, TimeoutS: 30, ApproverMinRole: "editor"}},
		{"timeout üst sınır", AIToolApproval{TimeoutS: 99999}, AIToolApproval{Tools: []string{}, TimeoutS: 3600, ApproverMinRole: "editor"}},
		{"timeout içeride", AIToolApproval{TimeoutS: 120}, AIToolApproval{Tools: []string{}, TimeoutS: 120, ApproverMinRole: "editor"}},
		{"rol admin", AIToolApproval{ApproverMinRole: " Admin "}, AIToolApproval{Tools: []string{}, TimeoutS: 300, ApproverMinRole: "admin"}},
		{"rol viewer → editor", AIToolApproval{ApproverMinRole: "viewer"}, AIToolApproval{Tools: []string{}, TimeoutS: 300, ApproverMinRole: "editor"}},
		{"araçlar kanonik", AIToolApproval{Enabled: true, Tools: []string{" b ", "a", "b", ""}}, AIToolApproval{Enabled: true, Tools: []string{"a", "b"}, TimeoutS: 300, ApproverMinRole: "editor"}},
	}
	for _, c := range cases {
		if got := NormalizeAIToolApproval(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
