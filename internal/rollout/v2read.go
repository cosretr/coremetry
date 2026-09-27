package rollout

import "strings"

// v2read.go — v0.10.984 — Rollouts v2 P2.3 okuma yolu sözlüğü
// (docs/rollouts/v2-audit.md §2.2 `/api/rollouts` satırı, §2.4, §10.3.1
// "Status vocabulary").
//
// rollouts.source="v2" iken /api/rollouts* rollout_events okur ama cevap
// v1 SÖZLÜĞÜNDE kalır: ?status= bağlantıları, Durum süzgeci, rozet tonları
// ve istatistik alanları (completed / inProgress / stalled) değişmez.
// Eşleme TEK yerde (bu dosya) — API, istatistik ve servis okuması aynı
// tabloyu kullanır; v2read_test.go tabloyu çiviler.
//
// Saf: I/O yok, saat yok.

// V2StatusToV1 — v0.10.984 — rollout_events.status → v1 API durumu.
// progressing→in_progress, succeeded→completed, stuck→stalled;
// rolled_back / superseded aynen. Sözlük dışı değer aynen geçer (okuyucu
// uydurmaz; FE bilinmeyeni nötr rozetle gösterir).
func V2StatusToV1(s string) string {
	switch s {
	case V2StatusProgressing:
		return StatusInProgress
	case V2StatusSucceeded:
		return StatusCompleted
	case V2StatusStuck:
		return StatusStalled
	}
	return s
}

// V1StatusToV2 — v0.10.984 — ?status= süzgecinin rollout_events değeri
// (V2StatusToV1'in tersi). v2 yazımı da kabul edilir (elle yazılmış
// ?status=succeeded boş liste yerine aynı satırları getirsin); başka her
// değer aynen geçer ve v1'deki gibi hiçbir satırla eşleşmez.
func V1StatusToV2(s string) string {
	switch s {
	case StatusInProgress:
		return V2StatusProgressing
	case StatusCompleted:
		return V2StatusSucceeded
	case StatusStalled:
		return V2StatusStuck
	}
	return s
}

// SplitImageRef — v0.10.984 — "repo[:tag]" / "repo@digest" /
// "repo:tag@digest" → (repo, tag). Önce "@digest" soyulur; tag ayracı
// kalanın son '/'dan SONRAKİ ilk ':'ı (registry portu "reg:5000/app" tag
// sanılmasın). Tag varsa tag döner (digest'e sabitlenmiş GitOps imajında
// sürüm okunur kalsın), yoksa digest. Boş → ("", "").
// v1 satırının image / image_tag çiftini v2'nin tam referans dizisinden
// kurmak için (rollout_events.images kube_pod_container_info `image`).
// İnceleme: "reg.io/app:1.2@sha256:…" eskiden repo="reg.io/app:1.2",
// tag=digest oluyordu (sürüm çipi / işaretler sha256 gösterirdi).
func SplitImageRef(ref string) (repo, tag string) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", ""
	}
	digest := ""
	if i := strings.Index(ref, "@"); i >= 0 {
		ref, digest = ref[:i], ref[i+1:]
	}
	slash := strings.LastIndex(ref, "/")
	if i := strings.Index(ref[slash+1:], ":"); i >= 0 {
		if t := ref[slash+1+i+1:]; t != "" {
			return ref[:slash+1+i], t
		}
		ref = ref[:slash+1+i]
	}
	return ref, digest
}

// PrimaryImagePair — v0.10.984 — v1'in tek imaj alanına (image / prevImage)
// giden referans ÇİFTİ, aynı repodan. images kube_pod_container_info'nun
// BÜTÜN konteynerleri (sidecar dahil, sıralı): sıradaki ilk referansı
// almak istio/linkerd/log-shipper sidecar'ını ("docker.io/istio/proxyv2"
// 'harbor.corp/...'dan önce sıralanır) sürüm sanırdı — sürüm çipi
// "1.20.1 → 1.20.1" ve VersionConstant=true olurdu (İnceleme). Seçim sırası:
//  1. iki dizide de olan ve tag'i DEĞİŞEN repo (yayılan imaj budur);
//  2. repo değişimi: yalnız images'ta olan repo, önceki tarafı yalnız
//     prevImages'ta kalan repo;
//  3. son yol parçası iş yükü adına eşit, sonra onu içeren / onun içinde
//     geçen repo (sidecar enjeksiyonu yeni repo getirse de uygulama kalır);
//  4. yalnız images'ta olan repo (önceki taraf boş);
//  5. ilk dolu referans.
//
// Önceki taraf aynı repodan (yoksa ilk dolu önceki referans). Saf;
// v2read_test.go tablosu çiviler.
func PrimaryImagePair(images, prevImages []string, workload string) (cur, prev string) {
	type ref struct{ full, repo, tag string }
	parse := func(list []string) []ref {
		var out []ref
		for _, im := range list {
			if s := strings.TrimSpace(im); s != "" {
				r, t := SplitImageRef(s)
				out = append(out, ref{s, r, t})
			}
		}
		return out
	}
	ci, pi := parse(images), parse(prevImages)
	if len(ci) == 0 && len(pi) == 0 {
		return "", ""
	}
	byRepo := func(list []ref, repo string) (ref, bool) {
		for _, r := range list {
			if r.repo == repo {
				return r, true
			}
		}
		return ref{}, false
	}
	firstFull := func(list []ref) string {
		if len(list) == 0 {
			return ""
		}
		return list[0].full
	}
	pairOf := func(c ref) (string, string) {
		if p, ok := byRepo(pi, c.repo); ok {
			return c.full, p.full
		}
		return c.full, firstFull(pi)
	}
	// 1. tag'i değişen ortak repo.
	for _, c := range ci {
		if p, ok := byRepo(pi, c.repo); ok && p.tag != c.tag {
			return c.full, p.full
		}
	}
	var added, removed []ref
	for _, c := range ci {
		if _, ok := byRepo(pi, c.repo); !ok {
			added = append(added, c)
		}
	}
	for _, p := range pi {
		if _, ok := byRepo(ci, p.repo); !ok {
			removed = append(removed, p)
		}
	}
	// 2. repo değişimi (eklenen + çıkan repo).
	if len(added) > 0 && len(removed) > 0 {
		return added[0].full, removed[0].full
	}
	// 3. iş yükü adıyla eşleşen repo (önce tam, sonra içerme).
	if w := strings.ToLower(strings.TrimSpace(workload)); w != "" {
		seg := func(repo string) string { return strings.ToLower(repo[strings.LastIndex(repo, "/")+1:]) }
		for _, exact := range []bool{true, false} {
			for _, c := range ci {
				s := seg(c.repo)
				if s != "" && (s == w || (!exact && (strings.Contains(w, s) || strings.Contains(s, w)))) {
					return pairOf(c)
				}
			}
		}
	}
	// 4. yalnız images'ta olan repo (önceki tarafta karşılığı yok).
	if len(added) > 0 && len(pi) > 0 {
		return added[0].full, ""
	}
	// 5. ilk dolu referans.
	if len(ci) > 0 {
		return pairOf(ci[0])
	}
	return "", firstFull(pi)
}
