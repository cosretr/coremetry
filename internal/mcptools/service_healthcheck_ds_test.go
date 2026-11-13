package mcptools

// service_healthcheck_ds_test.go — v0.10.2043 (servis sağlık kontrolü dilim
// 3): JBoss datasource havuzu. Datasource başına ayrı satır; ad bulunamazsa
// "Ölçülemedi — datasource metriği bulunamadı", değer uydurulmaz.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cilcenk/coremetry/internal/thanos"
)

func dsSample(name, pod, ds string, xa bool, v float64) thanos.Sample {
	l := map[string]string{"__name__": name, "pod": pod}
	if xa {
		l["xa_data_source"] = ds
	} else {
		l["data_source"] = ds
	}
	return thanos.Sample{Labels: l, Value: v}
}

func TestDatasourcePoolsFrom(t *testing.T) {
	const p = "jboss_datasources_pool_"
	cur := []thanos.Sample{
		dsSample(p+"in_use_count", "orders-api-1", "ORDERS_DS", false, 18),
		dsSample(p+"available_count", "orders-api-1", "ORDERS_DS", false, 2),
		dsSample(p+"in_use_count", "orders-api-2", "ORDERS_DS", false, 5),
		dsSample(p+"available_count", "orders-api-2", "ORDERS_DS", false, 15),
		dsSample(p+"wait_count", "orders-api-1", "ORDERS_DS", false, 12),
		dsSample(p+"timed_out", "orders-api-1", "ORDERS_DS", false, 3),
		// XA: yalnız active (in_use yok) → active yedek.
		dsSample(p+"active_count", "orders-api-1", "PAY_XA", true, 4),
		dsSample(p+"available_count", "orders-api-1", "PAY_XA", true, 0),
	}
	prev := []thanos.Sample{
		dsSample(p+"wait_count", "orders-api-1", "ORDERS_DS", false, 10),
		dsSample(p+"timed_out", "orders-api-1", "ORDERS_DS", false, 3),
		dsSample(p+"available_count", "orders-api-1", "PAY_XA", true, 0),
	}
	pools := DatasourcePoolsFrom(cur, prev, true)
	if len(pools) != 2 || pools[0].Name != "ORDERS_DS" || pools[1].Name != "PAY_XA" || !pools[1].XA {
		t.Fatalf("havuzlar: %+v", pools)
	}
	o := pools[0]
	if o.Pods != 2 || o.UtilPct == nil || *o.UtilPct != 90 || o.UtilPod != "orders-api-1" {
		t.Fatalf("ORDERS_DS doluluk: %+v", o)
	}
	if o.WaitInc == nil || *o.WaitInc != 2 || o.TimedOutInc == nil || *o.TimedOutInc != 0 || o.BlockFailInc != nil {
		t.Fatalf("ORDERS_DS sayaçlar: wait=%v to=%v bf=%v", o.WaitInc, o.TimedOutInc, o.BlockFailInc)
	}
	x := pools[1]
	if x.UtilPct == nil || *x.UtilPct != 100 || x.ZeroAvailPods != 1 || x.ZeroAvailBoth != 1 {
		t.Fatalf("PAY_XA: %+v", x)
	}
	// Sayaç sıfırlanması (pod restart): cur < prev → artış = cur.
	r := DatasourcePoolsFrom([]thanos.Sample{dsSample(p+"wait_count", "a", "D", false, 3)}, []thanos.Sample{dsSample(p+"wait_count", "a", "D", false, 50)}, true)
	if r[0].WaitInc == nil || *r[0].WaitInc != 3 {
		t.Fatalf("sıfırlama: %+v", r[0].WaitInc)
	}
	// Pencere başı okunamadı → artış bilinmiyor (nil), sıfır DEĞİL.
	u := DatasourcePoolsFrom([]thanos.Sample{dsSample(p+"wait_count", "a", "D", false, 3)}, nil, false)
	if u[0].WaitInc != nil {
		t.Fatalf("pencere başı yokken artış nil olmalı")
	}
}

func TestHCDatasources(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	base := ServicePodsCluster{Rows: []thanos.PodRow{{Namespace: "shop", Pod: "orders-api-1", CPUCores: 1, MemBytes: 1}}, DSDiscoveryOK: true,
		DSNames: []string{"jboss_datasources_pool_in_use_count"}}
	cases := []struct {
		name  string
		cl    func() ServicePodsCluster
		want  []string
		seen  string
		short string
	}{
		{"keşif okunamadı → ölçülemedi", func() ServicePodsCluster { c := base; c.DSDiscoveryOK, c.DSErr = false, "boom"; return c }, []string{HCUnknown}, "okunamadı", ""},
		{"ad yok → ölçülemedi", func() ServicePodsCluster { c := base; c.DSNames = nil; return c }, []string{HCUnknown}, "datasource metriği bulunamadı", ""},
		{"değer okunamadı → ölçülemedi", func() ServicePodsCluster { c := base; c.DSErr = "timeout"; return c }, []string{HCUnknown}, "okunamadı", ""},
		{"temiz", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 2, UtilPct: f(40), WaitInc: f(0), TimedOutInc: f(0)}}
			return c
		}, []string{HCOK}, "%40", ""},
		{"%85 → dikkat", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 2, UtilPct: f(85), UtilPod: "orders-api-1"}}
			return c
		}, []string{HCWarn}, "%85", "ORDERS_DS havuzu %85 dolu (cluster-a)"},
		{"%96 → sorun", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 2, UtilPct: f(96), UtilPod: "orders-api-1"}}
			return c
		}, []string{HCCrit}, "%96", "ORDERS_DS havuzu dolu (cluster-a)"},
		{"kullanılabilir 0 pencere boyunca → sorun", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 1, UtilPct: f(100), ZeroAvailPods: 1, ZeroAvailBoth: 1}}
			return c
		}, []string{HCCrit}, "kullanılabilir bağlantı 0", ""},
		{"bekleyen istek artışı → dikkat", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 1, UtilPct: f(40), WaitInc: f(4)}}
			return c
		}, []string{HCWarn}, "4 bekleyen", ""},
		{"zaman aşımı artışı → sorun", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 1, UtilPct: f(40), TimedOutInc: f(2)}}
			return c
		}, []string{HCCrit}, "2 zaman aşımı", ""},
		{"doluluk hesaplanamadı, sayaç yok → ölçülemedi", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "ORDERS_DS", Pods: 1}}
			return c
		}, []string{HCUnknown}, "hesaplanamadı", ""},
		{"biri sapan, biri sağlıklı → sapan önce, sağlıklı toplu satırda", func() ServicePodsCluster {
			c := base
			c.DS = []DatasourcePool{{Name: "A_DS", Pods: 1, UtilPct: f(10)}, {Name: "B_XA", XA: true, Pods: 1, UtilPct: f(99)}}
			return c
		}, []string{HCCrit, HCOK}, "%99", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows := hcDatasources("orders-api", "cluster-a", c.cl())
			if len(rows) != len(c.want) {
				t.Fatalf("satır %d, want %d: %+v", len(rows), len(c.want), rows)
			}
			for i, r := range rows {
				if r.State != c.want[i] || r.Check != HCCheckDatasource {
					t.Fatalf("satır %d: %q (%s), want %q", i, r.State, r.Seen, c.want[i])
				}
			}
			if !strings.Contains(rows[0].Seen, c.seen) {
				t.Fatalf("seen %q, %q içermeli", rows[0].Seen, c.seen)
			}
			if c.short != "" && rows[0].Short != c.short {
				t.Fatalf("short %q, want %q", rows[0].Short, c.short)
			}
		})
	}
	two := hcDatasources("orders-api", "cluster-a", cases[len(cases)-1].cl())
	if two[0].Subject != "B_XA (cluster-a)" || two[1].Subject != "sağlıklı olanlar (cluster-a)" || two[1].Seen != "diğer 1 havuz; A_DS doluluk %10" {
		t.Fatalf("özne: %q %q (%s)", two[0].Subject, two[1].Subject, two[1].Seen)
	}
}

// v0.10.2078 — operatör (prod ekranı): 10+ sağlıklı havuzun her biri ayrı
// satırdı. Sağlıklılar TEK satır; pod adı ve formül yalnız sapan satırda.
func TestHCDatasourcesHealthyCollapse(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	cl := ServicePodsCluster{DSDiscoveryOK: true, DSNames: []string{"jboss_datasources_pool_in_use_count"}}
	for i := 0; i < 11; i++ {
		u := float64(i % 3)
		if i == 4 {
			u = 8
		}
		cl.DS = append(cl.DS, DatasourcePool{Name: fmt.Sprintf("DS_%02d", i), XA: true, Pods: 2, UtilPct: f(u), UtilPod: "orders-api-7c9d8f6b5d-x2kqz"})
	}
	rows := hcDatasources("orders-api", "cluster-a", cl)
	if len(rows) != 1 || rows[0].State != HCOK || rows[0].Seen != "11 havuz; en dolu DS_04 %8" {
		t.Fatalf("hepsi sağlıklı → tek satır: %+v", rows)
	}
	if strings.Contains(rows[0].Seen, "orders-api-") || strings.Contains(rows[0].Seen, "kullanılabilir") {
		t.Fatalf("sağlıklı satırda pod adı / formül olmamalı: %q", rows[0].Seen)
	}
	cl.DS[2].UtilPct = f(96)
	rows = hcDatasources("orders-api", "cluster-a", cl)
	if len(rows) != 2 || rows[0].State != HCCrit || rows[0].Subject != "DS_02 (cluster-a)" ||
		!strings.Contains(rows[0].Seen, "en kötü pod orders-api-7c9d8f6b5d-x2kqz") || rows[1].Seen != "diğer 10 havuz; en dolu DS_04 %8" {
		t.Fatalf("biri sapan: %+v", rows)
	}
	if got := hcDSHealthySeen(3, false, "", -1); got != "3 havuz; bekleme / zaman aşımı yok" {
		t.Fatalf("doluluk yokken: %q", got)
	}
}
