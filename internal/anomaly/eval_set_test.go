//go:build evalset

package anomaly

// eval_set_test.go — v0.10.1793 (Davis F1.5): `make eval-anomaly`. Etiketli
// vaka dosyasını replay eder ve FP / recall tablosunu basar (CI DIŞI —
// `-tags evalset`).
//
//	COREMETRY_ANOMALY_EVAL=<vakalar.jsonl>          (yoksa sentetik fikstür)
//	COREMETRY_ANOMALY_EVAL_BASE_URL=https://coremetry (serisiz vakalar için canlı
//	COREMETRY_ANOMALY_EVAL_TOKEN=<admin API token>     anomaly_explain replay'i)
//	COREMETRY_ANOMALY_EVAL_OUT=<dosya.md>             (tabloyu yazar)

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// httpExplainReplayer — serisiz vakayı canlı anomaly_explain ucuyla replay
// eder (yalnız servis öznesi; ucu bugün servis × metrik).
type httpExplainReplayer struct {
	base, token string
	client      *http.Client
}

func (h httpExplainReplayer) Replay(ctx context.Context, c EvalCase) EvalOutcome {
	if h.base == "" {
		return EvalOutcome{CaseID: c.ID, Err: "canlı replay kapalı (COREMETRY_ANOMALY_EVAL_BASE_URL)"}
	}
	if c.SubjectKind != "" && c.SubjectKind != chstore.AnomalySubjectService {
		return EvalOutcome{CaseID: c.ID, Err: "özne türü " + c.SubjectKind + " için canlı replay ucu yok"}
	}
	q := url.Values{"service": {c.Subject}, "metric": {c.Metric},
		"from": {strconv.FormatInt(c.From, 10)}, "to": {strconv.FormatInt(c.To, 10)}}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(h.base, "/")+"/api/settings/anomaly-red-source/explain?"+q.Encode(), nil)
	if err != nil {
		return EvalOutcome{CaseID: c.ID, Err: err.Error()}
	}
	if h.token != "" {
		req.Header.Set("Authorization", "Bearer "+h.token)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return EvalOutcome{CaseID: c.ID, Err: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return EvalOutcome{CaseID: c.ID, Err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	var rep ExplainReport
	if err := json.NewDecoder(resp.Body).Decode(&rep); err != nil {
		return EvalOutcome{CaseID: c.ID, Err: err.Error()}
	}
	opened := rep.Problems > 0
	if c.Family == behaviorKind {
		opened = rep.BehaviorSteps > 0
	}
	return EvalOutcome{CaseID: c.ID, Opened: opened, Reason: rep.Verdict}
}

func TestAnomalyEvalSet(t *testing.T) {
	path := os.Getenv("COREMETRY_ANOMALY_EVAL")
	if path == "" {
		path = "testdata/eval/anomaly_cases.sample.jsonl"
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases, err := ParseEvalCases(f)
	if err != nil {
		t.Fatal(err)
	}
	rep := ChainReplayer{
		SeriesReplayer{Sens: chstore.DefaultAnomalySensitivity()},
		httpExplainReplayer{base: os.Getenv("COREMETRY_ANOMALY_EVAL_BASE_URL"), token: os.Getenv("COREMETRY_ANOMALY_EVAL_TOKEN"),
			client: &http.Client{Timeout: 60 * time.Second}},
	}
	outs := RunEval(context.Background(), cases, rep)
	for _, o := range outs {
		if o.Err != "" {
			t.Logf("ölçülemedi %s: %s", o.CaseID, o.Err)
		}
	}
	md := "Vaka dosyası: `" + path + "`\n\n" + RenderEvalMarkdown(ScoreEval(cases, outs))
	fmt.Println(md)
	if out := os.Getenv("COREMETRY_ANOMALY_EVAL_OUT"); out != "" {
		if err := os.WriteFile(out, []byte(md), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
