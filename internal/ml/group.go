package ml

// group.go — v0.10.2013 (anomali tekilleştirme D2,
// docs/anomaly/anomaly-reassessment-audit.md §4/§6 B): dedektörler arası
// gruplama. Aynı gerçek olay bugün trace_red + dependency_latency +
// trace_change_point (+ log_red …) olarak ayrı satırlara düşüyor;
// confirmers() yalnız kanıta "teyit" yazıyor, epizot kimliği (MLEpisodeID)
// tek dedektör içi.
//
// KURAL (tek cümle): aynı servisin, zaman pencereleri ([started_at,
// last_seen]) arasındaki boşluk teyit penceresini (quality.confirmWindowMin,
// varsayılan 10 dk) aşmayan ML olayları tek gruptur; zincirleme geçerlidir
// (A–B ve B–C örtüşüyorsa A, B, C tek grup). Kronik kayıt (stage=chronic,
// tekrarlayan desen) gruba girmez — uzun penceresi her şeyi yutardı.
//
// KAYIT YERİ: şemasız (audit seçenek a). Grup kimliği okuma anında
// deterministik hesaplanır: servis + grubun en erken üyesinin kimliği
// (FingerprintAnomaly). ml_events'e kolon EKLENMEDİ — tablonun dağıtık
// yerleşimi doğrulanmadı (audit §11); anomaly_groups tablosu kodda yok.
//
// Gruplama YALNIZ birleştirir: üyelerin hiçbir alanını (kanıt, takvim
// sınıfı, aşama) değiştirmez; batch kuralı yazımda (batchFilter) zaten
// uygulandı, gruplama kesilen adayı geri getirmez. Problem terfisi bu
// dosyada değil (D3 gölge kararı group_gate.go).

import (
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// MLGroup — bir servis × zaman penceresi grubu.
type MLGroup struct {
	ID        string   `json:"id"`
	Service   string   `json:"service"`
	Start     int64    `json:"start"` // unix ns, en erken üye başı
	End       int64    `json:"end"`   // unix ns, en geç üye son görülmesi
	Members   []string `json:"members"`
	Detectors []string `json:"detectors"` // farklı dedektörler, sıralı
	Signals   []string `json:"signals"`   // farklı sinyaller, sıralı
}

// mlGroupID — SAF: servis + en erken üye kimliği → grup kimliği.
func mlGroupID(service, firstMemberID string) string {
	return "mlg-" + chstore.FingerprintAnomaly("ml-group", firstMemberID, service)
}

// GroupMLEvents — SAF: olaylar → gruplar (servis, sonra başlangıç sırasıyla).
// Girdi sırası sonucu değiştirmez; girdi değiştirilmez.
func GroupMLEvents(evs []chstore.MLEvent, slack time.Duration) []MLGroup {
	bySvc := map[string][]chstore.MLEvent{}
	for _, e := range evs {
		if e.Stage == chstore.MLStageChronic || e.Service == "" || e.ID == "" {
			continue
		}
		bySvc[e.Service] = append(bySvc[e.Service], e)
	}
	svcs := make([]string, 0, len(bySvc))
	for s := range bySvc {
		svcs = append(svcs, s)
	}
	sort.Strings(svcs)
	var out []MLGroup
	for _, svc := range svcs {
		list := bySvc[svc]
		sort.Slice(list, func(i, j int) bool {
			if list[i].StartedAt != list[j].StartedAt {
				return list[i].StartedAt < list[j].StartedAt
			}
			return list[i].ID < list[j].ID
		})
		var cur []chstore.MLEvent
		var end int64
		flush := func() {
			if len(cur) > 0 {
				out = append(out, buildGroup(svc, cur, end))
			}
		}
		for _, e := range list {
			last := max(e.LastSeen, e.StartedAt)
			if len(cur) > 0 && e.StartedAt <= end+int64(slack) {
				cur = append(cur, e)
				end = max(end, last)
				continue
			}
			flush()
			cur, end = []chstore.MLEvent{e}, last
		}
		flush()
	}
	return out
}

func buildGroup(svc string, mem []chstore.MLEvent, end int64) MLGroup {
	g := MLGroup{ID: mlGroupID(svc, mem[0].ID), Service: svc, Start: mem[0].StartedAt, End: end}
	dets, sigs := map[string]bool{}, map[string]bool{}
	for _, e := range mem {
		g.Members = append(g.Members, e.ID)
		dets[e.Detector] = true
		sig := e.Signal
		if sig == "" {
			sig = chstore.MLDetectorSignal(e.Detector)
		}
		sigs[sig] = true
	}
	g.Detectors, g.Signals = sortedKeys(dets), sortedKeys(sigs)
	return g
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GroupIndex — SAF: olay kimliği → grubu.
func GroupIndex(gs []MLGroup) map[string]*MLGroup {
	idx := map[string]*MLGroup{}
	for i := range gs {
		for _, id := range gs[i].Members {
			idx[id] = &gs[i]
		}
	}
	return idx
}

// GroupSlack — SAF: gruplama boşluğu = teyit penceresi (confirmers ile aynı vida).
func GroupSlack(cfg chstore.MLConfig) time.Duration {
	return time.Duration(cfg.Quality.ConfirmWindowMin) * time.Minute
}
