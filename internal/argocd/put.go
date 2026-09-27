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
//
// ── v0.10.978 — (0) iyimser ön koşul: expectedUpdatedAt ───────────────────
//
// v0.10.974'te ertelenen karar (docs/DECISIONS.md "Açık kalan"): iki admin
// aynı anda düzenlerken bütün blob değiştirildiğinden son yazan kazanıyordu.
// Gövdedeki İSTEK-YALNIZ `expectedUpdatedAt` (GET settings.updatedAt; Instance
// gibi Settings alanı DEĞİL, bloba/cevaba girmez) kayıtlı blobun (api katmanı
// PUT'tan hemen önce LoadPersisted ile tazeler) UpdatedAt'iyle karşılaştırılır:
// tutmazsa StaleError → 409 {error, errorType:"stale", updatedAt:<kayıtlı>};
// gönderilmemişse kabul (API/token çağıranlar, eski bundle) — api katmanı bunu
// audit'te "precondition":"none" olarak işaretler. Denetim (a)'dan ÖNCE: bayat
// taban üzerinde alan hatası anlamsız, kullanıcının gördüğü blob artık yok.
//
// Karşılaştırma float64 üzerinden (sameStamp): tarayıcı JSON sayısını çift
// duyarlıkla okur; ns damgası 2^53'ün üstünde olduğundan 256'nın katına
// yuvarlanır ve JSON.stringify o yuvarlanmış değeri yazar. Tam int64 eşitliği
// her tarayıcı PUT'unu 409'a düşürürdü; float64 eşitliği tarayıcının gördüğü
// değerle aynıdır, iki kayıt arasındaki ≥256 ns farkı yine ayırır (bir kayıt
// milisaniyeler sürer). Tam sayı gönderen API istemcisi için de birebir.
//
// Atomiklik burada DEĞİL: karşılaştırma saf, kilit yok. api katmanı
// (argocdPutMu) yükle → karşılaştır → yaz sırasını POD BAŞINA mutex'le sıralar;
// aksi hâlde aynı damgayı taşıyan iki eşzamanlı PUT ikisi de geçer ve ikinci
// birincinin düzenlemesini ezerdi. Kalıcı blobu okuyamayan pod ön koşulu
// doğrulayamaz → 503 (bellekteki bayat bloba karşı karşılaştırma yanlış audit +
// kayıp yazım olurdu). Pod'lar arası aynı-ms çift yazım son-yazan-kazanır kalır:
// Store'da compare-and-set yok; gerekirse ileride PutSettingIf(key, value,
// expectedStamp) (docs/DECISIONS.md "Açık kalan").

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
	// ExpectedUpdatedAt — v0.10.978 — iyimser ön koşul: istemcinin GET'te
	// gördüğü settings.updatedAt (ns; hiç kaydedilmemiş blob için 0). nil =
	// gönderilmedi → ön koşul yok (dosya başlığı (0)).
	ExpectedUpdatedAt *int64
}

// StaleError — v0.10.978 — ön koşul tutmadı: api katmanı 409 {error,
// errorType:"stale", updatedAt: Current} yazar (FieldError DEĞİL: 400 değil).
// Metin kısa ve Türkçe — FE kendi kutusunu çizer, bu metin API/token
// çağıranlara gider.
type StaleError struct {
	Expected int64 // istemcinin gönderdiği
	Current  int64 // kayıtlı blobun damgası
}

func (e *StaleError) Error() string {
	return "ayarlar bu sayfa yüklendikten sonra başka biri tarafından değiştirildi — yeniden yükleyin"
}

// sameStamp — SAF: iki ns damgası tarayıcının gördüğü çift duyarlıkta eşit mi
// (dosya başlığı (0)).
func sameStamp(a, b int64) bool { return float64(a) == float64(b) }

// checkPrecondition — (0): gönderilmediyse geçer; tutmuyorsa StaleError.
func checkPrecondition(opts PutOptions, stored Settings) error {
	if opts.ExpectedUpdatedAt == nil || sameStamp(*opts.ExpectedUpdatedAt, stored.UpdatedAt) {
		return nil
	}
	return &StaleError{Expected: *opts.ExpectedUpdatedAt, Current: stored.UpdatedAt}
}

// ParsePut — v0.10.974 — ParseInput (düz token + eski tek-hub anahtarı reddi
// korunur) + instances[i].clearTokenRef. Bayrak varsa JSON bool olmalı (null
// dahil başka her şey 400); anahtar encoding/json gibi büyük/küçük harf
// duyarsız eşlenir. v0.10.978 — üst düzey expectedUpdatedAt: varsa 0 ya da
// pozitif JSON tam sayısı (null/dize/ondalık/üstel/negatif 400); yoksa nil.
func ParsePut(raw []byte) (Settings, PutOptions, error) {
	s, err := ParseInput(raw)
	if err != nil {
		return Settings{}, PutOptions{}, err
	}
	opts := PutOptions{ClearTokenRef: map[int]bool{}}
	var probe struct {
		Instances         []map[string]json.RawMessage `json:"instances"`
		ExpectedUpdatedAt json.RawMessage              `json:"expectedUpdatedAt"`
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
	if v := probe.ExpectedUpdatedAt; len(v) > 0 { // RawMessage: yok → boş, null → "null"
		var n int64
		if strings.TrimSpace(string(v)) == "null" || json.Unmarshal(v, &n) != nil || n < 0 {
			return Settings{}, PutOptions{}, fieldErr("expectedUpdatedAt", "0 ya da pozitif tam sayı olmalı (GET settings.updatedAt)")
		}
		opts.ExpectedUpdatedAt = &n
	}
	return s, opts, nil
}

// ApplyPut — v0.10.974 — PUT girdisi + kayıtlı blob → kalıcı yazılacak blob
// (dosya başlığındaki (0) + (a)–(e) sırası). in: ParsePut çıktısı; stored:
// canlı/kalıcı blob (svc.Current); clusters: Validate'in Remote Cluster yüzü.
func ApplyPut(in Settings, opts PutOptions, stored Settings, clusters []ClusterRef) (Settings, error) {
	if err := checkPrecondition(opts, stored); err != nil { // (0) v0.10.978
		return in, err
	}
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
