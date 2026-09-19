package config

import (
	"os"
	"reflect"
	"testing"
)

// v0.10.804 (dış skill denetimi M4) — COREMETRY_ALLOWED_ORIGINS köprüsü.
func TestAllowedOriginsEnv(t *testing.T) {
	cases := []struct {
		val  string
		set  bool
		want []string
	}{
		{"", false, nil},
		{"https://apm.example.com", true, []string{"https://apm.example.com"}},
		{" https://a.example.com , http://b.example.com:8080 ,, ", true, []string{"https://a.example.com", "http://b.example.com:8080"}},
		{" , ", true, nil},
	}
	for _, c := range cases {
		_ = os.Unsetenv("COREMETRY_ALLOWED_ORIGINS")
		if c.set {
			t.Setenv("COREMETRY_ALLOWED_ORIGINS", c.val)
		}
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !reflect.DeepEqual(cfg.AllowedOrigins, c.want) {
			t.Errorf("val=%q set=%v → %v, istenen %v", c.val, c.set, cfg.AllowedOrigins, c.want)
		}
	}
}
