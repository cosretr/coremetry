package chstore

// anomaly_sensitivity_load_test.go — v0.10.1039 (inceleme): ayar okuma hatası
// iyi bir değerin ÜSTÜNE varsayılan yayınlamaz.
//
// Neden şimdi: batch kalıplarıyla varsayılan liste (["-batch"]) TEK YÖNLÜ
// kapatma eylemleri sürüyor. Operatör `[]` (kural kapalı) ya da başka
// kalıplar kaydettiyse, tek bir okuma hıçkırığında varsayılanın yayınlanması
// açık batch problemlerini kapatıp bir tik sonra YENİ problem + yeni bildirim
// olarak geri açtırırdı.

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
)

func TestLoadAnomalySensitivityKeepsLastGoodValue(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	okWith := func(p ...string) func() (AnomalySensitivityConfig, error) {
		return func() (AnomalySensitivityConfig, error) {
			if p == nil {
				p = []string{}
			}
			c := DefaultAnomalySensitivity()
			c.BatchServicePatterns = &p
			return NormalizeAnomalySensitivity(c), nil
		}
	}
	fail := func() (AnomalySensitivityConfig, error) { return AnomalySensitivityConfig{}, errors.New("ch: timeout") }

	t.Run("hiç yüklenmemiş + hata → varsayılan yayınlanır ama DOĞRULANMAMIŞ", func(t *testing.T) {
		s := &Store{}
		got := s.loadAnomalySensitivityWith(fail)
		if s.anomalySensitivity.Load() == nil {
			t.Fatal("hiçbir şey yayınlanmadı")
		}
		if got.CriticalZ != DefaultAnomalySensitivity().CriticalZ {
			t.Fatalf("varsayılan yayınlanmadı: %+v", got)
		}
		if s.AnomalySensitivityConfirmed() {
			t.Fatal("hata ile yayınlanan varsayılan DOĞRULANMIŞ sayıldı")
		}
		// Karar noktaları batch kuralını devre dışı görür (tahminle susturma yok).
		if s.AnomalySensitivityForDetectors().IsBatchService("orders-batch") {
			t.Fatal("doğrulanmamış ayarda batch kuralı etkin")
		}
		// Yayınlanan değerin kendisi varsayılan (diğer okuyucular için).
		if !s.AnomalySensitivity().IsBatchService("orders-batch") {
			t.Fatal("yayınlanan varsayılan batch listesini taşımıyor")
		}
	})

	t.Run("iyi değer → hata → değer AYNEN kalır", func(t *testing.T) {
		s := &Store{}
		s.loadAnomalySensitivityWith(okWith()) // operatör kuralı kapattı
		if !s.AnomalySensitivityConfirmed() || s.AnomalySensitivity().IsBatchService("orders-batch") {
			t.Fatal("başarılı okuma yayınlanmadı / doğrulanmadı")
		}
		for i := 0; i < 3; i++ {
			got := s.loadAnomalySensitivityWith(fail)
			if got.IsBatchService("orders-batch") || s.AnomalySensitivityForDetectors().IsBatchService("orders-batch") {
				t.Fatal("okuma hatası varsayılanı (-batch) iyi değerin üstüne yayınladı")
			}
			if !s.AnomalySensitivityConfirmed() {
				t.Fatal("okuma hatası doğrulanmış durumu düşürdü")
			}
		}
	})

	t.Run("hatadan sonra başarı → yeni değer yayınlanır", func(t *testing.T) {
		s := &Store{}
		s.loadAnomalySensitivityWith(okWith("-batch"))
		s.loadAnomalySensitivityWith(fail)
		s.loadAnomalySensitivityWith(okWith("etl-"))
		c := s.AnomalySensitivityForDetectors()
		if !c.IsBatchService("etl-loader") || c.IsBatchService("orders-batch") {
			t.Fatalf("yeni değer yayınlanmadı: %v", c.BatchServicePatternList())
		}
		// Hiç yüklenmemişken hata, sonra başarı → doğrulanır.
		s2 := &Store{}
		s2.loadAnomalySensitivityWith(fail)
		s2.loadAnomalySensitivityWith(okWith("-batch"))
		if !s2.AnomalySensitivityConfirmed() || !s2.AnomalySensitivityForDetectors().IsBatchService("orders-batch") {
			t.Fatal("hatadan sonraki başarılı okuma doğrulamadı")
		}
	})

	t.Run("PUT (SetAnomalySensitivity) doğrular", func(t *testing.T) {
		s := &Store{}
		s.loadAnomalySensitivityWith(fail)
		s.SetAnomalySensitivity(DefaultAnomalySensitivity())
		if !s.AnomalySensitivityConfirmed() {
			t.Fatal("PUT ile gelen değer doğrulanmış sayılmadı")
		}
	})

	t.Run("hata logu geçişte bir kez", func(t *testing.T) {
		buf.Reset()
		s := &Store{}
		s.loadAnomalySensitivityWith(okWith("-batch"))
		for i := 0; i < 5; i++ {
			s.loadAnomalySensitivityWith(fail)
		}
		if n := strings.Count(buf.String(), "anomaly_sensitivity okunamadı"); n != 1 {
			t.Fatalf("5 ardışık hata %d log satırı üretti, 1 bekleniyordu", n)
		}
		s.loadAnomalySensitivityWith(okWith("-batch"))
		s.loadAnomalySensitivityWith(okWith("-batch"))
		if n := strings.Count(buf.String(), "yeniden okunabiliyor"); n != 1 {
			t.Fatalf("toparlanma %d kez loglandı, 1 bekleniyordu", n)
		}
		s.loadAnomalySensitivityWith(fail)
		if n := strings.Count(buf.String(), "anomaly_sensitivity okunamadı"); n != 2 {
			t.Fatalf("yeni hata geçişi loglanmadı (%d)", n)
		}
	})
}
