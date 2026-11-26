package chstore

// problem_standard_lag.go — v0.10.2475 (operatör bildirimi: "Hiçbir şey
// anlaşılmıyor"). Consumer lag etki olayının açtığı ML grup Problem'i
// (`anomaly-auto:group:*`, tek sinyal kolu) manşette iç kapı dilini ("tek
// güçlü sinyal (metrik, başka sinyal teyit etmedi)"), ham ölçü adını
// (ml_group_lag) ve birimsiz "N×" oranlarını gösteriyordu. Bu dosya lag
// Problem'inin SAF çekirdeği:
//
//  1. CÜMLE — "<svc> · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936
//     (topic orders-topic)." TEK TANIM: ML tiki Problem açıklamasına bu
//     işlevle yazar (ml singleProblemDescription), standart açıklamadan geri
//     okur (ParseMLConsumerLag) — manşet, etki, "kim etkileniyor" ve "ne
//     yapılabilir" aynı sayıları söyler. İngilizce arayüz anahtarı mlLagMsg.
//  2. ÖLÇÜ — normal / şimdi olayın KENDİ değerleri (gözlenen = en yeni ölçülen
//     dakikanın lag'i, taban = mevsimsel medyan). Problem'in Value /
//     Threshold'u (öncelik ölçüsü, v0.10.2396) yalnız daha yüksek oranla
//     yazıldığı için TEPE değeri taşır, "şimdi" değildir; ikisi de mesaj.
//
// Consumer group: lag serisi servis × topic anahtarlı (ml kafka_topic.go,
// `max by (svc, topic)`); consumer group etiketi seride yok, cümle onu
// iddia etmez. Servis = consumer.

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// StdLag — consumer lag Problem'inin gerçekleri (açıklamadaki cümleden).
type StdLag struct {
	Topic    string  `json:"topic,omitempty"` // "" = servis düzeyi seri
	Observed float64 `json:"observed"`        // şimdi (mesaj)
	Baseline float64 `json:"baseline"`        // normal (mevsimsel medyan, mesaj)
	// More — aynı serviste lag'i açık başka topic sayısı ("+N topic daha").
	More int `json:"more,omitempty"`
}

// MLConsumerLagSentence — SAF: consumer lag Problem'inin cümlesi (manşet;
// TEK TANIM). observed / baseline mesaj; more = lag'i açık diğer topic sayısı.
//
//	"checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936 (topic orders-topic)."
//	"checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936 (topic orders-topic, +2 topic daha)."
//	topic yoksa: "checkout · Kafka consumer lag: normalde ~5 mesaj, şimdi 17.936."
func MLConsumerLagSentence(service, topic string, observed, baseline float64, more int) string {
	if !finiteNonNeg(observed) {
		observed = 0
	}
	if !finiteNonNeg(baseline) {
		baseline = 0
	}
	s := service + " · Kafka consumer lag: normalde ~" + groupInt(baseline, '.') + " mesaj, şimdi " + groupInt(observed, '.')
	if topic = strings.TrimSpace(topic); topic != "" {
		s += " (topic " + topic
		if more > 0 {
			s += ", +" + strconv.Itoa(more) + " topic daha"
		}
		s += ")"
	}
	return s + "."
}

var mlLagRe = regexp.MustCompile(
	`^(.+) · Kafka consumer lag: normalde ~([0-9][0-9.]*) mesaj, şimdi ([0-9][0-9.]*)(?: \(topic (.+?)(?:, \+([0-9]+) topic daha)?\))?\.$`)

// trInt — SAF: "17.936" → 17936.
func trInt(s string) float64 {
	v, _ := strconv.ParseFloat(strings.ReplaceAll(s, ".", ""), 64)
	return v
}

// ParseMLConsumerLag — SAF: manşet TAM olarak MLConsumerLagSentence
// cümlesiyse gerçekler (servis cümlenin öznesi); değilse ok=false.
func ParseMLConsumerLag(headline string) (svc string, l StdLag, ok bool) {
	m := mlLagRe.FindStringSubmatch(headline)
	if m == nil {
		return "", StdLag{}, false
	}
	l = StdLag{Topic: m[4], Observed: trInt(m[3]), Baseline: trInt(m[2])}
	if m[5] != "" {
		l.More, _ = strconv.Atoi(m[5])
	}
	return m[1], l, true
}

// mlLagMsg — SAF: manşetin metin anahtarı (İngilizce arayüz); değilse nil.
// Sayılar dilden bağımsız biçimde (virgül binlik), EN şablonu aynen basar.
func mlLagMsg(headline string) *StdMsg {
	svc, l, ok := ParseMLConsumerLag(headline)
	if !ok {
		return nil
	}
	n, v := groupInt(l.Baseline, ','), groupInt(l.Observed, ',')
	switch {
	case l.Topic == "":
		return stdM("h.mlLag", "svc", svc, "n", n, "v", v)
	case l.More > 0:
		return stdM("h.mlLagTM", "svc", svc, "n", n, "v", v, "t", l.Topic, "more", strconv.Itoa(l.More))
	}
	return stdM("h.mlLagT", "svc", svc, "n", n, "v", v, "t", l.Topic)
}

// stdLagOf — SAF: ML Problem'inin açıklaması lag cümlesiyse gerçekler.
func stdLagOf(p Problem, k stdKindInfo) (StdLag, bool) {
	if k.kind != "anomaly_auto" {
		return StdLag{}, false
	}
	h, ok := MLPromotedHeadline(p)
	if !ok {
		return StdLag{}, false
	}
	_, l, ok := ParseMLConsumerLag(h)
	return l, ok
}

// lagImpactText — SAF: "Kim etkileniyor" ölçüsü (mesaj; topic adıyla).
func lagImpactText(l StdLag) (string, *StdMsg) {
	v, n := groupInt(l.Observed, '.'), groupInt(l.Baseline, '.')
	vEN, nEN := groupInt(l.Observed, ','), groupInt(l.Baseline, ',')
	if l.Topic == "" {
		return v + " mesaj geride (normalde ~" + n + ")", stdM("imp.lag", "v", vEN, "n", nEN)
	}
	return "topic " + l.Topic + ": " + v + " mesaj geride (normalde ~" + n + ")", stdM("imp.lagT", "t", l.Topic, "v", vEN, "n", nEN)
}

// lagNextStep — SAF: servisin Messaging sekmesi (topic süzgeciyle, ?ktopic).
func lagNextStep(p Problem, l StdLag) StdNextStep {
	q := url.Values{}
	q.Set("name", p.Service)
	q.Set("tab", "messaging")
	if l.Topic != "" {
		q.Set("ktopic", l.Topic)
	}
	return StdNextStep{Text: "Servisin Messaging sekmesinde lag'i aç", Href: "/service?" + q.Encode(), Msg: stdM("next.messaging")}
}

// lagEvidenceSentence — SAF: kanıt cümlesinin başı iç kapı dili ("Dedektör
// sürdürme koşulunu geçti") yerine etki kuralı; kuralın eşiği "Neden"
// satırlarında (impactThresholdLag). Kaynak parçası ve anahtarı aynen.
func lagEvidenceSentence(s string, m *StdMsg) (string, *StdMsg) {
	for _, base := range []string{"Dedektör sürdürme koşulunu geçti", "Tek ölçümde eşik aşıldı", "Kural eşiği aşıldı"} {
		if rest, ok := strings.CutPrefix(s, base); ok {
			s = "Lag etki kuralının eşiğini aştı" + rest
			break
		}
	}
	if m != nil {
		a := make(map[string]any, len(m.A)+1)
		for k, v := range m.A {
			a[k] = v
		}
		a["baseK"] = "ev.base.lag"
		m = &StdMsg{K: m.K, A: a}
	}
	return s, m
}
