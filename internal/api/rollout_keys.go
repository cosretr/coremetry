package api

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// rollout_keys.go — v0.10.200: /api/rollouts* önbellek anahtarları — SAF
// (rollouts_test.go pinler: her girdi anahtarda, ayraç saldırısına
// kapalı — serbest metinler fnvStr ile AYRI AYRI özetlenir, v0.5.187).
// Pencere cacheBucket ile 30 s ızgaraya oturur; limit/topN çağıranda
// kelepçelenmiş gelir (anahtardan ÖNCE — crafted ?limit= ayrı girdi basmasın).
//
// v0.10.984 (Rollouts v2 P2.3) — her anahtar okuma KAYNAĞINI (src: "v1" |
// "v2") taşır: aynı girdiler iki tablodan farklı cevap üretir; kaynak
// çevrildiğinde öteki pod'ların önbelleği eski tablonun cevabını
// servis etmesin.

func rolloutsListKey(src string, f chstore.RolloutFilter, limit int, from, to time.Time) string {
	return fmt.Sprintf("rollouts:list:src=%s:%s:lim=%d:w=%s",
		src, fnvStr(f.ClusterID, f.Namespace, f.Workload, f.Status, f.Kind), limit, cacheBucket(from, to))
}

func rolloutKey(src string, id chstore.RolloutID) string {
	// StartedAt de fnvStr içinde: ailenin tek sabit-genişlik-dışı damgası kalmasın
	return "rollouts:one:src=" + src + ":" + fnvStr(id.ClusterID, id.Namespace, id.Workload, id.Revision, strconv.FormatInt(id.StartedAt.UnixMilli(), 10))
}

// rolloutV2Key — v0.10.984 — 6 parçalı anahtarla tekil okuma (yalnız v2
// kaynağında çağrılır; "v2" öneki 5 parçalı anahtarla çakışmayı keser).
func rolloutV2Key(id chstore.RolloutV2ID) string {
	return "rollouts:one:v2key:" + rolloutV2IDDigest(id)
}

func rolloutV2IDDigest(id chstore.RolloutV2ID) string {
	return fnvStr(id.ClusterID, id.Namespace, id.Kind, id.Workload,
		strconv.FormatInt(id.IncarnationAt.UnixMilli(), 10), strconv.FormatUint(id.Generation, 10))
}

// rolloutDetailKey — pencereler now'a çapalı (önce/sonra RED): 30 s zaman
// kovası anahtarda, yoksa ilk cevap sonsuza dek servis edilirdi.
func rolloutDetailKey(src string, id chstore.RolloutID, now time.Time) string {
	return "rollouts:detail:src=" + src + ":" + fnvStr(id.ClusterID, id.Namespace, id.Workload, id.Revision, strconv.FormatInt(id.StartedAt.UnixMilli(), 10)) +
		":t=" + strconv.FormatInt(now.Truncate(30*time.Second).Unix(), 10)
}

// rolloutV2DetailKey — v0.10.984 — 6 parçalı anahtarlı çekmece.
func rolloutV2DetailKey(id chstore.RolloutV2ID, now time.Time) string {
	return "rollouts:detail:v2key:" + rolloutV2IDDigest(id) + ":t=" + strconv.FormatInt(now.Truncate(30*time.Second).Unix(), 10)
}

func rolloutStatsKey(src, cluster, ns string, topN int, from, to time.Time) string {
	return fmt.Sprintf("rollouts:stats:src=%s:%s:top=%d:w=%s", src, fnvStr(cluster, ns), topN, cacheBucket(from, to))
}

// rolloutRunsKey — v0.10.984 — v1 reconciler koşuları ile v2 dedektör
// koşuları ayrı girdiler.
func rolloutRunsKey(src string) string { return "rollouts:runs:src=" + src }
