package api

// services_namespace_test.go — v0.10.1140 regresyonu (operatör-raporlu, test
// ortamı): Services sayfasında namespace süzgeci payments-prep'te servisleri
// listeliyor, payments-uat'ta hiçbirini — aynı servisler uat'tan da trace
// gönderirken. Kök neden: service_metadata servis başına TEK namespace (en sık
// görülen) tutuyor, süzgeç ona eşitlikle bakıyordu (api.go getServices) ve
// seçenek listesi aynı katalogdan geliyordu (getNamespaces).

import (
	"reflect"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

func TestServicesAllowlistNamespaceMembership(t *testing.T) {
	// Katalog: deriver svc-gateway için yoğun trafiğin namespace'ini
	// (payments-prep) yazmış; svc-ledger'a operatör elle payments-uat
	// sabitlemiş; svc-batch'in namespace'i yok.
	catalog := map[string]chstore.ServiceMetadata{
		"svc-gateway": {Service: "svc-gateway", Namespace: "payments-prep", NamespaceAuto: "payments-prep", OwnerTeam: "team-a"},
		"svc-orders":  {Service: "svc-orders", Namespace: "payments-prep", NamespaceAuto: "payments-prep", OwnerTeam: "team-b"},
		"svc-ledger":  {Service: "svc-ledger", Namespace: "payments-uat", NamespaceAuto: "payments-prep", OwnerTeam: "team-a"},
		"svc-batch":   {Service: "svc-batch", OwnerTeam: "team-a"},
	}
	cases := []struct {
		name                 string
		seen                 []string // entity_seen_5m: namespace'te görülen servisler
		ownerTeam, namespace string
		want                 []string
	}{
		{
			// Asıl hata: prep'te yoğun, uat'ta seyrek koşan servis uat
			// süzgecinde görünmeli.
			name: "minority namespace returns the service", seen: []string{"svc-gateway"},
			namespace: "payments-uat", want: []string{"svc-gateway", "svc-ledger"},
		},
		{
			// Çoğunluk namespace'i davranışı değişmez.
			name: "majority namespace unchanged", seen: []string{"svc-gateway", "svc-orders"},
			namespace: "payments-prep", want: []string{"svc-gateway", "svc-orders"},
		},
		{
			// Elle sabitleme telemetri olmadan da korunur (∪).
			name: "pinned namespace kept without telemetry", seen: nil,
			namespace: "payments-uat", want: []string{"svc-ledger"},
		},
		{
			// Telemetri okuması düştüğünde (nil) eski katalog-yalnız davranış.
			name: "telemetry unavailable falls back to catalog", seen: nil,
			namespace: "payments-prep", want: []string{"svc-gateway", "svc-orders"},
		},
		{
			// Katalogda olmayan ama namespace'te görülen servis de gelir.
			name: "seen-only service included", seen: []string{"svc-new"},
			namespace: "payments-uat", want: []string{"svc-ledger", "svc-new"},
		},
		{
			// Takım süzgeci üyelikle kesişir; takım bilgisi yalnız katalogda
			// olduğundan katalogsuz servis düşer.
			name: "team filter intersects membership", seen: []string{"svc-gateway", "svc-new"},
			ownerTeam: "team-a", namespace: "payments-uat", want: []string{"svc-gateway", "svc-ledger"},
		},
		{
			name: "unknown namespace empty", seen: []string{},
			namespace: "payments-dev", want: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := servicesAllowlist(catalog, tc.seen, tc.ownerTeam, "", tc.namespace)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// Namespace'i olmayan servis HİÇBİR namespace süzgecinde görünmez.
func TestServicesAllowlistNoNamespaceNeverMatches(t *testing.T) {
	catalog := map[string]chstore.ServiceMetadata{
		"svc-batch":  {Service: "svc-batch"},
		"svc-orders": {Service: "svc-orders", Namespace: "payments-prep"},
	}
	for _, ns := range []string{"payments-prep", "payments-uat", "payments-dev"} {
		for _, seen := range [][]string{nil, {}, {"svc-orders"}} {
			for _, svc := range servicesAllowlist(catalog, seen, "", "", ns) {
				if svc == "svc-batch" {
					t.Fatalf("namespace=%s seen=%v: namespace'siz servis listelendi", ns, seen)
				}
			}
		}
	}
	// Süzgeç yoksa tüm katalog (takım/namespace boş → çağıran zaten
	// allowlist kurmaz; saf fonksiyon yine de tutarlı).
	if got := servicesAllowlist(catalog, nil, "", "", ""); !reflect.DeepEqual(got, []string{"svc-batch", "svc-orders"}) {
		t.Fatalf("süzgeçsiz: %v", got)
	}
}

func TestNamespaceOptionsIncludeMinority(t *testing.T) {
	cases := []struct {
		name    string
		catalog map[string]chstore.ServiceMetadata
		seen    []string
		want    []string
	}{
		{
			// payments-uat yalnız azınlıkta yaşıyor: katalogda yok, telemetride var.
			name: "minority-only namespace listed",
			catalog: map[string]chstore.ServiceMetadata{
				"svc-gateway": {Namespace: "payments-prep"},
				"svc-orders":  {Namespace: "payments-prep"},
			},
			seen: []string{"payments-prep", "payments-uat"},
			want: []string{"payments-prep", "payments-uat"},
		},
		{
			// service.namespace kaynaklı (k8s dışı) katalog değeri korunur.
			name:    "catalog-only namespace kept",
			catalog: map[string]chstore.ServiceMetadata{"svc-orders": {Namespace: "payments-vm"}},
			seen:    []string{"payments-uat"},
			want:    []string{"payments-uat", "payments-vm"},
		},
		{
			name:    "telemetry unavailable",
			catalog: map[string]chstore.ServiceMetadata{"svc-orders": {Namespace: " payments-prep "}, "svc-batch": {}},
			seen:    nil,
			want:    []string{"payments-prep"},
		},
		{name: "empty", catalog: nil, seen: nil, want: []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := namespaceOptions(tc.catalog, tc.seen); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestApplyNamespaceCounts(t *testing.T) {
	rows := []chstore.ServiceSummary{{Name: "svc-gateway"}, {Name: "svc-orders"}, {Name: "svc-batch"}}
	applyNamespaceCounts(rows, map[string]int{"svc-gateway": 2, "svc-orders": 1})
	if rows[0].NamespaceCount != 2 || rows[1].NamespaceCount != 0 || rows[2].NamespaceCount != 0 {
		t.Fatalf("yalnız >1 yazılmalı: %+v", rows)
	}
}

// Önbellek anahtarı namespace (ve diğer her girdi) değişince değişir.
func TestNamespaceFilterCacheKeys(t *testing.T) {
	bucket := "b1"
	keys := map[string]string{}
	for _, tc := range []struct{ name, namespace, cluster string }{
		{"none", "", ""},
		{"prep", "payments-prep", ""},
		{"uat", "payments-uat", ""},
		{"uat+cluster", "payments-uat", "cl-a"},
	} {
		k := servicesFullKey(true, 50, 0, bucket, "", "errorRate", "desc", "", "", tc.cluster, "", tc.namespace, false, chstore.ServiceDisplayFilters{}, false)
		for prev, pk := range keys {
			if pk == k {
				t.Fatalf("%s ile %s aynı anahtar: %s", tc.name, prev, k)
			}
		}
		keys[tc.name] = k
	}
	t0 := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	a := namespacesCacheKey(t0.Add(-time.Hour), t0)
	b := namespacesCacheKey(t0.Add(-24*time.Hour), t0)
	c := namespacesCacheKey(t0.Add(-time.Hour), t0.Add(time.Hour))
	if a == b || a == c || b == c {
		t.Fatalf("namespaces anahtarı from/to'yu hash'lemiyor: %s %s %s", a, b, c)
	}
	if a != namespacesCacheKey(t0.Add(-time.Hour), t0) {
		t.Fatal("namespaces anahtarı deterministik değil")
	}
}
