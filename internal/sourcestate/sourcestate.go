// Package sourcestate — v0.10.944 (CoSRE araştırma asistanı): bir veri
// kaynağı okumasının SONUCUNU, veriden ayrı ve açık bir sözleşme olarak
// taşır.
//
// Neden ayrı bir paket: trace (ClickHouse/Tempo), log (Elasticsearch ya da
// ClickHouse) ve metrik (VictoriaMetrics ya da ClickHouse) okumaları her
// biri kendi sözcüğüyle "olmadı" diyordu — logstore `degraded`/`partial`,
// promapi düz metin "HTTP 401", mcp toolerr altı sınıf. Modelin önündeki
// soru hep aynı: "bu kaynaktan gelen sonuç neyi kanıtlar, neyi
// kanıtlamaz?". Tek sözlük olmadan "log bulunamadı" ile "log kaynağına
// erişilemedi" aynı boş listeye dönüşüyor ve model ikisini de "hata yok"
// diye okuyordu. Paket SAF: yalnız stdlib; logstore/vmetrics/mcptools
// hepsi bunu içe aktarabilir, döngü oluşmaz.
//
// Neden durum BİRİNCİL + bayraklar: bir ES cevabı hem kısmi (shard
// düştü) hem limite takılmış olabilir; tek alana sıkıştırmak birini
// saklar. Birincil durum, modelin önce okuması gereken en kısıtlayıcı
// olandır (öncelik sırası statePriority'de); Flags hepsini listeler.
package sourcestate

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// State — bir kaynak okumasının sonucu.
type State string

const (
	// OK — sorgu başarılı, kayıt döndü.
	OK State = "ok"
	// Empty — sorgu BAŞARILI, eşleşen kayıt yok. "Hata yok" ya da
	// "sorun yok" DEĞİLDİR: yalnız bu filtre + pencere için kayıt yok.
	Empty State = "empty"
	// Unreachable — kaynağa ulaşılamadı (bağlantı reddi, DNS, 5xx).
	Unreachable State = "unreachable"
	// Unauthorized — kaynak kimlik/izin reddi verdi (401/403).
	Unauthorized State = "unauthorized"
	// Timeout — okuma süre bütçesini aştı.
	Timeout State = "timeout"
	// Partial — sonuç eksik kapsamlı: shard/replika düştü, bir filtre
	// uygulanamadı ya da kaynak kısmi cevap verdi.
	Partial State = "partial"
	// Delayed — veri gecikmiş: pencerenin sonu kaynakta henüz yok.
	Delayed State = "delayed"
	// Truncated — sonuç satır/seri limitine takıldı; tamamı değil.
	Truncated State = "truncated"
	// NotConfigured — kaynak bu kurulumda yapılandırılmamış.
	NotConfigured State = "not_configured"
	// Error — sınıflandırılamayan hata.
	Error State = "error"
)

// statePriority — birincil durumu seçerken öncelik (küçük = önce).
// Hata sınıfları veri sınıflarından önce gelir: erişilemeyen kaynağın
// "boş" görünmesi en pahalı yanlış okuma.
var statePriority = map[State]int{
	Unauthorized:  0,
	Unreachable:   1,
	Timeout:       2,
	NotConfigured: 3,
	Error:         4,
	Partial:       5,
	Truncated:     6,
	Delayed:       7,
	Empty:         8,
	OK:            9,
}

// ErrUnauthorized — kaynağın 401/403 cevabını taşıyan sentinel. Adaptör
// hatayı `fmt.Errorf("%w: …", sourcestate.ErrUnauthorized)` ile sarar;
// metin eşlemesi yalnız sarmalanmamış eski yollar için yedektir.
var ErrUnauthorized = errors.New("source unauthorized")

// ErrNotConfigured — kaynak yapılandırılmamış (ör. VictoriaMetrics kapalı).
var ErrNotConfigured = errors.New("source not configured")

// Status — bir kaynak okumasının modele ve arayüze giden sonucu.
type Status struct {
	Source   string   `json:"source"`            // traces | logs | metrics | deploys
	Backend  string   `json:"backend,omitempty"` // clickhouse | elasticsearch | victoriametrics | tempo | thanos
	State    State    `json:"state"`
	Flags    []State  `json:"flags,omitempty"` // birincil dahil tüm geçerli durumlar (2+ ise)
	Returned int      `json:"returned"`
	Limit    int      `json:"limit,omitempty"`
	FromISO  string   `json:"fromIso,omitempty"` // UTC RFC3339
	ToISO    string   `json:"toIso,omitempty"`
	Detail   string   `json:"detail,omitempty"` // kısa, sır/kimlik bilgisi içermez
	Notes    []string `json:"notes,omitempty"`  // "env filtresi uygulanamadı" gibi kapsam notları
}

// Outcome — başarılı bir okumanın bayrakları. Result bunlardan
// birincil durumu türetir.
type Outcome struct {
	Returned  int
	Limit     int
	Partial   bool
	Truncated bool
	Delayed   bool
}

// Result — başarılı bir okumanın Status'u. Birincil durum, geçerli
// bayraklardan en kısıtlayıcısı; hiçbiri yoksa kayıt sayısına göre
// ok/empty.
func Result(source, backend string, o Outcome) Status {
	var flags []State
	if o.Partial {
		flags = append(flags, Partial)
	}
	if o.Truncated {
		flags = append(flags, Truncated)
	}
	if o.Delayed {
		flags = append(flags, Delayed)
	}
	if o.Returned == 0 {
		flags = append(flags, Empty)
	}
	st := Status{Source: source, Backend: backend, Returned: o.Returned, Limit: o.Limit}
	st.State = primary(flags, OK)
	if len(flags) > 1 {
		st.Flags = flags
	}
	return st
}

// FromError — başarısız bir okumanın Status'u. İptal (context.Canceled)
// bir kaynak durumu DEĞİLDİR: çağıran vazgeçti; IsCancelled ile önce
// ayıklanmalı. Buraya düşerse Error olarak işaretlenir.
func FromError(source, backend string, err error) Status {
	st := Status{Source: source, Backend: backend, State: Classify(err)}
	if err != nil {
		st.Detail = capRunes(err.Error(), 200)
	}
	return st
}

// IsCancelled — hata çağıranın vazgeçmesi mi (kullanıcı durdurdu, bağlantı
// koptu). Evet ise kalan sorgular çalıştırılmamalı.
func IsCancelled(err error) bool {
	return err != nil && errors.Is(err, context.Canceled)
}

// Classify — hatayı duruma çevirir. SIRA: sentinel'ler → stdlib tipleri →
// metin sinyalleri (en spesifikten en genele). SAF, tablo testli.
func Classify(err error) State {
	if err == nil {
		return OK
	}
	switch {
	case errors.Is(err, ErrUnauthorized):
		return Unauthorized
	case errors.Is(err, ErrNotConfigured):
		return NotConfigured
	case errors.Is(err, context.DeadlineExceeded):
		return Timeout
	case errors.Is(err, context.Canceled):
		return Error
	}
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return Timeout
	}
	s := strings.ToLower(err.Error())
	if IsUnauthorizedText(s) {
		return Unauthorized
	}
	for _, sig := range timeoutSignals {
		if strings.Contains(s, sig) {
			return Timeout
		}
	}
	var operr *net.OpError
	if errors.As(err, &operr) {
		return Unreachable
	}
	for _, sig := range notConfiguredSignals {
		if strings.Contains(s, sig) {
			return NotConfigured
		}
	}
	for _, sig := range unreachableSignals {
		if strings.Contains(s, sig) {
			return Unreachable
		}
	}
	return Error
}

// IsUnauthorizedText — küçük harfli hata metni bir 401/403 mü. Adaptör
// metinleri: ES `(status 401)` / `security_exception`, promapi
// `HTTP 401` / `HTTP 403`. Exported: mcp toolerr aynı listeyi kullanır
// (iki ayrı liste zamanla ayrışırdı).
func IsUnauthorizedText(lower string) bool {
	for _, sig := range unauthorizedSignals {
		if strings.Contains(lower, sig) {
			return true
		}
	}
	return false
}

var (
	unauthorizedSignals = []string{
		"status 401", "status 403", "http 401", "http 403",
		"status: 401", "status: 403", "401 unauthorized", "403 forbidden",
		"security_exception", "authentication_exception",
		"source unauthorized", // sourcestate.ErrUnauthorized sentinel metni
	}
	timeoutSignals = []string{
		"deadline exceeded", "context deadline", "timeout exceeded",
		"timeout_exceeded", "code: 159", "max_execution_time",
		"client.timeout exceeded", "i/o timeout",
		"gateway timeout", "http 504", "status 504",
	}
	notConfiguredSignals = []string{
		"not configured", "yapılandırılmamış",
	}
	unreachableSignals = []string{
		"connection refused", "connection reset", "no such host",
		"no route to host", "network is unreachable", "dial tcp",
		"broken pipe", "unexpected eof", "slow/unreachable",
		"service unavailable", "bad gateway",
		"http 502", "http 503", "status 502", "status 503",
	}
)

// WithWindow — pencereyi UTC RFC3339 olarak ekler (sıfır zamanlar atlanır).
func (s Status) WithWindow(from, to time.Time) Status {
	if !from.IsZero() {
		s.FromISO = from.UTC().Format(time.RFC3339)
	}
	if !to.IsZero() {
		s.ToISO = to.UTC().Format(time.RFC3339)
	}
	return s
}

// WithNote — kapsam notu ekler; not verilirse Partial bayrağı da düşer
// (uygulanamayan filtre = eksik kapsam). Birincil durum yeniden seçilir.
func (s Status) WithNote(note string, partial bool) Status {
	if note != "" {
		s.Notes = append(s.Notes, note)
	}
	if partial && s.State != Partial && !hasFlag(s.Flags, Partial) {
		flags := s.Flags
		if len(flags) == 0 {
			flags = []State{s.State}
		}
		flags = append(flags, Partial)
		s.State = primary(flags, s.State)
		s.Flags = flags
	}
	return s
}

// Usable — sonuç kanıt olarak okunabilir mi (hata sınıfı değil). Empty
// kullanılabilir bir sonuçtur (eşleşme yok), ama "sorun yok" değildir.
func (s Status) Usable() bool {
	switch s.State {
	case OK, Empty, Partial, Truncated, Delayed:
		return true
	}
	return false
}

// SummaryTR — modele ve operatöre tek satırlık Türkçe durum cümlesi.
// Empty için "hata yok DEMEK DEĞİL" uyarısı bilinçli olarak metnin içinde:
// küçük model uyarıyı ayrı bir alandan okumuyor.
func (s Status) SummaryTR() string {
	src := s.Source
	if s.Backend != "" {
		src += "/" + s.Backend
	}
	var msg string
	switch s.State {
	case OK:
		msg = "başarılı"
	case Empty:
		msg = "sorgu başarılı, eşleşen kayıt yok (bu 'hata yok' ya da 'sorun yok' demek DEĞİL)"
	case Unreachable:
		msg = "kaynağa erişilemedi — bu kaynaktan kanıt YOK, kapsam eksik"
	case Unauthorized:
		msg = "kaynak yetki reddi verdi (401/403) — bu kaynaktan kanıt YOK, kapsam eksik"
	case Timeout:
		msg = "sorgu zaman aşımına uğradı — bu kaynaktan kanıt YOK ya da eksik"
	case Partial:
		msg = "sonuç kısmi — kapsam eksik"
	case Delayed:
		msg = "veri gecikmiş — pencerenin sonu henüz kaynakta yok"
	case Truncated:
		msg = "sonuç limite takıldı — tamamı değil"
	case NotConfigured:
		msg = "kaynak yapılandırılmamış — bu kaynaktan kanıt YOK"
	default:
		msg = "sınıflandırılamayan hata — bu kaynaktan kanıt YOK"
	}
	out := src + ": " + msg
	if len(s.Notes) > 0 {
		out += " (" + strings.Join(s.Notes, "; ") + ")"
	}
	return out
}

func primary(flags []State, fallback State) State {
	best, bestP := fallback, statePriority[fallback]
	for _, f := range flags {
		if p, ok := statePriority[f]; ok && p < bestP {
			best, bestP = f, p
		}
	}
	return best
}

func hasFlag(flags []State, f State) bool {
	for _, x := range flags {
		if x == f {
			return true
		}
	}
	return false
}

// capRunes — rune sınırında keser, kesildiğini "…" ile söyler.
func capRunes(s string, max int) string {
	n := 0
	for i := range s {
		if n == max {
			return s[:i] + "…"
		}
		n++
	}
	return s
}
