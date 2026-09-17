package chstore

// channel_kinds_test.go — v0.10.747 kanal başına olay türü süzgeci.
//
// Üç sözleşme: (1) sınıflandırıcı RuleID önekiyle anomali/kural ayırır,
// (2) allow-list boşsa HER ŞEY geçer (mevcut kanallar bayt-bayt eski
// davranış), doluysa yalnız üyeler — bilinmeyen/boş tür DÜŞER (küme
// üyeliği; minPriority'nin "hesaplanmamış → açık geç" istisnası burada
// yok, tür her zaman hesaplanır), (3) yüklem MatchesProblem'e bağlı ve
// diğerleriyle AND'lenir.

import (
	"strings"
	"testing"
)

func TestProblemNotifyKind(t *testing.T) {
	cases := []struct {
		ruleID string
		want   string
	}{
		{"anomaly:shop:p99_ms", NotifyKindAnomaly},
		{"anomaly:shop:service_silent", NotifyKindAnomaly},
		{"anomaly:ext:extsrc/OP1/E1:ext:fail_count", NotifyKindAnomaly},
		{"anomaly-cluster:shop-payment", NotifyKindAnomaly},
		{"exception-storm", NotifyKindAnomaly},
		{"exception:fatal-infrastructure", NotifyKindAnomaly},
		{"exception:shared-dependency", NotifyKindAnomaly},
		{"builtin-error-rate", NotifyKindProblem},
		{"r-1a2b3c", NotifyKindProblem},
		{"runtime:jvm-gc", NotifyKindProblem},
		{"slo:checkout:critical", NotifyKindProblem},
		{"db-slow-stmt", NotifyKindProblem},
		{"", NotifyKindProblem},
		// Önek sözcük sınırı: "anomalyx" bir anomali değil.
		{"anomalyx:shop", NotifyKindProblem},
	}
	for _, c := range cases {
		if got := ProblemNotifyKind(Problem{RuleID: c.ruleID}); got != c.want {
			t.Errorf("RuleID=%q → %q, istenen %q", c.ruleID, got, c.want)
		}
	}
}

func TestNormalizeNotifyKinds(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    string // virgülle
		wantErr bool
	}{
		{"nil → süzgeç yok", nil, "", false},
		{"boş dilim → süzgeç yok", []string{}, "", false},
		{"yalnız boşluk → süzgeç yok", []string{" ", ""}, "", false},
		{"kırp + küçült + tekrar at, sıra korunur", []string{" Anomaly", "problem", "anomaly"}, "anomaly,problem", false},
		{"üçü de", []string{"problem", "anomaly", "incident"}, "problem,anomaly,incident", false},
		{"bilinmeyen → hata", []string{"problem", "exception"}, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeNotifyKinds(c.in)
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if s := strings.Join(got, ","); s != c.want {
				t.Fatalf("got %q, istenen %q", s, c.want)
			}
			if c.want == "" && got != nil {
				t.Fatalf("boş sonuç nil olmalı (JSON'da omitempty), %v geldi", got)
			}
		})
	}
}

func TestKindsFilterRidesMatchesProblem(t *testing.T) {
	// Süzgeç yok = hepsi geçer, bilinmeyen/boş tür dahil.
	all := ChannelMatchRules{}
	for _, k := range []string{NotifyKindProblem, NotifyKindAnomaly, NotifyKindIncident, "", "garip"} {
		if !all.MatchesProblem(MatchInput{Service: "shop", Kind: k}) {
			t.Errorf("süzgeçsiz kanal %q türünü DÜŞÜRDÜ", k)
		}
	}
	// Yalnız anomali kanalı.
	an := ChannelMatchRules{Kinds: []string{NotifyKindAnomaly}}
	if !an.MatchesProblem(MatchInput{Service: "shop", Kind: NotifyKindAnomaly}) {
		t.Error("anomali kanalı anomaliyi ALMADI")
	}
	for _, k := range []string{NotifyKindProblem, NotifyKindIncident, ""} {
		if an.MatchesProblem(MatchInput{Service: "shop", Kind: k}) {
			t.Errorf("anomali kanalı %q türünü ALDI", k)
		}
	}
	// İki tür + diğer yüklemlerle AND.
	two := ChannelMatchRules{Kinds: []string{NotifyKindProblem, NotifyKindIncident}, Services: []string{"orders"}}
	if two.MatchesProblem(MatchInput{Service: "shop", Kind: NotifyKindProblem}) {
		t.Error("servis eşleşmemesine rağmen ateşledi — yüklemler AND'lenmiyor")
	}
	if !two.MatchesProblem(MatchInput{Service: "orders", Kind: NotifyKindIncident}) {
		t.Error("her iki yüklem de tutarken ateşlemedi")
	}
	if two.MatchesProblem(MatchInput{Service: "orders", Kind: NotifyKindAnomaly}) {
		t.Error("listede olmayan tür geçti")
	}
}
