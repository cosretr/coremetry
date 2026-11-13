package api

// ai_evalset_daily_test.go — v0.10.2109 (kalite döngüsü D4): günlük koşunun
// koşucu tarafı — yerel profil kapısı, kayıt (system:daily, v2), sürmekte
// olan koşuyla çakışma ve worker kapanışında "cancelled".

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/cosrelearn"
)

func TestEvalDailyNonLocalProfiles(t *testing.T) {
	local := func(id string) copilot.ModelProfile {
		return copilot.ModelProfile{ID: id, Provider: copilot.ProviderOpenAI, BaseURL: "http://10.0.0.5:8000/v1", LocalOnly: true}
	}
	cloud := func(id string) copilot.ModelProfile {
		return copilot.ModelProfile{ID: id, Provider: copilot.ProviderAnthropic}
	}
	unmarked := func(id string) copilot.ModelProfile {
		return copilot.ModelProfile{ID: id, Provider: copilot.ProviderOpenAI, BaseURL: "http://10.0.0.6:8000/v1"}
	}
	deep := func(p copilot.ModelProfile) copilot.ModelProfile { p.Deep = true; return p }
	for _, tc := range []struct {
		name     string
		profiles []copilot.ModelProfile
		def      string
		surface  map[string]string
		withDeep bool
		want     []string
	}{
		{"profil kümesi boş", nil, "default", nil, false, []string{"(profil kümesi boş)"}},
		{"tek yerel varsayılan", []copilot.ModelProfile{local("a")}, "a", nil, false, nil},
		{"varsayılan bulut", []copilot.ModelProfile{cloud("a")}, "a", nil, false, []string{"a"}},
		{"işaretsiz özel host da yerel sayılmaz", []copilot.ModelProfile{unmarked("a")}, "a", nil, false, []string{"a"}},
		{"yüzey haritasında bulut", []copilot.ModelProfile{local("a"), cloud("b")}, "a", map[string]string{"chat-intent": "b"}, false, []string{"b"}},
		{"ulaşılmayan bulut profili engel değil", []copilot.ModelProfile{local("a"), cloud("b")}, "a", nil, false, nil},
		{"bayat harita kimliği yok sayılır", []copilot.ModelProfile{local("a")}, "a", map[string]string{"chat": "gone"}, false, nil},
		{"Derin profil bulut, plan Derin içeriyor", []copilot.ModelProfile{local("a"), deep(cloud("d"))}, "a", nil, true, []string{"d"}},
		{"Derin profil bulut, plan Derin içermiyor", []copilot.ModelProfile{local("a"), deep(cloud("d"))}, "a", nil, false, nil},
		{"genel LLM adresi yerel işaretli olsa da reddedilir", []copilot.ModelProfile{{ID: "a", Provider: copilot.ProviderOpenAI, BaseURL: "https://api.openai.com/v1", LocalOnly: true}}, "a", nil, false, []string{"a"}},
		{"sıralı çıktı", []copilot.ModelProfile{cloud("z"), cloud("b"), local("a")}, "z", map[string]string{"x": "b", "y": "a"}, false, []string{"b", "z"}},
	} {
		got := evalDailyNonLocalProfiles(tc.profiles, tc.def, tc.surface, tc.withDeep)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: %v, istenen %v", tc.name, got, tc.want)
		}
	}
}

// markEvalHarnessLocal — harness'ın iki profilini (127.0.0.1 stub'ları) yerel işaretler.
func markEvalHarnessLocal(h *evalHarness, local bool) {
	h.s.copilot.SetProfiles([]copilot.ModelProfile{
		{ID: "big", Label: "Büyük", Provider: copilot.ProviderOpenAI, BaseURL: h.big.srv.URL, APIKey: "SECRET-BIG", Model: "gemma-big", LocalOnly: local},
		{ID: "small", Label: "Yerel qwen", Provider: copilot.ProviderOpenAI, BaseURL: h.small.srv.URL, APIKey: "SECRET-SMALL", Model: "qwen-small", LocalOnly: local},
	}, "big", map[string]string{"chat-intent": "small"})
}

func TestRunDailyEvalsetNonLocalSkipsWithoutRow(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	started, note, err := h.s.RunDailyEvalset(context.Background())
	if err != nil || started || !strings.Contains(note, "big") || !strings.Contains(note, "small") {
		t.Fatalf("yerel olmayan profil: started=%v note=%q err=%v", started, note, err)
	}
	if n := len(h.store.savesCopy()); n != 0 {
		t.Fatalf("atlanan koşu %d satır yazdı", n)
	}
	if h.big.hits.Load()+h.small.hits.Load() != 0 {
		t.Fatal("atlanan koşu modele istek attı")
	}
}

func TestRunDailyEvalsetRecordsDailyRun(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	markEvalHarnessLocal(h, true)
	started, note, err := h.s.RunDailyEvalset(context.Background())
	if err != nil || !started {
		t.Fatalf("koşu başlamadı: note=%q err=%v", note, err)
	}
	runs, _ := h.store.ListEvalRuns(context.Background(), 5)
	if len(runs) != 1 {
		t.Fatalf("%d koşu satırı", len(runs))
	}
	r := runs[0]
	if r.StartedBy != cosrelearn.EvalsetDailyStartedBy || r.Path != chstore.EvalRunPathV2 || r.Status != evalStatusDone ||
		r.Total != 4 || r.Done != 4 || r.Pass != 2 || r.Fail != 1 || r.Skipped != 1 {
		t.Fatalf("günlük koşu satırı: %+v", r)
	}
	if !cosrelearn.EvalsetDailyRanToday(runs, time.Now()) {
		t.Fatal("zamanlayıcı bu satırı 'bugün koştu' saymıyor")
	}
	select {
	case e := <-h.s.auditQ:
		if e.Action != "ai.evalset.run" || e.ActorID != "system" || !strings.Contains(e.Details, "daily=1") {
			t.Fatalf("audit: %+v", e)
		}
	default:
		t.Fatal("günlük koşu audit satırı yazmadı")
	}
}

func TestRunDailyEvalsetYieldsToRunningJob(t *testing.T) {
	h := newEvalHarness(t, false, evalTestCases())
	markEvalHarnessLocal(h, true)
	evalJobs.mu.Lock()
	evalJobs.running = &evalJob{row: chstore.EvalRun{ID: "ev-x", Status: evalStatusRunning}}
	evalJobs.mu.Unlock()
	started, note, err := h.s.RunDailyEvalset(context.Background())
	evalJobs.mu.Lock()
	evalJobs.running = nil
	evalJobs.mu.Unlock()
	if err != nil || started || !strings.Contains(note, "sürüyor") {
		t.Fatalf("süren koşuya rağmen: started=%v note=%q err=%v", started, note, err)
	}

	// Çapraz-pod: CH'de taze "running" satırı.
	_ = h.store.SaveEvalRun(context.Background(), chstore.EvalRun{ID: "ev-other", StartedAt: time.Now(), UpdatedAt: time.Now(), Status: evalStatusRunning})
	started, note, _ = h.s.RunDailyEvalset(context.Background())
	if started || !strings.Contains(note, "pod") {
		t.Fatalf("başka pod'un koşusuna rağmen: started=%v note=%q", started, note)
	}
}

func TestRunDailyEvalsetWorkerShutdownCancels(t *testing.T) {
	h := newEvalHarness(t, true, evalTestCases()) // stub cevabı bloklar
	markEvalHarnessLocal(h, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = h.s.RunDailyEvalset(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for h.big.hits.Load()+h.small.hits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("koşu modele ulaşmadı")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker kapanışı koşuyu durdurmadı")
	}
	runs, _ := h.store.ListEvalRuns(context.Background(), 1)
	if len(runs) != 1 || runs[0].Status != evalStatusCancelled {
		t.Fatalf("kapanışta durum: %+v", runs)
	}
}

func TestRunDailyEvalsetBudgetIsThirtyMinutes(t *testing.T) {
	if evalDailyBudget != 30*time.Minute {
		t.Fatalf("günlük koşu tavanı %s, operatör kararı 30 dk", evalDailyBudget)
	}
}
