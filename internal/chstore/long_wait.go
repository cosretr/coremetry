package chstore

// long_wait.go — v0.10.2466 (operatör bildirimi 2026-11-22 + ek karar):
// "Daha uzun bekle". Varsayılan okuma bütçesi (max_execution_time = 25 sn)
// AYNEN kalır; operatör AÇIKÇA istediğinde (Traces notundaki düğme ya da
// CoSRE'nin "daha uzun bekleyeyim mi?" sorusuna evet) YALNIZ o istek /
// tur uzun bütçeyle koşar: 60 sn, admin tavanı (traces_long_wait ayarı,
// 25–180) ile sınırlı — etkin = min(60, tavan).
//
// Mekanizma ctx'te: WithLongWait(ctx, sn). tracedConn.Query/QueryRow
// (traced_conn.go) iki şeyi yapar:
//  1. SQL'deki `max_execution_time = N` (N < sn) → sn. Sorgu-düzeyi SETTINGS
//     yan tümcesi ctx ayarlarını ezdiği için metin düzeyinde yapılır.
//  2. Sürücünün ReadTimeout'u (30 sn, store.go) sorgu başında BİR KEZ kurulur;
//     ctx'te son tarih yoksa sn+longWaitSlack son tarihli türev ctx açılır
//     (clickhouse-go: ctx son tarihi okuma zaman aşımını ezer). İptal,
//     satırlar kapanınca / tek satır okununca.
// ctx'te bayrak yoksa ikisi de no-op: her sorgu bayt bayt eski.

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

const (
	// DefaultReadBudgetSec — bugünkü genel okuma bütçesi (değişmedi).
	DefaultReadBudgetSec = 25
	// LongWaitSec — "daha uzun bekle" evetinin bütçesi (operatör: 60 sn).
	LongWaitSec = 60
	// LongWaitCapMin / LongWaitCapMax — admin tavanının geçerli aralığı.
	LongWaitCapMin = 25
	LongWaitCapMax = 180
	// longWaitSlack — sürücü son tarihi sunucu bütçesinin biraz üstünde:
	// sunucunun kendi 159 hatası (açıklayıcı) transport hatasından önce gelsin.
	longWaitSlack = 5 * time.Second
)

// longWaitCap — admin tavanı (traces_long_wait); 0 = ayarlanmamış (60).
var longWaitCap atomic.Int64

// SetLongWaitCap — ayar yüklemesi / admin PUT (aralık dışı kelepçelenir).
func SetLongWaitCap(sec int) { longWaitCap.Store(int64(ClampLongWaitCap(sec))) }

// LongWaitCap — geçerli tavan (ayarlanmamışsa LongWaitSec).
func LongWaitCap() int {
	if v := longWaitCap.Load(); v > 0 {
		return int(v)
	}
	return LongWaitSec
}

// ClampLongWaitCap — SAF: 0/negatif → LongWaitSec; aralığa kelepçe.
func ClampLongWaitCap(sec int) int {
	switch {
	case sec <= 0:
		return LongWaitSec
	case sec < LongWaitCapMin:
		return LongWaitCapMin
	case sec > LongWaitCapMax:
		return LongWaitCapMax
	}
	return sec
}

// EffectiveLongWaitSec — SAF: min(60, tavan).
func EffectiveLongWaitSec(capSec int) int {
	capSec = ClampLongWaitCap(capSec)
	if capSec < LongWaitSec {
		return capSec
	}
	return LongWaitSec
}

type longWaitKey struct{}

// WithLongWait — ctx'e uzun bekleme bütçesi (sn ≤ DefaultReadBudgetSec → aynı ctx).
func WithLongWait(ctx context.Context, sec int) context.Context {
	if sec <= DefaultReadBudgetSec {
		return ctx
	}
	return context.WithValue(ctx, longWaitKey{}, sec)
}

// LongWaitFrom — ctx'teki bütçe (0 = yok).
func LongWaitFrom(ctx context.Context) int {
	v, _ := ctx.Value(longWaitKey{}).(int)
	return v
}

// ReadBudgetSec — bu ctx'te bir okumanın sunucu bütçesi (25 ya da uzun).
func ReadBudgetSec(ctx context.Context) int {
	if v := LongWaitFrom(ctx); v > DefaultReadBudgetSec {
		return v
	}
	return DefaultReadBudgetSec
}

var maxExecRe = regexp.MustCompile(`max_execution_time\s*=\s*(\d+)`)

// applyLongWaitSQL — SAF: sec > 0 iken N < sec olan her max_execution_time
// sec olur; büyükler (ör. 60'lık rapor sorgusu) dokunulmaz.
func applyLongWaitSQL(q string, sec int) string {
	if sec <= 0 {
		return q
	}
	return maxExecRe.ReplaceAllStringFunc(q, func(m string) string {
		sub := maxExecRe.FindStringSubmatch(m)
		if n, err := strconv.Atoi(sub[1]); err == nil && n < sec {
			return "max_execution_time = " + strconv.Itoa(sec)
		}
		return m
	})
}

// longWaitQuery — tracedConn girişi: SQL + (gerekirse) son tarihli ctx.
// cancel nil = türev ctx açılmadı (satırlar sarılmaz, yol bayt bayt eski).
func longWaitQuery(ctx context.Context, q string) (context.Context, string, context.CancelFunc) {
	sec := LongWaitFrom(ctx)
	if sec <= 0 {
		return ctx, q, nil
	}
	q = applyLongWaitSQL(q, sec)
	if _, ok := ctx.Deadline(); ok {
		return ctx, q, nil
	}
	dctx, cancel := context.WithTimeout(ctx, time.Duration(sec)*time.Second+longWaitSlack)
	return dctx, q, cancel
}

// cancelRows / cancelRow — türev ctx satırlar bitince kapanır.
type cancelRows struct {
	driver.Rows
	cancel context.CancelFunc
}

func (r *cancelRows) Close() error {
	err := r.Rows.Close()
	r.cancel()
	return err
}

type cancelRow struct {
	driver.Row
	cancel context.CancelFunc
}

func (r *cancelRow) Scan(dest ...any) error {
	defer r.cancel()
	return r.Row.Scan(dest...)
}

func (r *cancelRow) ScanStruct(dest any) error {
	defer r.cancel()
	return r.Row.ScanStruct(dest)
}

// ── zaman aşımı kaydı (CoSRE "daha uzun bekleyeyim mi?") ───────────────────

// TimeoutRecorder — bir CoSRE turunda okuma zaman aşımı / kısmi sonuç oldu mu.
// GetTraces kısmi döndüğünde ve araç katmanı zaman aşımı hatası gördüğünde
// işaretlenir; tur sonunda soru + çip buna bakar.
type TimeoutRecorder struct {
	mu   sync.Mutex
	hits int
}

type timeoutRecorderKey struct{}

// WithTimeoutRecorder — ctx'e kayıtçı.
func WithTimeoutRecorder(ctx context.Context) (context.Context, *TimeoutRecorder) {
	r := &TimeoutRecorder{}
	return context.WithValue(ctx, timeoutRecorderKey{}, r), r
}

// NoteReadTimeout — ctx'te kayıtçı varsa işaretler (yoksa no-op).
func NoteReadTimeout(ctx context.Context) {
	if r, ok := ctx.Value(timeoutRecorderKey{}).(*TimeoutRecorder); ok && r != nil {
		r.mu.Lock()
		r.hits++
		r.mu.Unlock()
	}
}

// Hit — turda en az bir zaman aşımı / kısmi sonuç.
func (r *TimeoutRecorder) Hit() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits > 0
}

// IsReadTimeout — hata bir okuma bütçesi / kaynak tükenmesi mi (159, 241,
// i/o timeout …; mv_fallback.go'nun işaret kümesi).
func IsReadTimeout(err error) bool { return isResourceExhaustion(err) }

// ── ayar (system_settings 'traces_long_wait') ─────────────────────────────

// tracesLongWaitKey — system_settings anahtarı; blob {"maxSec": N}.
const tracesLongWaitKey = "traces_long_wait"

type tracesLongWaitBlob struct {
	MaxSec int `json:"maxSec"`
}

// GetLongWaitCapSetting — KAYITLI tavan (yoksa / okunamazsa LongWaitSec).
func (s *Store) GetLongWaitCapSetting(ctx context.Context) int {
	raw, err := s.GetSetting(ctx, tracesLongWaitKey)
	if err != nil || len(raw) == 0 {
		return LongWaitSec
	}
	var b tracesLongWaitBlob
	if json.Unmarshal(raw, &b) != nil {
		return LongWaitSec
	}
	return ClampLongWaitCap(b.MaxSec)
}

// SaveLongWaitCapSetting — system_settings'e yazar (yeni şema YOK).
func (s *Store) SaveLongWaitCapSetting(ctx context.Context, sec int) error {
	raw, err := json.Marshal(tracesLongWaitBlob{MaxSec: ClampLongWaitCap(sec)})
	if err != nil {
		return err
	}
	return s.PutSetting(ctx, tracesLongWaitKey, raw)
}
