package entity

import (
	"context"
	"log"
	"sort"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// hosts.go — v0.10.2260 (Entity S6 parça 1, P-b; docs/architecture/
// entity-s6-cost-note.md seçenek C): K8s DIŞI HOST entity'leri, şemasız.
//
// k8s_pod terfi kolonu k8s.pod.name yoksa host_name'e düşer; entity_seen_5m
// bu satırları namespace'i boş olarak taşır. Tick bunları zaten okuyor
// (RecentSeen) — span geçişi namespace'siz satırı atar, bu dosya onu host
// ADAYI yapar. Ek okuma yok; yalnız terfi kanıtı için aşağıdaki sınırlı okuma.
//
// Belirsizlik: namespace attr'ı basmayan bir K8s pod'u da namespace'siz
// satırdır. Üç kapı:
//  1. Pod şekilli ad (rasgele sonek: Deployment / DaemonSet / Job pod'u,
//     wlkind.FromPod) asla host olmaz. Sıra numaralı ad ("node-a-01")
//     şekilden reddedilmez: host adlarında yaygın.
//  2. Aynı tick'te KSM'nin (Thanos) pod olarak bildiği ad host olmaz.
//  3. KALICI katmana terfi yalnız ≥ 24 sa kesintisiz görülen ada:
//     entity_seen_5m'in kendi first_seen durumu ≤ şimdi − 24 sa VE son 24
//     saatin saatlik dilimlerinin en az 24'ünde (25 olası) kova. O zamana
//     dek ad yalnız Redis canlı dizinindedir (HostClusterID blob'u).
//
// Kimlik küresel `host:<ad>` (K3'ün servis emsali; host'un cluster değeri
// çoğu kurulumda boş). Kalıcı satırlar sahte cluster_id HostClusterID
// altında (TopologyClusterID emsali). `runs` (host→svc) iki ucu kalıcı
// olduğundan kalıcı kenardır; pod→svc `runs` dizinde kalır.

// HostClusterID — host satırlarının ve canlı blob'unun cluster_id'si.
const HostClusterID = "(host)"

// Sabitler (ayar yok, K1).
const (
	// HostEvery — kalıcı geçiş kadansı (canlı blob her tick).
	HostEvery = 15 * time.Minute
	// HostStableAfter — terfi için kesintisiz görülme süresi.
	HostStableAfter = 24 * time.Hour
	// HostMinHours — son HostStableAfter içinde kova taşıyan saatlik dilim
	// alt sınırı (25 olası dilim: kenar saatlerinden biri eksik olabilir).
	HostMinHours = 24
	// HostCloseAfter — kalıcı host / runs art arda başarılı geçişlerde bu
	// kadar görülmezse kapanır.
	HostCloseAfter = 24 * time.Hour
	// MaxHostEntities — kalıcı host tavanı (span sayısına göre en yoğunlar).
	MaxHostEntities = 5_000
	// MaxHostProofNames — geçiş başına kanıt okumasına giren aday tavanı.
	MaxHostProofNames = 500
	// seenReadLimit — chstore.EntitySeenRecent'in LIMIT'i; dolarsa görüntü
	// eksik sayılır (kapanış yok).
	seenReadLimit = 200_000
)

// HostCand — tick'in namespace'siz satırlarından bir host adayı.
type HostCand struct {
	Name          string
	Services      map[string]int64 // servis → span
	ClusterValues map[string]int64
	Spans         int64
	First, Last   time.Time
}

// LooksLikePod — SAF: adın rasgele pod soneki var mı (wlkind.FromPod;
// Deployment / Derived). Sıra numaralı (StatefulSet şekilli) ad hayır.
func LooksLikePod(name string) bool {
	kind, _, ok := wlkind.FromPod(name)
	return ok && kind != wlkind.StatefulSet
}

// HostCandidates — SAF: namespace'siz satırlar → host adayları. knownPods:
// bu tick KSM'nin pod olarak bildiği adlar.
func HostCandidates(rows []SeenRow, knownPods map[string]bool) map[string]*HostCand {
	out := map[string]*HostCand{}
	for _, r := range rows {
		if r.Namespace != "" || r.Pod == "" || knownPods[r.Pod] || LooksLikePod(r.Pod) {
			continue
		}
		c := out[r.Pod]
		if c == nil {
			c = &HostCand{Name: r.Pod, Services: map[string]int64{}, ClusterValues: map[string]int64{}}
			out[r.Pod] = c
		}
		if r.Service != "" {
			c.Services[r.Service] += int64(r.Spans)
		}
		if r.ClusterValue != "" {
			c.ClusterValues[r.ClusterValue] += int64(r.Spans)
		}
		c.Spans += int64(r.Spans)
		if !r.FirstSeen.IsZero() && (c.First.IsZero() || r.FirstSeen.Before(c.First)) {
			c.First = r.FirstSeen
		}
		if r.LastSeen.After(c.Last) {
			c.Last = r.LastSeen
		}
	}
	return out
}

// HostID — küresel host kimliği.
func HostID(name string) string { return "host:" + name }

// HostProof — bir adın kanıt okuması (entity_seen_5m, namespace'siz satırlar).
type HostProof struct {
	Name      string
	FirstSeen time.Time
	LastSeen  time.Time
	Hours     int // since'ten sonra kova taşıyan saatlik dilim
}

// HostKey — kanıt okumasının anahtarı (servis = MV ORDER BY öneki).
type HostKey struct{ Name, Service string }

// HostProofReader — chstore.EntityHostProof (sınırlı: ad tavanı, zaman
// penceresi, LIMIT, max_execution_time).
type HostProofReader interface {
	HostProof(ctx context.Context, keys []HostKey, since, hoursFrom time.Time) ([]HostProof, error)
}

// CHHostProof — chstore.EntityHostProof (yapısal tipleme).
type CHHostProof interface {
	EntityHostProof(ctx context.Context, keys []HostKey, since, hoursFrom time.Time) ([]HostProof, error)
}

type chHostProof struct{ ch CHHostProof }

// HostProofFromCH — main.go: *chstore.Store → HostProofReader.
func HostProofFromCH(ch CHHostProof) HostProofReader { return chHostProof{ch: ch} }

func (a chHostProof) HostProof(ctx context.Context, keys []HostKey, since, hoursFrom time.Time) ([]HostProof, error) {
	return a.ch.EntityHostProof(ctx, keys, since, hoursFrom)
}

// SetHostProof — terfi kanıtı kaynağı (nil = host kalıcı katmana hiç terfi etmez).
func (s *Syncer) SetHostProof(r HostProofReader) { s.hostProof = r }

// Proven — SAF: kanıt satırı terfi şartını sağlıyor mu.
func (p HostProof) Proven(now time.Time) bool {
	return !p.FirstSeen.IsZero() && !p.FirstSeen.After(now.Add(-HostStableAfter)) && p.Hours >= HostMinHours
}

// BuildHostStable — SAF: kanıtlanmış adaylar → kalıcı host entity + runs.
// Tavan span sayısıyla; dönen ikinci değer tavan dışı kalan ad sayısı.
func BuildHostStable(cands map[string]*HostCand, proven map[string]bool, max int) (map[string]Entity, map[string]Relation, int) {
	names := make([]string, 0, len(cands))
	for n := range cands {
		if proven[n] {
			names = append(names, n)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if cands[names[i]].Spans != cands[names[j]].Spans {
			return cands[names[i]].Spans > cands[names[j]].Spans
		}
		return names[i] < names[j]
	})
	dropped := 0
	if len(names) > max {
		dropped = len(names) - max
		names = names[:max]
	}
	ents := map[string]Entity{}
	rels := map[string]Relation{}
	for _, n := range names {
		id := HostID(n)
		ents[id] = Entity{Type: TypeHost, ClusterID: HostClusterID, ID: id, Name: n, Labels: map[string]string{}, Source: SourceSpan}
		for svc := range cands[n].Services {
			sid := ServiceID(svc)
			rels[relKey(RelRuns, id, sid)] = Relation{Type: RelRuns, ClusterID: HostClusterID, ParentID: id, ChildID: sid, Source: SourceSpan}
		}
	}
	return ents, rels, dropped
}

// BuildHostLive — SAF: adayların canlı görüntüsü (kanıtlanmamışlar dahil;
// Stable terfi durumunu söyler).
func BuildHostLive(gen time.Time, cands map[string]*HostCand, stable map[string]Entity) LiveSnapshot {
	out := LiveSnapshot{Format: liveFormat, ClusterID: HostClusterID, Gen: gen.UnixMilli(), Pods: []LivePod{}}
	for _, c := range cands {
		h := LiveHost{Name: c.Name, ClusterValue: argmax(c.ClusterValues), Source: SourceSpan}
		_, h.Stable = stable[HostID(c.Name)]
		for n, sp := range c.Services {
			h.Services = append(h.Services, LiveService{Name: n, Spans: sp})
		}
		sortLiveServices(h.Services)
		if !c.First.IsZero() {
			h.FirstSeen = c.First.Unix()
		}
		if !c.Last.IsZero() {
			h.LastSeen = c.Last.Unix()
		}
		out.Hosts = append(out.Hosts, h)
	}
	sort.Slice(out.Hosts, func(i, j int) bool { return out.Hosts[i].Name < out.Hosts[j].Name })
	return out
}

func sortLiveServices(s []LiveService) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Spans != s[j].Spans {
			return s[i].Spans > s[j].Spans
		}
		return s[i].Name < s[j].Name
	})
}

// syncHosts — host geçişi (lider tick'inde, tick'in span satırlarından).
// Canlı blob her tick; kalıcı katman HostEvery'de bir. rowsOK=false (span
// okuması başarısız) → hiçbir şey yazılmaz; truncated → kapanış yok.
func (s *Syncer) syncHosts(ctx context.Context, rows []SeenRow, rowsOK bool, knownPods map[string]bool) (st clusterTickStats) {
	if !rowsOK {
		return
	}
	now := s.now()
	cands := HostCandidates(rows, knownPods)
	truncated := len(rows) >= seenReadLimit
	stableNow := s.hostStableIDs()
	if s.now().Sub(time.Unix(0, s.hostsLast.Load())) >= HostEvery {
		s.hostsLast.Store(now.UnixNano())
		var ok bool
		st, stableNow, ok = s.syncHostsStable(ctx, now, cands, truncated)
		if ok {
			s.setHostStableIDs(stableNow)
		}
	}
	st.snapBytes = s.publishLive(ctx, HostClusterID, BuildHostLive(now, cands, stableNow))
	return st
}

func (s *Syncer) hostStableIDs() map[string]Entity {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hostStable
}

func (s *Syncer) setHostStableIDs(m map[string]Entity) {
	s.mu.Lock()
	s.hostStable = m
	s.mu.Unlock()
}

// syncHostsStable — kalıcı katman: açık host ömürleri + kanıt okuması
// (yalnız henüz kanıtlanmamış adaylar, ≤ MaxHostProofNames) → diff.
func (s *Syncer) syncHostsStable(ctx context.Context, now time.Time, cands map[string]*HostCand, truncated bool) (clusterTickStats, map[string]Entity, bool) {
	open, err := s.store.OpenLifetimes(ctx, HostClusterID)
	if err != nil {
		s.recordPassFailure(ctx, HostClusterID, now, "hosts", err)
		return clusterTickStats{}, nil, false
	}
	proven := map[string]bool{}
	s.mu.Lock()
	for n := range cands {
		if _, ok := open.Entities[HostID(n)]; ok || s.hostProved[n] {
			proven[n] = true
		}
	}
	s.mu.Unlock()
	need := make([]string, 0)
	for n := range cands {
		if !proven[n] {
			need = append(need, n)
		}
	}
	sort.Slice(need, func(i, j int) bool {
		if cands[need[i]].Spans != cands[need[j]].Spans {
			return cands[need[i]].Spans > cands[need[j]].Spans
		}
		return need[i] < need[j]
	})
	if len(need) > MaxHostProofNames {
		need = need[:MaxHostProofNames]
	}
	newlyProven := 0
	if len(need) > 0 && s.hostProof != nil {
		var keys []HostKey
		for _, n := range need {
			for svc := range cands[n].Services {
				keys = append(keys, HostKey{Name: n, Service: svc})
			}
		}
		proofs, perr := s.hostProof.HostProof(ctx, keys, now.Add(-HostStableAfter-time.Hour), now.Add(-HostStableAfter))
		if perr != nil {
			log.Printf("[entity] hosts kanıt okuması: %v (terfi bu geçişte yok)", perr)
		}
		s.mu.Lock()
		if s.hostProved == nil {
			s.hostProved = map[string]bool{}
		}
		for _, p := range proofs {
			if _, isCand := cands[p.Name]; isCand && p.Proven(now) {
				proven[p.Name], s.hostProved[p.Name] = true, true
				newlyProven++
			}
		}
		s.mu.Unlock()
	}
	ents, rels, dropped := BuildHostStable(cands, proven, MaxHostEntities)
	st, err := s.applyStable(ctx, HostClusterID, now, open, ents, rels, HostCloseAfter, truncated)
	if err != nil {
		return st, nil, false
	}
	log.Printf("[entity] hosts: candidates=%d proof_checked=%d promoted=%d stable=%d runs=%d dropped=%d truncated=%v written=%d/%d closed=%d",
		len(cands), len(need), newlyProven, len(ents), len(rels), dropped, truncated, st.ents, st.rels, st.closed)
	return st, ents, true
}

// ── okuma (resolver) ────────────────────────────────────────────────

// MaxHostNeighbours — HostServices / ServiceHosts cevap tavanı.
const MaxHostNeighbours = 100

// HostServices — host'ta koşan servisler (ad, span payı azalan). "Şimdi"
// canlı blob'dan (kanıtlanmamış aday dahil); geçmiş an ya da ıska → kalıcı
// runs (yalnız terfi etmiş host'lar).
func (r *Resolver) HostServices(ctx context.Context, name string, at time.Time) ([]string, error) {
	if v, ok := r.view(ctx, HostClusterID, at); ok {
		if h, found := v.Host(name); found {
			out := make([]string, 0, len(h.Services))
			for _, s := range h.Services {
				out = append(out, s.Name)
			}
			return capStrings(out, MaxHostNeighbours), nil
		}
	}
	if r.rels == nil {
		return nil, nil
	}
	edges, err := r.rels.RelationsFor(ctx, RelRuns, []string{HostID(name)}, false, at, MaxHostNeighbours)
	out := []string{}
	for _, e := range edges {
		if ref, ok := ParseID(e.ChildID); ok && ref.Type == TypeService {
			out = append(out, ref.Name)
		}
	}
	sort.Strings(out)
	return out, err
}

// ServiceHosts — servisi koşturan host'lar (host: id'leri, sıralı).
func (r *Resolver) ServiceHosts(ctx context.Context, service string, at time.Time) ([]string, error) {
	if v, ok := r.view(ctx, HostClusterID, at); ok {
		var out []string
		for _, h := range v.Hosts {
			for _, s := range h.Services {
				if s.Name == service {
					out = append(out, HostID(h.Name))
					break
				}
			}
		}
		sort.Strings(out)
		return capStrings(out, MaxHostNeighbours), nil
	}
	if r.rels == nil {
		return nil, nil
	}
	// Eski (S1) pod→svc runs satırları da child öneki eşleşir: host: süzülür;
	// ORDER BY parent_id'de "host:" "pod:"dan önce gelir.
	edges, err := r.rels.RelationsFor(ctx, RelRuns, []string{ServiceID(service)}, true, at, MaxHostNeighbours)
	seen := map[string]bool{}
	var out []string
	for _, e := range edges {
		if ref, ok := ParseID(e.ParentID); ok && ref.Type == TypeHost && !seen[e.ParentID] {
			seen[e.ParentID] = true
			out = append(out, e.ParentID)
		}
	}
	sort.Strings(out)
	return out, err
}

func capStrings(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
