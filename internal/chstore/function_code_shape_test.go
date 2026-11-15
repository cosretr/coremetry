package chstore

import (
	"regexp"
	"strings"
	"testing"
)

// v0.10.1226 — biçim ölçümü SQL'i sınırlı ve desen Go'da da geçerli.
func TestFunctionCodeShapeSQL(t *testing.T) {
	q := functionCodeShapeSQL("rollup_spans_wide_1m", "ts", "function_code", "sum(span_count)")
	for _, want := range []string{"ts >= ? AND ts < ?", "LIMIT 60", "max_execution_time = 20", FunctionCodeShape} {
		if !strings.Contains(q, want) {
			t.Errorf("sorgu %q içermiyor", want)
		}
	}
	re := regexp.MustCompile(FunctionCodeShape)
	for code, ok := range map[string]bool{"BQRZ001": true, "bqrz001": false, "123ABC": false, "A1B2C3D4E5F6G": false, "ABC": false} {
		if re.MatchString(code) != ok {
			t.Errorf("%s: %v bekleniyordu", code, ok)
		}
	}
}

// v0.10.1228 — "Where the time goes" örneklem sırası.
func TestEndpointSampleOrder(t *testing.T) {
	if endpointSampleOrder("") != "duration DESC" || endpointSampleOrder("x") != "duration DESC" {
		t.Error("varsayılan en yavaş olmalı")
	}
	if endpointSampleOrder(EndpointSampleRandom) != "cityHash64(trace_id)" {
		t.Error("random hash sırası olmalı")
	}
}

// v0.10.1241 — operatör: "Ölç" HTTP 500 ("converting Int32 to *int is
// unsupported"). SQL toInt32(length(...)) döndürür; tarama hedefi int32 olmalı.
func TestFunctionCodeShapeScanTypes(t *testing.T) {
	src := mustReadSource(t, "function_code_shape.go")
	if !strings.Contains(functionCodeShapeSQL("t", "ts", "c", "count()"), "toInt32(length(c)) AS len") {
		t.Fatal("len sütunu Int32 olarak seçilmeli")
	}
	if !strings.Contains(src, "var n int32") || strings.Contains(src, "&r.Len,") {
		t.Fatal("len int32 değişkene taranmalı, doğrudan int alana değil")
	}
}

// v0.10.1242 — topoloji düğüm adları operasyon okumasını hiç koşmaz.
func TestIsTopologyNodeName(t *testing.T) {
	for n, want := range map[string]bool{"db:oracle@uakb01_uat": true, "queue:kafka:orders": true, "ext:api.example.test": true, "checkout": false, "dbproxy": false, "": false} {
		if IsTopologyNodeName(n) != want {
			t.Errorf("%q: %v bekleniyordu", n, want)
		}
	}
}

// v0.10.1256 — hata sıralaması MV'den, sınırlı.
func TestTopErrorSQL(t *testing.T) {
	for _, q := range []string{topErrorServicesSQL(0), topErrorServicesSQL(2), topErrorOpsSQL()} {
		for _, want := range []string{"time_bucket >= ? AND time_bucket < ?", "LIMIT ?", "max_execution_time", "_summary_5m"} {
			if !strings.Contains(q, want) {
				t.Errorf("%q içermiyor: %s", want, q)
			}
		}
	}
	if !strings.Contains(topErrorServicesSQL(2), "service_name IN (?,?)") || strings.Contains(topErrorServicesSQL(0), " IN (") {
		t.Error("servis süzgeci")
	}
}
