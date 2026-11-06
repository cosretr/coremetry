package ml

// explain_spark.go — v0.10.1874, öğrenme döngüsü Faz 2 parti B (audit §7.2
// "Liste satırı sparkline": satır başına ≤ 24 nokta, kayıttan; mockup
// görünüm 1 "Seyir" sütunu). Liste satırının küçük seyri: olay anı kaydının
// serisinden (ml_event_snapshots; liste ucu TEK toplu okumayla alır — satır
// başına istek YOK). Başlangıçtan 8 kova önce başlar, bitişte (kapalı olay)
// ya da serinin sonunda (açık olay) biter; 24'ten uzunsa eşit gruplara
// ortalanır. A = başlangıcın çıktı içindeki indeksi (FE işaret çizgisi).

import (
	"encoding/json"
	"sort"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Spark — liste satırının seyri.
type Spark struct {
	V []float64 `json:"v"`
	A int       `json:"a"` // başlangıç kovasının indeksi
}

const (
	sparkMax  = 24
	sparkLead = 8
)

// BuildSpark — SAF: kayıt serisi (JSON) → ≤ 24 nokta. Nokta yoksa nil.
func BuildSpark(seriesJSON string, startedAt, lastSeen int64) *Spark {
	var pts []chstore.MLPoint
	if json.Unmarshal([]byte(seriesJSON), &pts) != nil || len(pts) == 0 {
		return nil
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].T < pts[j].T })
	onset, end := startedAt/1e9, lastSeen/1e9
	oi, ei := 0, len(pts)-1
	for i, p := range pts {
		if p.T <= onset {
			oi = i
		}
		if p.T <= end {
			ei = i
		}
	}
	if ei < oi {
		ei = oi
	}
	from := max(0, oi-sparkLead)
	// kısa pencere: sola doğru 24'e tamamla (bağlam), sonra sağa
	for ei-from+1 < sparkMax && from > 0 {
		from--
	}
	for ei-from+1 < sparkMax && ei < len(pts)-1 {
		ei++
	}
	win := pts[from : ei+1]
	if len(win) <= sparkMax {
		out := &Spark{V: make([]float64, len(win)), A: oi - from}
		for i, p := range win {
			out.V[i] = p.V
		}
		return out
	}
	out := &Spark{V: make([]float64, sparkMax)}
	n := len(win)
	for g := 0; g < sparkMax; g++ {
		lo, hi := g*n/sparkMax, (g+1)*n/sparkMax
		s := 0.0
		for _, p := range win[lo:hi] {
			s += p.V
		}
		out.V[g] = s / float64(hi-lo)
		if oi-from >= lo && oi-from < hi {
			out.A = g
		}
	}
	return out
}
