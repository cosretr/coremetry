package entity

import (
	"sort"

	"github.com/cilcenk/coremetry/internal/entity/wlkind"
)

// bridges.go — v0.10.2232 (Entity S2; plan §2.2, "S2–S6 ayarı"): kalıcı
// köprü kenarları, lider tick'inde bellekte, YENİ OKUMA YOK.
//
//   - serves (wl→svc): geçici pod ile kalıcı servis arasındaki TEK kalıcı
//     köprü. Kaynak aynı tick'in canlı görüntüsü: her pod'un baskın servisi
//     (span payı en büyük — LivePod.Services sıralı); iş yükünün kenarları =
//     pod'larından en az birinin baskın servisi olan servisler. Değişimde
//     aç/kapat (diff), günlük tazeleme. İş yükü görülüyor ama bu tick'te span
//     kanıtı yoksa (düşük trafik, pencere dışı) CH'deki açık kenar korunur:
//     susma, kenarın bittiği anlamına gelmez.
//   - same_as (eski→kanonik): CH'de açık, bu tick'te görülmeyen Derived /
//     sahipsiz ReplicaSet / pod adından türetilmiş iş yükü, aynı cluster +
//     namespace'te kanonik adı tutan YETKİLİ iş yükü görülünce. Aday birden
//     çoksa kenar yok (belirsizlikte yanlış kenar yerine kenar yok). Hedef
//     görüldükçe korunur; eski linkler resolver'da 1 adımda çözülür.

// BridgeRelations — SAF. ents: tick'in birleşik varlıkları (entByID);
// snap: aynı tick'in canlı görüntüsü; open: CH'deki açık kalıcı katman.
func BridgeRelations(cid string, ents map[string]Entity, snap LiveSnapshot, open OpenSet) []Relation {
	out := map[string]Relation{}
	add := func(typ, parent, child, source string) {
		out[relKey(typ, parent, child)] = Relation{Type: typ, ClusterID: cid, ParentID: parent, ChildID: child, Source: source}
	}
	// serves — pod başına baskın servis.
	evidence := map[string]bool{}
	for _, p := range snap.Pods {
		if p.Workload == "" || len(p.Services) == 0 || p.Services[0].Name == "" {
			continue
		}
		if ents[p.Workload].Type != TypeWorkload {
			continue
		}
		svc := ServiceID(p.Services[0].Name)
		if ents[svc].Type != TypeService {
			continue
		}
		evidence[p.Workload] = true
		add(RelServes, p.Workload, svc, SourceSpan)
	}
	// Kanıtsız ama görülen iş yükü: açık serves kenarı korunur. Hedef
	// görülmeyen same_as da korunmaz (diff kapatır).
	for _, rl := range open.Relations {
		switch rl.Type {
		case RelServes:
			if !evidence[rl.ParentID] && ents[rl.ParentID].Type == TypeWorkload {
				add(RelServes, rl.ParentID, rl.ChildID, rl.Source)
			}
		case RelSameAs:
			if _, ok := ents[rl.ChildID]; ok {
				add(RelSameAs, rl.ParentID, rl.ChildID, rl.Source)
			}
		}
	}
	// same_as — yetkili iş yükleri (ns + ad) dizini.
	authoritative := map[string][]string{}
	for id, e := range ents {
		if e.Type == TypeWorkload && !derivedWorkload(e) {
			k := e.Namespace + "/" + e.Name
			authoritative[k] = append(authoritative[k], id)
		}
	}
	for id, e := range open.Entities {
		if e.Type != TypeWorkload || !derivedWorkload(e) {
			continue
		}
		if _, still := ents[id]; still {
			continue
		}
		ref, ok := ParseID(id)
		if !ok {
			continue
		}
		_, name := wlkind.Canonical(ref.Kind, ref.Name)
		if c := authoritative[ref.Namespace+"/"+name]; len(c) == 1 && c[0] != id {
			add(RelSameAs, id, c[0], SourceThanos)
		}
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rels := make([]Relation, len(keys))
	for i, k := range keys {
		rels[i] = out[k]
	}
	return rels
}

// derivedWorkload — kimliği yetkili kaynaktan gelmeyen iş yükü: pod
// adından türetilmiş (labels.derived) ya da Derived / sahipsiz ReplicaSet.
func derivedWorkload(e Entity) bool {
	if e.Labels["derived"] != "" {
		return true
	}
	ref, ok := ParseID(e.ID)
	return ok && wlkind.Replaceable(ref.Kind, ref.Name)
}

// ClusterSameAs — K2 (S2): kaydın sahiplendiği span-only cluster değerinin
// kenarı (cluster:<değer> → cluster:<cid>).
func ClusterSameAs(cid, value string) Relation {
	return Relation{Type: RelSameAs, ClusterID: cid, ParentID: ClusterID(value), ChildID: ClusterID(cid), Source: SourceSpan}
}
