package chstore

// mv_inner_name_test.go — v0.10.832: combined MV'nin gizli iç tablosunun ADI
// ile NESNE uuid'si AYRI şeylerdir.
//
// Sözleşme (ClickHouse kaynağı, v20.3 → master istisnasız):
//   - ADI: generateInnerTableName(view_id) → `.inner_id.` + VIEW'ın uuid'si.
//     CREATE/ATTACH/RENAME/EXCHANGE/REFRESH dallarının hepsi bu gövdeyi
//     VIEW'ın kimliğiyle çağırır.
//   - NESNE uuid'si: DDL'deki `TO INNER UUID '<x>'` — StorageID'nin üçüncü
//     alanı. CH `to_inner_uuid == view uuid` durumunu yasaklar ("cannot point
//     to itself"), yani iki uuid ASLA eşit olamaz.
//   - Varsayılan show_table_uuid_in_table_create_query_if_not_nil = 0 iken
//     create_table_query metni İKİ uuid'yi de siler; ayar 1 iken metin
//     `… UUID '<VIEW>' TO INNER UUID '<NESNE>' …` döner.
//
// Semptom (v0.10.780 → v0.10.831): ad DDL'deki `TO INNER UUID` değerinden
// kuruluyordu. O adla tablo HİÇBİR ZAMAN yoktur. Varsayılan ayarda regex hiç
// eşleşmediği için zararsız görünüyordu — DOĞRULUK BİZİM OLMAYAN BİR AYARA
// ASILIYDI. Ayar bir profilde/oturumda 1 ise: kapsama kartı her MV × her
// host'u `dangling`, artık kartı CANLI her iç tabloyu `oksuz` sınıflar; iki
// kartın düğmeleri de (DROP … SYNC + purgeGuard) canlı veriyi tarihçesiyle
// götürür ve audit bunu BAŞARI yazar.
//
// Bu dosya BİLEREK yalnız saf sınıflandırma gövdelerini ve ÇIPLAK ad
// sabitlerini kullanır (innerTableName gibi yardımcıları DEĞİL): sözleşme
// "hangi yardımcıyı çağırdın" değil, "hangi adı çözdün" üzerinedir.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	// inViewUUID — VIEW'ın uuid'si: iç tablonun ADI bundan doğar.
	inViewUUID = "11111111-aaaa-4bbb-8ccc-111111111111"
	// inObjUUID — iç tablonun KENDİ nesne uuid'si (MV'nin TO INNER UUID'si).
	// Ada ASLA girmez.
	inObjUUID = "22222222-aaaa-4bbb-8ccc-222222222222"
)

// inMVRow — ayar AÇIK biçimli combined MV satırı (iki uuid de metinde).
func inMVRow(host, name string) mvTableRow {
	return mvTableRow{Host: host, Name: name, UUID: inViewUUID, Engine: "MaterializedView",
		CreateQuery: "CREATE MATERIALIZED VIEW coremetry." + name + " UUID '" + inViewUUID +
			"' TO INNER UUID '" + inObjUUID + "' (`x` Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1"}
}

// inInnerRow — CANLI iç tablo: adı VIEW uuid'sinden, uuid KOLONU nesne
// uuid'si (gerçek system.tables satırı böyle gelir).
func inInnerRow(host, engine string) mvTableRow {
	return mvTableRow{Host: host, Name: ".inner_id." + inViewUUID, UUID: inObjUUID, Engine: engine}
}

// TestInnerNameFromViewUUIDNotObjectUUID — üç sınıflandırma gövdesi de ADI
// VIEW uuid'sinden çözmeli; DDL'de BAŞKA bir nesne uuid'si dursa bile.
func TestInnerNameFromViewUUIDNotObjectUUID(t *testing.T) {
	const (
		view    = "service_summary_5m_local"
		wantIn  = ".inner_id." + inViewUUID
		wrongIn = ".inner_id." + inObjUUID
	)
	rows := []mvTableRow{
		inMVRow("ch-01", view), inInnerRow("ch-01", "ReplicatedAggregatingMergeTree"),
		inMVRow("ch-02", view), // iç tablo YOK → gerçekten sarkan
	}
	shard := map[string]int{"ch-01": 1, "ch-02": 1}

	t.Run("danglingFromRows", func(t *testing.T) {
		got := danglingFromRows(rows, shard)
		if len(got) != 1 {
			t.Fatalf("CANLI iç tablosu olan host sarkan sayıldı — %d satır: %+v", len(got), got)
		}
		if got[0].Host != "ch-02" {
			t.Fatalf("sarkan host ch-02 olmalı: %+v", got[0])
		}
		// Hata metnindeki ad ile aynı uuid: `.inner_id.<VIEW uuid>`.
		if got[0].UUID != inViewUUID {
			t.Errorf("sarkan satırın uuid'si %q — ad VIEW uuid'sinden doğar (%q), nesne uuid'sinden değil", got[0].UUID, inViewUUID)
		}
		// Eş replika: ch-01'in iç tablosu Replicated ve aynı shard'da.
		if got[0].PeerHost != "ch-01" {
			t.Errorf("eş replika ch-01 olmalı (adı çözülemeyen eş, eş DEĞİLDİR): %q", got[0].PeerHost)
		}
	})

	t.Run("mvCoverageFromRows", func(t *testing.T) {
		got := mvCoverageFromRows(rows, []string{view}, []string{"ch-01", "ch-02"}, true, shard)
		byHost := map[string]MVHostState{}
		for _, s := range got {
			byHost[s.Host] = s
		}
		if st := byHost["ch-01"]; st.State != MVStateOK {
			t.Errorf("ch-01 CANLI iç tabloya sahip, durum %q (beklenen %q) — bu satırda 'Yeniden kur' AÇIK olurdu ve DROP … SYNC canlı tabloyu götürürdü", st.State, MVStateOK)
		}
		if st := byHost["ch-02"]; st.State != MVStateDangling {
			t.Errorf("ch-02 gerçekten sarkan, durum %q", st.State)
		}
		if st := byHost["ch-01"]; st.UUID != inViewUUID {
			t.Errorf("hücrenin uuid'si %q — ADIN uuid'si (VIEW) olmalı: %q", st.UUID, inViewUUID)
		}
	})

	t.Run("mvOwnersByHost", func(t *testing.T) {
		owners := mvOwnersByHost(rows)
		if owners["ch-01"][wantIn] != view {
			t.Errorf("sahip haritası %q anahtarını taşımalı, taşıdığı: %v", wantIn, owners["ch-01"])
		}
		if _, has := owners["ch-01"][wrongIn]; has {
			t.Errorf("harita var olmayan bir adı (%q) taşıyor — o ad NESNE uuid'sinden kurulmuş", wrongIn)
		}
	})
}

// TestTOTableMVIsNeverDanglingAtAnySetting — v0.10.832 inceleme (ÖNEMLİ):
// `TO <tablo>` biçimli MV'nin gizli iç tablosu YOKTUR, hiçbir profilde bulgu
// üretmemeli.
//
// Semptom: `reMVWithTO` regex'i `VIEW <ad> TO` dizilişini arıyordu; ayar 1
// iken CH araya `UUID '<view>'` token'ı koyuyor
// (`CREATE MATERIALIZED VIEW db.x UUID 'aaaa…' TO db.hedef …`) ve regex HİÇ
// eşleşmiyordu. `innerObjectUUIDFromDDL` de "" dönüyor (TO'lu MV'de TO INNER
// UUID yok) → `mvHasInnerTable` true → deponun TEK TO'lu MV'si HER host'ta
// `dangling`, "Yeniden kur" açık ve `verifyMVRebuild` aynı yüklemi
// uyguladığı için her denemede "iç tablo doğmadı". v0.10.832'nin ilk turu
// asılılığı combined MV'den TO'lu MV'ye TAŞIMIŞTI.
func TestTOTableMVIsNeverDanglingAtAnySetting(t *testing.T) {
	const view = "span_links_reverse_mv"
	forms := map[string]string{
		"ayar 0 (uuid'ler silinmiş)": "CREATE MATERIALIZED VIEW coremetry." + view +
			" TO coremetry.span_links_reverse (`x` Int) AS SELECT 1",
		"ayar 1 (araya UUID token'ı girer)": "CREATE MATERIALIZED VIEW coremetry." + view +
			" UUID '" + inViewUUID + "' TO coremetry.span_links_reverse (`x` Int) AS SELECT 1",
		"IF NOT EXISTS + ayar 1": "CREATE MATERIALIZED VIEW IF NOT EXISTS coremetry." + view +
			" UUID '" + inViewUUID + "' TO coremetry.span_links_reverse (`x` Int) AS SELECT 1",
		"ATTACH biçimi": "ATTACH MATERIALIZED VIEW coremetry." + view +
			" UUID '" + inViewUUID + "' TO coremetry.span_links_reverse (`x` Int) AS SELECT 1",
	}
	for name, cq := range forms {
		t.Run(name, func(t *testing.T) {
			rows := []mvTableRow{{Host: "ch-01", Name: view, UUID: inViewUUID,
				Engine: "MaterializedView", CreateQuery: cq}}
			if mvHasInnerTable(cq) {
				t.Errorf("TO'lu MV gizli iç tablosu VARMIŞ sayıldı: %s", cq)
			}
			if got := danglingFromRows(rows, map[string]int{"ch-01": 1}); len(got) != 0 {
				t.Errorf("sarkan listesine girdi: %+v", got)
			}
			got := mvCoverageFromRows(rows, []string{view}, []string{"ch-01"}, true, map[string]int{"ch-01": 1})
			if len(got) != 1 || got[0].State != MVStateOK {
				t.Errorf("kapsama durumu %q, beklenen %q — 'Yeniden kur' düğmesi açılırdı ve doğrulama her denemede düşerdi", got[0].State, MVStateOK)
			}
			if owners := mvOwnersByHost(rows); len(owners["ch-01"]) != 0 {
				t.Errorf("sahip haritasına girdi (gizli iç tablosu yok): %v", owners["ch-01"])
			}
		})
	}
}

// TestMVLeftoversLiveInnerIsNotOrphan — ayar AÇIK biçimli satırlarda CANLI iç
// tablo `oksuz` SINIFLANMAZ. Sınıflansaydı DropOrphanInner'ın üç kapısı da
// (taze ölçüm / distributedRefs / son replika) bu haritadan geçtiği için
// DROP koşar ve audit'e "başarılı" yazılırdı.
func TestMVLeftoversLiveInnerIsNotOrphan(t *testing.T) {
	storage := func(mv string) string { return mv + "_local" }
	rows := []mvTableRow{
		inMVRow("ch-01", "db_summary_5m_local"),
		inInnerRow("ch-01", "ReplicatedAggregatingMergeTree"),
	}
	got := mvLeftoversFromRows(rows, true, storage, nil)
	for _, l := range got {
		if l.Kind == MVLeftoverOrphan {
			t.Errorf("CANLI iç tablo öksüz sınıflandı (%s@%s) — sahibi db_summary_5m_local, DROP veriyi götürürdü", l.Inner, l.Host)
		}
	}
	if len(got) != 0 {
		t.Errorf("güncel `_local` bulgu üretmemeli: %+v", got)
	}
	// Gerçekten sahipsiz bir iç tablo HÂLÂ öksüzdür (tanı körelmesin).
	orphan := mvTableRow{Host: "ch-01", Name: ".inner_id.33333333-aaaa-4bbb-8ccc-333333333333",
		UUID: "44444444-aaaa-4bbb-8ccc-444444444444", Engine: "AggregatingMergeTree"}
	got = mvLeftoversFromRows(append(rows, orphan), true, storage, nil)
	if len(got) != 1 || got[0].Kind != MVLeftoverOrphan || got[0].Inner != orphan.Name {
		t.Errorf("sahipsiz iç tablo öksüz kalmalı: %+v", got)
	}
}

// TestInnerNameNeverBuiltFromObjectUUID — repo geneli KAYNAK pini: paket
// içinde hiçbir ad, DDL'den okunan nesne uuid'sinden kurulmaz.
//
// Gate kendi metnini ısırmasın: yalnız _test.go OLMAYAN dosyalar taranır
// (aranan kalıplar bu dosyada geçer).
func TestInnerNameNeverBuiltFromObjectUUID(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, line := range strings.Split(string(b), "\n") {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue // yorumlar sözleşmeyi ANLATIR, kurmaz
			}
			builds := strings.Contains(code, "innerTablePrefix +") || strings.Contains(code, "innerTableName(")
			fromDDL := strings.Contains(code, "innerObjectUUIDFromDDL") || strings.Contains(code, "reInnerObjectUUID")
			if builds && fromDDL {
				t.Errorf("%s: ad NESNE uuid'sinden kuruluyor — o adla tablo hiç yoktur: %s", f, code)
			}
		}
	}
}
