// v0.9.838 — alert-problem öncelik merdiveninin vidaları operatörün
// eline geçti.
//
// Operator-reported: "hâlâ çok fazla alert rule'dan P1 geliyor". Prod'da
// 29 açık critical'in 22'si P1 rozetiyle duruyordu; örnek bir error_rate
// problemi 3.93/1.31 = 3.0× oranıyla "2× eşik ihlali" kapısından geçip
// otomatik P1 oluyordu. P1'in "her şeyi bırak" anlamı, listenin dörtte
// üçü P1 olduğunda kalmıyor.
//
// Kök neden chstore.computePriority'nin iki GÖMÜLÜ sabitiydi (ratio ≥ 2,
// openHours ≥ 4) ve hiçbir ayar blobu bu hatta dokunmuyordu —
// exception_triage yalnız exception gruplarına, anomaly_* yalnız anomali
// dedektörlerine iniyor. Sabiti bir çentik ötelemek exception tarafında
// ÜÇ kez denendi ve üç kez operatör bir çentik ötede aynı duvara çarptı
// (v0.9.627 / v0.9.699 / v0.9.775). Aynı hatayı burada tekrarlamıyoruz.
//
// BU SÜRÜM DAVRANIŞ DEĞİŞTİRMİYOR: varsayılanlar eski sabitlerin birebir
// aynısı. Katıyı sıkmak operatörün kararı, bir sürümün değil.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// LoadProblemPriority hydrates the process-global config from
// system_settings. Hata hâlinde sessizce varsayılanlarda kalır (Get
// zaten soft-fail'dir) — boot'u bir ayar okumasına bağlamıyoruz.
//
// Global chstore'da yaşıyor (api'de DEĞİL, exception_triage'ın aksine):
// chstore.EnrichProblemsWithPriority paket-düzeyi bir fonksiyon ve
// notify / anomaly paketlerinden de çağrılıyor, yani config'i okuyan yer
// chstore'un kendisi olmak zorunda.
//
// v0.10.1072 — okuma hatasında SON İYİ DEĞER korunur (LoadProblemPriorityWith):
// istisna listesi inbox'ın P1 görünümünü belirliyor, bir CH hıçkırığı onu
// varsayılana döndürüp satırları bir tik oynatmamalı.
func (s *Server) LoadProblemPriority(ctx context.Context) {
	chstore.LoadProblemPriorityWith(func() (chstore.ProblemPriorityConfig, error) {
		return s.store.ReadProblemPriority(ctx)
	})
}

// StartProblemPriorityRefresh — çok-pod yakınsaması. PUT'u alan pod
// anında günceller; diğerleri bir sonraki tikte. Leader gerekmiyor: bu
// bir OKUMA, yan etkisi yok.
func (s *Server) StartProblemPriorityRefresh(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.LoadProblemPriority(ctx)
		}
	}
}

// getProblemPriority — GET /api/settings/problem-priority.
//
// KAYITLI blob'dan okunuyor (canlı global'den değil): ikisi ayrışmış
// olabilir (bu pod henüz yenilemedi) ve ayar sayfası KAYDEDİLENİ
// göstermeli — operatör ne yazdığını görsün, hangi pod'a düştüğünü değil.
// metric_exclusions ucunun duruşuyla aynı.
//
// v0.10.1072 — okuma hatası artık HATA döner (varsayılan DEĞİL): ayar ekranı
// varsayılanla dolup bir Kaydet'te kayıtlı istisna listesini ezebilirdi
// (anomaly_sensitivity v0.10.1039 emsali).
func (s *Server) getProblemPriority(w http.ResponseWriter, r *http.Request) {
	c, err := s.store.ReadProblemPriority(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

// putProblemPriority validates + persists the knobs and swaps the
// process-global in the same request, so the next /api/problems read on
// THIS pod already uses them (restart yok).
//
// Sınırlar üst uçta bilerek geniş: "bende ancak 10× ihlal gece kaldırır"
// gerçek bir politika, yazım hatası değil. Alt uç 1.1×, çünkü 1.0
// "eşiği aşan HER ŞEY büyük ihlal" demek olurdu ve merdivenin üst
// basamağı anlamsızlaşırdı.
func (s *Server) putProblemPriority(w http.ResponseWriter, r *http.Request) {
	// v0.10.1072 — KAYITLI değerin üstüne decode (eskiden varsayılanların
	// üstüne): gövdede olmayan alan KAYITLI hâlinde kalır. Varsayılan tabanı,
	// istisna listesini göndermeyen bir istemcinin (eski UI, betik) kayıtlı
	// listeyi — operatörün `[]` ile kapattığı dahil — sessizce varsayılana
	// döndürmesi demekti. Okuma hatası 5xx: tahminin üstüne yazmayız.
	stored, err := s.store.ReadProblemPriority(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	c, err := decodeProblemPriorityPut(stored, r.Body)
	if err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if c.BigBreachRatio < chstore.MinBigBreachRatio || c.BigBreachRatio > 100 {
		http.Error(w, fmt.Sprintf("bigBreachRatio must be between %.1f and 100", chstore.MinBigBreachRatio),
			http.StatusBadRequest)
		return
	}
	// 0 GEÇERLİ: bayat-critical terfisini tamamen kapatır. Negatif değil.
	if c.StaleCriticalHours < 0 || c.StaleCriticalHours > 720 {
		http.Error(w, "staleCriticalHours must be between 0 and 720 (0 = promotion off)",
			http.StatusBadRequest)
		return
	}
	// v0.10.1072 — istisna listesi: normalize'ın SESSİZCE düşüreceği bir
	// girdi (literal öneksiz / 3 literalden az — "*:*" —, aşırı uzun, 20'den
	// fazla farklı kalıp) burada 400 — operatör yazdığının kaydedilmediğini görsün.
	if c.InboxKeepSourcePriority != nil {
		if err := chstore.ValidateInboxKeepSourcePriority(*c.InboxKeepSourcePriority); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	c = chstore.NormalizeProblemPriority(c)
	if err := s.store.SaveProblemPriority(r.Context(), c); err != nil {
		writeErr(w, err)
		return
	}
	chstore.SetProblemPriority(c)
	// Liste inbox satır önceliklerini değiştirir; 15 sn'lik önbellek eski
	// görünümü servis etmesin (susturma yazımlarıyla aynı önek düşürme).
	s.invalidateInboxCaches(r)
	keepJSON, _ := json.Marshal(c.InboxKeepSourcePriorityList())
	// v0.10.1081 — SLO burn-rate Problem anahtarı da audit'te (etkin değer).
	s.audit(r, "settings.update", "problem_priority", "problem_priority",
		fmt.Sprintf(`{"bigBreachRatio":%g,"staleCriticalHours":%g,"inboxKeepSourcePriority":%s,"sloBurnProblems":%t}`,
			c.BigBreachRatio, c.StaleCriticalHours, keepJSON, c.SLOBurnProblemsEnabled()))
	staleNote := fmt.Sprintf("%gsa", c.StaleCriticalHours)
	if c.StaleCriticalHours == 0 {
		staleNote = "KAPALI"
	}
	log.Printf("[settings] problem_priority: ihlal katı %g× · bayat-critical %s · SLO burn problemi %t",
		c.BigBreachRatio, staleNote, c.SLOBurnProblemsEnabled())
	writeJSON(w, c)
}

// decodeProblemPriorityPut — PUT gövdesini KAYITLI değerin üstüne çözer
// (SAF, tablo-testli). Gövdede olmayan alan kayıtlı hâlinde kalır; istisna
// listesi taze bir dilime kopyalanır ki decode kayıtlı değerin dizisini
// yerinde ezmesin. `"inboxKeepSourcePriority": null` açıkça varsayılana
// dönüş demektir (nil işaretçi = varsayılan liste).
func decodeProblemPriorityPut(stored chstore.ProblemPriorityConfig, body io.Reader) (chstore.ProblemPriorityConfig, error) {
	c := stored
	if stored.InboxKeepSourcePriority != nil {
		cp := append([]string{}, *stored.InboxKeepSourcePriority...)
		c.InboxKeepSourcePriority = &cp
	}
	// v0.10.1081 — SLO burn bayrağı da taze işaretçiye: decode kayıtlı değerin
	// bool'unu yerinde ezmesin. Gövdede alan yoksa kayıtlı değer kalır.
	if stored.SLOBurnProblems != nil {
		v := *stored.SLOBurnProblems
		c.SLOBurnProblems = &v
	}
	if err := json.NewDecoder(body).Decode(&c); err != nil {
		return chstore.ProblemPriorityConfig{}, err
	}
	return c, nil
}
