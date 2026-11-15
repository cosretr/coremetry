package api

// entities.go — v0.10.130 (K8s entity katmanı adım 6: sorgu katmanı ve
// pivot uçları; docs/plans/entity-layer-design-2026-08-28.md §4-§5).
//
// api.go BÜYÜMEYECEK kuralı: rotalar burada, api.go tek satır.
//
//	GET /api/entities/clusters                       viewer — Remote Cluster listesi (id/name) + son koşu
//	GET /api/entities?cluster=&type=&namespace=&q=&at=&limit=   viewer — sunucu-taraflı arama (picker kuralı)
//	GET /api/entity?id=&at=                          viewer — varlık + ebeveyn zinciri + çocuk sayıları + ömürler
//	GET /api/entity/services?id=&from&to             viewer — pod/node/ns/wl → servisler + sağlık (entity_seen_5m)
//	GET /api/entity/metrics?id=&from&to              viewer — pod → Thanos CPU/bellek trendi (delegasyon)
//	GET /api/services/{name}/pods?cluster=&from&to   viewer — servisi taşıyan pod'lar + sağlık
//
// Rol kapısı YOK — salt-okunur drill-down; küresel middleware kimliksiz
// isteği 401 yapar ve viewer bu veriyi GÖRMELİ. Bayrak kapalıyken uçlar
// 404 {disabled:true} — mevcut sayfalar etkilenmez. Cluster ZORUNLU
// (design §4 kuralı): cluster'sız liste/servis-pod sorgusu tüm cluster'lara
// yayılmaz; servis→pod'lar cluster'sız verilirse yanıt clusterAmbiguous ilan
// eder. Her cevap serveCached; anahtarlar entity_keys.go (tüm girdiler).

import (
	"context"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/entity"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func (s *Server) registerEntityQueryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/entities/clusters", s.getEntityClusters)
	mux.HandleFunc("GET /api/entities", s.listEntities)
	// entity_id '/' taşır ("pod:<cid>/<ns>/<pod>") — yol segmenti olamaz
	// (Go mux çok-segmentli {id}'yi yalnız sonda kabul eder); ?id= ile.
	mux.HandleFunc("GET /api/entity", s.getEntity)
	mux.HandleFunc("GET /api/entity/services", s.getEntityServices)
	mux.HandleFunc("GET /api/entity/metrics", s.getEntityMetrics)
	mux.HandleFunc("GET /api/entity/containers", s.getEntityContainers) // v0.10.135 — pod konteyner durumları (Thanos KSM)
	mux.HandleFunc("GET /api/entity/latency", s.getEntityLatency)       // v0.10.139 — node/namespace giriş-span latency özeti
	mux.HandleFunc("GET /api/services/{name}/pods", s.getServicePods)
}

// entityEnabled — v0.10.2214 (S1, K1): katman her zaman açık; yalnız
// bağlanmamış sunucu (testler) 404 {disabled:true} döner.
func (s *Server) entityEnabled(w http.ResponseWriter) bool {
	if s.entitySettings == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"disabled":true}`))
		return false
	}
	return true
}

// resolveCluster — ?cluster= id ya da ad → (cluster id, span cluster değeri).
func (s *Server) resolveCluster(ref string) (thanos.ClusterConfig, bool) {
	if s.thanos == nil || ref == "" {
		return thanos.ClusterConfig{}, false
	}
	return s.thanos.ClusterByRef(ref)
}

func parseAt(q string) time.Time {
	if q == "" {
		return time.Time{}
	}
	if n, err := strconv.ParseInt(q, 10, 64); err == nil {
		// v0.10.135 — üç birim (ns / ms / s): FE linkleri ms taşır (Date.now
		// ölçeği); ms'yi saniye sanmak 50k yıl ileri bir "an" üretirdi.
		switch {
		case n > 1e15: // ns
			return time.Unix(0, n).UTC()
		case n > 1e11: // ms
			return time.UnixMilli(n).UTC()
		}
		return time.Unix(n, 0).UTC()
	}
	if t, err := time.Parse(time.RFC3339, q); err == nil {
		return t.UTC()
	}
	return time.Time{}
}

func (s *Server) getEntityClusters(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	s.serveCached(w, r, "entities:clusters", 15*time.Second, func(ctx context.Context) (any, error) {
		out := []map[string]any{}
		if s.thanos == nil {
			return map[string]any{"clusters": out}, nil
		}
		runs, _ := s.store.EntitySyncRuns(ctx, time.Now().Add(-24*time.Hour), 500)
		last := map[string]entity.Run{}
		for _, ru := range runs { // DESC sıralı: ilk görülen en yeni
			if _, ok := last[ru.ClusterID]; !ok {
				last[ru.ClusterID] = ru
			}
		}
		cfg := s.thanos.CurrentSettings()
		for _, c := range cfg.Clusters {
			if !c.Enabled {
				continue
			}
			m := map[string]any{"id": c.EffectiveID(), "name": c.Name, "spanClusterValue": c.SpanClusterKey(), "spanClusterValues": c.SpanClusterKeys()}
			if ru, ok := last[c.EffectiveID()]; ok {
				m["lastRun"] = ru
			}
			out = append(out, m)
		}
		if ru, ok := last[entity.UnmappedClusterID]; ok {
			return map[string]any{"clusters": out, "unmapped": ru}, nil
		}
		return map[string]any{"clusters": out}, nil
	})
}

func (s *Server) listEntities(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	q := r.URL.Query()
	c, ok := s.resolveCluster(strings.TrimSpace(q.Get("cluster")))
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "cluster parametresi zorunlu (Remote Cluster id ya da adı)")
		return
	}
	typ, ns, search := strings.TrimSpace(q.Get("type")), strings.TrimSpace(q.Get("namespace")), strings.TrimSpace(q.Get("q"))
	limit := parseInt(q.Get("limit"), 100)
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	at := parseAt(q.Get("at"))
	key := entityListKey(c.EffectiveID(), typ, ns, search, limit, at)
	s.serveCached(w, r, key, 15*time.Second, func(ctx context.Context) (any, error) {
		cid := c.EffectiveID()
		// v0.10.2225 (E1): pod/container CH satırı değil — pod'lar resolver'dan
		// (canlı dizin, yoksa MV); container durumu /api/entity/containers.
		switch typ {
		case entity.TypePod:
			pods, err := s.entityResolver().Pods(ctx, cid, entity.PodQuery{Namespace: ns, Search: search, Limit: limit}, at)
			if err != nil && len(pods) == 0 {
				return nil, err
			}
			return map[string]any{"cluster": cid, "items": chstore.PodRecords(pods)}, nil
		case entity.TypeContainer:
			return map[string]any{"cluster": cid, "items": []chstore.EntityRecord{}}, nil
		}
		rows, err := s.store.EntityList(ctx, chstore.EntityListQuery{ClusterID: cid, Type: typ, Namespace: ns, Search: search, At: at, Limit: limit})
		if err != nil {
			return nil, err
		}
		if typ == "" && len(rows) < limit {
			if pods, _ := s.entityResolver().Pods(ctx, cid, entity.PodQuery{Namespace: ns, Search: search, Limit: limit - len(rows)}, at); len(pods) > 0 {
				rows = append(rows, chstore.PodRecords(pods)...)
			}
		}
		return map[string]any{"cluster": cid, "items": rows}, nil
	})
}

func (s *Server) getEntity(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "geçersiz entity id")
		return
	}
	at := parseAt(r.URL.Query().Get("at"))
	key := entityPivotKey("get", id+"@"+atBucket(at), time.Time{}, time.Time{})
	s.serveCached(w, r, key, 15*time.Second, func(ctx context.Context) (any, error) {
		switch ref.Type {
		case entity.TypePod:
			return s.podEntityDetail(ctx, id, ref, at)
		case entity.TypeContainer:
			return nil, errNotFound // v0.10.2225: container satırı yok
		}
		cur, match, all, err := s.store.EntityLifetimesAt(ctx, id, at)
		if err != nil {
			return nil, err
		}
		// v0.10.2232 (S2) — birebir kayıt yoksa kanonik kimlik (sahipsiz
		// ReplicaSet → Deployment) ve same_as (Derived → yetkili iş yükü,
		// span-only cluster → kayıt), en çok 1 adım. Ad araması yok.
		reqID := id
		id := id
		if cur == nil {
			if alt := s.entityResolver().Canonical(ctx, id, at); alt != id {
				if c2, m2, a2, err2 := s.store.EntityLifetimesAt(ctx, alt, at); err2 == nil && c2 != nil {
					cur, match, all, id = c2, m2, a2, alt
				}
			}
		}
		if cur == nil {
			return nil, errNotFound
		}
		// v0.10.135 — ölü / o-an-geçersiz kayıt 404 DEĞİL: en yeni ömür +
		// atMatch=false döner; sayfa "artık mevcut değil, son görülme X" +
		// tarihçe gösterir. Zincir ve çocuklar kaydın KENDİ zamanında çözülür
		// (ölü pod'un konteynerleri bugün değil, son görüldüğü anda geçerliydi).
		eff := at
		if eff.IsZero() && cur.ValidTo != nil {
			eff = cur.LastSeen
		}
		parents := s.store.EntityParents(ctx, id, eff)
		children, _ := s.store.EntityChildrenCounts(ctx, ref.ClusterID, id, eff)
		if children == nil {
			children = map[string]int{}
		}
		// v0.10.2225 (E1): S1'den kalan pod/container satırları sayılmaz;
		// doğrudan çocuk pod'lar resolver'dan.
		delete(children, entity.TypePod)
		delete(children, entity.TypeContainer)
		if n := s.podChildren(ctx, ref, id, eff); n > 0 {
			children[entity.TypePod] = n
		}
		out := map[string]any{"entity": cur, "parents": parents, "children": children, "lifetimes": all, "atMatch": match}
		if id != reqID {
			out["resolvedFrom"] = reqID // v0.10.1420 — FE: istenen kimlik yedekle çözüldü
		}
		if c, ok := s.thanos.ClusterByID(ref.ClusterID); ok {
			out["cluster"] = map[string]any{"id": c.EffectiveID(), "name": c.Name}
		}
		return out, nil
	})
}

// podEntityDetail — v0.10.2225 (C + R1b): pod'un CH satırı yok. Kayıt
// resolver'dan (şimdi → canlı dizin, ıska/geçmiş an → entity_seen_5m);
// ebeveyn zinciri kalıcı katmandan; node ve kardeşler aynı kaynaktan. Ömür
// = görülme penceresi (tek kayıt).
func (s *Server) podEntityDetail(ctx context.Context, id string, ref entity.Ref, at time.Time) (any, error) {
	res := s.entityResolver()
	got, err := res.PodsByID(ctx, []string{id}, at)
	p, ok := got[id]
	if !ok {
		if err != nil {
			return nil, err
		}
		return nil, errNotFound
	}
	rec := chstore.PodRecord(p)
	parents := []chstore.EntityRecord{}
	parentID := p.ParentID()
	cur, _, perr := s.store.EntityLifetimes(ctx, parentID, at)
	if (perr != nil || cur == nil) && p.Workload != "" {
		if alt := s.entityResolver().Canonical(ctx, p.Workload, at); alt != p.Workload {
			parentID = alt
			cur, _, _ = s.store.EntityLifetimes(ctx, alt, at)
		}
	}
	if cur != nil {
		parents = append(parents, *cur)
		parents = append(parents, s.store.EntityParents(ctx, parentID, at)...)
	}
	out := map[string]any{"entity": rec, "parents": parents, "children": map[string]int{}, "lifetimes": []chstore.EntityRecord{rec}, "atMatch": true, "live": p.Live}
	if p.Node != "" {
		out["node"] = entity.NodeID(ref.ClusterID, p.Node)
	}
	if p.Workload != "" {
		if sib, err := res.Pods(ctx, ref.ClusterID, entity.PodQuery{Namespace: p.Namespace, Workload: p.Workload}, at); err == nil || len(sib) > 0 {
			keep := make([]entity.ResolvedPod, 0, len(sib))
			for _, x := range sib {
				if x.ID != id && len(keep) < 50 {
					keep = append(keep, x)
				}
			}
			out["siblings"] = chstore.PodRecords(keep)
		}
	}
	if c, ok := s.thanos.ClusterByID(ref.ClusterID); ok {
		out["cluster"] = map[string]any{"id": c.EffectiveID(), "name": c.Name}
	}
	return out, nil
}

// podChildren — v0.10.2225: ebeveyni id olan pod sayısı (iş yükünün
// pod'ları; namespace'in iş yüksüz pod'ları) resolver'dan.
func (s *Server) podChildren(ctx context.Context, ref entity.Ref, id string, at time.Time) int {
	var q entity.PodQuery
	switch ref.Type {
	case entity.TypeWorkload:
		q = entity.PodQuery{Namespace: ref.Namespace, Workload: id}
	case entity.TypeNamespace:
		q = entity.PodQuery{Namespace: ref.Name}
	default:
		return 0
	}
	pods, _ := s.entityResolver().Pods(ctx, ref.ClusterID, q, at)
	n := 0
	for _, p := range pods {
		if ref.Type == entity.TypeWorkload || p.Workload == "" {
			n++
		}
	}
	return n
}

// getEntityServices — pod → servisler; node/namespace/workload → altındaki
// pod'lar → servisler; hepsi entity_seen_5m sağlığıyla.
func (s *Server) getEntityServices(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "geçersiz entity id")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	c, ok := s.thanos.ClusterByID(ref.ClusterID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "cluster not configured")
		return
	}
	key := entityPivotKey("services", id, from, to)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		// v0.10.2225 (C + R1b): pod'lar CH satırı değil — resolver (canlı
		// dizin, ıska/geçmiş → entity_seen_5m). 500 tavanı süzgeç İÇİ.
		// İnceleme (v0.10.135): `ns:` id'de ParseID namespace'i Name'e koyar.
		res := s.entityResolver()
		var pods []chstore.EntityRecord
		var q entity.PodQuery
		switch ref.Type {
		case entity.TypePod:
			got, _ := res.PodsByID(ctx, []string{id}, to)
			if p, ok := got[id]; ok {
				pods = []chstore.EntityRecord{chstore.PodRecord(p)}
			} else {
				pods = []chstore.EntityRecord{{Type: entity.TypePod, ClusterID: ref.ClusterID, ID: id, Namespace: ref.Namespace, Name: ref.Name}}
			}
		case entity.TypeNode:
			q = entity.PodQuery{Node: ref.Name, Limit: 500}
		case entity.TypeNamespace:
			q = entity.PodQuery{Namespace: ref.Name, Limit: 500}
		case entity.TypeWorkload:
			q = entity.PodQuery{Namespace: ref.Namespace, Workload: id, Limit: 500}
		default:
			return nil, errBadRequest
		}
		if ref.Type != entity.TypePod {
			list, err := res.Pods(ctx, ref.ClusterID, q, to)
			if err != nil && len(list) == 0 {
				return nil, err
			}
			pods = chstore.PodRecords(list)
		}
		if len(pods) == 0 {
			return map[string]any{"entity": id, "pods": []any{}, "services": []any{}}, nil
		}
		// Sağlık: pod'ların (ns, pod) çiftleri → entity_seen_5m.
		byNS := map[string][]string{}
		for _, p := range pods {
			byNS[p.Namespace] = append(byNS[p.Namespace], p.Name)
		}
		var aggs []chstore.EntitySeenAgg
		for ns, names := range byNS {
			a, err := s.store.EntitySeenForPods(ctx, c.SpanClusterKeys(), ns, names, from, to)
			if err != nil {
				return nil, err
			}
			aggs = append(aggs, a...)
		}
		type svcRow struct {
			Service string  `json:"service"`
			Pods    int     `json:"pods"`
			Spans   int64   `json:"spans"`
			Errors  int64   `json:"errors"`
			AvgMs   float64 `json:"avgMs"`
		}
		svc := map[string]*svcRow{}
		podSet := map[string]map[string]bool{}
		for _, a := range aggs {
			row, ok := svc[a.Service]
			if !ok {
				row = &svcRow{Service: a.Service}
				svc[a.Service] = row
				podSet[a.Service] = map[string]bool{}
			}
			// v0.10.190 (inceleme #3) — yalnız pod ADI: namespace'siz ('') ve
			// namespace'li satır aynı pod'dur (cluster bu çağrıda sabit); ikiye
			// sayılmasın.
			if !podSet[a.Service][a.Pod] {
				podSet[a.Service][a.Pod] = true
				row.Pods++
			}
			row.AvgMs = (row.AvgMs*float64(row.Spans) + a.AvgMs*float64(a.Spans)) / float64(max64(row.Spans+a.Spans, 1))
			row.Spans += a.Spans
			row.Errors += a.Errors
		}
		services := make([]svcRow, 0, len(svc))
		for _, v := range svc {
			services = append(services, *v)
		}
		sort.Slice(services, func(i, j int) bool { return services[i].Spans > services[j].Spans })
		// v0.10.190 — namespace'siz MV satırları (collector k8s.namespace.name
		// basmıyor) pod adıyla eşlendi; sayısı ilan edilir, sessiz varsayım yok.
		nsMissing := 0
		for _, a := range aggs {
			if a.Namespace == "" {
				nsMissing++
			}
		}
		resp := map[string]any{"entity": id, "cluster": c.EffectiveID(), "pods": pods, "services": services, "rows": aggs}
		if nsMissing > 0 {
			resp["nsMissingRows"] = nsMissing
		}
		return resp, nil
	})
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// getEntityMetrics — pod → Thanos CPU/bellek trendi (cluster matcher'lı).
func (s *Server) getEntityMetrics(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	if !ok || ref.Type != entity.TypePod {
		writeJSONError(w, http.StatusBadRequest, "yalnız pod varlıkları için")
		return
	}
	c, ok := s.thanos.ClusterByID(ref.ClusterID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "cluster not configured")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	from, to, _ = clampThanosWindow(from, to)
	key := entityPivotKey("metrics", id, from, to)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		pts, err := s.thanos.PodTrend(qctx, c, ref.Namespace, ref.Name, from, to)
		if err != nil {
			return nil, err
		}
		if pts == nil {
			pts = []thanos.TrendPoint{}
		}
		return map[string]any{"entity": id, "cluster": c.EffectiveID(), "points": pts}, nil
	})
}

// getServicePods — servisi taşıyan pod'lar + sağlık. cluster verilmezse
// tüm cluster'lar okunur ve yanıt clusterAmbiguous ile ilan eder.
func (s *Server) getServicePods(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, "service name required")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	clusterRef := strings.TrimSpace(r.URL.Query().Get("cluster"))
	var clusterValues []string
	var clusterID string
	if clusterRef != "" {
		c, ok := s.resolveCluster(clusterRef)
		if !ok {
			writeJSONError(w, http.StatusNotFound, "cluster not configured")
			return
		}
		clusterValues, clusterID = c.SpanClusterKeys(), c.EffectiveID() // v0.10.139 — çoklu değer
	}
	key := servicePodsKey(name, clusterID, from, to)
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		aggs, err := s.store.EntitySeenForService(ctx, name, clusterValues, from, to)
		if err != nil {
			return nil, err
		}
		// span cluster değeri → Remote Cluster id (eşlenemeyen ilan edilir)
		byValue := map[string]thanos.ClusterConfig{}
		for _, c := range s.thanos.CurrentSettings().Clusters {
			if c.Enabled {
				for _, k := range c.SpanClusterKeys() { // v0.10.139 — bir kayıt birden çok değer
					byValue[k] = c
				}
			}
		}
		out := make([]servicePodRow, 0, len(aggs))
		clusters := map[string]bool{}
		unmapped := map[string]bool{}
		for _, a := range aggs {
			row := servicePodRow{EntitySeenAgg: a}
			if c, ok := byValue[a.Cluster]; ok {
				row.ClusterID = c.EffectiveID()
				clusters[c.EffectiveID()] = true
			} else {
				unmapped[a.Cluster] = true
			}
			out = append(out, row)
		}
		// v0.10.190 — entity id + kayıt ÇÖZÜMÜ Thanos durumundan SONRA
		// (aşağıda): namespace'siz satırlarda id `pod:<cid>//<pod>` bozuk
		// doğuyor, kayıt bulunamıyor, WORKLOAD boş kalıyordu.
		// v0.10.136 — adım 2 zenginleştirme. (a) Latency: ham spans, giriş-span,
		// pod başına p50/p95/p99 (entity_seen_5m yalnız ortalama taşır); hata
		// best-effort — 0011 kolonu olmayan CH'de yüzdelikler boş kalır, tablo
		// yaşar. (b) Durum: cluster başına TEK Thanos envanter sorgusu, hedefli
		// pod=~ regex (≤200 ad; fazlası statusNotes ile ilan). (c) Zincir:
		// satırların entity ebeveynlerinden workload/namespace özeti.
		// v0.10.139 (inceleme) — çoklu değerli kayıtta aynı pod iki ham cluster
		// değeriyle iki satır veriyordu (yinelenen pod, şişen workload sayısı):
		// (clusterId, namespace, pod) üzerinden birleştir.
		out = mergeServicePodRows(out)
		statusNotes := []string{}
		var notesMu sync.Mutex
		note := func(n string) { notesMu.Lock(); statusNotes = append(statusNotes, n); notesMu.Unlock() }
		if len(out) > 0 {
			if lat, err := s.store.PodLatencyForService(ctx, name, clusterValues, from, to); err == nil {
				byKey := make(map[[3]string]chstore.PodLatencyRow, len(lat))
				for _, l := range lat {
					byKey[[3]string{l.Cluster, l.Namespace, l.Pod}] = l
				}
				for i := range out {
					if l, ok := byKey[[3]string{out[i].Cluster, out[i].Namespace, out[i].Pod}]; ok {
						out[i].EntrySpans, out[i].P50Ms, out[i].P95Ms, out[i].P99Ms = l.EntrySpans, l.P50Ms, l.P95Ms, l.P99Ms
					}
				}
				if len(lat) >= chstore.PodLatencyLimit {
					note("latency yalnız en yoğun " + strconv.Itoa(chstore.PodLatencyLimit) + " pod için")
				}
			} else {
				log.Printf("[entities] pod latency for %s: %v", name, err)
				note("latency alınamadı (sunucu günlüğü)")
			}
		}
		byCluster := map[string][]int{}
		for i := range out {
			if out[i].ClusterID != "" {
				byCluster[out[i].ClusterID] = append(byCluster[out[i].ClusterID], i)
			}
		}
		// Cluster'lar PARALEL, tek 10 s bütçe (inceleme: ardışık 10 s × N cluster
		// serveCached 30 s TTL'ini aşıyordu). Her goroutine yalnız kendi
		// cluster'ının satır indekslerine yazar — örtüşme yok.
		sctx, scancel := context.WithTimeout(ctx, 10*time.Second)
		var wg sync.WaitGroup
		for cid, idxs := range byCluster {
			c, ok := s.thanos.ClusterByID(cid)
			if !ok {
				continue
			}
			names := make([]string, 0, len(idxs))
			for _, i := range idxs {
				names = append(names, out[i].Pod)
			}
			re, truncated := thanos.PodNamesRegex(names)
			if truncated {
				note(c.Name + ": durum yalnız ilk pod'lar için (seçici tavanı)")
			}
			if re == "" {
				continue
			}
			wg.Add(1)
			go func(c thanos.ClusterConfig, idxs []int, re string) {
				defer wg.Done()
				prows, _, err := s.thanos.PodMetrics(sctx, c, re)
				if err != nil {
					note(c.Name + ": durum alınamadı")
					return
				}
				idx := indexThanosPods(prows)
				filled, ambiguous := 0, 0
				for _, i := range idxs {
					// v0.10.190 (operatör-bildirimi, prod: Pods tablosunda NAMESPACE
					// boş, STATUS/CPU «—»): span'i namespace taşımayan cluster'da
					// anahtar "/pod" oluyor ve Thanos satırı hiç eşleşmiyordu.
					// Saf eşleyici: matchThanosPod (tablo-testli).
					p, res := matchThanosPod(idx, out[i].Namespace, out[i].Pod)
					switch res {
					case thanosMatchFilled:
						out[i].Namespace, out[i].NamespaceFromThanos = p.Namespace, true
						filled++
					case thanosMatchAmbiguous:
						ambiguous++
					}
					if res == thanosMatchExact || res == thanosMatchFilled {
						out[i].Phase, out[i].Restarts, out[i].RestartsUnknown, out[i].LastTermReason = p.Phase, p.Restarts, p.RestartsUnknown, p.LastTermReason
						out[i].CPUCores, out[i].MemBytes, out[i].StatusKnown = p.CPUCores, p.MemBytes, true
					}
				}
				if filled > 0 {
					note(c.Name + ": " + strconv.Itoa(filled) + " pod'un namespace'i span'de yok (collector k8s.namespace.name basmıyor) — Thanos'tan tamamlandı")
				}
				if ambiguous > 0 {
					note(c.Name + ": " + strconv.Itoa(ambiguous) + " pod adı birden çok namespace'te — namespace'siz span satırı eşlenmedi")
				}
			}(c, idxs, re)
		}
		wg.Wait()
		scancel()
		// v0.10.190 (inceleme #2) — Thanos'tan tamamlanan namespace, span'den
		// namespace'li gelen satırla ÇAKIŞABİLİR (collector düzeltildikten sonra
		// pencerede hem '' hem 'ns' kovası; karışık collector'lar): ikinci
		// birleştirme — aynı pod tek satır, durum alanları taşınır.
		out = mergeServicePodRows(out)
		// v0.10.190 — entity kimliği/kaydı: namespace artık (Thanos'tan) dolu
		// olabilir; boş kalanlar için kimlik üretilmez (bozuk `pod:cid//pod`
		// yerine link yok). Kayıt okuması ardışık — satır sayısı ≤ 500.
		nsUnknown := 0
		podIDs := make([]string, 0, len(out))
		for i := range out {
			if out[i].ClusterID == "" {
				continue
			}
			if out[i].Namespace == "" {
				nsUnknown++
				continue
			}
			out[i].EntityID = entity.PodID(out[i].ClusterID, out[i].Namespace, out[i].Pod)
			podIDs = append(podIDs, out[i].EntityID)
		}
		// v0.10.2225: kayıt resolver'dan, tek toplu okuma (eskiden pod başına
		// bir CH sorgusu).
		recs, _ := s.entityResolver().PodsByID(ctx, podIDs, to)
		for i := range out {
			if p, ok := recs[out[i].EntityID]; ok && out[i].EntityID != "" {
				rec := chstore.PodRecord(p)
				out[i].Entity = &rec
			}
		}
		if nsUnknown > 0 {
			note(strconv.Itoa(nsUnknown) + " pod'un namespace'i bilinmiyor (span'de yok, Thanos'ta bulunamadı) — entity/workload linki yok")
		}
		type chainItem struct {
			ID        string `json:"id"`
			Type      string `json:"type"`
			Name      string `json:"name"`
			Kind      string `json:"kind,omitempty"`
			Namespace string `json:"namespace,omitempty"`
			ClusterID string `json:"clusterId"`
			Pods      int    `json:"pods"`
		}
		chainIdx := map[string]int{}
		chain := []chainItem{}
		for _, row := range out {
			if row.Entity == nil || row.Entity.ParentID == "" {
				continue
			}
			pid := row.Entity.ParentID
			if j, ok := chainIdx[pid]; ok {
				chain[j].Pods++
				continue
			}
			pr, ok := entity.ParseID(pid)
			if !ok {
				continue
			}
			ci := chainItem{ID: pid, Type: pr.Type, Name: pr.Name, Kind: pr.Kind, Namespace: pr.Namespace, ClusterID: pr.ClusterID, Pods: 1}
			if pr.Type == entity.TypeNamespace {
				ci.Namespace = pr.Name
			}
			chainIdx[pid] = len(chain)
			chain = append(chain, ci)
		}
		sort.SliceStable(chain, func(a, b int) bool { return chain[a].Pods > chain[b].Pods })
		resp := map[string]any{"service": name, "pods": out, "chain": chain}
		if len(statusNotes) > 0 {
			sort.Strings(statusNotes)
			resp["statusNotes"] = statusNotes
		}
		if clusterID == "" && len(clusters) > 1 {
			ids := make([]string, 0, len(clusters))
			for id := range clusters {
				ids = append(ids, id)
			}
			sort.Strings(ids)
			resp["clusterAmbiguous"] = ids
		}
		if len(unmapped) > 0 {
			vals := make([]string, 0, len(unmapped))
			for v := range unmapped {
				vals = append(vals, v)
			}
			sort.Strings(vals)
			resp["unmappedClusters"] = vals
		}
		return resp, nil
	})
}

// getEntityContainers — v0.10.135 (DETAY SAYFALARI adım 1). Pod'un konteyner
// durumları Thanos/KSM'den anlık (ready / restart / bekleme sebebi / son
// sonlanma sebebi). Thanos hatası 5xx DEĞİL: 200 + error alanı — panel
// "bilinmiyor" der, sayfanın geri kalanı yaşar (vmetrics probe duruşu).
func (s *Server) getEntityContainers(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	if !ok || ref.Type != entity.TypePod {
		writeJSONError(w, http.StatusBadRequest, "pod entity id gerekli")
		return
	}
	c, ok := s.thanos.ClusterByID(ref.ClusterID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "cluster kaydı yok")
		return
	}
	key := entityPivotKey("containers", id, time.Time{}, time.Time{})
	s.serveCached(w, r, key, 30*time.Second, func(ctx context.Context) (any, error) {
		// Dört ardışık Thanos sorgusu; getEntityMetrics gibi 10 s üst sınır —
		// takılan Querier isteği 60 s'e kadar asmasın (inceleme, v0.10.135).
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		rows, err := s.thanos.PodContainers(qctx, c, ref.Namespace, ref.Name)
		if err != nil {
			return map[string]any{"entity": id, "containers": []any{}, "error": err.Error()}, nil
		}
		if rows == nil {
			rows = []thanos.ContainerStatus{}
		}
		return map[string]any{"entity": id, "containers": rows}, nil
	})
}

// mergeSeenAgg — v0.10.139 (inceleme): aynı pod'un iki ham cluster değerli
// satırı (çoklu değerli Remote Cluster kaydı) tek satıra: sayımlar toplanır,
// ortalama ağırlıklı, ilk/son görülme uç değerler, cluster BİRİNCİL değer
// (ilk satır). Saf; tablo-testli.
func mergeSeenAgg(a, b chstore.EntitySeenAgg) chstore.EntitySeenAgg {
	total := a.Spans + b.Spans
	if total > 0 {
		a.AvgMs = (a.AvgMs*float64(a.Spans) + b.AvgMs*float64(b.Spans)) / float64(total)
	}
	a.Spans, a.Errors = total, a.Errors+b.Errors
	if a.Node == "" {
		a.Node = b.Node
	}
	if b.FirstSeen.Before(a.FirstSeen) {
		a.FirstSeen = b.FirstSeen
	}
	if b.LastSeen.After(a.LastSeen) {
		a.LastSeen = b.LastSeen
	}
	return a
}

// getEntityLatency — v0.10.139 (DETAY SAYFALARI adım 5). Node / namespace
// giriş-span latency özeti (p50/p95/p99 + hata): entity_seen_5m yüzdelik
// taşımaz; ham spans terfi kolonlu (set index), zaman + cluster sınırlı.
// serveCached 60 s; anahtar id + pencere (entityPivotKey).
func (s *Server) getEntityLatency(w http.ResponseWriter, r *http.Request) {
	if !s.entityEnabled(w) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	ref, ok := entity.ParseID(id)
	var dim string
	switch {
	case ok && ref.Type == entity.TypeNode:
		dim = "k8s_node"
	case ok && ref.Type == entity.TypeNamespace:
		dim = "k8s_namespace"
	default:
		writeJSONError(w, http.StatusBadRequest, "node ya da namespace entity id gerekli")
		return
	}
	c, ok := s.thanos.ClusterByID(ref.ClusterID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "cluster kaydı yok")
		return
	}
	from, to := parseFromTo(r, time.Hour)
	// Ham spans, servis öneki YOK (PK budaması yalnız zaman): 7g/30g presetleri
	// tüm pencereyi tarardı → 24 saate kelepçe + ilan (inceleme).
	const maxLatencyWindow = 24 * time.Hour
	clamped := false
	if to.Sub(from) > maxLatencyWindow {
		from, clamped = to.Add(-maxLatencyWindow), true
	}
	key := entityPivotKey("latency", id, from, to)
	s.serveCached(w, r, key, 60*time.Second, func(ctx context.Context) (any, error) {
		lat, err := s.store.EntityLatencyFor(ctx, dim, ref.Name, c.SpanClusterKeys(), from, to) // tüm değerler (v0.10.139)
		if err != nil {
			return nil, err
		}
		return map[string]any{"entity": id, "cluster": c.EffectiveID(), "latency": lat, "clamped": clamped, "from": from.UnixNano(), "to": to.UnixNano()}, nil
	})
}
