//go:build evalset

// evalset_test.go — v0.10.422 (CoSRE denetimi E1/E7): donmuş vakaları
// YEREL modele karşı yeniden oynatır ve davranışı skorlar. CI DIŞI —
// bilinçli (denetim: "yerel model yanı başında"). Koşum:
//
//	COREMETRY_EVAL_BASE_URL=http://localhost:11434/v1 \
//	COREMETRY_EVAL_MODEL=qwen3:8b \
//	go test -tags evalset ./internal/api/ -run TestEvalsetReplay -v
//
// Üretim COREMETRY_AI_* değişkenleri OKUNMAZ: evalset bir geliştiricinin
// gerçek anahtarına asla ateşlenmez. Kayıt bellek içi (ai_calls satırı
// yok). Gecikme METRİK (soğuk model 60 sn+ olabilir): aşım uyarı, kırmızı
// değil. Altbilgi prompt_version + model taşır; onsuz yeşil koşum hiçbir
// şey söylemez (prompt değişince eski skor kıyaslanamaz).
//
// v0.10.940 — aynı vakalar Settings › AI › Değerlendirme panelinden SUNUCUDA
// da koşar (ai_evalset_runs.go; üretim profili, ai_calls'a "evalset-" önekli
// satır). Bu CLI geliştirici yolu olarak kalır; vaka adımları ortak
// (runEvalsetCase), fikstür gömülü kümeden (loadEvalsetCases).

package api

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/copilot"
)

type evalRecorder struct {
	mu   sync.Mutex
	recs []copilot.CallRecord
}

func (r *evalRecorder) RecordCall(_ context.Context, c copilot.CallRecord) {
	r.mu.Lock()
	r.recs = append(r.recs, c)
	r.mu.Unlock()
}

func TestEvalsetReplay(t *testing.T) {
	base := os.Getenv("COREMETRY_EVAL_BASE_URL")
	model := os.Getenv("COREMETRY_EVAL_MODEL")
	if base == "" || model == "" {
		t.Skip("COREMETRY_EVAL_BASE_URL / COREMETRY_EVAL_MODEL yok — evalset atlandı")
	}
	provider := os.Getenv("COREMETRY_EVAL_PROVIDER")
	if provider == "" {
		provider = copilot.ProviderOpenAI
	}
	key := os.Getenv("COREMETRY_EVAL_API_KEY")

	svc := copilot.New(provider, key, model)
	svc.Configure(provider, key, model, base, false, true)
	temp := 0.0
	svc.ConfigureTuning(0, &temp, 120) // sıfır sıcaklık: yeniden oynatım kararlılığı
	rec := &evalRecorder{}
	svc.SetRecorder(rec)

	cases := loadEvalset(t)
	pass, fail, skipped := 0, 0, 0
	var results []evalrubric.CaseResult // v0.10.666 — Faz A rubrik + artefakt
	// v0.10.940 — vaka yolu panelle ORTAK (runEvalsetCase, ai_evalset_core.go);
	// burada yalnız çağrının kendisi: özel Service + sıfır sıcaklık, bellek
	// içi kayıt. Şema adı üretim etiketi (chat-intent, rootcause-verdict…) —
	// üretim gövdesiyle aynı; NLToQuery/CHQueryOptimize artık üretim şemasıyla.
	call := func(ctx context.Context, surface, system, user string, schema map[string]any, json bool) (string, error) {
		ctx = copilot.WithMeta(ctx, copilot.CallMeta{Surface: evalsetSurfacePrefix + surface, UserID: "evalset", Shield: aiShield})
		if json {
			ctx = copilot.WithJSONMode(ctx)
			if len(schema) > 0 {
				ctx = copilot.WithJSONSchema(ctx, evalProductionLabel(surface), schema)
			}
		}
		return svc.Explain(ctx, system, user)
	}
	t.Logf("id\tsurface\tok\tlatency_ms\tunknown_entities\trubric\tfails")
	for _, c := range cases {
		o := runEvalsetCase(context.Background(), call, c)
		if o.Skipped { // v0.10.431
			skipped++
			t.Logf("%s\t%s\tSKIP\t-\t-\t%s", c.ID, c.Surface, o.SkipReason)
			continue
		}
		okS := "ok"
		if len(o.Fails) > 0 {
			okS = "FAIL"
			fail++
		} else {
			pass++
		}
		results = append(results, evalrubric.CaseResult{ID: c.ID, Surface: c.Surface, OK: len(o.Fails) == 0, LatencyMs: o.LatencyMs, Fails: o.Fails, Rubric: o.Rubric})
		t.Logf("%s\t%s\t%s\t%d\t%d\t%.2f\t%s", c.ID, c.Surface, okS, o.LatencyMs, o.Unknown, o.Rubric.Total, strings.Join(o.Fails, " | "))
		// v0.10.940 — fikstür hatası (bilinmeyen yüzey / bozuk hipotez) artık
		// koşuyu t.Fatalf ile DÜŞÜRMEZ: "fixture: …" ihlaliyle kırmızı vaka.
		if len(o.Fails) > 0 {
			t.Errorf("%s (%s): %s\n  why: %s\n  answer: %s", c.ID, c.Surface, strings.Join(o.Fails, "; "), c.Why, strings.TrimSpace(o.Answer))
		}
		if c.Expect.MaxLatencyMs > 0 && o.LatencyMs > int64(c.Expect.MaxLatencyMs) {
			t.Logf("  ⚠ %s gecikme %d ms > %d ms (metrik, kırmızı değil)", c.ID, o.LatencyMs, c.Expect.MaxLatencyMs)
		}
	}
	// Kayıt sayacı ile skorlayıcı aynı tanımı paylaşır (E6): ShieldHits
	// toplamı, skorlayıcının uydurma sayımından bağımsız bir çapraz kontrol.
	time.Sleep(200 * time.Millisecond)
	rec.mu.Lock()
	var shield uint64
	for _, r := range rec.recs {
		shield += uint64(r.ShieldHits)
	}
	n := len(rec.recs)
	rec.mu.Unlock()
	sum := evalrubric.Summarize(results, skipped)
	t.Logf("prompt_version=%s model=%s n=%d pass=%d fail=%d skipped=%d recorded=%d shield_hits_total=%d rubric_mean=%.3f below_threshold=%d",
		copilot.PromptVersion(), model, len(cases), pass, fail, skipped, n, shield, sum.RubricMean, sum.BelowThr)
	// v0.10.666 — koşum artefaktı (Faz A skor geçmişi): COREMETRY_EVAL_OUT dizini
	// verildiyse JSON yazılır; iki koşum `go run ./cmd/evalsetdiff` ile kıyaslanır.
	// ai_calls'a DEĞİL: replay CH'ye bağımlı olmamalı (başlıktaki sözleşme).
	if out := os.Getenv("COREMETRY_EVAL_OUT"); out != "" {
		run := evalrubric.Run{Schema: evalrubric.RunSchema, At: time.Now(), PromptVersion: copilot.PromptVersion(), Model: model, Provider: provider, Cases: results, Summary: sum}
		if p, err := evalrubric.WriteRun(out, run); err != nil {
			t.Errorf("koşum artefaktı yazılamadı: %v", err)
		} else {
			t.Logf("koşum artefaktı: %s", p)
		}
	}
}
