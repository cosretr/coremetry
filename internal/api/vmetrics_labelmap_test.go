package api

// v0.10.944 (CoSRE, çapraz kaynak eşlemesi — VM yarısı) — iki şey pinlenir:
//
//  1. LabelMap PUT sözleşmesi: YOKLUK saklı eşlemeyi korur (bugünkü form alanı
//     göndermiyor; ilgisiz her kayıt eşlemeyi silmemeli), `{}` bilinçli
//     temizler, noktalı ad reddedilir. GET gövdesi eşlemeyi taşır.
//  2. Metrik router'ı mcptools araçlarının TİP İDDİASIYLA aradığı kabiliyetleri
//     taşır: VM yapılandırılmışsa keşif + eşleme + bayraklı sorgu (401 tipli
//     kalır, route dışlamaları uygulanır); değilse CH kaynağı bu kabiliyetleri
//     TAŞIMAZ ve araç ClickHouse yoluna düşer — not_configured DEĞİL.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/vmetrics"
)

func TestMergeVMSettingsLabelMap(t *testing.T) {
	stored := vmetrics.Settings{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: vmetrics.LabelMap{Env: "env", Namespace: "namespace"}}

	// Alan yok → saklı eşleme korunur.
	got, bad := mergeVMSettings(vmSettingsInput{Enabled: true, BaseURL: "http://vm:8428"}, stored)
	if bad != "" || got.LabelMap != stored.LabelMap {
		t.Fatalf("yokluk eşlemeyi korumadı: %+v (%s)", got.LabelMap, bad)
	}
	// Verilen eşleme (kırpılarak) yerine geçer.
	got, bad = mergeVMSettings(vmSettingsInput{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: &vmetrics.LabelMap{Service: " app ", Pod: "pod"}}, stored)
	if bad != "" || got.LabelMap != (vmetrics.LabelMap{Service: "app", Pod: "pod"}) {
		t.Fatalf("eşleme uygulanmadı: %+v (%s)", got.LabelMap, bad)
	}
	// `{}` bilinçli temizler.
	got, bad = mergeVMSettings(vmSettingsInput{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: &vmetrics.LabelMap{}}, stored)
	if bad != "" || got.LabelMap != (vmetrics.LabelMap{}) {
		t.Fatalf("boş nesne temizlemedi: %+v (%s)", got.LabelMap, bad)
	}
	// Noktalı ad → 400 metni, doğru yazımı önerir.
	_, bad = mergeVMSettings(vmSettingsInput{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: &vmetrics.LabelMap{Namespace: "k8s.namespace.name"}}, stored)
	if !strings.Contains(bad, "labelMap.namespace") || !strings.Contains(bad, "k8s_namespace_name") {
		t.Fatalf("geçersiz etiket adı reddedilmedi: %q", bad)
	}
}

// PUT gövdesindeki anahtar adı ve GET'in eşlemeyi taşıması (JSON tel sözleşmesi).
func TestVMSettingsLabelMapWire(t *testing.T) {
	var in vmSettingsInput
	if err := json.Unmarshal([]byte(`{"enabled":true,"baseUrl":"http://vm:8428","labelMap":{"env":"deployment_environment"}}`), &in); err != nil {
		t.Fatal(err)
	}
	if in.LabelMap == nil || in.LabelMap.Env != "deployment_environment" {
		t.Fatalf("labelMap PUT gövdesinden okunmadı: %+v", in.LabelMap)
	}
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{BaseURL: "http://vm:8428", LabelMap: vmetrics.LabelMap{Env: "env"}})
	s := &Server{vmetrics: vm}
	w := httptest.NewRecorder()
	s.getVMSettings(w, httptest.NewRequest("GET", "/api/settings/victoria-metrics", nil))
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	lm, ok := body["labelMap"].(map[string]any)
	if !ok || lm["env"] != "env" {
		t.Fatalf("GET labelMap taşımıyor: %s", w.Body.String())
	}
}

// mcptools'un aradığı kabiliyetler — imzalar metrics_tools.go ile birebir.
type (
	toolBackendNamer interface{ MetricBackend() string }
	toolDiscoverer   interface {
		MetricLabelNamesIn(ctx context.Context, metric string, from, to time.Time) ([]string, bool, error)
	}
	toolDetailQuerier interface {
		QueryMetricDetailed(ctx context.Context, f chstore.MetricQueryFilter) (vmetrics.QueryDetail, error)
	}
	toolLabelMapper interface{ MetricLabelMap() vmetrics.LabelMap }
	toolValuesIn    interface {
		MetricLabelValuesIn(ctx context.Context, metric, label string, from, to time.Time, limit int) ([]string, bool, error)
	}
	toolAttrKeys interface {
		MetricAttrKeys(ctx context.Context, metric, service string, since time.Duration) ([]string, error)
	}
)

func TestMCPDepsMetricCapabilities(t *testing.T) {
	// VM yapılandırılmamış → CH router'ı: adı "clickhouse", VM kabiliyetleri YOK
	// (araç konvansiyon yoluna düşer; not_configured yalnız HİÇBİR kaynak yokken).
	s := &Server{store: &chstore.Store{}, vmetrics: vmetrics.New()}
	d := s.mcpDeps()
	if n, ok := d.Metrics.(toolBackendNamer); !ok || n.MetricBackend() != "clickhouse" {
		t.Fatalf("CH router adı: %T", d.Metrics)
	}
	if _, ok := d.Metrics.(toolDetailQuerier); ok {
		t.Fatal("CH router VM bayraklı sorgu kabiliyeti taşıyor — araç VM yolunu seçer")
	}
	if _, ok := d.Metrics.(toolAttrKeys); !ok {
		t.Fatal("CH router etiket anahtarı okuyucusu taşımıyor — CH doğrulaması kapanır")
	}

	// VM yapılandırılmış → tüm kabiliyetler.
	s.vmetrics.Configure(vmetrics.Settings{Enabled: true, BaseURL: "http://vm:8428",
		LabelMap: vmetrics.LabelMap{Env: "env"}})
	d = s.mcpDeps()
	for name, ok := range map[string]bool{
		"namer":      func() bool { _, ok := d.Metrics.(toolBackendNamer); return ok }(),
		"discoverer": func() bool { _, ok := d.Metrics.(toolDiscoverer); return ok }(),
		"detail":     func() bool { _, ok := d.Metrics.(toolDetailQuerier); return ok }(),
		"labelmap":   func() bool { _, ok := d.Metrics.(toolLabelMapper); return ok }(),
		"valuesIn":   func() bool { _, ok := d.Metrics.(toolValuesIn); return ok }(),
	} {
		if !ok {
			t.Errorf("VM router %s kabiliyetini taşımıyor", name)
		}
	}
	if lm := d.Metrics.(toolLabelMapper).MetricLabelMap(); lm.Env != "env" {
		t.Fatalf("router canlı LabelMap'i iletmiyor: %+v", lm)
	}
	if d.Metrics.(toolBackendNamer).MetricBackend() != vmetrics.BackendName {
		t.Fatal("VM router adı")
	}
}

// Router'ın bayraklı sorgusu: isPartial iletilir, 401 hem errUpstream (HTTP
// 502 sözleşmesi) hem sourcestate.ErrUnauthorized (araç durumu) olarak kalır.
func TestVMRouterDetailedQuery(t *testing.T) {
	status := http.StatusOK
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		gotQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","isPartial":true,"data":{"resultType":"matrix","result":[]}}`))
	}))
	defer srv.Close()
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: srv.URL})
	v := vmMetricSource{svc: vm}
	from := time.Now().Add(-time.Hour)
	f := chstore.MetricQueryFilter{Name: "jvm_memory_used_bytes", Service: "checkout", From: from, To: from.Add(time.Hour)}

	d, err := v.QueryMetricDetailed(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Partial || !strings.Contains(gotQuery, `service_name="checkout"`) {
		t.Fatalf("isPartial/sorgu iletilmedi: %+v %s", d, gotQuery)
	}

	status = http.StatusUnauthorized
	_, err = v.QueryMetricDetailed(context.Background(), f)
	if !errors.Is(err, errUpstream) || !errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("401 etiketleri kayboldu: %v", err)
	}
	if got := sourcestate.Classify(err); got != sourcestate.Unauthorized {
		t.Fatalf("Classify = %s", got)
	}
	_, _, err = v.MetricLabelNamesIn(context.Background(), "m", from, from.Add(time.Hour))
	if !errors.Is(err, errUpstream) || !errors.Is(err, sourcestate.ErrUnauthorized) {
		t.Fatalf("keşif hatası etiketsiz: %v", err)
	}
}

// v0.10.944 — router etiket uçlarının isPartial'ını araca iletir (adaptör
// bayrağı düşürürse araç eksik kümeyi "etiket yok" diye reddeder).
func TestVMRouterLabelDiscoveryPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/labels" {
			_, _ = w.Write([]byte(`{"status":"success","isPartial":true,"data":["job"]}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","isPartial":true,"data":["checkout"]}`))
	}))
	defer srv.Close()
	vm := vmetrics.New()
	vm.Configure(vmetrics.Settings{Enabled: true, BaseURL: srv.URL})
	v := vmMetricSource{svc: vm}
	from := time.Now().Add(-time.Hour)
	names, partial, err := v.MetricLabelNamesIn(context.Background(), "m", from, from.Add(time.Hour))
	if err != nil || !partial || len(names) != 1 {
		t.Fatalf("etiket adları: %v partial=%v err=%v", names, partial, err)
	}
	vals, partial, err := v.MetricLabelValuesIn(context.Background(), "m", "job", from, from.Add(time.Hour), 10)
	if err != nil || !partial || len(vals) != 1 {
		t.Fatalf("etiket değerleri: %v partial=%v err=%v", vals, partial, err)
	}
}
