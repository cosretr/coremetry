package chstore

// exception_spread.go — v0.10.949 — "aynı exception, aynı anda, birden çok
// serviste" olgusu (operatör 2026-09-26: "aynı anda farklı servislerden
// gelmiyorsa 5'ten düşük exception'ı göstermeye gerek yok; tek servisten
// gelen 5'ten küçük exception'ları göstermeyebiliriz").
//
// Varsayılan occurrence tabanı 5'e çıktı (api.inboxDefaultMinOcc); bu dosya
// tabanın İSTİSNASINI hesaplar: 5'in altındaki bir grup, AYNI exception aynı
// anda en az bir BAŞKA serviste de görülüyorsa varsayılan listede kalır.
//
// "Aynı exception" anahtarı = ex_type + "\x00" + normalizeMessageMask(mesaj,
// false) — parmak izinin normalizasyonu, TIRNAK İÇİ KORUNARAK (v0.10.949 —
// JDK yardımcı-NPE mesajları: `Cannot invoke "X.y()" because "z" is null`;
// tırnak maskesiyle tüm NPE'ler tek anahtara çöküyordu). Parmak izinin
// servissiz hâli DEĞİL: yığın kipindeki parmak izi her servisin KENDİ
// çerçevelerini hash'ler, yani aynı arıza iki serviste neredeyse hiç
// eşleşmez; mesajın ŞEKLİ eşleşir. Normalizasyon Go'da, tek kaynaktan
// (normalizeMessageMask) — SQL'de bir regex kopyası zamanla ayrışırdı.
//
// v0.10.949 — filo-genel anahtar freni: kimlik taşımayan şablon mesajlar
// (`Index # out of bounds for length #`, `No value present`) filoda sürekli
// bir yerlerde başlar; 7 günde ≥ spreadGenericSlots AYRI pencerede başlamış
// anahtar istisna ALMAZ (eşzamanlılık tesadüf). Gerçek eşzamanlı arıza (çok
// servis, bir-iki pencere) istisnasını korur.
//
// "Aynı anda" = etkinlik aralıkları S kadar boşluk payıyla örtüşür:
// max(g.first, h.first) − min(g.last, h.last) ≤ S (kapsayıcı). S =
// ExceptionTriageConfig.StormWindow() — ürünün "aynı anda" için zaten
// ayarlanabilir olan tek vidası (fırtına dedektörü, v0.9.1194).
//
// Tablo yalnız first_seen/last_seen/toplam taşır; kova başına sayım yok. 5'in
// altındaki satırlar için [first, last] her olayı kapsar, yani örtüşme o
// tarafta neredeyse kesin. Bilinen kusur: boşluklu uzun ömürlü bir ortak ya
// da first_seen'i korunmuş (regressed) bir grup aralığı genişletir.

import (
	"context"
	"errors"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// SpreadLookback — v0.10.949 — Inbox'ın en geniş `since` basamağı (7d)
	// ile aynı. Daha eski satırlar istisna ALMAZ (taban normal uygulanır).
	SpreadLookback = 7 * 24 * time.Hour
	// SpreadExemptCap — ExemptBelow'un döndürdüğü en çok parmak izi. Liste
	// SQL'e `fingerprint IN (?)` olarak bağlanır; tavan sorgu boyunu sınırlar.
	SpreadExemptCap = 2000
	// spreadTypeCap / spreadRowCap — iki okumanın LIMIT'leri. Tavana
	// çarpılırsa ExceptionSpread.Capped=true ve bir log satırı (sessiz tavan
	// yok).
	spreadTypeCap = 2000
	spreadRowCap  = 20000
	// spreadMsgPrefix — mesajın SQL'de kırpıldığı bayt sayısı
	// (substring(ex_message, 1, 512)); anahtar fonksiyonu da aynı kırpmayı
	// yapar ki kaynak ne olursa olsun anahtar aynı çıksın.
	spreadMsgPrefix = 512
	// spreadKeyNormCap — normalize EDİLMİŞ mesajın anahtara giren kısmı.
	// Ham 512 baytlık kırpma, uzunluğu değişen dinamik bir değerden
	// (order 7 / order 123456) sonra kuyruğu kaydırır; normalize edilmiş
	// metnin ilk 256 baytı o kaymadan etkilenmez.
	spreadKeyNormCap = 256
	// spreadPartnersShown — ipucunda adı yazılan ortak servis sayısı.
	spreadPartnersShown = 5
	// spreadPartnersMax — saymayı bırakma sınırı (satır başına iş sınırı).
	spreadPartnersMax = 99
	// spreadMemoTTL — süreç-içi memo (memo.go). Görüntü kuralı; 60 sn
	// bayatlık operatör için görünmez, iki okuma dakikada bir kez ödenir.
	spreadMemoTTL = 60 * time.Second
	// spreadReadBudget — v0.10.949 — iki okumanın istemci bütçesi (SQL'deki
	// max_execution_time = 3 ile aynı). Yayılım isteğe bağlı bir işaret;
	// Inbox listesinin / rozetin bütçesini yiyemez.
	spreadReadBudget = 3 * time.Second
	// spreadFailBackoff — v0.10.949 — hata sonrası negatif önbellek: bu süre
	// boyunca CH'a gidilmez, memo kilidine de girilmez (ttlMemo hatayı
	// SAKLAMAZ — sözleşmesi memo_test.go'da; OpenProblemsSnapshot ona dayanır).
	spreadFailBackoff = 30 * time.Second
	// spreadGenericSlots — v0.10.949 — bir anahtarın satırları 7 günde bu
	// kadar AYRI başlangıç penceresine (first_seen / max(S, 1 dk)) yayılmışsa
	// anahtar filo-genel sayılır ve istisna üretmez. Operatör ayarlayabilir
	// (plan §5 sorusu); gerçek eşzamanlı arıza bir-iki pencerede kalır.
	spreadGenericSlots = 24
)

// errSpreadBackoff — v0.10.949 — son hatadan beri spreadFailBackoff dolmadı;
// api.exceptionSpread bunu da soft-fail sayar (istisnasız taban).
var errSpreadBackoff = errors.New("exception spread: backoff after recent failure")

// spreadGenericLogged — filo-genel anahtar sayısının son loglanan değeri;
// aynı sayı her 60 sn'lik anlık görüntüde tekrar yazılmaz.
var spreadGenericLogged atomic.Int64

// spreadPlaceholders — normalizeMessageMask'ın ürettiği yer tutucular. Yalnız
// bunlardan oluşan bir mesaj ("#", "#uuid") tür adından daha ayırt edici
// değildir. ("#q" anahtar kipinde üretilmez; parmak izi kipiyle ortak liste.)
var spreadPlaceholders = strings.NewReplacer(
	"#uuid", "", "#email", "", "#hex", "", "#tok", "", "#ts", "", "#q", "")

// ExceptionSpreadKey — v0.10.949 — "aynı exception" anahtarı. SAF.
//
// ok=false: normalize edilmiş mesaj boşsa ya da yer tutucular dışında tek
// bir harf bile taşımıyorsa. Tür TEK BAŞINA fazla genel
// (NullPointerException, çıplak bir 503): binlerce servisli bir filoda iki
// servis birkaç dakika içinde hep birini paylaşır ve kural hiçbir şeyi
// gizlemezdi. Bu bir anahtar-kalitesi kuralıdır, tür kapısı DEĞİL — httperror
// satırları da aynı kurala tabi (mesajsız 4xx/5xx grupları istisna almaz).
func ExceptionSpreadKey(exType, msg string) (string, bool) {
	if len(msg) > spreadMsgPrefix {
		msg = msg[:spreadMsgPrefix]
	}
	// v0.10.949 — tırnak içi KORUNUR (parmak izinden farklı): bkz. normalizeMessageMask.
	norm := strings.TrimSpace(normalizeMessageMask(msg, false))
	if len(norm) > spreadKeyNormCap {
		cut := spreadKeyNormCap
		for cut > 0 && !utf8.RuneStart(norm[cut]) {
			cut--
		}
		norm = norm[:cut]
	}
	if !spreadHasSignal(norm) {
		return "", false
	}
	return exType + "\x00" + norm, true
}

// spreadHasSignal — yer tutucular çıkarıldıktan sonra en az bir harf var mı.
func spreadHasSignal(norm string) bool {
	for _, r := range spreadPlaceholders.Replace(norm) {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// ExceptionSpreadRow — hesaplamanın girdisi (exception_groups'tan bir satır).
type ExceptionSpreadRow struct {
	Fingerprint string
	Type        string
	Message     string // ilk 512 bayt yeterli
	Service     string
	FirstSeen   int64 // unix ns
	LastSeen    int64 // unix ns
	Occurrences uint64
}

// SpreadInfo — bir grubun yayılımı. Services = 1 + aynı anda aynı anahtarla
// görülen FARKLI diğer servis sayısı (99 ortakta saymayı bırakır). Partners
// en çok 5 ad, alfabetik. Occurrences/LastSeen hesap anındaki değerler
// (ExemptBelow onlarla süzer ve sıralar).
type SpreadInfo struct {
	Services    int
	Partners    []string
	Occurrences uint64
	LastSeen    int64
}

// ComputeExceptionSpread — v0.10.949 — SAF. Yalnız yayılımı ≥2 olan
// parmak izleri haritada yer alır.
//
// Anahtara göre gruplar, grubu first_seen'e göre sıralar ve her satır için
// örtüşen FARKLI servisleri sayar. İlişki g'ye göre ikili, geçişli değil:
// A–B–C zincirinde A ile C örtüşmüyorsa A=2, B=3, C=2. Sıralı tarama
// h.first > g.last+S olduğunda keser; sayım grubun erişilebilir servis
// sayısına (ya da 99'a) ulaşınca durur.
//
// v0.10.949 — satırları ≥ spreadGenericSlots ayrı başlangıç penceresine
// yayılmış anahtar atlanır (filo-genel şablon mesaj; bkz. dosya başı).
func ComputeExceptionSpread(rows []ExceptionSpreadRow, slack time.Duration) map[string]SpreadInfo {
	out, _ := computeExceptionSpread(rows, slack)
	return out
}

// computeExceptionSpread — ComputeExceptionSpread + atlanan filo-genel
// anahtar sayısı (store okuması loglar). SAF.
func computeExceptionSpread(rows []ExceptionSpreadRow, slack time.Duration) (map[string]SpreadInfo, int) {
	if slack < 0 {
		slack = 0
	}
	s := slack.Nanoseconds()
	groups := map[string][]int{}
	for i, r := range rows {
		if r.Fingerprint == "" || r.Service == "" {
			continue
		}
		k, ok := ExceptionSpreadKey(r.Type, r.Message)
		if !ok {
			continue
		}
		groups[k] = append(groups[k], i)
	}
	out := map[string]SpreadInfo{}
	slotW := max(s, int64(time.Minute))
	generic := 0
	for _, idx := range groups {
		if len(idx) < 2 {
			continue
		}
		svcs := make(map[string]struct{}, len(idx))
		for _, i := range idx {
			svcs[rows[i].Service] = struct{}{}
		}
		if len(svcs) < 2 {
			continue // tek servisli anahtar: kimse ortak olamaz
		}
		slots := make(map[int64]struct{}, len(idx))
		for _, i := range idx {
			slots[rows[i].FirstSeen/slotW] = struct{}{}
		}
		if len(slots) >= spreadGenericSlots {
			generic++
			continue // v0.10.949 — filo-genel anahtar: 7 günde ≥24 ayrı pencerede başlamış; eşzamanlılık tesadüf, bilgi taşımaz
		}
		reach := min(len(svcs)-1, spreadPartnersMax)
		sort.Slice(idx, func(a, b int) bool {
			ra, rb := rows[idx[a]], rows[idx[b]]
			if ra.FirstSeen != rb.FirstSeen {
				return ra.FirstSeen < rb.FirstSeen
			}
			return ra.Fingerprint < rb.Fingerprint
		})
		for _, i := range idx {
			g := rows[i]
			gLast := max(g.LastSeen, g.FirstSeen)
			partners := map[string]struct{}{}
			for _, j := range idx {
				h := rows[j]
				if h.FirstSeen > gLast+s {
					break // first_seen sıralı: sonrakiler de başlamadı
				}
				if h.Service == g.Service {
					continue
				}
				if _, dup := partners[h.Service]; dup {
					continue
				}
				if spreadOverlaps(g, h, s) {
					partners[h.Service] = struct{}{}
					if len(partners) >= reach {
						break
					}
				}
			}
			if len(partners) == 0 {
				continue
			}
			names := make([]string, 0, len(partners))
			for p := range partners {
				names = append(names, p)
			}
			sort.Strings(names)
			if len(names) > spreadPartnersShown {
				names = names[:spreadPartnersShown]
			}
			out[g.Fingerprint] = SpreadInfo{
				Services: 1 + len(partners), Partners: names,
				Occurrences: g.Occurrences, LastSeen: g.LastSeen,
			}
		}
	}
	return out, generic
}

// spreadOverlaps — aralıklar S payıyla örtüşüyor mu (kapsayıcı). last<first
// olan bozuk satır tek noktalı aralık sayılır.
func spreadOverlaps(g, h ExceptionSpreadRow, slackNs int64) bool {
	gl, hl := max(g.LastSeen, g.FirstSeen), max(h.LastSeen, h.FirstSeen)
	return max(g.FirstSeen, h.FirstSeen)-min(gl, hl) <= slackNs
}

// ExceptionSpread — v0.10.949 — filo-geneli yayılım anlık görüntüsü. Takım/
// ortam/aramadan BAĞIMSIZ: "şu an başka bir serviste de oluyor" filonun bir
// olgusudur. Memo'da PAYLAŞILIR — okuyucular yalnız Of/ExemptBelow üzerinden
// erişir ve ikisi de kopya döndürür. nil alıcı güvenli (soft-fail: istisna
// yok, rozet yok).
type ExceptionSpread struct {
	by map[string]SpreadInfo
	// Capped — okumalardan biri LIMIT'ine çarptı; hesap eksik kümeyle yapıldı.
	Capped bool

	slack     time.Duration
	truncOnce sync.Once
}

// NewExceptionSpread — hesaplanmış haritadan anlık görüntü (testler ve
// store okuması için).
func NewExceptionSpread(by map[string]SpreadInfo, capped bool) *ExceptionSpread {
	if by == nil {
		by = map[string]SpreadInfo{}
	}
	return &ExceptionSpread{by: by, Capped: capped}
}

// Of — parmak izinin yayılımı; yoksa ok=false. Partners KOPYALANIR: memo
// paylaşılır, çağıranın dilimi değiştirmesi başka bir isteğe sızmamalı.
func (sp *ExceptionSpread) Of(fp string) (SpreadInfo, bool) {
	if sp == nil {
		return SpreadInfo{}, false
	}
	info, ok := sp.by[fp]
	if !ok {
		return SpreadInfo{}, false
	}
	info.Partners = append([]string(nil), info.Partners...)
	return info, true
}

// ExemptBelow — tabanın ALTINDA kalan ama yayılımı ≥2 olan parmak izleri,
// last_seen DESC (eşitlikte parmak izi ASC), en çok SpreadExemptCap. Kırpma
// anlık görüntü başına bir kez loglanır. floor 0 → nil (taban yok, istisna
// anlamsız).
func (sp *ExceptionSpread) ExemptBelow(floor uint64) []string {
	if sp == nil || floor == 0 {
		return nil
	}
	type cand struct {
		fp   string
		last int64
	}
	var c []cand
	for fp, info := range sp.by {
		if info.Services >= 2 && info.Occurrences < floor {
			c = append(c, cand{fp, info.LastSeen})
		}
	}
	sort.Slice(c, func(i, j int) bool {
		if c[i].last != c[j].last {
			return c[i].last > c[j].last
		}
		return c[i].fp < c[j].fp
	})
	if len(c) > SpreadExemptCap {
		n := len(c)
		sp.truncOnce.Do(func() {
			log.Printf("[exception-spread] taban istisnası %d parmak izi, en yeni %d'si uygulanıyor", n, SpreadExemptCap)
		})
		c = c[:SpreadExemptCap]
	}
	out := make([]string, len(c))
	for i := range c {
		out[i] = c[i].fp
	}
	return out
}

// ExceptionSpread — v0.10.949 — 60 sn'lik memo arkasında filo yayılımı.
// Memo nil ise (test kurulumu) doğrudan okur — OpenProblemsSnapshot duruşu.
// Pay (slack) değişmişse memo'daki eski hesap atılır.
//
// v0.10.949 — okuma üç sıcak yolda (Inbox listesi, rozet + ısıtma döngüsü,
// /api/exception-groups). ttlMemo hatayı saklamaz ve fn'i kilit altında
// çalıştırır: CH yavaşken kilit kuyruğundaki her çağıran Q1+Q2'yi sırayla
// yeniden koşardı. Bütçe spreadReadBudget; hata spreadFailBackoff boyunca
// negatif önbelleğe yazılır (memo'ya dokunmadan) — kuyruktakiler kilidi
// alınca backoff'u görüp hemen döner, sonrakiler kilide hiç girmez.
func (s *Store) ExceptionSpread(ctx context.Context, slack time.Duration) (*ExceptionSpread, error) {
	if s.spreadInBackoff() {
		return nil, errSpreadBackoff // hızlı yol: memo kilidine hiç girme
	}
	load := func(ctx context.Context) (*ExceptionSpread, error) {
		if s.spreadInBackoff() {
			return nil, errSpreadBackoff // kilit kuyruğundakiler CH'a tekrar gitmez
		}
		rctx, cancel := context.WithTimeout(ctx, spreadReadBudget)
		defer cancel()
		sp, err := s.exceptionSpreadUncached(rctx, slack)
		if err != nil && ctx.Err() == nil { // çağıranın kendi iptali backoff tetiklemez
			s.spreadFailUntil.Store(time.Now().Add(spreadFailBackoff).UnixNano())
		}
		return sp, err
	}
	if s.spreadMemo == nil {
		return load(ctx)
	}
	sp, err := s.spreadMemo.Get(ctx, load)
	if err != nil {
		return nil, err
	}
	if sp.slack != slack {
		s.spreadMemo.Invalidate()
		return s.spreadMemo.Get(ctx, load)
	}
	return sp, nil
}

// spreadInBackoff — v0.10.949 — son yayılım okuması hata verdi ve
// spreadFailBackoff henüz dolmadı.
func (s *Store) spreadInBackoff() bool {
	u := s.spreadFailUntil.Load()
	return u != 0 && time.Now().UnixNano() < u
}

// exceptionSpreadUncached — iki bağlı sorgu, alt sorgu YOK (anomaly_event.go
// kuralı: IN bir DEĞER listesi; dağıtıkta shard-yerel alt sorgu tuzağı yok).
//
//	Q1: en az iki serviste görülen, mesajlı türler (aday daraltması).
//	Q2: o türlerin satırları, last_seen DESC.
//
// Mesaj SQL'de 512 bayta kırpılır (left(…, 512) eşdeğeri substring).
func (s *Store) exceptionSpreadUncached(ctx context.Context, slack time.Duration) (*ExceptionSpread, error) {
	since := time.Now().Add(-SpreadLookback).UnixNano()
	trows, err := s.conn.Query(ctx, `
		SELECT ex_type
		FROM exception_groups FINAL
		WHERE state != ? AND last_seen >= fromUnixTimestamp64Nano(?) AND ex_message != ''
		GROUP BY ex_type
		HAVING uniqExact(service) >= 2
		LIMIT ?
		SETTINGS max_execution_time = 3`, ExStateIgnored, since, spreadTypeCap)
	if err != nil {
		return nil, err
	}
	var types []string
	for trows.Next() {
		var t string
		if err := trows.Scan(&t); err != nil {
			trows.Close()
			return nil, err
		}
		types = append(types, t)
	}
	trows.Close()
	if err := trows.Err(); err != nil {
		return nil, err
	}
	capped := len(types) >= spreadTypeCap
	if len(types) == 0 {
		sp := NewExceptionSpread(nil, false)
		sp.slack = slack
		return sp, nil
	}
	rows, err := s.conn.Query(ctx, `
		SELECT fingerprint, ex_type, substring(ex_message, 1, 512), service,
		       toUnixTimestamp64Nano(first_seen), toUnixTimestamp64Nano(last_seen),
		       occurrences
		FROM exception_groups FINAL
		WHERE state != ? AND last_seen >= fromUnixTimestamp64Nano(?) AND ex_message != ''
		  AND ex_type IN (?)
		ORDER BY last_seen DESC
		LIMIT ?
		SETTINGS max_execution_time = 3`, ExStateIgnored, since, types, spreadRowCap)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	in := make([]ExceptionSpreadRow, 0, 256)
	for rows.Next() {
		var r ExceptionSpreadRow
		if err := rows.Scan(&r.Fingerprint, &r.Type, &r.Message, &r.Service,
			&r.FirstSeen, &r.LastSeen, &r.Occurrences); err != nil {
			return nil, err
		}
		in = append(in, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(in) >= spreadRowCap {
		capped = true
	}
	if capped {
		log.Printf("[exception-spread] okuma tavana çarptı (tür=%d/%d, satır=%d/%d) — yayılım en yeni satırlarla hesaplandı",
			len(types), spreadTypeCap, len(in), spreadRowCap)
	}
	by, generic := computeExceptionSpread(in, slack)
	// v0.10.949 — filo-genel anahtar sayısı anlık görüntü başına; yalnız
	// değiştiğinde yazılır (60 sn'de bir aynı satır log'u boğmasın).
	if int64(generic) != spreadGenericLogged.Swap(int64(generic)) && generic > 0 {
		log.Printf("[exception-spread] %d filo-genel anahtar istisna dışı (7 günde ≥%d ayrı başlangıç penceresi)",
			generic, spreadGenericSlots)
	}
	sp := NewExceptionSpread(by, capped)
	sp.slack = slack
	return sp, nil
}
