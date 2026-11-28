package main

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/anomaly"
	"github.com/cilcenk/coremetry/internal/runtimesig"
)

func rtNames(t *testing.T, lang string) []string {
	t.Helper()
	set := map[string]bool{}
	for _, m := range runtimePodMetrics(Service{Name: "svc-a", Lang: lang, Pods: []string{"svc-a-1"}}, "svc-a-1", 1_700_000_000_000_000_000, 1, 1) {
		set[m.Name] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Dil → üretilen ad kümesi == sözleşme (fazla da eksik de yok).
func TestRuntimeMetricsByLanguage(t *testing.T) {
	cases := map[string]string{
		"java": "jvm", "kotlin": "jvm", "dotnet": "dotnet", ".net": "dotnet",
		"go": "go", "python": "python", "nodejs": "", "rust": "", "": "",
	}
	for lang, fam := range cases {
		got := rtNames(t, lang)
		want := append([]string(nil), runtimeUIContract[fam]...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%q (%s): got %v want %v", lang, fam, got, want)
		}
	}
}

// RuntimeCharts.tsx'in sorguladığı her ad sözleşmede (grafik ad değiştirirse
// demo sessizce kör kalmasın). Önek → aile.
func TestRuntimeContractCoversRuntimeCharts(t *testing.T) {
	src, err := os.ReadFile("../../frontend/src/pages/service/RuntimeCharts.tsx")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`metric: '([a-z0-9_.]+)'`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(string(src), -1) {
		fam := ""
		switch {
		case strings.HasPrefix(m[1], "jvm."):
			fam = "jvm"
		case strings.HasPrefix(m[1], "process.runtime.go."):
			fam = "go"
		default:
			t.Errorf("tanınmayan grafik adı %q", m[1])
			continue
		}
		n++
		if !contains(runtimeUIContract[fam], m[1]) {
			t.Errorf("RuntimeCharts %q sözleşmede (%s) yok", m[1], fam)
		}
	}
	if n < 8 {
		t.Fatalf("RuntimeCharts'tan yalnız %d ad okundu — regex bayat mı?", n)
	}
	for _, h := range []string{anomaly.HeapMetricPostGC, anomaly.HeapMetricLimit} {
		if !contains(runtimeUIContract["jvm"], h) {
			t.Errorf("heap bandı adı %q sözleşmede yok", h)
		}
	}
}

// Üretilen adlarla sunucunun runtime profili, panellerin çizdiği sinyalleri
// çözer (DotnetRuntimePanel / Python paneli / Go + JVM "kendi ailesi").
func TestRuntimeProfileResolvesPanelSignals(t *testing.T) {
	want := map[runtimesig.Runtime][]runtimesig.SignalKey{
		runtimesig.RuntimeJVM: {runtimesig.SigHeapUsed, runtimesig.SigHeapCommitted, runtimesig.SigHeapLimit, runtimesig.SigThreads},
		runtimesig.RuntimeDotnet: {runtimesig.SigHeapUsed, runtimesig.SigHeapCommitted, runtimesig.SigFragmentation,
			runtimesig.SigGCCollections, runtimesig.SigGCTime, runtimesig.SigAllocRate, runtimesig.SigThreads,
			runtimesig.SigPoolQueue, runtimesig.SigPoolCompleted, runtimesig.SigLockContention, runtimesig.SigExceptions,
			runtimesig.SigJITMethods, runtimesig.SigWorkingSet},
		runtimesig.RuntimeGo:     {runtimesig.SigHeapUsed, runtimesig.SigThreads},
		runtimesig.RuntimePython: {runtimesig.SigGCCollections, runtimesig.SigCPUUtil, runtimesig.SigMemoryRSS, runtimesig.SigThreads, runtimesig.SigCtxSwitches},
	}
	lang := map[runtimesig.Runtime]string{runtimesig.RuntimeJVM: "java", runtimesig.RuntimeDotnet: "dotnet", runtimesig.RuntimeGo: "go", runtimesig.RuntimePython: "python"}
	for rt, sigs := range want {
		p := runtimesig.Resolve(rt, rtNames(t, lang[rt]))
		for _, k := range sigs {
			if !p.Has(k) {
				t.Errorf("%s: sinyal %s çözülmedi", rt, k)
			}
		}
		if rt == runtimesig.RuntimeDotnet && p.DotnetGen != runtimesig.GenInstrumentation {
			t.Errorf(".NET kuşağı %q, beklenen instrumentation", p.DotnetGen)
		}
	}
}

// Pod başına ayrışma + senaryolu olay pod'u atlanır.
func TestRuntimeMetricsPerPod(t *testing.T) {
	if podFactor("a-1") == podFactor("a-2") {
		t.Fatal("pod çarpanı ayrışmıyor")
	}
	prev := k8sMetrics
	defer func() { k8sMetrics = prev }()
	k8sMetrics = nil // simülatör kapalı: yine de olay pod'u (servisin 2. pod'u) atlanır
	rms := runtimeResourceMetrics(1_700_000_000_000_000_000, 1, 1)
	pods := map[string]bool{}
	for _, rm := range rms {
		var svc, pod string
		for _, a := range rm.Resource.Attributes {
			switch a.Key {
			case "service.name":
				svc = a.GetValue().GetStringValue()
			case "k8s.pod.name":
				pod = a.GetValue().GetStringValue()
			}
		}
		pods[svc+"/"+pod] = true
	}
	if pods[k8sIncidentService+"/gw-prod-2"] {
		t.Error("olay pod'u runtime metriği almamalı")
	}
	if !pods[k8sIncidentService+"/gw-prod-1"] || !pods["account-service/acct-prod-1"] || !pods["account-service/acct-prod-2"] {
		t.Errorf("pod başına Resource yok: %d kayıt", len(pods))
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
