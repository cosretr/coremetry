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
//   - iş yükü ↔ pod sahipliği: çağıran KSM'den bildiği sahipliği `owned`
//     ile verebilir; vermezse canlı dizinden.
//
// v0.10.2225 (entity-storage-cost-note C + R1b): pod'lar CH'de satır
// DEĞİL. "Şimdi" soruları (at sıfır ya da son 2 tick) önce lider tick'inin
// Redis'e yazdığı canlı dizinden (live.go) cevaplanır; ıska / Redis
// kesintisi → aynı soruyu entity_seen_5m'den (S0 yolu). "T anında canlı
// mıydı" HER ZAMAN MV'den. Pod okuyan her yüzey (api/entities.go,
// mcptools) Pods / PodsByID'den geçer.
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
	// SeenPods — v0.10.2225: cluster değerlerinin pencerede span basan pod'ları
	// (namespace "" = tümü), zaman sınırlı + LIMIT. Pods'un MV yedeği.
	SeenPods(ctx context.Context, clusterValues []string, namespace string, from, to time.Time) ([]SeenEdge, error)
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
	live     *LiveIndex
	now      func() time.Time
}

func NewResolver(src ResolverSource, clusters ClusterMap) *Resolver {
	return &Resolver{src: src, clusters: clusters, now: time.Now}
}

// WithLive — canlı dizin okuyucusu (nil = yalnız MV yolu).
func (r *Resolver) WithLive(l *LiveIndex) *Resolver {
	r.live = l
	return r
}

// isNow — soru "şimdi" mi: at sıfır ya da son 2 tick içinde. Daha eski an
// canlı dizine hiç sorulmaz.
func (r *Resolver) isNow(at time.Time) bool {
	if at.IsZero() {
		return true
	}
	d := r.now().Sub(at)
	return d >= -SyncInterval && d <= 2*SyncInterval
}

// view — "şimdi" sorusunda cluster'ın canlı görüntüsü.
func (r *Resolver) view(ctx context.Context, cid string, at time.Time) (*LiveView, bool) {
	if r.live == nil || !r.isNow(at) {
		return nil, false
	}
	return r.live.View(ctx, cid)
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
		served := false
		if v, live := r.view(ctx, cid, at); live {
			if p, found := v.Pod(h.Namespace, h.Pod); found {
				served = true
				for _, s := range p.Services {
					addSvc(s.Name)
				}
			}
		}
		if !served && r.src != nil {
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
// (diğer tipler yok sayılır). owned: wl id → pod id'leri (KSM'den, çağıran
// verir; vermezse "şimdi" sorusunda canlı dizinden). Pod kenarları önce
// canlı dizinden, bulunamayanlar MV'den. Best-effort: kaynak hatası ilgili
// alanları boş bırakır, ilk hata döner.
func (r *Resolver) Paths(ctx context.Context, ids []string, at time.Time, owned map[string][]string) (map[string]Path, error) {
	views := map[string]*LiveView{}
	viewOf := func(cid string) *LiveView {
		v, done := views[cid]
		if !done {
			v, _ = r.view(ctx, cid, at)
			views[cid] = v
		}
		return v
	}
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
	wlPods := map[string][]string{} // wl id → pod adları
	for _, id := range ids {
		ref, ok := ParseID(id)
		if !ok {
			continue
		}
		switch ref.Type {
		case TypePod:
			refs[id] = ref
			addPod(ref)
			if _, known := ownerOf[id]; !known {
				if p, found := viewOf(ref.ClusterID).Pod(ref.Namespace, ref.Name); found {
					ownerOf[id] = p.Workload
				}
			}
		case TypeWorkload:
			refs[id] = ref
			if pods, given := owned[id]; given {
				for _, p := range pods {
					if pr, ok := ParseID(p); ok && pr.Type == TypePod {
						addPod(pr)
						wlPods[id] = append(wlPods[id], pr.Name)
					}
				}
				continue
			}
			if v := viewOf(ref.ClusterID); v != nil {
				for _, p := range v.Pods {
					if p.Namespace == ref.Namespace && SameWorkload(p.Workload, id) {
						addPod(Ref{Type: TypePod, ClusterID: ref.ClusterID, Namespace: p.Namespace, Name: p.Name})
						wlPods[id] = append(wlPods[id], p.Name)
					}
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
		v := viewOf(cid)
		var seen []SeenEdge
		var all []string
		filterNS := ""
		for ns, pods := range byNS {
			filterNS = ns
			for p := range pods {
				if lp, found := v.Pod(ns, p); found {
					seen = append(seen, lp.edges()...)
					continue
				}
				all = append(all, p)
			}
		}
		if len(byNS) > 1 {
			filterNS = "" // çok namespace'li liste: süzgeç Go'da (IndexPodEdges)
		}
		var values []string
		if r.clusters != nil {
			values = r.clusters.Values(cid)
		}
		if len(all) > 0 && len(values) > 0 && r.src != nil {
			sort.Strings(all)
			for i := 0; i < len(all); i += MaxPathIDs {
				end := min(i+MaxPathIDs, len(all))
				s, err := r.src.SeenForPods(ctx, values, filterNS, all[i:end], at.Add(-SeenWindow), at)
				keep(err)
				seen = append(seen, s...)
			}
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
			pods := wlPods[id]
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

// edges — dizindeki pod → SeenEdge'ler (IndexPodEdges girdisi; servisi
// yoksa yalnız node taşıyan tek satır).
func (p LivePod) edges() []SeenEdge {
	if len(p.Services) == 0 {
		return []SeenEdge{{Namespace: p.Namespace, Pod: p.Name, Node: p.Node, Cluster: p.ClusterValue}}
	}
	out := make([]SeenEdge, len(p.Services))
	for i, s := range p.Services {
		out[i] = SeenEdge{Namespace: p.Namespace, Pod: p.Name, Node: p.Node, Service: s.Name, Spans: s.Spans, Cluster: p.ClusterValue}
	}
	return out
}

// SameWorkload — iki wl: id aynı iş yükü mü: cluster + namespace + ad;
// tür yok sayılır (KSM RS sahibi eksikse ReplicaSet, ad türetmesi Derived).
func SameWorkload(a, b string) bool {
	if a == b {
		return a != ""
	}
	ra, ok1 := ParseID(a)
	rb, ok2 := ParseID(b)
	return ok1 && ok2 && ra.Type == TypeWorkload && rb.Type == TypeWorkload &&
		ra.ClusterID == rb.ClusterID && ra.Namespace == rb.Namespace && ra.Name == rb.Name
}
