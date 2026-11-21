package sre

// playbook.go — CoSRE Faz B (araştırma kalitesi), B1: problem türü başına
// açık araştırma planı (New Relic "strategy per problem type" emsali).
//
// SAF, veri güdümlü tablo: problem türü → sıralı kontroller; her kontrol
// mevcut okuma adımlarına (kaynak = Step.Name) ve beslediği hipotez
// türlerine bağlanır. YENİ OKUYUCU YOK — planın eksik bıraktığı adımlar
// (log, kritik yol, node) aynı okuyucularla eklenir.
//
// Varsayılan AÇIK (yalnız işi yapılandırır). Değerlendirme varyantı
// "plain" (Investigation.Plain) planı kapatır — plan bayt bayt B öncesi.
// Kontrol sırası sunum / erişim sırasıdır; adımlar yine dalgalarda paralel.

import (
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

// Problem türleri (Investigation.Playbook.Type; FE aynası investigationQuality.ts).
const (
	PlaybookFailureRate = "failure_rate"
	PlaybookLatency     = "latency"
	PlaybookConsumerLag = "consumer_lag"
	PlaybookGeneric     = "generic"
)

// PlaybookCheck — oyun kitabının tek kontrolü.
type PlaybookCheck struct {
	ID    string `json:"id"`
	Label string `json:"label"` // TR (FE pb.check.<id> anahtarıyla çevirir)
	// Sources — kontrolün okuduğu adımlar (tercih sırası).
	Sources []string `json:"sources"`
	// Hypotheses — kontrolün beslediği hipotez türleri (kanıt kapısının
	// "eksik kaynak" önerisi buradan).
	Hypotheses []string `json:"hypotheses,omitempty"`
}

// Playbook — problem türünün sıralı kontrolleri.
type Playbook struct {
	Type   string          `json:"type"`
	Label  string          `json:"label"`
	Checks []PlaybookCheck `json:"checks"`
}

// playbookTable — tek kaynak (değişiklik = TestPlaybookTable satırı).
var playbookTable = []Playbook{
	{PlaybookFailureRate, "Hata oranı artışı", []PlaybookCheck{
		{"error_breakdown", "Hata kırılımı (endpoint / durum kodu)", []string{stepErrors, stepSweep}, []string{HypCodeRegression, HypDependency}},
		{"downstream_calls", "Aşağı akış çağrıları", []string{stepDeps, stepPivot}, []string{HypDependency, HypExternal, HypDBSlow}},
		{"recent_deploys", "Son deploy'lar", []string{stepDeploys}, []string{HypDeploy, HypConfig, HypCodeRegression}},
		{"dependency_errors", "Bağımlılık hataları", []string{stepEvents, stepDeps}, []string{HypDependency, HypExternal, HypDBSlow}},
		{"logs_exceptions", "Loglar / exception'lar", []string{stepLogSpike, stepErrors}, []string{HypCodeRegression, HypDeploy}},
	}},
	{PlaybookLatency, "Gecikme artışı", []PlaybookCheck{
		{"critical_path", "Kritik yol", []string{stepPivot}, []string{HypDBSlow, HypDependency, HypCodeRegression}},
		{"slow_dependency", "Yavaş bağımlılık / DB", []string{stepDeps, stepEvents}, []string{HypDBSlow, HypDependency, HypExternal}},
		{"saturation", "Doygunluk", []string{stepSweep, stepNode}, []string{HypSaturation, HypTrafficSurge}},
		{"recent_deploys", "Son deploy'lar", []string{stepDeploys}, []string{HypDeploy, HypConfig}},
	}},
	{PlaybookConsumerLag, "Tüketici gecikmesi (lag)", []PlaybookCheck{
		{"throughput_vs_rate", "Tüketici hızı ↔ üretici hızı", []string{stepSweep, stepDeps}, []string{HypTrafficSurge, HypSaturation}},
		{"consumer_errors", "Tüketici hataları / rebalance", []string{stepLogSpike, stepErrors}, []string{HypCodeRegression, HypDeploy}},
		{"downstream", "Aşağı akış", []string{stepDeps, stepEvents}, []string{HypDependency, HypDBSlow, HypExternal}},
		{"recent_deploys", "Son deploy'lar", []string{stepDeploys}, []string{HypDeploy, HypConfig}},
	}},
	{PlaybookGeneric, "Genel", []PlaybookCheck{
		{"service_sweep", "Servis taraması", []string{stepSweep}, []string{HypSaturation, HypTrafficSurge}},
		{"recent_deploys", "Son deploy'lar", []string{stepDeploys}, []string{HypDeploy, HypConfig}},
		{"dependencies", "Bağımlılıklar", []string{stepDeps, stepEvents}, []string{HypDependency, HypExternal, HypDBSlow}},
		{"logs", "Loglar", []string{stepLogSpike}, []string{HypCodeRegression}},
	}},
}

// PlaybookFor — SAF: tür → oyun kitabı (bilinmeyen → genel).
func PlaybookFor(typ string) Playbook {
	for _, pb := range playbookTable {
		if pb.Type == typ {
			return pb
		}
	}
	return playbookTable[len(playbookTable)-1]
}

// isLagMetric — SAF: tüketici gecikmesi ölçüsü mü (consumer_lag,
// kafka_lag_max, "*_lag"; "flag" gibi sözcük içi eşleşme değil).
func isLagMetric(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') }) {
		if tok == "lag" {
			return true
		}
	}
	return false
}

// ProblemTypeOf — SAF: problem + belirti → oyun kitabı türü. Lag önce
// (lag kuralı gecikme sözcükleri de taşıyabilir), sonra belirti.
func ProblemTypeOf(p chstore.Problem, symptom string) string {
	if isLagMetric(p.Metric) || isLagMetric(p.RuleName) || isLagMetric(p.RuleID) {
		return PlaybookConsumerLag
	}
	switch symptom {
	case symptomError:
		return PlaybookFailureRate
	case symptomLatency:
		return PlaybookLatency
	}
	return PlaybookGeneric
}

// playbookSources — SAF: oyun kitabının kaynakları (ilk görülme sırası, tekrarsız).
func playbookSources(pb Playbook) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range pb.Checks {
		for _, s := range c.Sources {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// playbookSymptom — eklenen pivot / log adımının belirtisi.
func playbookSymptom(typ, sym string) string {
	switch typ {
	case PlaybookFailureRate:
		return symptomError
	case PlaybookLatency:
		return symptomLatency
	}
	return sym
}

// applyPlaybook — SAF: planın eksik bıraktığı oyun kitabı kaynaklarını
// aynı okuyucularla ekler. Var olan adımlar DEĞİŞMEZ (sıra korunur, yeni
// adımlar sona). Hata özeti (errors) Context Pack okuyucusudur — paket
// kapalıyken EKLENMEZ (TestContextPackFlagOffRecordByteIdentical); kırılımı
// o zaman taramanın operasyon bulguları taşır, erişim beyanı "not_run" der. node dalga 3'tür → eklenmez, needNode=true döner
// (orkestratör tembel node kontrolünü zorlar). Servissiz problemde ekleme yok.
func applyPlaybook(specs []StepSpec, pb Playbook, p chstore.Problem, hyp *chstore.RootCauseHypothesis, in planInput) (out []StepSpec, needNode bool) {
	out = specs
	if p.Service == "" {
		return out, false
	}
	have := map[string]bool{}
	for _, s := range specs {
		have[s.Name] = true
	}
	from, to := in.Anchor.From, in.Anchor.To
	sym := playbookSymptom(pb.Type, symptomOf(anomaly.PlanFamilies(p)))
	for _, src := range playbookSources(pb) {
		if have[src] {
			continue
		}
		switch src {
		case stepNode:
			needNode = true
		case stepLogSpike:
			level := "all"
			if pb.Type == PlaybookFailureRate || pb.Type == PlaybookConsumerLag {
				level = "error"
			}
			out = append(out, StepSpec{Name: stepLogSpike, Wave: 2, Cap: capLogSpike, Spike: &mcptools.LogSpikeRequest{
				Service: p.Service, Env: in.Env, Level: level, SpikeFrom: time.Unix(0, from).UTC(), SpikeTo: time.Unix(0, to).UTC()}})
		case stepPivot:
			if pr, ex, ok := pickExemplar(p, hyp, sym, in); ok {
				out = append(out, StepSpec{Name: stepPivot, Wave: 2, Cap: capPivot, Pivot: &pr, Exemplar: ex})
			}
		}
		have[src] = true
	}
	return out, needNode
}
