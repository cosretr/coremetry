package chstore

import "testing"

// v0.10.901 (paritesi #6 dilim 2) — disk chip'i eşleme pinleri.
//
// Kümede id tam eşleşir (iki taraf da hostName()). Tek düğümde sysstats
// host boş okur ama evaluator id'de host taşır → son-ek eşlemesi; iki aday
// varsa belirsiz → chip yok. Başka kuralın satırı bağlanmaz; satır yoksa
// alan nil (JSON'da yok). Değerler Problem'den aynen (uydurma yok); ton
// (Critical) Days<2'den türer, yaş-eskalasyonlu Severity'den değil;
// acknowledged satır da çözülmemiştir → chip görünür.
func TestAttachDiskForecast(t *testing.T) {
	open := []*Problem{
		{ID: "self-disk-eta:ch-1/default", RuleID: SelfDiskRuleID, Status: "open", Value: 4.5, Severity: "warning", Threshold: 7, Description: "ch-1 default diski 4.5 gün içinde DOLACAK"},
		{ID: "self-disk-eta:ch-3/default", RuleID: SelfDiskRuleID, Status: "acknowledged", Value: 0.375, Severity: "critical", Threshold: 7},
		{ID: "self-disk-eta:ch-3/cold", RuleID: SelfDiskRuleID, Status: "open", Value: 3, Severity: "warning", Threshold: 7},
		// 6.9 gün kaldı ama satır 30 dk'dır açık → yaş eskalasyonu critical yazdı.
		{ID: "self-disk-eta:ch-4/default", RuleID: SelfDiskRuleID, Status: "open", Value: 6.9, Severity: "critical", Threshold: 7},
		{ID: "self-spool-depth:ch-1", RuleID: "self-spool-depth", Status: "open", Value: 99, Severity: "critical"},
		nil,
	}

	t.Run("küme: tam eşleşme, satırsız disk boş, başka kural bağlanmaz, ack görünür", func(t *testing.T) {
		disks := []DiskStat{
			{Host: "ch-1", Name: "default"},
			{Host: "ch-2", Name: "default"},
			{Host: "ch-3", Name: "default"},
			{Host: "ch-3", Name: "cold"},
			{Host: "ch-4", Name: "default"},
		}
		attachDiskForecast(disks, open)
		if f := disks[0].Forecast; f == nil || f.Days != 4.5 || f.Critical || f.Severity != "warning" || f.ThresholdDays != 7 ||
			f.ProblemID != "self-disk-eta:ch-1/default" || f.Note == "" {
			t.Fatalf("ch-1: %+v", disks[0].Forecast)
		}
		if disks[1].Forecast != nil {
			t.Fatalf("ch-2 satırsız, chip olmamalı: %+v", disks[1].Forecast)
		}
		if f := disks[2].Forecast; f == nil || f.Days != 0.375 || !f.Critical || f.Severity != "critical" {
			t.Fatalf("ch-3 default (acknowledged, <2 gün): %+v", disks[2].Forecast)
		}
		if f := disks[3].Forecast; f == nil || f.Days != 3 || f.Critical {
			t.Fatalf("ch-3 cold: %+v", disks[3].Forecast)
		}
		if f := disks[4].Forecast; f == nil || f.Critical || f.Severity != "critical" {
			t.Fatalf("ch-4: 6.9 gün → ton kritik DEĞİL, satır ciddiyeti aynen taşınır: %+v", disks[4].Forecast)
		}
	})

	t.Run("tek düğüm: host boş → son-ek eşlemesi", func(t *testing.T) {
		single := []*Problem{open[0], open[4]}
		disks := []DiskStat{{Name: "default"}, {Name: "cold"}}
		attachDiskForecast(disks, single)
		if f := disks[0].Forecast; f == nil || f.ProblemID != "self-disk-eta:ch-1/default" {
			t.Fatalf("tek düğüm default: %+v", disks[0].Forecast)
		}
		if disks[1].Forecast != nil {
			t.Fatalf("tek düğüm cold satırsız: %+v", disks[1].Forecast)
		}
	})

	t.Run("tek düğüm: host'suz id ile tam eşleşme (son-ek gerekmez)", func(t *testing.T) {
		disks := []DiskStat{{Name: "default"}}
		attachDiskForecast(disks, []*Problem{{ID: "self-disk-eta:default", RuleID: SelfDiskRuleID, Status: "open", Value: 2}})
		if f := disks[0].Forecast; f == nil || f.ProblemID != "self-disk-eta:default" {
			t.Fatalf("host'suz tam eşleşme: %+v", disks[0].Forecast)
		}
	})

	t.Run("küme: host dolu disk, host'suz satıra son-ek ile BAĞLANMAZ", func(t *testing.T) {
		disks := []DiskStat{{Host: "ch-1", Name: "default"}}
		attachDiskForecast(disks, []*Problem{{ID: "self-disk-eta:default", RuleID: SelfDiskRuleID, Status: "open", Value: 2}})
		if disks[0].Forecast != nil {
			t.Fatalf("kümede son-ek eşlemesi olmamalı: %+v", disks[0].Forecast)
		}
	})

	t.Run("tek düğüm: iki aday → belirsiz, chip yok", func(t *testing.T) {
		disks := []DiskStat{{Name: "default"}}
		attachDiskForecast(disks, open) // ch-1/default, ch-3/default, ch-4/default
		if disks[0].Forecast != nil {
			t.Fatalf("belirsiz eşleme bağlandı: %+v", disks[0].Forecast)
		}
	})

	t.Run("tavanda: 0 gün → Critical, FE 'dolu' yazar", func(t *testing.T) {
		disks := []DiskStat{{Host: "ch-9", Name: "default"}}
		attachDiskForecast(disks, []*Problem{{ID: "self-disk-eta:ch-9/default", RuleID: SelfDiskRuleID, Status: "open", Value: 0, Severity: "critical"}})
		if f := disks[0].Forecast; f == nil || f.Days != 0 || !f.Critical {
			t.Fatalf("tavanda: %+v", disks[0].Forecast)
		}
	})

	t.Run("boş girdiler", func(t *testing.T) {
		disks := []DiskStat{{Host: "ch-1", Name: "default"}}
		attachDiskForecast(disks, nil)
		attachDiskForecast(nil, open)
		attachDiskForecast(disks, []*Problem{open[4]})
		if disks[0].Forecast != nil {
			t.Fatalf("boş/başka kural bağlandı: %+v", disks[0].Forecast)
		}
	})
}
