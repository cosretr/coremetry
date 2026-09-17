package chstore

// exception_inbox_bucket_test.go — v0.10.751 (operatör: "Inbox ignore
// hariç hepsi statüsüyle gözüksün, ignore edilenler sekmede"). Durum
// kovaları: "inbox" = ignored hariç her durum (resolved dahil); "open" =
// new/acknowledged/regressed (Inbox sayfası ve rozet bunu kullanmaya
// devam eder); tekil durum = eşitlik; boş = ignored hariç (eski varsayılan,
// inbox ile aynı küme — iki yazım bilinçli: URL grameri "inbox" der,
// boş parametre eski çağıranları kırmaz).

import (
	"strings"
	"testing"
)

func TestExceptionGroupStateBuckets(t *testing.T) {
	cases := []struct {
		state    string
		wantCond string
		wantArg  any
	}{
		{"inbox", "state != ?", ExStateIgnored},
		{"", "state != ?", ExStateIgnored},
		{"open", "state IN ('new','acknowledged','regressed')", nil},
		{"resolved", "state = ?", "resolved"},
		{"ignored", "state = ?", "ignored"},
	}
	for _, c := range cases {
		wc := buildExceptionGroupWhere(ExceptionGroupFilter{State: c.state})
		joined := strings.Join(wc.conds, " AND ")
		if !strings.Contains(joined, c.wantCond) {
			t.Errorf("state=%q: koşul %q yok: %s", c.state, c.wantCond, joined)
		}
		if c.wantArg != nil {
			found := false
			for _, a := range wc.args {
				if a == c.wantArg {
					found = true
				}
			}
			if !found {
				t.Errorf("state=%q: arg %v yok: %v", c.state, c.wantArg, wc.args)
			}
		}
		// inbox/boş resolved'ı DIŞLAMAZ, open dışlar.
		excludesResolved := strings.Contains(joined, "IN ('new','acknowledged','regressed')") || strings.Contains(joined, "state = ?") && c.state != "resolved"
		if (c.state == "inbox" || c.state == "") && excludesResolved {
			t.Errorf("state=%q resolved'ı dışlıyor: %s", c.state, joined)
		}
	}
}
