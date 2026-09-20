package config

import (
	"os"
	"testing"
)

// v0.10.822 — okuma tarafı replika seçimi env köprüsü. Sözleşme: config
// yalnız TAŞIR (boşluk kırpar, küçük harfe indirir); izin listesi ve uyarı
// TEK gövdede, chstore.chReadBalancingSettings içinde yaşar — orası ayarı
// gerçekten uygulayan yer. İkiz bir izin listesi burada olsaydı sapardı
// (v0.9.1358 dersi), o yüzden geçersiz değer burada AYNEN saklanır ve
// boot'ta chstore uyarısına düşer.
func TestReadBalancingEnv(t *testing.T) {
	t.Run("load balancing", func(t *testing.T) {
		cases := []struct {
			val  string
			set  bool
			want string
		}{
			{"", false, ""},
			{"", true, ""},
			{"in_order", true, "in_order"},
			{"  IN_ORDER  ", true, "in_order"},
			{"first_or_random", true, "first_or_random"},
			{"nearest_hostname", true, "nearest_hostname"},
			{"round_robin", true, "round_robin"},
			{"random", true, "random"},
			// Geçersiz: sessizce boşa çevrilmez — chstore boot'ta uyarır.
			{"in-order", true, "in-order"},
		}
		for _, c := range cases {
			_ = os.Unsetenv("COREMETRY_CH_READ_LOAD_BALANCING")
			if c.set {
				t.Setenv("COREMETRY_CH_READ_LOAD_BALANCING", c.val)
			}
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ClickHouse.ReadLoadBalancing != c.want {
				t.Errorf("val=%q set=%v → %q, istenen %q", c.val, c.set, cfg.ClickHouse.ReadLoadBalancing, c.want)
			}
		}
	})
	t.Run("prefer localhost üç durumlu", func(t *testing.T) {
		cases := []struct {
			val  string
			set  bool
			want string
		}{
			{"", false, ""}, // ayarsız = ayar gönderme
			{"", true, ""},  // boş da ayarsız sayılır
			{"1", true, "1"},
			{"0", true, "0"},
			{"TRUE", true, "true"},
			{"  false ", true, "false"},
			{"maybe", true, "maybe"}, // chstore uyarır ve yok sayar
		}
		for _, c := range cases {
			_ = os.Unsetenv("COREMETRY_CH_READ_PREFER_LOCALHOST")
			if c.set {
				t.Setenv("COREMETRY_CH_READ_PREFER_LOCALHOST", c.val)
			}
			cfg, err := Load("")
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.ClickHouse.ReadPreferLocalhost != c.want {
				t.Errorf("val=%q set=%v → %q, istenen %q", c.val, c.set, cfg.ClickHouse.ReadPreferLocalhost, c.want)
			}
		}
	})
}
