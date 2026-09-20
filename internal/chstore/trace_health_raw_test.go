package chstore

// trace_health_raw_test.go — v0.10.823: isteğe bağlı ham sayımın sözleşmesi.
//
// EN PAHALI VAKA (inceleme bulgusu, ilk yazımda hatalıydı): sağlıklı RF=2
// kümede clusterAllReplicas toplamı Distributed toplamının İKİ KATIDIR.
// İlk hâli bu iki sayıyı doğrudan kıyaslıyor ve sağlıklı kümede
// "replikalar ayrışmış" diyordu — not TERSİNE çalışıyordu. Doğru sinyal
// ŞARD İÇİ yayılım; şardlar arası fark shard anahtarının kendisidir.
//
// Sözleşme: (1) ham sorgular zaman sınırlı + tavanlı + bütçeli, bütçe FROM
// ile AYNI ifadede; (2) küme sorgusu clusterAllReplicas + yerel tablo +
// skip_unavailable_shards, currentDatabase() YOK; (3) eşikler
// replica_consistency.go'dan ödünç; (4) host başına okuma düşerse toplam
// YİNE döner (kısmi sonuç).

import (
	"os"
	"strings"
	"testing"
)

func TestRawSpanSQLBounded(t *testing.T) {
	total := rawSpanTotalSQL()
	for _, w := range []string{
		"FROM spans",
		"time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')",
		"LIMIT 1",
		"max_execution_time = 15",
	} {
		if !strings.Contains(total, w) {
			t.Errorf("toplam sorgusu %q içermeli:\n%s", w, total)
		}
	}
	byHost := rawSpanByHostSQL("uptrace_all", "spans_local")
	for _, w := range []string{
		"clusterAllReplicas('uptrace_all', spans_local)",
		"hostName()",
		"time >= toDateTime64(?, 9, 'UTC') AND time < toDateTime64(?, 9, 'UTC')",
		"GROUP BY 1",
		"ORDER BY 1",
		"LIMIT 1000",
		"max_execution_time = 15",
		"skip_unavailable_shards = 1",
	} {
		if !strings.Contains(byHost, w) {
			t.Errorf("host sorgusu %q içermeli:\n%s", w, byHost)
		}
	}
	if strings.Contains(byHost, "currentDatabase()") {
		t.Error("küme geneli sorgu currentDatabase() ÇÖZMEMELİ (her düğüm kendi varsayılanını kullanır)")
	}
	if s := rawSpanByHostSQL("c", "spans"); !strings.Contains(s, "max_execution_time = 15") {
		t.Errorf("tek düğüm adıyla da bütçeli olmalı:\n%s", s)
	}
}

func TestRawSpanCountsSourceContract(t *testing.T) {
	b, err := os.ReadFile("trace_health_raw.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	for _, w := range []string{
		"rows.Err()",                                 // yarım liste = uydurma ayrışma
		"s.conn.Query(ctx, rawSpanByHostSQL(",        // ana bağlantı (hostName() kimliği)
		"s.hostShardMap(ctx, cluster, names)",        // eşleme Replika kartıyla AYNI makineden
		"out.ByHostError = ",                         // kısmi sonuç
		"replicaDivergeMinRows", "replicaDivergePct", // eşikler ödünç
	} {
		if !strings.Contains(src, w) {
			t.Errorf("trace_health_raw.go %q içermeli", w)
		}
	}
	// Host başına okuma düştüğünde toplam KORUNMALI: o dallarda hata dönmez.
	if strings.Contains(src, "return out, fmt.Errorf(\"host başına ham sayım") {
		t.Error("host başına hata toplamı çöpe atmamalı — ByHostError'a yazılmalı")
	}
}

func TestRawShardSpreads(t *testing.T) {
	got := rawShardSpreads([]RawSpanHost{
		{Host: "ch-02", Shard: 1, Count: 400}, {Host: "ch-01", Shard: 1, Count: 900},
		{Host: "ch-03", Shard: 2, Count: 1000}, {Host: "ch-04", Shard: 2, Count: 1000},
		{Host: "ch-05", Shard: -1, Count: 777}, // eşlenemedi → hiç girmez
	})
	if len(got) != 2 || got[0].Shard != 1 || got[1].Shard != 2 {
		t.Fatalf("shard'a göre sıralı iki satır bekleniyordu: %+v", got)
	}
	if got[0].Min != 400 || got[0].Max != 900 || got[0].Hosts != 2 {
		t.Errorf("shard 1 özeti yanlış: %+v", got[0])
	}
	if d := got[0].SpreadPct - 55.5555; d > 0.01 || d < -0.01 {
		t.Errorf("yayılım (900-400)/900 ≈ %%55.6 olmalı: %v", got[0].SpreadPct)
	}
	if got[1].SpreadPct != 0 {
		t.Errorf("eşit replikalarda yayılım 0 olmalı: %v", got[1].SpreadPct)
	}
	if len(rawShardSpreads(nil)) != 0 {
		t.Error("boş girdi → boş özet")
	}
	// Sıfır satırlı shard: bölme yok, yayılım 0.
	if s := rawShardSpreads([]RawSpanHost{{Host: "a", Shard: 3, Count: 0}}); s[0].SpreadPct != 0 {
		t.Errorf("0 sayımda yayılım 0 olmalı: %+v", s[0])
	}
}

func TestRawSpanAnalysis(t *testing.T) {
	const big = uint64(1_000_000)
	cases := []struct {
		name      string
		total     uint64
		byHost    []RawSpanHost
		wantNotes int
		wantAny   string // notlardan birinde geçmeli
	}{
		{
			// SAĞLIKLI RF=2, iki shard: host toplamı (4M) Distributed toplamının
			// (2M) iki katı — bu NORMAL. Hiçbir not olmamalı.
			name: "RF=2 sağlıklı, iki shard", total: 2 * big,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big},
				{Host: "ch-03", Shard: 2, Count: big}, {Host: "ch-04", Shard: 2, Count: big},
			},
			wantNotes: 0,
		},
		{
			// Şardlar arası fark (shard anahtarı) tek başına not üretmez.
			name: "şardlar arası fark normal", total: 1_500_000,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big},
				{Host: "ch-03", Shard: 2, Count: 500_000}, {Host: "ch-04", Shard: 2, Count: 500_000},
			},
			wantNotes: 0,
		},
		{
			// Bir shard'ın replikaları ayrık: not O SHARD'ı adlandırmalı.
			name: "shard 1 replikaları ayrışmış", total: 1_100_000,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: 900_000}, {Host: "ch-02", Shard: 1, Count: 400_000},
				{Host: "ch-03", Shard: 2, Count: 200_000}, {Host: "ch-04", Shard: 2, Count: 200_000},
			},
			wantNotes: 1, wantAny: "shard 1 replikaları farklı veri tutuyor (host ch-01 900000 · host ch-02 400000)",
		},
		{
			// Eşik altı: 5000 satır tabanının altında yayılım not üretmez.
			name: "taban altı yayılım sessiz", total: 4000,
			byHost:    []RawSpanHost{{Host: "ch-01", Shard: 1, Count: 4000}, {Host: "ch-02", Shard: 1, Count: 10}},
			wantNotes: 0,
		},
		{
			// %2 (replicaDivergePct) altı yayılım: replikasyon gecikmesi, sessiz.
			name: "eşik altı yüzde sessiz", total: 100_000,
			byHost:    []RawSpanHost{{Host: "ch-01", Shard: 1, Count: 100_000}, {Host: "ch-02", Shard: 1, Count: 99_000}},
			wantNotes: 0,
		},
		{
			name: "tek düğüm", total: 50_000,
			byHost:    []RawSpanHost{{Host: "ch-01", Shard: 0, Count: 50_000}},
			wantNotes: 0,
		},
		{
			name: "eşlenemeyen host kıyasa girmez ama adı geçer", total: 2 * big,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big},
				{Host: "ch-03", Shard: 2, Count: big}, {Host: "ch-04", Shard: 2, Count: big},
				{Host: "ch-05", Shard: -1, Count: 12345},
			},
			wantNotes: 1, wantAny: "ch-05: shard'a eşlenemedi",
		},
		{
			// %25.8 semptomu: Distributed okuma eksik replikaya düşmüş.
			name: "Distributed toplam bandın altında", total: 300_000,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big},
				{Host: "ch-03", Shard: 2, Count: big}, {Host: "ch-04", Shard: 2, Count: big},
			},
			wantNotes: 1, wantAny: "shard bandının dışında [2000000, 2000000]",
		},
		{
			name: "Distributed toplam bandın üstünde", total: 5 * big,
			byHost: []RawSpanHost{
				{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big},
			},
			wantNotes: 1, wantAny: "shard bandının dışında",
		},
		{
			// Band payı (%1) içindeki fark: süregelen ingest, not yok.
			name: "band payı içinde", total: 1_005_000,
			byHost:    []RawSpanHost{{Host: "ch-01", Shard: 1, Count: big}, {Host: "ch-02", Shard: 1, Count: big}},
			wantNotes: 0,
		},
		{
			name: "host listesi boş (fan-out düştü) → not yok", total: big,
			byHost: nil, wantNotes: 0,
		},
		{
			name: "boş pencere", total: 0,
			byHost:    []RawSpanHost{{Host: "ch-01", Shard: 1, Count: 0}, {Host: "ch-02", Shard: 1, Count: 0}},
			wantNotes: 0,
		},
	}
	for _, c := range cases {
		spreads, notes := rawSpanAnalysis(c.total, c.byHost)
		if len(notes) != c.wantNotes {
			t.Errorf("%s: %d not, beklenen %d — %v", c.name, len(notes), c.wantNotes, notes)
			continue
		}
		if c.wantAny != "" {
			found := false
			for _, n := range notes {
				if strings.Contains(n, c.wantAny) {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: notlarda %q yok — %v", c.name, c.wantAny, notes)
			}
		}
		// Eşlenemeyen host hiçbir shard özetine sızmamalı.
		for _, sp := range spreads {
			if sp.Shard < 0 {
				t.Errorf("%s: eşlenemeyen host özete girdi: %+v", c.name, sp)
			}
		}
	}
}

// Eşikler Replika tutarlılığı kartıyla AYNI olmalı: iki yüzey aynı olguyu
// farklı eşikle yargılarsa biri "tutarlı" derken öteki "ayrışmış" der.
func TestRawSpanUsesReplicaConsistencyThresholds(t *testing.T) {
	just := uint64(replicaDivergeMinRows)
	hosts := []RawSpanHost{{Host: "ch-01", Shard: 1, Count: just}, {Host: "ch-02", Shard: 1, Count: just / 2}}
	if _, notes := rawSpanAnalysis(just, hosts); len(notes) == 0 {
		t.Error("tam tabanda (replicaDivergeMinRows) %50 yayılım not vermeli")
	}
	below := []RawSpanHost{{Host: "ch-01", Shard: 1, Count: just - 1}, {Host: "ch-02", Shard: 1, Count: 0}}
	for _, n := range mustNotes(rawSpanAnalysis(just-1, below)) {
		if strings.Contains(n, "replikaları farklı veri tutuyor") {
			t.Errorf("taban altında ayrışma notu olmamalı: %q", n)
		}
	}
}

func mustNotes(_ []RawShardSpread, notes []string) []string { return notes }
