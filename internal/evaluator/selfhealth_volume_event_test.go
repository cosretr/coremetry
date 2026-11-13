package evaluator

// selfhealth_volume_event_test.go — v0.10.2118: "servis hacmi sıçradı"
// Problem değil ANOMALİ (ml_events, detector=svc_volume_spike).
//
// Operatör kararı (2026-11-13): Problem değil anomali; açılış bildirimi
// GÖNDERİLMEZ; koşul geçince kendiliğinden kapanma aynı. Eski yol her açılışta
// SendProblemAlert çağırıyordu. Pinlenen yaşam döngüsü (eski reconcile
// birebir): açılış / tazelemede started_at ve kimlik korunur / koşul geçince
// aynı satır stage=closed / taze ölçüm yoksa yazım yok / aile kapalı → hepsi
// closed / CH tohumu açık epizodu sürdürür. Kural kararı (volumeSpikeDecision)
// selfhealth_volume_test.go'da, dokunulmadı.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestVolumeEventFor(t *testing.T) {
	cfg := chstore.DefaultSelfHealth()
	start := time.Unix(1_800_000_000, 0)
	now := start.Add(3 * time.Hour)
	ev := volumeEventFor(chstore.ServiceVolume{Service: "orders-api", Cur: 1_200_000, Prev: 200_000}, cfg, start, now)

	wantID := chstore.MLEpisodeID(chstore.MLEventID(chstore.MLDetSvcVolumeSpike, "orders-api", ""), start)
	if ev.ID != wantID || ev.Detector != chstore.MLDetSvcVolumeSpike || ev.Signal != chstore.MLSignalTrace {
		t.Fatalf("kimlik/dedektör/sinyal: %+v", ev)
	}
	if ev.Stage != chstore.MLStageShadow || ev.Direction != "up" || ev.Service != "orders-api" {
		t.Fatalf("stage/yön/servis: %+v", ev)
	}
	if ev.StartedAt != start.UnixNano() || ev.LastSeen != now.UnixNano() {
		t.Fatalf("zaman: start=%d last=%d", ev.StartedAt, ev.LastSeen)
	}
	if ev.Baseline != 200_000 || ev.Observed != 1_200_000 || ev.Score != 6 {
		t.Fatalf("taban/gözlenen/skor: %v/%v/%v", ev.Baseline, ev.Observed, ev.Score)
	}
	// window_sec = tarama kadansı (3600): MLEventOpen 4 sa emniyet kapanışı.
	if ev.WindowSec != 3600 {
		t.Fatalf("window_sec = %d, 3600 bekleniyordu", ev.WindowSec)
	}
	var e volumeEvidence
	if err := json.Unmarshal([]byte(ev.Evidence), &e); err != nil {
		t.Fatal(err)
	}
	if e.Prev != 200_000 || e.Cur != 1_200_000 || e.Factor != 6 || e.Threshold != cfg.VolumeSpikeFactor ||
		e.MeasureWindowSec != 86400 || e.MinSpans != cfg.VolumeSpikeMinSpans {
		t.Fatalf("kanıt: %+v", e)
	}
	if !chstore.MLEventOpen(ev, now.Add(4*time.Hour).UnixNano()) {
		t.Fatal("4 sa içinde açık sayılmalı")
	}
}

func TestPlanVolumeEvents(t *testing.T) {
	cfg := chstore.DefaultSelfHealth()
	t0 := time.Unix(1_800_000_000, 0)
	spike := func(svc string, cur, prev uint64) chstore.ServiceVolume {
		return chstore.ServiceVolume{Service: svc, Cur: cur, Prev: prev}
	}

	// 1) Açılış: açık yok, iki sıçrama → iki yazım, start = now.
	w, open := planVolumeEvents(nil, []chstore.ServiceVolume{spike("b-svc", 900_000, 200_000), spike("a-svc", 1_000_000, 100_000)}, cfg, true, t0)
	if len(w) != 2 || w[0].Service != "a-svc" || w[1].Service != "b-svc" {
		t.Fatalf("açılış: %+v", w)
	}
	if w[0].StartedAt != t0.UnixNano() || len(open) != 2 {
		t.Fatalf("açılış start/açık küme: %+v %d", w[0], len(open))
	}
	idA := w[0].ID

	// 2) Önbellekli tik (taze ölçüm yok): yazım YOK, açık küme aynı.
	t1 := t0.Add(time.Minute)
	w, open = planVolumeEvents(open, []chstore.ServiceVolume{spike("a-svc", 1_000_000, 100_000), spike("b-svc", 900_000, 200_000)}, cfg, false, t1)
	if len(w) != 0 || len(open) != 2 {
		t.Fatalf("önbellekli tik yazdı: %+v", w)
	}

	// 3) Taze ölçüm: tazeleme — kimlik + started_at KORUNUR, değerler yeni.
	t2 := t0.Add(time.Hour)
	w, open = planVolumeEvents(open, []chstore.ServiceVolume{spike("a-svc", 1_500_000, 100_000), spike("b-svc", 900_000, 200_000)}, cfg, true, t2)
	if len(w) != 2 || w[0].ID != idA || w[0].StartedAt != t0.UnixNano() || w[0].LastSeen != t2.UnixNano() || w[0].Observed != 1_500_000 {
		t.Fatalf("tazeleme: %+v", w)
	}

	// 4) Koşul geçti (a-svc artık sıçramıyor): aynı satır closed, b sürer.
	t3 := t0.Add(2 * time.Hour)
	w, open = planVolumeEvents(open, []chstore.ServiceVolume{spike("b-svc", 900_000, 200_000)}, cfg, true, t3)
	var closed *chstore.MLEvent
	for i := range w {
		if w[i].Service == "a-svc" {
			closed = &w[i]
		}
	}
	if closed == nil || closed.ID != idA || closed.Stage != chstore.MLStageClosed || closed.LastSeen != t3.UnixNano() || closed.StartedAt != t0.UnixNano() {
		t.Fatalf("kapanış: %+v", closed)
	}
	if _, still := open["a-svc"]; still || len(open) != 1 {
		t.Fatalf("kapanan servis açık kümede kaldı: %v", open)
	}

	// 5) Aile kapalı / batch süzgeci (sıçrama listesi boş, önbellekli tik
	// bile): açık olan HEPSİ kapanır.
	w, open = planVolumeEvents(open, nil, cfg, false, t3.Add(time.Minute))
	if len(w) != 1 || w[0].Service != "b-svc" || w[0].Stage != chstore.MLStageClosed || len(open) != 0 {
		t.Fatalf("aile kapalı: %+v / %v", w, open)
	}

	// 6) Yeniden sıçrama = YENİ epizot (yeni kimlik).
	t4 := t3.Add(5 * time.Hour)
	w, _ = planVolumeEvents(open, []chstore.ServiceVolume{spike("a-svc", 1_000_000, 100_000)}, cfg, true, t4)
	if len(w) != 1 || w[0].ID == idA || w[0].StartedAt != t4.UnixNano() {
		t.Fatalf("yeni epizot eski kimliği taşıdı: %+v", w)
	}
}

func TestSeedVolumeOpen(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mk := func(id, svc, stage string, start, last time.Time) chstore.MLEvent {
		return chstore.MLEvent{ID: id, Detector: chstore.MLDetSvcVolumeSpike, Service: svc, Stage: stage,
			StartedAt: start.UnixNano(), LastSeen: last.UnixNano(), WindowSec: 3600}
	}
	evs := []chstore.MLEvent{
		mk("old", "a", chstore.MLStageShadow, now.Add(-30*time.Hour), now.Add(-20*time.Hour)), // bayat (> 4 sa)
		mk("cur", "a", chstore.MLStageShadow, now.Add(-10*time.Hour), now.Add(-time.Hour)),
		mk("done", "b", chstore.MLStageClosed, now.Add(-5*time.Hour), now.Add(-time.Hour)),
		{ID: "x", Detector: "op_latency", Service: "c", StartedAt: now.UnixNano(), LastSeen: now.UnixNano(), WindowSec: 300},
	}
	got := seedVolumeOpen(evs, now)
	if len(got) != 1 || got["a"].ID != "cur" {
		t.Fatalf("tohum: %+v", got)
	}
}

// Geçiş: eski açık Problem kapanırken not düşülür; tekrar uygulanınca çoğalmaz.
func TestVolumeMovedNote(t *testing.T) {
	got := volumeMovedNote("orders-api son 24 saatte …")
	if !strings.HasPrefix(got, "Anomali tarafına taşındı") || !strings.HasSuffix(got, "orders-api son 24 saatte …") {
		t.Fatalf("not: %q", got)
	}
	if volumeMovedNote(got) != got {
		t.Fatal("not iki kez eklendi")
	}
}
