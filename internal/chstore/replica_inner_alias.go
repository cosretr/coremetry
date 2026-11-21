package chstore

import (
	"fmt"
	"sort"
	"strings"
)

// replica_inner_alias.go — v0.10.2434. Operatör-bildirimli (prod, 2×2 küme):
// `trace_id_ts` MV'si dört host'ta da eksikti; MV onarımı kartının "Yeniden
// kur"u host başına koştu ve her host kendi uuid'siyle bir `.inner_id.<uuid>`
// iç tablo aldı. ZK yolu shard başına ORTAK (…/tables/01/trace_id_ts), 2/2
// replika — replikasyon sağlam. Ama Replika tutarlılığı kartı tabloları ADLA
// grupluyordu: dört iç tablonun her biri "eksik replika" (eş: "tablo yok")
// çıktı ve ATTACH/EXCHANGE runbook'u önerildi — yanlış pozitif.
//
// Düzeltme: aynı mantıksal MV'nin iç tabloları (aynı sahip view adı YA DA
// aynı zookeeper_path) tek satırda birleşir; shard başına karar sonra
// replicaVerdict'te ZK yoluyla verilir (yol farklıysa no_replication, aynı
// yol + kayıtlı replikalar = tutarlı). Eksik replika yalnız shard'ın hiçbir
// host'unda bu MV'nin iç tablosu yokken çıkar.
//
// Yalnız iç tablolar: düz tabloların adı replikalar arasında kurgu gereği
// aynıdır; aynı ZK yolundaki FARKLI adlı düz tablo (`_fix`, `_old`) bilinçli
// bir onarım ara hâlidir ve ayrı satırda görünmelidir.

// mergeInnerAliases — SAF. byTable/engineOf/owners YERİNDE güncellenir;
// dönüş: kanonik ad → birleşen öteki adlar (sıralı). Kanonik ad bileşenin
// alfabetik ilk adıdır.
//
// Güvenlik kapısı: birleşen bileşende bir host İKİ ad taşıyorsa (aynı host'ta
// sahipsiz eski iç tablo + yenisi) birleşme YAPILMAZ — tek host'u iki replika
// gibi saymak gerçek bir sorunu "tutarlı" gösterebilirdi.
func mergeInnerAliases(byTable map[string][]ReplicaState, engineOf map[string]map[string]string, owners map[string]innerOwner) map[string][]string {
	hostsOf := map[string]map[string]bool{}
	add := func(name, host string) {
		if !isInnerTable(name) {
			return
		}
		if hostsOf[name] == nil {
			hostsOf[name] = map[string]bool{}
		}
		hostsOf[name][host] = true
	}
	for t, rs := range byTable {
		for _, r := range rs {
			add(t, r.Host)
		}
	}
	for t, hs := range engineOf {
		for h := range hs {
			add(t, h)
		}
	}
	names := make([]string, 0, len(hostsOf))
	for n := range hostsOf {
		names = append(names, n)
	}
	sort.Strings(names)

	parent := map[string]string{}
	var find func(string) string
	find = func(x string) string {
		if p, ok := parent[x]; ok && p != x {
			r := find(p)
			parent[x] = r
			return r
		}
		return x
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra == rb {
			return
		}
		if rb < ra {
			ra, rb = rb, ra
		}
		parent[rb] = ra
	}
	byView, byPath := map[string]string{}, map[string]string{}
	for _, n := range names {
		parent[n] = n
		if v := owners[n].View; v != "" {
			if first, ok := byView[v]; ok {
				union(first, n)
			} else {
				byView[v] = n
			}
		}
		for _, r := range byTable[n] {
			if r.ZKPath == "" {
				continue
			}
			if first, ok := byPath[r.ZKPath]; ok {
				union(first, n)
			} else {
				byPath[r.ZKPath] = n
			}
		}
	}

	groups := map[string][]string{}
	for _, n := range names {
		root := find(n)
		groups[root] = append(groups[root], n)
	}
	out := map[string][]string{}
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		seen, clash := map[string]bool{}, false
		for _, m := range members {
			for h := range hostsOf[m] {
				if seen[h] {
					clash = true
				}
				seen[h] = true
			}
		}
		if clash {
			continue
		}
		sort.Strings(members)
		canon := members[0] // kök: union küçük adı tutar
		o := owners[canon]
		for _, m := range members[1:] {
			byTable[canon] = append(byTable[canon], byTable[m]...)
			delete(byTable, m)
			if len(engineOf[m]) > 0 {
				if engineOf[canon] == nil {
					engineOf[canon] = map[string]string{}
				}
				for h, e := range engineOf[m] {
					engineOf[canon][h] = e
				}
			}
			delete(engineOf, m)
			if mo, ok := owners[m]; ok {
				if o.View == "" || (mo.View != "" && mo.View < o.View) {
					o.View = mo.View
				}
				o.Hosts = append(o.Hosts, mo.Hosts...)
				delete(owners, m)
			}
			out[canon] = append(out[canon], m)
		}
		if o.View != "" || len(o.Hosts) > 0 {
			sort.Strings(o.Hosts)
			owners[canon] = o
		}
	}
	return out
}

// innerAliasNotes — kart notu: birleşen adlar görünür kalsın (operatör
// system.tables'ta farklı adlar görecek).
func innerAliasNotes(aliases map[string][]string, owners map[string]innerOwner) []string {
	canons := make([]string, 0, len(aliases))
	for c := range aliases {
		canons = append(canons, c)
	}
	sort.Strings(canons)
	out := make([]string, 0, len(canons))
	for _, c := range canons {
		label := owners[c].View
		if label == "" {
			label = c
		}
		out = append(out, fmt.Sprintf("%s: iç tablo adları host'lar arasında farklı (%s, %s) — host başına kurulumda her host kendi uuid'sini aldı. Replikalar sahip view / ZooKeeper yoluyla eşlendi; adlar bilgi amaçlı, replikasyonu etkilemez.", label, c, strings.Join(aliases[c], ", ")))
	}
	return out
}
