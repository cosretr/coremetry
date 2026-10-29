package anomaly

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// prompt_golden_test.go — v0.9.518. SENARYO tabanlı prompt altın-kümesi.
//
// NE ÖLÇER, NE ÖLÇMEZ — bu ayrım önemli:
//   ÖLÇER: modele GİDEN girdiyi. Doğru sinyaller toplandı mı, denetim izi
//     doğru mu, uydurma yasağı yerinde mi, kanıt yokken prompt eski haline
//     dürüstçe düşüyor mu. Hepsi deterministik, model çağrısı GEREKTİRMEZ.
//   ÖLÇMEZ: modelin ÇIKTI kalitesini. Onun için ya insan değerlendirmesi
//     ya canlı bir koşum gerekir; ikisi de CI'da koşamaz.
//
// Yani bu küme "AI iyi cevap veriyor mu" sorusunu cevaplamaz; "AI'a iyi
// bir soru sorduk mu" sorusunu cevaplar. Kontrol edebildiğimiz yarı bu ve
// bugüne kadar hiç pinli değildi.

// senaryo — gerçekçi bir P1 şekli ve ondan beklenen prompt özellikleri.
type promptScenario struct {
	name      string
	problem   chstore.Problem
	deep      chstore.DeepEvidence
	wantIn    []string // prompt'ta MUTLAKA geçmeli
	wantNotIn []string // prompt'ta ASLA geçmemeli
}

func TestPromptGoldenScenarios(t *testing.T) {
	scenarios := []promptScenario{
		{
			name: "hata orani + exception + kanal kirilimi",
			problem: chstore.Problem{
				Service: "checkout", RuleName: "hata oranı", Metric: "error_rate",
				Severity: "critical", Value: 4.2, Threshold: 1,
			},
			deep: chstore.DeepEvidence{
				Checked: []chstore.CheckedSignal{
					{Family: "exceptions", Found: true, Detail: "3 exception grubu", Records: 3},
					{Family: "logs", Found: false, Detail: "bakıldı, kayıt yok"},
					{Family: "business", Found: true, Detail: "2 iş boyutu değeri", Records: 2},
				},
				Exceptions: []chstore.ExceptionGroup{{Type: "SocketTimeout", Message: "connect timed out", Occurrences: 412}},
				Business: map[string][]chstore.BusinessSlice{
					"CHANNEL_CODE": {{Value: "0012", Calls: 1200, Errors: 340, ErrPct: 28.3}},
				},
				CodeMeaning: map[string]string{"0012": "0012 - Mobil Kanal"},
			},
			wantIn: []string{
				"SORUŞTURMA",        // denetim izi başlığı
				"exceptions", "VAR", // bulunan sinyal
				"logs", "yok", // bulunmayan sinyal AÇIKÇA yok diyor
				"SocketTimeout",       // asıl kanıt
				"0012", "Mobil Kanal", // iş boyutu + RAG'dan çözülen anlam
				"KURAL", // uydurma yasağı
				"sebep olarak GÖSTERME",
			},
		},
		{
			name: "gecikme + heap doygunlugu",
			problem: chstore.Problem{
				Service: "payments", RuleName: "p99", Metric: "p99_ms",
				Severity: "critical", Value: 2100, Threshold: 800,
			},
			deep: chstore.DeepEvidence{
				Checked: []chstore.CheckedSignal{
					{Family: "saturation", Found: true, Detail: "2 pod heap örneği", Records: 2},
					{Family: "operations", Found: true, Detail: "5 operasyon", Records: 5},
				},
				Heap:    []chstore.CapacitySample{{Instance: "payments", Subkey: "pod-a", Usage: 94, Limit: 100}},
				SlowOps: []chstore.OperationSummary{{Name: "POST /pay", P99Ms: 1980, SpanCount: 5000}},
			},
			wantIn: []string{"SORUŞTURMA", "saturation", "pod-a", "POST /pay", "KURAL"},
		},
		{
			// EN ÖNEMLİ SENARYO: hiçbir sinyalde kanıt yok. Model burada
			// sebep uydurmaya en yatkın; prompt onu açıkça yasaklamalı.
			name: "hicbir sinyalde kanit yok",
			problem: chstore.Problem{
				Service: "ledger", RuleName: "hata oranı", Metric: "error_rate", Severity: "critical",
			},
			deep: chstore.DeepEvidence{
				Checked: []chstore.CheckedSignal{
					{Family: "exceptions", Found: false, Detail: "bakıldı, kayıt yok"},
					{Family: "logs", Found: false, Detail: "bakıldı, kayıt yok"},
				},
			},
			wantIn: []string{
				"SORUŞTURMA", "exceptions", "yok",
				"sebep uydurma", // yasak metni birebir orada
			},
		},
	}

	// v0.10.1577 (V1 dilim 2) — sentezleyicinin BubbleUp kanıtı.
	t0 := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	bu := func(status, errClass string) *chstore.BubbleUpEvidence {
		e := &chstore.BubbleUpEvidence{Mode: "errors", AnchorStartNs: t0.UnixNano(), FromNs: t0.UnixNano(),
			ToNs: t0.Add(10 * time.Minute).UnixNano(), ComputedNs: t0.Add(20 * time.Minute).UnixNano(),
			Attempts: 1, Status: status, Error: errClass}
		if status == chstore.BubbleUpStatusOK {
			e.SelectionTotal, e.BaselineTotal = 40, 900
			e.Attributes = []chstore.BubbleUpAttribute{{Key: "http.route", Values: []chstore.BubbleUpValue{
				{Value: "/v1/pay-now", SelectionCount: 32, BaselineCount: 99, SelectionPct: 0.8, BaselinePct: 0.11, Score: 0.69}}}}
		}
		return e
	}
	errProb := chstore.Problem{Service: "orders-api", RuleName: "hata oranı", Metric: "error_rate", Severity: "critical", Value: 4.2, Threshold: 1}
	scenarios = append(scenarios,
		promptScenario{
			name:    "BubbleUp yogunlasma + sorusturma",
			problem: errProb,
			deep: chstore.DeepEvidence{
				Checked:  []chstore.CheckedSignal{{Family: "exceptions", Found: false, Detail: "bakıldı, kayıt yok"}},
				BubbleUp: bu(chstore.BubbleUpStatusOK, ""),
			},
			wantIn: []string{
				"SORUŞTURMA", "Ayrışan boyutlar (BubbleUp)",
				"problemin açılışını izleyen 10 dk, hatalı 40 / tüm 900 span",
				"  - http.route=/v1/pay-now: hatalı kümede %80, tüm span'lerde %11\n",
				"tek başına sebep kanıtı değildir", "KURAL",
			},
			wantNotIn: []string{"OKUNAMADI", "%1,"},
		},
		promptScenario{
			name:      "BubbleUp okunamadi",
			problem:   errProb,
			deep:      chstore.DeepEvidence{Checked: []chstore.CheckedSignal{{Family: "logs", Found: false, Detail: "bakıldı"}}, BubbleUp: bu(chstore.BubbleUpStatusUnreadable, "timeout")},
			wantIn:    []string{"Ayrışan boyutlar (BubbleUp) OKUNAMADI (timeout)", "BİLİNMİYOR", "KURAL"},
			wantNotIn: []string{"belirgin ayrışma YOK", "http.route"},
		},
		promptScenario{
			name:    "BubbleUp zaman kiyasi (gecikme) — hata dili yok",
			problem: chstore.Problem{Service: "orders-api", RuleName: "p99", Metric: "p99_ms", Severity: "critical", Value: 2100, Threshold: 800},
			deep: func() chstore.DeepEvidence {
				e := bu(chstore.BubbleUpStatusOK, "")
				e.Mode = "window"
				return chstore.DeepEvidence{Checked: []chstore.CheckedSignal{{Family: "operations", Found: true, Detail: "1 operasyon", Records: 1}}, BubbleUp: e}
			}(),
			wantIn:    []string{"bu pencere ÖNCEKİ eş-boy pencereyle kıyaslandı; problemin açılışını izleyen 10 dk, bu pencere 40 / önceki 900 span", "bu pencerede %80, önceki pencerede %11"},
			wantNotIn: []string{"hatalı kümede", "hatalı span"},
		},
		promptScenario{
			name:      "yalniz BubbleUp (plan bos)",
			problem:   errProb,
			deep:      chstore.DeepEvidence{BubbleUp: bu(chstore.BubbleUpStatusOK, "")},
			wantIn:    []string{"\nAyrışan boyutlar (BubbleUp) — ", "http.route=/v1/pay-now", "KURAL", "sebep uydurma"},
			wantNotIn: []string{"SORUŞTURMA"},
		},
	)

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			bundle := EvidenceBundle{Problem: sc.problem, Deep: sc.deep}
			got := buildProblemPrompt(sc.problem, bundle, nil, time.UTC)
			for _, w := range sc.wantIn {
				if !strings.Contains(got, w) {
					t.Errorf("prompt %q içermiyor:\n%s", w, got)
				}
			}
			for _, w := range sc.wantNotIn {
				if strings.Contains(got, w) {
					t.Errorf("prompt %q içermemeli:\n%s", w, got)
				}
			}
		})
	}
}

// Derin kanıt YOKKEN prompt v0.9.510 ÖNCESİ haliyle bayt-aynı olmalı.
// P2/P3 problemlerin tamamı bu yoldan geçiyor; buraya sızacak bir
// "SORUŞTURMA" başlığı modele "bakıldı ama bulunamadı" izlenimi verir —
// oysa hiç bakılmadı. Yanlış izlenim, sessiz kalitesizlik.
func TestPromptUnchangedWithoutDeepEvidence(t *testing.T) {
	p := chstore.Problem{Service: "checkout", RuleName: "r", Metric: "error_rate", Severity: "warning"}

	withEmpty := buildProblemPrompt(p, EvidenceBundle{Problem: p}, nil, time.UTC)
	if strings.Contains(withEmpty, "SORUŞTURMA") {
		t.Errorf("derin kanıt yokken SORUŞTURMA bloğu basılmamalı:\n%s", withEmpty)
	}
	if strings.Contains(withEmpty, "KURAL") {
		t.Errorf("derin kanıt yokken uydurma yasağı da basılmamalı (bağlamsız kural gürültü):\n%s", withEmpty)
	}
}

// Denetim izi ile anlatım arasındaki ÇELİŞKİ yakalanabilir olmalı: iz
// "bakıldı, kayıt yok" derken prompt o sinyali kanıt gibi sunmamalı.
// Bu testin işi, izin bulunmayan sinyali "VAR" diye göstermediğini pinlemek.
func TestAuditTrailDoesNotClaimMissingSignals(t *testing.T) {
	var sb strings.Builder
	renderDeepEvidence(&sb, chstore.DeepEvidence{
		Checked: []chstore.CheckedSignal{
			{Family: "saturation", Found: false, Detail: "bakıldı, kayıt yok"},
		},
	})
	out := sb.String()
	if strings.Contains(out, "saturation: VAR") {
		t.Errorf("bulunmayan sinyal VAR olarak işaretlenmiş:\n%s", out)
	}
	if !strings.Contains(out, "saturation: yok") {
		t.Errorf("bulunmayan sinyal açıkça 'yok' demeli:\n%s", out)
	}
}
