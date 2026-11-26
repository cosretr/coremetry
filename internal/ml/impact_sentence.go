package ml

// impact_sentence.go — CoSRE araştırması (sre "impact" adımı): Problem'in
// üye olayının etki kuralı cümlesi ("normalde X, şimdi Y"). Cümlenin TEK
// TANIMI chstore'da (MLErrRateSentence / MLResponseTimeSentence /
// MLEndpoint* / MLConsumerLagSentence); burada yalnız dedektör → cümle
// seçimi, Problem detayı ve açıklamayla aynı metin.

import "github.com/cilcenk/coremetry/internal/chstore"

// ImpactSentence — SAF: etki kuralı olayının cümlesi. Kuralın sayısal
// cümlesi olmayan dedektörde ok=false (çağıran olayı bulgu yapmaz).
func ImpactSentence(e chstore.MLEvent) (string, bool) {
	if topic, ok := lagEventTopic(e); ok {
		return chstore.MLConsumerLagSentence(e.Service, topic, e.Observed, e.Baseline, 0), true
	}
	switch e.Detector {
	case detSvcErrorRate, detSvcResponseTime, detEndpointImpact:
		return svcEventSentence(e), true
	}
	return "", false
}
