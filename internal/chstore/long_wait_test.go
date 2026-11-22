package chstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// v0.10.2466 — Operator-reported (prod, 2026-11-22): Traces 12 saat +
// `resource.openshift.cluster.name = ocp4` → liste 159 (25 s). Bu dosya üç
// sözleşmeyi çiviler: (1) cluster anahtarı kolona derlenir, (2) yenilik
// basamakları + kısmi sonuç, (3) "daha uzun bekle" bütçesi.

func TestClusterResourceFilterUsesColumn(t *testing.T) {
	prev := clusterColReady.Load()
	defer clusterColReady.Store(prev)

	for _, key := range []string{"resource.openshift.cluster.name", "resource.k8s.cluster.name", "cluster"} {
		f := FilterExpr{Key: key, Op: "=", Values: []string{"ocp4"}}

		clusterColReady.Store(true)
		sql, args, err := f.SQLWithPromoted(nil)
		if err != nil {
			t.Fatal(err)
		}
		if sql != "cluster = ?" || len(args) != 1 || args[0] != "ocp4" {
			t.Errorf("%s kolon hazırken: %q %v — `cluster = ?` bekleniyordu", key, sql, args)
		}
		if strings.Contains(sql, "res_values") || strings.Contains(sql, "kvh") {
			t.Errorf("%s: dizi araması kalmış: %q", key, sql)
		}

		// Kolon okunamıyorsa (harici Distributed, code 47 sınıfı) kolon adı ASLA geçmez.
		clusterColReady.Store(false)
		sql, _, err = f.SQLWithPromoted(nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(sql, "cluster ") {
			t.Errorf("%s kolon yokken kolona derlendi: %q", key, sql)
		}
	}

	// Liste WHERE'i (ham yol) çipi kolona taşır — Cluster seçicisiyle aynı yüklem.
	clusterColReady.Store(true)
	tf := TraceFilter{From: time.Unix(0, 0), To: time.Unix(43200, 0),
		Filters: []FilterExpr{{Key: "resource.openshift.cluster.name", Op: "=", Values: []string{"ocp4"}}}}
	wc := buildGetTracesWhere(tf, clusterColExpr)
	if !strings.Contains(wc.sql(), "cluster = ?") || strings.Contains(wc.sql(), "res_values") {
		t.Errorf("liste WHERE'i kolon yüklemi taşımıyor: %s", wc.sql())
	}
	// Başka resource anahtarı (kolonsuz) dizi yolunda kalır.
	other, _, _ := FilterExpr{Key: "resource.cloud.region", Op: "=", Values: []string{"x"}}.SQLWithPromoted(nil)
	if !strings.Contains(other, "res_values") {
		t.Errorf("kolonsuz resource anahtarı dizi yolundan çıkmış: %q", other)
	}
	// Taban haritalar değişmedi (filterexpr_test pinleri + metric_points yolu).
	if wellKnown["cluster"] != clusterDeriveExpr || wellKnownResource["openshift.cluster.name"] != "" {
		t.Error("taban wellKnown haritaları mutasyona uğramış")
	}
}

func TestTraceRawProbeLookbackFor(t *testing.T) {
	if got := traceRawProbeLookbackFor(15 * time.Minute); got != 15*time.Minute {
		t.Errorf("15m basamağı L=15m olmalı, %v", got)
	}
	if got := traceRawProbeLookbackFor(6 * time.Hour); got != time.Hour {
		t.Errorf("geniş basamak L=1h tavanı, %v", got)
	}
}

func TestTraceRawProbeExact(t *testing.T) {
	floor := int64(1000)
	rows := []TraceRow{{TraceID: "a", StartTime: 1300}, {TraceID: "b", StartTime: 1200}, {TraceID: "c", StartTime: 1100}, {TraceID: "d", StartTime: 900}}
	page, more := traceRawProbeExact(rows, floor, 0, 2)
	if len(page) != 2 || page[0].TraceID != "a" || !more {
		t.Fatalf("ilk sayfa: %+v more=%v", page, more)
	}
	page, more = traceRawProbeExact(rows, floor, 2, 2)
	if len(page) != 1 || page[0].TraceID != "c" || more {
		t.Fatalf("şüpheli satır (floor altı) sayfaya girmemeli: %+v more=%v", page, more)
	}
	page, more = traceRawProbeExact(nil, floor, 0, 50)
	if page == nil || len(page) != 0 || more {
		t.Fatalf("basamak yokken boş (nil değil) liste: %+v", page)
	}
}

func TestTraceTimeoutAsPartial(t *testing.T) {
	ctx, rec := WithTimeoutRecorder(context.Background())
	timeout := errors.New("code: 159, message: Timeout exceeded: elapsed 25068 ms, maximum: 25000 ms")

	// Partial istenmemiş (CSV dışa aktarım vb.): hata AYNEN.
	if _, _, err := traceTimeoutAsPartial(ctx, nil, nil, false, timeout); err == nil {
		t.Fatal("Partial nil iken hata yutulmamalı")
	}
	if rec.Hit() {
		t.Fatal("Partial nil iken kayıtçı işaretlenmemeli")
	}
	// Bütçe dışı hata kısmi sonuca dönmez.
	var p TracePartial
	if _, _, err := traceTimeoutAsPartial(ctx, &p, nil, false, errors.New("code: 62, syntax error")); err == nil || p.Hit {
		t.Fatal("sözdizimi hatası kısmi sonuç sayılmamalı")
	}
	// 159 → boş kısmi sonuç, 500 yok, tur kayıtçısı işaretli.
	rows, more, err := traceTimeoutAsPartial(ctx, &p, nil, false, timeout)
	if err != nil || rows == nil || len(rows) != 0 || more || !p.Hit || !p.CoveredFrom.IsZero() {
		t.Fatalf("159 kısmi sonuca dönmeli: rows=%v more=%v err=%v p=%+v", rows, more, err, p)
	}
	if !rec.Hit() {
		t.Fatal("kısmi sonuç CoSRE kayıtçısını işaretlemeli")
	}
	// Basamağın kapsamı işaretlenmişse korunur.
	var p2 TracePartial
	cov := time.Unix(1700000000, 0)
	p2.mark(cov, timeout)
	if _, _, err := traceTimeoutAsPartial(context.Background(), &p2, []TraceRow{{TraceID: "x"}}, true, nil); err != nil || !p2.CoveredFrom.Equal(cov) {
		t.Fatalf("kapsam kaybolmuş: %+v", p2)
	}
}

func TestApplyLongWaitSQL(t *testing.T) {
	q := "SELECT 1 SETTINGS max_execution_time = 25, optimize_read_in_order = 1"
	if got := applyLongWaitSQL(q, 0); got != q {
		t.Fatalf("bayraksız SQL değişmemeli: %q", got)
	}
	if got := applyLongWaitSQL(q, 60); !strings.Contains(got, "max_execution_time = 60") || strings.Contains(got, "= 25") {
		t.Fatalf("25 → 60 olmalı: %q", got)
	}
	big := "SELECT 1 SETTINGS max_execution_time=120"
	if got := applyLongWaitSQL(big, 60); got != big {
		t.Fatalf("daha büyük bütçe küçültülmemeli: %q", got)
	}
}

func TestLongWaitCapAndCtx(t *testing.T) {
	cases := map[int]int{0: 60, -5: 60, 10: 25, 25: 25, 90: 90, 500: 180}
	for in, want := range cases {
		if got := ClampLongWaitCap(in); got != want {
			t.Errorf("ClampLongWaitCap(%d)=%d, beklenen %d", in, got, want)
		}
	}
	// Etkin = min(60, tavan) — operatör: evet = 60 sn, tavan yalnız üst sınır.
	for capSec, want := range map[int]int{25: 25, 40: 40, 60: 60, 180: 60} {
		if got := EffectiveLongWaitSec(capSec); got != want {
			t.Errorf("EffectiveLongWaitSec(%d)=%d, beklenen %d", capSec, got, want)
		}
	}
	ctx := context.Background()
	if WithLongWait(ctx, 25) != ctx || ReadBudgetSec(ctx) != DefaultReadBudgetSec {
		t.Fatal("varsayılan bütçede ctx aynen kalmalı")
	}
	lw := WithLongWait(ctx, 60)
	if LongWaitFrom(lw) != 60 || ReadBudgetSec(lw) != 60 {
		t.Fatal("uzun bütçe ctx'te taşınmalı")
	}
	// Son tarihsiz ctx'te sürücü son tarihi açılır (30 s ReadTimeout'u ezer).
	dctx, q, cancel := longWaitQuery(lw, "SELECT 1 SETTINGS max_execution_time = 25")
	if cancel == nil {
		t.Fatal("son tarihsiz uzun bekleme türev ctx açmalı")
	}
	defer cancel()
	if dl, ok := dctx.Deadline(); !ok || time.Until(dl) < 60*time.Second {
		t.Fatalf("son tarih ≥ 60 s olmalı: %v %v", dl, ok)
	}
	if !strings.Contains(q, "= 60") {
		t.Fatalf("SQL bütçesi yükselmedi: %q", q)
	}
	if _, _, c := longWaitQuery(ctx, "SELECT 1"); c != nil {
		t.Fatal("bayraksız sorgu sarılmamalı (bayt bayt eski yol)")
	}
}
