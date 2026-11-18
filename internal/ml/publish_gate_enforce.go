package ml

// publish_gate_enforce.go — v0.10.2343, anomali kartı A2 (docs/ml/
// anomaly-card-audit.md §10 A2, §12 K10/K16): yayın kapılarının UYGULAMA
// yolu. KAPALI GELİR: mlPublishGatesEnforce false iken hiçbir şey değişmez
// (yazılan satır, liste, Problem terfisi, bildirim, kart A1 ile bayt bayt
// aynı — publish_gate_a2_pin_test.go pinler). K16: anahtar, operatör gölge
// raporunu (/api/ml/scorecard tick.publishGates; Anomaliler › ML › Teknik
// detay › "Yayın kapıları") okuyup onaylamadan AÇILMAZ. Açılış tek satır:
// aşağıdaki sabit true.
//
// Açıkken (A2):
//   - kapı kararı yazılır: drop kapısı kalan olay publish_state=held; kanıt
//     ya da etki kalan olay class=info (kayıt JSON'undaki sınıf dedektörün
//     sınıfı kalır — kapılar her tik aynı kayıttan yeniden değerlendirilir);
//   - liste SQL'i (servis / platform görünümü) held satırı düşürür; olay id
//     ile ve "Elenenler" görünümünde okunur kalır;
//   - grup ve servis düzeyi Problem kapısı yalnız YAYIMLANAN service_anomaly
//     olayını sayar (held / invalid / info / telemetry_volume aday değil);
//     kaydı olmayan olay kapıdan geçmiş sayılır (A1: değerlendirilemeyen kapı
//     olayı tutmaz);
//   - tek sinyal kolu kalır ama tetikleyen olayın evidence[] bağımsız kanıt
//     sayısı ≥ 2 şartına bağlanır (K10, B16 çözümü);
//   - bildirim yalnız bu Problem'lerden çıktığı için yalnız yayımlanan
//     service_anomaly'den gelir.
// Karar olayın SON yazımındaki sonuçtan okunur (kayıt tik sonunda eklenir,
// terfi kararı ondan önce): yeni olay ilk tikinde Problem adayı değildir
// (bir tik gecikme; tutulacak olayın bir tiklik Problem + bildirim açması
// yerine).

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// mlPublishGatesEnforce — A2 anahtarı (K2: kod sabiti, ayar değil). K16:
// operatör gölge raporunu onaylamadan true yapılmaz.
const mlPublishGatesEnforce = false

// publishGatesEnforce — testlerin dikiş yeri; üretim yalnız sabiti okur.
var publishGatesEnforce = mlPublishGatesEnforce

// PublishGatesEnforced — A2 yürürlükte mi (api liste süzgeci, rapor kipi).
func PublishGatesEnforced() bool { return publishGatesEnforce }

// PublishGatesCriterion — K16 inceleme ölçütü (rapor ve ekran metni).
const PublishGatesCriterion = "K16: kapılar, operatör bu gölge raporunu okuyup onaylamadan yürürlüğe girmez. " +
	"İnceleme: dedektör başına \"tutulurdu\" olaylardan gerçek bir Problem'le çakışan ya da Problem'e dönüşen var mı; " +
	"varsa örnek olaylar açılıp kapı kararı tek tek doğrulanır. Onaydan sonra açılış tek satır (mlPublishGatesEnforce)."

// PublishGatesMode — rapor kipi metni.
func PublishGatesMode() string {
	if publishGatesEnforce {
		return "A2: açık"
	}
	return "A2: kapalı"
}

// enforceVerdict — SAF: gölge kararının satıra yazımı (yalnız A2 açıkken
// çağrılır). Drop → held; info → sınıf info. Önceki tikten taşınan held /
// info, karar değişince geri alınır (yayımlanır / kaydın sınıfı).
func enforceVerdict(ev chstore.MLEvent, recClass string, o gateOutcome) chstore.MLEvent {
	switch {
	case o.Drop:
		ev.PublishState = PublishHeld
	case ev.PublishState == PublishHeld:
		ev.PublishState = PublishPublished
	}
	switch {
	case o.WouldClass == ClassInfo:
		ev.Class = ClassInfo
	case ev.Class == ClassInfo && recClass != ClassInfo:
		ev.Class = recClass
	}
	return ev
}

// PubVerdict — bir olayın son yazımındaki A2 sonucu (terfi kapısının girdisi).
type PubVerdict struct {
	State       string // publish_state ('' = kayıt yok)
	Class       string // class ('' = kayıt yok)
	Independent int    // evidence[] bağımsız kanıt sayısı (K9); kayıt yoksa 0
	At          int64  // unix sn (budama)
}

// pubVerdictOf — SAF: yazılan satırdan terfi girdisi.
func pubVerdictOf(ev chstore.MLEvent, now time.Time) PubVerdict {
	v := PubVerdict{State: ev.PublishState, Class: ev.Class, At: now.Unix()}
	if ev.Record != "" {
		var rec EventRecord
		if json.Unmarshal([]byte(ev.Record), &rec) == nil {
			v.Independent = independentEvidence(rec)
		}
	}
	return v
}

// problemCandidate — SAF: A2'de olay Problem adayı mı. pub nil = A2 kapalı
// (her olay aday; bugünkü davranış). Karar yoksa (ilk tik) aday değil.
// Yayımlanan service_anomaly ya da kaydı olmayan (kapılar değerlendiremez)
// olay aday; held / invalid / info / telemetry_volume değil.
func problemCandidate(e chstore.MLEvent, pub map[string]PubVerdict) bool {
	if pub == nil {
		return true
	}
	v, ok := pub[e.ID]
	if !ok {
		return false
	}
	switch {
	case v.State == PublishHeld || v.State == PublishInvalid:
		return false
	case v.Class != "" && v.Class != ClassServiceAnomaly:
		return false
	}
	return true
}

// singleArmEvidenceOK — SAF (K10): tek sinyal kolunun tetikleyicisi ≥ 2
// bağımsız kanıt taşıyor mu. pub nil = A2 kapalı (şart yok).
func singleArmEvidenceOK(e chstore.MLEvent, pub map[string]PubVerdict) bool {
	return pub == nil || pub[e.ID].Independent >= gateMinIndependent
}

// publishedCandidates — SAF: servis düzeyi kapının girdisi; pub nil → aynen.
func publishedCandidates(kept []chstore.MLEvent, pub map[string]PubVerdict) []chstore.MLEvent {
	if pub == nil {
		return kept
	}
	out := make([]chstore.MLEvent, 0, len(kept))
	for _, e := range kept {
		if problemCandidate(e, pub) {
			out = append(out, e)
		}
	}
	return out
}

// pubVerdictTTL — karar haritasının budama penceresi (gölge raporuyla aynı).
const pubVerdictTTL = gateShadowWindow

// pubVerdicts — kaydedicinin A2 karar haritası (yalnız A2 açıkken dolar;
// tik goroutine'i tek yazıcı/okuyucu).
type pubVerdicts map[string]PubVerdict

func (m pubVerdicts) observe(ev chstore.MLEvent, now time.Time) {
	if ev.ID != "" {
		m[ev.ID] = pubVerdictOf(ev, now)
	}
}

func (m pubVerdicts) prune(now time.Time) {
	cut := now.Add(-pubVerdictTTL).Unix()
	for id, v := range m {
		if v.At < cut {
			delete(m, id)
		}
	}
}

// publishVerdicts — tik başı: A2 açıksa budanmış karar haritası, kapalıysa
// nil (terfi kapısı bugünkü gibi).
func (r *Recorder) publishVerdicts(now time.Time) map[string]PubVerdict {
	if !publishGatesEnforce {
		r.pub = nil
		return nil
	}
	if r.pub == nil {
		r.pub = pubVerdicts{}
	}
	r.pub.prune(now)
	return r.pub
}

// observePublish — yazılan satırın kararı (yalnız A2 açıkken).
func (r *Recorder) observePublish(ev chstore.MLEvent, now time.Time) {
	if publishGatesEnforce && r.pub != nil {
		r.pub.observe(ev, now)
	}
}

// markGateLinks — K16 raporu: tutulacak olay varsa açık Problem anlık
// görüntüsü (OpenProblemsSnapshot, 5 sn memo — canlı yol aynı tikte zaten
// okur) ile servis çakışması; ML kararına giriş bellekten. Ek ES / VM yok.
func (r *Recorder) markGateLinks(ctx context.Context, groups []decidedGroup, svc map[string]SvcShadow) {
	if !global.gateShadowHasDrop() {
		return
	}
	var svcs map[string]bool
	if r.store != nil {
		if snap, err := r.store.OpenProblemsSnapshot(ctx); err == nil && snap != nil {
			svcs = map[string]bool{}
			for _, p := range snap.All() {
				if p != nil && p.Service != "" {
					svcs[p.Service] = true
				}
			}
		}
	}
	global.markGateLinks(escalatedIDs(groups, svc), svcs)
}
