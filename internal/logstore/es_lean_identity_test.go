package logstore

// es_lean_identity_test.go — v0.10.1496 (d), operatör (prod ES): /logs
// "Desenler" satırlarında Servisler "–", Seviye 0 (hacim çubuğu DEBUG 260).
// Kök neden: desen örneklemesinin lean `_source`u mapHit'in servis
// zincirini içermiyordu. Test gerçek bir _source izdüşümü (ES includes
// semantiği: düz anahtar ya da iç içe nesne yolu) üstünden mapHit'i koşar
// ve tam kaynakla AYNI servis/seviyeyi ister.

import (
	"strings"
	"testing"
)

// projectSource — ES `_source.includes` taklidi: her yol ya düz (noktalı
// literal) anahtar ya da iç içe nesne yolu olarak kopyalanır.
func projectSource(src map[string]any, paths []string) map[string]any {
	out := map[string]any{}
	for _, p := range paths {
		if v, ok := src[p]; ok {
			out[p] = v
			continue
		}
		parts := strings.Split(p, ".")
		cur, dst := src, out
		for i, part := range parts {
			v, ok := cur[part]
			if !ok {
				break
			}
			if i == len(parts)-1 {
				dst[part] = v
				break
			}
			next, ok := v.(map[string]any)
			if !ok {
				break
			}
			d, _ := dst[part].(map[string]any)
			if d == nil {
				d = map[string]any{}
				dst[part] = d
			}
			cur, dst = next, d
		}
	}
	return out
}

func TestLeanSourceKeepsRowIdentity(t *testing.T) {
	cases := []struct {
		name    string
		src     map[string]any
		wantSvc string
		wantSev string
	}{
		{"openshift cluster-logging", map[string]any{
			"@timestamp": "2026-10-24T03:40:41Z", "message": `{"request_id":"QXC1015060201WKue0000"}`,
			"level":      "debug",
			"kubernetes": map[string]any{"container_name": "orders-api", "namespace_name": "kxzone-prod"},
		}, "orders-api", "debug"},
		{"otel ecs nested", map[string]any{
			"@timestamp": "2026-10-24T03:40:41Z", "message": "boot ok",
			"service": map[string]any{"name": "billing-api"}, "severity_text": "INFO",
		}, "billing-api", "INFO"},
		{"flat shipper", map[string]any{
			"@timestamp": "2026-10-24T03:40:41Z", "message": "x", "serviceName": "checkout", "SeverityText": "WARN",
		}, "checkout", "WARN"},
		{"ecs nested container + log.level", map[string]any{
			"@timestamp": "2026-10-24T03:40:41Z", "message": "x",
			"kubernetes": map[string]any{"container": map[string]any{"name": "ledger"}},
			"log":        map[string]any{"level": "error"},
		}, "ledger", "error"},
	}
	cfg := ESConfig{}
	cfg.defaults()
	s := &ESStore{fields: cfg.Fields, cfg: cfg}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			full := s.mapHit("d", c.src, nil, "", "")
			lean := s.mapHit("d", projectSource(c.src, s.leanSourceFields()), nil, "", "")
			if full.ServiceName != c.wantSvc || full.SeverityText != c.wantSev {
				t.Fatalf("fixture: full mapHit svc=%q sev=%q", full.ServiceName, full.SeverityText)
			}
			if lean.ServiceName != full.ServiceName || lean.SeverityText != full.SeverityText || lean.Severity != full.Severity {
				t.Fatalf("lean _source lost row identity: svc=%q sev=%q/%d, full svc=%q sev=%q/%d",
					lean.ServiceName, lean.SeverityText, lean.Severity, full.ServiceName, full.SeverityText, full.Severity)
			}
		})
	}
}

// Desen satırı: seviye sayısı metinden, metin yoksa JSON gövdeden; servis
// boşsa tek-servis süzgecinden (patternRowIdentity).
func TestPatternRowIdentity(t *testing.T) {
	cases := []struct {
		name              string
		rec               LogRecord
		bodyLevel, filter string
		sev               uint8
		txt, svc          string
	}{
		{"text only → number", LogRecord{SeverityText: "DEBUG", ServiceName: "a"}, "", "", 5, "DEBUG", "a"},
		{"number kept", LogRecord{Severity: 17, SeverityText: "ERROR", ServiceName: "a"}, "", "", 17, "ERROR", "a"},
		{"body level fallback", LogRecord{}, "warn", "", 13, "warn", ""},
		{"record text wins over body", LogRecord{SeverityText: "INFO"}, "error", "", 9, "INFO", ""},
		{"single service filter fills", LogRecord{}, "", "orders-api", 0, "", "orders-api"},
		{"record service wins", LogRecord{ServiceName: "b"}, "", "orders-api", 0, "", "b"},
		{"unknown text", LogRecord{SeverityText: "notice"}, "", "", 0, "notice", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := c.rec
			sev, txt, svc := patternRowIdentity(&r, c.bodyLevel, c.filter)
			if sev != c.sev || txt != c.txt || svc != c.svc {
				t.Fatalf("got (%d,%q,%q) want (%d,%q,%q)", sev, txt, svc, c.sev, c.txt, c.svc)
			}
		})
	}
}
