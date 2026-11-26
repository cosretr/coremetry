package ml

// publish_gate_enforce.go — gürültü kapılarının UYGULAMA yolu. v0.10.2343'te
// kod sabitiyle (mlPublishGatesEnforce = false) kapalı geldi; v0.10.2473
// (operatör kararı 2026-11-26) sabit operatör ayarı oldu: "ml" blobunda
// publishGates = off | shadow | on, varsayılan ON (chstore.MLConfig.
// PublishGatesMode; kayıtlı açık değer kazanır). Kip tik başında ayardan
// okunur ve bu dosyadaki işlevlere girdi olarak geçer (paket düzeyi durum yok).
//
//   - off:    kapılar hiç değerlendirilmez (kayda gates[] yazılmaz).
//   - shadow: v0.10.2472 davranışı — sonuç yalnız record.gates[]'e ve gölge
//     raporuna yazılır; liste, Problem adaylığı, bildirim aynen
//     (publish_gate_a2_pin_test.go bayt bayt pinler).
//   - on:     HACİM (min_volume), MUTLAK FARK (min_abs_diff) ya da GÖRÜNÜR
//     SAPMA (visible_deviation) kapısında kalan olay publish_state=held
//     yazılır; ana Anomaliler listesi (servis / platform görünümü) held satırı
//     düşürür, grup ve servis düzeyi Problem kapısı onu aday saymaz. Olay
//     kaydı durur: kimliğiyle açılır ve "Elenenler" görünümünde nedeniyle
//     listelenir.
//
// HİÇBİR kipte uygulanmayanlar (yalnız kayda yazılır):
//   - K10 "≥ 2 bağımsız kanıt" (independent_evidence): operatör kararı
//     2026-11-19 — tek etki olayı Problem açar. Sınıf info'ya inmez, tek
//     sinyal kolu kanıt sayısına bakmaz (publish_gate_enforce_test.go TestK10NeverEnforced pinler).
//   - service_impact ve series_consistency: operatör yalnız üç kapıyı onayladı.
//
// ETKİ KURALI OLAYLARI MUAF (impactRuleRun: servis hata oranı, servis yanıt
// süresi, etki kuralıyla açılmış consumer lag): kendi yük kapıları ve eşikleri
// var (impact_rule.go); kapı satırları kayda yazılmaya devam eder ama olay
// hiçbir kipte tutulmaz ve karar beklemeden adaydır (çift kapı yok).
//
// Karar olayın SON yazımındaki sonuçtan okunur (kayıt tik sonunda eklenir,
// terfi kararı ondan önce): etki kuralı dışındaki yeni olay ilk tikinde
// Problem adayı değildir (bir tik gecikme; tutulacak olayın bir tiklik
// Problem + bildirim açması yerine). Kip açıldığı / lider değiştiği ilk tik
// karar haritası boştur: o tik kapanış da yapılmaz (aday görünmeyen açık
// Problem kapanıp bir tik sonra yeniden açılmasın).

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// gatesEvaluated / gatesEnforced — SAF: kipin iki sorusu.
func gatesEvaluated(mode string) bool { return mode != chstore.MLGatesOff }
func gatesEnforced(mode string) bool  { return mode == chstore.MLGatesOn }

// PublishGatesCriterion — rapor metni: hangi kipte neyin uygulandığı.
const PublishGatesCriterion = "Kip ayardan okunur (Settings › Anomaly › ML › Gürültü kapıları: off | shadow | on, varsayılan on). " +
	"on: hacim (en az 100 çağrı), mutlak fark (en az 25 ms) ya da görünür sapma kapısında kalan olay ana listede görünmez ve Problem adayı olmaz; Elenenler'de nedeniyle okunur. " +
	"Bağımsız kanıt (K10), servis etkisi ve seri tutarlılığı satırları hiçbir kipte uygulanmaz, yalnız kayda yazılır. " +
	"Etki kuralı olayları (servis hata oranı, servis yanıt süresi, consumer lag) muaftır."

// gateHoldExempt — SAF: kapıların hiçbir kipte tutmadığı olay. Etki kuralı
// aileleri kendi yük kapısı ve eşiğiyle yargılanır (çift kapı yok).
func gateHoldExempt(ev chstore.MLEvent) bool { return impactRuleRun(ev) }

// enforceVerdict — SAF: kararın satıra yazımı (yalnız on kipinde çağrılır).
// Uygulanan kapıda kalan olay held; önceki tikten taşınan held karar
// değişince geri alınır. Sınıfa DOKUNMAZ (K10 / etki satırı uygulanmaz).
func enforceVerdict(ev chstore.MLEvent, o gateOutcome) chstore.MLEvent {
	switch {
	case o.Drop:
		ev.PublishState = PublishHeld
	case ev.PublishState == PublishHeld:
		ev.PublishState = PublishPublished
	}
	return ev
}

// PubVerdict — bir olayın son yazımındaki sonuç (terfi kapısının girdisi).
type PubVerdict struct {
	State string // publish_state ('' = kayıt yok)
	At    int64  // unix sn (budama)
}

// problemCandidate — SAF: olay Problem adayı mı. pub nil = kip on değil (her
// olay aday; shadow / off davranışı). Etki kuralı olayı her zaman aday (muaf,
// karar beklemez). Karar yoksa (ilk tik) aday değil; held aday değil. Kaydı
// olmayan, geçersiz ya da sınıfı farklı olay bugünkü gibi adaydır: yalnız üç
// gürültü kapısı eler.
func problemCandidate(e chstore.MLEvent, pub map[string]PubVerdict) bool {
	if pub == nil || gateHoldExempt(e) {
		return true
	}
	v, ok := pub[e.ID]
	return ok && v.State != PublishHeld
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

// pubVerdicts — kaydedicinin karar haritası (yalnız on kipinde dolar; tik
// goroutine'i tek yazıcı/okuyucu).
type pubVerdicts map[string]PubVerdict

func (m pubVerdicts) observe(ev chstore.MLEvent, now time.Time) {
	if ev.ID != "" {
		m[ev.ID] = PubVerdict{State: ev.PublishState, At: now.Unix()}
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

// publishVerdicts — tik başı: on kipinde budanmış karar haritası, değilse nil
// (terfi kapısı shadow / off'taki gibi). warming: harita bu tik kuruldu (kip
// yeni açıldı ya da lider yeni) — hiçbir olayın kararı yok; çağıran o tik
// Problem kapatmaz.
func (r *Recorder) publishVerdicts(now time.Time, mode string) (pub map[string]PubVerdict, warming bool) {
	if !gatesEnforced(mode) {
		r.pub = nil
		return nil, false
	}
	if r.pub == nil {
		r.pub, warming = pubVerdicts{}, true
	}
	r.pub.prune(now)
	return r.pub, warming
}

// observePublish — yazılan satırın kararı (harita yalnız on kipinde dolu).
func (r *Recorder) observePublish(ev chstore.MLEvent, now time.Time) {
	if r.pub != nil {
		r.pub.observe(ev, now)
	}
}

// markGateLinks — gölge raporu: tutulacak olay varsa açık Problem anlık
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
