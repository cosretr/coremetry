package ml

// explain_evidence_test.go — v0.10.1873 (audit §7.1 "Kanıt"; mockup görünüm
// 2–4): endpoint / mesaj kalıbı / komşu metrik bölümlerinin saf kurulumu.
// Sayı uydurulmaz: önceki pencere okunamadıysa oran ve "yeni" yazılmaz.

import (
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestEvidenceWindow(t *testing.T) {
	in := playFixture() // 14:30 başladı, açık, şimdi 14:44
	from, to, prev := EvidenceWindow(in)
	eq(t, "pencere", from.In(trLoc).Format("15:04")+"–"+to.In(trLoc).Format("15:04"), "14:30–14:44")
	eq(t, "önceki", prev.In(trLoc).Format("15:04"), "14:16")

	long := playFixture()
	long.LastSeen, long.Now = at(18, 0), at(18, 1)
	from, to, _ = EvidenceWindow(long)
	if to.Sub(from) != time.Hour {
		t.Errorf("tavan 1 sa: %s", to.Sub(from))
	}
	short := playFixture()
	short.Now = at(14, 33)
	from, to, _ = EvidenceWindow(short)
	if to.Sub(from) != 3*time.Minute {
		t.Errorf("en az 15 dk ama şimdiyi aşmaz: %s", to.Sub(from))
	}
	if !strings.HasPrefix(EvidenceRange(in), "custom:") {
		t.Errorf("range %q", EvidenceRange(in))
	}
}

func TestEndpointEvidence(t *testing.T) {
	in := playFixture() // endpoint /notify/order-status
	cur := []chstore.EndpointRow{
		{Path: "GET /notify/templates/{id}", Calls: 1700, P95Ms: 200},
		{Path: "/notify/order-status", Calls: 8400, P95Ms: 2300},
		{Path: "/notify/payment-result", Calls: 9100, P95Ms: 1400},
	}
	prev := []chstore.EndpointRow{{Path: "/notify/order-status", P95Ms: 600}, {Path: "/notify/payment-result", P95Ms: 900}}
	rows := EndpointEvidence(in, cur, prev, "custom:1-2")
	var got []string
	for _, r := range rows {
		got = append(got, r.Route)
	}
	eqList(t, "sıra (olayın endpoint'i önce, sonra çağrı)", got, []string{"/notify/order-status", "/notify/payment-result", "GET /notify/templates/{id}"})
	if !rows[0].Focus || !rows[0].Deviates || rows[1].Deviates || rows[2].PrevP95 != 0 || rows[2].Deviates {
		t.Errorf("odak/sapma: %+v", rows)
	}
	if !strings.Contains(rows[0].Href, "/traces?") || !strings.Contains(rows[0].Href, "http.route") {
		t.Errorf("href %q", rows[0].Href)
	}
}

func TestPatternEvidence(t *testing.T) {
	in, _ := logCourseFixture()
	groups := []logstore.SignatureGroup{
		{Template: "Order <*> failed: upstream unavailable", Count: 64, PrevCount: 50, Query: "upstream unavailable"},
		{Template: "Timeout calling payments-mobile after <*> ms", Count: 166, PrevCount: 14, Query: "Timeout calling payments-mobile"},
		{Template: "Connection pool exhausted: active=<*>", Count: 410, PrevCount: 0, FirstSeen: at(14, 31).UnixNano(), Sample: "ERROR Connection pool exhausted: active=50", Query: "Connection pool exhausted"},
		{Template: "Health check ok", Count: 30, PrevCount: 31},
	}
	rows := PatternEvidence(in, groups, true, "custom:1-2")
	if len(rows) != 3 {
		t.Fatalf("yalnız yeni ve artan: %+v", rows)
	}
	eq(t, "yeni önce", rows[0].Compare, "önceki eşit pencerede görülmedi")
	eq(t, "ilk görülme", rows[0].FirstSeen, "14:31")
	eq(t, "oran", rows[1].Compare, "normalin yaklaşık 12 katı")
	eq(t, "biraz", rows[2].Compare, "biraz yüksek")
	if !rows[0].New || !strings.Contains(rows[0].Href, "severity=17") {
		t.Errorf("hata logu linki: %+v", rows[0])
	}
	unread := PatternEvidence(in, groups, false, "")
	if len(unread) != 4 || unread[0].Compare != "" || unread[0].New {
		t.Errorf("taban okunamadı: oran/yeni yazılmaz: %+v", unread)
	}
}

func TestMetricNeighbors(t *testing.T) {
	cfg := chstore.MLConfig{MetricFamilies: map[string][]chstore.MLMetricExpr{
		"metric_pool":        {{ID: "pending", Label: "havuzda bekleyen istek"}, {ID: "active", Label: "aktif bağlantı"}},
		"metric_http_client": {{ID: "p95", Label: "çağrı süresi", Unit: "s"}},
	}}
	in := CardInput{Detector: "metric_pool", Signal: chstore.MLSignalMetric, Service: "payments-mobile", Part: "metric_pool|active",
		StartedAt: at(14, 30), LastSeen: at(14, 40), Now: at(14, 44), Loc: trLoc, BucketSec: 300}
	pts := func(vs ...float64) []chstore.MLPoint {
		var out []chstore.MLPoint
		for i, v := range vs {
			out = append(out, chstore.MLPoint{T: at(14, 15).Add(time.Duration(i) * 5 * time.Minute).Unix(), V: v})
		}
		return out
	}
	series := map[string][]chstore.MLPoint{
		"metric_pool|active":     pts(10, 10, 10, 50, 50, 50), // olayın kendisi: listede yok
		"metric_pool|pending":    pts(0, 0, 0, 12, 40, 41),
		"metric_http_client|p95": pts(0.2, 0.2, 0.2, 1.1, 1.6, 1.5),
		"metric_jvm|heap":        pts(100, 101, 100, 102, 101, 100),
		"metric_kafka|lag":       pts(5, 5, 5, 5, 5.4, 5),
		"metric_pool|single":     {{T: at(14, 0).Unix(), V: 1}}, // tek nokta: grafik olmaz
	}
	ns := MetricNeighbors(in, series, cfg)
	if len(ns) != 3 {
		t.Fatalf("en çok 3: %+v", ns)
	}
	eq(t, "en çok sapan önce", ns[0].Label, "havuzda bekleyen istek")
	eq(t, "metin", ns[0].Text, "0 → 41.")
	eq(t, "saniye", ns[1].Text, "0,2 sn → 1,6 sn.")
	eq(t, "değişmedi", ns[2].Text, "Değişmedi.")
	for _, n := range ns {
		if n.Part == in.Part {
			t.Error("olayın kendi serisi komşu değil")
		}
	}
}
