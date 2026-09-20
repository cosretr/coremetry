package chstore

import (
	"os"
	"strings"
	"testing"
)

// v0.10.822 — okuma tarafı replika seçimi (operatör 2026-09-19, test kümesi:
// aynı sorgu her yenilemede FARKLI sayı döndürüyor, çünkü load_balancing=random
// her Distributed SELECT'te shard başına başka replika seçiyor ve replikalar
// ıraksamış). Sözleşme: chReadBalancingSettings SAF bir eşleyici — env'den
// gelen iki dizeyi CH ayarlarına çevirir, tanımadığını SESSİZCE DÜŞÜRMEZ,
// insan-okunur uyarı döndürür. Tek gövde: izin listesi yalnız burada yaşar
// (config yalnız taşır), böylece ikiz kural sapamaz.
func TestChReadBalancingSettings(t *testing.T) {
	cases := []struct {
		name     string
		lb       string
		prefer   string
		want     map[string]any
		wantWarn []string // her biri uyarı metninde aranan parça
	}{
		{name: "ikisi de boş — CH varsayılanı, hiçbir ayar gönderilmez"},
		{name: "in_order", lb: "in_order", want: map[string]any{"load_balancing": "in_order"}},
		{name: "first_or_random", lb: "first_or_random", want: map[string]any{"load_balancing": "first_or_random"}},
		{name: "random", lb: "random", want: map[string]any{"load_balancing": "random"}},
		{name: "nearest_hostname", lb: "nearest_hostname", want: map[string]any{"load_balancing": "nearest_hostname"}},
		{name: "round_robin", lb: "round_robin", want: map[string]any{"load_balancing": "round_robin"}},
		{name: "boşluk + büyük harf normalize edilir", lb: "  IN_ORDER  ", want: map[string]any{"load_balancing": "in_order"}},
		{
			name: "izin listesi dışı — ayar YOK, uyarı env adını ve listeyi söyler",
			lb:   "in-order",
			wantWarn: []string{
				"COREMETRY_CH_READ_LOAD_BALANCING", "in-order",
				"in_order", "first_or_random", "random", "nearest_hostname", "round_robin",
			},
		},
		{name: "prefer 1", prefer: "1", want: map[string]any{"prefer_localhost_replica": 1}},
		{name: "prefer true", prefer: "true", want: map[string]any{"prefer_localhost_replica": 1}},
		{name: "prefer on", prefer: "on", want: map[string]any{"prefer_localhost_replica": 1}},
		{name: "prefer yes", prefer: "yes", want: map[string]any{"prefer_localhost_replica": 1}},
		{name: "prefer TRUE büyük harf", prefer: "TRUE", want: map[string]any{"prefer_localhost_replica": 1}},
		{name: "prefer 0", prefer: "0", want: map[string]any{"prefer_localhost_replica": 0}},
		{name: "prefer false", prefer: "false", want: map[string]any{"prefer_localhost_replica": 0}},
		{name: "prefer off", prefer: "off", want: map[string]any{"prefer_localhost_replica": 0}},
		{name: "prefer no", prefer: "no", want: map[string]any{"prefer_localhost_replica": 0}},
		{name: "prefer boşluklu false", prefer: "  False ", want: map[string]any{"prefer_localhost_replica": 0}},
		{
			name:     "prefer çöp — ayar YOK, uyarı env adını söyler",
			prefer:   "maybe",
			wantWarn: []string{"COREMETRY_CH_READ_PREFER_LOCALHOST", "maybe", "0", "1"},
		},
		{
			name:     "prefer 2 sayı da olsa izinli değil",
			prefer:   "2",
			wantWarn: []string{"COREMETRY_CH_READ_PREFER_LOCALHOST", "2"},
		},
		{
			// Test kümesi önerisi İKİSİ BİRLİKTE: in_order + 0. Yalnız
			// in_order yetmez — prefer_localhost_replica CH'de VARSAYILAN 1
			// ve koordinatörün kendi barındırdığı shard'da load_balancing'e
			// hiç bakılmadan yerel plan seçilir; okuma havuzu RoundRobin
			// olduğu için koordinatör döner ve sayı yine zıplar.
			name: "test kümesi önerisi: in_order + 0 birlikte",
			lb:   "in_order", prefer: "0",
			want: map[string]any{"load_balancing": "in_order", "prefer_localhost_replica": 0},
		},
		{
			name: "ikisi de çöp — iki ayrı uyarı, hiç ayar yok",
			lb:   "sticky", prefer: "sometimes",
			wantWarn: []string{"COREMETRY_CH_READ_LOAD_BALANCING", "COREMETRY_CH_READ_PREFER_LOCALHOST"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, warns := chReadBalancingSettings(c.lb, c.prefer)
			if len(got) != len(c.want) {
				t.Fatalf("ayar sayısı %d, istenen %d (got=%v)", len(got), len(c.want), got)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("%s = %#v, istenen %#v", k, got[k], v)
				}
			}
			joined := strings.Join(warns, " | ")
			if len(c.wantWarn) == 0 {
				if len(warns) != 0 {
					t.Errorf("uyarı beklenmiyordu: %s", joined)
				}
				return
			}
			for _, w := range c.wantWarn {
				if !strings.Contains(joined, w) {
					t.Errorf("uyarı %q içermeli, gelen: %s", w, joined)
				}
			}
		})
	}
}

// v0.10.822 — boot logu ETKİN değeri yazar: gönderilmeyen ayar CH
// varsayılanı olarak görünür, geçersiz env "uygulanmış" gibi loglanmaz.
func TestChReadBalancingEffective(t *testing.T) {
	cases := []struct {
		lb, prefer, wantLB, wantPrefer string
	}{
		{"", "", "random (CH varsayılanı)", "1 (CH varsayılanı)"},
		{"in_order", "0", "in_order", "0"},
		{"in_order", "", "in_order", "1 (CH varsayılanı)"},
		{"", "1", "random (CH varsayılanı)", "1"},
		// Geçersiz env: varsayılan görünür, uygulanmış gibi DEĞİL.
		{"sticky", "maybe", "random (CH varsayılanı)", "1 (CH varsayılanı)"},
	}
	for _, c := range cases {
		rb, _ := chReadBalancingSettings(c.lb, c.prefer)
		lb, prefer := chReadBalancingEffective(rb)
		if lb != c.wantLB || prefer != c.wantPrefer {
			t.Errorf("(%q,%q) → (%q,%q), istenen (%q,%q)", c.lb, c.prefer, lb, prefer, c.wantLB, c.wantPrefer)
		}
	}
}

// v0.10.822 — izin listesinin KAPSAMI ölçülür: listedeki her yazım ayar
// üretmeli, listede OLMAYAN makul yazımlar (CH'de gerçekten var olmayanlar
// dahil) uyarıya düşmeli. Varlık değil YOKLUK ölçen kapı.
func TestChReadBalancingAllowlistCoverage(t *testing.T) {
	for _, ok := range []string{"in_order", "first_or_random", "random", "nearest_hostname", "round_robin"} {
		s, w := chReadBalancingSettings(ok, "")
		if s["load_balancing"] != ok || len(w) != 0 {
			t.Errorf("%q izinli olmalı: ayar=%#v uyarı=%v", ok, s["load_balancing"], w)
		}
	}
	for _, bad := range []string{"in order", "inorder", "hostname", "roundrobin", "first_or_nearest", "nearest", "0", "true"} {
		s, w := chReadBalancingSettings(bad, "")
		if _, set := s["load_balancing"]; set || len(w) == 0 {
			t.Errorf("%q izinli OLMAMALI: ayar=%#v uyarı=%v", bad, s["load_balancing"], w)
		}
	}
}

// v0.10.822 (inceleme düzeltmesi) — KAPSAM pini: ayarlar PAYLAŞILAN chOpts
// kapanışında UYGULANMAZ, çünkü ingest havuzu da oradan doğar ve her iki ayar
// Distributed INSERT'te de replika seçer (internal_replication açıkken sink
// aynı load_balancing önceliğini kullanır, yerel-yazma kestirmesini yalnız
// prefer_localhost_replica=1 iken alır). Yalnız ana bağlantı + okuma havuzu.
// Uyarılar boot'ta BİR kez loglanır.
func TestChReadBalancingSourcePins(t *testing.T) {
	b, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	i := strings.Index(src, "chOpts := func() *clickhouse.Options {")
	if i < 0 {
		t.Fatal("chOpts kurucusu yok")
	}
	block := src[i:]
	if j := strings.Index(block, "\n\t}\n"); j > 0 {
		block = block[:j]
	}
	if !strings.Contains(block, "max_replica_delay_for_distributed_queries") {
		t.Error("beklenen komşu blok (v0.10.790) yok — pencere kaymış olabilir")
	}
	for _, forbidden := range []string{"applyReadBalancing(", "chReadBalancingSettings("} {
		if strings.Contains(block, forbidden) {
			t.Errorf("%s PAYLAŞILAN chOpts kapanışında OLMAMALI — ingest havuzu da oradan doğar", forbidden)
		}
	}
	// Ayarlar bir kez hesaplanır (uyarı logu dahil), iki yerde uygulanır.
	if n := strings.Count(src, "chReadBalancingSettings("); n != 1 {
		t.Errorf("tek hesap bekleniyordu (boot'ta), bulunan: %d", n)
	}
	if n := strings.Count(src, "applyReadBalancing("); n != 2 {
		t.Errorf("iki uygulama bekleniyordu (ana bağlantı + okuma havuzu), bulunan: %d", n)
	}
	for _, want := range []string{"applyReadBalancing(mainOpts, readBalancing)", "applyReadBalancing(readOpts, readBalancing)"} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// Ingest havuzu dokunulmadan kalır: opts'un doğduğu satırdan Open'a
	// kadarki pencerede hiçbir çağrı olmamalı (pencere komşuya taşmasın).
	ib := strings.Index(src, "ingestOpts := chOpts()")
	ie := strings.Index(src, "clickhouse.Open(ingestOpts)")
	if ib < 0 || ie <= ib {
		t.Fatal("ingest havuzu penceresi bulunamadı")
	}
	if strings.Contains(src[ib:ie], "applyReadBalancing") {
		t.Error("ingest havuzuna replika seçimi uygulanmamalı (yazma dağılımı bozulur)")
	}
	if strings.Contains(src, "applyReadBalancing(ingestOpts") {
		t.Error("ingestOpts hiçbir yerde replika seçimi almamalı")
	}
	// Uyarılar + ETKİN değer boot'ta görünür.
	if !strings.Contains(src, "chReadBalancingEffective(readBalancing)") {
		t.Error("boot logu ETKİN değeri yazmalı (ham env değil)")
	}
	if k := strings.Index(src, "chReadBalancingSettings("); k > 0 {
		tail := src[k:min(k+400, len(src))]
		if !strings.Contains(tail, "log.Printf") || !strings.Contains(tail, "WARNING") {
			t.Error("chReadBalancingSettings uyarıları boot'ta WARNING olarak loglanmalı")
		}
	}
	for _, env := range []string{"COREMETRY_CH_READ_LOAD_BALANCING", "COREMETRY_CH_READ_PREFER_LOCALHOST"} {
		if !strings.Contains(src, env) {
			t.Errorf("boot log satırı %s adını taşımalı (etkin değer tek satırdan okunsun)", env)
		}
	}
	for _, name := range []string{`"load_balancing"`, `"prefer_localhost_replica"`} {
		if strings.Contains(src, name) {
			t.Errorf("%s ayar adı store.go'da değil, read_balancing.go'da yaşamalı (tek gövde)", name)
		}
	}
	hb, err := os.ReadFile("read_balancing.go")
	if err != nil {
		t.Fatal(err)
	}
	h := string(hb)
	for _, name := range []string{`"load_balancing"`, `"prefer_localhost_replica"`} {
		if n := strings.Count(h, name); n != 1 {
			t.Errorf("%s tek yerde (sabit tanımında) olmalı (bulunan: %d)", name, n)
		}
	}
	// Gerekçe kodda kalsın: ingest dışlaması ve varsayılan-1 kestirmesi.
	for _, want := range []string{"INGEST HAVUZUNA UYGULANMAZ", "VARSAYILAN 1", "in_order + 0"} {
		if !strings.Contains(h, want) {
			t.Errorf("read_balancing.go başlığı %q gerekçesini taşımalı", want)
		}
	}
}

// v0.10.822 — İMAJ VARSAYILANI YOK: bu iki anahtar okuma davranışını
// değiştirir ve ıraksamayı GİZLER; her kurulum kendi kararını versin.
// Dockerfile yalnız yorumla yol gösterir, docs/ENV.md satırları taşır.
func TestReadBalancingHasNoImageDefault(t *testing.T) {
	df, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	d := string(df)
	for _, env := range []string{"COREMETRY_CH_READ_LOAD_BALANCING", "COREMETRY_CH_READ_PREFER_LOCALHOST"} {
		if strings.Contains(d, "ENV "+env+"=") {
			t.Errorf("%s imaj varsayılanı OLMAMALI (CH varsayılanı kalsın)", env)
		}
		if !strings.Contains(d, env) {
			t.Errorf("Dockerfile %s anahtarını yorumla anlatmalı", env)
		}
	}
	// Yorum, anahtarın NE YAPMADIĞINI ve nereye gidileceğini söylemeli:
	// ayrışmayı gizler, veriyi düzeltmez → Replika tutarlılığı / onarımı.
	for _, want := range []string{"DÜZELTMEZ", "Replika tutarlılığı", "Replika onarımı"} {
		if !strings.Contains(d, want) {
			t.Errorf("Dockerfile yorumu %q taşımalı", want)
		}
	}
	// İnceleme düzeltmesi: öneri in_order + 0; "+ 1" geri sızmasın
	// (prefer_localhost_replica=1 CH VARSAYILANI ve load_balancing'i
	// koordinatörün kendi shard'ında devre dışı bırakır).
	if !strings.Contains(d, "in_order + 0") {
		t.Error("Dockerfile önerisi 'in_order + 0' olmalı (ikisi birlikte)")
	}
	if strings.Contains(d, "in_order + 1") {
		t.Error("'in_order + 1' önerisi YANLIŞ — prefer_localhost_replica=1 yerel kestirmeyi açık bırakır")
	}
	envmd, err := os.ReadFile("../../docs/ENV.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, env := range []string{"COREMETRY_CH_READ_LOAD_BALANCING", "COREMETRY_CH_READ_PREFER_LOCALHOST"} {
		if !strings.Contains(string(envmd), env) {
			t.Errorf("docs/ENV.md %s satırını taşımalı", env)
		}
	}
	e := string(envmd)
	// docs/ENV.md aynı gerçeği taşımalı: PREFER satırı 'boş = CH varsayılanı
	// 1' demeli ve iki satır da ingest dışlamasını söylemeli.
	for _, want := range []string{"CH varsayılanı (`1`)", "ingest havuzuna uygulanmaz"} {
		if !strings.Contains(e, want) {
			t.Errorf("docs/ENV.md %q taşımalı", want)
		}
	}
	if n := strings.Count(e, "ingest havuzuna uygulanmaz"); n != 2 {
		t.Errorf("iki satır da ingest dışlamasını söylemeli (bulunan: %d)", n)
	}
}
