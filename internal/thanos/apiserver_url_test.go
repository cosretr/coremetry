package thanos

import (
	"strings"
	"testing"
)

// v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md §3.2, §5.3; karar 7).
//
// Sözleşme: Argo `dest_server` yazımı kayıttan kayda değişir (port, sondaki
// `/`, büyük/küçük harf). Remote Cluster `apiServerUrls` ile eşleşme ANCAK iki
// taraf aynı kanonik biçime indirgenirse güvenilir; bu yüzden tek bir saf
// normaliser var ve hem yazımda (PUT) hem ileride okuma tarafında (P3
// eşleyici) kullanılır:
//   - boşluk kırpılır; şema http|https zorunlu;
//   - şema + host küçük harf; sondaki `/` atılır;
//   - port yoksa AÇIK `:6443` (OpenShift API varsayılanı; karar 7);
//   - yol yalnız "" ya da "/" olabilir; userinfo, sorgu ve fragment reddedilir;
//   - normalise edilmiş değer yeniden normalise edilince DEĞİŞMEZ (idempotent).

func TestNormalizeAPIServerURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://api.cluster-a.example.invalid:6443", "https://api.cluster-a.example.invalid:6443"},
		{"  HTTPS://API.Cluster-A.Example.Invalid:6443/  ", "https://api.cluster-a.example.invalid:6443"},
		{"https://api.cluster-a.example.invalid", "https://api.cluster-a.example.invalid:6443"},
		{"https://api.cluster-a.example.invalid/", "https://api.cluster-a.example.invalid:6443"},
		{"http://api.cluster-b.example.invalid:8443", "http://api.cluster-b.example.invalid:8443"},
		{"https://api.cluster-b.example.invalid:443/", "https://api.cluster-b.example.invalid:443"},
		{"https://kubernetes.default.svc", "https://kubernetes.default.svc:6443"},
		{"https://10.0.0.1", "https://10.0.0.1:6443"},
		{"https://[FD00::1]", "https://[fd00::1]:6443"},
		{"https://[fd00::1]:443", "https://[fd00::1]:443"},
	}
	for _, tc := range ok {
		got, err := NormalizeAPIServerURL(tc.in)
		if err != nil {
			t.Fatalf("NormalizeAPIServerURL(%q) beklenmedik hata: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("NormalizeAPIServerURL(%q) = %q, beklenen %q", tc.in, got, tc.want)
		}
		again, err := NormalizeAPIServerURL(got)
		if err != nil || again != got {
			t.Fatalf("idempotent olmalı: %q → %q (%v)", got, again, err)
		}
	}

	bad := []struct{ in, why string }{
		{"", "boş"},
		{"   ", "yalnız boşluk"},
		{"api.cluster-a.example.invalid:6443", "şema yok"},
		{"ftp://api.cluster-a.example.invalid", "şema http/https değil"},
		{"https://", "host yok"},
		{"https:api.cluster-a.example.invalid", "opak URL"},
		{"https://user:secret@api.cluster-a.example.invalid:6443", "userinfo"},
		{"https://user@api.cluster-a.example.invalid", "userinfo (parolasız)"},
		{"https://api.cluster-a.example.invalid:6443/k8s", "yol"},
		{"https://api.cluster-a.example.invalid:6443//", "çift eğik çizgi yolu"},
		{"https://api.cluster-a.example.invalid:6443?x=1", "sorgu"},
		{"https://api.cluster-a.example.invalid:6443#frag", "fragment"},
		{"https://api.cluster-a.example.invalid:0", "port 0"},
		{"https://api.cluster-a.example.invalid:70000", "port aralık dışı"},
		{"https://api.cluster-a.example.invalid:abc", "port sayı değil"},
	}
	for _, tc := range bad {
		got, err := NormalizeAPIServerURL(tc.in)
		if err == nil {
			t.Fatalf("NormalizeAPIServerURL(%q) reddedilmeliydi (%s), alınan %q", tc.in, tc.why, got)
		}
		// Hata metni operatöre gider: userinfo'daki parola geri YANKILANMAZ.
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("hata metni userinfo parolasını sızdırmamalı: %v", err)
		}
	}
}

// v0.10.956 — inceleme: küme-içi yüklem PORT'tan ve .cluster.local
// FQDN'inden bağımsız (servis gerçekte 443'te dinler; :6443 yalnız kanonik
// anahtarın varsayılanı). Aynı tanım lane R3'ün argocd.IsInClusterServer'ı
// ile hizalı — iki paket aynı adrese farklı cevap vermesin.
func TestIsInClusterAPIServerURL(t *testing.T) {
	for _, in := range []string{
		"https://kubernetes.default.svc",
		"https://kubernetes.default.svc/",
		"HTTPS://Kubernetes.Default.Svc",
		"https://kubernetes.default.svc:6443",
		"https://kubernetes.default.svc:443",
		"https://kubernetes.default.svc.cluster.local",
		"https://kubernetes.default.svc.cluster.local:443/",
	} {
		if !IsInClusterAPIServerURL(in) {
			t.Fatalf("%q hub'ın küme-içi adresi sayılmalı", in)
		}
	}
	for _, in := range []string{
		"https://api.cluster-a.example.invalid:6443",
		"http://kubernetes.default.svc",
		"https://kubernetes.default.svc.example.invalid",
		"https://kubernetes.default",
		"not a url",
		"",
	} {
		if IsInClusterAPIServerURL(in) {
			t.Fatalf("%q küme-içi adres sayılmamalı", in)
		}
	}
}
