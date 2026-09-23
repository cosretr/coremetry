package evaluator

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// v0.10.901 (paritesi #6 inceleme turu) — lider değişimi taşıması.
//
// Sınıf: yeni liderin bellek-içi disk serisi boş → diskETADays ok=false →
// ilk tik açık self-disk-eta satırını "çözüldü" diye kapatıyor, 30 dk sonra
// aynı disk yeni StartedAt + yeni bildirimle yeniden açılıyordu. Kapı
// (diskSeriesWarm) ısınmayı "gerçekten eğilim yok"tan ayırır; taşıma
// (diskCarryOver) satırı son değeriyle aynen yeniden sunar.
func TestDiskSeriesWarm(t *testing.T) {
	gib := float64(uint64(1) << 30)
	cases := []struct {
		name string
		pts  []diskSample
		warm bool
	}{
		{"boş seri (yeni lider ilk tik)", nil, true},
		{"3 nokta (min 4 altı)", diskSeries(3, 600, 50*gib, 10*gib/86400), true},
		{"4 nokta, 15 dk (aralık altı)", diskSeries(4, 300, 50*gib, 10*gib/86400), true},
		{"4 nokta, 30 dk → ısınma bitti", diskSeries(4, 600, 50*gib, 10*gib/86400), false},
		{"düz ama uzun seri → ısınma DEĞİL (satır kapanmalı)", diskSeries(8, 600, 50*gib, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := diskSeriesWarm(c.pts); got != c.warm {
				t.Fatalf("warm=%v, beklenen %v", got, c.warm)
			}
		})
	}
}

func TestDiskCarryOver(t *testing.T) {
	p := &chstore.Problem{
		ID: "self-disk-eta:ch-1/default", RuleID: chstore.SelfDiskRuleID,
		RuleName: "Coremetry · disk dolacak", Metric: "self.disk_eta_days",
		Severity: "critical", // yaş eskalasyonu yükseltmiş olabilir — düşürülmez
		Value:    1.5, Threshold: 7, Comparator: "",
		Description: "ch-1 default diski 1.5 gün içinde DOLACAK",
	}
	w := diskCarryOver(p)
	if w.id != p.ID || w.ruleID != selfDiskRuleID || w.ruleName != p.RuleName || w.metric != p.Metric ||
		w.severity != "critical" || w.value != 1.5 || w.threshold != 7 || w.description != p.Description {
		t.Fatalf("taşıma alanları: %+v", w)
	}
	if w.comparator != "<" {
		t.Fatalf("comparator kuralın sözleşmesi olmalı: %q", w.comparator)
	}
	// Taşınan satır reconcile'ın "tazeleme" dalına düşer (wanted[id]=true);
	// kapanma döngüsü onu atlar — sözleşme: wanted olan satır kapanmaz.
	var snapNil *chstore.OpenProblems
	if snapNil.ByID(p.ID) != nil {
		t.Fatal("nil snapshot nil döner (selfDiskETA nil-güvenli çağırır)")
	}
}
