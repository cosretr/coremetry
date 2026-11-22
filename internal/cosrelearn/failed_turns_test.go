package cosrelearn

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var ftT0 = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

func ftTurn(id, user string, at time.Duration, q string) chstore.CosreInteraction {
	return chstore.CosreInteraction{ExchangeID: id, ConversationID: "c-" + user, UserEmail: user + "@example.com",
		CreatedAt: ftT0.Add(at), TotalMs: 2000, Question: q, Status: "ok", Tier: "guided"}
}

func ftReasons(turns []FailedTurn) map[string][]string {
	out := map[string][]string{}
	for _, t := range turns {
		out[t.ExchangeID] = t.Reasons
	}
	return out
}

func TestNormalizeQuestionFoldsTurkish(t *testing.T) {
	if got := NormalizeQuestion("  Noldu?? ŞİMDİ  çalışmadı!"); got != "noldu simdi calismadi" {
		t.Fatalf("got %q", got)
	}
}

func TestIsComplaint(t *testing.T) {
	yes := []string{"noldu", "Ne oldu?", "tekrar dene", "cevap vermedin", "yanlış", "o değil", "hayır bu değil, gecikmeyi sordum"}
	no := []string{"checkout servisinde yanlış giriş hataları kaç tane oldu son bir saatte", "checkout sağlığı nasıl?", "", "kafka lag var mı"}
	for _, q := range yes {
		if !IsComplaint(q) {
			t.Errorf("%q şikâyet sayılmalı", q)
		}
	}
	for _, q := range no {
		if IsComplaint(q) {
			t.Errorf("%q şikâyet sayılmamalı", q)
		}
	}
}

func TestProviderError(t *testing.T) {
	if !ProviderError("openai-compat 400: tools must not be empty") || !ProviderError("gateway status 503 Service Unavailable") {
		t.Fatal("4xx/5xx sağlayıcı hatası yakalanmalı")
	}
	if ProviderError("tool search_logs failed: no rows") {
		t.Fatal("sayısız hata sağlayıcı hatası değil")
	}
}

func TestTurnFailReasons(t *testing.T) {
	r := ftTurn("x", "a", 0, "q")
	r.Status, r.ErrorMsg = "error", "context deadline exceeded"
	r.Tools = []chstore.CosreTool{{Name: "search_logs", Error: "boom"}, {Name: "skip", Error: "skipped"}, {Name: "ok", OK: true}}
	if got := TurnFailReasons(r); !reflect.DeepEqual(got, []string{FailToolError, FailTimeout}) {
		t.Fatalf("got %v", got)
	}
	r = ftTurn("y", "a", 0, "q")
	r.EmptyAnswer, r.Tier = true, "clarify"
	if got := TurnFailReasons(r); !reflect.DeepEqual(got, []string{FailEmpty, FailClarifyOnly}) {
		t.Fatalf("got %v", got)
	}
	r = ftTurn("z", "a", 0, "q")
	r.Status, r.ErrorMsg = "error", "context canceled"
	if got := TurnFailReasons(r); len(got) != 0 {
		t.Fatalf("iptal başarısızlık değil: %v", got)
	}
	r.Status, r.ErrorMsg = "error", "openai-compat 500: internal"
	if got := TurnFailReasons(r); !reflect.DeepEqual(got, []string{FailProviderError}) {
		t.Fatalf("got %v", got)
	}
}

func TestDetectFailedTurnsCrossTurn(t *testing.T) {
	rows := []chstore.CosreInteraction{
		ftTurn("a1", "ali", 0, "checkout servisinin sağlığı nasıl?"),
		ftTurn("a2", "ali", 30*time.Second, "Checkout servisinin sağlığı nasıl"), // ≤60 sn yeniden sorma
		ftTurn("a3", "ali", 3*time.Minute, "noldu"),                              // a2'ye şikâyet
		ftTurn("b1", "bob", 0, "kafka lag var mı"),
		ftTurn("b2", "bob", 5*time.Minute, "kafka lag var mı"), // 60 sn dışında: yeniden sorma değil
		ftTurn("c1", "cem", 0, "payments trace bul"),
		ftTurn("c2", "cem", 10*time.Second, "payments trace bul"), // çip tıkı: yeniden sorma değil
	}
	sigs := []chstore.CosreSignal{
		{ExchangeID: "b1", Kind: SigVerifyMismatch},
		{ExchangeID: "c1", Kind: SigSuggestionClick},
		{ExchangeID: "b2", Kind: SigThumbsDown}, // oy GİRDİ DEĞİL
		{ExchangeID: "zz", Kind: SigVerifyMismatch},
	}
	got := ftReasons(DetectFailedTurns(rows, sigs))
	want := map[string][]string{
		"a1": {FailReask},
		"a2": {FailComplaint},
		"b1": {FailVerifyMismatch},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestDetectFailedTurnsOrderAndRetry(t *testing.T) {
	r1 := ftTurn("r1", "u", 0, "endpoint neden yavaş")
	r2 := ftTurn("r2", "u", 10*time.Minute, "endpoint neden yavaş")
	r2.Attempt = "retry"
	out := DetectFailedTurns([]chstore.CosreInteraction{r2, r1}, nil)
	if len(out) != 1 || out[0].ExchangeID != "r1" || out[0].NextQuestion != r2.Question {
		t.Fatalf("got %+v", out)
	}
}

func TestStoredFlagsMerge(t *testing.T) {
	rows := []chstore.CosreInteraction{ftTurn("s1", "u", 0, "q one")}
	sigs := []chstore.CosreSignal{{ExchangeID: "s1", Kind: SigFailedTurn, Detail: FailedTurnDetail([]string{FailReask, "bogus"})}}
	if got := ftReasons(DetectFailedTurns(rows, sigs)); !reflect.DeepEqual(got["s1"], []string{FailReask}) {
		t.Fatalf("got %v", got)
	}
}

func TestClusterFailedTurnsDeterministic(t *testing.T) {
	mk := func(id string, at time.Duration, q, user string) FailedTurn {
		return FailedTurn{ExchangeID: id, At: ftT0.Add(at), Question: q, UserEmail: user, Reasons: []string{FailReask}}
	}
	turns := []FailedTurn{
		mk("1", 0, "checkout servisi kafka lag var mı", "a"),
		mk("2", time.Hour, "checkout kafka lag", "b"),
		mk("3", 2*time.Hour, "payments loglarında hata mesajı", "a"),
		mk("4", 3*time.Hour, "checkout servisinde kafka lag ne durumda", "c"),
		mk("5", 8*24*time.Hour, "checkout kafka lag", "a"), // sonraki hafta
		mk("6", 4*time.Hour, "??", "a"),
		mk("7", 5*time.Hour, "?!", "b"),
	}
	a := ClusterFailedTurns(turns)
	rev := append([]FailedTurn(nil), turns...)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	if b := ClusterFailedTurns(rev); !reflect.DeepEqual(a, b) {
		t.Fatalf("girdi sırası sonucu değiştirdi:\n%+v\n%+v", a, b)
	}
	if a[0].Week != ISOWeek(ftT0.Add(8*24*time.Hour)) || a[0].Count != 1 {
		t.Fatalf("en yeni hafta önce: %+v", a[0])
	}
	top := a[1]
	if top.Count != 3 || top.Users != 3 || !reflect.DeepEqual(top.ExchangeIDs, []string{"1", "2", "4"}) {
		t.Fatalf("kafka kümesi: %+v", top)
	}
	if top.Label != "checkout kafka lag durumda" {
		t.Fatalf("etiket %q", top.Label)
	}
	if len(top.Reasons) != 1 || top.Reasons[0].Count != 3 {
		t.Fatalf("gerekçe sayacı: %+v", top.Reasons)
	}
	var punct *FailedCluster
	for i := range a {
		if a[i].Label == "" {
			punct = &a[i]
		}
	}
	if punct != nil {
		t.Fatalf("kelimesiz soruların etiketi boş kalmamalı: %+v", *punct)
	}
}

type ftStore struct {
	rows     []chstore.CosreInteraction
	sigs     []chstore.CosreSignal
	inserted []chstore.CosreSignal
}

func (f *ftStore) ListCosreInteractionsForScoring(_ context.Context, _, _ time.Time) ([]chstore.CosreInteraction, error) {
	return f.rows, nil
}
func (f *ftStore) ListCosreSignals(_ context.Context, _, _ time.Time) ([]chstore.CosreSignal, error) {
	return append(append([]chstore.CosreSignal{}, f.sigs...), f.inserted...), nil
}
func (f *ftStore) InsertCosreSignal(_ context.Context, g chstore.CosreSignal) error {
	f.inserted = append(f.inserted, g)
	return nil
}

func TestRunFailedTurnSweepIdempotentAndSettled(t *testing.T) {
	now := ftT0.Add(2 * time.Hour)
	old := ftTurn("old", "u", 0, "q")
	old.EmptyAnswer = true
	fresh := ftTurn("fresh", "u", 2*time.Hour-time.Minute, "q2") // oturmadı
	fresh.EmptyAnswer = true
	st := &ftStore{rows: []chstore.CosreInteraction{old, fresh}}
	n, err := RunFailedTurnSweep(context.Background(), st, now)
	if err != nil || n != 1 || st.inserted[0].ExchangeID != "old" || st.inserted[0].UserEmail != SystemSignalActor ||
		st.inserted[0].Detail != "reasons="+FailEmpty || !SystemSignalKinds[st.inserted[0].Kind] {
		t.Fatalf("n=%d err=%v ins=%+v", n, err, st.inserted)
	}
	if n, _ := RunFailedTurnSweep(context.Background(), st, now); n != 0 {
		t.Fatalf("ikinci koşu yazmamalı: %d", n)
	}
}

func TestFailedTurnIsNotALabel(t *testing.T) {
	if w, ok := Weight[SigFailedTurn]; ok && w != 0 {
		t.Fatal("failed_turn puana girmemeli")
	}
	if ClientSignalKinds[SigFailedTurn] {
		t.Fatal("istemci failed_turn yazamaz")
	}
}
