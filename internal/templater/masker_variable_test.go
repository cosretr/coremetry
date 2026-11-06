package templater

// masker_variable_test.go — v0.10.1829 regresyon testi (Drain kimliği).
//
// Operator-reported: "Sadece bazen loglarda GUID, UUID, id değerleri
// oluyor; bunları yeni log pattern'i algılamasın." Aynı mesaj farklı bir
// GUID / UUID / id / sayıyla gelince Drain ayrı şablon (ayrı kimlik,
// defterde first_seen = şimdi) üretiyor, "yeni şablon" uyarısı doğuyordu.
// Korpus imza testiyle ORTAK (logstore/maskcorpus). Geçiş: defterdeki eski
// (v1) şablon Remask ile yeni maskenin çıktısına TAM eşitlenir.

import (
	"testing"

	"github.com/cilcenk/coremetry/internal/logstore/maskcorpus"
)

func TestMask_SameMessageDifferentIDs(t *testing.T) {
	for _, g := range maskcorpus.Same {
		t.Run(g.Name, func(t *testing.T) {
			want := Mask(g.Lines[0])
			for _, l := range g.Lines[1:] {
				if got := Mask(l); got != want {
					t.Errorf("template split:\n  %q → %q\n  %q → %q", g.Lines[0], want, l, got)
				}
			}
		})
	}
}

func TestMask_MustStayDifferent(t *testing.T) {
	for _, c := range maskcorpus.Different {
		if c.SignatureOnly {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			if a, b := Mask(c.A), Mask(c.B); a == b {
				t.Errorf("over-merged: %q and %q → %q", c.A, c.B, a)
			}
		})
	}
}

// Uçtan uca: her grubun satırları soğuk bir ağaçta TEK küme, tek kimlik
// (puller'ın tik başına soğuk kurduğu ağaç — v0.10.1030).
func TestDrain_CorpusGroupIsOneTemplate(t *testing.T) {
	for _, g := range maskcorpus.Same {
		t.Run(g.Name, func(t *testing.T) {
			d := NewDrain()
			for i, l := range g.Lines {
				d.Add(l, "orders-api", int64(i))
			}
			if snap := d.Snapshot(); len(snap) != 1 {
				for _, c := range snap {
					t.Logf("%d × %s", c.Count, c.TemplateString())
				}
				t.Fatalf("want 1 template, got %d", len(snap))
			}
		})
	}
}

// ORA kodları BUGÜN de (v1) tek kalıp: `ORA-<*>` — hata kodu ayrımı ayrı bir
// karar (operatöre raporlandı), bu sürüm davranışı DEĞİŞTİRMEZ.
func TestMask_OracleCodeBehaviourUnchanged(t *testing.T) {
	for _, l := range []string{"Encountered ORA-00942 fault", "Encountered ORA-01017 fault"} {
		if got, old := Mask(l), maskV1(l); got != "Encountered ORA-<*> fault" || old != got {
			t.Fatalf("Mask(%q) = %q, v1 = %q", l, got, old)
		}
	}
}

// remaskFixtures — korpus + v1 testlerinin satırları + Drain ön kurallarına
// takılan biçimler (yol, köşeli parantez, URL, önekli kimlik, JSON).
func remaskFixtures() []string {
	out := []string{
		"User usr_54347 initiated transfer request of $1000.50 to account acc_1523",
		"Order ord_12345 from cust_67 via txn_8888888",
		"Starting agent v1.2.3", "Returned 200 OK", "Encountered ORA-12345 fault",
		`{"request_id":"SPE1015060201ZTod000000000000202610240340413867","msg":"inquiry completed in 15ms"}`,
		"[http-nio-8080-exec-7] GET https://acme.example/api/orders/54015241?x=1 from 10.0.0.1:443",
		"reading /var/log/orders-api/app-7d9f6-x2k4p.log at 2026-09-02T10:15:30.123Z",
		"bean com.acme.OrderService@7f9e2d4c ready, pool-3-thread-7 took 18.45MB in 1,500ms",
		"cache id_550e8400-e29b-41d4-a716-446655440000 evicted; key {6ba7b810-9dad-11d1-80b4-00c04fd430c8}",
		"client fe80::1ff:fe23:4567:890a asked for urn:uuid:550e8400-e29b-41d4-a716-446655440000",
		"Record not found: {0}. Record not found: 26,916,627.",
		"processed 1 234 567 rows for TX9F3K2 at 10:15:30",
	}
	for _, g := range maskcorpus.Same {
		out = append(out, g.Lines...)
	}
	for _, p := range maskcorpus.Different {
		out = append(out, p.A, p.B)
	}
	return out
}

// Geçiş sözleşmesi: defterdeki v1 şablonu (MaskIDTokens son geçişli) GÜNCEL
// maskeyle yeniden maskelenince yeni maskenin çıktısına TAM eşit — bilinen
// şablon karşılaştırması (variantOfKnown, ML aile tekilleştirmesi) eski
// satırı yeni adayla eşleştirir.
func TestRemask_V1TemplateEqualsNewMask(t *testing.T) {
	for _, l := range remaskFixtures() {
		if got, want := Remask(maskV1(l)), Mask(l); got != want {
			t.Errorf("line %q\n  Remask(v1) = %q\n  Mask       = %q", l, got, want)
		}
	}
}

// Remask idempotent ve belirteç sayısını korur (Drain katman 1 + bilinen-
// şablon SQL ön süzgeci TokenCounts eski satırların sayısıyla çalışır).
func TestRemask_IdempotentAndTokenCountInvariant(t *testing.T) {
	for _, l := range remaskFixtures() {
		m := Mask(l)
		if r := Remask(m); r != m {
			t.Errorf("not idempotent: %q → %q → %q", l, m, r)
		}
		old := maskV1(l)
		if a, b := countTemplateTokens(old), countTemplateTokens(Remask(old)); a != b {
			t.Errorf("token count changed %d → %d: %q → %q", a, b, old, Remask(old))
		}
	}
}

// Aile yüklemi eski defter satırını (v1) yeni adayla (v2) aynı aile sayar —
// anomaly.variantOfKnown ve ml.selectFamilies buna dayanır.
func TestSameTemplateFamily_OldRowVsNewCandidate(t *testing.T) {
	cases := [][2]string{
		{"query took 1500ms for TX9F3K2", "query took 30ms for QZ7M4P1"},
		{"pod orders-api-7d9f6-x2k4p ready", "pod orders-api-84f6d-wq2zx ready"},
		{"Record not found: {0}.", "Record not found: 54015241."},
		{"heap 18.4MB used", "heap 512MB used"},
	}
	for _, c := range cases {
		oldRow, cand := maskV1(c[0]), Mask(c[1])
		if oldRow == cand {
			t.Fatalf("fixture does not exercise the migration: %q", oldRow)
		}
		if !SameTemplateFamily(oldRow, cand) || !ParseTemplate(oldRow).SameFamily(ParseTemplate(cand)) {
			t.Errorf("old %q vs new %q: not one family", oldRow, cand)
		}
	}
}
