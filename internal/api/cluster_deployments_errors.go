package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"
)

// v0.10.2367 — GET /api/clusters/deployments hata sınıfı (operatör: servis
// sayfasında tek bir kümede 500, gövde ~49 bayt, neden yakalanmamış).
//
// Uç tek bir dış sistemi (o kümenin Thanos'u) 10 sn bağlamla sorgular; oradan
// gelen hata Coremetry arızası değildir:
//   - zaman aşımı sınıfı (isTimeoutClass)        → 504 {"reason":"timeout"}
//   - diğer her Thanos hatası (bağlantı, HTTP 5xx, yetki, çözümleme) → 502
//     {"reason":"upstream"}
//   - istemci iptali → writeErr'in 499'u (değişmedi).
// Ham hata metni gövdeye değil loga gider: anahtar (küme+namespace) başına
// dakikada bir satır. Başarılar serveCached'in 60 sn'lik katmanında kalır;
// hata süreç içi 15 sn olumsuz önbelleğe yazılır ki yavaş küme her sayfa
// yüklemesinde yeniden 10 sn yorulmasın (?refresh=1 atlar).

const (
	clusterDepNegTTL = 15 * time.Second
	clusterDepLogGap = time.Minute
)

type clusterDepNeg struct {
	until  time.Time
	code   int
	reason string
}

var clusterDepErrs = struct {
	sync.Mutex
	neg     map[string]clusterDepNeg
	lastLog map[string]time.Time
}{neg: map[string]clusterDepNeg{}, lastLog: map[string]time.Time{}}

// clusterDepNow — test kancası.
var clusterDepNow = time.Now

// classifyClusterDepErr — SAF: hata → HTTP kodu + kısa neden. 0 = istemci iptali.
func classifyClusterDepErr(err error) (int, string) {
	switch {
	case errors.Is(err, context.Canceled):
		return 0, ""
	case isTimeoutClass(err):
		return http.StatusGatewayTimeout, "timeout"
	default:
		return http.StatusBadGateway, "upstream"
	}
}

func clusterDepReasonText(reason string) string {
	if reason == "timeout" {
		return "cluster workload query timed out"
	}
	return "cluster workload query failed"
}

// clusterDepNegGet — süresi dolmamış olumsuz girdi.
func clusterDepNegGet(key string) (clusterDepNeg, bool) {
	clusterDepErrs.Lock()
	defer clusterDepErrs.Unlock()
	e, ok := clusterDepErrs.neg[key]
	if !ok {
		return e, false
	}
	if !clusterDepNow().Before(e.until) {
		delete(clusterDepErrs.neg, key)
		return e, false
	}
	return e, true
}

// clusterDepFail — olumsuz girdiyi yazar, gerekiyorsa loglar (anahtar başına
// dakikada bir). Dönüş: logged.
func clusterDepFail(key, cluster, namespace string, code int, reason string, err error) bool {
	now := clusterDepNow()
	clusterDepErrs.Lock()
	defer clusterDepErrs.Unlock()
	// Harita sınırsız büyümesin: süresi dolanları ara sıra süpür.
	if len(clusterDepErrs.neg) > 1024 {
		for k, e := range clusterDepErrs.neg {
			if !now.Before(e.until) {
				delete(clusterDepErrs.neg, k)
			}
		}
	}
	clusterDepErrs.neg[key] = clusterDepNeg{until: now.Add(clusterDepNegTTL), code: code, reason: reason}
	logKey := cluster + "\x00" + namespace
	if last, ok := clusterDepErrs.lastLog[logKey]; ok && now.Sub(last) < clusterDepLogGap {
		return false
	}
	if len(clusterDepErrs.lastLog) > 1024 {
		for k, t := range clusterDepErrs.lastLog {
			if now.Sub(t) >= clusterDepLogGap {
				delete(clusterDepErrs.lastLog, k)
			}
		}
	}
	clusterDepErrs.lastLog[logKey] = now
	log.Printf("[api] cluster deployments cluster=%s namespace=%s reason=%s: %v", cluster, namespace, reason, err)
	return true
}

func writeClusterDepErr(w http.ResponseWriter, code int, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": clusterDepReasonText(reason), "reason": reason})
}

// serveClusterDeployments — serveCached'in çekirdeği (cachedJSON) + yukarıdaki
// hata sınıfı ve olumsuz önbellek.
func (s *Server) serveClusterDeployments(w http.ResponseWriter, r *http.Request, key, cluster, namespace string, fn func(ctx context.Context) (any, error)) {
	start := time.Now()
	bypass := r.URL.Query().Get("refresh") == "1"
	if !bypass {
		if e, ok := clusterDepNegGet(key); ok {
			writeClusterDepErr(w, e.code, e.reason)
			return
		}
	}
	body, tier, err := s.cachedJSON(r.Context(), key, 60*time.Second, bypass, fn)
	if err != nil {
		code, reason := classifyClusterDepErr(err)
		if code == 0 {
			writeErr(w, err) // 499, gövdesiz
			return
		}
		clusterDepFail(key, cluster, namespace, code, reason, err)
		writeClusterDepErr(w, code, reason)
		return
	}
	setServerTiming(w, r, start, tier)
	writeCacheHit(w, tier, body)
}
