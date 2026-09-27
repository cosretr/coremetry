package argocd

// put.go — v0.10.974 — Argo CD ayar sekmesi onayının (operatör, 2026-09-27,
// "Onay rollout için önerin"; docs/DECISIONS.md "Argo CD ayar sekmesi: dört
// arka uç kuralı") PUT kuralları. SAF: ağ yok, depo yok; api katmanı
// (putArgoCDSettings) kayıtlı blobu (svc.Current, PUT'tan hemen önce
// LoadPersisted ile tazelenmiş) ve Remote Cluster yüzünü verir.
//
// ApplyPut sırası (her adım ilk hatada 400 {error, field} döner):
//
//	(a) BE4 ön denetim  — kayıtlı bir hub hubs'tan çıkarılırken ona bağlı
//	    instance gövdede duruyorsa instances[i].hubClusterId (Validate'ten
//	    ÖNCE: aksi hâlde genel "hubs listesinde değil" metni kazanırdı).
//	(b) Validate        — aynen; instance sırasını KORUR, dizinler geçerli kalır.
//	(c) clearTokenRef   — bayrak + dolu tokenRef birlikte → instances[i].clearTokenRef.
//	(d) BE3             — kayıtlı kimlik değiştirilemez (aşağıda).
//	(e) BE1             — boş tokenRef kayıtlıyı korur (aşağıda).
//
// ── BE1: boş tokenRef kayıtlı referansı korur ─────────────────────────────
//
// CLAUDE.md "empty input preserves stored value": FE ref girdisini hep BOŞ
// açar (değeri önceden doldurmaz), dokunulmamış satır `tokenRef: ""` gönderir.
// Instance başına (id kırpılmış): dolu ref → değiştir (secretref.Valid);
// boş ref + bayrak yok + id kayıtlı → kayıtlı ref kopyalanır; boş ref +
// `clearTokenRef: true` → kaldırılır; id kayıtlı değil → ref yok, bayrak
// etkisiz. `clearTokenRef` İSTEK-YALNIZ: Instance alanı DEĞİL (JSON adları
// sözleşmesi TestSettingsJSONFieldNames değişmez), kalıcı blobda, GET/PUT
// cevabında ve audit'te hiç görünmez. Anahtar id — BE3 id'nin sessizce başka
// bir instance'a geçmesini engellediği için güvenli.
//
// ── BE3: kayıtlı instance kimliği salt okunur (RED, sil+ekle değil) ────────
//
// Faz 3'te id ClickHouse `instance_id`'dir; bir yuvanın (hubClusterId,
// hubNamespace — hub'da namespace başına tek Argo CD) kimliğini sessizce
// değiştirmek geçmişi yetim bırakırdı. Seçilen güvenli yol: gövdede kayıtlı
// OLMAYAN bir id, kayıtlı bir yuvayı alıyor VE o yuvanın kayıtlı id'si gövdede
// YOKSA bu bir yeniden adlandırmadır → 400 instances[i].id (eski id, yeni id,
// hub/ns ve çare: önce eski satırı kaldırıp kaydet, sonra yeni kimlikle ekle).
// Açık "sil+ekle" bayrağı YOK — iki ayrı kayıt yeterince açıktır. İzinli
// kalanlar: kayıtlı id'yi başka hub'a taşımak, id silmek, boş yuvaya yeni id,
// taşınan (gövdede duran) bir id'nin boşalttığı yuvaya yeni id, iki kayıtta
// sil ve ekle. Namespace'i de değişen "yeniden adlandırma" sil+ekle'den ayırt
// edilemez ve izinlidir; pinleri pins[i].instanceId denetimi korur.
//
// ── BE4: bağlı instance'ı olan hub kaldırılamaz ───────────────────────────
//
// Yol bugünkü instances[i].hubClusterId (mockup'ın kayıt hatası listesi ve
// settings_test.go pini); mesaj hub'ı (küme adı + id), bağlı instance
// sayısını ve çareyi (taşı ya da hub'ı bırak) söyler.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cilcenk/coremetry/internal/secretref"
)

// PutOptions — v0.10.974 — PUT gövdesinin kalıcı OLMAYAN, istek-yalnız
// bayrakları. ClearTokenRef GÖNDERİLEN instances[] dizinine göre.
type PutOptions struct {
	ClearTokenRef map[int]bool
}

// ParsePut — v0.10.974 — ParseInput (düz token + eski tek-hub anahtarı reddi
// korunur) + instances[i].clearTokenRef. Bayrak varsa JSON bool olmalı (null
// dahil başka her şey 400); anahtar encoding/json gibi büyük/küçük harf
// duyarsız eşlenir.
func ParsePut(raw []byte) (Settings, PutOptions, error) {
	s, err := ParseInput(raw)
	if err != nil {
		return Settings{}, PutOptions{}, err
	}
	opts := PutOptions{ClearTokenRef: map[int]bool{}}
	var probe struct {
		Instances []map[string]json.RawMessage `json:"instances"`
	}
	_ = json.Unmarshal(raw, &probe) // ParseInput gövdeyi zaten doğruladı
	for i, inst := range probe.Instances {
		for _, k := range sortedKeys(inst) {
			if !strings.EqualFold(k, "clearTokenRef") {
				continue
			}
			var b bool
			if v := inst[k]; strings.TrimSpace(string(v)) == "null" || json.Unmarshal(v, &b) != nil {
				return Settings{}, PutOptions{}, fieldErr(fmt.Sprintf("instances[%d].clearTokenRef", i), "true ya da false olmalı")
			}
			if b {
				opts.ClearTokenRef[i] = true
			}
		}
	}
	return s, opts, nil
}

// ApplyPut — v0.10.974 — PUT girdisi + kayıtlı blob → kalıcı yazılacak blob
// (dosya başlığındaki (a)–(e) sırası). in: ParsePut çıktısı; stored:
// canlı/kalıcı blob (svc.Current); clusters: Validate'in Remote Cluster yüzü.
func ApplyPut(in Settings, opts PutOptions, stored Settings, clusters []ClusterRef) (Settings, error) {
	names := make(map[string]string, len(clusters))
	for _, c := range clusters {
		names[c.ID] = c.Name
	}
	if err := checkRemovedHubs(in, stored, names); err != nil { // (a) BE4
		return in, err
	}
	out, err := Validate(in, clusters) // (b)
	if err != nil {
		return in, err
	}
	for i, inst := range out.Instances { // (c)
		if opts.ClearTokenRef[i] && inst.TokenRef != "" {
			return in, fieldErr(fmt.Sprintf("instances[%d].clearTokenRef", i),
				"tokenRef doluyken clearTokenRef gönderilemez — yeni referansı yazın ya da kaldırın, ikisi birden değil")
		}
	}
	if err := checkRenamedIDs(out, stored, names); err != nil { // (d) BE3
		return in, err
	}
	storedRef := make(map[string]string, len(stored.Instances)) // (e) BE1
	for _, s := range stored.Instances {
		storedRef[strings.TrimSpace(s.ID)] = strings.TrimSpace(s.TokenRef)
	}
	out.Instances = append([]Instance(nil), out.Instances...)
	for i := range out.Instances {
		inst := &out.Instances[i]
		if inst.TokenRef != "" || opts.ClearTokenRef[i] {
			continue
		}
		ref := storedRef[inst.ID]
		if ref == "" {
			continue
		}
		// Kayıtlı ref config import'la (tam değiştirme, doğrulamasız) bozuk
		// gelmiş olabilir: PUT yolu onu sessizce yeniden kalıcılaştırmaz ve
		// değeri (düz token olabilir) mesajda YANKILAMAZ.
		if !secretref.Valid(ref) {
			return in, fieldErr(fmt.Sprintf("instances[%d].tokenRef", i),
				"kayıtlı referans geçersiz biçimde — yeni referans yazın ya da kaldırın (clearTokenRef); %s", secretref.InvalidMessage)
		}
		inst.TokenRef = ref
	}
	return out, nil
}

// hubLabel — mesajlarda hub: `"hub-2" (c-bbbb0002)`; ad bilinmiyorsa yalnız id.
func hubLabel(id string, names map[string]string) string {
	if n := names[id]; n != "" && n != id {
		return fmt.Sprintf("%q (%s)", n, id)
	}
	return fmt.Sprintf("%q", id)
}

// checkRemovedHubs — BE4: kayıtlı bir hub gövdenin hubs'ında yok ama bir
// instance hâlâ ona bağlı → ilk o instance'ın yolu.
func checkRemovedHubs(in, stored Settings, names map[string]string) error {
	storedHub := make(map[string]bool, len(stored.Hubs))
	for _, h := range stored.Hubs {
		storedHub[strings.TrimSpace(h.ClusterID)] = true
	}
	keptHub := make(map[string]bool, len(in.Hubs))
	for _, h := range in.Hubs {
		keptHub[strings.TrimSpace(h.ClusterID)] = true
	}
	attached := map[string]int{}
	for _, inst := range in.Instances {
		attached[strings.TrimSpace(inst.HubClusterID)]++
	}
	for i, inst := range in.Instances {
		h := strings.TrimSpace(inst.HubClusterID)
		if h == "" || !storedHub[h] || keptHub[h] {
			continue
		}
		return fieldErr(fmt.Sprintf("instances[%d].hubClusterId", i),
			"hub %s listeden çıkarılıyor ama %d instance ona bağlı — instance'ları başka hub'a taşıyın ya da hub'ı listede bırakın",
			hubLabel(h, names), attached[h])
	}
	return nil
}

// checkRenamedIDs — BE3: out Validate çıktısıdır (id/hub/ns kırpılmış, tek
// hub'da boş hub tamamlanmış). Kayıtlı blobda hubClusterId boşsa (eski blob)
// ve tek kayıtlı hub varsa o hub sayılır.
func checkRenamedIDs(out, stored Settings, names map[string]string) error {
	storedID := make(map[string]bool, len(stored.Instances))
	slot := make(map[[2]string]string, len(stored.Instances)) // (hub, ns) → kayıtlı id
	for _, s := range stored.Instances {
		id := strings.TrimSpace(s.ID)
		hub := strings.TrimSpace(s.HubClusterID)
		if hub == "" && len(stored.Hubs) == 1 {
			hub = strings.TrimSpace(stored.Hubs[0].ClusterID)
		}
		storedID[id] = true
		slot[[2]string{hub, strings.TrimSpace(s.HubNamespace)}] = id
	}
	incoming := make(map[string]bool, len(out.Instances))
	for _, inst := range out.Instances {
		incoming[inst.ID] = true
	}
	for i, inst := range out.Instances {
		if storedID[inst.ID] {
			continue
		}
		old, ok := slot[[2]string{inst.HubClusterID, inst.HubNamespace}]
		if !ok || incoming[old] {
			continue
		}
		hub := inst.HubClusterID
		if n := names[hub]; n != "" {
			hub = n
		}
		return fieldErr(fmt.Sprintf("instances[%d].id", i),
			"%q kayıtlı %q instance'ının yerini alıyor (%s/%s): kayıtlı kimlik değiştirilemez (Faz 3'te ClickHouse instance_id). "+
				"Önce %q satırını kaldırıp kaydedin, sonra %q ile ekleyin.",
			inst.ID, old, hub, inst.HubNamespace, old, inst.ID)
	}
	return nil
}
