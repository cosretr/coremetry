package chstore

// dangling_mv_admin_test.go — v0.10.762 sarkan MV: saf tespit (host başına,
// TO'lu MV hariç, sıfır uuid hariç), nesne adı → kanonik ad, ON CLUSTER
// sökme; ulaşılabilirlik: dropCombinedMV artık temizliğini çağırır, boot
// dedektörü New()'da.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestDanglingFromRows(t *testing.T) {
	rows := []mvTableRow{
		// sağlıklı: view + iç tablo aynı host
		{Host: "n1", Name: "service_summary_5m_local", UUID: "aaaa", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.service_summary_5m_local (`x` UInt8) ENGINE = ReplicatedAggregatingMergeTree AS SELECT"},
		{Host: "n1", Name: ".inner_id.aaaa", UUID: "1111", Engine: "ReplicatedAggregatingMergeTree"},
		// sarkan: view var, iç tablo yok (n2)
		{Host: "n2", Name: "service_summary_5m_local", UUID: "bbbb", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.service_summary_5m_local (`x` UInt8) ENGINE = ReplicatedAggregatingMergeTree AS SELECT"},
		// iç tablo başka host'ta olsa sayılmaz (host başına)
		{Host: "n1", Name: ".inner_id.bbbb", UUID: "2222", Engine: "ReplicatedAggregatingMergeTree"},
		// TO'lu MV: iç tablosu olmaz → sarkan değil
		{Host: "n2", Name: "span_links_reverse_mv", UUID: "cccc", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT"},
		// sıfır uuid → atla
		{Host: "n2", Name: "weird", UUID: zeroUUID, Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.weird ENGINE = AggregatingMergeTree AS SELECT"},
		// kanonik olmayan sarkan (migrations MV'si) → listelenir, Canonical=false
		{Host: "n2", Name: "rollup_custom_mv", UUID: "dddd", Engine: "MaterializedView", CreateQuery: "CREATE MATERIALIZED VIEW coremetry.rollup_custom_mv ENGINE = AggregatingMergeTree AS SELECT"},
	}
	got := danglingFromRows(rows, map[string]int{"n1": 1, "n2": 1})
	if len(got) != 2 {
		t.Fatalf("2 sarkan bekleniyordu, %d: %+v", len(got), got)
	}
	if got[0].Host != "n2" || got[0].View != "rollup_custom_mv" || got[0].Canonical {
		t.Errorf("ilk satır (ada göre sıralı, kanonik değil): %+v", got[0])
	}
	if got[1].View != "service_summary_5m_local" || got[1].UUID != "bbbb" || !got[1].Canonical {
		t.Errorf("ikinci satır: %+v", got[1])
	}
}

func TestCanonicalMVForObjectAndStrip(t *testing.T) {
	if name, ok := canonicalMVForObject("service_summary_5m"); !ok || name != "service_summary_5m" {
		t.Errorf("çıplak kanonik ad: %q %v", name, ok)
	}
	if name, ok := canonicalMVForObject("service_summary_5m_local"); !ok || name != "service_summary_5m" {
		t.Errorf("_local terfi adı: %q %v", name, ok)
	}
	if _, ok := canonicalMVForObject("not_a_view"); ok {
		t.Error("bilinmeyen ad kanonik sayıldı")
	}
	in := "CREATE MATERIALIZED VIEW IF NOT EXISTS x_local ON CLUSTER `prod_eu` ENGINE = ReplicatedAggregatingMergeTree('/p/{shard}/x', '{replica}') AS SELECT 1"
	out := stripOnCluster(in)
	if strings.Contains(out, "ON CLUSTER") || !strings.Contains(out, "x_local ENGINE") {
		t.Errorf("ON CLUSTER sökülmedi: %s", out)
	}
	if stripOnCluster("CREATE TABLE t ON CLUSTER c AS x") != "CREATE TABLE t AS x" {
		t.Error("tırnaksız küme adı")
	}
	for _, bad := range []string{"", "a b", "x;drop", "`x`", "a.b"} {
		if chObjRe.MatchString(bad) {
			t.Errorf("%q nesne adı olarak kabul edildi", bad)
		}
	}
}

func TestDanglingMVReachable(t *testing.T) {
	src, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "s.dropLeftoverViewObjects(ctx, mv)") {
		t.Error("dropCombinedMV artık view temizliğini çağırmalı")
	}
	if !strings.Contains(s, "s.LogDanglingMVs(") {
		t.Error("New() boot sonrası sarkan MV logunu koşmalı")
	}
	bs, err := os.ReadFile("trace_backfill_shards.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bs), "func (s *Store) clusterHostRows(") {
		t.Error("clusterHostRows yardımcısı (tüm replikalar) yok")
	}
}

// v0.10.832 — ad ile NESNE uuid'si ayrı: `innerObjectUUIDFromDDL` yalnız
// nesne uuid'sini okur (ADI KURMAZ), `injectTableUUID` adı ve nesne uuid'sini
// ayrı parametre alır.
//
// v0.10.780 bu iki işi tek değişkende topluyordu: DDL'de `TO INNER UUID`
// görürse onu ADIN kaynağı yapıyordu. O adla tablo hiç yoktur (CH iki uuid'nin
// eşit olmasını yasaklar), yani sınıflandırma varsayılan ayarın regex'i
// eşleştirmemesine bağlıydı.
func TestInnerObjectUUIDAndInject(t *testing.T) {
	v := "11111111-1111-1111-1111-111111111111"
	const obj = "f8b2b97f-662a-4df3-a654-3a13313e363f"
	ddl := "CREATE MATERIALIZED VIEW coremetry.service_summary_5m UUID '" + v + "' TO INNER UUID '" + strings.ToUpper(obj) + "' (`time_bucket` DateTime) ENGINE = ReplicatedAggregatingMergeTree(...) AS SELECT ..."
	if got := innerObjectUUIDFromDDL(ddl); got != obj {
		t.Errorf("TO INNER UUID okunmalı (küçük harf): %q", got)
	}
	// Varsayılan ayarda (uuid'ler metinden silinmiş) nesne uuid'si YOKTUR —
	// view uuid'sine DÜŞMEZ, "" döner ve çağıran eylemi reddeder.
	if got := innerObjectUUIDFromDDL("CREATE MATERIALIZED VIEW x (a Int) ENGINE = MergeTree ORDER BY a AS SELECT 1"); got != "" {
		t.Errorf("TO INNER UUID yoksa boş dönmeli, dönen: %q", got)
	}
	// Ad DAİMA view uuid'sinden.
	if got := innerTableName(strings.ToUpper(v)); got != ".inner_id."+v {
		t.Errorf("ad view uuid'sinden (küçük harf): %q", got)
	}

	name := ".inner_id." + v // ADI kuran uuid
	show := "CREATE TABLE coremetry.`" + name + "`\n(\n    `time_bucket` DateTime\n)\nENGINE = ReplicatedAggregatingMergeTree('/clickhouse/tables/{uuid}/{shard}', '{replica}')\nORDER BY time_bucket"
	got := injectTableUUID(show, name, obj)
	want := "CREATE TABLE coremetry.`" + name + "` UUID '" + obj + "'\n(\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("NESNE uuid'si tablo adından hemen sonra eklenmeli:\n%s", got)
	}
	if strings.Contains(got, "UUID '"+v+"'") {
		t.Errorf("ADIN uuid'si nesne uuid'si olarak gömülmüş — MV hedefini bulamaz:\n%s", got)
	}
	if injectTableUUID(got, name, obj) != got {
		t.Error("zaten tablo uuid'si taşıyan DDL'e ikinci kez eklenmemeli")
	}
	if injectTableUUID("CREATE TABLE x (a Int)", name, obj) != "CREATE TABLE x (a Int)" {
		t.Error("ad eşleşmiyorsa dokunma")
	}
	// v0.10.832 — YANLIŞ POZİTİF: `TO INNER UUID '` da "UUID '" içerir.
	// Eski gövde bunu "zaten var" sayıp eklemeyi ATLIYORDU; tablo o zaman
	// rastgele bir nesne uuid'siyle doğar, kart yeşile döner, ingest kırık
	// kalır.
	mvText := "ATTACH MATERIALIZED VIEW coremetry.`" + name + "` TO INNER UUID '" + obj + "' (`x` Int) ENGINE = MergeTree ORDER BY x"
	if out := injectTableUUID(mvText, name, obj); !strings.Contains(out, "`"+name+"` UUID '"+obj+"'") {
		t.Errorf("`TO INNER UUID '` yanlış pozitifi ekleme kararını yutuyor:\n%s", out)
	}
}

// ── "Eşten kur" dalı: NESNE uuid'si açıkça istenir ───────────────────

// Sahte bağlantı TEK GÖVDE: scriptConn (mv_inner_conn_test.go). İkinci bir
// ikiz ayrışır ve iki test iki farklı "ClickHouse" ile konuşur.

// TestInnerObjectUUIDOnIsFailClosed — v0.10.832: nesne uuid'si TAHMİN
// EDİLMEZ. Varsayılan ayarda DDL metninde hiç görünmediği için sorgu ayarı
// KENDİ üzerinde açmalı; okunamıyorsa HATA döner ve çağıran eylemi reddeder
// (view uuid'sine DÜŞMEZ — o, adın uuid'sidir).
func TestInnerObjectUUIDOnIsFailClosed(t *testing.T) {
	view := "11111111-1111-1111-1111-111111111111"
	const obj = "f8b2b97f-662a-4df3-a654-3a13313e363f"
	cases := []struct {
		name    string
		ddl     string
		err     error
		want    innerObjectUUID
		wantErr bool
	}{
		{
			name: "ayar açık biçimi → nesne uuid'si",
			ddl:  "CREATE MATERIALIZED VIEW coremetry.service_summary_5m UUID '" + view + "' TO INNER UUID '" + obj + "' (x Int) AS SELECT 1",
			want: obj,
		},
		{
			name:    "uuid'siz metin → RET (view uuid'sine düşmez)",
			ddl:     "CREATE MATERIALIZED VIEW coremetry.service_summary_5m (x Int) ENGINE = AggregatingMergeTree AS SELECT 1",
			wantErr: true,
		},
		{
			name:    "TO'lu MV → RET (gizli iç tablo yok)",
			ddl:     "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT 1",
			wantErr: true,
		},
		{
			name:    "okuma hatası → RET",
			err:     errors.New("read: i/o timeout"),
			wantErr: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			conn := &scriptConn{name: "yerel", steps: []scriptStep{
				{match: "SELECT create_table_query", vals: []any{c.ddl}, err: c.err},
			}}
			s := &Store{}
			got, err := s.innerObjectUUIDOn(context.Background(), conn, "service_summary_5m")
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v, hata beklendi mi: %v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("nesne uuid'si %q, beklenen %q", got, c.want)
			}
			if string(got) == view {
				t.Error("view uuid'sine düşülmüş — o ADIN uuid'sidir, MV hedefi DEĞİL")
			}
			// Ayar BİZİM DEĞİL: metinde görünmesi için O SORGUDA açılmalı.
			if !strings.Contains(strings.Join(conn.queries, "\n"), "show_table_uuid_in_table_create_query_if_not_nil = 1") {
				t.Errorf("sorgu ayarı açmıyor — varsayılan profilde metin uuid TAŞIMAZ: %q", strings.Join(conn.queries, "\n"))
			}
			if !strings.Contains(strings.Join(conn.queries, "\n"), "max_execution_time") {
				t.Errorf("system.* okuması tavansız: %q", strings.Join(conn.queries, "\n"))
			}
		})
	}
}

// TestRepairInnerOnPins — v0.10.832 KAYNAK pini, davranış testinin YANINDA
// (yerine değil — mv_inner_peer_test.go): sıralama ve fail-closed kapıları.
func TestRepairInnerOnPins(t *testing.T) {
	// v0.10.835 — merdiven İKİYE ayrıldı: hazırlık SALT OKUMA
	// (prepareInnerFromPeer), uygulama DEĞİŞTİRİR (applyInnerFromPeer).
	// Gerekçe: hedef uuid onarımı yıkıcı adımı bu üç okumanın ÖNÜNE
	// koyuyordu ve okuma düştüğünde ad boşalmış, tablo kurulmamış kalıyordu.
	// Sözleşme DAĞILMADI, yeri değişti — pin ikisini birlikte tarar.
	// Yorumlar sökülür: muhafız kendi gerekçe metnini ısırmasın.
	prep := stripGoComments(funcBody(t, "dangling_mv_admin.go", "func (s *Store) prepareInnerFromPeer("))
	app := stripGoComments(funcBody(t, "dangling_mv_admin.go", "func (s *Store) applyInnerFromPeer("))
	ladder := stripGoComments(funcBody(t, "dangling_mv_admin.go", "func (s *Store) repairInnerOn("))
	fn := prep + "\n" + app
	for _, want := range []string{
		"innerTableName(row.UUID)",          // ad VIEW uuid'sinden
		"s.innerObjectUUIDOn(ctx, conn,",    // nesne uuid'si YEREL MV'den, açıkça
		"chInnerUUIDSettings",               // ayar O SORGUDA
		"injectTableUUID(ddl, inner, want)", // ad ve nesne uuid'si AYRI
		"SELECT toString(uuid) FROM system.tables",
		"s.verifyPeerZKPath(", // tarihçeyi belirleyen şey de ölçülür
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("eşten kurulum merdiveninde eksik: %s", want)
		}
	}
	// HAZIRLIK HİÇBİR ŞEY DEĞİŞTİRMEZ — bütün ayrımın sebebi bu.
	if strings.Contains(prep, ".Exec(") {
		t.Error("hazırlık DDL koşuyor — salt okuma olmalı, yoksa çağıran onu yıkıcı adımın önüne koyamaz")
	}
	// Fail-closed: iki kapı da HAZIRLIKTA, yani Exec'ten önce.
	for _, gate := range []string{"s.innerObjectUUIDOn(ctx, conn,", "EqualFold(peerRaw,"} {
		if !strings.Contains(prep, gate) {
			t.Errorf("%s kapısı hazırlıkta olmalı (fail-closed: Exec'e hiç gidilmez)", gate)
		}
	}
	// TEK GÖVDE sözleşmesi: eski çağıranlar için repairInnerOn ikisini SIRAYLA
	// çağırmayı sürdürür.
	if i, j := strings.Index(ladder, "s.prepareInnerFromPeer("), strings.Index(ladder, "s.applyInnerFromPeer("); i < 0 || j < 0 || i > j {
		t.Error("repairInnerOn hazırla → uygula sırasını korumalı")
	}
	// Yalnız count()>0 bakan doğrulama YALAN söyler: yanlış uuid'li bir tablo
	// da bir satırdır ve kaskad kırık kalır.
	if strings.Contains(fn, "SELECT count() FROM system.tables") {
		t.Error("doğrulama count()'a düşmüş — nesne uuid'si karşılaştırılmalı")
	}
	// ADIN uuid'si nesne uuid'si olarak GÖMÜLMEZ (v0.10.780'in hatası).
	if strings.Contains(fn, "injectTableUUID(ddl, inner, row.UUID)") {
		t.Error("ad uuid'si nesne uuid'si olarak kullanılmış — MV hedefini bulamaz")
	}
	// Eş bağlantısı ÇÖZÜMÜ dışarıda: bu gövdeler test edilebilir kalmalı.
	if strings.Contains(fn, "s.shardConn(") {
		t.Error("eş bağlantısı bu gövdede çözülüyor — dal yine test edilemez hâle gelir")
	}
}

func TestDanglingFromRowsNamesByViewUUID(t *testing.T) {
	v := "11111111-1111-1111-1111-111111111111"
	const obj = "f8b2b97f-662a-4df3-a654-3a13313e363f"
	mv := func(host string) mvTableRow {
		return mvTableRow{Host: host, Name: "service_summary_5m", UUID: v, Engine: "MaterializedView",
			CreateQuery: "CREATE MATERIALIZED VIEW coremetry.service_summary_5m UUID '" + v + "' TO INNER UUID '" + obj + "' (x Int) ENGINE = MergeTree ORDER BY x AS SELECT 1"}
	}
	rows := []mvTableRow{
		// h1: iç tablo VAR (adı view uuid'sinden, uuid KOLONU nesne uuid'si).
		mv("h1"), {Host: "h1", Name: ".inner_id." + v, UUID: obj, Engine: "ReplicatedAggregatingMergeTree"},
		mv("h2"), // iç tablo YOK → sarkan
		// h3: NESNE uuid'siyle adlandırılmış bir tablo — CH'de böyle bir ad
		// asla doğmaz; sarkanlığı DEĞİŞTİRMEZ (h3 yine sarkan).
		mv("h3"), {Host: "h3", Name: ".inner_id." + obj, UUID: obj, Engine: "ReplicatedAggregatingMergeTree"},
	}
	// v0.10.825 — eş AYNI shard'da olmalı; üç host da shard 1.
	got := danglingFromRows(rows, map[string]int{"h1": 1, "h2": 1, "h3": 1})
	if len(got) != 2 || got[0].Host != "h2" || got[1].Host != "h3" {
		t.Fatalf("beklenen h2 ve h3: %+v", got)
	}
	for _, d := range got {
		if d.UUID != v {
			t.Errorf("%s: sarkan satırın uuid'si ADIN uuid'si (view) olmalı: %q", d.Host, d.UUID)
		}
	}
	if got[0].PeerHost != "h1" {
		t.Errorf("eş replika adayı h1 olmalı: %+v", got[0])
	}
}
