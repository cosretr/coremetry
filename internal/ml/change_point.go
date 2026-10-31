package ml

// change_point.go — ML kırılma noktası (log dilim 4, v0.10.1757; trace T3
// aynı çekirdeği kullanır). Seri son 6 sa (hedef kova), referans = aynı
// takvim sınıfı günlerin AYNI gün-içi diliminin çeyreklikleri (CH profili,
// tek okuma): z = (v − Q50) / max(IQR/1.349, 0.1·Q50, 1). İki yönlü CUSUM
// (k = 1, h = 2 × kritik z): S⁺ = max(0, S⁺ + z − k), S⁻ = max(0, S⁻ − z − k).
// Olay yalnız kayma pencere SONUNDA hâlâ sürüyorsa (S > h); kırılma anı =
// alarmdan önce S'nin son sıfır olduğu kova. Eksik kova sıfır değil boşluk:
// boşlukta CUSUM sıfırlanır (VM/ES eksiği kayma sayılmaz). Kırılma ±15 dk
// içinde servisin rollout'u varsa kanıta bağlanır (rollout_causes deseni:
// eşleme, nedensellik iddiası değil). Tatilde aşağı kayma aday değil.

import (
	"context"
	"math"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	cpWindow     = 6 * time.Hour
	cpRolloutPad = 15 * time.Minute
	cpK          = 1.0
	cpMinPoints  = 12
	cpTail       = 3 // kayma pencere sonunda sürmeli: son 3 kovanın z'si alarm yönünde > k
)

// cpResult — kırılma kararı.
type cpResult struct {
	Fire      bool
	Direction string
	At        int64 // kırılma kovası (unix s)
	S         float64
	Shift     float64 // kırılmadan sonraki ortalama − referans medyan ortalaması
	Ref       float64
	After     float64
}

// cusum — SAF. pts artan zamanlı; ref: slot → çeyreklik; slotOf: t → slot.
func cusum(pts []chstore.MLPoint, ref map[int]chstore.MLProfileStat, slotOf func(int64) int, step int64, h float64) cpResult {
	var sp, sn float64
	var lastZeroP, lastZeroN int64 = -1, -1
	n := 0
	var prevT int64
	var tail []float64
	for i, p := range pts {
		st, ok := ref[slotOf(p.T)]
		if !ok {
			sp, sn, lastZeroP, lastZeroN, tail = 0, 0, -1, -1, nil
			continue
		}
		if i > 0 && p.T-prevT > step { // boşluk: sıfırla
			sp, sn, lastZeroP, lastZeroN, tail = 0, 0, -1, -1, nil
		}
		prevT = p.T
		n++
		sigma := math.Max((st.Q75-st.Q25)/1.349, math.Max(0.1*st.Q50, 1))
		// kırpılmış katkı: tek bir dev sıçrama birikimi tek başına taşımasın
		z := math.Max(-h/2, math.Min(h/2, (p.V-st.Q50)/sigma))
		tail = append(tail, z)
		if len(tail) > cpTail {
			tail = tail[1:]
		}
		sp = math.Max(0, sp+z-cpK)
		sn = math.Max(0, sn-z-cpK)
		if sp == 0 || lastZeroP < 0 {
			lastZeroP = p.T
		}
		if sn == 0 || lastZeroN < 0 {
			lastZeroN = p.T
		}
	}
	r := cpResult{}
	if n < cpMinPoints || len(tail) < cpTail {
		return r
	}
	sustained := func(sign float64) bool {
		for _, z := range tail {
			if sign*z <= cpK {
				return false
			}
		}
		return true
	}
	switch {
	case sp > h && sp >= sn && sustained(1):
		r.Fire, r.Direction, r.At, r.S = true, "up", lastZeroP+step, sp
	case sn > h && sustained(-1):
		r.Fire, r.Direction, r.At, r.S = true, "down", lastZeroN+step, sn
	default:
		return r
	}
	var sumV, sumR float64
	cnt := 0
	for _, p := range pts {
		if p.T < r.At {
			continue
		}
		if st, ok := ref[slotOf(p.T)]; ok {
			sumV += p.V
			sumR += st.Q50
			cnt++
		}
	}
	if cnt > 0 {
		r.After, r.Ref = sumV/float64(cnt), sumR/float64(cnt)
		r.Shift = r.After - r.Ref
	}
	return r
}

// cpTarget — bir kırılma taraması: sinyal, parçalar, dedektör adı.
type cpTarget struct {
	signal   string
	parts    []string
	prefix   string
	suffix   string
	detector string
	// partOf — olay kimliği/Code/Level için parça → (level, code).
	fields func(part string) (level, code string)
}

// runChangePoint — iki CH okuması (son 6 sa seri + mevsimsel profil) + bir
// rollout okuması; tüm servisler.
func runChangePoint(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time, tg cpTarget, capOK func() bool) ([]chstore.MLEvent, error) {
	step := int64(cfg.BucketSec)
	last := lastCompleteBucket(now, cfg.BucketSec, cfg.IngestLagSec)
	since := last.Add(-cpWindow)
	cal, loc := calendarFor(deps.Store.AnomalySensitivityForDetectors())
	dates, class := baselineDates(last, cfg.BaselineDays, cal, loc)
	slotOf := func(t int64) int {
		lt := time.Unix(t, 0).In(loc)
		return (lt.Hour()*3600 + lt.Minute()*60) / cfg.BucketSec
	}
	var slots []int
	seen := map[int]bool{}
	for t := since.Unix(); t <= last.Unix(); t += step {
		if s := slotOf(t); !seen[s] {
			seen[s] = true
			slots = append(slots, s)
		}
	}
	t0 := time.Now()
	recent, err := deps.Store.MLSeriesRecent(ctx, tg.signal, tg.parts, tg.prefix, tg.suffix, since, cfg.BucketSec)
	if err != nil {
		return nil, err
	}
	prof, err := deps.Store.MLSeasonalProfile(ctx, chstore.MLProfileRequest{Signal: tg.signal, Parts: tg.parts, PartPrefix: tg.prefix, PartSuffix: tg.suffix,
		Dates: dates, Slots: slots, TargetSec: cfg.BucketSec, TZ: loc.String()})
	global.lastCHMs.Store(time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	deploys, _ := deps.Store.GetDeploysInWindow(ctx, since.Add(-cpRolloutPad), now, 1000)
	off := isOffDay(class)
	h := 2 * cfg.CriticalZ
	var evs []chstore.MLEvent
	for k, pts := range recent {
		ref := prof[k]
		if len(ref) == 0 {
			continue
		}
		r := cusum(pts, ref, slotOf, step, h)
		if !r.Fire || (r.Direction == "down" && off) || !capOK() {
			continue
		}
		svc, part := splitKey(k)
		level, code := tg.fields(part)
		evm := map[string]any{"changeAt": r.At, "cusum": r.S, "afterMean": r.After, "refMean": r.Ref, "dayClass": class,
			"windowHours": int(cpWindow.Hours()), "part": part, "bucketSec": cfg.BucketSec}
		var match []chstore.RecentDeployEntry
		for _, d := range deploys {
			at := d.FirstSeenNs / 1e9
			if d.Service == svc && at >= r.At-int64(cpRolloutPad.Seconds()) && at <= r.At+int64(cpRolloutPad.Seconds()) {
				match = append(match, d)
			}
		}
		if len(match) > 0 {
			evm["rollouts"] = match
		}
		evs = append(evs, chstore.MLEvent{ID: chstore.MLEventID(tg.detector, svc, part), Detector: tg.detector, Signal: tg.signal,
			Stage: chstore.MLStageShadow, Service: svc, Level: level, Code: code, StartedAt: r.At * 1e9, LastSeen: last.Add(time.Duration(step) * time.Second).UnixNano(),
			WindowSec: uint32(cfg.BucketSec), Baseline: r.Ref, Observed: r.After, Score: r.S, Direction: r.Direction, Evidence: mergeEvidence("", evm)})
	}
	return evs, nil
}

func splitKey(k string) (svc, part string) {
	for i := 0; i < len(k); i++ {
		if k[i] == 0 {
			return k[:i], k[i+1:]
		}
	}
	return k, ""
}

// LogChangePoint — log dilim 4 dedektörü (_total / _error serileri).
type LogChangePoint struct{ capper }

func NewLogChangePoint() *LogChangePoint { return &LogChangePoint{capper{daily: map[string]int{}}} }

func (d *LogChangePoint) Name() string { return "log_change_point" }

func (d *LogChangePoint) Enabled(cfg chstore.MLConfig) bool { return cfg.DetectorOn(detChangePoint) }

func (d *LogChangePoint) Tick(ctx context.Context, deps Deps, cfg chstore.MLConfig, now time.Time) ([]chstore.MLEvent, error) {
	return runChangePoint(ctx, deps, cfg, now, cpTarget{signal: chstore.MLSignalLog, parts: []string{partTotal, partError}, detector: detChangePoint,
		fields: func(part string) (string, string) {
			if part == partError {
				return "error", ""
			}
			return "", ""
		}}, func() bool { return d.take(detChangePoint, cfg, now) })
}

const detChangePoint = "change_point"

// capper — dedektör başına günlük olay tavanı (ortak).
type capper struct {
	daily map[string]int
	day   string
}

func (c *capper) take(det string, cfg chstore.MLConfig, now time.Time) bool {
	day := now.UTC().Format("2006-01-02")
	if day != c.day {
		c.day, c.daily = day, map[string]int{}
	}
	if c.daily[det] >= cfg.DailyEventCap {
		return false
	}
	c.daily[det]++
	return true
}
