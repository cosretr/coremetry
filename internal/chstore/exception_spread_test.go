package chstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// v0.10.949 — "aynı exception aynı anda farklı servislerde" istisnasının
// saf çekirdeği. Operatör 2026-09-26: tek servisten gelen 5'in altındaki
// exception'lar gizlenebilir; aynı anda farklı servislerden geliyorsa görünür.

func TestExceptionSpreadKey(t *testing.T) {
	k1, ok1 := ExceptionSpreadKey("java.net.SocketTimeoutException", "Read timed out after 3000 ms on order 12345")
	k2, ok2 := ExceptionSpreadKey("java.net.SocketTimeoutException", "Read timed out after 1500 ms on order 7")
	if !ok1 || !ok2 || k1 != k2 {
		t.Fatalf("yalnız kimlik/sayı farkı olan mesajlar aynı anahtara inmeli: %q/%v vs %q/%v", k1, ok1, k2, ok2)
	}
	k3, ok3 := ExceptionSpreadKey("java.io.IOException", "Read timed out after 3000 ms on order 12345")
	if !ok3 || k3 == k1 {
		t.Fatalf("farklı tür ayrı anahtar olmalı: %q", k3)
	}
	for _, msg := range []string{"", "   ", "\t\n", "12345", "550e8400-e29b-41d4-a716-446655440000", "0xdeadbeef 42"} {
		if k, ok := ExceptionSpreadKey("NullPointerException", msg); ok {
			t.Errorf("mesaj %q anahtar üretmemeli (tür tek başına fazla genel), %q", msg, k)
		}
	}
	if _, ok := ExceptionSpreadKey("503", "Service Unavailable: upstream connect error"); !ok {
		t.Error("mesajlı bir 503 geçerli bir anahtar")
	}
	// Uzun mesaj: ham 512 baytlık kırpma dinamik değerin boyuna göre kayar;
	// normalize edilmiş önek yine eşleşmeli.
	tail := strings.Repeat("at com.acme.Handler.process step failed ", 20)
	ka, oka := ExceptionSpreadKey("X", "order 7 "+tail)
	kb, okb := ExceptionSpreadKey("X", "order 1234567 "+tail)
	if !oka || !okb || ka != kb {
		t.Fatalf("uzun mesajda kırpma kayması anahtarı bölmemeli")
	}

	// v0.10.949 — JDK yardımcı-NPE: tırnak içi kod kimliğidir. Parmak izi
	// normalizasyonu (tırnak maskeli) ikisini de "Cannot invoke #q because
	// #q is null"a indiriyordu → ilgisiz NPE'ler tek anahtar.
	const npe = "java.lang.NullPointerException"
	npeA := `Cannot invoke "String.length()" because "<local1>" is null`
	npeB := `Cannot invoke "com.acme.Order.getId()" because "order" is null`
	npeC := `Cannot read field "value" because "s" is null`
	kA, okA := ExceptionSpreadKey(npe, npeA)
	kB, okB := ExceptionSpreadKey(npe, npeB)
	kC, okC := ExceptionSpreadKey(npe, npeC)
	if !okA || !okB || !okC {
		t.Fatalf("yardımcı-NPE mesajları anahtar üretmeli: %v %v %v", okA, okB, okC)
	}
	if kA == kB {
		t.Errorf("farklı metot/yerel değişkenli NPE'ler ayrı anahtar olmalı: %q", kA)
	}
	if kC == kA || kC == kB {
		t.Errorf("'Cannot read field' şekli 'Cannot invoke' şeklinden ayrı olmalı: %q", kC)
	}
	if kA2, _ := ExceptionSpreadKey(npe, npeA); kA2 != kA {
		t.Errorf("aynı NPE metni aynı anahtar: %q vs %q", kA2, kA)
	}
	// Ortak kütüphanedeki aynı NPE farklı servislerde yine TEK anahtar
	// (istisnanın amacı); tırnak içindeki rakamlar yine maskelenir.
	shared := `Cannot invoke "com.acme.Client.call()" because "this.pool" is null`
	kS1, _ := ExceptionSpreadKey(npe, shared)
	kS2, _ := ExceptionSpreadKey(npe, shared)
	if kS1 != kS2 {
		t.Errorf("aynı ortak-kütüphane NPE'si tek anahtar olmalı")
	}
	kL1, _ := ExceptionSpreadKey(npe, `Cannot invoke "String.length()" because "<local1>" is null`)
	kL2, _ := ExceptionSpreadKey(npe, `Cannot invoke "String.length()" because "<local7>" is null`)
	if kL1 != kL2 {
		t.Errorf("tırnak içi rakam maskesi korunmalı: %q vs %q", kL1, kL2)
	}
	// Parmak izi normalizasyonu DEĞİŞMEDİ (saklanan parmak izleri).
	if got := normalizeMessage(npeA); got != "Cannot invoke #q because #q is null" {
		t.Errorf("normalizeMessage değişti: %q", got)
	}
}

const min1 = int64(time.Minute)

func spreadRow(fp, svc, msg string, firstMin, lastMin int64, occ uint64) ExceptionSpreadRow {
	return ExceptionSpreadRow{
		Fingerprint: fp, Type: "java.sql.SQLTimeoutException", Message: msg, Service: svc,
		FirstSeen: firstMin * min1, LastSeen: lastMin * min1, Occurrences: occ,
	}
}

func TestComputeExceptionSpread(t *testing.T) {
	const msg = "ORA-01013: user requested cancel of current operation"
	slack := 10 * time.Minute
	cases := []struct {
		name string
		rows []ExceptionSpreadRow
		want map[string]int // fp → Services; yoksa haritada olmamalı
	}{
		{
			name: "iki serviste örtüşen → ikisi de 2",
			rows: []ExceptionSpreadRow{spreadRow("a", "svc-a", msg, 0, 5, 2), spreadRow("b", "svc-b", msg, 3, 4, 1)},
			want: map[string]int{"a": 2, "b": 2},
		},
		{
			name: "S'den fazla ayrık → hiçbiri",
			rows: []ExceptionSpreadRow{spreadRow("a", "svc-a", msg, 0, 5, 2), spreadRow("b", "svc-b", msg, 16, 20, 1)},
			want: map[string]int{},
		},
		{
			name: "boşluk tam S → örtüşür (kapsayıcı)",
			rows: []ExceptionSpreadRow{spreadRow("a", "svc-a", msg, 0, 5, 2), spreadRow("b", "svc-b", msg, 15, 20, 1)},
			want: map[string]int{"a": 2, "b": 2},
		},
		{
			name: "aynı servis iki parmak izi → yayılım yok",
			rows: []ExceptionSpreadRow{spreadRow("a", "svc-a", msg, 0, 5, 2), spreadRow("a2", "svc-a", msg, 1, 4, 1)},
			want: map[string]int{},
		},
		{
			name: "A–B–C zinciri, A ile C ayrık → A=2 B=3 C=2 (geçişli değil)",
			rows: []ExceptionSpreadRow{
				spreadRow("a", "svc-a", msg, 0, 5, 1),
				spreadRow("b", "svc-b", msg, 10, 30, 1),
				spreadRow("c", "svc-c", msg, 35, 40, 1),
			},
			want: map[string]int{"a": 2, "b": 3, "c": 2},
		},
		{
			name: "farklı anahtarlar asla birleşmez",
			rows: []ExceptionSpreadRow{
				spreadRow("a", "svc-a", msg, 0, 5, 1),
				spreadRow("b", "svc-b", "Connection reset by peer", 0, 5, 1),
			},
			want: map[string]int{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeExceptionSpread(tc.rows, slack)
			if len(got) != len(tc.want) {
				t.Fatalf("haritada %d giriş, want %d: %+v", len(got), len(tc.want), got)
			}
			for fp, n := range tc.want {
				if got[fp].Services != n {
					t.Errorf("%s: Services=%d, want %d", fp, got[fp].Services, n)
				}
			}
		})
	}

	t.Run("ortaklar alfabetik, en çok 5; kendi servisi yok", func(t *testing.T) {
		var rows []ExceptionSpreadRow
		for i := 7; i >= 0; i-- {
			rows = append(rows, spreadRow(fmt.Sprintf("fp%d", i), fmt.Sprintf("svc-%d", i), msg, 0, 5, 1))
		}
		info := ComputeExceptionSpread(rows, slack)["fp0"]
		if info.Services != 8 {
			t.Fatalf("Services=%d, want 8", info.Services)
		}
		want := []string{"svc-1", "svc-2", "svc-3", "svc-4", "svc-5"}
		if !reflect.DeepEqual(info.Partners, want) {
			t.Fatalf("Partners=%v, want %v", info.Partners, want)
		}
		if info.Occurrences != 1 || info.LastSeen != 5*min1 {
			t.Fatalf("Occurrences/LastSeen satırdan gelmeli: %+v", info)
		}
	})

	// v0.10.949 — filo-genel anahtar freni: aynı şablon mesaj 7 günde ≥24
	// ayrı pencerede başlamışsa eşzamanlılık tesadüftür → istisna yok. Gerçek
	// eşzamanlı arıza (çok servis, tek pencere) istisnasını korur.
	t.Run("filo-genel anahtar: 30 servis, 30 ayrı pencere → boş", func(t *testing.T) {
		var rows []ExceptionSpreadRow
		for i := 0; i < 30; i++ {
			// 20 dk arayla başlar, 10 dk sürer: komşular S=10 dk payıyla
			// ikili örtüşür (fren olmasa her satır Services ≥ 2 alırdı).
			first := int64(i * 20)
			rows = append(rows, spreadRow(fmt.Sprintf("fp%02d", i), fmt.Sprintf("svc-%02d", i),
				"Index # out of bounds for length #", first, first+10, 1))
		}
		if got := ComputeExceptionSpread(rows, slack); len(got) != 0 {
			t.Fatalf("filo-genel anahtar istisna üretmemeli, got %d giriş", len(got))
		}
		if _, n := computeExceptionSpread(rows, slack); n != 1 {
			t.Fatalf("atlanan filo-genel anahtar sayısı=%d, want 1", n)
		}
	})
	t.Run("eşzamanlı arıza: 30 servis tek pencerede → hepsi ≥2", func(t *testing.T) {
		var rows []ExceptionSpreadRow
		for i := 0; i < 30; i++ {
			rows = append(rows, spreadRow(fmt.Sprintf("fp%02d", i), fmt.Sprintf("svc-%02d", i),
				"Index # out of bounds for length #", 0, 5, 1))
		}
		got := ComputeExceptionSpread(rows, slack)
		if len(got) != 30 {
			t.Fatalf("30 parmak izinin hepsi istisna almalı, got %d", len(got))
		}
		for fp, info := range got {
			if info.Services < 2 {
				t.Errorf("%s: Services=%d, want ≥2", fp, info.Services)
			}
		}
	})

	t.Run("99 ortakta saymayı bırakır", func(t *testing.T) {
		var rows []ExceptionSpreadRow
		for i := 0; i < 150; i++ {
			rows = append(rows, spreadRow(fmt.Sprintf("fp%03d", i), fmt.Sprintf("svc-%03d", i), msg, 0, 5, 1))
		}
		got := ComputeExceptionSpread(rows, slack)
		if got["fp000"].Services != 100 {
			t.Fatalf("Services=%d, want 100 (1 + 99 tavan)", got["fp000"].Services)
		}
	})
}

func TestExceptionSpreadExemptBelow(t *testing.T) {
	sp := NewExceptionSpread(map[string]SpreadInfo{
		"old":   {Services: 2, Occurrences: 1, LastSeen: 10},
		"new":   {Services: 3, Occurrences: 4, LastSeen: 30},
		"mid":   {Services: 2, Occurrences: 2, LastSeen: 20},
		"above": {Services: 2, Occurrences: 5, LastSeen: 40}, // tabanda: istisnaya gerek yok
		"solo":  {Services: 1, Occurrences: 1, LastSeen: 50}, // tek servis: istisna yok
	}, false)
	if got := sp.ExemptBelow(5); !reflect.DeepEqual(got, []string{"new", "mid", "old"}) {
		t.Fatalf("ExemptBelow(5)=%v, want tabanın altındakiler last_seen DESC", got)
	}
	if got := sp.ExemptBelow(0); got != nil {
		t.Fatalf("taban 0 → nil, got %v", got)
	}
	var nilSp *ExceptionSpread
	if nilSp.ExemptBelow(5) != nil {
		t.Fatal("nil anlık görüntü → istisna yok")
	}
	if _, ok := nilSp.Of("x"); ok {
		t.Fatal("nil anlık görüntü → yayılım yok")
	}

	big := map[string]SpreadInfo{}
	for i := 0; i < SpreadExemptCap+10; i++ {
		big[fmt.Sprintf("fp%05d", i)] = SpreadInfo{Services: 2, Occurrences: 1, LastSeen: int64(i)}
	}
	got := NewExceptionSpread(big, false).ExemptBelow(5)
	if len(got) != SpreadExemptCap || got[0] != fmt.Sprintf("fp%05d", SpreadExemptCap+9) {
		t.Fatalf("tavan %d, en yeni önce: len=%d first=%s", SpreadExemptCap, len(got), got[0])
	}
}

// Of KOPYA döndürür: memo paylaşılır, çağıranın dilimi bozması başka isteğe sızmamalı.
func TestExceptionSpreadOfCopies(t *testing.T) {
	sp := NewExceptionSpread(map[string]SpreadInfo{"a": {Services: 2, Partners: []string{"svc-b"}}}, false)
	info, ok := sp.Of("a")
	if !ok {
		t.Fatal("a bulunmalı")
	}
	info.Partners[0] = "bozuldu"
	again, _ := sp.Of("a")
	if again.Partners[0] != "svc-b" {
		t.Fatal("Of paylaşılan dilimi döndürdü")
	}
}

func TestExceptionGroupWhereFloorExempt(t *testing.T) {
	t.Run("taban + istisna → OR cümlesi", func(t *testing.T) {
		wc := buildExceptionGroupWhere(ExceptionGroupFilter{MinOccurrences: 5, FloorExempt: []string{"fp1", "fp2"}})
		if !strings.Contains(wc.sql(), "(occurrences >= ? OR fingerprint IN (?))") {
			t.Fatalf("OR cümlesi yok:\n%s", wc.sql())
		}
		if strings.Contains(wc.sql(), "AND occurrences >= ?") {
			t.Fatalf("çıplak taban cümlesi istisnayı ezer:\n%s", wc.sql())
		}
	})
	t.Run("tabansız istisna → cümle yok", func(t *testing.T) {
		wc := buildExceptionGroupWhere(ExceptionGroupFilter{FloorExempt: []string{"fp1"}})
		if strings.Contains(wc.sql(), "fingerprint IN") || strings.Contains(wc.sql(), "occurrences") {
			t.Fatalf("taban yokken istisna cümlesi girdi:\n%s", wc.sql())
		}
	})
	t.Run("yalnız taban → eski cümle", func(t *testing.T) {
		wc := buildExceptionGroupWhere(ExceptionGroupFilter{MinOccurrences: 5})
		if !strings.Contains(wc.sql(), "occurrences >= ?") || strings.Contains(wc.sql(), "fingerprint IN") {
			t.Fatalf("eski taban cümlesi değişti:\n%s", wc.sql())
		}
	})
}

// v0.10.949 (operatör kararı 2026-09-26) — regressed grup (resolve sonrası
// yeniden görülen → P2 "regressed") 5'in altında ve tek serviste de olsa
// varsayılan görünümde kalır. "Regressed" = state kolonu (ExStateRegressed),
// öncelik merdiveninin baktığı alan. İstisna tabanla AYNI koşulda; taban
// yoksa hiçbir cümle üretmez; bayraksız filtre eski cümleyi bayt-bayt korur.
func TestExceptionGroupWhereFloorExemptRegressed(t *testing.T) {
	cases := []struct {
		name     string
		f        ExceptionGroupFilter
		wantCond string // "" = taban cümlesi YOK
		wantArgs []any
	}{
		{
			name:     "taban + regressed",
			f:        ExceptionGroupFilter{MinOccurrences: 5, FloorExemptRegressed: true},
			wantCond: "(occurrences >= ? OR state = ?)",
			wantArgs: []any{ExStateIgnored, uint64(5), ExStateRegressed},
		},
		{
			name:     "taban + regressed + çoklu-servis",
			f:        ExceptionGroupFilter{MinOccurrences: 5, FloorExemptRegressed: true, FloorExempt: []string{"fp1"}},
			wantCond: "(occurrences >= ? OR state = ? OR fingerprint IN (?))",
			wantArgs: []any{ExStateIgnored, uint64(5), ExStateRegressed, []string{"fp1"}},
		},
		{
			name:     "taban + yalnız çoklu-servis (regressed bayrağı yok)",
			f:        ExceptionGroupFilter{MinOccurrences: 5, FloorExempt: []string{"fp1"}},
			wantCond: "(occurrences >= ? OR fingerprint IN (?))",
			wantArgs: []any{ExStateIgnored, uint64(5), []string{"fp1"}},
		},
		{
			name:     "açık ?minOcc=N: bayrak yok → eski cümle",
			f:        ExceptionGroupFilter{MinOccurrences: 5},
			wantCond: "occurrences >= ?",
			wantArgs: []any{ExStateIgnored, uint64(5)},
		},
		{
			name:     "tabansız regressed → cümle yok (show all)",
			f:        ExceptionGroupFilter{FloorExemptRegressed: true},
			wantArgs: []any{ExStateIgnored},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wc := buildExceptionGroupWhere(tc.f)
			sql := wc.sql()
			if tc.wantCond == "" {
				if strings.Contains(sql, "occurrences") || strings.Contains(sql, "state = ?") {
					t.Fatalf("taban yokken istisna cümlesi girdi:\n%s", sql)
				}
			} else if !strings.Contains(sql, "AND "+tc.wantCond) && !strings.HasSuffix(sql, tc.wantCond) {
				t.Fatalf("beklenen cümle %q yok:\n%s", tc.wantCond, sql)
			}
			if tc.f.FloorExemptRegressed && tc.wantCond != "" && strings.Contains(sql, "AND occurrences >= ?") {
				t.Fatalf("çıplak taban cümlesi regressed istisnasını ezer:\n%s", sql)
			}
			if !reflect.DeepEqual(wc.args, tc.wantArgs) {
				t.Fatalf("args = %#v, want %#v", wc.args, tc.wantArgs)
			}
		})
	}
	// Taban-altı çekimi (MaxOccurrences) bayraktan etkilenmez: iki çekim
	// hâlâ occurrences üstünden bölüşür, regressed satır üst çekimde de
	// gelir ve api tarafında parmak iziyle tekilleşir.
	wc := buildExceptionGroupWhere(ExceptionGroupFilter{MaxOccurrences: 5, FloorExemptRegressed: true})
	if strings.Contains(wc.sql(), "state = ?") || !strings.Contains(wc.sql(), "occurrences < ?") {
		t.Fatalf("taban-altı çekimi değişmemeli:\n%s", wc.sql())
	}
}

// İki okuma bağlı değer listesiyle, alt sorgusuz (anomaly_event.go kuralı).
func TestExceptionSpreadQueriesShape(t *testing.T) {
	src := mustReadSource(t, "exception_spread.go")
	for _, want := range []string{
		"HAVING uniqExact(service) >= 2",
		"AND ex_type IN (?)",
		"substring(ex_message, 1, 512)",
		"ORDER BY last_seen DESC",
		"max_execution_time = 3", // v0.10.949 — istemci bütçesi (spreadReadBudget) ile aynı
	} {
		if !strings.Contains(src, want) {
			t.Errorf("exception_spread.go %q içermeli", want)
		}
	}
	if strings.Contains(src, "IN (SELECT") {
		t.Error("alt sorgu yok — tür listesi bağlı değer olarak geçmeli")
	}
	if strings.Contains(src, "max_execution_time = 10") {
		t.Error("yayılım okuması 3 sn sunucu sınırıyla koşmalı (liste/rozet bütçesini yemesin)")
	}
}

// v0.10.949 — backoff penceresinde ExceptionSpread CH'a ve memo kilidine
// gitmeden döner. conn nil: bir CH çağrısı panikler — paniğin yokluğu
// "sorgu atılmadı"nın kanıtı.
func TestExceptionSpreadBackoffSkipsCH(t *testing.T) {
	s := &Store{spreadMemo: newTTLMemo[*ExceptionSpread](time.Minute)}
	s.spreadFailUntil.Store(time.Now().Add(time.Minute).UnixNano())
	sp, err := s.ExceptionSpread(context.Background(), 0)
	if !errors.Is(err, errSpreadBackoff) || sp != nil {
		t.Fatalf("backoff içinde errSpreadBackoff/nil beklenirdi, got %v/%v", sp, err)
	}
	// Memo'suz kurulum (test yolu) da aynı hızlı yoldan döner.
	s2 := &Store{}
	s2.spreadFailUntil.Store(time.Now().Add(time.Minute).UnixNano())
	if _, err := s2.ExceptionSpread(context.Background(), 0); !errors.Is(err, errSpreadBackoff) {
		t.Fatalf("memo'suz kurulum da backoff'a uymalı: %v", err)
	}
	// Süresi geçmiş backoff etkisiz; sıfır değer = backoff yok.
	s3 := &Store{}
	s3.spreadFailUntil.Store(time.Now().Add(-time.Second).UnixNano())
	if s3.spreadInBackoff() || (&Store{}).spreadInBackoff() {
		t.Fatal("geçmiş/sıfır backoff aktif sayılmamalı")
	}
}
