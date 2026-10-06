package anomaly

// v0.10.1113 — "Başlangıçta doğan log şablonları" (kök neden kanıtı). Pinler:
// pencere [başlangıç − 10 dk, başlangıç + 5 dk) ve servis kümesi SQL'de VE Go
// kemerinde; "yeni" kararı dedektörün aile süzgeciyle (bilinen varyant
// düşer, aynı aileden yalnız en erken doğan); sıra başlangıca yakınlık, sonra
// sayı; ≤5 satır, ≤5 servis; okuma bütçesi (servis yok → 0, aday yok → 1,
// en çok 2) ve sınırlı okuma (FINAL + LIMIT + max_execution_time).

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
)

func TestLogTemplateEvidenceSelection(t *testing.T) {
	start := time.Unix(1_790_000_000, 0).UnixNano()
	from, to := LogTemplateEvidenceWindow(start)
	sec := int64(time.Second)
	const svc, cause, other = "payments-api", "svc-orders", "checkout"

	cases := []struct {
		name     string
		services []string
		cands    []chstore.LogTemplate
		known    []chstore.LogTemplate
		wantIDs  []string
	}{
		{
			name:     "pencerede doğan yeni aile → kalır",
			services: []string{svc},
			cands:    []chstore.LogTemplate{tplRow("new", plainNew, start-40*sec, 12, svc)},
			wantIDs:  []string{"new"},
		},
		{
			name:     "bilinen şablonun Drain-varyantı → düşer",
			services: []string{svc},
			cands:    []chstore.LogTemplate{tplRow("v2", userV2, start+10*sec, 30, svc)},
			known:    []chstore.LogTemplate{tplRow("v1", userV1, from-int64(time.Hour), 900, svc)},
			wantIDs:  []string{},
		},
		{
			name:     "başka servisin bilinen varyantı bastırmaz",
			services: []string{svc},
			cands:    []chstore.LogTemplate{tplRow("v2", userV2, start+10*sec, 30, svc)},
			known:    []chstore.LogTemplate{tplRow("v1", userV1, from-int64(time.Hour), 900, other)},
			wantIDs:  []string{"v2"},
		},
		{
			name:     "pencere dışı (önce / üst sınırda / sonra) → düşer (kemer)",
			services: []string{svc},
			cands: []chstore.LogTemplate{
				tplRow("early", plainNew, from-sec, 12, svc),
				tplRow("edge", orderV1, to, 12, svc),
				tplRow("late", userV1, to+int64(time.Minute), 12, svc),
				tplRow("lo-edge", orderPrefix+` x"}`, from, 5, svc), // alt sınır DAHİL
			},
			wantIDs: []string{"lo-edge"},
		},
		{
			name:     "istenmeyen servis / servissiz → düşer (kemer)",
			services: []string{svc},
			cands: []chstore.LogTemplate{
				tplRow("foreign", plainNew, start, 12, other),
				tplRow("svcless", orderV1, start, 12),
			},
			wantIDs: []string{},
		},
		{
			// F1 — puller total_count'u her tikin örneğiyle EZER: geçmiş
			// problemin şablonu sonradan 1-2'ye inse de kanıt kaybolmaz
			// (dedektörün ≥3 tabanı burada YOK).
			name:     "sayı 1-2 (sonraki tik ezdi) → kalır",
			services: []string{svc},
			cands: []chstore.LogTemplate{
				tplRow("one", userV1, start+sec, 1, svc),
				tplRow("two", plainNew, start-sec, 2, svc),
			},
			wantIDs: []string{"two", "one"}, // |offset| eşit (1 sn) → sayı azalan
		},
		{
			// F1 — bilinen kümede sayı tabanı yok: tek kez görülmüş eski bir
			// varyant da bastırır (dedektörün BlipIsNotKnown'unun tersi —
			// kanıtta daha çok gizlemek güvenli yön).
			name:     "bilinen blip (sayı 1) varyantı da bastırır",
			services: []string{svc},
			cands:    []chstore.LogTemplate{tplRow("v2", userV2, start, 30, svc)},
			known:    []chstore.LogTemplate{tplRow("v1", userV1, from-int64(time.Hour), 1, svc)},
			wantIDs:  []string{},
		},
		{
			name:     "aynı aileden yalnız en erken doğan",
			services: []string{svc},
			cands: []chstore.LogTemplate{
				tplRow("u3", userV3, start-2*sec, 40, svc),
				tplRow("u1", userV1, start-5*sec, 4, svc),
			},
			wantIDs: []string{"u1"},
		},
		{
			name:     "sıra: başlangıca yakın önce, eşitlikte sayı azalan",
			services: []string{svc, cause},
			cands: []chstore.LogTemplate{
				tplRow("far-before", plainNew, start-9*int64(time.Minute), 900, svc),
				tplRow("near-after", orderV1, start+20*sec, 5, cause),
				tplRow("near-before-big", userV1, start-20*sec, 50, svc),
				tplRow("mid", "worker pool exhausted after <*> retries on queue <*>", start+3*int64(time.Minute), 7, cause),
			},
			wantIDs: []string{"near-before-big", "near-after", "mid", "far-before"},
		},
		{
			name:     "≤5 satır",
			services: []string{svc},
			cands: []chstore.LogTemplate{
				tplRow("a", "alpha failure code <*>", start+1*sec, 3, svc),
				tplRow("b", "bravo stream closed by peer <*> <*>", start+2*sec, 3, svc),
				tplRow("c", "charlie cache eviction storm on shard <*> now", start+3*sec, 3, svc),
				tplRow("d", "delta deadlock detected while holding lock <*> for <*> ms", start+4*sec, 3, svc),
				tplRow("e", "echo upstream returned status <*>", start+5*sec, 3, svc),
				tplRow("f", "foxtrot token refresh rejected", start+6*sec, 3, svc),
			},
			wantIDs: []string{"a", "b", "c", "d", "e"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{c.cands, c.known}}
			got, err := logTemplateEvidence(context.Background(), fake, svc, c.services, start)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(got))
			for _, g := range got {
				ids = append(ids, g.TemplateID)
			}
			if !reflect.DeepEqual(ids, c.wantIDs) {
				t.Errorf("çıkan = %v, want %v", ids, c.wantIDs)
			}
		})
	}
}

func TestLogTemplateEvidenceReads(t *testing.T) {
	start := time.Unix(1_790_000_000, 0).UnixNano()
	from, to := LogTemplateEvidenceWindow(start)
	if from != start-int64(10*time.Minute) || to != start+int64(5*time.Minute) {
		t.Fatalf("pencere = [%d, %d), want [başlangıç−10dk, başlangıç+5dk)", from-start, to-start)
	}
	services := []string{"payments-api", "svc-orders"}
	fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{
		{tplRow("c-user2", userV2, start-30*int64(time.Second), 7, "payments-api", "checkout")},
		{tplRow("k-order", orderV1, from-int64(time.Hour), 90, "checkout")},
	}}
	got, err := logTemplateEvidence(context.Background(), fake, "payments-api", services, start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].OffsetSec != -30 || got[0].Service != "payments-api" {
		t.Fatalf("got %+v", got)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("okuma sayısı %d, want 2", len(fake.calls))
	}
	// Aday okuması: dedektörün süzgeci + pencere üst sınırı + servis kümesi,
	// sayı tabanı 1 (F1: dedektörün 3'ü değil).
	wantCand := candidateTemplatesFilter(from)
	wantCand.FirstSeenBeforeNs = to
	wantCand.AnyServices = services
	wantCand.MinTotalCount = 1
	if !reflect.DeepEqual(fake.calls[0], wantCand) {
		t.Errorf("aday süzgeci:\n  got  %+v\n  want %+v", fake.calls[0], wantCand)
	}
	if fake.calls[0].Limit <= 0 || fake.calls[0].Limit > 500 {
		t.Errorf("aday okuması sınırsız: %+v", fake.calls[0])
	}
	// Bilinen okuması: dedektörün AYNI kurucusu, "now" = pencere sonu, sayı
	// tabanı YOK (0 = SQL kısıtı yok).
	wantKnown := knownTemplatesFilter(parseLogTemplates([]chstore.LogTemplate{
		tplRow("c-user2", userV2, start-30*int64(time.Second), 7, "payments-api", "checkout"),
	}), time.Unix(0, to), from)
	wantKnown.MinTotalCount = 0
	if !reflect.DeepEqual(fake.calls[1], wantKnown) {
		t.Errorf("bilinen süzgeci:\n  got  %+v\n  want %+v", fake.calls[1], wantKnown)
	}
	if wantKnown.SinceNs != to-int64(knownTemplatesHorizon) || wantKnown.FirstSeenBeforeNs != from {
		t.Errorf("bilinen ufku/tümleyeni: %+v", wantKnown)
	}

	t.Run("servis yok → okuma yok", func(t *testing.T) {
		f := &fakeTemplateLister{}
		got, err := logTemplateEvidence(context.Background(), f, "", []string{" ", ""}, start)
		if err != nil || got == nil || len(got) != 0 || len(f.calls) != 0 {
			t.Fatalf("got=%v err=%v calls=%d", got, err, len(f.calls))
		}
	})
	t.Run("aday yok → tek okuma", func(t *testing.T) {
		f := &fakeTemplateLister{resp: [][]chstore.LogTemplate{{}}}
		got, err := logTemplateEvidence(context.Background(), f, "payments-api", services, start)
		if err != nil || got == nil || len(got) != 0 || len(f.calls) != 1 {
			t.Fatalf("got=%v err=%v calls=%d", got, err, len(f.calls))
		}
	})
	t.Run("bilinen okuması düşer → fail-closed hata", func(t *testing.T) {
		f := &fakeTemplateLister{
			resp: [][]chstore.LogTemplate{{tplRow("c", plainNew, start, 9, "payments-api")}},
			errs: []error{nil, errors.New("code: 159, timeout exceeded")},
		}
		got, err := logTemplateEvidence(context.Background(), f, "payments-api", services, start)
		if err == nil || !strings.Contains(err.Error(), "fail-closed") || got != nil {
			t.Fatalf("got=%v err=%v — varyantlar 'yeni' diye gösterilmemeli", got, err)
		}
	})
}

// TestLogTemplateEvidenceKeepsDetectorFloor — F1'in sınırı: kanıtın gevşek
// sayı tabanı paylaşılan işlevlerin VARSAYILANINI değiştirmez; dedektörün
// aday / bilinen süzgeçleri ve kemerleri hâlâ ≥3.
func TestLogTemplateEvidenceKeepsDetectorFloor(t *testing.T) {
	since := time.Unix(1_790_000_000, 0).UnixNano()
	if f := candidateTemplatesFilter(since); f.MinTotalCount != 3 {
		t.Errorf("dedektör aday süzgeci tabanı %d, want 3", f.MinTotalCount)
	}
	cands := parseLogTemplates([]chstore.LogTemplate{tplRow("c", plainNew, since, 5, "payments-api")})
	if f := knownTemplatesFilter(cands, time.Unix(0, since), since); f.MinTotalCount != 3 {
		t.Errorf("dedektör bilinen süzgeci tabanı %d, want 3", f.MinTotalCount)
	}
	rows := []chstore.LogTemplate{
		tplRow("two", plainNew, since, 2, "payments-api"),
		tplRow("three", orderV1, since, 3, "payments-api"),
	}
	if got := emittedIDs(newTemplateCandidates(rows, since)); !reflect.DeepEqual(got, []string{"three"}) {
		t.Errorf("dedektör aday kemeri %v, want [three]", got)
	}
	old := []chstore.LogTemplate{
		tplRow("k2", plainNew, since-1, 2, "payments-api"),
		tplRow("k3", orderV1, since-1, 3, "payments-api"),
	}
	if got := emittedIDs(usableKnownTemplates(old, since)); !reflect.DeepEqual(got, []string{"k3"}) {
		t.Errorf("dedektör bilinen kemeri %v, want [k3]", got)
	}
	if got := emittedIDs(usableKnownTemplatesMin(old, since, logTemplateEvidenceKnownMin)); !reflect.DeepEqual(got, []string{"k2", "k3"}) {
		t.Errorf("kanıtın bilinen kemeri %v, want [k2 k3]", got)
	}
}

func TestLogTemplateEvidenceRow(t *testing.T) {
	start := time.Unix(1_790_000_000, 0).UnixNano()
	long := "payments-api ledger write rejected for account <*> because " + strings.Repeat("upstream reservation conflict ", 8) + "on <*>"
	row := tplRow("long", long, start+int64(2*time.Minute)+int64(400*time.Millisecond), 9, "svc-orders", "payments-api")
	row.Sample = strings.Repeat("ş", 400)
	ev := logTemplateEvidenceFrom(row, "payments-api", []string{"payments-api", "svc-orders"}, start)
	if ev.OffsetSec != 120 {
		t.Errorf("offset = %d, want 120 (saniyeye yuvarlı)", ev.OffsetSec)
	}
	if ev.Template != truncTemplate(long, 160) || !strings.HasSuffix(ev.Template, "…") {
		t.Errorf("şablon dedektörle aynı kesilmeli: %q", ev.Template)
	}
	if ev.Query != logstore.PatternSearchQuery(long) || ev.Query == "" {
		t.Errorf("arama metni KESİLMEMİŞ şablondan: %q", ev.Query)
	}
	if r := []rune(ev.Sample); len(r) != 301 { // 300 + "…"
		t.Errorf("örnek rune-güvenli kesilmeli: %d rune", len(r))
	}
	if ev.Service != "payments-api" {
		t.Errorf("özne şablondaysa atıf özneye: %q", ev.Service)
	}
	// Özne yoksa: istenen servislerden sözlükte en küçüğü (sıradan bağımsız).
	for _, svcs := range [][]string{{"svc-orders", "checkout"}, {"checkout", "svc-orders"}} {
		if got := evidenceServiceOf([]string{"svc-orders", "checkout", "zeta"}, "", svcs); got != "checkout" {
			t.Errorf("atıf %v → %q, want checkout", svcs, got)
		}
	}
	if got := evidenceServiceOf([]string{"zeta"}, "payments-api", []string{"payments-api"}); got != "" {
		t.Errorf("kesişim yokken atıf %q", got)
	}
}

func TestLogTemplateEvidenceServices(t *testing.T) {
	cases := []struct {
		subject string
		related []string
		want    []string
	}{
		{"payments-api", nil, []string{"payments-api"}},
		{"", []string{"svc-orders"}, []string{"svc-orders"}},
		{" payments-api ", []string{"payments-api", "", " svc-orders", "svc-orders"}, []string{"payments-api", "svc-orders"}},
		{"payments-api", []string{"a", "b", "c", "d", "e", "f"}, []string{"payments-api", "a", "b", "c", "d"}},
		{"", nil, []string{}},
	}
	for _, c := range cases {
		got := LogTemplateEvidenceServices(c.subject, c.related)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("(%q, %v) = %v, want %v", c.subject, c.related, got, c.want)
		}
		if len(got) > LogTemplateEvidenceMaxServices {
			t.Errorf("servis tavanı aşıldı: %v", got)
		}
	}
}

// TestLogTemplateEvidenceSourcePin — çatal yok: çekirdek dedektörün süzgeç
// kurucularını ve aile süzgecini ÇAĞIRIR; okuma ListLogTemplates'in sınırlı
// sorgusundan geçer (FINAL + LIMIT + max_execution_time, tavan 500).
func TestLogTemplateEvidenceSourcePin(t *testing.T) {
	src, err := os.ReadFile("log_template_evidence.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	for _, w := range []string{
		"candidateTemplatesFilter(fromNs)",
		"newTemplateCandidatesMin(tmpls, fromNs, logTemplateEvidenceCandidateMin)",
		"knownTemplatesFilter(cands, time.Unix(0, toNs), fromNs)",
		"filterNewTemplateFamilies(cands, parseLogTemplates(usableKnownTemplatesMin(known, fromNs, logTemplateEvidenceKnownMin)))",
		"truncTemplate(t.Template, logTemplateEvidenceTemplateMax)",
	} {
		if !strings.Contains(body, w) {
			t.Errorf("çekirdekte yok (dedektör işlevi yeniden kullanılmalı): %s", w)
		}
	}
	if strings.Count(body, "store.ListLogTemplates(") != 2 {
		t.Errorf("okuma bütçesi: tam iki ListLogTemplates çağrısı beklenir")
	}
	ch, err := os.ReadFile("../chstore/log_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	i := strings.Index(string(ch), "func (s *Store) ListLogTemplates(")
	if i < 0 {
		t.Fatal("ListLogTemplates bulunamadı")
	}
	fn := string(ch)[i:]
	fn = fn[:strings.Index(fn, "\n}\n")]
	for _, w := range []string{"f.Limit > 500", "FROM log_templates FINAL", "LIMIT ?", "SETTINGS max_execution_time = 5"} {
		if !strings.Contains(fn, w) {
			t.Errorf("ListLogTemplates sınırı kayboldu: %q", w)
		}
	}
}
