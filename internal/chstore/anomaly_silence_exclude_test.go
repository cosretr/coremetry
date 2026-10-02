package chstore

import (
	"reflect"
	"strings"
	"testing"
)

// anomaly_silence_exclude_test.go — v0.10.1042 (operatör: "Anomalide 'Mute'
// sonrası satır listeden düşsün").
//
// Inbox open listesi susturulmuş anomalileri SQL'de eler (ExcludeIDs), rozet
// de aynı kümeyi CountActiveAnomalyEvents'te. İkisi AYNI yüklem işlevinden
// (anomalyExcludeIDsSQL) beslenmezse liste ile kenar çubuğu ayrışır — v0.9.322
// sınıfı (rozet 29, liste 2). Eleme LIMIT'ten ÖNCE olmalı: Go'da sonradan
// düşürmek ActiveOnly'nin (v0.9.335) kapattığı tarama-bütçesi açığını geri
// açardı. CH'siz CI için şekil testleri.

func TestAnomalyExcludeIDsSQL(t *testing.T) {
	if cond, args := anomalyExcludeIDsSQL(nil); cond != "" || args != nil {
		t.Errorf("boş küme koşul üretti: %q %v — susturma yokken SQL birebir eskisi olmalı", cond, args)
	}
	if cond, args := anomalyExcludeIDsSQL([]string{}); cond != "" || args != nil {
		t.Errorf("boş dilim koşul üretti: %q %v", cond, args)
	}
	cond, args := anomalyExcludeIDsSQL([]string{"aa", "bb", "cc"})
	if cond != " AND id NOT IN (?,?,?)" {
		t.Errorf("koşul %q", cond)
	}
	if !reflect.DeepEqual(args, []any{"aa", "bb", "cc"}) {
		t.Errorf("argümanlar %v — sıra korunmalı", args)
	}
	if strings.Count(cond, "?") != len(args) {
		t.Error("yer tutucu sayısı argüman sayısına eşit değil")
	}
}

// Liste ve rozet AYNI yüklem işlevini çağırır; kendi NOT IN'lerini yazmazlar.
func TestAnomalyListAndCountShareExclusion(t *testing.T) {
	src := mustReadSource(t, "anomaly_event.go")
	if !strings.Contains(src, "ExcludeIDs []string") {
		t.Fatal("ListAnomalyEventsFilter.ExcludeIDs yok")
	}
	if !strings.Contains(src, "exclSQL, exclArgs := anomalyExcludeIDsSQL(f.ExcludeIDs)") ||
		!strings.Contains(src, "+activeSQL+svcSQL+winSQL+exclSQL+") {
		t.Error("ListAnomalyEvents eleme koşulunu WHERE'e (LIMIT'ten önce) koymuyor")
	}
	if !strings.Contains(src, "exclSQL, exclArgs := anomalyExcludeIDsSQL(excludeIDs)") ||
		!strings.Contains(src, "SECOND`+envSQL+exclSQL,") {
		t.Error("CountActiveAnomalyEvents listeyle aynı eleme yüklemini kullanmıyor — rozet listeden büyük olur")
	}
	if n := strings.Count(src, "NOT IN"); n != 1 {
		t.Errorf("anomaly_event.go'da %d NOT IN — tek kaynak anomalyExcludeIDsSQL olmalı", n)
	}
}

// Yer tutucular konumsal: eleme argümanları WHERE'deki yerleriyle aynı sırada
// (pencereden sonra, LIMIT'ten önce) bağlanmalı. Yanlış sıra hata vermez —
// pencere zaman damgasını kimlik olarak bağlar, sessizce başka cevap döner.
func TestAnomalyExcludePlaceholderOrder(t *testing.T) {
	src := mustReadSource(t, "anomaly_event.go")
	i := strings.Index(src, "func (s *Store) ListAnomalyEvents(")
	if i < 0 {
		t.Fatal("ListAnomalyEvents bulunamadı")
	}
	body := src[i:]
	order := []string{
		"args = append(args, toAnySlice(f.Services)...)",
		"args = append(args, f.FromNs)",
		"args = append(args, f.ToNs)",
		"args = append(args, exclArgs...)",
		"args = append(args, f.Limit)",
	}
	pos := -1
	for _, w := range order {
		p := strings.Index(body, w)
		if p <= pos {
			t.Fatalf("argüman sırası bozuk: %q", w)
		}
		pos = p
	}
	j := strings.Index(src, "func (s *Store) CountActiveAnomalyEvents(")
	if j < 0 {
		t.Fatal("CountActiveAnomalyEvents bulunamadı")
	}
	cnt := src[j:]
	if a, b := strings.Index(cnt, "args = append(args, envServices)"), strings.Index(cnt, "args = append(args, exclArgs...)"); a < 0 || b < 0 || a > b {
		t.Error("rozet sayımında eleme argümanları env argümanlarından SONRA bağlanmalı (WHERE sırası)")
	}
}
