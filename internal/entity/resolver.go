package entity

import (
	"context"
	"sort"
	"strings"
	"time"
)

// resolver.go — v0.10.2193 (Entity S0, docs/architecture/
// entity-autodiscovery-plan.md §4): TEK çözümleyici v0. Sayfalar arası her
// bağ (pod → servis, iş yükü → servis, pod → node) buradan geçer; sayfaya
// özel join yazılmaz. v0 mevcut kaynaklar üstünde, şema yok:
//
//   - pod → servis: entity_seen_5m "runs" kenarları (span payı en büyük;
//     IndexPodEdges). Metrik kaynağı S6'da (VM); ham metric_points yok.
//   - iş yükü → servis: ResolveWorkloadService sırası (pod runs çoğunluğu >
//     k8s.deployment.name attr bağı > ad eşitliği, son çare).
//   - iş yükü ↔ pod sahipliği: S0'da depo okumuyor; çağıran KSM'den bildiği
//     sahipliği `owned` ile verir (S2'de depodan).
//
// Belirsizlikte yanlış kenar yerine kenar yok (alan "").

// SeenWindow — pod → servis kenarının bakış penceresi (düşük trafikli
// servisin pod'u 15 dk'da span basmayabilir).
const SeenWindow = time.Hour

// MaxPathIDs — tek MV okumasının pod tavanı (EntitySeenForPods sözleşmesi);
// fazlası aynı cluster için ek okumaya bölünür.
const MaxPathIDs = 500

// ResolverSource — v0 kaynakları (yapısal tipleme; api katmanı chstore'u bağlar).
type ResolverSource interface {
	// SeenForPods — entity_seen_5m, zaman sınırlı + LIMIT (namespace "" = süzgeçsiz).
	SeenForPods(ctx context.Context, clusterValues []string, namespace string, pods []string, from, to time.Time) ([]SeenEdge, error)
	DeploymentLinks(ctx context.Context) ([]DeploymentLink, error)
	KnownServices(ctx context.Context) ([]string, error)
}

// ClusterMap — entity cid ↔ span cluster değerleri (Remote Cluster kaydı).
type ClusterMap interface {
	Values(cid string) []string
	ID(value string) (string, bool)
}

// Resolver — istek başına kurulur (durumsuz); önbellek çağıranın serveCached'i.
type Resolver struct {
	src      ResolverSource
	clusters ClusterMap
}

func NewResolver(src ResolverSource, clusters ClusterMap) *Resolver {
	return &Resolver{src: src, clusters: clusters}
}

// Hint — ham sinyal (span/metrik/log alanları).
type Hint struct{ ClusterValue, Namespace, Pod, Host, Service, Workload string }

// Node — çözülmüş entity.
type Node struct {
	ID   string
	Type string
	Name string
}

// Path — pod → iş yükü → servis → namespace → node → cluster (entity id'leri;
// "" = bilinmiyor).
type Path struct {
	Pod, Workload, Service, Namespace, Node, Cluster string
	// ClusterValue — pod'un span'lerinin taşıdığı cluster DEĞERİ (yalnız pod
	// yolunda, kenar varsa). Trace süzgeci ham ad değil bu değerle (v0.10.2195).
	ClusterValue string
}

// ServiceName — svc:<ad> → ad.
func (p Path) ServiceName() string { return strings.TrimPrefix(p.Service, "svc:") }

// NodeName — node:<cid>/<ad> → ad.
func (p Path) NodeName() string {
	if r, ok := ParseID(p.Node); ok {
		return r.Name
	}
	return ""
}

// Lookup — ham sinyal → entity'ler. Servis adı → svc; cluster değeri +
// pod → pod ve span payına göre sıralı servisleri; iş yükü → wl (Kind
// bilinmiyorsa Derived). Kayıtsız cluster değeri → pod/iş yükü yok (K2 S1'de).
func (r *Resolver) Lookup(ctx context.Context, h Hint, at time.Time) []Node {
	var out []Node
	seenSvc := map[string]bool{}
	addSvc := func(name string) {
		if name != "" && !seenSvc[name] {
			seenSvc[name] = true
			out = append(out, Node{ID: ServiceID(name), Type: TypeService, Name: name})
		}
	}
	addSvc(h.Service)
	cid, ok := "", false
	if h.ClusterValue != "" && r.clusters != nil {
		cid, ok = r.clusters.ID(h.ClusterValue)
	}
	if ok && h.Pod != "" && h.Namespace != "" {
		out = append(out, Node{ID: PodID(cid, h.Namespace, h.Pod), Type: TypePod, Name: h.Pod})
		if r.src != nil {
			seen, err := r.src.SeenForPods(ctx, []string{h.ClusterValue}, h.Namespace, []string{h.Pod}, at.Add(-SeenWindow), at)
			if err == nil {
				for _, s := range rankServices(h.Namespace, h.Pod, seen) {
					addSvc(s)
				}
			}
		}
	}
	if ok && h.Workload != "" && h.Namespace != "" {
		out = append(out, Node{ID: WorkloadID(cid, h.Namespace, DerivedKind, h.Workload), Type: TypeWorkload, Name: h.Workload})
	}
	return out
}

// rankServices — SAF: pod'un servisleri, span payı azalan (eşitlikte ad).
func rankServices(ns, pod string, seen []SeenEdge) []string {
	spans := map[string]int64{}
	for _, e := range seen {
		if e.Pod == pod && e.Service != "" && (e.Namespace == "" || e.Namespace == ns) {
			spans[e.Service] += e.Spans
		}
	}
	out := make([]string, 0, len(spans))
	for s := range spans {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if spans[out[i]] != spans[out[j]] {
			return spans[out[i]] > spans[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Paths — liste sayfaları için toplu yol çözümü. ids: pod: ve wl: id'leri
// (diğer tipler yok sayılır). owned: wl id → pod id'leri (S0'da KSM'den,
// çağıran verir). Best-effort: kaynak hatası ilgili alanları boş bırakır,
// ilk hata döner.
func (r *Resolver) Paths(ctx context.Context, ids []string, at time.Time, owned map[string][]string) (map[string]Path, error) {
	ownerOf := map[string]string{}
	for wl, pods := range owned {
		for _, p := range pods {
			ownerOf[p] = wl
		}
	}
	// cid → namespace → pod adları (iş yüklerinin pod'ları dahil).
	type nsPods map[string]map[string]bool
	want := map[string]nsPods{}
	addPod := func(ref Ref) {
		m := want[ref.ClusterID]
		if m == nil {
			m = nsPods{}
			want[ref.ClusterID] = m
		}
		if m[ref.Namespace] == nil {
			m[ref.Namespace] = map[string]bool{}
		}
		m[ref.Namespace][ref.Name] = true
	}
	refs := map[string]Ref{}
	for _, id := range ids {
		ref, ok := ParseID(id)
		if !ok {
			continue
		}
		switch ref.Type {
		case TypePod:
			refs[id] = ref
			addPod(ref)
		case TypeWorkload:
			refs[id] = ref
			for _, p := range owned[id] {
				if pr, ok := ParseID(p); ok && pr.Type == TypePod {
					addPod(pr)
				}
			}
		}
	}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// (cid, ns) → pod → kenar.
	edges := map[string]map[string]PodEdges{}
	for cid, byNS := range want {
		var values []string
		if r.clusters != nil {
			values = r.clusters.Values(cid)
		}
		if len(values) == 0 || r.src == nil {
			continue
		}
		var all []string
		filterNS := ""
		for ns, pods := range byNS {
			filterNS = ns
			for p := range pods {
				all = append(all, p)
			}
		}
		if len(byNS) > 1 {
			filterNS = "" // çok namespace'li liste: süzgeç Go'da (IndexPodEdges)
		}
		sort.Strings(all)
		var seen []SeenEdge
		for i := 0; i < len(all); i += MaxPathIDs {
			end := min(i+MaxPathIDs, len(all))
			s, err := r.src.SeenForPods(ctx, values, filterNS, all[i:end], at.Add(-SeenWindow), at)
			keep(err)
			seen = append(seen, s...)
		}
		for ns := range byNS {
			edges[cid+"/"+ns] = IndexPodEdges(ns, seen)
		}
	}
	var links []DeploymentLink
	var known []string
	fetched := false
	out := make(map[string]Path, len(refs))
	for id, ref := range refs {
		p := Path{Namespace: NamespaceID(ref.ClusterID, ref.Namespace), Cluster: ClusterID(ref.ClusterID)}
		e := edges[ref.ClusterID+"/"+ref.Namespace]
		switch ref.Type {
		case TypePod:
			p.Pod, p.Workload = id, ownerOf[id]
			pe := e[ref.Name]
			if pe.Service != "" {
				p.Service = ServiceID(pe.Service)
				p.ClusterValue = pe.ClusterValue
			}
			if pe.Node != "" {
				p.Node = NodeID(ref.ClusterID, pe.Node)
			}
		case TypeWorkload:
			p.Workload = id
			var pods []string
			for _, pid := range owned[id] {
				if pr, ok := ParseID(pid); ok {
					pods = append(pods, pr.Name)
				}
			}
			svc := ResolveWorkloadService(ref.Name, ref.Namespace, pods, e, nil, nil)
			if svc == "" && r.src != nil {
				if !fetched {
					fetched = true
					var err error
					links, err = r.src.DeploymentLinks(ctx)
					keep(err)
					known, err = r.src.KnownServices(ctx)
					keep(err)
				}
				svc = ResolveWorkloadService(ref.Name, ref.Namespace, pods, e, links, known)
			}
			if svc != "" {
				p.Service = ServiceID(svc)
			}
		}
		out[id] = p
	}
	return out, firstErr
}
