package chstore

// problem_graph_enforce.go — v0.10.2341 (entity grafı ile Problem birleştirme
// S4, docs/problems/entity-graph-merge-design.md §2, §7, §9): "birleşmeyi
// uygula" diliminin BİLDİRİM yarısı. KAPALI GELİR.
//
// v0.10.2473 — dilim ikiye ayrıldı:
//   - GÖRÜNÜM (Problems listesi bağlı kümeyi tek satırda katlar, sayaç kümeyi
//     sayar, manşete köken yazılır; emme sınırı — 90 dk, yalnız servisler
//     arası; aynı serviste "geç katıldı" işareti — küme kurulumunda uygulanır)
//     artık operatör ayarı: problem_graph_merge_setting.go, varsayılan `on`.
//   - BİLDİRİM (sonradan bağlanan üyenin açılış bildirimi NotifiableAt'ın
//     `merged` tutma koduyla tutulur; P1 ve şiddet artışı baypas eder) bu
//     dosyadaki kod sabitinde kalır: problemGraphMergeEnforce = false. Ayar
//     bu sabiti DEĞİŞTİRMEZ; false iken bildirim kararları, politika gölge
//     satırı, özet ve webhook / MCP gövdeleri v0.10.2472 ile bayt bayt aynı.
// Hiçbir Problem kapatılmaz (karar 1): kapatmak veri onarımı gerektirmez.
//
// Bildirim tutmasını açma ölçütü (tasarım §9) DEĞİŞMEDİ: en az 7 gün gölge;
// tutulacak bildirimler arasında sonradan P1'e çıkan / onaylı vakaya dönen
// SIFIR; yanlış bağ örnekleri operatörce gözden geçirilmiş. Admin gölge
// raporu bu sayıları gösterir.

import (
	"sync/atomic"
	"time"
)

// problemGraphMergeEnforce — S4 BİLDİRİM TUTMASI anahtarı (v0.10.2473'ten
// beri yalnız bu; görünüm kipi ayarda). Açmak = bu satırı true yapmak.
const problemGraphMergeEnforce = false

// problemGraphFrequentEnforce — v0.10.2342 (S5, tasarım §6): "sık tekrar
// eden" tutma kodu `frequent` gölgeden enforce yoluna geçer. KAPALI GELİR;
// açmak = bu satırı true yapmak. Kapalıyken bildirim kararı, politika satırı,
// S3 gölge satırı ve liste çipleri bugünküyle aynı. Açma ölçütü S4 ile aynı
// (tasarım §9): 7 gün gölge, tutulacaklardan P1'e çıkan / onaylı vakaya
// dönen sıfır.
const problemGraphFrequentEnforce = false

var graphFrequentOverride atomic.Int32

// ProblemGraphFrequentEnforced — S5 açık mı (sabit; testte kapı).
func ProblemGraphFrequentEnforced() bool {
	switch graphFrequentOverride.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	return problemGraphFrequentEnforce
}

// SetProblemGraphFrequentEnforceForTest — TEST KAPISI (S5); üretim çağırmaz.
func SetProblemGraphFrequentEnforceForTest(on bool) (restore func()) {
	prev := graphFrequentOverride.Load()
	v := int32(2)
	if on {
		v = 1
	}
	graphFrequentOverride.Store(v)
	return func() { graphFrequentOverride.Store(prev) }
}

// frequentBypass — SAF (S5): sık tekrar eden açılış yine de bildirilir mi.
// Tutulduktan sonra önceliği yükselen (D5 yükseliş) geçer; şiddet arttı =
// penceredeki en yüksek önceliği / en büyük büyüklüğün 2 katını aşan ya da
// yüksek güvenli P1 (notifyBypass — gölgedekiyle aynı ölçü, şimdiki değerle).
func frequentBypass(std ProblemStandard, prio, heldPrio string, v FrequentVerdict) string {
	if heldPrio != "" && prioRank(prio) > prioRank(heldPrio) {
		return "escalated"
	}
	return notifyBypass(std, prio, v.MaxPriority, v.MaxMagnitude)
}

// Emme sınırı (tasarım §7, operatör kararı 3).
const ProblemGraphAbsorbLimit = 90 * time.Minute

// NotifyHoldMerged — S4 tutma kodu (gölge satırında "hold:merged").
const NotifyHoldMerged = "merged"

// graphMergeOverride — yalnız test kapısı: 0 = sabit, 1 = açık, 2 = kapalı.
var graphMergeOverride atomic.Int32

// ProblemGraphMergeEnforced — S4 bildirim tutması açık mı (sabit; testte
// kapı). Görünüm kipi DEĞİL: liste / manşet için ProblemGraphMergeOn().
func ProblemGraphMergeEnforced() bool {
	switch graphMergeOverride.Load() {
	case 1:
		return true
	case 2:
		return false
	}
	return problemGraphMergeEnforce
}

// SetProblemGraphMergeEnforceForTest — TEST KAPISI: sabiti düzenlemeden
// kipi değiştirir; dönen işlev eski hâline getirir. Üretim kodu çağırmaz.
func SetProblemGraphMergeEnforceForTest(on bool) (restore func()) {
	prev := graphMergeOverride.Load()
	v := int32(2)
	if on {
		v = 1
	}
	graphMergeOverride.Store(v)
	return func() { graphMergeOverride.Store(prev) }
}

// MergeHold — sonradan bağlanan üyenin tutma girdisi (NotifyHistory.Merged).
// Ref* = kümenin (sahip dahil, bu üye hariç) en yüksek görünüm önceliği ve
// en büyük büyüklüğü: üye bunları aşarsa şiddet arttı sayılır (D3 ölçüsü).
type MergeHold struct {
	Rule         string   `json:"rule"` // üyeyi kümeye bağlayan kural (M-*)
	OwnerID      string   `json:"ownerId"`
	OwnerName    string   `json:"ownerName"`
	RefPriority  string   `json:"refPriority,omitempty"`
	RefMagnitude float64  `json:"refMagnitude,omitempty"`
	Late         bool     `json:"late,omitempty"` // aynı servis, emme sınırından sonra ("geç katıldı")
	Owner        *Problem `json:"-"`              // sahibin satırı (bildirim kontrolü)
}

// ProblemGraphMergeReport — S4 geçiş ölçütü sayaçları (24 sa, lider belleği).
type ProblemGraphMergeReport struct {
	Enforce bool `json:"enforce"` // S4 bildirim tutması açık mı (yazım anında)
	// Mode — v0.10.2473: görünüm kipi (yazım anında; off | shadow | on).
	Mode string `json:"mode,omitempty"`
	// Joined — sonradan bağlanan FARKLI üye satırı; WouldHold — bunlardan
	// açılışı `merged` koduyla tutulacak olanlar (P3 değil, P1 değil, şiddet
	// artmamış); HeldBecameP1 — tutulacaklardan sonradan P1'e çıkan (hedef 0).
	Joined       int64        `json:"joined"`
	WouldHold    int64        `json:"wouldHold"`
	HeldBecameP1 int64        `json:"heldBecameP1"`
	BecameP1     []FrequentP1 `json:"becameP1,omitempty"`
	Late         int64        `json:"late"` // aynı servis, emme sınırından sonra ("geç katıldı")
}

// MergeWouldHold — SAF: üye açılışı `merged` adımında tutulur muydu (tutulmuş
// öncelik defteri olmadan; rapor ve test için).
func MergeWouldHold(p Problem, prio string, m MergeHold, now time.Time) bool {
	return mergeBypass(StandardizeProblem(p, StdFacts{Now: now}), prio, "", m) == ""
}

// mergeBypass — SAF: tutulacak üye yine de bildirilir mi. P1 her zaman
// geçer (D5: P1'e geçiş bildirimi), tutulmuş üyenin önceliği yükseldiyse
// geçer, kümenin önceliğini / büyüklüğünü aşan şiddet geçer (notifyBypass).
func mergeBypass(std ProblemStandard, prio, heldPrio string, m MergeHold) string {
	if prio == "P1" {
		return "p1"
	}
	if heldPrio != "" && prioRank(prio) > prioRank(heldPrio) {
		return "escalated"
	}
	return notifyBypass(std, prio, m.RefPriority, m.RefMagnitude)
}
