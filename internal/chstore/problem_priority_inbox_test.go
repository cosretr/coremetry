package chstore

// v0.10.1072 — inbox görünüm kuralının dar istisna listesi
// (problem_priority.inboxKeepSourcePriority). Operatör (prod, 2026-10-02):
// "Dün akşam CRM database'inde sorun oldu ama problemlerde P1 gelmedi" —
// kritik hata oranı anomalisi kaynağında P1, inbox'ta P3'tü.

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// TestMatchInboxKeepSourcePriority — eşleştirici tablosu: varsayılan liste
// NEYİ korur, NEYİ bilinçli olarak korumaz.
func TestMatchInboxKeepSourcePriority(t *testing.T) {
	def := DefaultInboxKeepSourcePriority()
	cases := []struct {
		name     string
		patterns []string
		id       string
		want     string // "" = eşleşmemeli
	}{
		{"hata oranı anomalisi", def, "anomaly:crm-core:error_rate", InboxKeepErrorRateAnomaly},
		{"dış kaynak öznesi '/' taşır", def, "anomaly:ext:ora/db1:error_rate", InboxKeepErrorRateAnomaly},
		{"başka metrik anomalisi korunmaz", def, "anomaly:crm-core:p99_latency", ""},
		{"error_rate SONEK olmalı (tam eşleşme)", def, "anomaly:crm-core:error_rate_5m", ""},
		{"yerleşik kural", def, "builtin-error-rate-15pct", InboxKeepBuiltin},
		{"db sağlık öneki", def, "db-health:replication-lag", InboxKeepDBHealth},
		{"kritik incident", def, "incident:critical", InboxKeepCriticalIncident},
		{"warning incident korunmaz", def, "incident:warning", ""},
		{"terfi anomalisi (anomaly-auto) korunmaz", def, "anomaly-auto:7f3a", ""},
		{"SLO burn korunmaz", def, "slo:checkout-avail:critical", ""},
		{"self-health korunmaz", def, "self-volume-spike", ""},
		{"boş kimlik hiçbir şeye uymaz", def, "", ""},
		{"boş liste hiçbir şeye uymaz (saf v0.9.487)", []string{}, "builtin-x", ""},
		{"orta yıldız boş dizgiye de uyar", []string{"a*b"}, "ab", "a*b"},
		{"çoklu yıldız geri izler", []string{"a*b*c"}, "axxbyybzc", "a*b*c"},
		{"büyük-küçük duyarlı", []string{"builtin-*"}, "BUILTIN-x", ""},
		{"ilk eşleşen kalıp döner", []string{"db-*", "db-health:*"}, "db-health:x", "db-*"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := MatchInboxKeepSourcePriority(c.patterns, c.id)
			if ok != (c.want != "") || got != c.want {
				t.Fatalf("Match(%v, %q) = (%q, %v), want %q", c.patterns, c.id, got, ok, c.want)
			}
		})
	}
}

// TestNormalizeInboxKeepSourcePriority — kırp / tekrar / yalnız-yıldız /
// tavan; ASLA nil.
func TestNormalizeInboxKeepSourcePriority(t *testing.T) {
	long := make([]byte, inboxKeepMaxLen+1)
	for i := range long {
		long[i] = 'a'
	}
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil → boş (nil DEĞİL)", nil, []string{}},
		{"kırpılır, boş düşer", []string{"  builtin-*  ", "", "   "}, []string{"builtin-*"}},
		{"tekrar düşer, sıra korunur", []string{"bbb", "aaa", "bbb"}, []string{"bbb", "aaa"}},
		{"yalnız yıldız düşer", []string{"*", "**", "db-health:*"}, []string{"db-health:*"}},
		// v0.10.1072 inceleme — yalnız-yıldız kontrolü "*:*" ile atlatılıyordu.
		{"geniş kalıp düşer (literal öneksiz / <3 literal)", []string{"*:*", "*-*", "*error_rate", "a*b", "slo:*"}, []string{"slo:*"}},
		{"aşırı uzun düşer", []string{string(long), "xyz"}, []string{"xyz"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeInboxKeepSourcePriority(c.in)
			if got == nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Normalize(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
	many := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		many = append(many, "p"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if n := len(NormalizeInboxKeepSourcePriority(many)); n != inboxKeepMaxCount {
		t.Errorf("tavan: %d kalıp kaldı, want %d", n, inboxKeepMaxCount)
	}
}

// TestProblemPriorityInboxKeepNilVsEmpty — nil = varsayılan, boş = kapalı;
// boş liste kayıt-okuma turunda `[]` kalır (null olup varsayılana dönmez).
func TestProblemPriorityInboxKeepNilVsEmpty(t *testing.T) {
	if got := (ProblemPriorityConfig{}).InboxKeepSourcePriorityList(); !reflect.DeepEqual(got, DefaultInboxKeepSourcePriority()) {
		t.Errorf("alan yokken liste = %v, want varsayılan", got)
	}
	empty := []string{}
	off := NormalizeProblemPriority(ProblemPriorityConfig{BigBreachRatio: 2, StaleCriticalHours: 4, InboxKeepSourcePriority: &empty})
	raw, err := json.Marshal(off)
	if err != nil {
		t.Fatal(err)
	}
	back := DefaultProblemPriority() // ReadProblemPriority'nin önceden doldurulmuş hedefi
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeProblemPriority(back).InboxKeepSourcePriorityList(); len(got) != 0 {
		t.Errorf("boş liste kayıt-okuma turunda %v oldu (%s) — operatörün kapattığı istisna geri açıldı", got, raw)
	}
	// Eski blob (alan yok) varsayılan listeyi alır.
	old := DefaultProblemPriority()
	if err := json.Unmarshal([]byte(`{"bigBreachRatio":3,"staleCriticalHours":4}`), &old); err != nil {
		t.Fatal(err)
	}
	if got := NormalizeProblemPriority(old).InboxKeepSourcePriorityList(); !reflect.DeepEqual(got, DefaultInboxKeepSourcePriority()) {
		t.Errorf("eski blob listesi = %v, want varsayılan", got)
	}
}

// TestLoadProblemPriorityKeepsLastGood — okuma hatası iyi değerin ÜSTÜNE
// varsayılan yayınlamaz (anomaly_sensitivity v0.10.1039 emsali).
func TestLoadProblemPriorityKeepsLastGood(t *testing.T) {
	prev := CurrentProblemPriority()
	t.Cleanup(func() { SetProblemPriority(prev); problemPriorityReadFailing.Store(false) })

	only := []string{"db-health:*"}
	good := ProblemPriorityConfig{BigBreachRatio: 3, StaleCriticalHours: 6, InboxKeepSourcePriority: &only}
	LoadProblemPriorityWith(func() (ProblemPriorityConfig, error) { return good, nil })
	got := LoadProblemPriorityWith(func() (ProblemPriorityConfig, error) {
		return ProblemPriorityConfig{}, errors.New("ch: timeout")
	})
	if got.BigBreachRatio != 3 || !reflect.DeepEqual(got.InboxKeepSourcePriorityList(), only) {
		t.Fatalf("hata sonrası yayın = %+v (%v), want son iyi değer", got, got.InboxKeepSourcePriorityList())
	}
	if c := CurrentProblemPriority(); !reflect.DeepEqual(c.InboxKeepSourcePriorityList(), only) {
		t.Fatalf("global liste = %v, want %v", c.InboxKeepSourcePriorityList(), only)
	}
}
