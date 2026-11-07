package loginsummary

// project.go — Project: anonim uca giden TEK boğum (audit §4). Snapshot ADLARI
// taşır; cevap bu fonksiyondan geçmeden yazılmaz. Saf: CH/VM/Redis yok, saat
// ve ayarlar parametre. Ayar değişimi yeniden kurulum beklemez.

import (
	"strings"
	"time"
)

// StaleAfter — anlık görüntü bundan eskiyse cevap {enabled:true, ready:false}.
const StaleAfter = 15 * time.Minute

// Flags — Project'in ayar girdileri (branding + VM ayarı). Now sıfırsa time.Now().
type Flags struct {
	Enabled  bool   // branding.loginSummary != "off"
	Topology string // TopologyOff | TopologyAnonymous | TopologyNamed ("" = anonymous)
	Now      time.Time
}

// Project — Snapshot + ayarlar → Response.
func Project(snap *Snapshot, f Flags) Response {
	// v0.10.1917 — topoloji panelden BAĞIMSIZ: panel kapalı ama topoloji
	// açıksa cevap {enabled:false, ready:true, topology} — sayı, grafik,
	// aktivite, bileşen YOK. İkisi de kapalıysa yalnız {enabled:false}.
	topoOn := f.Topology != TopologyOff
	if !f.Enabled && !topoOn {
		return Response{Enabled: false}
	}
	now := f.Now
	if now.IsZero() {
		now = time.Now()
	}
	if snap == nil || snap.V != SnapshotVersion || snap.GeneratedAt <= 0 ||
		now.Sub(time.UnixMilli(snap.GeneratedAt)) > StaleAfter {
		return Response{Enabled: f.Enabled, Ready: false}
	}
	if !f.Enabled {
		return Response{Enabled: false, Ready: true, GeneratedAt: snap.GeneratedAt,
			Topology: projectTopology(snap.Topo, f.Topology)}
	}
	out := Response{
		Enabled: true, Ready: true, GeneratedAt: snap.GeneratedAt,
		SpansToday: snap.SpansToday, TracesToday: snap.TracesToday, Services: snap.Services,
		Endpoints: snap.Endpoints,
	}
	if v, ok := snap.Ring.View(); ok {
		out.HTTP24h = &HTTP24h{
			From: v.From, To: v.To, StepS: ringStepS,
			Throughput: v.Throughput, GRPC: v.GRPC, Kafka: v.Kafka, Avg: v.Avg, Summary: v.Summary,
		}
	}
	if a := snap.Activity; a != nil {
		out.Activity = &Activity{Logins: a.Logins, CosreQuestions: a.CosreQuestions}
	}
	out.Topology = projectTopology(snap.Topo, f.Topology)
	state := snap.State
	if state != StateOK {
		state = StateDegraded
	}
	out.Components = &Components{State: state, CheckedAt: snap.CheckedAt, CHBytes: snap.CHBytes}
	return out
}

func projectTopology(t *SnapTopology, mode string) *Topology {
	if t == nil || len(t.Nodes) == 0 {
		return nil
	}
	switch mode {
	case TopologyOff:
		return nil
	case "", TopologyAnonymous, TopologyNamed:
	default:
		mode = TopologyAnonymous // bilinmeyen değer: en dar kip
	}
	named := mode == TopologyNamed
	out := &Topology{WindowS: t.WindowS, Named: named, Nodes: []Node{}, Edges: []Edge{}}
	ok := map[string]bool{}
	remap := map[int]int{} // anlık görüntü düğüm dizini → cevap düğüm dizini (düşenler yok)
	for si, n := range t.Nodes {
		// savunma: ext:/node:/db:/queue: ham kimlikler ya da instance işareti
		// taşıyan bir ad (anlık görüntü bozuk/zehirli olsa bile) hiçbir kipte çıkmaz.
		if n.ID == "" || isDroppedNode(n.Name) || strings.Contains(n.Name, "@") ||
			strings.HasPrefix(n.Name, "db:") || strings.HasPrefix(n.Name, "queue:") {
			continue
		}
		node := Node{ID: n.ID, Tier: tierOrSvc(n.Tier), Weight: round2(n.Weight)}
		if named {
			switch {
			case node.Tier == TierDB && !sysNameRe.MatchString(n.Name) && !dbLabelRe.MatchString(n.Name):
				node.Name = "db"
			case node.Tier == TierQueue && !sysNameRe.MatchString(n.Name) && !topicRe.MatchString(n.Name):
				node.Name = "queue"
			default:
				node.Name = n.Name
			}
		}
		remap[si] = len(out.Nodes)
		out.Nodes = append(out.Nodes, node)
		ok[n.ID] = true
	}
	for _, e := range t.Edges {
		if ok[e.From] && ok[e.To] {
			out.Edges = append(out.Edges, Edge{From: e.From, To: e.To, Weight: round2(e.Weight)})
		}
	}
	if len(out.Nodes) == 0 {
		return nil
	}
	// v0.10.1937 — adsız servis yıldızları: yalnız [yoğunluk, dizin]; düşen
	// düğüme bağlı yıldız -1 alır; tavan savunma amaçlı yeniden uygulanır.
	for i, st := range t.Stars {
		if i >= topoMaxStars {
			break
		}
		near := -1
		if j, has := remap[st.Near]; has {
			near = j
		}
		out.Stars = append(out.Stars, [2]float64{round2(st.W), float64(near)})
	}
	return out
}

func tierOrSvc(t string) string {
	switch t {
	case TierGateway, TierBFF, TierService, TierDB, TierQueue:
		return t
	}
	return TierService
}
