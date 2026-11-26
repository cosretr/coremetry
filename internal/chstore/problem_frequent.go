package chstore

// problem_frequent.go — v0.10.2280 (entity grafı ile Problem birleştirme S3,
// docs/problems/entity-graph-merge-design.md §6): "sık tekrar eden" kuralının
// GÖLGE ölçümü. Bu dosya SAF kural + tek sınırlı geçmiş okuması taşır.
//
// Kural: aynı tür = (köken varlık, aile, yön) — kural kimliği imzaya girmez.
// Problem'in açılışından önceki 24 saatte ≥ 3 bölüm YA DA 7 günde ≥ 4 ayrı
// günde görülme → "sık tekrar eden". Bölüm = aynı imzayla açılıp kapanmış
// (resolved) Problem. Şiddet arttıysa (öncelik penceredeki en yüksek
// önceliğin üstünde, büyüklük penceredeki en büyüğün ≥ 2 katı ya da yüksek
// güvenli P1 — NotifiableAt baypasıyla AYNI ölçü, notifyBypass) kural
// TUTMAZ. Sayılar kod sabiti; ayar yok.
//
// GÖLGE: NotifiableAt DEĞİŞMEZ (enforce yolu onu çağırır). Karar yalnız
// politika gölgesinin YANINA ayrı bir satır olarak yazılır (notify
// policy_shadow.go): channel_kind='policy', error = FrequentShadow* kodu.
// Mevcut gölge satırı bayt bayt aynı kalır; replay "shadow" sayımı yeni
// kodları ayrı anahtar olarak gösterir.
//
// Geçmiş okuması: `problems` ORDER BY id (partition yok) — zaman ya da özne
// birincil anahtarı budamaz. Sınır: özne IN listesi (≤ 200), started_at alt
// sınırı (≤ 14 gün), LIMIT 2000, max_execution_time 5, max_rows_to_read
// 5M + read_overflow_mode 'break' (tablo bundan büyükse okuma KESİLİR,
// kısmi sonuç = daha az bölüm = daha az "tutardı"; güvenli yön). Okunan
// satır sayısı sorgu ilerlemesinden ölçülür (RowsRead). log_comment
// "problem_frequent_history" — operatör system.query_log'dan ölçer.

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// Kural sabitleri (operatör kararı 4; ayar yok).
const (
	FrequentWin24h   = 24 * time.Hour
	FrequentWin7d    = 7 * 24 * time.Hour
	FrequentMinPrior = 3 // son 24 saatte en az 3 önceki bölüm
	FrequentMinDays  = 4 // YA DA son 7 günde en az 4 ayrı gün

	FrequentHistoryLimit    = 2000
	FrequentHistorySubjects = 200
	FrequentHistoryMaxRead  = 5_000_000
	FrequentHistoryTag      = "problem_frequent_history"
	frequentHistoryMaxBack  = 14 * 24 * time.Hour
	frequentEpisodesShown   = 5

	// NotifyHoldFrequent — tasarımın tutma kodu; S3'te YALNIZ gölge, v0.10.2342
	// (S5) açıkken NotifiableAt enforce yolunda.
	NotifyHoldFrequent = "frequent"
	// Gölge satırının `error` kodları (politika satırının yanında, ayrı satır).
	FrequentShadowHold     = "frequent:hold"        // kural uygulansaydı açılış bildirimi gitmezdi
	FrequentShadowSeverity = "frequent:severity_up" // sık ama şiddet arttı: yine bildirilirdi
	FrequentShadowEarlier  = "frequent:earlier"     // sık, ama daha önceki bir kapı zaten tutuyor

	FrequentKindHold     = "hold"
	FrequentKindSeverity = "severity"
)

// FrequentEpisode — geçmiş bir bölüm (en çok 5'i blobda).
type FrequentEpisode struct {
	ID         string  `json:"id"`
	StartedAt  int64   `json:"startedAt"`
	ResolvedAt int64   `json:"resolvedAt,omitempty"`
	Priority   string  `json:"priority,omitempty"`
	Magnitude  float64 `json:"magnitude,omitempty"`
}

// FrequentVerdict — bir açık Problem için kuralın gölge kararı.
type FrequentVerdict struct {
	Signature string `json:"signature"` // varlık|aile|yön
	Entity    string `json:"entity"`
	Name      string `json:"name"`
	Family    string `json:"family"`
	Direction string `json:"direction,omitempty"` // yalnız trafik: down | up
	ViaOrigin bool   `json:"viaOrigin,omitempty"` // imza kümenin kökeninden
	// Measure — v0.10.2475: yalnız görünüm ("Tür" satırı ailenin yerine
	// ölçüyü söyler: "consumer_lag"). İmzaya GİRMEZ (geçmiş eşleşmesi aynen).
	Measure  string `json:"measure,omitempty"`
	Prior24h int    `json:"prior24h"`
	Prior7d  int    `json:"prior7d"`
	Days7d   int    `json:"days7d"`
	Frequent bool   `json:"frequent"`
	// Kind — "" (sık değil) | hold (tutardı) | severity (şiddet arttı, giderdi).
	Kind         string            `json:"kind,omitempty"`
	Bypass       string            `json:"bypass,omitempty"` // priority_up | impact_2x | high_conf_p1
	Priority     string            `json:"priority,omitempty"`
	MaxPriority  string            `json:"maxPriority,omitempty"`
	Magnitude    float64           `json:"magnitude,omitempty"`
	MaxMagnitude float64           `json:"maxMagnitude,omitempty"`
	Episodes     []FrequentEpisode `json:"episodes,omitempty"` // en yeni önce, ≤ 5
	Partial      bool              `json:"partial,omitempty"`  // geçmiş okuması kesildi / okunamadı
}

// FrequentDirection — SAF: yön yalnız trafikte ayırır (düşüş / artış).
func FrequentDirection(p Problem, family string) string {
	if family != StdFamilyTraffic {
		return ""
	}
	if isDown(p) || (p.Threshold > 0 && p.Value < p.Threshold) {
		return "down"
	}
	return "up"
}

// FrequentSignature — SAF.
func FrequentSignature(entityKey, family, dir string) string {
	return entityKey + "|" + family + "|" + dir
}

// FrequentFamilyEligible — SAF: kural hangi ailelere bakar (self ve olay
// meta aileleri hariç; bilinmeyen aile yok).
func FrequentFamilyEligible(family string) bool {
	switch family {
	case "", StdFamilySelf, StdFamilyIncident:
		return false
	}
	return true
}

// EvaluateFrequent — SAF: geçmiş bölümlerden karar. hist = aynı imzalı,
// kapanmış Problem'ler (sıra önemsiz). cur'un başlangıcı referans anıdır.
func EvaluateFrequent(v FrequentVerdict, cur Problem, std ProblemStandard, prio string, hist []FrequentEpisode) FrequentVerdict {
	ref := cur.StartedAt
	var w24, w7 []FrequentEpisode
	days := map[string]bool{}
	for _, e := range hist {
		if e.ID == cur.ID || e.StartedAt >= ref || e.StartedAt < ref-int64(FrequentWin7d) {
			continue
		}
		w7 = append(w7, e)
		days[time.Unix(0, e.StartedAt).UTC().Format("2006-01-02")] = true
		if e.StartedAt >= ref-int64(FrequentWin24h) {
			w24 = append(w24, e)
		}
	}
	v.Prior24h, v.Prior7d, v.Days7d = len(w24), len(w7), len(days)
	v.Priority, v.Magnitude = prio, StdMagnitude(std)
	sort.Slice(w7, func(i, j int) bool { return w7[i].StartedAt > w7[j].StartedAt })
	v.Episodes = w7
	if len(v.Episodes) > frequentEpisodesShown {
		v.Episodes = v.Episodes[:frequentEpisodesShown]
	}
	var win []FrequentEpisode
	switch {
	case len(w24) >= FrequentMinPrior:
		win = w24
	case len(days) >= FrequentMinDays:
		win = w7
	default:
		return v
	}
	v.Frequent = true
	for _, e := range win {
		if prioRank(e.Priority) > prioRank(v.MaxPriority) {
			v.MaxPriority = e.Priority
		}
		if e.Magnitude > v.MaxMagnitude {
			v.MaxMagnitude = e.Magnitude
		}
	}
	if b := notifyBypass(std, prio, v.MaxPriority, v.MaxMagnitude); b != "" {
		v.Kind, v.Bypass = FrequentKindSeverity, b
	} else {
		v.Kind = FrequentKindHold
	}
	return v
}

// FrequentWouldHoldAt — SAF: eşik karşılaştırması (rapor 2 / 3 / 5).
func FrequentWouldHoldAt(v FrequentVerdict, minPrior int) bool {
	return v.Prior24h >= minPrior || v.Days7d >= FrequentMinDays
}

// FrequentShadowCode — SAF: NotifiableAt kararı + gölge karar → ayrı gölge
// satırının kodu ("" = satır yok). `frequent` tasarımdaki yerinde: etki
// tabanından SONRA, "problem değil" ve tekrar penceresinden ÖNCE; daha
// önceki kapı (gizli, P3, düşük güven, etki tabanı) tutuyorsa kural
// ulaşılmazdı.
func FrequentShadowCode(dec NotifyDecision, v FrequentVerdict) string {
	if !v.Frequent {
		return ""
	}
	if !dec.Notifiable {
		switch dec.Hold {
		case NotifyHoldNoise, NotifyHoldDedupe, NotifyHoldQuiet, NotifyHoldMerged, NotifyHoldFrequent:
		default:
			return FrequentShadowEarlier
		}
	}
	if v.Kind == FrequentKindSeverity {
		return FrequentShadowSeverity
	}
	return FrequentShadowHold
}

// FrequentHistoryRead — okuma sonucu + ölçü.
type FrequentHistoryRead struct {
	Rows     []Problem
	RowsRead int64 // sorgu ilerlemesinden okunan satır (tahmin değil, CH'nin sayımı)
	Capped   bool  // LIMIT'e değdi
}

// frequentHistorySQL — SAF: sınırlı geçmiş okuması.
func frequentHistorySQL(selectExpr string) string {
	return `
		SELECT ` + selectExpr + `
		FROM problems FINAL
		WHERE started_at >= ? AND service IN (?) AND status = 'resolved'
		LIMIT ` + fmt.Sprint(FrequentHistoryLimit) + `
		SETTINGS max_execution_time = 5, max_rows_to_read = ` + fmt.Sprint(FrequentHistoryMaxRead) + `, read_overflow_mode = 'break'`
}

// ProblemHistoryForSubjects — öznelerin (problems.service değeri) since'ten
// beri kapanmış Problem'leri. Sınırlar dosya başında.
func (s *Store) ProblemHistoryForSubjects(ctx context.Context, subjects []string, since time.Time) (FrequentHistoryRead, error) {
	var out FrequentHistoryRead
	if len(subjects) == 0 {
		return out, nil
	}
	if len(subjects) > FrequentHistorySubjects {
		subjects = subjects[:FrequentHistorySubjects]
	}
	if floor := time.Now().Add(-frequentHistoryMaxBack); since.Before(floor) {
		since = floor
	}
	var scanned atomic.Int64
	qctx := clickhouse.Context(WithQueryTag(ctx, FrequentHistoryTag),
		clickhouse.WithProgress(func(p *clickhouse.Progress) { scanned.Add(int64(p.Rows)) }))
	cols := s.problemCols()
	rows, err := s.conn.Query(qctx, frequentHistorySQL(problemSelectExprFor(cols)), chDateTime64Arg(since), subjects)
	if err != nil {
		return out, fmt.Errorf("problem frequent history: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		p, err := scanProblemRow(rows, cols)
		if err != nil {
			return out, err
		}
		out.Rows = append(out.Rows, p)
	}
	out.RowsRead = scanned.Load()
	out.Capped = len(out.Rows) >= FrequentHistoryLimit
	return out, rows.Err()
}

// FrequentReads — geçmiş okuması ölçüsü (lider başlangıcından beri).
type FrequentReads struct {
	Passes  int64 `json:"passes"`
	Reads   int64 `json:"reads"`   // problems geçmiş okuması (hatırlama ıskası)
	Rows    int64 `json:"rows"`    // toplam okunan satır (CH ilerlemesi)
	MaxRows int64 `json:"maxRows"` // tek okumada en çok okunan satır
	Capped  int64 `json:"capped"`  // LIMIT'e değen okuma
	Errors  int64 `json:"errors"`
}

// FrequentTopSig — en çok tutulacak imzalar.
type FrequentTopSig struct {
	Signature string `json:"signature"`
	Name      string `json:"name"`
	Family    string `json:"family"`
	Direction string `json:"direction,omitempty"`
	Held      int64  `json:"held"`
}

// FrequentP1 — tutulacakken sonradan P1'e çıkan Problem (kaçırılacak gerçek olay adayı).
type FrequentP1 struct {
	ProblemID string `json:"id"`
	Name      string `json:"name"`
	Family    string `json:"family"`
}

// FrequentReport — gölge raporu (24 sa, FARKLI Problem; lider belleği).
type FrequentReport struct {
	Frequent      int64            `json:"frequent"`     // sık tekrar eden açık Problem
	WouldHold     int64            `json:"wouldHold"`    // kural uygulansaydı açılış bildirimi gitmezdi
	SeverityUp    int64            `json:"severityUp"`   // sık ama şiddet arttı: yine bildirilirdi
	HeldBecameP1  int64            `json:"heldBecameP1"` // kabul ölçütü: hedef 0
	BecameP1      []FrequentP1     `json:"becameP1,omitempty"`
	AtThreshold   map[string]int64 `json:"atThreshold,omitempty"` // 24 sa eşiği 2 / 3 / 5 (ya da 7 günde 4 gün)
	TopSignatures []FrequentTopSig `json:"topSignatures,omitempty"`
	Reads         FrequentReads    `json:"reads"`
}
