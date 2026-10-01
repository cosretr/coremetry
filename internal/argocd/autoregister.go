package argocd

// autoregister.go — v0.10.1013 — keşfedilen Argo CD instance'larının
// KENDİLİĞİNDEN kaydı (operatör, 2026-10-01: "Argocd entegrasyonu da
// autodiscover etse daha iyi olacak, şu anda tek tek ekle diyorum"; kuyruktan
// "devam sırayla").
//
// Annex §7.2'nin kuralı "öner, asla otomatik yazma" idi: keşif adayları
// gösterir, kaydı operatör yapar. Prod'da ekip × ortam başına ayrı instance
// (hub başına ~190) olduğundan her yeni ekipte Ayarlar'a girip "Tümünü ekle +
// Kaydet" gerekiyordu; kaydı olmayan instance'ın uygulamaları eşleyicide yok,
// GitOps sekmesi eksik kalıyordu. Bu dosya o kuralı AÇIK BİR ANAHTARLA
// (autoRegister.enabled, varsayılan KAPALI) tersine çevirir.
//
// Sınırlar — otomatik yazım yalnız EKLER:
//
//   - Aday kuralı elle "Tümünü ekle" ile AYNI (FE addableRows): yeni, hatasız,
//     namespace'i belli. Namespace'i bilinmeyen aday (durum B, çok namespace)
//     eklenmez — onu operatör elle tamamlar.
//   - Mevcut instance'a DOKUNULMAZ (alanı değişmez, silinmez, etkinliği
//     değişmez). Kimliği ya da (hub, namespace) yuvası dolu aday atlanır.
//   - Tavan maxInstances; dolunca kalanlar atlanır ve sayılır.
//   - Eklenen satır elle eklenenle aynı şekli taşır (enabled, discovered;
//     apiUrl / tokenRef boş).
//
// BİLİNEN SONUÇ: operatörün SİLDİĞİ bir instance, Argo'da hâlâ varsa bir
// sonraki turda geri eklenir. İstenmeyen instance silinmez, DEVRE DIŞI
// bırakılır (kaydı durduğu için yeniden eklenmez). Ayar ekranı bunu söyler.
//
// Tur, taze blob üzerinde koşar ve sonucu Validate'ten geçirir (PUT ile aynı
// kurallar); doğrulama düşerse HİÇBİR ŞEY yazılmaz.

// AutoRegisterSettings — otomatik kaydın anahtarı. Validate entegrasyon
// kapalıyken bunu da kapatır (metricsWorker emsali).
type AutoRegisterSettings struct {
	Enabled bool `json:"enabled"`
}

// AutoRegisterActive — SAF bayrak kapısı: entegrasyon açık VE anahtar açık VE
// en az bir hub.
func AutoRegisterActive(s Settings) bool {
	return s.Enabled && s.AutoRegister.Enabled && len(s.Hubs) > 0
}

// AutoRegisterable — SAF: aday otomatik kayda uygun mu (FE addableRows ile
// aynı kural + kimlik/hub dolu).
func AutoRegisterable(c Candidate) bool {
	return c.Error == "" && c.ConfiguredID == "" && c.HubNamespace != "" && c.ID != "" && c.HubClusterID != ""
}

// autoRegisterInstance — SAF: aday → instance (FE candidateRow ile aynı şekil).
func autoRegisterInstance(c Candidate) Instance {
	return Instance{
		ID: c.ID, HubClusterID: c.HubClusterID, HubNamespace: c.HubNamespace, MetricsJob: c.MetricsJob,
		Enabled: true, Discovered: true, AppsAnyNamespace: c.AppsAnyNamespace || c.NamespaceCase == "C",
	}
}

// AutoRegisterResult — bir turun dökümü.
type AutoRegisterResult struct {
	Added        []Instance // bloba eklenenler (sırasıyla)
	SkippedTaken int        // kimliği ya da (hub, namespace) yuvası dolu
	SkippedFull  int        // tavan (maxInstances) doldu
	SkippedOther int        // uygun değil (hatalı / kayıtlı / namespace'siz)
}

// MergeAutoRegistered — SAF (tablo testli): taze bloba uygun adayları EKLER.
// Mevcut instance'lar aynen ve aynı sırada kalır; eklenenler sona gelir.
func MergeAutoRegistered(cur Settings, cands []Candidate) (Settings, AutoRegisterResult) {
	var res AutoRegisterResult
	out := cur
	out.Instances = append([]Instance(nil), cur.Instances...)
	ids := make(map[string]bool, len(out.Instances))
	slots := make(map[[2]string]bool, len(out.Instances))
	hubs := make(map[string]bool, len(cur.Hubs))
	for _, h := range cur.Hubs {
		hubs[h.ClusterID] = true
	}
	for _, inst := range out.Instances {
		ids[inst.ID] = true
		slots[[2]string{inst.HubClusterID, inst.HubNamespace}] = true
	}
	for _, c := range cands {
		if !AutoRegisterable(c) || !hubs[c.HubClusterID] {
			res.SkippedOther++
			continue
		}
		slot := [2]string{c.HubClusterID, c.HubNamespace}
		if ids[c.ID] || slots[slot] {
			res.SkippedTaken++
			continue
		}
		if len(out.Instances) >= maxInstances {
			res.SkippedFull++
			continue
		}
		inst := autoRegisterInstance(c)
		out.Instances = append(out.Instances, inst)
		ids[inst.ID], slots[slot] = true, true
		res.Added = append(res.Added, inst)
	}
	return out, res
}
