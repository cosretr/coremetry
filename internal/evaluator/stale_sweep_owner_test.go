package evaluator

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.592 — bayat süpürme poller'ın Problem'lerini ATLAR.
//
// Evaluator somut *chstore.Store ile kurulur; sahte yok, süpürücüyü CH'siz
// koşturamayız. Bu yüzden karar SAF fonksiyonda ve burada tablo testli;
// KABLOLAMA ise yorum-süzülmüş kaynak piniyle: sweepStaleProblems'ın
// döngüsü `stale` üzerinden değil `toClose` üzerinden dönmeli — aksi hâlde
// saf fonksiyon yeşil, ekranda hiçbir şey değişmemiş olur.
func TestStaleSweepCandidates(t *testing.T) {
	stale := []chstore.Problem{
		{ID: "a", RuleID: "anomaly:shop-payment:p99_ms"},
		{ID: "b", RuleID: chstore.RuleExtDownPrefix + "ext:oracle-errlog"},
		{ID: "c", RuleID: "anomaly:ext:extsrc/OP1/E1:ext:fail_count"}, // seri Problem'i — v0.10.900: kaynak yaşarken MUAF (nil = yaşıyor)
		{ID: "d", RuleID: chstore.RuleExtCapPrefix + "ext:extsrc:ext:fail_count"},
	}
	toClose, skipped := staleSweepCandidates(stale, nil) // nil = 592 davranışı
	ids := func(ps []chstore.Problem) string {
		var b []string
		for _, p := range ps {
			b = append(b, p.ID)
		}
		return strings.Join(b, ",")
	}
	if got := ids(toClose); got != "a" {
		t.Fatalf("süpürülecek yalnız a olmalı (seri Problem'i kaynak yaşarken muaf, v0.10.900), %q", got)
	}
	if got := ids(skipped); got != "b,c,d" {
		t.Fatalf("atlananlar b,c,d (ext-down, seri, ext-cap), %q", got)
	}
	// Kaynak yaşamıyorsa seri/küme satırları da süpürülür.
	dead := func(string) bool { return false }
	if tc, _ := staleSweepCandidates(append(stale, chstore.Problem{ID: "e", RuleID: chstore.RuleExtClusterPrefix + "extsrc/OP1"}), dead); ids(tc) != "a,b,c,d,e" {
		t.Fatalf("ölü kaynakta hepsi süpürülür: %q", ids(tc))
	}
	if tc, sk := staleSweepCandidates(nil, nil); len(tc) != 0 || len(sk) != 0 {
		t.Fatal("boş girdi boş çıktı")
	}
	// v0.10.605 — canlılık: oracle-errlog yaşıyor, extsrc silinmiş → d (extsrc
	// ext-cap) SÜPÜRÜLÜR, b (oracle ext-down) muaf kalır. Özne ext-cap'ten
	// metriksiz çıkarılır ("ext:extsrc:ext:fail_count" → "ext:extsrc").
	live := func(subject string) bool { return subject == "ext:oracle-errlog" }
	toClose, skipped = staleSweepCandidates(stale, live)
	if got := ids(toClose); got != "a,c,d" {
		t.Fatalf("ölü kaynağın ext-cap'i süpürülmeli: a,c,d bekleniyor, %q", got)
	}
	if got := ids(skipped); got != "b" {
		t.Fatalf("yalnız canlı kaynağın ext-down'ı muaf: b bekleniyor, %q", got)
	}
	none := func(string) bool { return false }
	if tc, sk := staleSweepCandidates(stale, none); len(tc) != 4 || len(sk) != 0 {
		t.Fatalf("hiç kaynak yaşamıyorsa hepsi süpürülür: %d/%d", len(tc), len(sk))
	}
}

func TestSweepStaleProblemsUsesCandidates(t *testing.T) {
	raw, err := os.ReadFile("evaluator.go")
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`(?m)//.*$`).ReplaceAllString(string(raw), "")
	start := strings.Index(code, "func (e *Evaluator) sweepStaleProblems(")
	if start < 0 {
		t.Fatal("sweepStaleProblems bulunamadı")
	}
	end := strings.Index(code[start:], "\n}\n")
	body := code[start : start+end]
	if !strings.Contains(body, "toClose, skipped := staleSweepCandidates(stale, e.pollerSourceLive)") {
		t.Fatal("süpürücü staleSweepCandidates'ı çağırmıyor — poller Problem'leri yine süpürülür")
	}
	if !strings.Contains(body, "for i := range toClose {") || strings.Contains(body, "for i := range stale {") {
		t.Fatal("döngü toClose üzerinden dönmeli, stale üzerinden değil")
	}
}
