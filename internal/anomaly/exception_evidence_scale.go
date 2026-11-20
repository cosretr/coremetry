package anomaly

// exception_evidence_scale.go — v0.10.2384: exception açıklama girdisinin
// (BuildExceptionExplainInput) KENDİ kanıt tavanları çağrının kanıt ölçeğiyle
// büyür. v0.10.2378 yalnız çekmecenin dış kelepçesini ölçekliyordu; girdi
// kurucusu stack'i 1800 rune'da, mesajı 400'de, log satırını 500'de kesmeye
// devam ediyordu.
//
// Ölçek AÇIK PARAMETREDİR (BuildExceptionExplainInputScaled; global ya da
// ayar yok): api çağrının profilindeki çarpanı (1…8) verir. Ölçek 1 = bugünkü
// sabitler, girdi bayt bayt eski. Arka plan açıklayıcı (ExceptionExplainer)
// ve Oracle grubu kurucusu ölçek 1'de kalır.
//
// Büyüyen arka uç okumaları (hepsi zaman-sınırlı, LIMIT'li, zaman aşımlı):
// trace logları LIMIT 30 → 30 × ölçek (tek trace, span zarfı ±1 dk, 6 sn);
// örnek trace logları LIMIT 60 → 60 × ölçek (≤3 trace, damga ±2 dk, 6 sn).
// Trace okuması (GetTrace nokta okuması) aynı.

import "fmt"

// Taban tavanlar (ölçek 1). Sayılar bu dilimden ÖNCEKİ satır içi sabitlerdir.
const (
	exStackPromptRunes = 1800 // temsilî stack'in prompt kopyası
	exGroupMsgRunes    = 400  // grup mesajı (meta)
	exLogStackRunes    = 900  // log satırının stack kopyası
	exLogBodyRunes     = 500  // trace log satırı gövdesi
	exDBStatementRunes = 600  // hata span'inin SQL ifadesi
	// exLogsBlockBytes / exSampleLogsBlockBytes — YALNIZ ölçekli yolda: blokların
	// toplam gövde + stack baytı (satır sayısı × satır boyu karesel büyümesin).
	// Ölçek 1'de bütçe uygulanmaz (bayt bayt eski).
	exLogsBlockBytes       = 24000
	exSampleLogsBlockBytes = 12000
	exEvidenceScaleMax     = 8
)

// exLogsOmittedFmt — ölçekli yolda yazılmayan log satırlarının notu (%d: okunan
// satır, verilen satır).
const exLogsOmittedFmt = "\n(Okunan %d log satırının en ciddi %d tanesi verildi; kalanı bütçe nedeniyle yazılmadı — eksik satırlara dayanarak sayım yapma.)"

// exLimits — exception girdisinin kanıt tavanları.
type exLimits struct {
	scale                                           int
	stackRunes, msgRunes                            int
	traceSpans, traceErrSpans, dbStmtRunes          int
	logFetch, logLines, logBodyRunes, logStackRunes int
	sampleFetch, samplePerTrace, sampleBodyRunes    int
	logsBlockBytes, sampleBlockBytes                int // 0 = bütçe yok (ölçek 1)
}

// exLimitsFor — SAF: ölçek → tavanlar. Ölçek ≤ 1 = bugünkü sabitler.
func exLimitsFor(scale int) exLimits {
	scale = min(max(scale, 1), exEvidenceScaleMax)
	l := exLimits{
		scale:      scale,
		stackRunes: exStackPromptRunes * scale, msgRunes: exGroupMsgRunes * scale,
		traceSpans: traceEvidenceMaxSpans * scale, traceErrSpans: traceEvidenceErrSpans * scale, dbStmtRunes: exDBStatementRunes * scale,
		logFetch: traceLogsFetch * scale, logLines: traceLogsLines * scale, logBodyRunes: exLogBodyRunes * scale, logStackRunes: exLogStackRunes * scale,
		sampleFetch: sampleLogFetch * scale, samplePerTrace: sampleLogPerTrace * scale, sampleBodyRunes: sampleLogBodyRunes * scale,
	}
	if scale > 1 {
		l.logsBlockBytes, l.sampleBlockBytes = exLogsBlockBytes*scale, exSampleLogsBlockBytes*scale
	}
	return l
}

// logsOmittedNote — SAF: ölçekli yolda bütçe / satır tavanı yüzünden yazılmayan
// satır varsa not; ölçek 1'de hep "" (bayt bayt eski).
func (l exLimits) logsOmittedNote(read, written int) string {
	if l.scale <= 1 || written >= read {
		return ""
	}
	return fmt.Sprintf(exLogsOmittedFmt, read, written)
}
