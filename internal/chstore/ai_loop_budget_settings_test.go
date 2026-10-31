package chstore

// ai_loop_budget_settings_test.go — CoSRE v2 araç seti dilim 4 (tools-audit §10 G3):
// `ai.loopBudget` blobunun ayrıştırması. Negatif değer 0'a (= sabit) iner.

import "testing"

func TestParseAILoopBudget(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want AILoopBudget
		ok   bool
	}{
		{"boş", "", AILoopBudget{}, true},
		{"bozuk", "{", AILoopBudget{}, false},
		{"tam", `{"maxCalls":3,"maxRounds":2,"maxTokens":9000,"maxSeconds":120,"toolTextBytes":1024}`,
			AILoopBudget{MaxCalls: 3, MaxRounds: 2, MaxTokens: 9000, MaxSeconds: 120, ToolTextBytes: 1024}, true},
		{"negatif sıfıra", `{"maxCalls":-1,"maxRounds":-2,"maxTokens":-3,"maxSeconds":-4,"toolTextBytes":-5}`, AILoopBudget{}, true},
		{"eksik alan", `{"maxRounds":4}`, AILoopBudget{MaxRounds: 4}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseAILoopBudget([]byte(c.raw))
			if ok != c.ok || got != c.want {
				t.Fatalf("got %+v,%v want %+v,%v", got, ok, c.want, c.ok)
			}
		})
	}
}
