package thanos

// worker_query.go — v0.10.955 — ROLLOUTS v2 İŞÇİ OKUYUCUSU (P1.1).
// Kaynak: docs/rollouts/v2-audit.md §3.4, §4.10, §5.4 + operatör onaylı
// kararlar 1–2 (2026-09-26).
//
// ── NE ───────────────────────────────────────────────────────────────────
//
// Arka plan işçileri (P2 KSM dedektörü, P3 Argo metrik işçisi) için TEK
// dışa açık giriş: WorkerQuery. Konsolun taşımasını (consoleEval →
// consoleCall: POST form, akışlı seri tavanı + toplam sayım, açık
// response_too_large, JSON hata gövdesi, URL sızıntı kuralı) AYNEN
// kullanır. Konsol yeniden platformlanmaz; doQuery ve promapi'ye
// dokunulmaz (promapi.go:21-26 ev kuralının lafzı korunur).
//
// ── NEDEN AYRI SINIRLAR ──────────────────────────────────────────────────
//
// ConsoleLimits tavanları (2000 seri / 128 MiB / 120 s) insan-etkileşimli
// konsol içindir; argocd_app_info (~40k seri) ve büyük cluster'ların RS
// serileri oradan geçemez. WorkerLimits kendi normalizasyonunu taşır:
// ≤ 50 000 seri, ≤ 64 MiB gövde, 30 s varsayılan / 45 s tavan (karar 2).
// ConsoleLimits.normalized() burada ÇAĞRILMAZ — çağrılsa seri tavanı
// 2000'e inerdi; konsolun kendi tavanları değişmez. Her gövde işçi
// belleğinden geçer: bellek sınırı bu tavanlardır.
//
// ── FAIL-CLOSED TOKEN ────────────────────────────────────────────────────
//
// Konsol ve doQuery çözülemeyen TokenRef'te başlıksız istek gönderir
// (effectiveToken saklı token'a düşmez, BOŞ döner; console.go:516). İşçi
// GÖNDERMEZ: TokenRef dolu + çözülmemiş → ErrWorkerTokenUnresolved, HTTP
// turu YOK. authType'tan bağımsız: ref operatörün kimlik niyetidir;
// çözülmeyen ref bir yapılandırma hatasıdır ve işçi koşu kaydında
// görünmeli. Kimliksiz sorgu oauth-proxy giriş sayfasına ya da başka bir
// yetki görünümüne düşebilir; işçi bunu "seri yok" diye okuyup yanlış diff
// üretmemeli. Denetlenen token yerel kopyaya sabitlenir; consoleCall onu
// yeniden OKUMAZ, bu yüzden denetim ile istek arasındaki 30 s yenileme
// (Configure) başlıksız bir isteğe yol açamaz.
//
// ── THANOS PARAMETRELERİ ─────────────────────────────────────────────────
//
// dedup ve partial_response HER çağrıda açıkça gönderilir: varsayılan
// dedup=true (HA replikaları tek seriye iner; querier varsayılanına
// güvenilmez), partial_response=false (eksik store sessiz boşluk değil
// hata olur). timeout=<ayar>s. Yalnız anlık sorgu (/api/v1/query): §4.11
// ve §5.4 okuma biçimleri anlıktır (range fonksiyonları, ör.
// increase(argocd_app_sync_total[1h]), anlık sorgunun İÇİNDE); aralık
// varyantı bilerek yok.
//
// ── KISMİ SONUÇ ──────────────────────────────────────────────────────────
//
// Herhangi bir warning ⇒ Partial() (Thanos kısmi yanıtı warnings ile
// bildirir). Kural (§4.10): Truncated YA DA Partial ise çağıran o shard'ın
// diff'ini ATLAR ve koşuyu partial işaretler; kısmi okumadan yokluk
// çıkarılmaz.
//
// ── CLUSTER ÇÖZÜMÜ ───────────────────────────────────────────────────────
//
// Yalnız id (ClusterByID: ETKİN kayıt; EffectiveID — §1.3 risk 5: işçiler
// daima EffectiveID ile anahtarlanır). Ad kabul edilmez: işçi tabloları id
// taşır, ad yeniden adlandırılabilir. Cluster matcher enjeksiyonu konsolla
// aynı: ClusterConfig.EffectiveQuery (cluster_matcher.go değişmez).
// Namespace kalkanı ÇAĞIRANINDIR: ifadeye NamespaceMatcher eklenir
// (promql.go; §4.9).

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// v0.10.955 — İşçi okuyucu tavanları (karar 2). Seri ve gövde için varsayılan =
// tavan. Dışa açık: argocd ayar blobunun reader{…} alanı aynı tavanlara
// kırpar (tek doğruluk kaynağı).
const (
	WorkerMaxSeries       = 50000
	WorkerMaxBodyMiB      = 64
	WorkerDefaultTimeoutS = 30
	WorkerMaxTimeoutS     = 45
)

// WorkerLimits — v0.10.955 — işçi çağrısı başına sınırlar. Sıfır değer güvenli
// varsayılandır: 50 000 seri, 64 MiB, 30 s, dedup=true,
// partial_response=false. Sıfır/negatif → varsayılan, tavan üstü → tavan.
type WorkerLimits struct {
	MaxSeries  int // akışlı seri tavanı; fazlası yalnız sayılır (Truncated + TotalSeries)
	MaxBodyMiB int // yanıt gövdesi tavanı → response_too_large
	TimeoutS   int // adanmış istemci + bağlam son tarihi + Thanos timeout=
	// Dedup — nil → true. Açık false yalnız bilinçli HA-ham okuma için.
	Dedup           *bool
	PartialResponse bool // partial_response= (varsayılan false)
}

func (l WorkerLimits) normalized() WorkerLimits {
	switch {
	case l.MaxSeries <= 0, l.MaxSeries > WorkerMaxSeries:
		l.MaxSeries = WorkerMaxSeries
	}
	switch {
	case l.MaxBodyMiB <= 0, l.MaxBodyMiB > WorkerMaxBodyMiB:
		l.MaxBodyMiB = WorkerMaxBodyMiB
	}
	switch {
	case l.TimeoutS <= 0:
		l.TimeoutS = WorkerDefaultTimeoutS
	case l.TimeoutS > WorkerMaxTimeoutS:
		l.TimeoutS = WorkerMaxTimeoutS
	}
	if l.Dedup == nil {
		on := true
		l.Dedup = &on
	}
	return l
}

// consoleLimits — v0.10.955 — normalize edilmiş işçi sınırları → konsol taşımasının
// sınır tipi. ConsoleLimits.normalized()'dan GEÇMEZ (bkz. dosya başı).
func (l WorkerLimits) consoleLimits() ConsoleLimits {
	return ConsoleLimits{
		Timeout:         time.Duration(l.TimeoutS) * time.Second,
		MaxSeries:       l.MaxSeries,
		MaxBodyBytes:    int64(l.MaxBodyMiB) << 20,
		PartialResponse: l.PartialResponse,
	}
}

// v0.10.955 — İşçi girişinin HTTP turundan ÖNCEKİ tipli retleri (errors.Is ile).
// Taşıma/upstream hataları ise *ConsoleError (Type ile sınıflanır).
var (
	ErrWorkerClusterUnavailable = errors.New("thanos worker: cluster not found, not enabled or has no URL")
	ErrWorkerTokenUnresolved    = errors.New("thanos worker: cluster token reference does not resolve")
)

// WorkerQuery — v0.10.955 — işçi anlık sorgusu (POST /api/v1/query) clusterID'nin
// ETKİN kaydına. Hata: ErrWorkerClusterUnavailable / ErrWorkerTokenUnresolved
// (sarılı; istek YOK) ya da *ConsoleError (bad_data, timeout, canceled,
// unavailable, internal, response_too_large…). Sonuçta Truncated ya da
// Partial() doğruysa çağıran diff'i atlar.
func (s *Service) WorkerQuery(ctx context.Context, clusterID, expr string, lim WorkerLimits) (*ConsoleResult, error) {
	lim = lim.normalized()
	cl := lim.consoleLimits()
	if err := ctx.Err(); err != nil {
		// Kapanan işçi (ya da süresi geçmiş tik) dial etmez.
		return nil, workerError(ctx, consoleCtxError(ctx, ctx, cl, err), lim)
	}
	if strings.TrimSpace(expr) == "" {
		return nil, consoleBadData("query is empty")
	}
	if s == nil {
		return nil, fmt.Errorf("%w (%q)", ErrWorkerClusterUnavailable, clusterID)
	}
	c, ok := s.ClusterByID(clusterID)
	if !ok || strings.TrimSpace(c.URL) == "" {
		return nil, fmt.Errorf("%w (%q)", ErrWorkerClusterUnavailable, clusterID)
	}
	tok := s.effectiveTokenFor(c)
	if c.TokenRef != "" && tok == "" {
		return nil, fmt.Errorf("%w (cluster %q)", ErrWorkerTokenUnresolved, c.Name)
	}
	// v0.10.955 — DENETLENEN token GÖNDERİLEN token'dır: yerel kopyaya
	// sabitlenir (TokenRef boş → consoleCall'daki effectiveTokenFor saklı
	// alanı, yani bu değeri döndürür). Sabitlenmezse consoleCall token'ı
	// AYRI bir kilitle yeniden okur; arada 30 s yenilemenin Configure'u ref'i
	// çözülmez yaparsa istek BAŞLIKSIZ giderdi (fail-closed delinir; yük
	// testiyle yakalandı: TestWorkerQueryTokenCheckedIsTokenSent).
	c.Token, c.TokenRef = tok, ""
	eff := c.EffectiveQuery(expr)
	form := url.Values{
		"query": {eff},
		"dedup": {strconv.FormatBool(*lim.Dedup)},
	}
	// consoleEval partial_response + timeout'u cl'den yazar, akışlı çözer,
	// konumu geri çevirir; dedup form'da kalır.
	res, err := s.consoleEval(ctx, c, "/api/v1/query", form, cl, expr, eff)
	if err != nil {
		return nil, workerError(ctx, err, lim)
	}
	return res, nil
}

// Partial — v0.10.955 — Thanos kısmi yanıtı (en az bir warning). Konsol
// davranışı değişmez (alan değil yöntem: konsolun JSON'una girmez).
// infos kısmilik değildir.
func (r *ConsoleResult) Partial() bool { return r != nil && len(r.Warnings) > 0 }

// workerError — v0.10.955 — konsol mesajlarındaki "console" ifadeleri işçi koşu
// kaydında yanıltıcı: tavan ve zaman aşımı mesajları işçi diliyle yeniden
// yazılır. Tür, UpstreamStatus ve Unwrap nöbetçisi (DeadlineExceeded /
// Canceled) aynen kalır.
func workerError(ctx context.Context, err error, lim WorkerLimits) error {
	var ce *ConsoleError
	if !errors.As(err, &ce) {
		return err
	}
	switch ce.Type {
	case ConsoleErrResponseTooLarge:
		ce.Message = fmt.Sprintf("response exceeds the %d MiB worker limit — shard the query (by namespace or instance) or aggregate it (max by, count by)", lim.MaxBodyMiB)
	case ConsoleErrTimeout:
		if ce.UpstreamStatus != 0 {
			break // Thanos'un kendi timeout cevabı: upstream mesajı korunur
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			ce.Message = "the caller's deadline expired before the query finished"
		} else {
			ce.Message = fmt.Sprintf("query exceeded the %ds worker timeout", lim.TimeoutS)
		}
	}
	return ce
}
