package api

// services_namespace.go — v0.10.1140 (operatör-raporlu, test ortamı):
// Services sayfasının namespace süzgeci ve seçenek listesi telemetriden.
//
// Belirti: payments-prep seçilince servisler geliyor, payments-uat seçilince
// hiçbiri — oysa aynı servisler uat'tan da trace gönderiyor.
// Kök neden: service_metadata servis başına TEK namespace tutar (deriver en
// sık değeri yazar, chstore.marginalModes); getServices süzgeci bu tek değere
// eşitlikle bakıyordu → çok-namespace'li bir service.name azınlık
// namespace'inde kayboluyordu; getNamespaces de aynı katalogdan beslendiği
// için yalnız azınlıkta yaşayan namespace listede hiç yoktu.
//
// Çözüm: üyelik = entity_seen_5m'de pencere içinde görülen (namespace,
// servis) çiftleri ∪ katalog değeri (elle sabitlenmiş ya da türetilmiş
// service_metadata.Namespace — elle atama ASLA kaybolmaz; service.namespace
// kaynaklı, k8s dışı namespace'ler de buradan gelir). Süzgeç hâlâ serviceIn
// allowlist'ine çözülür → MV hızlı yolu kapanmaz. Telemetri okuması düşerse
// (entity_seen_5m yok) fail-open: eski katalog-yalnız davranış.
//
// Metrik notu: service_summary_5m'de namespace boyutu YOK; satırın RED
// değerleri tüm namespace'lerin TOPLAMIdır. Süzgeç açıkken servis birden
// çok namespace'te koşuyorsa satıra NamespaceCount yazılır, FE "metrikler
// toplamdır" rozetini gösterir (DECISIONS 2026-10-09).

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// namespacesCacheKey — /api/namespaces anahtarı. Girdiler yalnız from/to
// (30 s kovaya hizalı); v2 öneki, kaynak değişti (katalog → telemetri ∪
// katalog): yuvarlanan dağıtımda eski pod'un katalog-yalnız listesi aynı
// anahtardan servis edilmesin.
func namespacesCacheKey(from, to time.Time) string {
	return "namespaces:v2:" + cacheBucket(from, to)
}

// namespaceOptions — SAF: telemetride görülen namespace'ler ∪ katalog
// değerleri; boşlar atılır, tekil, alfabetik. Nil yerine [].
func namespaceOptions(catalog map[string]chstore.ServiceMetadata, seen []string) []string {
	set := map[string]struct{}{}
	for _, ns := range seen {
		if ns = strings.TrimSpace(ns); ns != "" {
			set[ns] = struct{}{}
		}
	}
	for _, md := range catalog {
		if ns := strings.TrimSpace(md.Namespace); ns != "" {
			set[ns] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// servicesAllowlist — SAF: takım + namespace süzgeçlerinin servis listesi.
// namespace boş değilse üyelik = katalog değeri == namespace ∪ seen
// (telemetri). Takım süzgeci verildiyse katalog satırı ŞART (takım bilgisi
// yalnız katalogda). Sıralı (deterministik IN listesi).
func servicesAllowlist(catalog map[string]chstore.ServiceMetadata, seen []string, ownerTeam, sreTeam, namespace string) []string {
	seenSet := make(map[string]bool, len(seen))
	for _, s := range seen {
		seenSet[s] = true
	}
	candidates := map[string]bool{}
	for name := range catalog {
		candidates[name] = true
	}
	for name := range seenSet {
		candidates[name] = true
	}
	out := []string{}
	for name := range candidates {
		md, inCatalog := catalog[name]
		if (ownerTeam != "" || sreTeam != "") && !inCatalog {
			continue
		}
		if ownerTeam != "" && md.OwnerTeam != ownerTeam {
			continue
		}
		if sreTeam != "" && md.SRETeam != sreTeam {
			continue
		}
		if namespace != "" && md.Namespace != namespace && !seenSet[name] {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// servicesFilterAllowlist — getServices'in takım/namespace süzgeci.
// filtered=false → süzgeç yok (serviceIn nil, tüm servisler).
// filtered=true ve boş liste → çağıran boş sayfa döner (spans sorgusu yok).
func (s *Server) servicesFilterAllowlist(ctx context.Context, from, to time.Time, cluster, ownerTeam, sreTeam, namespace string) (serviceIn []string, filtered bool, err error) {
	if ownerTeam == "" && sreTeam == "" && namespace == "" {
		return nil, false, nil
	}
	catalog, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		return nil, true, fmt.Errorf("catalog: %w", err)
	}
	var seen []string
	if namespace != "" {
		if seen, err = s.store.ServicesSeenInNamespace(ctx, namespace, cluster, from, to); err != nil {
			log.Printf("[services] namespace üyeliği okunamadı, katalog-yalnız: %v", err)
			seen = nil
		}
	}
	return servicesAllowlist(catalog, seen, ownerTeam, sreTeam, namespace), true, nil
}

// applyNamespaceCounts — SAF: count > 1 olan satırlara NamespaceCount yaz.
func applyNamespaceCounts(rows []chstore.ServiceSummary, counts map[string]int) {
	for i := range rows {
		if n := counts[rows[i].Name]; n > 1 {
			rows[i].NamespaceCount = n
		}
	}
}

// applyServiceNamespaceCounts — namespace süzgeci açıkken sayfadaki
// servislerin kaç namespace'te koştuğu (sınırlı: sayfa ≤500 ad, ORDER BY
// öneki okuması). Süzgeç yoksa sorgu YOK; hata yumuşak (rozet çıkmaz).
func (s *Server) applyServiceNamespaceCounts(ctx context.Context, rows []chstore.ServiceSummary, namespace string, from, to time.Time) {
	if namespace == "" || len(rows) == 0 {
		return
	}
	names := make([]string, len(rows))
	for i := range rows {
		names[i] = rows[i].Name
	}
	counts, err := s.store.ServiceNamespaceCounts(ctx, names, from, to)
	if err != nil {
		return
	}
	applyNamespaceCounts(rows, counts)
}
