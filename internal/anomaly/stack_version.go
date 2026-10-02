package anomaly

import (
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/devops"
)

// stack_version.go — v0.10.1044 (operatör: "Kod, dalın ucundan değil çalışan
// sürümden okunsun").
//
// "Kodu da incele" kodu stack'i basan servisin deposundan okur; satır
// numaraları ancak o servisin KOŞAN sürümünün koduyla eşleşir. Sürüm ek bir
// okumayla bulunmaz: trace explain'in ve exception explain'in elindeki
// span'lerin / logun resource attribute'larından seçilir (frame linklerinin
// runningVersion zinciri: image tag → k8s image tag → service.version).

// StackVersion — SAF: stack'i taşıyan KAYDIN çalışan sürümü. Sıra:
//
//  1. kaydın kendi span'i (spanID, eldeki span'lerden) — stack'i basan pod;
//  2. kaydın kendi resource attribute'ları (log satırı; iki log arka ucu da
//     doldurur) — span trace'te yoksa;
//  3. çoğunluk: service'in bu trace'teki span'leri (StackServiceVersion) —
//     son çare.
//
// Neden çoğunluk ÖNCE değil: canary senaryosu. Yeni sürümdeki tek pod düşer,
// eski sürümdeki iki deneme başarılı olur; çoğunluk tam da patlayan sürümü
// kaybeder. Hiçbiri sürüm vermezse "" — çağıran kodu dal ucundan okur.
func StackVersion(spans []chstore.SpanRow, service, spanID string, logRes map[string]string) string {
	if v := SpanVersion(spans, spanID); v != "" {
		return v
	}
	if v := devops.RunningVersion(logRes); v != "" {
		return v
	}
	return StackServiceVersion(spans, service)
}

// SpanVersion — SAF: kimliği spanID olan span'in çalışan sürümü; span yoksa
// ya da sürüm taşımıyorsa "".
func SpanVersion(spans []chstore.SpanRow, spanID string) string {
	if spanID == "" {
		return ""
	}
	for _, sp := range spans {
		if sp.SpanID == spanID {
			return devops.RunningVersion(sp.ResourceAttributes)
		}
	}
	return ""
}

// StackServiceVersion — SAF: span'lerden YALNIZ service'in span'leri
// süzülür, devops.PickRunningVersion en sık çözülen sürümü seçer (eşitlikte
// en yeni span'inki, o da eşitse sözlük sırası). service boşsa ya da hiçbir
// span sürüm taşımıyorsa "". StackVersion'ın son çaresi.
func StackServiceVersion(spans []chstore.SpanRow, service string) string {
	if service == "" {
		return ""
	}
	samples := make([]devops.VersionSample, 0, len(spans))
	for _, sp := range spans {
		if sp.ServiceName != service {
			continue
		}
		samples = append(samples, devops.VersionSample{Resource: sp.ResourceAttributes, Start: sp.StartTime})
	}
	return devops.PickRunningVersion(samples)
}
