package chstore

// v0.10.1280 — CoSRE adım 8: kalıcılık + geri bildirim → ai_calls.
//
// 👍/👎 artık somut ai_calls satırına bağlanıyor (ai_feedback.call_id) ve
// 👎'nin hazır nedeni (reason) saklanıyor; cosre_interactions çip başına
// kanıt özeti (tool_evidence) taşıyor. Üç kolon da düşük hacimli tablolara
// `ADD COLUMN IF NOT EXISTS` ile geliyor — iki-boot sözleşmesi: küme
// kipinde ALTER ertelenir, INSERT/SELECT probe görene dek kolonları ANMAZ.
// Bu dosya şunları pinler:
//   - migration idempotent: ifadeler `alters` diliminde, eleyicinin
//     tanıdığı biçimde; kolon varken ikinci boot hiçbirini GÖNDERMEZ,
//     yokken hepsini gönderir; aynı planı iki kez koşmak aynı sonucu verir;
//   - INSERT ve 👎 okumalarının kolon sayısı == arg/Scan hedefi, iki dalda;
//   - /ai etkileşim süzgecinin SQL'i (GLOBAL IN, bind sırası, kapalı küme).

import (
	"os"
	"strings"
	"testing"
	"time"
)

var v1277Alters = map[string]string{
	"ai_feedback.call_id":              "String DEFAULT ''",
	"ai_feedback.reason":               "LowCardinality(String) DEFAULT ''",
	"cosre_interactions.tool_evidence": "Array(String)",
}

func TestV1277MigrationIdempotent(t *testing.T) {
	alters := migrateDDLSlice(t, "alters")
	var mine []string
	for _, q := range alters {
		tbl, col, ok := ddlAddsColumn(q)
		if !ok {
			continue
		}
		want, hit := v1277Alters[tbl+"."+col]
		if !hit {
			continue
		}
		if !strings.Contains(q, want) {
			t.Errorf("%s.%s tipi/DEFAULT'u %q değil:\n%s", tbl, col, want, q)
		}
		mine = append(mine, q)
	}
	if len(mine) != len(v1277Alters) {
		t.Fatalf("alters diliminde %d/%d v0.10.1280 ALTER'ı bulundu: %v", len(mine), len(v1277Alters), mine)
	}
	// Kolon yokken (taze kurulum / ilk boot) hepsi gider.
	send, skipped := planAlterDDL(mine, map[string]bool{})
	if len(send) != len(mine) || skipped != 0 {
		t.Errorf("kolonlar yokken gönderilen %d, elenen %d", len(send), skipped)
	}
	// Kolon varken (ikinci boot) hiçbiri gitmez — idempotent ve ucuz.
	have := map[string]bool{}
	for k := range v1277Alters {
		have[k] = true
	}
	for run := 0; run < 2; run++ {
		send, skipped = planAlterDDL(mine, have)
		if len(send) != 0 || skipped != len(mine) {
			t.Errorf("koşum %d: kolonlar varken gönderilen %v", run, send)
		}
	}
	// Küme kipinde yalnız yerel parçada görünen kolon da eler (existingColumns
	// `_local` sonekini düşürüp bildirimsel adı da yazar).
	send, _ = planAlterDDL(mine, map[string]bool{"ai_feedback.call_id": true})
	for _, q := range send {
		if strings.Contains(q, " call_id ") {
			t.Errorf("var olan call_id yeniden gönderildi: %s", q)
		}
	}
	if len(send) != len(mine)-1 {
		t.Errorf("yalnız eksik kolonlar gitmeli: %v", send)
	}
}

// v0.10.1393 — INSERT dalı (UpsertAIFeedback) yazma ucuyla birlikte silindi;
// 👎 okumasının kolon/Scan eşleşmesi iki dalda pinli kalır.
func TestNegFeedbackReadBranches(t *testing.T) {
	for _, cc := range []bool{false, true} {
		cols := negFeedbackCols(cc)
		var r NegativeFeedbackCall
		if n, m := len(strings.Split(cols, ",")), len(negFeedbackScan(&r, cc)); n != m {
			t.Errorf("cc=%v: 👎 okuması %d kolon, %d Scan hedefi", cc, n, m)
		}
	}
}

func TestAIFeedbackProbeTargetsInsertTable(t *testing.T) {
	for file, want := range map[string]string{
		"ai_feedback.go":        "table = 'ai_feedback' AND name IN ('call_id', 'reason')",
		"cosre_interactions.go": "table = 'cosre_interactions' AND name IN ('tool_evidence', 'toolset', 'blocks')", // v0.10.1302 — tek probe; v0.10.1444 + blocks
	} {
		src := readFileT(t, file)
		if !strings.Contains(src, want) {
			t.Errorf("%s: probe INSERT hedefine bakmalı (%q)", file, want)
		}
		if strings.Contains(src, "_local'") {
			t.Errorf("%s: probe yerel parçaya bakmamalı", file)
		}
	}
	// Probe'lar boot'ta ve ertelenen DDL sonrası yeniden koşar.
	for _, f := range []string{"store.go", "ddl_defer.go"} {
		src := readFileT(t, f)
		for _, call := range []string{"probeAIFeedbackCallColumns(ctx)", "probeCosreInteractionsColumns(ctx)"} {
			if !strings.Contains(src, call) {
				t.Errorf("%s %s çağırmıyor", f, call)
			}
		}
	}
}

func TestAIExchangeFeedbackFilterSQL(t *testing.T) {
	cases := []struct {
		in, norm, cond string
	}{
		{"", "", ""},
		{"down", "down", "verdict = -1"},
		{"up", "up", "verdict = 1"},
		{"rated", "rated", "verdict != 0"},
		{"DOWN", "", ""},
		{"down' OR 1=1 --", "", ""},
	}
	from := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	for _, c := range cases {
		if got := NormalizeAIExchangeFeedback(c.in); got != c.norm {
			t.Errorf("Normalize(%q) = %q, beklenen %q", c.in, got, c.norm)
		}
		q := aiExchangeTurnsSQL(true, ListAIExchangesParams{Feedback: c.norm})
		args := aiExchangeTurnsArgs(ListAIExchangesParams{From: from, To: to, Limit: 50, Feedback: c.norm})
		if n := strings.Count(q, "?"); n != len(args) {
			t.Errorf("%q: %d bind, %d arg", c.in, n, len(args))
		}
		if args[len(args)-2] != 50 || args[len(args)-1] != 0 {
			t.Errorf("%q: LIMIT, OFFSET son iki bind olmalı (v0.10.1646): %v", c.in, args)
		}
		if c.cond == "" {
			if strings.Contains(q, "ai_feedback") {
				t.Errorf("%q: süzgeçsiz sorgu ai_feedback okumamalı:\n%s", c.in, q)
			}
			continue
		}
		for _, want := range []string{"GLOBAL IN", "ai_feedback FINAL", "WHERE " + c.cond, aiCallsTurnCond, "LIMIT ?"} {
			if !strings.Contains(q, want) {
				t.Errorf("%q: %q eksik:\n%s", c.in, want, q)
			}
		}
		// Süzgeç LIMIT'ten ÖNCE (WHERE içinde).
		if strings.Index(q, "GLOBAL IN") > strings.Index(q, "LIMIT ?") {
			t.Errorf("%q: süzgeç LIMIT'ten sonra", c.in)
		}
	}
	if !strings.Contains(aiExchangeConversationSQL, "FROM cosre_interactions") || strings.Count(aiExchangeConversationSQL, "?") != 4 ||
		!strings.Contains(aiExchangeConversationSQL, "max_execution_time") {
		t.Errorf("konuşma okuması: %s", aiExchangeConversationSQL)
	}
}

func TestAssembleAIExchangesFeedbackCallAndConversation(t *testing.T) {
	const a = "aaaa0000aaaa0000aaaa0000aaaa0000"
	turns := []aiTurnRow{{ExchangeID: a + ":turn", Surface: "cosre-turn:loop", Status: "aborted"}}
	calls := []aiExchangeChild{{Root: a, Call: AIExchangeCall{ID: "c1", Surface: "chat", Model: "m"}}}
	fb := map[string]aiExchangeVerdict{a: {Verdict: -1, Comment: "kanıt yok", Reason: "incomplete", CallID: "c1"}}
	conv := map[string]aiExchangeConv{a: {ConversationID: "conv-9", Turn: 3}}
	got := assembleAIExchanges(turns, calls, fb, conv)
	if len(got) != 1 {
		t.Fatalf("%+v", got)
	}
	x := got[0]
	if x.Feedback != -1 || x.FeedbackReason != "incomplete" || x.FeedbackCallID != "c1" || x.FeedbackNote != "kanıt yok" {
		t.Errorf("oy alanları: %+v", x)
	}
	if x.ConversationID != "conv-9" || x.Turn != 3 || x.Status != "aborted" {
		t.Errorf("konuşma/sonuç: %+v", x)
	}
	// Konuşma haritası yokken (soft-fail) alanlar boş kalır.
	if y := assembleAIExchanges(turns, calls, nil, nil)[0]; y.ConversationID != "" || y.Feedback != 0 {
		t.Errorf("boş haritalar: %+v", y)
	}
}

func readFileT(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
