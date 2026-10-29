package api

import (
	"encoding/json"
	"strings"
	"testing"
)

// chat_budget_step_test.go — v0.10.1594 (CoSRE v2 Faz 1, R6): bütçe adımı
// yapısal sayaç taşır (v2 istemcisi TR etiketini ayrıştırmasın) ve serbest
// döngünün araç çipi geçerli JSON argümanı argsJson olarak taşır. v1 etiketi
// ve args metni aynen kalır; v1 UI yeni alanları okumaz.

func TestChatBudgetStep(t *testing.T) {
	cases := []struct {
		name  string
		label string
		b     chatBudget
		want  string
	}{
		{"start", chatBudgetLabelTR(6, 5), chatBudget{MaxCalls: 6, MaxRounds: 5},
			`{"budget":{"maxCalls":6,"maxRounds":5,"slotsUsed":0,"executed":0,"skipped":0,"roundsUsed":0,"exhausted":false},"label":"araştırma bütçesi: 6 araç · 5 tur"}`},
		{"exhausted", "x", chatBudget{MaxCalls: 6, MaxRounds: 5, SlotsUsed: 6, Executed: 4, Skipped: 2, RoundsUsed: 5, Exhausted: true},
			`{"budget":{"maxCalls":6,"maxRounds":5,"slotsUsed":6,"executed":4,"skipped":2,"roundsUsed":5,"exhausted":true},"label":"x"}`},
	}
	for _, c := range cases {
		b, _ := json.Marshal(chatBudgetStep(c.label, c.b))
		if string(b) != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, b, c.want)
		}
	}
}

func TestEmitStepChipInputBounds(t *testing.T) {
	big := `{"q":"` + strings.Repeat("a", 8<<10) + `"}`
	cases := []struct {
		name    string
		input   json.RawMessage
		hasJSON bool
	}{
		{"valid", json.RawMessage(`{"service":"orders-api"}`), true},
		{"invalid", json.RawMessage(`{"service":`), false},
		{"empty", nil, false},
		{"over 8 KiB", json.RawMessage(big), false},
	}
	for _, c := range cases {
		var got map[string]any
		emit := withStepIDs(func(kind string, p any) {
			if kind == "step" {
				got = p.(map[string]any)
			}
		})
		n := emitStepChipInput(emit, "get_service_health", string(c.input), "", c.input)
		if n != 1 {
			t.Errorf("%s: i=%d", c.name, n)
		}
		_, has := got["argsJson"]
		if has != c.hasJSON {
			t.Errorf("%s: argsJson var=%v, want %v", c.name, has, c.hasJSON)
		}
		if got["args"] != string(c.input) || got["tool"] != "get_service_health" {
			t.Errorf("%s: v1 alanları değişti: %v", c.name, got)
		}
	}
}
