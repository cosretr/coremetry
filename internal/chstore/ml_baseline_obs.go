package chstore

// ml_baseline_obs.go — v0.10.1813, öğrenme döngüsü Faz 1.2 (audit
// docs/anomaly/learning-loop-audit.md §4.1). Tablo store.go
// `ml_baseline_obs`: seri × haftanın saati (slot) × ISO hafta gözlemi.
// Bu dosya yalnız yazma + sınırlı okuma + slot kodlaması; günlük iş (§4.2),
// dedektör bağlantısı ve UI SONRAKİ dilimler. Bant (haftaların medyanı)
// ml.BaselineBand'de, Go'da.

import (
	"context"
	"fmt"
	"time"
)

// MLBaselineObs — bir (seri, slot, hafta) gözlemi.
type MLBaselineObs struct {
	Signal    string
	Service   string
	Part      string
	Slot      uint8     // 0..167 = (ISO gün − 1) × 24 + saat (takvim dilimi)
	WeekStart time.Time // ISO haftanın pazartesisi (takvim dilimi), tarih
	Source    string    // "ml_series" (S1); boş = ml_series
	Class     string    // normal | holiday | half_day | month_end; boş = normal
	Q10, Q50  float32
	Q90, Q99  float32
	VMax      float32
	Calls     uint32
	Buckets   uint8 // slotta gözlenen kova sayısı (örnek sayısı)
	CfgHash   string
}

const (
	MLBaselineSourceSeries = "ml_series"
	MLBaselineClassNormal  = "normal"
	// mlBaselineMaxWeeks — okuma ufku tavanı (TTL 70 g ≈ 10 hafta normal;
	// özel günler 400 g ≈ 57 hafta).
	mlBaselineMaxWeeks = 60
	// mlBaselineReadLimit — tek okumanın satır tavanı (saat başı bir okuma,
	// §4.3: servis düzeyinde ≤ 400 bin satır).
	mlBaselineReadLimit = 500000
)

// MLBaselineSlot — SAF: t'nin (takvim dilimindeki) haftalık slotu ve ISO
// haftanın pazartesisi. Pazartesi 00:00 → 0, pazar 23:00 → 167. Yaz saati
// geçişinde yerel saat kullanılır (atlanan saat boş slot, tekrar eden saat
// aynı slota iki kez düşer) — bant yerel takvimle çalışır (§4.1).
func MLBaselineSlot(t time.Time, loc *time.Location) (uint8, time.Time) {
	if loc == nil {
		loc = time.UTC
	}
	lt := t.In(loc)
	day := (int(lt.Weekday()) + 6) % 7 // pazartesi = 0
	y, m, d := lt.Date()
	monday := time.Date(y, m, d-day, 0, 0, 0, 0, time.UTC)
	return uint8(day*24 + lt.Hour()), monday
}

// InsertMLBaselineObs — toplu yazım; açık kolon listesi (RMT: aynı anahtar
// yeniden yazılır, idempotent). version DEFAULT'tan.
func (s *Store) InsertMLBaselineObs(ctx context.Context, rows []MLBaselineObs) error {
	if len(rows) == 0 {
		return nil
	}
	batch, err := s.conn.PrepareBatch(ctx, `INSERT INTO ml_baseline_obs (signal, service, part, slot, week_start, source, class, q10, q50, q90, q99, vmax, calls, buckets, cfg_hash)`)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Slot > 167 {
			return fmt.Errorf("ml_baseline_obs: slot %d > 167", r.Slot)
		}
		src, class := r.Source, r.Class
		if src == "" {
			src = MLBaselineSourceSeries
		}
		if class == "" {
			class = MLBaselineClassNormal
		}
		ws := r.WeekStart
		ws = time.Date(ws.Year(), ws.Month(), ws.Day(), 0, 0, 0, 0, time.UTC)
		if err := batch.Append(r.Signal, r.Service, r.Part, r.Slot, ws, src, class,
			r.Q10, r.Q50, r.Q90, r.Q99, r.VMax, r.Calls, r.Buckets, r.CfgHash); err != nil {
			return err
		}
	}
	return batch.Send()
}

// MLBaselineBandRequest — (seriler × slotlar) için [AsOf − Weeks hafta,
// AsOf) aralığındaki haftalık gözlemler. AsOf'a göre süzmek geçmiş bandı
// aynen verir (replay).
type MLBaselineBandRequest struct {
	Signal   string
	Services []string // seri anahtarının servis kümesi
	Parts    []string // seri anahtarının part kümesi (çapraz çarpım okunur, Go'da süzülür)
	Slots    []uint8
	AsOf     time.Time // bu haftanın pazartesisi dahil DEĞİL: yalnız tamamlanmış haftalar
	Weeks    int       // 1..60
	Class    string    // boş = normal
	Source   string    // boş = ml_series
}

// mlBaselineBandSQL — SAF: okuma sorgusu (test). Zaman (week_start) ve
// anahtar sınırlı, FINAL, LIMIT, max_execution_time.
func mlBaselineBandSQL() string {
	return `SELECT service, part, slot, week_start, class, q10, q50, q90, q99, vmax, calls, buckets, cfg_hash
		FROM ml_baseline_obs FINAL
		WHERE signal = ? AND source = ? AND class = ?
		  AND week_start >= ? AND week_start < ?
		  AND service IN ? AND part IN ? AND slot IN ?
		ORDER BY service, part, slot, week_start
		LIMIT ?
		SETTINGS max_execution_time = 10`
}

// mlBaselineBandWindow — SAF: [from, to) hafta sınırları (pazartesi, UTC
// tarih). AsOf'un haftası hariç; Weeks 1..60'a kelepçeli.
func mlBaselineBandWindow(asOf time.Time, weeks int) (time.Time, time.Time) {
	if weeks < 1 {
		weeks = 1
	}
	if weeks > mlBaselineMaxWeeks {
		weeks = mlBaselineMaxWeeks
	}
	y, m, d := asOf.Date()
	day := (int(asOf.Weekday()) + 6) % 7
	to := time.Date(y, m, d-day, 0, 0, 0, 0, time.UTC)
	return to.AddDate(0, 0, -7*weeks), to
}

// MLBaselineBand — sınırlı okuma; haftalık satırlar (bant Go'da). Boş
// küme → okuma yok.
func (s *Store) MLBaselineBand(ctx context.Context, req MLBaselineBandRequest) ([]MLBaselineObs, error) {
	if len(req.Services) == 0 || len(req.Parts) == 0 || len(req.Slots) == 0 {
		return nil, nil
	}
	src, class := req.Source, req.Class
	if src == "" {
		src = MLBaselineSourceSeries
	}
	if class == "" {
		class = MLBaselineClassNormal
	}
	from, to := mlBaselineBandWindow(req.AsOf, req.Weeks)
	want := make(map[string]bool, len(req.Services)*len(req.Parts))
	for _, sv := range req.Services {
		for _, p := range req.Parts {
			want[MLSeriesKey(sv, p)] = true
		}
	}
	// []uint8 = []byte: doğrudan bağlanırsa dizgi olarak gider — []int'e çevir.
	slots := make([]int, len(req.Slots))
	for i, v := range req.Slots {
		slots[i] = int(v)
	}
	rows, err := s.conn.Query(ctx, mlBaselineBandSQL(), req.Signal, src, class, from.Format("2006-01-02"), to.Format("2006-01-02"),
		req.Services, req.Parts, slots, mlBaselineReadLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MLBaselineObs
	for rows.Next() {
		o := MLBaselineObs{Signal: req.Signal, Source: src}
		if err := rows.Scan(&o.Service, &o.Part, &o.Slot, &o.WeekStart, &o.Class, &o.Q10, &o.Q50, &o.Q90, &o.Q99, &o.VMax, &o.Calls, &o.Buckets, &o.CfgHash); err != nil {
			return nil, err
		}
		if want[MLSeriesKey(o.Service, o.Part)] {
			out = append(out, o)
		}
	}
	return out, rows.Err()
}
