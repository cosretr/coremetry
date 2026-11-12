package templater

// v0.10.2029 — yeni kalıp kararına seviye: örneklemde ÇOĞUNLUK ERROR+ ise
// kalıp ERROR; tek ERROR satırı yetmez; "other" (seviyesiz) satır ERROR
// sayılmaz ama paydadadır. Kayıt süreç içi, kalıp kimliğiyle.

import (
	"testing"
	"time"
)

func TestIsErrorSeverity(t *testing.T) {
	cases := []struct {
		num  uint8
		text string
		want bool
	}{
		{17, "", true}, {21, "", true}, {9, "INFO", false}, {0, "ERROR", true}, {0, "Fatal", true},
		{0, "warn", false}, {0, "", false}, {0, "other", false}, {13, "WARN", false},
	}
	for _, c := range cases {
		if got := IsErrorSeverity(c.num, c.text); got != c.want {
			t.Errorf("IsErrorSeverity(%d,%q) = %v, istenen %v", c.num, c.text, got, c.want)
		}
	}
}

func TestMajorityErrorLevel(t *testing.T) {
	cases := []struct {
		name           string
		errPlus, total uint64
		want           string
	}{
		{"hepsi ERROR", 25, 25, "ERROR"},
		{"çoğunluk ERROR (3/5)", 3, 5, "ERROR"},
		{"tek ERROR satırı yetmez (1/25)", 1, 25, ""},
		{"tam yarı çoğunluk değil (10/20)", 10, 20, ""},
		{"hepsi other / seviyesiz", 0, 40, ""},
		{"örneklem yok", 0, 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := MajorityErrorLevel(c.errPlus, c.total); got != c.want {
				t.Fatalf("MajorityErrorLevel(%d,%d) = %q, istenen %q", c.errPlus, c.total, got, c.want)
			}
		})
	}
}

// Puller yolu: Add + NoteErrorPlus kümenin sayacını artırır, kayıt kalıp
// kimliğiyle okunur; bayat kayıt düşer.
func TestTemplateSeverityRegistry(t *testing.T) {
	d := NewDrain()
	var c *Cluster
	for i, lvl := range []string{"ERROR", "ERROR", "INFO"} {
		c = d.Add("Batch job 7 failed with code 42", "orders-batch", int64(i))
		if IsErrorSeverity(0, lvl) {
			d.NoteErrorPlus(c)
		}
	}
	if c.ErrPlus != 2 || c.Count != 3 {
		t.Fatalf("sayaç %d/%d", c.ErrPlus, c.Count)
	}
	RecordTemplateSeverity(c.ID, c.ErrPlus, c.Count, time.Now())
	if got := TemplateLevel(c.ID); got != "ERROR" {
		t.Fatalf("seviye %q", got)
	}
	if TemplateLevel("bilinmeyen") != "" {
		t.Error("kayıtta olmayan kalıp bilinmiyor olmalı")
	}
	RecordTemplateSeverity("eski", 5, 5, time.Now().Add(-2*severityTTL))
	if TemplateLevel("eski") != "" {
		t.Error("bayat kayıt kullanılmamalı")
	}
}
