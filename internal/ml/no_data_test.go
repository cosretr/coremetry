package ml

// v0.10.2359 — veri gelmediğinde açık olay "banda döndü" kapanmaz: aile kör
// tikte kaçırma sayılmaz, noDataHoldMax sonra endReason=no_data kapanır ve
// ML Problem'i "bitti" kapanışından çıkar. Saat/zaman dilimi bağımsız.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// closeStaleInBand — her aile veri değerlendirdi (eski davranış).
func (t *tracker) closeStaleInBand(now time.Time) []chstore.MLEvent {
	c, _ := t.closeStale(now, tickCoverage{})
	return c
}

func noDataFixture(t *testing.T) (*tracker, time.Time, time.Duration, string) {
	t.Helper()
	tr, tick, t0 := newTracker(), 5*time.Minute, time.Unix(1_800_000_000, 0).UTC()
	ev := chstore.MLEvent{ID: chstore.MLEventID("op_latency", "pay", "GET /x"), Detector: "op_latency", Service: "pay", Signal: "trace", Evidence: `{"dwell":3}`}
	_, ep := tr.observe(ev, t0, tick)
	ev.ID = ep
	tr.setEvent(ev)
	return tr, t0, tick, ep
}

func blindCov(blind bool) tickCoverage {
	return tickCoverage{classOf: map[string]string{"op_latency": "trace_red"}, fam: map[string]famCoverage{"trace_red": {blind: blind}}}
}

func TestNoDataKeepsEventOpenThenEndsWithReason(t *testing.T) {
	tr, t0, tick, ep := noDataFixture(t)
	var heldRows int
	n := int(noDataHoldMax / tick)
	for i := 1; i < n; i++ {
		closing, held := tr.closeStale(t0.Add(time.Duration(i)*tick), blindCov(true))
		if len(closing) != 0 {
			t.Fatalf("tik %d: veri yokken kapandı: %+v", i, closing)
		}
		heldRows += len(held)
		if len(held) == 1 {
			if s, _ := NoDataOf(held[0].Evidence); s != t0.Add(tick).Unix() {
				t.Fatalf("noDataSince: %d", s)
			}
		}
	}
	if heldRows != 1 {
		t.Fatalf("tutma satırı bir kez yazılır, %d", heldRows)
	}
	if !tr.openIDs(nil)[ep] {
		t.Fatal("tutulan koşu açık (grup bitmez)")
	}
	if len(tr.noDataServices()) != 0 {
		t.Fatal("açıkken Problem tutması yok")
	}
	closing, _ := tr.closeStale(t0.Add(time.Duration(n)*tick), blindCov(true))
	if len(closing) != 1 || closing[0].Stage != chstore.MLStageClosed {
		t.Fatalf("sınırda kapanmalı: %+v", closing)
	}
	if _, ended := NoDataOf(closing[0].Evidence); !ended {
		t.Fatalf("endReason yok: %s", closing[0].Evidence)
	}
	if !strings.Contains(closing[0].Evidence, `"dwell":3`) {
		t.Fatal("dedektör kanıtı korunur")
	}
	// Problem: "veri gelmedi" servisinin kapanışı plandan çıkar (bayat süpürme yolu).
	silent := tr.noDataServices()
	if !silent["pay"] {
		t.Fatalf("servis sessiz değil: %v", silent)
	}
	res := []chstore.Problem{{ID: "p1", Service: "pay"}, {ID: "p2", Service: "cart"}, {ID: "p3", Service: "pay"}}
	got := dropNoDataResolves(res, map[string]bool{"p3": true}, silent)
	if len(got) != 2 || got[0].ID != "p2" || got[1].ID != "p3" {
		t.Fatalf("pay kapanmaz (ikiz hariç): %+v", got)
	}
}

func TestBackInBandClosesAsBefore(t *testing.T) {
	tr, t0, tick, ep := noDataFixture(t)
	var closing []chstore.MLEvent
	for i := 1; i <= episodeCloseMisses; i++ {
		closing, _ = tr.closeStale(t0.Add(time.Duration(i)*tick), blindCov(false))
	}
	if len(closing) != 1 || closing[0].ID != ep {
		t.Fatalf("3 değerlendirilmiş tik sonra kapanır: %+v", closing)
	}
	if _, ended := NoDataOf(closing[0].Evidence); ended {
		t.Fatal("banda dönen olayda endReason yok")
	}
	if len(tr.noDataServices()) != 0 {
		t.Fatal("banda dönen olay Problem'i tutmaz")
	}
}

func TestErrorTickIsNotBackInBand(t *testing.T) {
	vmErr := errors.New("victoriametrics: 503")
	if !familyBlind(vmErr, false, false, 5) || !familyBlind(nil, true, false, 0) || !familyBlind(nil, false, true, 0) {
		t.Fatal("hata / backoff atlaması / boş kova = kör")
	}
	if familyBlind(nil, false, true, 4) || familyBlind(nil, false, false, 0) {
		t.Fatal("değerlendirilen tik kör değil")
	}
	tr, t0, tick, _ := noDataFixture(t)
	// 2 bant içi, araya hata tiki, 1 bant içi daha → ancak 3. değerlendirilmiş tikte kapanır.
	seq := []bool{false, false, true, false}
	for i, blind := range seq {
		closing, _ := tr.closeStale(t0.Add(time.Duration(i+1)*tick), blindCov(blind))
		if want := i == len(seq)-1; (len(closing) == 1) != want {
			t.Fatalf("tik %d: kapanış %v, beklenen %v", i+1, len(closing), want)
		}
	}
	// Veri tutma içinde dönerse aynı epizot sürer (yeni kimlik açılmaz).
	tr2, t2, tick2, ep2 := noDataFixture(t)
	for i := 1; i < 24; i++ { // 2 sa kör tik
		tr2.closeStale(t2.Add(time.Duration(i)*tick2), blindCov(true))
	}
	if _, ep := tr2.observe(chstore.MLEvent{ID: chstore.MLEventID("op_latency", "pay", "GET /x")}, t2.Add(2*time.Hour), tick2); ep != ep2 {
		t.Fatal("tutma içinde geri gelen aynı epizot")
	}
}

func TestCardNoDataWording(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	in := CardInput{Stage: chstore.MLStageShadow, StartedAt: now.Add(-2 * time.Hour), LastSeen: now.Add(-time.Hour), BucketSec: 300, Now: now, NoDataSince: now.Add(-50 * time.Minute).Unix()}
	if !cardOpen(in) {
		t.Fatal("veri-yok tutması açık")
	}
	in.NoDataSince = now.Add(-noDataHoldMax - time.Minute).Unix()
	if cardOpen(in) {
		t.Fatal("sınır sonrası açık değil")
	}
	in.Stage, in.EndNoData = chstore.MLStageClosed, true
	if s := statusOf(in, false, in.LastSeen); !strings.HasPrefix(s, "Veri gelmedi, izlenemedi") {
		t.Fatalf("durum: %q", s)
	}
	in.EndNoData = false
	if s := statusOf(in, false, in.LastSeen); !strings.HasPrefix(s, "Bitti") {
		t.Fatalf("durum: %q", s)
	}
}
