package chstore

// mv_target_uuid_test.go — v0.10.833: "ad doğru, NESNE uuid'si yanlış"
// saptaması.
//
// Semptom: MV gizli iç tablosunu ADLA değil `TO INNER UUID` ile çözer. Ad
// doğru ama tablonun kendi uuid'si farklıysa kapsama kartı `ok` (yeşil) der,
// MV hedefini bulamaz ve ingest "Target table … doesn't exist" demeye devam
// eder. v0.10.832 ÖNCESİ FE runbook'u operatöre iç tabloyu ADIN uuid'siyle
// kurdurtuyordu — bu şekli ürünün kendisi öğretmiş olabilir.
//
// Bu dosyanın çividiği sözleşmeler:
//   - BOŞ ASLA UYUŞMAZLIK DEĞİLDİR (iki uuid'den biri okunamadıysa karar
//     `unmeasured`, `mismatch` değil);
//   - probe HATASI kapsama sınıflarını düşürmez, hücreleri `unmeasured` yapar;
//   - kapsamanın verdiği `byname` kararı probe geçişinde EZİLMEZ;
//   - yıkıcı eylem kapısı ALLOWLIST'tir (yeni bir durum varsayılan olarak
//     reddedilir);
//   - ayar=1 ile okunan create_table_query metni sınıflandırmaya FİZİKSEL
//     OLARAK ulaşamaz (probe'un dönüş tipinde metin alanı yok).

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2"
)

// stripGoComments — kaynak pini yardımcısı (v0.10.833). Grep kapıları KENDİ
// gerekçe metinlerini ısırır: "ayar = 1" bir yorumda da geçer. Tarama yalnız
// KODU görmeli. Tırnak/backtick durumu izlenir ki bir string literalinin
// içindeki `//` yanlışlıkla yorum sanılmasın.
func stripGoComments(src string) string {
	var out strings.Builder
	inStr, inRaw, inRune, inLine, inBlock := false, false, false, false, false
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		// Rune literali (v0.10.833 inceleme, G): `'"'` izlenmezse tarayıcı
		// oradan itibaren her şeyi string sanar ve dosyanın yarısı kaybolur.
		case inRune:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				out.WriteByte(src[i])
			} else if c == '\'' {
				inRune = false
			}
		case inLine:
			if c == '\n' {
				inLine = false
				out.WriteByte(c)
			}
		case inBlock:
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				inBlock = false
				i++
			}
		case inRaw:
			out.WriteByte(c)
			if c == '`' {
				inRaw = false
			}
		case inStr:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(src) {
				i++
				out.WriteByte(src[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			inLine = true
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			inBlock = true
			i++
		default:
			out.WriteByte(c)
			switch c {
			case '"':
				inStr = true
			case '`':
				inRaw = true
			case '\'':
				inRune = true
			}
		}
	}
	return out.String()
}

const (
	tgtView  = "11111111-1111-1111-1111-111111111111" // view uuid → iç tablonun ADI
	tgtRight = "22222222-2222-2222-2222-222222222222" // MV'nin TO INNER UUID'si
	tgtWrong = "33333333-3333-3333-3333-333333333333" // adı doğru ama BAŞKA nesne
)

// probeOf — tek host/view için probe kısayolu.
func probeOf(host, view string, target string, shown bool) *mvTargetProbe {
	return &mvTargetProbe{Read: map[string]map[string]mvTargetRead{
		host: {view: {Target: innerObjectUUID(target), Shown: shown}},
	}}
}

// ── 1. dört değerli saf karar ─────────────────────────────────────────

func TestMVTargetVerdict(t *testing.T) {
	const host, view = "ch-01", "spanmetrics_hist_5m_local"
	cases := []struct {
		name       string
		state      string
		innerUUID  string
		probe      *mvTargetProbe
		want       string
		wantTarget string
		noteHas    string
	}{
		{
			name:  "iki uuid de okundu ve AYNI → ok",
			state: MVStateOK, innerUUID: tgtRight,
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetOK, wantTarget: tgtRight,
		},
		{
			name:  "iki uuid de okundu ve FARKLI → mismatch (BULGU)",
			state: MVStateOK, innerUUID: tgtWrong,
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetMismatch, wantTarget: tgtRight, noteHas: "hedef",
		},
		{
			name:  "büyük/küçük harf farkı uyuşmazlık DEĞİL",
			state: MVStateOK, innerUUID: strings.ToUpper(tgtRight),
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetOK, wantTarget: tgtRight,
		},
		{
			// BOŞ ASLA UYUŞMAZLIK DEĞİLDİR (i).
			name:  "token YOK ama ayar UYGULANDI → byname, mismatch DEĞİL",
			state: MVStateOK, innerUUID: tgtWrong,
			probe: probeOf(host, view, "", true),
			want:  MVTargetByName, noteHas: "Nil",
		},
		{
			// Sessiz kırpma: uzak (SECONDARY_QUERY) düğümde profil kısıtı
			// ayarı istisna ATMADAN kırpar — host BAZINDA ölçülemedi.
			name:  "hiç uuid token'ı yok → unmeasured (sessiz kırpma)",
			state: MVStateOK, innerUUID: tgtWrong,
			probe: probeOf(host, view, "", false),
			want:  MVTargetUnmeasured, noteHas: "kırp",
		},
		{
			// BOŞ ASLA UYUŞMAZLIK DEĞİLDİR (ii): envanter tarafı boş.
			name:  "iç tablonun kendi uuid'si okunamadı → unmeasured",
			state: MVStateOK, innerUUID: "",
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetUnmeasured, wantTarget: tgtRight, noteHas: "iç tablo",
		},
		{
			name:  "sıfır uuid de BOŞTUR → unmeasured",
			state: MVStateOK, innerUUID: zeroUUID,
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetUnmeasured, wantTarget: tgtRight,
		},
		{
			name:  "probe hatası → unmeasured (kapsama sınıfı DURUR)",
			state: MVStateOK, innerUUID: tgtWrong,
			probe: &mvTargetProbe{Err: "TIMEOUT_EXCEEDED"},
			want:  MVTargetUnmeasured, noteHas: "TIMEOUT_EXCEEDED",
		},
		{
			name:  "probe hiç koşmadı → unmeasured",
			state: MVStateOK, innerUUID: tgtWrong, probe: nil,
			want: MVTargetUnmeasured,
		},
		{
			name:  "host probe cevabında yok → unmeasured",
			state: MVStateOK, innerUUID: tgtWrong,
			probe: probeOf("ch-02", view, tgtRight, true),
			want:  MVTargetUnmeasured,
		},
		{
			name:  "MV bu host'ta yok → unmeasured, sebebi DURUMUN KENDİSİ",
			state: MVStateMissing, innerUUID: "",
			probe: probeOf("ch-02", view, tgtRight, true),
			want:  MVTargetUnmeasured, noteHas: "yok",
		},
		{
			name:  "sarkan: karşılaştırılacak iç tablo yok → unmeasured",
			state: MVStateDangling, innerUUID: "",
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetUnmeasured, wantTarget: tgtRight, noteHas: "iç tablo",
		},
		{
			// Düz iç tablo da yanlış uuid taşıyabilir: iki hastalık DİK.
			name:  "düz iç tablo + yanlış uuid → mismatch",
			state: MVStatePlain, innerUUID: tgtWrong,
			probe: probeOf(host, view, tgtRight, true),
			want:  MVTargetMismatch, wantTarget: tgtRight,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, gotTarget, note := mvTargetVerdict(c.state, c.innerUUID, host, view, c.probe)
			if got != c.want {
				t.Errorf("karar %q, beklenen %q (not: %s)", got, c.want, note)
			}
			if gotTarget != c.wantTarget {
				t.Errorf("hedef uuid %q, beklenen %q", gotTarget, c.wantTarget)
			}
			if got != MVTargetOK && note == "" {
				t.Error("ok DIŞINDAKİ her karar sebebini YAZMALI — sessiz bir rozet operatöre hiçbir şey söylemez")
			}
			if c.noteHas != "" && !strings.Contains(note, c.noteHas) {
				t.Errorf("not %q parçasını içermeli: %s", c.noteHas, note)
			}
		})
	}
}

// TestMVTargetVerdictNeverInventsMismatchFromEmpty — sözleşmenin kendisi,
// kombinatoryal: iki uuid'den EN AZ BİRİ boş/sıfırken karar ASLA mismatch
// olamaz. Tablo genişlediğinde bu kapı kapanmaz.
func TestMVTargetVerdictNeverInventsMismatchFromEmpty(t *testing.T) {
	const host, view = "ch-01", "db_summary_5m_local"
	empties := []string{"", zeroUUID}
	for _, inner := range append([]string{tgtWrong}, empties...) {
		for _, target := range append([]string{tgtRight}, empties...) {
			if validUUID(inner) && validUUID(target) {
				continue // ikisi de dolu: mismatch MEŞRU
			}
			for _, shown := range []bool{true, false} {
				got, _, note := mvTargetVerdict(MVStateOK, inner, host, view, probeOf(host, view, target, shown))
				if got == MVTargetMismatch {
					t.Errorf("inner=%q target=%q shown=%v → %q (boş bir taraf UYUŞMAZLIK DEĞİLDİR): %s", inner, target, shown, got, note)
				}
			}
		}
	}
}

// ── 2. metin ayrıştırma: iki token, iki soru ──────────────────────────

func TestMVTargetTokensFromDDL(t *testing.T) {
	const name = "coremetry.spanmetrics_hist_5m_local"
	cases := []struct {
		name       string
		ddl        string
		wantTarget innerObjectUUID
		wantShown  bool
	}{
		{
			name:       "ayar=1, combined MV: iki token da var",
			ddl:        "CREATE MATERIALIZED VIEW " + name + " UUID '" + tgtView + "' TO INNER UUID '" + tgtRight + "' (x Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1",
			wantTarget: innerObjectUUID(tgtRight), wantShown: true,
		},
		{
			name:      "ayar=1, to_inner_uuid Nil (eski sürüm / ATTACH): yalnız view uuid'si",
			ddl:       "CREATE MATERIALIZED VIEW " + name + " UUID '" + tgtView + "' (x Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1",
			wantShown: true,
		},
		{
			name: "ayar KIRPILMIŞ (ya da 0): hiç token yok",
			ddl:  "CREATE MATERIALIZED VIEW " + name + " (x Int) ENGINE = ReplicatedAggregatingMergeTree AS SELECT 1",
		},
		{
			name:       "ATTACH biçimi de ölçülür",
			ddl:        "ATTACH MATERIALIZED VIEW " + name + " UUID '" + tgtView + "' TO INNER UUID '" + tgtRight + "' (x Int) ENGINE = AggregatingMergeTree AS SELECT 1",
			wantTarget: innerObjectUUID(tgtRight), wantShown: true,
		},
		{
			name:      "TO'lu MV, ayar=1: hedef GERÇEK tablo, nesne uuid'si yok",
			ddl:       "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv UUID '" + tgtView + "' TO coremetry.span_links_reverse AS SELECT 1",
			wantShown: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := innerObjectUUIDFromDDL(c.ddl); got != c.wantTarget {
				t.Errorf("hedef uuid %q, beklenen %q", got, c.wantTarget)
			}
			if got := mvUUIDTokenShown(c.ddl); got != c.wantShown {
				t.Errorf("ayar uygulandı mı %v, beklenen %v", got, c.wantShown)
			}
		})
	}
}

// ── 3. hücrelere basma: byname EZİLMEZ, hiçbir hücre boş kalmaz ───────

func TestMVApplyTargetVerdict(t *testing.T) {
	const view = "spanmetrics_hist_5m_local"
	cells := []MVHostState{
		{Host: "ch-01", View: view, State: MVStateOK, InnerUUID: tgtRight},
		{Host: "ch-02", View: view, State: MVStateOK, InnerUUID: tgtWrong},
		{Host: "ch-03", View: view, State: MVStateMissing},
		// Kapsamanın KENDİ kararı (TO'lu MV): probe bunu EZMEMELİ.
		{Host: "ch-04", View: "span_links_reverse_mv", State: MVStateOK, Target: MVTargetByName, TargetNote: "gizli iç tablo yok"},
	}
	p := &mvTargetProbe{Read: map[string]map[string]mvTargetRead{
		"ch-01": {view: {Target: tgtRight, Shown: true}},
		"ch-02": {view: {Target: tgtRight, Shown: true}},
		"ch-04": {"span_links_reverse_mv": {Target: "", Shown: false}},
	}}
	mvApplyTargetVerdict(cells, p)
	want := []string{MVTargetOK, MVTargetMismatch, MVTargetUnmeasured, MVTargetByName}
	for i, w := range want {
		if cells[i].Target != w {
			t.Errorf("%s@%s: karar %q, beklenen %q", cells[i].View, cells[i].Host, cells[i].Target, w)
		}
	}
	if cells[3].TargetNote != "gizli iç tablo yok" {
		t.Errorf("kapsamanın kararı EZİLDİ: %q", cells[3].TargetNote)
	}
	if cells[1].TargetUUID != tgtRight || cells[1].InnerUUID != tgtWrong {
		t.Errorf("bulgu satırı İKİ uuid'yi de taşımalı (runbook onları yazar): %+v", cells[1])
	}
	for _, c := range cells {
		if c.Target == "" {
			t.Errorf("%s@%s kararsız kaldı — dört değerden biri ŞART", c.View, c.Host)
		}
	}
}

// TestMVCoverageDecidesByNameForMVsWithoutInnerTable — kapsamanın kendi kolu:
// gizli iç tablosu OLMAYAN MV'de hedef uuid sorusu GEÇERSİZDİR. Eskiden bu
// hücreler probe'a kalırdı ve TO'lu her kanonik MV kalıcı olarak
// "ölçülemedi" sayılıp yeşil rozeti sonsuza dek kapatırdı.
func TestMVCoverageDecidesByNameForMVsWithoutInnerTable(t *testing.T) {
	rows := []mvTableRow{
		{Host: "ch-01", Name: "span_links_reverse_mv", UUID: tgtView, Engine: "MaterializedView",
			CreateQuery: "CREATE MATERIALIZED VIEW coremetry.span_links_reverse_mv TO coremetry.span_links_reverse AS SELECT 1"},
	}
	got := mvCoverageFromRows(rows, []string{"span_links_reverse_mv"}, []string{"ch-01"}, true, map[string]int{"ch-01": 1})
	if len(got) != 1 || got[0].State != MVStateOK || got[0].Target != MVTargetByName {
		t.Fatalf("TO'lu MV: durum ok + hedef byname olmalı: %+v", got)
	}
}

// TestMVCoverageCarriesInnerObjectUUID — karşılaştırmanın envanter tarafı
// gerçekten TAŞINIYOR mu (eskiden `inner[host][name] = engine` ile atılıyordu).
func TestMVCoverageCarriesInnerObjectUUID(t *testing.T) {
	const view = "service_summary_5m_local"
	rows := []mvTableRow{
		combinedMVRow("ch-01", view),
		{Host: "ch-01", Name: innerTablePrefix + mvTestView, UUID: tgtWrong, Engine: "ReplicatedAggregatingMergeTree"},
	}
	got := mvCoverageFromRows(rows, []string{view}, []string{"ch-01"}, true, map[string]int{"ch-01": 1})
	if len(got) != 1 || got[0].State != MVStateOK {
		t.Fatalf("durum ok beklenirdi (varlık + motor doğru): %+v", got)
	}
	if got[0].InnerUUID != tgtWrong {
		t.Errorf("iç tablonun KENDİ uuid'si taşınmadı: %q", got[0].InnerUUID)
	}
}

// ── 4. yıkıcı eylem kapısı ALLOWLIST ─────────────────────────────────

// TestMVRebuildAllowlist — "Yeniden kur" yalnız BUGÜNKÜ üç durum için açık.
// Tablo, mv_coverage.go'daki MVState* sabitlerinin TAMAMINI kaynaktan
// tarayarak doğrular: yeni bir durum sabiti eklenip burada karar verilmezse
// test DÜŞER (eklenen durum sessizce yıkıcı eylem alamaz).
func TestMVRebuildAllowlist(t *testing.T) {
	yes, no := true, false
	// İKİ EKSEN: durum (allowlist) × hedef ÖLÇÜMÜ (veto). nil = ölçülmedi ve
	// veto DEĞİLDİR — yalnız KANIT kapıyı kapatır.
	want := map[string]map[string]bool{
		//                 ölçülmedi     çözülüyor      çözülmüyor
		MVStateOK:       {"nil": false, "true": false, "false": false}, // sağlıklı: DROP tarihçeyi bedava yakar
		MVStatePlain:    {"nil": true, "true": false, "false": true},
		MVStateDangling: {"nil": true, "true": false, "false": true}, // ← CANLI sarkan satırda düğme KAPANIR
		MVStateMissing:  {"nil": true, "true": false, "false": true},
	}
	probes := map[string]*bool{"nil": nil, "true": &yes, "false": &no}
	for state, byProbe := range want {
		for probe, allowed := range byProbe {
			if got := mvRebuildAllowed(state, probes[probe]); got != allowed {
				t.Errorf("durum %q + hedef %s: izin %v, beklenen %v", state, probe, got, allowed)
			}
		}
	}
	// Bilinmeyen / gelecekteki durum: VARSAYILAN RET (her üç ölçümde de).
	for _, s := range []string{"", "readonly", "detached", "unknown"} {
		for probe, p := range probes {
			if mvRebuildAllowed(s, p) {
				t.Errorf("%q bilinmeyen bir durum (hedef %s) — allowlist dışı kalmalı", s, probe)
			}
		}
	}
	// Kaynak taraması PAKET GENELİ (F-v): sabit başka bir dosyaya taşınırsa
	// tek dosyaya bakan tarama onu GÖRMEZDİ.
	re := regexp.MustCompile(`(?m)^\s*(MVState\w+)\s*=\s*"([^"]+)"`)
	found := 0
	for _, f := range packageGoFiles(t) {
		for _, m := range re.FindAllStringSubmatch(stripGoComments(readGoSource(t, f)), -1) {
			found++
			if _, ok := want[m[2]]; !ok {
				t.Errorf("%s (%q, %s) allowlist tablosunda KARAR ALMAMIŞ — yeni durum varsayılan olarak yıkıcı eylem ALMAMALI", m[1], m[2], f)
			}
		}
	}
	if found < 4 {
		t.Fatalf("MVState* sabitleri bulunamadı (%d) — tarama kalıbı bozulmuş", found)
	}
}

// packageGoFiles — paketin test OLMAYAN kaynakları. Kapılar tek DOSYA ADINA
// bağlıysa okumayı başka dosyaya taşımak hepsini yeşil geçer (v0.10.833 F-i).
func packageGoFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range all {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	if len(out) < 10 {
		t.Fatalf("paket taraması %d dosya buldu — glob bozulmuş", len(out))
	}
	return out
}

// TestRebuildMVOnHostUsesAllowlist — kapının GÖVDEDE gerçekten allowlist
// olduğu ve onarım çağrısından ÖNCE durduğu (saf çekirdek yeşil olup
// çağrıldığı yer pinlenmemişse düzeltme kendini iptal eder).
func TestRebuildMVOnHostUsesAllowlist(t *testing.T) {
	fn := funcBody(t, "mv_coverage.go", "func (s *Store) RebuildMVOnHost(")
	for _, want := range []string{
		"mvRebuildAllowed(row.State, row.TargetResolves)",
		"s.mvHardenCell(ctx, row, cluster)", // kararı ÖLÇ, varsayma
		"s.mvCoverageReport(ctx, false)",    // kapaklı SÜPÜRME atlanır (D)
	} {
		if !strings.Contains(fn, want) {
			t.Errorf("RebuildMVOnHost içinde eksik: %s", want)
		}
	}
	if strings.Contains(fn, "s.MVCoverage(ctx)") {
		t.Error("eylem yolu süpürmeyi koşmamalı — FE onarımdan sonra yeniden tarar, tıklama başına İKİ KEZ ödenir (v0.10.833 D)")
	}
	rebuild := strings.Index(fn, "s.rebuildMVOnConn(")
	for _, gate := range []string{"s.mvHardenCell(", "mvRebuildAllowed("} {
		if i := strings.Index(fn, gate); i < 0 || rebuild < 0 || i > rebuild {
			t.Errorf("%s onarım çağrısından ÖNCE olmalı", gate)
		}
	}
}

// ── 5. düğüm-yerel sertleştirme ──────────────────────────────────────

// TestMVTargetCheckNote — ÜÇ HÂL. Gerçek CH 24.8 ölçümü: metadata
// uyuşmazlığı hem ingest'i düşüren şekli (kod 60) hem MV'nin toplamaya
// DEVAM ettiği şekli (SELECT ve INSERT başarılı) üretiyor. "Uyuşmazlık =
// ingest düşüyor" varsayımı ölçülebilir şekilde yanlıştı.
func TestMVTargetCheckNote(t *testing.T) {
	cases := []struct {
		name     string
		state    string
		err      error
		wantRes  string // "true" | "false" | "nil"
		wantHas  string
		wantMiss string // notta GEÇMEMESİ gereken parça
	}{
		{
			name:  "kod 60 UNKNOWN_TABLE → ÇÖZEMİYOR (kesin, ingest düşüyor)",
			err:   &clickhouse.Exception{Code: 60, Message: "Table coremetry..inner_id.x doesn't exist"},
			state: MVStateOK, wantRes: "false", wantHas: "ÇÖZEMİYOR",
		},
		{
			name:  "sarmalanmış kod 60 da sayılır",
			err:   fmt.Errorf("doğrulama: %w", &clickhouse.Exception{Code: 60}),
			state: MVStateOK, wantRes: "false", wantHas: "ÇÖZEMİYOR",
		},
		{
			// ŞEKİL-2: MV BAŞKA ADLI var olan bir tabloya yazıyor ve TOPLUYOR.
			name: "view AÇILDI (ok) → ÇÖZÜYOR, ölü kopya dili",
			err:  nil, state: MVStateOK, wantRes: "true",
			wantHas: "ÖLÜ KOPYA", wantMiss: "ÇÖZEMİYOR",
		},
		{
			// A: sarkan görünen satır CANLI — yıkıcı düğme burada kapanır.
			name: "view AÇILDI (sarkan) → adı beklenen değil ama VERİ AKIYOR",
			err:  nil, state: MVStateDangling, wantRes: "true",
			wantHas: "veri akıyor", wantMiss: "ÇÖZEMİYOR",
		},
		{
			name:  "başka CH hatası → ölçülemedi, bulgu OLDUĞU GİBİ kalır",
			err:   &clickhouse.Exception{Code: 159, Message: "TIMEOUT_EXCEEDED"},
			state: MVStateOK, wantRes: "nil",
		},
		{
			name:  "ağ hatası → ölçülemedi, şekil VARSAYILMAZ",
			err:   errors.New("dial tcp: connection refused"),
			state: MVStateDangling, wantRes: "nil",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, note := mvTargetCheckNote(c.state, c.err)
			got := "nil"
			if res != nil {
				got = "false"
				if *res {
					got = "true"
				}
			}
			if got != c.wantRes {
				t.Errorf("çözülüyor mu %s, beklenen %s (%q)", got, c.wantRes, note)
			}
			if c.wantRes == "nil" {
				if note != "" {
					t.Errorf("ölçülemeyen bir okuma bulguyu DEĞİŞTİRMEMELİ: %q", note)
				}
				return
			}
			if !strings.Contains(note, c.wantHas) {
				t.Errorf("not %q içermeli: %q", c.wantHas, note)
			}
			if c.wantMiss != "" && strings.Contains(note, c.wantMiss) {
				t.Errorf("not %q İÇERMEMELİ (şekil-1 dili şekil-2'de yanlış): %q", c.wantMiss, note)
			}
		})
	}
}

// TestMVTargetNeedsCheck — sertleştirme NADİR satırlar içindir, ama
// SARKAN satırı da kapsar (A): hedef uuid okunabildiyse o satır CANLI
// olabilir ve kart ona İKİ danger düğme doğrultur.
func TestMVTargetNeedsCheck(t *testing.T) {
	cases := []struct {
		state, target, targetUUID string
		want                      bool
	}{
		{MVStateOK, MVTargetMismatch, tgtRight, true},
		{MVStatePlain, MVTargetMismatch, tgtRight, true},
		{MVStateDangling, MVTargetUnmeasured, tgtRight, true}, // ← A
		{MVStateDangling, MVTargetUnmeasured, "", false},      // ölçülecek şey yok
		{MVStateDangling, MVTargetUnmeasured, zeroUUID, false},
		{MVStateMissing, MVTargetUnmeasured, tgtRight, false}, // view YOK
		{MVStateOK, MVTargetOK, tgtRight, false},              // sağlıklı: bedava sorgu
		{MVStateOK, MVTargetByName, "", false},
		{MVStateOK, MVTargetUnmeasured, tgtRight, false}, // ok + ölçülemedi: metadata yeter
	}
	for _, c := range cases {
		if got := mvTargetNeedsCheck(c.state, c.target, c.targetUUID); got != c.want {
			t.Errorf("(%s, %s, %q) → %v, beklenen %v", c.state, c.target, c.targetUUID, got, c.want)
		}
	}
}

// TestMVTargetCheckPlan — KAPAK (D). Ölçüldü: kapaksız süpürme 6 host × 21
// MV = 126 hücrede en kötü 21 dk sürüyordu ve bedel üç yerde ödeniyordu.
func TestMVTargetCheckPlan(t *testing.T) {
	// 6 host × 4 MV, hepsi aday.
	var cells []MVHostState
	for v := 0; v < 4; v++ {
		for h := 1; h <= 6; h++ {
			cells = append(cells, MVHostState{
				Host: fmt.Sprintf("ch-%02d", h), View: fmt.Sprintf("mv_%d", v),
				State: MVStateOK, Target: MVTargetMismatch, TargetUUID: tgtRight,
				TargetNote: "metadata kanıtı",
			})
		}
	}
	run, capped := mvTargetCheckPlan(cells)
	if len(run) > mvTargetCheckTotal {
		t.Errorf("toplam kapak aşıldı: %d > %d", len(run), mvTargetCheckTotal)
	}
	if len(run)+len(capped) != len(cells) {
		t.Errorf("her aday ya ölçülür ya kapağa takılır: %d + %d ≠ %d", len(run), len(capped), len(cells))
	}
	perHost := map[string]int{}
	for _, i := range run {
		perHost[cells[i].Host]++
	}
	for h, n := range perHost {
		if n > mvTargetCheckPerHost {
			t.Errorf("%s: host başına %d satır ölçüldü (kapak %d)", h, n, mvTargetCheckPerHost)
		}
	}
	// Kapağa takılan satır SESSİZ kalmaz ve metadata kanıtını KORUR.
	for _, i := range capped {
		n := mvTargetCappedNote(cells[i].TargetNote)
		if !strings.Contains(n, "metadata kanıtı") || !strings.Contains(n, "KOŞULMADI") {
			t.Errorf("kapak notu kanıtı korumalı ve doğrulanmadığını söylemeli: %q", n)
		}
	}
	// Aday OLMAYAN satırlar plana hiç girmez.
	none, noneCapped := mvTargetCheckPlan([]MVHostState{
		{Host: "ch-01", View: "a_mv", State: MVStateOK, Target: MVTargetOK},
		{Host: "ch-01", View: "b_mv", State: MVStateOK, Target: MVTargetByName},
	})
	if len(none) != 0 || len(noneCapped) != 0 {
		t.Errorf("sağlıklı satırlar plana girmemeli: %v %v", none, noneCapped)
	}
}

// TestMVCheckTargetResolutionRunsOnlyPlannedRows — süpürme plana uyar ve
// kararı DEĞİŞTİRMEZ, yalnız TargetResolves'u doldurur.
func TestMVCheckTargetResolutionRunsOnlyPlannedRows(t *testing.T) {
	conn := &scriptConn{name: "yerel"}
	s := &Store{conn: conn}
	cells := []MVHostState{
		{Host: "ch-01", View: "a_mv", State: MVStateOK, Target: MVTargetOK},
		{Host: "ch-01", View: "b_mv", State: MVStateOK, Target: MVTargetByName},
		{Host: "ch-01", View: "c_mv", State: MVStateMissing, Target: MVTargetUnmeasured},
	}
	s.mvCheckTargetResolution(context.Background(), cells, "")
	if len(conn.execs) != 0 {
		t.Errorf("aday olmayan satırlar için sorgu koşmamalı: %v", conn.execs)
	}
	mism := []MVHostState{
		{Host: "ch-01", View: "d_mv", State: MVStateOK, Target: MVTargetMismatch, TargetUUID: tgtRight, TargetNote: "ilk bulgu"},
		// AYNI host: kapak yüzünden ölçülmez ama notu kanıtı korur.
		{Host: "ch-01", View: "e_mv", State: MVStateDangling, Target: MVTargetUnmeasured, TargetUUID: tgtRight, TargetNote: "ikinci bulgu"},
	}
	s.mvCheckTargetResolution(context.Background(), mism, "")
	if len(conn.execs) != 1 || !strings.Contains(conn.execs[0], "`d_mv`") || !strings.Contains(conn.execs[0], "LIMIT 0") {
		t.Fatalf("host başına TEK düğüm-yerel LIMIT 0 okuması bekleniyordu: %v", conn.execs)
	}
	if mism[0].Target != MVTargetMismatch || mism[1].State != MVStateDangling {
		t.Error("sertleştirme Target'ı da State'i de DEĞİŞTİRMEZ — dik alanı doldurur")
	}
	if mism[0].TargetResolves == nil || !*mism[0].TargetResolves {
		t.Errorf("sahte bağlantı hata vermedi → hedef ÇÖZÜLÜYOR olarak işaretlenmeliydi: %+v", mism[0])
	}
	if !strings.Contains(mism[1].TargetNote, "ikinci bulgu") || !strings.Contains(mism[1].TargetNote, "KOŞULMADI") {
		t.Errorf("kapağa takılan satır kanıtını korumalı: %q", mism[1].TargetNote)
	}
}

// ── 6. kalıntı kapısı (G) ────────────────────────────────────────────

func TestMVLeftoverStorageGate(t *testing.T) {
	const storage = "db_summary_5m_local"
	yes, no := true, false
	cases := []struct {
		name     string
		state    string
		target   string
		resolves *bool
		wantHas  string // "" = kapı geçer
	}{
		{name: "sağlıklı + hedef doğru → geçer", state: MVStateOK, target: MVTargetOK},
		{name: "sağlıklı + hedef ADLA çözülür → geçer", state: MVStateOK, target: MVTargetByName},
		{name: "sağlıklı + hedef ölçülemedi → geçer (ölçülemedi ≠ bozuk)", state: MVStateOK, target: MVTargetUnmeasured},
		{name: "sarkan → 'Yeniden kur'a yönlendir", state: MVStateDangling, target: MVTargetUnmeasured, wantHas: "Yeniden kur"},
		{name: "eksik → 'Yeniden kur'a yönlendir", state: MVStateMissing, target: MVTargetUnmeasured, wantHas: "Yeniden kur"},
		{name: "kapsamada satır yok → reddet", state: "", target: "", wantHas: "Yeniden kur"},
		{
			// Bu satırda "Yeniden kur" KAPALI (durum ok) — var olmayan bir
			// düğmeyi işaret etme.
			name:  "uyuşmazlık + ÇÖZEMİYOR → runbook'a yönlendir, düğmeye DEĞİL",
			state: MVStateOK, target: MVTargetMismatch, resolves: &no, wantHas: "runbook",
		},
		{
			// ÖLÇÜLDÜ (gerçek CH 24.8): şekil-2'de MV TOPLUYOR ve kalıntı
			// temizliği MEŞRU. Eski kapı bunu yanlış gerekçeyle reddediyordu.
			name:  "uyuşmazlık ama hedef ÇÖZÜLÜYOR → GEÇER (MV topluyor)",
			state: MVStateOK, target: MVTargetMismatch, resolves: &yes,
		},
		{
			// Sertleştirme koşmadıysa şekil VARSAYILMAZ: v0.10.832 davranışı korunur.
			name:  "uyuşmazlık, ölçülemedi → GEÇER (bilinmeyen yeni engel üretmez)",
			state: MVStateOK, target: MVTargetMismatch, resolves: nil,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := mvLeftoverStorageGate(storage, c.state, c.target, c.resolves)
			if c.wantHas == "" {
				if got != "" {
					t.Errorf("kapı geçmeliydi: %q", got)
				}
				return
			}
			if !strings.Contains(got, c.wantHas) {
				t.Errorf("ret metni %q içermeli: %q", c.wantHas, got)
			}
			if c.target == MVTargetMismatch && strings.Contains(got, "Yeniden kur") {
				t.Errorf("hedef uyuşmazlığında 'Yeniden kur' DENMEMELİ (o düğme bu satırda kapalı): %q", got)
			}
		})
	}
}

// ── 7. sızdırma yasağı + sorgu sözleşmesi ────────────────────────────

// TestMVTargetProbeCarriesNoDDLText — YAPISAL kalkan: probe'un dönüş tipi
// create_table_query metni taşımaz. Bir string alanı eklemek (ör. hata
// ayıklama için) ayar=1 metnini sınıflandırmaya ulaşılabilir kılardı ve
// v0.10.832'nin düzelttiği hata geri gelirdi.
func TestMVTargetProbeCarriesNoDDLText(t *testing.T) {
	rt := reflect.TypeOf(mvTargetRead{})
	want := map[string]string{"Target": "chstore.innerObjectUUID", "Shown": "bool"}
	if rt.NumField() != len(want) {
		t.Fatalf("mvTargetRead alan sayısı %d, beklenen %d — yeni alan metin taşıyor olabilir", rt.NumField(), len(want))
	}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if w, ok := want[f.Name]; !ok || f.Type.String() != w {
			t.Errorf("beklenmeyen alan %s %s — ölçüm tipi METİN TAŞIMAZ", f.Name, f.Type)
		}
	}
	// Probe zarfı da TAM alan kümesiyle çivili (v0.10.833 inceleme, G): yalnız
	// `reflect.String` KIND'ını reddetmek []byte, fmt.Stringer ya da bir
	// yapıya gömülü metni kaçırırdı.
	pt := reflect.TypeOf(mvTargetProbe{})
	wantProbe := map[string]string{
		"Err":     "string",
		"Read":    "map[string]map[string]chstore.mvTargetRead",
		"Targets": "chstore.MVTargetSet",
	}
	if pt.NumField() != len(wantProbe) {
		t.Fatalf("mvTargetProbe alan sayısı %d, beklenen %d — yeni alan DDL taşıyor olabilir", pt.NumField(), len(wantProbe))
	}
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		if w, ok := wantProbe[f.Name]; !ok || f.Type.String() != w {
			t.Errorf("beklenmeyen alan mvTargetProbe.%s %s — zarf DDL METNİ TAŞIMAZ", f.Name, f.Type)
		}
	}
	// Hedef kümesi de: yalnız bayrak + uuid haritası.
	st := reflect.TypeOf(MVTargetSet{})
	wantSet := map[string]string{"Measured": "bool", "uuids": "map[chstore.innerObjectUUID]bool"}
	if st.NumField() != len(wantSet) {
		t.Fatalf("MVTargetSet alan sayısı %d, beklenen %d", st.NumField(), len(wantSet))
	}
	for i := 0; i < st.NumField(); i++ {
		f := st.Field(i)
		if w, ok := wantSet[f.Name]; !ok || f.Type.String() != w {
			t.Errorf("beklenmeyen alan MVTargetSet.%s %s", f.Name, f.Type)
		}
	}
}

// TestMVTargetProbeQueryContract — okuma KÜME GENELİ TEK sorgu, yalnız MV
// satırları, db BAĞLI, metin KIRPILMAMIŞ, ayar AÇIK ve tavan envanterinkinden
// düşük DEĞİL.
func TestMVTargetProbeQueryContract(t *testing.T) {
	src := readGoSource(t, "mv_target_uuid.go")
	for _, want := range []string{
		"clusterAllReplicas('%s', system.tables)",
		"engine = 'MaterializedView'",
		"WHERE database = ?",
		"chMVTargetProbeSettings",
		"show_table_uuid_in_table_create_query_if_not_nil = 1",
		"max_execution_time = 15",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("eksik: %s", want)
		}
	}
	// Olumsuz kapılar YALNIZ koda bakar: gerekçe yorumları sabitin adını ve
	// yasak kalıpları anmak ZORUNDA ("gate kendi metnini ısırır", v0.10.833).
	code := stripGoComments(src)
	if strings.Contains(code, "substring(create_table_query") {
		t.Error("create_table_query KIRPILMAMALI — ayar=1'de metin uzar, `TO INNER UUID` pencerenin dışına düşer")
	}
	if strings.Contains(code, "database = currentDatabase()") {
		t.Error("clusterAllReplicas sorgusunda currentDatabase() süzgeci: uzak düğümde başka DB'ye çözülür — bağlı parametre kullan")
	}
	// Kendi tavanını chInnerUUIDSettings'ten AYIRMIŞ olmalı: o sabit 10 sn
	// taşır ve tek düğüme giden bir okuma içindir.
	if strings.Contains(code, "chInnerUUIDSettings") {
		t.Error("küme geneline yayılan okuma AYRI bir ayar sabiti kullanmalı (chInnerUUIDSettings tavanı 10 sn)")
	}
	// SINIFLANDIRMA YAZMAZ: bu dosya State'i OKUYABİLİR (hedef kararı ona
	// bağlı) ama ATAYAMAZ. Ayar=1 ile okunan metnin kapsama durumunu
	// belirlediği an v0.10.832'nin hatası geri gelir.
	//
	// Karar METİNLE değil AST ile verilir (v0.10.833 inceleme, F-iii):
	// `c.State, c.Target = …` gibi ÇOKLU atamayı regex görmüyordu.
	if f := assignedSelectors(t, "mv_target_uuid.go")["State"]; f {
		t.Error("probe dosyası State ATAMAZ — ayar=1 metni sınıflandırmayı belirleyemez")
	}
	// Ölçüm sonucu DİK alana yazılır; bunu gerçekten yaptığını da çivile
	// (yoksa yukarıdaki yasak boş bir sözleşmeye dönüşür).
	if f := assignedSelectors(t, "mv_target_uuid.go")["TargetResolves"]; !f {
		t.Error("düğüm-yerel ölçüm TargetResolves alanına yazmalı")
	}
}

// assignedSelectors — bir dosyada ATAMANIN SOL TARAFINDA geçen alan adları
// (AST; çoklu atama dahil). Metin taraması `a, b = …` biçimini kaçırıyordu.
func assignedSelectors(t *testing.T, file string) map[string]bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, lhs := range as.Lhs {
			if sel, ok := lhs.(*ast.SelectorExpr); ok {
				out[sel.Sel.Name] = true
			}
		}
		return true
	})
	return out
}

// settingConstNames — ayarı `want` (0/1) yapan SABİT adları, PAKET GENELİ.
// Yazım normalize edilir: `= 1` ile `=1` aynı ayardır (v0.10.833 F-ii).
func settingConstNames(t *testing.T, want int) []string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s*=\s*"[^"]*` +
		regexp.QuoteMeta(uuidSettingName) + fmt.Sprintf(`\s*=\s*%d`, want))
	var out []string
	for _, f := range packageGoFiles(t) {
		for _, m := range re.FindAllStringSubmatch(stripGoComments(readGoSource(t, f)), -1) {
			out = append(out, m[1])
		}
	}
	return out
}

const uuidSettingName = "show_table_uuid_in_table_create_query_if_not_nil"

// TestClassificationTextIsPinnedPackageWide — v0.10.833 inceleme (F): kapılar
// ATLATILABİLİYORDU. (i) pinler DOSYA ADINA bağlıydı — okumayı başka dosyaya
// taşımak hepsini yeşil geçerdi; (ii) `if_not_nil=1` (boşluksuz) hem beyaz
// listeyi hem sabit-adı taramasını atlatıyordu; (iv) sayı eşitliği hem
// kolon adını yazmayan bir okumayı (SHOW CREATE) kaçırıyor hem pinli bir
// sorguda ikinci kullanımda yanlış KIRMIZI veriyordu.
//
// Sözleşme (kapsam SINIFLANDIRMAYA KATILAN dosyalar): MV'nin gizli iç
// tablosu hakkında karar veren gövdeleri (mvHasInnerTable /
// innerObjectUUIDFromDDL / mvUUIDTokenShown) ÇAĞIRAN her dosyada, DDL metni
// okuyan HER sorgu ayarı AÇIKÇA sabitler — 0 (sınıflandırma) ya da 1 (beyaz
// listedeki nesne-uuid okuması). Profile bırakılmış tek bir okuma bile
// v0.10.832'nin hatasını geri getirir.
func TestClassificationTextIsPinnedPackageWide(t *testing.T) {
	// Ayarı bilerek 1 yapan dosyalar. Genişletmek BİLİNÇLİ bir karardır.
	allowed := map[string]string{
		"dangling_mv_admin.go": "nesne uuid'si okuması (onarım anı)",
		"mv_target_uuid.go":    "hedef uuid probe'u (v0.10.833)",
	}
	pin0, pin1 := uuidSettingName+"=0", uuidSettingName+"=1"
	// Pin, ayarı taşıyan SABİT ADIYLA da verilmiş olabilir (sorgu metni
	// birleştirme ile kuruluyor) — ad da geçerli bir pindir.
	pins := []string{pin0, pin1}
	pins = append(pins, squashAll(settingConstNames(t, 0))...)
	pins = append(pins, squashAll(settingConstNames(t, 1))...)

	classifiers := []string{"mvHasInnerTable(", "innerObjectUUIDFromDDL(", "mvUUIDTokenShown("}
	seen, participating := map[string]bool{}, 0
	for _, file := range packageGoFiles(t) {
		code := squashSpace(stripGoComments(readGoSource(t, file)))
		if strings.Contains(code, pin1) {
			seen[file] = true
			if _, ok := allowed[file]; !ok {
				t.Errorf("%s ayarı 1 yapıyor — bu metin sınıflandırmaya sızabilir; kapıyı BİLİNÇLİ genişlet", file)
			}
		}
		joins := false
		for _, c := range classifiers {
			if strings.Contains(code, squashSpace(c)) {
				joins = true
				break
			}
		}
		if !joins {
			continue // bu dosya MV iç tablosu hakkında karar VERMİYOR
		}
		participating++
		// Her DDL okuması pinli mi: kolon adı VEYA `SHOW CREATE`, ve pini
		// okumadan sonraki pencerede ara (SETTINGS sorgunun SONUNDA gelir,
		// aynı sorgudaki ikinci kullanım da aynı pini görür).
		for _, needle := range []string{"create_table_query", "SHOWCREATE"} {
			for i := 0; ; {
				j := strings.Index(code[i:], needle)
				if j < 0 {
					break
				}
				at := i + j
				i = at + len(needle)
				end := at + 600
				if end > len(code) {
					end = len(code)
				}
				win := code[at:end]
				pinned := false
				for _, p := range pins {
					if strings.Contains(win, p) {
						pinned = true
						break
					}
				}
				if !pinned {
					t.Errorf("%s: %q okuması (ofset %d) ayarı SABİTLEMİYOR — sınıflandırmaya katılan bir dosyada profile bırakılmış DDL metni v0.10.832'nin hatasını geri getirir", file, needle, at)
				}
			}
		}
	}
	if participating < 2 {
		t.Fatalf("sınıflandırmaya katılan %d dosya bulundu — gövde adları değişmiş, kapı KÖR", participating)
	}
	for f, why := range allowed {
		if !seen[f] {
			t.Errorf("%s artık ayarı 1 yapmıyor (%s) — nesne uuid'si varsayılan profilde metinde HİÇ görünmez, okuma sessizce boş dönüyor olabilir", f, why)
		}
	}
}

func squashAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, squashSpace(s))
	}
	return out
}

// squashSpace — tüm boşlukları söker. `= 1` ile `=1` aynı ayardır; kapı
// yazımı normalize etmezse ikinci biçim onu atlatır (v0.10.833 F-ii).
func squashSpace(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
