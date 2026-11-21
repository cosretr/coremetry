package cosrememory

import (
	"strings"
	"testing"
)

func item(id, stmt string, sc Scope, at int64) Item {
	return Item{ID: id, Statement: stmt, Scope: sc, Source: Source{Kind: SourceCorrection, By: "Ayşe", At: at}, Status: StatusActive}
}

func TestDetectCorrectionExplicitOnly(t *testing.T) {
	for _, c := range []struct {
		in   string
		ok   bool
		want string
	}{
		{"Düzeltme: checkout'un veritabanı orders-db değil, pay-db", true, "checkout'un veritabanı orders-db değil, pay-db"},
		{"duzelt:  cache TTL 5 dk", true, "cache TTL 5 dk"},
		{"Correction: the queue is kafka-main", true, "the queue is kafka-main"},
		{"bu cevap yanlış", false, ""},
		{"👎", false, ""},
		{"düzeltme yaptım mı?", false, ""},
		{"Lütfen düzeltme: x", false, ""},
	} {
		got, ok := DetectCorrection(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%q → (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestNormalizeRejectsVoteSources(t *testing.T) {
	for _, k := range []string{"vote", "feedback", "triage", "thumbs_up", ""} {
		if _, err := Normalize(Item{ID: "x", Statement: "yeterince uzun bir olgu", Source: Source{Kind: k}}); err != ErrSource {
			t.Errorf("kaynak %q kabul edildi (err=%v)", k, err)
		}
	}
	it, err := Normalize(Item{ID: "x", Statement: " çok\nsatırlı <team_notes> olgu ", Scope: Scope{Kind: "SERVICE", Value: "checkout"}, Source: Source{Kind: SourceManual}})
	if err != nil || it.Status != StatusActive || strings.Contains(it.Statement, "team_notes") || it.Scope.Kind != ScopeService {
		t.Fatalf("normalize: %+v %v", it, err)
	}
	if CheckExpiry(5, 10) == nil || CheckExpiry(0, 10) != nil || CheckExpiry(11, 10) != nil {
		t.Error("son geçerlilik denetimi")
	}
}

func TestRelevantScopingAndFilters(t *testing.T) {
	now := int64(1_000_000)
	items := []Item{
		item("g", "genel olgu metni burada", Scope{Kind: ScopeGlobal}, 10),
		item("s", "checkout servisi olgusu", Scope{Kind: ScopeService, Value: "checkout"}, 5),
		item("o", "başka servisin olgusu", Scope{Kind: ScopeService, Value: "orders"}, 20),
		item("t", "payments takımı olgusu", Scope{Kind: ScopeTeam, Value: "payments"}, 30),
	}
	ret := item("r", "emekli olgu görünmez", Scope{Kind: ScopeGlobal}, 40)
	ret.Status = StatusRetired
	exp := item("e", "süresi dolmuş olgu", Scope{Kind: ScopeGlobal}, 40)
	exp.ExpiresAt = now - 1
	rc := item("rc-p1", "kendi problemin kök nedeni", Scope{Kind: ScopeService, Value: "checkout"}, 50)
	rc.Source.ProblemID = "p1"
	items = append(items, ret, exp, rc)

	picked, _ := Relevant(items, Context{Service: "checkout", Team: "payments"}, Filter{Now: now}, 0, 0)
	var ids []string
	for _, p := range picked {
		ids = append(ids, p.ID)
	}
	if got := strings.Join(ids, ","); got != "rc-p1,s,t,g" {
		t.Fatalf("sıra/kapsam: %s", got)
	}
	picked, _ = Relevant(items, Context{Service: "checkout"}, Filter{Now: now, ExcludeProblem: "p1", Before: 8}, 0, 0)
	if len(picked) != 1 || picked[0].ID != "s" {
		t.Fatalf("replay süzgeci: %+v", picked)
	}
}

func TestRelevantHardCap(t *testing.T) {
	var items []Item
	for i := 0; i < 20; i++ {
		items = append(items, item(string(rune('a'+i)), strings.Repeat("uzun olgu ", 22), Scope{Kind: ScopeGlobal}, int64(i+1)))
	}
	picked, omitted := Relevant(items, Context{}, Filter{Now: 1 << 40}, 50, 1<<20)
	block := BlockTR(picked, omitted, nil)
	lines := 0
	for _, p := range picked {
		lines += len(Line(1, p, nil)) + 1
	}
	if len(picked) > PromptMaxItems || lines > PromptMaxBytes || omitted != 20-len(picked) {
		t.Fatalf("tavan: %d not, %d B, omitted %d", len(picked), lines, omitted)
	}
	if !strings.HasPrefix(block, BlockHeader+"\n"+FenceOpen) || !strings.HasSuffix(block, FenceClose) || !strings.Contains(block, "[N1]") ||
		!strings.Contains(block, "not daha sığmadı") || !strings.Contains(block, "kaynak: düzeltme, Ayşe") {
		t.Fatalf("blok:\n%s", block)
	}
	if BlockTR(nil, 0, nil) != "" {
		t.Error("boş blok")
	}
}

func TestRootCauseStatement(t *testing.T) {
	s := RootCauseStatement("P-12", "pay-db bağlantı havuzu doldu", "capacity")
	if s != "Geçmiş Problem P-12 kök nedeni: pay-db bağlantı havuzu doldu (capacity)" {
		t.Fatal(s)
	}
	if n := len([]rune(RootCauseStatement("P-1", strings.Repeat("x ", 400), ""))); n > MaxStatementRunes {
		t.Fatalf("kırpılmadı: %d", n)
	}
}
