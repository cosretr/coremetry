package api

// es_request_meter_test.go — v0.10.1878.

import (
	"context"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestESRequestsWindow(t *testing.T) {
	cases := map[string][2]int{"": {180, 60}, "60": {60, 60}, "180": {180, 60}, "360": {360, 300}, "1440": {1440, 900}, "7": {180, 60}, "abc": {180, 60}, "99999": {180, 60}}
	for raw, want := range cases {
		if w, s := esRequestsWindow(raw); w != want[0] || s != want[1] {
			t.Errorf("esRequestsWindow(%q) = %d/%d, beklenen %d/%d", raw, w, s, want[0], want[1])
		}
	}
}

func TestSummarizeESRequests(t *testing.T) {
	to := time.Date(2026, 11, 6, 13, 0, 0, 0, time.UTC)
	from := to.Add(-60 * time.Minute)
	at := func(minAgo int) int64 { return to.Add(-time.Duration(minAgo) * time.Minute).Unix() }
	buckets := []chstore.SelfESBucket{
		// taban: 45 dakikada 450 istek = 10/dk
		{T: at(50), Op: "search", Surface: "route:GET /api/logs", Count: 300, DurMs: 30000},
		{T: at(40), Op: "search", Surface: "worker:templater", Count: 150, DurMs: 1500},
		// son 15 dk: 900 istek = 60/dk (6 kat artış), hepsi /api/logs
		{T: at(10), Op: "search", Surface: "route:GET /api/logs", Count: 600, Errors: 6, DurMs: 120000},
		{T: at(5), Op: "msearch", Surface: "route:GET /api/logs", Count: 300, DurMs: 60000},
	}
	got := summarizeESRequests(buckets, from, to, 60, 60)
	if got.Total != 1350 || got.Errors != 6 {
		t.Fatalf("toplam %v hata %v", got.Total, got.Errors)
	}
	if got.RecentPerMin != 60 || got.BasePerMin != 10 {
		t.Fatalf("son 15 dk %v/dk, taban %v/dk (beklenen 60 ve 10)", got.RecentPerMin, got.BasePerMin)
	}
	if len(got.Points) != 60 {
		t.Fatalf("kova sayısı %d, beklenen 60 (boş kovalar 0 ile dolu)", len(got.Points))
	}
	var seen float64
	for _, p := range got.Points {
		seen += p.Count
	}
	if seen != 1350 {
		t.Fatalf("seri toplamı %v", seen)
	}
	if got.ByOp[0].Op != "search" || got.ByOp[0].Count != 1050 || got.ByOp[1].Op != "msearch" {
		t.Fatalf("işlem kırılımı: %+v", got.ByOp)
	}
	// En çok SON 15 dakikada istek atan yüzey önde.
	top := got.BySurface[0]
	if top.Surface != "route:GET /api/logs" || top.RecentPerMin != 60 || top.Count != 1200 || top.Errors != 6 {
		t.Fatalf("ilk yüzey: %+v", top)
	}
	if top.AvgMs != 175 { // (30000+120000+60000)/1200
		t.Fatalf("ort. süre %v", top.AvgMs)
	}
	if w := got.BySurface[1]; w.Surface != "worker:templater" || w.RecentPerMin != 0 || w.BasePerMin != 150.0/45 {
		t.Fatalf("ikinci yüzey: %+v", w)
	}
}

func TestSummarizeESRequestsFoldsTail(t *testing.T) {
	to := time.Date(2026, 11, 6, 13, 0, 0, 0, time.UTC)
	from := to.Add(-60 * time.Minute)
	var buckets []chstore.SelfESBucket
	for i := 0; i < esMeterTopSurface+5; i++ {
		buckets = append(buckets, chstore.SelfESBucket{T: to.Add(-30 * time.Minute).Unix(), Op: "search",
			Surface: "route:GET /api/s" + string(rune('a'+i)), Count: float64(100 - i)})
	}
	got := summarizeESRequests(buckets, from, to, 60, 60)
	if len(got.BySurface) != esMeterTopSurface+1 || got.BySurface[esMeterTopSurface].Surface != "diğerleri" {
		t.Fatalf("kuyruk katlanmadı: %d satır", len(got.BySurface))
	}
	var sum float64
	for _, r := range got.BySurface {
		sum += r.Count
	}
	if sum != got.Total {
		t.Fatalf("yüzey toplamı %v ≠ toplam %v (istek kaybı)", sum, got.Total)
	}
	// Boş girdi: sıfır değerli, boş olmayan (nil değil) diziler.
	empty := summarizeESRequests(nil, from, to, 60, 60)
	if empty.Total != 0 || empty.ByOp == nil || empty.BySurface == nil || len(empty.Points) != 60 {
		t.Fatalf("boş yanıt: %+v", empty)
	}
}

type fakeESSink struct {
	got []*chstore.MetricPoint
	err error
}

func (f *fakeESSink) InsertMetrics(_ context.Context, pts []*chstore.MetricPoint) error {
	f.got = append(f.got, pts...)
	return f.err
}

func TestFlushESRequestStatsNoWorkNoWrite(t *testing.T) {
	// Sayaç süreç genelidir; bu test yalnız "boşken yazma yok"u ve hata
	// dönüşünü sınar (hücre içeriği logstore/chstore testlerinde).
	sink := &fakeESSink{}
	far := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) // bundan önce hücre olamaz
	if err := flushESRequestStats(context.Background(), sink, "pod-a", far); err != nil || len(sink.got) != 0 {
		t.Fatalf("boşken yazıldı: err=%v n=%d", err, len(sink.got))
	}
}
