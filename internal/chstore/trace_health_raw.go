package chstore

// trace_health_raw.go — v0.10.823 "Trace hattı sağlığı" kartının İSTEĞE
// BAĞLI ham sayımı (operatör test kümesi, 2026-09-19).
//
// Neden ayrı dosya ve neden isteğe bağlı: invariant "agregat için ham
// spans okumak bug"tır — bu yüzden panelin VARSAYILAN sayısı hâlâ
// service_summary_5m'den gelir (trace_health.go, MV-first pini
// TestTraceHealthSQLBounded). Ham sayım bir agregat DEĞİL, MV sayısının
// DOĞRULAMASIDIR.
//
// ⚠ İKİ SAYI AYNI POPÜLASYON DEĞİLDİR — bu dosyanın en pahalı dersi:
//   - `count() FROM spans` Distributed sarmalayıcıyı okur: shard başına
//     BİR replika (load_balancing=random). Yani ≈ shard toplamlarının
//     toplamı.
//   - `clusterAllReplicas(spans_local)` HER replikayı ayrı ayrı sayar.
//     Sağlıklı RF=2 kümede host toplamı Distributed toplamının İKİ
//     KATIDIR — yapısı gereği, arıza değil.
// İlk yazım ikisini doğrudan kıyaslıyordu ve sağlıklı bir kümede
// "replikalar ayrışmış" diye bağırırdı. Doğru sinyal ŞARD İÇİ yayılım:
// aynı shard'ın replikaları birbirinden farklıysa ayrışma vardır; farklı
// shard'ların farklı olması shard anahtarının kendisidir.
//
// Distributed toplam ayrı bir soruya cevap verir: shard bandının
// [Σmin, Σmax] dışına düşüyorsa okuma eksik veri tutan bir replikaya
// düşmüştür — test kümesindeki %25.8 mutabakatın ta kendisi.
//
// Eşikler replica_consistency.go'dan ÖDÜNÇ ALINIR (replicaDivergePct /
// replicaDivergeMinRows): aynı olguyu iki yüzey farklı eşikle yargılarsa
// kart "tutarlı" derken bu panel "ayrışmış" der.

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// RawSpanHost — bir host'un yerel ham span sayımı. Shard < 0 = host
// shard'a eşlenemedi ({shard} makrosu yok / küme tanımıyla uyuşmuyor);
// kıyaslara GİRMEZ, ayrı notta adı geçer.
type RawSpanHost struct {
	Host  string `json:"host"`
	Shard int    `json:"shard"`
	Count uint64 `json:"count"`
}

// RawShardSpread — bir shard'ın replikaları arasındaki yayılım.
type RawShardSpread struct {
	Shard     int     `json:"shard"`
	Hosts     int     `json:"hosts"`
	Min       uint64  `json:"min"`
	Max       uint64  `json:"max"`
	SpreadPct float64 `json:"spreadPct"`
}

// RawSpanCount — ham sayım zarfı. ByHost/ByShard ASLA null olmaz ([]).
// ByHostError dolu + Total geçerli = KISMİ sonuç: fan-out düştü ama
// Distributed toplam elde; hesaplanmış sayıyı çöpe atmak teşhisi
// zayıflatırdı.
type RawSpanCount struct {
	Total       uint64           `json:"total"`
	ByHost      []RawSpanHost    `json:"byHost"`
	ByShard     []RawShardSpread `json:"byShard"`
	WindowS     int              `json:"windowS"`
	Source      string           `json:"source"`
	Notes       []string         `json:"notes,omitempty"`
	ByHostError string           `json:"byHostError,omitempty"`
}

// rawSpanBandTolerancePct — Distributed toplamın shard bandına kıyas payı.
// Şard içi yayılımdan AYRI bir ölçü: burada kıyaslanan iki ayrı okuma
// (farklı anlar, süregelen ingest), orada tek fan-out'un satırları.
const rawSpanBandTolerancePct = 1.0

// rawShardSpreads — SAF: host sayımlarını shard'a indirger. Eşlenemeyen
// host (Shard < 0) dışarıda kalır. Sıra shard numarasına göre.
func rawShardSpreads(byHost []RawSpanHost) []RawShardSpread {
	byShard := map[int][]RawSpanHost{}
	for _, h := range byHost {
		if h.Shard < 0 {
			continue
		}
		byShard[h.Shard] = append(byShard[h.Shard], h)
	}
	out := make([]RawShardSpread, 0, len(byShard))
	for sh, hs := range byShard {
		mn, mx := hs[0].Count, hs[0].Count
		for _, h := range hs {
			if h.Count < mn {
				mn = h.Count
			}
			if h.Count > mx {
				mx = h.Count
			}
		}
		var pct float64
		if mx > 0 {
			pct = float64(mx-mn) / float64(mx) * 100
		}
		out = append(out, RawShardSpread{Shard: sh, Hosts: len(hs), Min: mn, Max: mx, SpreadPct: pct})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Shard < out[j].Shard })
	return out
}

// rawHostList — SAF: bir shard'ın host'ları "ch-01 900 · ch-02 400".
func rawHostList(shard int, byHost []RawSpanHost) string {
	var hs []RawSpanHost
	for _, h := range byHost {
		if h.Shard == shard {
			hs = append(hs, h)
		}
	}
	sort.Slice(hs, func(i, j int) bool { return hs[i].Host < hs[j].Host })
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, fmt.Sprintf("host %s %d", h.Host, h.Count))
	}
	return strings.Join(parts, " · ")
}

// rawSpanAnalysis — SAF: shard indirgemesi + notlar. Üç ayrı soru:
//  1. Şard İÇİ yayılım (replicaDivergePct / replicaDivergeMinRows) —
//     ayrışma budur. Şardlar ARASI fark normaldir, hiç bakılmaz.
//  2. Eşlenemeyen host — kıyasa girmedi, sessiz kalmamalı.
//  3. Distributed toplam shard bandının [Σmin, Σmax] dışında mı — okuma
//     eksik veri tutan replikaya düşmüş.
//
// byHost boşsa (fan-out düştü / tek düğüm hatası) hiç not yok: bilinmezlik
// ayrışma değildir.
func rawSpanAnalysis(total uint64, byHost []RawSpanHost) ([]RawShardSpread, []string) {
	spreads := rawShardSpreads(byHost)
	if len(byHost) == 0 {
		return spreads, nil
	}
	var notes []string
	for _, sp := range spreads {
		if sp.Hosts < 2 || sp.Max < replicaDivergeMinRows || sp.SpreadPct <= replicaDivergePct {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"shard %d replikaları farklı veri tutuyor (%s) — Admin → Replika tutarlılığı / onarımı",
			sp.Shard, rawHostList(sp.Shard, byHost)))
	}
	var unmapped []string
	for _, h := range byHost {
		if h.Shard < 0 {
			unmapped = append(unmapped, h.Host)
		}
	}
	if len(unmapped) > 0 {
		sort.Strings(unmapped)
		notes = append(notes, fmt.Sprintf(
			"%s: shard'a eşlenemedi ({shard} makrosu yok ya da küme tanımıyla uyuşmuyor) — kıyasa girmedi",
			strings.Join(unmapped, ", ")))
	}
	if len(spreads) == 0 {
		return spreads, notes
	}
	var lo, hi uint64
	for _, sp := range spreads {
		lo += sp.Min
		hi += sp.Max
	}
	tol := func(n uint64) float64 { return float64(n) * rawSpanBandTolerancePct / 100 }
	if float64(total) < float64(lo)-tol(lo) || float64(total) > float64(hi)+tol(hi) {
		notes = append(notes, fmt.Sprintf(
			"Distributed toplamı (%d) shard bandının dışında [%d, %d]: okuma eksik veri tutan replikaya düştü",
			total, lo, hi))
	}
	return spreads, notes
}

// rawSpanTotalSQL — SAF: iki zaman sınırı + tavan + bütçe, tek satır döner.
// Bütçe FROM spans ile AYNI ifadede (denetim CHECK 6 buna bakar).
func rawSpanTotalSQL() string {
	return `
		SELECT count()
		FROM spans
		WHERE time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')
		LIMIT 1
		SETTINGS max_execution_time = 15`
}

// rawSpanByHostSQL — SAF: küme geneli host başına yerel sayım. Tablo adı
// LocalTableName'den gelir (spans → spans_local), küme adı literal —
// clusterAllReplicas ilk argümanı bağlanamaz.
//
// v0.10.826 — operatör hatası: tablo argümanı ÇIPLAK `spans_local`
// geçiliyordu, ClickHouse kod 42 ile reddediyordu ("Table name was not
// found in function arguments"). KULLANICI tablosu veritabanıyla
// nitelenmiş olmalı; system.* adları veritabanını kendi taşıdığı için
// kardeş okumalar çalışıyordu. Şekil v0.10.810'daki yedek okumadan
// kopyalanmıştı — ikisi de burada nitelendi.
//
// currentDatabase() YOK: tablo fonksiyonunun argümanı BAŞLATAN düğümde
// çözülür, uzak düğümde başka bir veritabanına işaret edebilir
// (replica_consistency.go ile aynı disiplin). Yapılandırılmış ad ve
// tablo adı chObjRe ile doğrulanıp backtick'le nitelenir; doğrulama
// düşerse SQL kurulmaz — çağıran hatayı ByHostError'a yazar.
func rawSpanByHostSQL(cluster, db, localTable string) (string, error) {
	if !chObjRe.MatchString(db) || !chObjRe.MatchString(localTable) {
		return "", fmt.Errorf("geçersiz ad (db %q, tablo %q) — clusterAllReplicas argümanına eklenmedi", db, localTable)
	}
	return `
		SELECT hostName() AS host, count() AS n
		FROM clusterAllReplicas('` + cluster + "', `" + db + "`.`" + localTable + "`" + `)
		WHERE time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')
		GROUP BY 1
		ORDER BY 1
		LIMIT 1000
		SETTINGS max_execution_time = 15, skip_unavailable_shards = 1`, nil
}

// RawSpanCounts — [from, to) penceresinde ham span sayımı. Pencere
// ÇAĞIRAN tarafından verilir ve MV sayısının penceresiyle AYNI olmalıdır.
//
// Hata sözleşmesi: dönen hata YALNIZ Distributed toplam okunamadığında
// dolar. Host başına fan-out düşerse toplam yine döner, sebep
// ByHostError'dadır (kısmi sonuç > hiç sonuç).
func (s *Store) RawSpanCounts(ctx context.Context, from, to time.Time) (RawSpanCount, error) {
	out := RawSpanCount{
		ByHost: []RawSpanHost{}, ByShard: []RawShardSpread{},
		WindowS: int(to.Sub(from) / time.Second),
	}
	lo, hi := chDateTime64Arg(from), chDateTime64Arg(to)
	if err := s.telemetryReadConn().QueryRow(ctx, rawSpanTotalSQL(), lo, hi).Scan(&out.Total); err != nil {
		return out, fmt.Errorf("ham span toplamı: %w", err)
	}
	if !s.clusterMode() {
		// Tek düğüm: host başına sayım = toplamın kendisi. İkinci bir tam
		// tarama aynı sayıyı verir, yalnız iki kat pahalıya — host adı ucuz
		// bir skalerle alınır. Shard 0: tek shard, yayılım tanımsız değil sıfır.
		out.Source = "spans (tek düğüm)"
		var host string
		if err := s.conn.QueryRow(ctx, "SELECT hostName()").Scan(&host); err != nil {
			out.ByHostError = err.Error()
			return out, nil
		}
		out.ByHost = append(out.ByHost, RawSpanHost{Host: host, Shard: 0, Count: out.Total})
		out.ByShard, out.Notes = rawSpanAnalysis(out.Total, out.ByHost)
		return out, nil
	}
	cluster := s.cfg.ClusterName
	local := s.LocalTableName("spans")
	// Etiket BİLEREK ayraçsız: "clusterAllReplicas(" düzyazısı kaynak
	// pininin (cluster_all_replicas_args_test.go, v0.10.826) taradığı
	// şekilden ayrılmalı — gate kendi metnini ısırmasın.
	out.Source = "spans (Distributed, shard başına bir replika) + clusterAllReplicas · " + local + " (her replika)"
	// system.* okumalarıyla aynı gerekçe (replica_consistency.go): hostName()
	// kimliği taşıyan küme geneli okuma ANA bağlantıda koşar, RoundRobin
	// telemetri havuzunda değil.
	byHostQ, qerr := rawSpanByHostSQL(cluster, s.cfg.Database, local)
	if qerr != nil {
		out.ByHostError = qerr.Error()
		return out, nil
	}
	rows, err := s.conn.Query(ctx, byHostQ, lo, hi)
	if err != nil {
		out.ByHostError = err.Error()
		return out, nil
	}
	hosts := []RawSpanHost{}
	scanErr := func() error {
		defer rows.Close()
		for rows.Next() {
			h := RawSpanHost{Shard: -1}
			if err := rows.Scan(&h.Host, &h.Count); err != nil {
				return err
			}
			hosts = append(hosts, h)
		}
		return rows.Err() // yarım liste = uydurma "ayrışma"
	}()
	if scanErr != nil {
		out.ByHostError = scanErr.Error()
		return out, nil
	}
	names := make([]string, 0, len(hosts))
	for _, h := range hosts {
		names = append(names, h.Host)
	}
	// Şard eşlemesi Replika tutarlılığı kartıyla AYNI makineden; okunamazsa
	// host'lar -1 kalır ve "eşlenemedi" notuna düşer (sessiz yanlış kıyas yok).
	shards, err := s.hostShardMap(ctx, cluster, names)
	if err != nil {
		out.ByHostError = "shard eşlemesi okunamadı: " + err.Error()
	}
	for i := range hosts {
		if sh, ok := shards[hosts[i].Host]; ok {
			hosts[i].Shard = sh
		}
	}
	out.ByHost = hosts
	out.ByShard, out.Notes = rawSpanAnalysis(out.Total, out.ByHost)
	return out, nil
}
