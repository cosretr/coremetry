package thanos

// apiserver_url.go — v0.10.956 — Rollouts v2 P1.3 (docs/rollouts/v2-audit.md
// §3.2, §5.3; operatör kararı 7, 2026-09-26).
//
// Argo CD `argocd_app_info{dest_server}` hedef cluster'ı API server URL'i
// ile söyler; değer Argo cluster Secret'ında nasıl yazıldıysa öyle gelir —
// port, sondaki `/` ve harf büyüklüğü kayıttan kayda değişir. Remote Cluster
// kaydının `apiServerUrls` listesiyle eşleşme ANCAK iki taraf aynı kanonik
// biçime indirgenirse güvenilir. Bu yüzden TEK saf normaliser: yazımda (PUT
// → ReconcileClusterSettings) ve P3 eşleyicisinde aynı fonksiyon.
//
// Neden `net/url` + elle kurulan çıktı, `u.String()` DEĞİL: String() yolu,
// userinfo'yu ve boş sorgu işaretini geri basar; kanonik anahtar yalnız
// şema + host + port taşımalı.
//
// Neden hata metninde ham girdi YOK: `url.Parse` hatası girdiyi aynen
// yankılar; `https://user:parola@…` biçimi hatalıysa parola audit/400
// gövdesine sızardı. Çağıran konumu (kayıt adı + sıra) ekler.

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// DefaultAPIServerPort — v0.10.956 — port yazılmamış URL'e eklenen AÇIK port
// (OpenShift API server varsayılanı; karar 7). Kanonik anahtar bir
// bağlanma adresi değil, eşleme anahtarıdır: iki taraf da aynı kuralla
// normalise edildiği sürece `https://kubernetes.default.svc` gibi 443'te
// dinleyen adresler de doğru eşleşir.
const DefaultAPIServerPort = "6443"

// InClusterAPIServerURL — v0.10.956 — Argo CD'nin "hub'ın kendisi" hedefi
// (argo-cd declarative-setup: in-cluster). Remote Cluster'ın apiServerUrls'üne
// YAZILMAZ (checkClusterUniqueness reddeder): iki hub varken (audit §5.6)
// "hub'ın kendisi" ancak instance'ın hubClusterId'siyle çözülür (argocd hubs[]).
const InClusterAPIServerURL = "https://kubernetes.default.svc"

// inClusterAPIServerHosts — v0.10.956 — küme-içi hedefin host yazımları
// (kısa ad + cluster.local FQDN). Port yüklemde BAKILMAZ: servis gerçekte
// 443'te dinler, :6443 yalnız kanonik anahtarın varsayılanı; `…svc`,
// `…svc:443` ve `…svc.cluster.local` aynı hedeftir. Tanım lane R3'ün
// argocd.IsInClusterServer'ı ile aynı (inceleme: iki paket farklı cevap
// veriyordu, thanos PUT'u argocd PUT'unun reddedeceği yapılandırmayı
// kabul ediyordu).
var inClusterAPIServerHosts = map[string]bool{
	"kubernetes.default.svc":               true,
	"kubernetes.default.svc.cluster.local": true,
}

// NormalizeAPIServerURL — v0.10.956 — saf; tablo-testli
// (apiserver_url_test.go). Kurallar: boşluk kırpılır; şema http|https;
// şema + host küçük harf; sondaki `/` atılır; port yoksa `:6443`; yol
// yalnız "" ya da "/"; userinfo, sorgu, fragment reddedilir. Çıktı
// idempotent: Normalize(Normalize(x)) == Normalize(x).
func NormalizeAPIServerURL(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", errors.New("boş API server URL")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("API server URL ayrıştırılamadı (biçim: https://<host>[:port])")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("API server URL şeması http ya da https olmalı")
	}
	if u.Opaque != "" {
		return "", errors.New("API server URL'de host yok (biçim: https://<host>[:port])")
	}
	if u.User != nil {
		return "", errors.New("API server URL kullanıcı bilgisi (user@) taşıyamaz; kimlik bilgisi URL'e yazılmaz")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", errors.New("API server URL'de host yok (biçim: https://<host>[:port])")
	}
	if u.RawQuery != "" || u.ForceQuery {
		return "", errors.New("API server URL sorgu (?…) taşıyamaz")
	}
	if u.Fragment != "" || strings.Contains(s, "#") {
		return "", errors.New("API server URL fragment (#…) taşıyamaz")
	}
	if u.Path != "" && u.Path != "/" {
		return "", errors.New("API server URL yol taşıyamaz (yalnız şema + host + port)")
	}
	port := DefaultAPIServerPort
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("API server URL portu 1-65535 aralığında olmalı")
		}
		port = strconv.Itoa(n)
	}
	return scheme + "://" + net.JoinHostPort(host, port), nil
}

// IsInClusterAPIServerURL — v0.10.956 — değer (normalise edilince) Argo'nun
// küme-içi hedefi mi: şema https, host kubernetes.default.svc ya da
// kubernetes.default.svc.cluster.local, port fark etmez. Geçersiz URL false.
// Kanonik ANAHTAR yine porta göre ayrışır (eşleme anahtarı, karar 7);
// tekillik (checkClusterUniqueness) tüm yazımları tek "hub'ın kendisi"
// sayar.
func IsInClusterAPIServerURL(raw string) bool {
	n, err := NormalizeAPIServerURL(raw)
	if err != nil || !strings.HasPrefix(n, "https://") {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimPrefix(n, "https://"))
	return err == nil && inClusterAPIServerHosts[host]
}
