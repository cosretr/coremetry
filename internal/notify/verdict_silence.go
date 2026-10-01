package notify

// verdict_silence.go — v0.10.1016 — "Problem değil" imzalarının BİLDİRİMİ.
//
// v0.10.1015 kararları yalnız görünümdü: operatör bir imzayı "problem değil"
// diye işaretlese de mail / kanal bildirimi gelmeye devam ediyordu. Bu dosya
// öğretmeyi bildirime bağlayan kapı — ama YALNIZ yönetici politikayı açtıysa
// (system_settings["problem_verdict_policy"].muteNotifications; varsayılan
// KAPALI). Kapalıyken tek maliyet 30 sn'de bir küçük ayar okuması; hiçbir
// bildirim değişmez.
//
// Açıkken susan: "problem değil" imzalı alarm kuralı problemi (p:<kural>|
// <servis>) ile exception / HTTP hata grubu (e:<parmak izi>) — ekip maili,
// kanallar, çözüm bildirimi ve P1 exception anonsu dahil. Susmayan: canlı
// akış (SSE), Problem kaydının kendisi, olaylar (incident — imzası yok) ve
// anomali OLAYI imzaları (a:…; bildirim hunisine ayrı bir kimlikle girmezler).
//
// İmza sunucuda Problem'den yeniden üretilir ve FE'nin ürettiğiyle
// (frontend/src/lib/problemVerdict.ts inboxSignature) birebir aynı olmalıdır.
//
// Güvenli yön: ayar ya da karar listesi okunamazsa SUSTURMA YOK — bildirim
// kaybetmek fazladan bir mailden kötü (NotificationIgnored kapısıyla aynı).

import (
	"context"
	"log"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// verdictSilenceTTL — politika + karar listesi önbelleği. Bir imzayı "problem
// değil" yapmak (ya da geri almak) en geç bu kadar sonra bildirime yansır.
const verdictSilenceTTL = 30 * time.Second

// verdictSignature — SAF: bildirim hunisindeki Problem → öğretme imzası.
// "" = öğretilemeyen tür (olay, kural kimliği olmayan sentetik kayıt).
func verdictSignature(p chstore.Problem) string {
	if p.Kind == chstore.NotifyKindIncident {
		return ""
	}
	if fp := exceptionGroupFingerprint(p.ID); fp != "" {
		return exceptionVerdictSignature(fp)
	}
	if p.RuleID == "" {
		return ""
	}
	return "p:" + p.RuleID + "|" + p.Service
}

// exceptionVerdictSignature — SAF: exception / HTTP hata grubu imzası.
func exceptionVerdictSignature(fingerprint string) string {
	if fingerprint == "" {
		return ""
	}
	return "e:" + fingerprint
}

// noiseSignatureSet — SAF: karar listesi → "problem değil" imza kümesi.
func noiseSignatureSet(list []chstore.ProblemVerdict) map[string]struct{} {
	out := make(map[string]struct{})
	for _, v := range list {
		if v.Verdict == chstore.ProblemVerdictNoise {
			out[v.Signature] = struct{}{}
		}
	}
	return out
}

// loadVerdictSilence — politika + (yalnız açıksa) karar listesi. Herhangi bir
// okuma hatası = susturma yok.
func (n *Notifier) loadVerdictSilence(ctx context.Context) (bool, map[string]struct{}) {
	pol, err := n.store.GetProblemVerdictPolicy(ctx)
	if err != nil {
		log.Printf("[notify] problem verdict policy fetch: %v", err)
		return false, nil
	}
	if !pol.MuteNotifications {
		return false, nil
	}
	list, err := n.store.ListProblemVerdicts(ctx)
	if err != nil {
		log.Printf("[notify] problem verdicts fetch: %v", err)
		return false, nil
	}
	return true, noiseSignatureSet(list)
}

// verdictSilenced — bu imza "problem değil" ve politika açık mı. Önbellek
// verdictSilenceTTL'den eskiyse tazeler.
func (n *Notifier) verdictSilenced(ctx context.Context, sig string) bool {
	if n == nil || n.store == nil || sig == "" {
		return false
	}
	n.pvMu.Lock()
	defer n.pvMu.Unlock()
	if time.Since(n.pvAt) > verdictSilenceTTL {
		n.pvMute, n.pvNoise = n.loadVerdictSilence(ctx)
		n.pvAt = time.Now()
	}
	if !n.pvMute {
		return false
	}
	_, ok := n.pvNoise[sig]
	return ok
}
