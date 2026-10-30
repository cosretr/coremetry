package chstore

// ai_exchanges_subject_test.go — v0.10.1645. Operator-reported: /ai
// "Açıklama turu" çekmecesi `Bu trace'i açıkla · eea6c7b0…` gösteriyordu;
// eski satırlarda tam kimlik yalnız cosre_interactions'ın trace_id /
// exception_id / problem_id kolonlarında. Okuma bu kolonları taşır, birleşim
// AIExchange'e geçirir.

import (
	"strings"
	"testing"
)

func TestAIExchangeConversationSQLCarriesSubjectIDs(t *testing.T) {
	for _, col := range []string{"trace_id", "exception_id", "problem_id"} {
		if !strings.Contains(aiExchangeConversationSQL, col) {
			t.Errorf("konuşma okuması %s taşımıyor: %s", col, aiExchangeConversationSQL)
		}
	}
}

func TestAssembleAIExchangesSubjectIDs(t *testing.T) {
	const a = "bbbb0000bbbb0000bbbb0000bbbb0000"
	const tr = "0af7651916cd43dd8448eb211c80319c"
	turns := []aiTurnRow{{ExchangeID: a + ":turn", Surface: "cosre-turn:explain", Status: "ok"}}
	cases := []struct {
		name string
		conv aiExchangeConv
	}{
		{"trace", aiExchangeConv{ConversationID: "conv-1", TraceID: tr}},
		{"exception without conversation", aiExchangeConv{ExceptionID: "a1b2c3d4e5f60718"}},
		{"problem", aiExchangeConv{ProblemID: "p-1234567890"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			x := assembleAIExchanges(turns, nil, nil, map[string]aiExchangeConv{a: c.conv})[0]
			if x.TraceID != c.conv.TraceID || x.ExceptionID != c.conv.ExceptionID || x.ProblemID != c.conv.ProblemID {
				t.Fatalf("bağlam kimlikleri geçmedi: %+v", x)
			}
		})
	}
}
