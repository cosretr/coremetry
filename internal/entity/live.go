package entity

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// live.go — v0.10.2225 (docs/architecture/entity-storage-cost-note.md, C +
// R1b, E1/E2): CANLI DİZİN. Pod / container ve pod uçlu kenarlar artık CH
// satırı DEĞİL; lider tick'i cluster'ın o anki görüntüsünü (KSM + span
// geçişi, son SeenWindow) TEK gzip'li JSON blob olarak Redis'e yazar:
//
//	coremetry:entity:v1:live:<cid>   TTL = LiveTTL (3 tick)
//
// Redis ASLA gerçeğin kaynağı değildir: anahtar yoksa (TTL, flush,
// tahliye, Redis kapalı) cevap "bilinmiyor"dur ve okuyucu S0'ın MV yoluna
// (entity_seen_5m) düşer; bir sonraki tick blob'u yeniden kurar. "T anında
// canlı mıydı" sorusu buraya hiç gelmez (Resolver.isNow).

// LiveKeyPrefix — cluster başına blob anahtarının öneki.
const LiveKeyPrefix = "coremetry:entity:v1:live:"

// LiveTTL — blob ömrü: 3 tick. Ölen liderin görüntüsü en geç bu kadar sonra düşer.
const LiveTTL = 3 * SyncInterval

// LiveLocalTTL — okuyucu replikanın çözülmüş kopyayı yeniden çekme aralığı
// (ıska da bu kadar önbelleklenir: Redis kapalıyken istek başına Get yok).
const LiveLocalTTL = 30 * time.Second

const liveFormat = 1

// LiveKey — cluster'ın blob anahtarı.
func LiveKey(cid string) string { return LiveKeyPrefix + cid }

// LiveService — pod'un servisi + penceredeki span sayısı.
type LiveService struct {
	Name  string `json:"n"`
	Spans int64  `json:"s,omitempty"`
}

// LivePod — dizindeki bir pod (kısa JSON alanları: blob boyu).
type LivePod struct {
	Namespace    string        `json:"ns"`
	Name         string        `json:"n"`
	UID          string        `json:"u,omitempty"`
	Workload     string        `json:"w,omitempty"`  // wl: id (KSM sahibi ya da türetilmiş)
	Node         string        `json:"no,omitempty"` // node adı
	Services     []LiveService `json:"s,omitempty"`  // span payı azalan
	ClusterValue string        `json:"cv,omitempty"` // span cluster değeri
	Source       string        `json:"src,omitempty"`
	FirstSeen    int64         `json:"f,omitempty"` // span ilk görülme (unix sn; pencere içi)
	LastSeen     int64         `json:"l,omitempty"` // span son görülme (unix sn)
}

// LiveHost — v0.10.2260 (S6 parça 1): K8s dışı host adayı (yalnız
// HostClusterID blob'unda). Stable: kalıcı katmana terfi etti mi (≥ 24 sa
// kesintisiz görülme, hosts.go); terfi etmemiş aday YALNIZ burada.
type LiveHost struct {
	Name         string        `json:"n"`
	Services     []LiveService `json:"s,omitempty"` // span payı azalan
	ClusterValue string        `json:"cv,omitempty"`
	Source       string        `json:"src,omitempty"`
	Stable       bool          `json:"st,omitempty"`
	FirstSeen    int64         `json:"f,omitempty"`
	LastSeen     int64         `json:"l,omitempty"`
}

// LiveSnapshot — bir cluster'ın tek tick'lik canlı görüntüsü.
type LiveSnapshot struct {
	Format    int       `json:"v"`
	ClusterID string    `json:"cid"`
	Gen       int64     `json:"gen"` // tick anı (unix ms)
	Pods      []LivePod `json:"pods"`
	// Hosts — v0.10.2260: yalnız HostClusterID blob'u (ek alan; biçim aynı).
	Hosts []LiveHost `json:"hosts,omitempty"`
}

// BuildLiveSnapshot — SAF: tick'in birleşik görüntüsünden (Thanos + span
// geçişi) pod dizini. ents/rels syncCluster'ın entByID / relByKey'i.
func BuildLiveSnapshot(cid string, gen time.Time, ents map[string]Entity, rels map[string]Relation, spanRows []SeenRow) LiveSnapshot {
	pods := map[string]*LivePod{}
	for id, e := range ents {
		if e.Type != TypePod {
			continue
		}
		p := &LivePod{Namespace: e.Namespace, Name: e.Name, UID: e.UID, Source: e.Source}
		if strings.HasPrefix(e.ParentID, "wl:") {
			p.Workload = e.ParentID
		}
		pods[id] = p
	}
	type agg struct {
		spans       map[string]int64
		cv          map[string]int64
		first, last time.Time
	}
	byPod := map[string]*agg{}
	for _, r := range spanRows {
		if r.Namespace == "" || r.Pod == "" {
			continue
		}
		id := PodID(cid, r.Namespace, r.Pod)
		a := byPod[id]
		if a == nil {
			a = &agg{spans: map[string]int64{}, cv: map[string]int64{}}
			byPod[id] = a
		}
		if r.Service != "" {
			a.spans[r.Service] += int64(r.Spans)
		}
		if r.ClusterValue != "" {
			a.cv[r.ClusterValue] += int64(r.Spans)
		}
		if !r.FirstSeen.IsZero() && (a.first.IsZero() || r.FirstSeen.Before(a.first)) {
			a.first = r.FirstSeen
		}
		if r.LastSeen.After(a.last) {
			a.last = r.LastSeen
		}
	}
	for _, rl := range rels {
		p := pods[rl.ParentID]
		if p == nil {
			continue
		}
		switch rl.Type {
		case RelRunsOn:
			if ref, ok := ParseID(rl.ChildID); ok && p.Node == "" {
				p.Node = ref.Name
			}
		case RelRuns:
			name := strings.TrimPrefix(rl.ChildID, "svc:")
			if a := byPod[rl.ParentID]; a != nil {
				if _, ok := a.spans[name]; !ok {
					a.spans[name] = 0
				}
			} else {
				byPod[rl.ParentID] = &agg{spans: map[string]int64{name: 0}, cv: map[string]int64{}}
			}
		}
	}
	for id, a := range byPod {
		p := pods[id]
		if p == nil {
			continue
		}
		for name, n := range a.spans {
			p.Services = append(p.Services, LiveService{Name: name, Spans: n})
		}
		sort.Slice(p.Services, func(i, j int) bool {
			if p.Services[i].Spans != p.Services[j].Spans {
				return p.Services[i].Spans > p.Services[j].Spans
			}
			return p.Services[i].Name < p.Services[j].Name
		})
		p.ClusterValue = argmax(a.cv)
		if !a.first.IsZero() {
			p.FirstSeen = a.first.Unix()
		}
		if !a.last.IsZero() {
			p.LastSeen = a.last.Unix()
		}
	}
	out := LiveSnapshot{Format: liveFormat, ClusterID: cid, Gen: gen.UnixMilli(), Pods: make([]LivePod, 0, len(pods))}
	for _, p := range pods {
		out.Pods = append(out.Pods, *p)
	}
	sort.Slice(out.Pods, func(i, j int) bool {
		if out.Pods[i].Namespace != out.Pods[j].Namespace {
			return out.Pods[i].Namespace < out.Pods[j].Namespace
		}
		return out.Pods[i].Name < out.Pods[j].Name
	})
	return out
}

// EncodeLive — gzip(JSON).
func EncodeLive(s LiveSnapshot) ([]byte, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeLive — EncodeLive'ın tersi; bilinmeyen biçim hata (ıska sayılır).
func DecodeLive(b []byte) (LiveSnapshot, error) {
	var s LiveSnapshot
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return s, err
	}
	raw, err := io.ReadAll(io.LimitReader(zr, 256<<20))
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	if s.Format != liveFormat {
		return s, fmt.Errorf("entity live: biçim %d", s.Format)
	}
	return s, nil
}

// LiveStore — cache.Cache'in gereken iki metodu (yapısal tipleme; entity
// paketi cache'i import etmez).
type LiveStore interface {
	Get(ctx context.Context, key string) ([]byte, bool, error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
}

// LiveView — çözülmüş blob + arama dizini.
type LiveView struct {
	ClusterID string
	Gen       time.Time
	Pods      []LivePod
	Hosts     []LiveHost
	byKey     map[string]int // ns/pod → Pods indeksi
	byHost    map[string]int // ad → Hosts indeksi
}

func newLiveView(s LiveSnapshot) *LiveView {
	v := &LiveView{ClusterID: s.ClusterID, Gen: time.UnixMilli(s.Gen), Pods: s.Pods, Hosts: s.Hosts,
		byKey: make(map[string]int, len(s.Pods)), byHost: make(map[string]int, len(s.Hosts))}
	for i, p := range s.Pods {
		v.byKey[p.Namespace+"/"+p.Name] = i
	}
	for i, h := range s.Hosts {
		v.byHost[h.Name] = i
	}
	return v
}

// Host — adla host (yalnız HostClusterID görüntüsü).
func (v *LiveView) Host(name string) (LiveHost, bool) {
	if v == nil {
		return LiveHost{}, false
	}
	i, ok := v.byHost[name]
	if !ok {
		return LiveHost{}, false
	}
	return v.Hosts[i], true
}

// Pod — ns/ad ile pod.
func (v *LiveView) Pod(ns, name string) (LivePod, bool) {
	if v == nil {
		return LivePod{}, false
	}
	i, ok := v.byKey[ns+"/"+name]
	if !ok {
		return LivePod{}, false
	}
	return v.Pods[i], true
}

// LiveIndex — okuyucu: cluster başına çözülmüş kopya, LiveLocalTTL'de bir
// Get. Gen'i LiveTTL'den eski kopya kullanılmaz (ölü liderin görüntüsü).
type LiveIndex struct {
	kv  LiveStore
	now func() time.Time

	mu sync.Mutex
	m  map[string]liveEntry
}

type liveEntry struct {
	view    *LiveView
	fetched time.Time
}

// NewLiveIndex — kv nil ise her okuma ıskadır.
func NewLiveIndex(kv LiveStore) *LiveIndex {
	return &LiveIndex{kv: kv, now: time.Now, m: map[string]liveEntry{}}
}

var liveIndexes sync.Map // LiveStore → *LiveIndex

// LiveIndexFor — süreç başına tek okuyucu (api istek başına Resolver kurar;
// çözülmüş kopya istekler arasında paylaşılır). kv nil → nil.
func LiveIndexFor(kv LiveStore) *LiveIndex {
	if kv == nil {
		return nil
	}
	if v, ok := liveIndexes.Load(kv); ok {
		return v.(*LiveIndex)
	}
	v, _ := liveIndexes.LoadOrStore(kv, NewLiveIndex(kv))
	return v.(*LiveIndex)
}

// View — cluster'ın taze görüntüsü; yoksa (anahtar yok, Redis hatası,
// bozuk blob, bayat Gen) false → çağıran MV yoluna düşer.
func (l *LiveIndex) View(ctx context.Context, cid string) (*LiveView, bool) {
	if l == nil || l.kv == nil || cid == "" {
		return nil, false
	}
	now := l.now()
	l.mu.Lock()
	e, ok := l.m[cid]
	l.mu.Unlock()
	if !ok || now.Sub(e.fetched) >= LiveLocalTTL {
		e.fetched = now
		raw, hit, err := l.kv.Get(ctx, LiveKey(cid))
		switch {
		case err != nil:
			// Redis kesintisi: elimizdeki kopya Gen'i taze kaldıkça kullanılır.
		case !hit:
			e.view = nil
		default:
			if s, derr := DecodeLive(raw); derr == nil && s.ClusterID == cid {
				e.view = newLiveView(s)
			} else {
				e.view = nil
			}
		}
		l.mu.Lock()
		l.m[cid] = e
		l.mu.Unlock()
	}
	if e.view == nil || now.Sub(e.view.Gen) > LiveTTL {
		return nil, false
	}
	return e.view, true
}
