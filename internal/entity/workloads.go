package entity

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// workloads.go — v0.10.2262 (Entity S6 parça 3; docs/architecture/
// entity-s6-cost-note.md bulgu 2, seçenek C): AÇIK İŞ YÜKÜ ADI, şemasız.
//
// workload_revision_activity_1m (0012, rollout katmanı) span'in
// k8s.deployment/statefulset/daemonset.name ve replicaset adını zaten
// topluyor. Lider tick'i 15 dk'da bir son 1 saati okur (GROUP BY cluster,
// ns, iş yükü, revizyon, servis; LIMIT, 10 s) ve span geçişine verir:
//
//   - KSM'nin görmediği pod'un iş yükü: önce revizyon eşleşmesi (pod adı
//     "<replicaset>-<rand5>", replicaset bu tabloda → Deployment <ad>), sonra
//     ad türevinin (wlkind.FromPod) tablodaki adla doğrulanması (tür tablodan:
//     StatefulSet / DaemonSet; Derived yerini gerçek türe bırakır). Eşleşen
//     iş yükü "derived" etiketi taşımaz (yetkili sayılır; eski Derived kimlik
//     S2 same_as ile ona bağlanır), etiket source=attr.
//   - Tablodaki her (iş yükü, servis) çifti için serves (wl→svc) kenarı ve
//     KSM bilmiyorsa iş yükü entity'si (kaynak önceliğinde KSM'den sonra).
//
// Tablonun eşleyemediği pod (attr'sız ya da revizyon penceresi dışında) için
// iş yükü ad türevi kalır. Tablo yoksa (0012 uygulanmamış) geçiş süreçte bir
// kez günlükler ve atlanır.

// Sabitler (ayar yok, K1).
const (
	ExplicitWorkloadsEvery  = 15 * time.Minute
	ExplicitWorkloadsWindow = time.Hour
	ExplicitWorkloadsTTL    = 2*ExplicitWorkloadsEvery + SyncInterval
	MaxExplicitWorkloadRows = 50_000
)

// ErrExplicitWorkloadsAbsent — workload_revision_activity_1m bu kurulumda yok.
var ErrExplicitWorkloadsAbsent = errors.New("workload_revision_activity_1m yok (0012 uygulanmamış)")

// ExplicitWorkload — tablodan bir (cluster değeri, ns, iş yükü, revizyon, servis) satırı.
type ExplicitWorkload struct {
	ClusterValue, Namespace, Name, Kind, Revision, Service string
}

// ExplicitWorkloadSource — chstore.EntityExplicitWorkloads (sınırlı).
type ExplicitWorkloadSource interface {
	RecentWorkloads(ctx context.Context, since time.Time, limit int) ([]ExplicitWorkload, bool, error)
}

// CHExplicitWorkloads — chstore.EntityExplicitWorkloads (yapısal tipleme).
type CHExplicitWorkloads interface {
	EntityExplicitWorkloads(ctx context.Context, since time.Time, limit int) ([]ExplicitWorkload, bool, error)
}

type chExplicit struct{ ch CHExplicitWorkloads }

// ExplicitWorkloadsFromCH — main.go: *chstore.Store → ExplicitWorkloadSource.
func ExplicitWorkloadsFromCH(ch CHExplicitWorkloads) ExplicitWorkloadSource {
	return chExplicit{ch: ch}
}

func (a chExplicit) RecentWorkloads(ctx context.Context, since time.Time, limit int) ([]ExplicitWorkload, bool, error) {
	return a.ch.EntityExplicitWorkloads(ctx, since, limit)
}

// SetExplicitWorkloads — kaynak (nil = ad türevi tek yol).
func (s *Syncer) SetExplicitWorkloads(src ExplicitWorkloadSource) { s.explicit = src }

// ExplicitIndex — bir cluster'ın açık iş yükü dizini.
type ExplicitIndex struct {
	byRev  map[string][2]string // ns/replicaset → (tür, ad)
	byName map[string]string    // ns/ad → tür
	Rows   []ExplicitWorkload
}

func canonicalKind(k string) string {
	switch k {
	case wlkind.Deployment, wlkind.StatefulSet, wlkind.DaemonSet:
		return k
	}
	return ""
}

// BuildExplicitIndexes — SAF: satırlar → cid başına dizin (cluster değeri
// eşlemesi GroupSeenByCluster'ın aynısı; eşlenemeyen değer, span-only
// cluster'ın id'si olarak kalır).
func BuildExplicitIndexes(rows []ExplicitWorkload, refs []ClusterRef) map[string]*ExplicitIndex {
	byValue := map[string]string{}
	for _, r := range refs {
		if r.ID == "" {
			continue
		}
		vals := append([]string{r.SpanClusterValue}, r.SpanClusterValues...)
		if r.SpanClusterValue == "" && len(r.SpanClusterValues) == 0 {
			vals = []string{r.Name}
		}
		for _, v := range vals {
			if v != "" {
				byValue[v] = r.ID
			}
		}
	}
	out := map[string]*ExplicitIndex{}
	for _, w := range rows {
		kind := canonicalKind(w.Kind)
		if w.Namespace == "" || w.Name == "" || kind == "" {
			continue
		}
		cid, ok := byValue[w.ClusterValue]
		if !ok {
			cid = w.ClusterValue // K2 span-only cluster id = değer
		}
		if cid == "" {
			continue
		}
		ix := out[cid]
		if ix == nil {
			ix = &ExplicitIndex{byRev: map[string][2]string{}, byName: map[string]string{}}
			out[cid] = ix
		}
		w.Kind = kind
		ix.Rows = append(ix.Rows, w)
		ix.byName[w.Namespace+"/"+w.Name] = kind
		if kind == wlkind.Deployment && w.Revision != "" {
			ix.byRev[w.Namespace+"/"+w.Revision] = [2]string{kind, w.Name}
		}
	}
	return out
}

// ForPod — SAF: sahibi bilinmeyen pod'un açık iş yükü. derivedKind/Name/ok:
// wlkind.FromPod çıktısı. found=false → ad türevi kalır.
func (ix *ExplicitIndex) ForPod(ns, pod, derivedKind, derivedName string, ok bool) (kind, name string, found bool) {
	if ix == nil {
		return "", "", false
	}
	if i := strings.LastIndexByte(pod, '-'); i > 0 {
		if kn, hit := ix.byRev[ns+"/"+pod[:i]]; hit {
			return kn[0], kn[1], true
		}
	}
	if ok {
		if k, hit := ix.byName[ns+"/"+derivedName]; hit {
			return k, derivedName, true
		}
	}
	return "", "", false
}

// explicitIndexes — TTL içindeki son dizinler (yoksa nil).
func (s *Syncer) explicitIndexes(now time.Time) map[string]*ExplicitIndex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.explicitIx == nil || now.Sub(s.explicitAt) > ExplicitWorkloadsTTL {
		return nil
	}
	return s.explicitIx
}

// refreshExplicit — 15 dk'da bir tek sınırlı okuma (lider tick'inde).
// Hata: önceki dizin TTL'ine kadar kullanılır; tablo yoksa bir kez günlük.
func (s *Syncer) refreshExplicit(ctx context.Context, refs []ClusterRef) int {
	now := s.now()
	if s.explicit == nil || now.Sub(time.Unix(0, s.explicitLast.Load())) < ExplicitWorkloadsEvery {
		return 0
	}
	s.explicitLast.Store(now.UnixNano())
	rows, truncated, err := s.explicit.RecentWorkloads(ctx, now.Add(-ExplicitWorkloadsWindow), MaxExplicitWorkloadRows)
	if errors.Is(err, ErrExplicitWorkloadsAbsent) {
		if s.explicitSkipLogged.CompareAndSwap(false, true) {
			log.Printf("[entity] workloads: açık iş yükü geçişi atlandı — %v; pod → iş yükü ad türevi (bu mesaj süreçte bir kez)", err)
		}
		return 0
	}
	if err != nil {
		log.Printf("[entity] workloads: açık iş yükü okunamadı: %v (önceki dizin TTL'ine kadar)", err)
		return 0
	}
	ix := BuildExplicitIndexes(rows, refs)
	s.mu.Lock()
	s.explicitIx, s.explicitAt = ix, now
	s.mu.Unlock()
	log.Printf("[entity] workloads: rows=%d clusters=%d truncated=%v", len(rows), len(ix), truncated)
	return len(rows)
}

// ExplicitServes — SAF: dizinin (iş yükü, servis) çiftleri → entity
// (KSM bilmiyorsa) + serves kenarı. known: tick'in Thanos varlıkları;
// knownWl: ns/ad → KSM iş yükü id'si.
func ExplicitServes(cid string, ix *ExplicitIndex, known map[string]Entity, knownWl map[string]string, ents map[string]Entity, rels map[string]Relation) {
	if ix == nil {
		return
	}
	for _, w := range ix.Rows {
		wlID := WorkloadID(cid, w.Namespace, w.Kind, w.Name)
		nsID := NamespaceID(cid, w.Namespace)
		if id, ok := knownWl[w.Namespace+"/"+w.Name]; ok {
			wlID = id
		} else if _, ok := known[wlID]; !ok {
			if _, dup := ents[wlID]; !dup {
				if _, ok := known[nsID]; !ok {
					if _, dup := ents[nsID]; !dup {
						ents[nsID] = Entity{Type: TypeNamespace, ClusterID: cid, ID: nsID, Name: w.Namespace, ParentID: ClusterID(cid), Labels: map[string]string{}, Source: SourceSpan}
						rels[RelParent+"|"+ClusterID(cid)+"|"+nsID] = Relation{Type: RelParent, ClusterID: cid, ParentID: ClusterID(cid), ChildID: nsID, Source: SourceSpan}
					}
				}
				ents[wlID] = Entity{Type: TypeWorkload, ClusterID: cid, ID: wlID, Namespace: w.Namespace, Name: w.Name, ParentID: nsID,
					Labels: map[string]string{"kind": w.Kind, "source": "attr"}, Source: SourceSpan}
				rels[RelParent+"|"+nsID+"|"+wlID] = Relation{Type: RelParent, ClusterID: cid, ParentID: nsID, ChildID: wlID, Source: SourceSpan}
			}
		}
		if w.Service == "" {
			continue
		}
		svcID := ServiceID(w.Service)
		if _, ok := known[svcID]; !ok {
			if _, dup := ents[svcID]; !dup {
				ents[svcID] = Entity{Type: TypeService, ClusterID: cid, ID: svcID, Name: w.Service, Labels: map[string]string{}, Source: SourceSpan}
			}
		}
		rels[RelServes+"|"+wlID+"|"+svcID] = Relation{Type: RelServes, ClusterID: cid, ParentID: wlID, ChildID: svcID, Source: SourceSpan}
	}
}
