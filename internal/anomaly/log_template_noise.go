package anomaly

// log_template_noise.go — v0.10.2028: log_template_new gürültü tabanı.
//
// Operator-reported: -batch adlı bir servis 30 dakikada milyonlarca satır
// yazarken, örneklemde 3 kez görülen yeni bir log kalıbı "anomali" diye
// açılıyordu; 24 saatte 133 olay. Kök neden: dedektörün tek tabanı
// newTemplateMinCount = 3 (şablonun TotalCount'u) idi; servis hacmine
// oran, batch kuralı ve seviye bakılmıyordu.
//
// Operatörün onayladığı kural: yeni kalıp, ancak anlamlı bir satır sayısına
// ya da servisin log hacmi içinde anlamlı bir paya ulaşınca olay sayılır;
// batch servislerde hiç sayılmaz (yalnız ERROR ve üstü seviye, aynı
// eşiklerle — gerçek hata sinyali körlenmez). Seviye bilinmiyorsa batch'te
// üretilmez. Sabitler burada, tek yerde; ayar alanı yok.
//
// Payda (servisin penceredeki log satırı) dedektörün elinde YOK: şablon
// defteri (log_templates) yalnız şablon başına toplam sayım taşır ve
// templater örnekleme tabanlıdır; payda için yeni bir ES sorgusu gerekir.
// Bu yüzden canlı yolda yalnız "≥ 20 satır" kuralı uygulanır (ServiceLines
// = 0 = bilinmiyor); pay kuralı saf fonksiyonda hazır ve sınanmış, payda
// eklendiğinde kendiliğinden devreye girer. v0.10.2029 — seviye süreç içi
// kayıttan (templater/severity.go: örneklemde çoğunluk ERROR+ ise "ERROR");
// batch serviste yalnız böyle bir kalıp ≥ 20 satırla olay üretir. Kayıtta
// yoksa (puller başka pod'da, ya da satırlar seviyesiz) üretilmez.
//
// Yalnız anomaly_events yazımını keser: şablon defteri (Logs "patterns")
// ve geçmiş olaylar değişmez.

import "strings"

const (
	newTemplateEventMinLines      = 20   // tek başına yeterli satır
	newTemplateEventMinShareLines = 5    // pay kuralının asgari satırı
	newTemplateEventMinShare      = 0.01 // servis hacminin payı
)

// newTemplateNoiseInput — kararın girdileri.
type newTemplateNoiseInput struct {
	Lines        uint64 // kalıbın görülen satır sayısı
	ServiceLines uint64 // servisin aynı penceredeki satırı; 0 = bilinmiyor
	Batch        bool   // servis batch kalıbına uyuyor
	Level        string // kalıbın seviyesi; "" = bilinmiyor
}

// newTemplateIsEvent — SAF karar (dosya başı kuralı).
func newTemplateIsEvent(in newTemplateNoiseInput) bool {
	if in.Batch && !isErrorOrAbove(in.Level) {
		return false
	}
	if in.Lines >= newTemplateEventMinLines {
		return true
	}
	return in.ServiceLines > 0 && in.Lines >= newTemplateEventMinShareLines &&
		float64(in.Lines)/float64(in.ServiceLines) >= newTemplateEventMinShare
}

// isErrorOrAbove — SAF: ERROR ve üstü seviye adları.
func isErrorOrAbove(level string) bool {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "error", "err", "fatal", "critical", "crit", "severe", "emerg", "emergency", "alert", "panic":
		return true
	}
	return false
}

// filterNewTemplateNoise — SAF: recorder adımı; taban altındaki adaylar
// düşer (yazılmaz), sayıları döner. Payda ve seviye canlı yolda yok.
//
// v0.10.2029 — levelOf: kalıbın örneklem seviyesi (templater.TemplateLevel;
// çoğunluk ERROR+ ise "ERROR", değilse ""). nil = seviye bilinmiyor. Batch
// olmayan serviste seviye karara girmez (davranış aynı).
func filterNewTemplateNoise(in []LogTemplateAnomaly, isBatch func(string) bool, levelOf func(templateID string) string) ([]LogTemplateAnomaly, int) {
	out := in[:0:0]
	cut := 0
	for _, a := range in {
		batch := isBatch != nil && isBatch(a.Service)
		level := ""
		if batch && levelOf != nil {
			level = levelOf(a.TemplateID)
		}
		if newTemplateIsEvent(newTemplateNoiseInput{Lines: a.TotalCount, Batch: batch, Level: level}) {
			out = append(out, a)
		} else {
			cut++
		}
	}
	return out, cut
}
