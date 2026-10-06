package anomaly

// log_template_evidence.go — v0.10.1113 (operatör onaylı kuyruk maddesi "Log
// şablonu kök neden bağlantısı"): bir Problem başladığında, etkilenen
// servis(ler)de TAM o sırada doğan log şablonları — "bu başladığı anda yeni
// bir hata mesajı belirdi" ipucu.
//
// Neden dedektör DEĞİL, kanıt: `log_template_new` dedektörü v0.10.1061'den
// beri varsayılan KAPALI (tek başına problem olarak fazla gürültülü). Ama
// templater puller'ı `log_templates` defterini anahtardan bağımsız yazmayı
// sürdürüyor. Aynı defter, tek başına alarm üretmek yerine başka bir
// Problem'in başlangıcına bağlanınca gürültü olmaktan çıkar: pencere dar
// (başlangıçtan 10 dk önce → 5 dk sonra), servis kümesi küçük (özne + RCA
// şüphelileri, ≤5) ve sonuç bir alarm değil, detay sayfasında bir satır.
//
// "Yeni" kararı dedektörle AYNI işlevlerle verilir — çatal YOK:
// candidateTemplatesFilter (pencere + servis + üst sınırla daraltılır),
// newTemplateCandidatesMin, parseLogTemplates, knownTemplatesFilter,
// usableKnownTemplatesMin, filterNewTemplateFamilies (v0.10.1030 aile
// süzgeci: bilinen bir şablonun Drain-varyantı yeni sayılmaz, aynı aileden
// yalnız en erken doğan çıkar). TEK fark sayı tabanı: dedektörün ≥3'ü yerine
// aday ≥1, bilinen kümede taban yok (gerekçe logTemplateEvidenceCandidateMin).
//
// Okuma bütçesi: en çok İKİ ListLogTemplates çağrısı (aday + aday varsa
// bilinen), ikisi de FINAL + LIMIT ≤ 500 + max_execution_time 5
// (chstore.ListLogTemplates); servis kümesi boşsa HİÇ okuma yok. Bilinen
// okuması düşerse fail-closed (dedektörün yönü): varyantları "yeni" diye
// göstermek yerine hata döner, çağıran önbelleğe yazmaz.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

const (
	// LogTemplateEvidenceLookback / Lookahead — kanıt penceresi
	// [başlangıç − 10 dk, başlangıç + 5 dk). Geri bakış uzun: kök neden
	// belirtiden ÖNCE loglanır; ileri bakış puller'ın 5 dk'lık örnekleme
	// adımını ve başlangıç damgasının kova gecikmesini karşılar.
	LogTemplateEvidenceLookback  = 10 * time.Minute
	LogTemplateEvidenceLookahead = 5 * time.Minute
	// LogTemplateEvidenceMaxServices — tek okumada bakılan servis tavanı
	// (özne + RCA şüphelileri).
	LogTemplateEvidenceMaxServices = 5
	// LogTemplateEvidenceMaxRows — Problem başına gösterilen şablon tavanı.
	LogTemplateEvidenceMaxRows = 5
	// logTemplateEvidenceTemplateMax — gösterilen şablon metninin tavanı;
	// dedektörün çıktı kesimiyle (logTemplateAnomalyFrom) aynı.
	logTemplateEvidenceTemplateMax = 160
	// logTemplateEvidenceSampleMax — örnek satır (rune).
	logTemplateEvidenceSampleMax = 300
	// logTemplateEvidenceCandidateMin / KnownMin — kanıtın sayı tabanları
	// (dedektörün 3'ü DEĞİL). Puller Drain'i her tik sıfırlar ve satırın
	// total_count'unu o tikin örnek sayısıyla EZER: dedektör için "şu an ≥3"
	// doğru soru, ama geçmiş bir problemin kanıtı şablon sonradan seyrekleşince
	// 3'ün altına düşüp bölümden kaybolurdu. Aday tabanı 1 (aile süzgeci + ≤5
	// tavan küçük tutar); bilinen kümede taban YOK — daha geniş bilinen küme
	// yalnız daha çok varyantı gizler, güvenli yön.
	logTemplateEvidenceCandidateMin uint64 = 1
	logTemplateEvidenceKnownMin     uint64 = 0
)

// LogTemplateEvidence — problemin başlangıcı çevresinde doğmuş, gerçekten
// yeni bir aileye ait bir Drain şablonu.
type LogTemplateEvidence struct {
	TemplateID string `json:"templateId"`
	// Template — dedektörle aynı kesim (160 bayt, sözcük sınırı + "…").
	Template string `json:"template"`
	// Service — şablonun servislerinden kanıta bağlanan: özne varsa özne,
	// yoksa istenen servislerin sözlük sırasında en küçüğü.
	Service     string `json:"service"`
	FirstSeenNs int64  `json:"firstSeen"`
	LastSeenNs  int64  `json:"lastSeen"`
	// TotalCount — defterin sayacı: puller her tikte o tikin örnek sayısını
	// ÜZERİNE yazar (birikimli değil) — "kaç kez görüldü"nün kaba ölçüsü.
	TotalCount uint64 `json:"totalCount"`
	// OffsetSec — first_seen − problem başlangıcı, saniye (negatif = önce).
	OffsetSec int64 `json:"offsetSec"`
	// Query — /logs arama metni; KESİLMEMİŞ şablondan, Şablonlar
	// sekmesiyle aynı tek türetici (logstore.PatternSearchQuery).
	Query  string `json:"query"`
	Sample string `json:"sample,omitempty"`
}

// LogTemplateEvidenceServices — kanıt okumasının servis kümesi: özne önce,
// sonra ilgili servisler (RCA sırası), kırpılmış, tekrarsız, boşsuz,
// ≤ LogTemplateEvidenceMaxServices. SAF; sıra korunur.
func LogTemplateEvidenceServices(subject string, related []string) []string {
	out := make([]string, 0, LogTemplateEvidenceMaxServices)
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] || len(out) >= LogTemplateEvidenceMaxServices {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	add(subject)
	for _, s := range related {
		add(s)
	}
	return out
}

// LogTemplateEvidenceWindow — [from, to) unix ns; aday okumasının
// first_seen sınırları.
func LogTemplateEvidenceWindow(startedNs int64) (int64, int64) {
	return startedNs - int64(LogTemplateEvidenceLookback), startedNs + int64(LogTemplateEvidenceLookahead)
}

// LogTemplateEvidenceFor — dışa açık giriş (*chstore.Store ListLogTemplates
// ile karşılar); çekirdek logTemplateEvidence sahte depoyla tablo testli.
func LogTemplateEvidenceFor(ctx context.Context, store logTemplateLister, subject string, services []string, startedNs int64) ([]LogTemplateEvidence, error) {
	return logTemplateEvidence(ctx, store, subject, services, startedNs)
}

// evidenceCandidatesFilter — dedektörün aday süzgeci (candidateTemplatesFilter:
// en erken doğan önce, 500) + kanıtın daraltmaları: pencerenin üst sınırı,
// servis kümesi (hasAny) ve kanıtın sayı tabanı (1; dedektörün 3'ü değil).
// Servissiz şablonlar BİLEREK yok: bir problemin servisine bağlanamayan satır
// onun kanıtı değil.
func evidenceCandidatesFilter(services []string, fromNs, toNs int64) chstore.ListLogTemplatesFilter {
	f := candidateTemplatesFilter(fromNs)
	f.FirstSeenBeforeNs = toNs
	f.AnyServices = services
	f.MinTotalCount = logTemplateEvidenceCandidateMin
	return f
}

// evidenceKnownFilter — dedektörün bilinen süzgeci ("now" = pencere sonu, 7
// gün ufku, aynı servis / belirteç daraltması), sayı tabanı KALDIRILMIŞ (0 =
// SQL kısıtı yok).
func evidenceKnownFilter(cands []parsedLogTemplate, fromNs, toNs int64) chstore.ListLogTemplatesFilter {
	f := knownTemplatesFilter(cands, time.Unix(0, toNs), fromNs)
	f.MinTotalCount = logTemplateEvidenceKnownMin
	return f
}

func logTemplateEvidence(ctx context.Context, store logTemplateLister, subject string, services []string, startedNs int64) ([]LogTemplateEvidence, error) {
	services = LogTemplateEvidenceServices("", services)
	out := []LogTemplateEvidence{}
	if len(services) == 0 || startedNs <= 0 {
		return out, nil
	}
	fromNs, toNs := LogTemplateEvidenceWindow(startedNs)
	tmpls, err := store.ListLogTemplates(ctx, evidenceCandidatesFilter(services, fromNs, toNs))
	if err != nil {
		return nil, err
	}
	cands := parseLogTemplates(evidenceCandidates(tmpls, services, fromNs, toNs))
	if len(cands) == 0 {
		// Aday yoksa bilinen okuması da yok.
		return out, nil
	}
	// Bilinen = pencereden ÖNCE doğmuş, pencere sonundan geriye 7 günde
	// görülmüş şablonlar — dedektörün "now"u pencerenin sonu.
	known, err := store.ListLogTemplates(ctx, evidenceKnownFilter(cands, fromNs, toNs))
	if err != nil {
		// FAIL-CLOSED (dedektörle aynı yön): bilinen küme okunamadıysa aile
		// süzgeci koşamaz; süzülmemiş varyantları "yeni" diye göstermek
		// tam da v0.10.1030'un kapattığı gürültü olurdu.
		return nil, fmt.Errorf("known templates read failed, %d candidates held back (fail-closed): %w", len(cands), err)
	}
	emit, _ := filterNewTemplateFamilies(cands, parseLogTemplates(usableKnownTemplatesMin(known, fromNs, logTemplateEvidenceKnownMin)))
	for _, t := range emit {
		out = append(out, logTemplateEvidenceFrom(t, subject, services, startedNs))
	}
	rankLogTemplateEvidence(out)
	if len(out) > LogTemplateEvidenceMaxRows {
		out = out[:LogTemplateEvidenceMaxRows]
	}
	return out, nil
}

// evidenceCandidates — SQL süzgecinin Go emniyet kemeri: dedektörün aday
// kemeri (newTemplateCandidatesMin: first_seen ≥ from, kanıtın sayı tabanı) +
// pencerenin üst sınırı + şablonun istenen servislerden en az birini taşıması.
func evidenceCandidates(tmpls []chstore.LogTemplate, services []string, fromNs, toNs int64) []chstore.LogTemplate {
	in := newTemplateCandidatesMin(tmpls, fromNs, logTemplateEvidenceCandidateMin)
	out := in[:0]
	for _, t := range in {
		if t.FirstSeen >= toNs || evidenceServiceOf(t.Services, "", services) == "" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// evidenceServiceOf — şablonun servislerinden kanıta bağlanan: özne
// şablondaysa özne, değilse istenen servislerden şablonda olan sözlük
// sırasında en küçük (önbellek anahtarı kümeyi SIRALI taşır — sonuç istenen
// kümenin sırasına bağlı olmamalı). Kesişim yoksa "".
func evidenceServiceOf(tplServices []string, subject string, services []string) string {
	has := make(map[string]bool, len(tplServices))
	for _, s := range tplServices {
		has[s] = true
	}
	if subject != "" && has[subject] {
		return subject
	}
	best := ""
	for _, s := range services {
		if has[s] && (best == "" || s < best) {
			best = s
		}
	}
	return best
}

func logTemplateEvidenceFrom(t chstore.LogTemplate, subject string, services []string, startedNs int64) LogTemplateEvidence {
	return LogTemplateEvidence{
		TemplateID:  t.ID,
		Template:    truncTemplate(t.Template, logTemplateEvidenceTemplateMax),
		Service:     evidenceServiceOf(t.Services, subject, services),
		FirstSeenNs: t.FirstSeen,
		LastSeenNs:  t.LastSeen,
		TotalCount:  t.TotalCount,
		OffsetSec:   int64(math.Round(float64(t.FirstSeen-startedNs) / float64(time.Second))),
		Query:       logstore.PatternSearchQuery(t.Template),
		Sample:      truncSampleRunes(strings.TrimSpace(t.Sample), logTemplateEvidenceSampleMax),
	}
}

// rankLogTemplateEvidence — başlangıca en yakın önce (|offset|), eşitlikte
// sayı azalan, sonra kimlik (karıştırılmış girdi aynı sırayı versin).
func rankLogTemplateEvidence(in []LogTemplateEvidence) {
	abs := func(d int64) int64 {
		if d < 0 {
			return -d
		}
		return d
	}
	sort.SliceStable(in, func(i, j int) bool {
		di, dj := abs(in[i].OffsetSec), abs(in[j].OffsetSec)
		if di != dj {
			return di < dj
		}
		if in[i].TotalCount != in[j].TotalCount {
			return in[i].TotalCount > in[j].TotalCount
		}
		return in[i].TemplateID < in[j].TemplateID
	})
}
