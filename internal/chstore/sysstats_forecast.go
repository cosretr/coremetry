package chstore

import "strings"

// sysstats_forecast.go — v0.10.901 (Dynatrace paritesi #6, dilim 2; spec
// Onay 2026-09-23): /admin/stats disk satırına "kaç gün kaldı" chip'i.
//
// KAYNAK: yeni rota yok, polling yok, hesap yok. Evaluator (yalnız liderde,
// bellek-içi 6 saatlik seri) zaten self-disk-eta problemini yazıyor;
// Problem.Value = gün (evaluator/selfhealth.go). Sayı problems tablosunda
// yaşıyor → OpenProblemsSnapshot (5 s memo) her pod'dan okur; zarf zaten
// 60 s serveCached. Chip yalnız ÇÖZÜLMEMİŞ satır varken görünür — "açık"
// bu projede open|acknowledged demektir (snapshot sözleşmesi; ack edilmiş
// disk yine dolar). Eşik SelfHealthConfig.DiskEtaDays (varsayılan 7 gün):
// "chip yok" = "7 günden uzak ya da tahmin yok" — panel alt yazısı bunu
// söyler. Sayı satırda yoksa yazılmaz (R² satırda yok → chip'te R² yok;
// dilim 3+).
//
// TAZELİK: sayı değerlendiricinin son tikinden gelir. Değerlendirici
// durursa satır donar (süpürme de değerlendiricinin içinde koşar) — FE bu
// yüzden rozeti /api/evaluator/health ile birlikte okur ve değerlendirici
// sessizken rozeti soluklaştırıp "N dk sessiz" yazar; buraya damga konmadı,
// kalp atışı zaten var (v0.9.550).
//
// ANAHTAR UYUŞMAZLIĞI (jüri bulgusu, koddan doğrulandı): CollectDisks her
// iki dalda hostName() seçer → problem id her zaman "self-disk-eta:<host>/
// <disk>". sysstats ise tek düğümde host='' okur → DiskKey("", disk) =
// "<disk>". Tam eşleşme kümede çalışır; tek düğümde SON-EK eşlemesi
// ("/<disk>") gerekir ve yalnız TEK aday varsa bağlanır — iki aday
// (olağan dışı) belirsizdir, chip dürüstçe çıkmaz.

// SelfDiskRuleID — evaluator'ın self-disk-eta kural kimliği; problem id
// "SelfDiskRuleID:" + DiskKey(host, disk). Yazan (evaluator) ve okuyan
// (burası, runbook haritası, kategori) aynı sabiti kullanır.
const SelfDiskRuleID = "self-disk-eta"

// SelfDiskCriticalDays — bunun altındaki koşu payı kritiktir (evaluator
// selfDiskCriticalDays ile aynı sayı). Rozet TONU bu sayıdan türer, satırın
// Severity'sinden DEĞİL: Severity yaş eskalasyonuyla 30 dk sonra critical'a
// çıkar (Inbox sözleşmesi) — "kırmızı = 2 günden az" cümlesi ancak böyle
// doğru kalır.
const SelfDiskCriticalDays = 2.0

// DiskForecast — DiskStat.Forecast; JSON alanı yalnız satır varken.
type DiskForecast struct {
	// Days — kalan gün (Problem.Value; <1 gün ise FE saat yazar; 0 =
	// projeksiyon zaten tavanda, FE "dolu" yazar).
	Days float64 `json:"days"`
	// Critical — Days < SelfDiskCriticalDays (rozet tonu).
	Critical bool `json:"critical"`
	// Severity — satırın ANLIK ciddiyeti, yaş eskalasyonu dâhil (Inbox ile
	// aynı); rozet tonu için Critical'a bakılır.
	Severity string `json:"severity"`
	// ThresholdDays — problemin açıldığı eşik (SelfHealthConfig.DiskEtaDays).
	ThresholdDays float64 `json:"thresholdDays,omitempty"`
	// Note — diskReason cümlesi (tooltip): "… diski 9 saat içinde DOLACAK …".
	Note string `json:"note,omitempty"`
	// ProblemID — Inbox'a köprü (/problems?problem=<id>).
	ProblemID string `json:"problemId"`
}

// attachDiskForecast — SAF (tablo-testli). open: snapshot'ın çözülmemiş
// problem listesi (yalnız SelfDiskRuleID satırları dikkate alınır).
func attachDiskForecast(disks []DiskStat, open []*Problem) {
	if len(disks) == 0 || len(open) == 0 {
		return
	}
	byID := make(map[string]*Problem, 4)
	for _, p := range open {
		if p == nil || p.RuleID != SelfDiskRuleID {
			continue
		}
		byID[p.ID] = p
	}
	if len(byID) == 0 {
		return
	}
	for i := range disks {
		d := &disks[i]
		p := byID[SelfDiskRuleID+":"+DiskKey(d.Host, d.Name)]
		if p == nil && d.Host == "" {
			p = diskSuffixMatch(byID, "/"+d.Name)
		}
		if p == nil {
			continue
		}
		d.Forecast = &DiskForecast{
			Days:          p.Value,
			Critical:      p.Value < SelfDiskCriticalDays,
			Severity:      p.Severity,
			ThresholdDays: p.Threshold,
			Note:          p.Description,
			ProblemID:     p.ID,
		}
	}
}

// diskSuffixMatch — tek düğüm: "self-disk-eta:<host>/<disk>" içinde
// "/<disk>" son-eki; yalnız TEK aday varsa döner.
func diskSuffixMatch(byID map[string]*Problem, suffix string) *Problem {
	var found *Problem
	for id, p := range byID {
		if !strings.HasSuffix(id, suffix) {
			continue
		}
		if found != nil {
			return nil // belirsiz
		}
		found = p
	}
	return found
}
