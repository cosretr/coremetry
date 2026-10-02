package anomaly

// v0.10.1030 regresyon testi — "yeni log şablonu" seli. Operatör (prod, ekran
// görüntüsüyle): "Çok fazla problem geliyor." Tek servis için on+
// `log_template_new` satırı; hepsi aynı JSON önekli aynı satır ailesinin
// tikten tike farklı inceltilmiş (farklı kimlikli) hâlleri. Dedektör eskiden
// pencerede doğan HER şablonu çıkarıyordu; artık bilinen bir şablonun
// Drain-ailesi olan adayı bastırır ve tik başına aile başına tek aday çıkarır.

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/templater"
)

const userPrefix = `{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"User {UserName} logged in from {Region} via {Channel}","RenderedMessage":"User`

const orderPrefix = `{"Timestamp":"<*>","Level":"Warning","MessageTemplate":"Order {OrderId} was shipped to {Region} via {Carrier}","RenderedMessage":"Order`

var (
	// Aynı ailenin üç inceltme düzeyi (templater testindeki iki tikin
	// gerçek Drain çıktıları + bir ara düzey).
	userV1 = userPrefix + ` <*> logged in from eu-west via web"}`
	userV2 = userPrefix + ` alice logged in from <*> via <*>`
	userV3 = userPrefix + ` <*> logged in from <*> via web"}`
	// Başka bir aile (aynı JSON öneki, başka mesaj şablonu).
	orderV1 = orderPrefix + ` <*> was shipped to eu-west via carrier-a"}`
	orderV2 = orderPrefix + ` <*> was shipped to <*> via carrier-a"}`
	// Düz metin, bambaşka biçim.
	plainNew = "payments-api circuit breaker opened for upstream <*> after <*> failures"
)

func tplRow(id, tmpl string, firstSeen int64, count uint64, svcs ...string) chstore.LogTemplate {
	if svcs == nil {
		svcs = []string{}
	}
	return chstore.LogTemplate{ID: id, Template: tmpl, FirstSeen: firstSeen, LastSeen: firstSeen + 1, TotalCount: count, Services: svcs}
}

func emittedIDs(in []chstore.LogTemplate) []string {
	out := make([]string, 0, len(in))
	for _, t := range in {
		out = append(out, t.ID)
	}
	return out
}

// filterRaw — saf süzgeç, ayrıştırma dahil (üretimdeki çağrının aynısı).
func filterRaw(cands, known []chstore.LogTemplate) ([]chstore.LogTemplate, familyFilterStats) {
	return filterNewTemplateFamilies(parseLogTemplates(cands), parseLogTemplates(known))
}

// Fikstür geçerliliği: test yalnız yüklemin bu çiftlere verdiği cevaba
// dayanıyor; yüklem değişirse burada açıkça düşsün.
func TestNewTemplateFamilyFixtures(t *testing.T) {
	for _, p := range [][2]string{{userV1, userV2}, {userV1, userV3}, {userV2, userV3}, {orderV1, orderV2}} {
		if !templater.SameTemplateFamily(p[0], p[1]) {
			t.Fatalf("fikstür: aynı aile sayılmalı:\n  %q\n  %q", p[0], p[1])
		}
	}
	for _, p := range [][2]string{{userV1, orderV1}, {userV2, orderV2}, {userV1, plainNew}, {orderV1, plainNew}} {
		if templater.SameTemplateFamily(p[0], p[1]) {
			t.Fatalf("fikstür: farklı aile sayılmalı:\n  %q\n  %q", p[0], p[1])
		}
	}
}

func TestFilterNewTemplateFamilies(t *testing.T) {
	const svc, other = "payments-api", "checkout"
	cases := []struct {
		name           string
		cands, known   []chstore.LogTemplate
		want           []string
		variant, dupes int
	}{
		{
			name:    "bilinen şablonun varyantı bastırılır",
			cands:   []chstore.LogTemplate{tplRow("c-user2", userV2, 200, 5, svc)},
			known:   []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, svc)},
			want:    []string{},
			variant: 1,
		},
		{
			name:  "gerçekten yeni biçim çıkar",
			cands: []chstore.LogTemplate{tplRow("c-order1", orderV1, 200, 5, svc), tplRow("c-plain", plainNew, 210, 3, svc)},
			known: []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, svc)},
			want:  []string{"c-order1", "c-plain"},
		},
		{
			name: "aynı tikte aynı aileden birkaç aday → tek (en erken doğan)",
			cands: []chstore.LogTemplate{
				tplRow("c-user2", userV2, 300, 50, svc),
				tplRow("c-user1", userV1, 100, 3, svc),
				tplRow("c-user3", userV3, 200, 10, svc),
			},
			want:  []string{"c-user1"},
			dupes: 2,
		},
		{
			// TotalCount tik başına üzerine yazılır → temsilci seçimine girmez.
			name: "eşit first_seen → ID (TotalCount sıralamaya girmez)",
			cands: []chstore.LogTemplate{
				tplRow("c-b", userV1, 100, 5, svc),
				tplRow("c-c", userV2, 100, 90, svc),
				tplRow("c-a", userV3, 100, 3, svc),
			},
			want:  []string{"c-a"},
			dupes: 2,
		},
		{
			name:  "farklı servisin bilineni bastırmaz",
			cands: []chstore.LogTemplate{tplRow("c-user2", userV2, 200, 5, svc)},
			known: []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, other)},
			want:  []string{"c-user2"},
		},
		{
			name: "aynı tikte farklı servislerin aynı ailesi ikisi de çıkar",
			cands: []chstore.LogTemplate{
				tplRow("c-user1", userV1, 100, 5, svc),
				tplRow("c-user2", userV2, 110, 5, other),
			},
			want: []string{"c-user1", "c-user2"},
		},
		{
			name:    "ortak servis paylaşan bilinen bastırır",
			cands:   []chstore.LogTemplate{tplRow("c-user2", userV2, 200, 5, svc)},
			known:   []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, other, svc)},
			want:    []string{},
			variant: 1,
		},
		{
			name: "aynı tikte ortak servis paylaşan iki aday → tek",
			cands: []chstore.LogTemplate{
				tplRow("c-user1", userV1, 100, 5, svc, other),
				tplRow("c-user2", userV2, 110, 5, other),
			},
			want:  []string{"c-user1"},
			dupes: 1,
		},
		{
			name:    "servissiz aday tüm bilinenlerle karşılaştırılır",
			cands:   []chstore.LogTemplate{tplRow("c-user2", userV2, 200, 5)},
			known:   []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, other)},
			want:    []string{},
			variant: 1,
		},
		{
			name:  "servissiz bilinen servisli adayı bastırmaz",
			cands: []chstore.LogTemplate{tplRow("c-user2", userV2, 200, 5, svc)},
			known: []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40)},
			want:  []string{"c-user2"},
		},
		{
			name: "bilinen liste boş → her aile bir kez",
			cands: []chstore.LogTemplate{
				tplRow("c-user2", userV2, 120, 5, svc),
				tplRow("c-order2", orderV2, 130, 5, svc),
				tplRow("c-user1", userV1, 100, 5, svc),
				tplRow("c-order1", orderV1, 105, 5, svc),
				tplRow("c-plain", plainNew, 140, 5, svc),
			},
			want:  []string{"c-user1", "c-order1", "c-plain"},
			dupes: 2,
		},
		{
			name:  "depo adayın kendisini bilinen döndürürse aday kendini bastırmaz",
			cands: []chstore.LogTemplate{tplRow("c-user1", userV1, 200, 5, svc)},
			known: []chstore.LogTemplate{tplRow("c-user1", userV1, 200, 5, svc)},
			want:  []string{"c-user1"},
		},
		{
			name: "aynı ID iki kez aday → tek",
			cands: []chstore.LogTemplate{
				tplRow("c-user1", userV1, 100, 5, svc),
				tplRow("c-user1", userV1, 100, 5, svc),
			},
			want:  []string{"c-user1"},
			dupes: 1,
		},
		{"aday yok", nil, []chstore.LogTemplate{tplRow("k-user1", userV1, 10, 40, svc)}, []string{}, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emit, st := filterRaw(tc.cands, tc.known)
			if got := emittedIDs(emit); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("çıkan = %v, want %v", got, tc.want)
			}
			if st.VariantOfKnown != tc.variant || st.SameTickDup != tc.dupes {
				t.Errorf("sayılar: variant=%d dup=%d, want %d/%d", st.VariantOfKnown, st.SameTickDup, tc.variant, tc.dupes)
			}
			if st.Candidates != len(tc.cands) || st.Known != len(tc.known) || st.Emitted != len(emit) ||
				st.Emitted+st.VariantOfKnown+st.SameTickDup != st.Candidates {
				t.Errorf("özet tutarsız: %+v", st)
			}
		})
	}
}

// TestFilterNewTemplateFamilies_ShuffleDeterministic — aynı küme her sırada
// aynı çıktıyı versin (iki kayıtçı tiki, iki pod aynı satırları açsın).
func TestFilterNewTemplateFamilies_ShuffleDeterministic(t *testing.T) {
	const svc, other = "payments-api", "checkout"
	cands := []chstore.LogTemplate{
		tplRow("c-user1", userV1, 100, 5, svc),
		tplRow("c-user2", userV2, 100, 5, svc),
		tplRow("c-user3", userV3, 90, 2, svc),
		tplRow("c-user1o", userV1, 150, 8, other),
		tplRow("c-order1", orderV1, 120, 30, svc),
		tplRow("c-order2", orderV2, 120, 30, svc),
		tplRow("c-plain", plainNew, 130, 4),
		tplRow("c-plain2", plainNew, 125, 4, other),
	}
	known := []chstore.LogTemplate{
		tplRow("k-order", orderV2, 10, 40, other),
		tplRow("k-user", userV2, 20, 40, "inventory", other),
	}
	wantEmit, wantSt := filterRaw(cands, known)
	if len(wantEmit) == 0 || len(wantEmit) == len(cands) || wantSt.VariantOfKnown == 0 || wantSt.SameTickDup == 0 {
		t.Fatalf("kurgu hem iki bastırma türünü hem çıkışı içermeli: %v %+v", emittedIDs(wantEmit), wantSt)
	}
	r := rand.New(rand.NewSource(1030))
	for i := 0; i < 200; i++ {
		c := append([]chstore.LogTemplate(nil), cands...)
		k := append([]chstore.LogTemplate(nil), known...)
		r.Shuffle(len(c), func(a, b int) { c[a], c[b] = c[b], c[a] })
		r.Shuffle(len(k), func(a, b int) { k[a], k[b] = k[b], k[a] })
		emit, st := filterRaw(c, k)
		if !reflect.DeepEqual(emittedIDs(emit), emittedIDs(wantEmit)) || st != wantSt {
			t.Fatalf("karıştırma %d farklı sonuç: %v %+v, want %v %+v", i, emittedIDs(emit), st, emittedIDs(wantEmit), wantSt)
		}
	}
	// Girdi dilimi yerinde sıralanmamalı (çağıranın sırası korunur).
	if cands[0].ID != "c-user1" || cands[7].ID != "c-plain2" {
		t.Errorf("girdi dilimi yerinde değişti: %v", emittedIDs(cands))
	}
}

// TestCandidateKnownComplement — v0.10.1030 (gözden geçirme F1): aday ve
// bilinen kümeleri AYNI sinceNs ile kurulur ve tam tümleyendir. Sınırdaki
// satır (first_seen == sinceNs) adaydır, bilinen DEĞİL; blip (sayı < 3)
// ikisinde de yok. Tam saniye OLMAYAN `now` ile (bind ns kesin).
func TestCandidateKnownComplement(t *testing.T) {
	now := time.Unix(1_790_000_000, 987_654_321)
	since := now.Add(-10 * time.Minute).UnixNano()
	edge := tplRow("edge", userV1, since, 5, "payments-api")
	before := tplRow("before", userV1, since-1, 5, "payments-api")
	blipBefore := tplRow("blip", userV1, since-1, 2, "payments-api")

	if got := emittedIDs(newTemplateCandidates([]chstore.LogTemplate{edge, before, blipBefore}, since)); !reflect.DeepEqual(got, []string{"edge"}) {
		t.Errorf("adaylar = %v, want [edge]", got)
	}
	if got := emittedIDs(usableKnownTemplates([]chstore.LogTemplate{edge, before, blipBefore}, since)); !reflect.DeepEqual(got, []string{"before"}) {
		t.Errorf("bilinenler = %v, want [before]", got)
	}

	cf := candidateTemplatesFilter(since)
	kf := knownTemplatesFilter(parseLogTemplates([]chstore.LogTemplate{edge}), now, since)
	if cf.FirstSeenSinceNs != since || kf.FirstSeenBeforeNs != since || since%int64(time.Second) == 0 {
		t.Errorf("sınırlar aynı ns değeri olmalı: aday %d, bilinen %d, since %d", cf.FirstSeenSinceNs, kf.FirstSeenBeforeNs, since)
	}
	// F1(c): aday okuması first_seen ile, sayı tabanı SQL'de, en erken önce,
	// okuyucunun azami tavanı; last_seen süzgeci YOK.
	wantCand := chstore.ListLogTemplatesFilter{FirstSeenSinceNs: since, MinTotalCount: 3, SortBy: "first_seen_asc", Limit: 500}
	if !reflect.DeepEqual(cf, wantCand) {
		t.Errorf("aday süzgeci:\n  got  %+v\n  want %+v", cf, wantCand)
	}
}

func TestKnownTemplatesFilterScope(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	since := now.Add(-10 * time.Minute).UnixNano()
	// Hepsi servisli: yalnız hasAny, servissiz satırlar hariç; belirteç
	// sayıları adaylarınki (farklı, sıralı).
	f := knownTemplatesFilter(parseLogTemplates([]chstore.LogTemplate{
		tplRow("a", userV1, since, 3, "payments-api", "checkout"),
		tplRow("b", plainNew, since, 3, "checkout"),
		tplRow("c", orderV1, since, 3, "payments-api"),
	}), now, since)
	userN := templater.ParseTemplate(userV1).TokenCount()
	plainN := templater.ParseTemplate(plainNew).TokenCount()
	if !reflect.DeepEqual(f.AnyServices, []string{"checkout", "payments-api"}) || f.IncludeServiceless {
		t.Errorf("servis kapsamı = %v / %v", f.AnyServices, f.IncludeServiceless)
	}
	if !reflect.DeepEqual(f.TokenCounts, []int{plainN, userN}) {
		t.Errorf("belirteç sayıları = %v, want [%d %d]", f.TokenCounts, plainN, userN)
	}
	if f.MinTotalCount != 3 || f.FirstSeenBeforeNs != since || f.SortBy != "last_seen" || f.Limit != 500 ||
		f.SinceNs != now.Add(-7*24*time.Hour).UnixNano() || f.FirstSeenSinceNs != 0 {
		t.Errorf("bilinen süzgeci: %+v", f)
	}
	// Servissiz aday varsa servis süzgeci DÜŞMEZ; servissiz satırlar eklenir.
	f = knownTemplatesFilter(parseLogTemplates([]chstore.LogTemplate{
		tplRow("a", userV1, since, 3, "payments-api"),
		tplRow("b", userV2, since, 3),
	}), now, since)
	if !reflect.DeepEqual(f.AnyServices, []string{"payments-api"}) || !f.IncludeServiceless {
		t.Errorf("servissiz adayla kapsam = %v / %v", f.AnyServices, f.IncludeServiceless)
	}
	// Yalnız servissiz adaylar: yalnız servissiz satırlar.
	f = knownTemplatesFilter(parseLogTemplates([]chstore.LogTemplate{tplRow("b", userV2, since, 3)}), now, since)
	if f.AnyServices != nil || !f.IncludeServiceless {
		t.Errorf("yalnız servissiz kapsam = %v / %v", f.AnyServices, f.IncludeServiceless)
	}
}

// TestEmittedService — v0.10.1030 (F5): olay servisi varış sırasından
// bağımsız (sözlükte en küçük); Services[0] tikten tike değişip ikinci satır
// açıyordu.
func TestEmittedService(t *testing.T) {
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"payments-api"}, "payments-api"},
		{[]string{"payments-api", "checkout"}, "checkout"},
		{[]string{"checkout", "payments-api"}, "checkout"},
		{[]string{"", "inventory", "checkout"}, "checkout"},
	} {
		if got := emittedService(tc.in); got != tc.want {
			t.Errorf("emittedService(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
	a := logTemplateAnomalyFrom(tplRow("x", userV1, 1, 3, "payments-api", "checkout"))
	b := logTemplateAnomalyFrom(tplRow("x", userV1, 1, 3, "checkout", "payments-api"))
	if a.Service != "checkout" || a.Service != b.Service {
		t.Errorf("servis sıraya bağlı: %q / %q", a.Service, b.Service)
	}
	if chstore.FingerprintAnomaly("log_template_new", a.TemplateID, a.Service) != chstore.FingerprintAnomaly("log_template_new", b.TemplateID, b.Service) {
		t.Error("aynı şablon iki parmak izi üretti")
	}
}

// fakeTemplateLister — çağrı sırasıyla yanıt/hata veren sahte depo.
type fakeTemplateLister struct {
	calls []chstore.ListLogTemplatesFilter
	resp  [][]chstore.LogTemplate
	errs  []error
}

func (f *fakeTemplateLister) ListLogTemplates(_ context.Context, flt chstore.ListLogTemplatesFilter) ([]chstore.LogTemplate, error) {
	i := len(f.calls)
	f.calls = append(f.calls, flt)
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	if i < len(f.resp) {
		return f.resp[i], nil
	}
	return nil, nil
}

func TestDetectNewLogTemplates_FamilyAware(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	window := 10 * time.Minute
	since := now.Add(-window).UnixNano()
	born := since + int64(time.Minute)
	fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{
		{ // aday okuması
			tplRow("c-user2", userV2, born, 7, "payments-api"),
			tplRow("c-order1", orderV1, born, 4, "payments-api", "checkout"),
			tplRow("old", userV1, since-int64(time.Hour), 90, "payments-api"), // pencereden önce doğmuş → aday değil (kemer)
			tplRow("blip", plainNew, born, 2, "payments-api"),                 // < 3 → aday değil (kemer)
		},
		{ // bilinen okuması
			tplRow("old", userV1, since-int64(time.Hour), 90, "payments-api"),
		},
	}}
	got, err := detectNewLogTemplates(context.Background(), fake, window, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].TemplateID != "c-order1" || got[0].Service != "checkout" {
		t.Fatalf("çıkan = %+v, want yalnız c-order1 (servis checkout)", got)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("tik başına iki okuma beklenir, %d", len(fake.calls))
	}
	if !reflect.DeepEqual(fake.calls[0], candidateTemplatesFilter(since)) {
		t.Errorf("aday okuması süzgeci: %+v", fake.calls[0])
	}
	want := chstore.ListLogTemplatesFilter{
		SinceNs:           now.Add(-7 * 24 * time.Hour).UnixNano(),
		FirstSeenBeforeNs: since,
		MinTotalCount:     3,
		SortBy:            "last_seen",
		Limit:             500,
		AnyServices:       []string{"checkout", "payments-api"},
		TokenCounts:       []int{15},
	}
	if !reflect.DeepEqual(fake.calls[1], want) {
		t.Errorf("bilinen okuması süzgeci:\n  got  %+v\n  want %+v", fake.calls[1], want)
	}
}

// TestDetectNewLogTemplates_BlipIsNotKnown — v0.10.1030 (gözden geçirme M1):
// bir-iki kez örneklenmiş (sayı < 3, aday olmamış) bir şablon on dakika sonra
// "bilinen" olup kendi inceltilmiş, gerçek ≥3 şablonunu 7 gün bastırmamalı —
// v0.9.47'nin "gerçek yeni hata 3'ü geçer ve sonraki turda yakalanır"
// sözü. Depo blip'i döndürse bile (kemer) aday ÇIKAR; sayısı ≥ 3 olan
// bilinen ise hâlâ bastırır.
func TestDetectNewLogTemplates_BlipIsNotKnown(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	since := now.Add(-10 * time.Minute).UnixNano()
	born := since + int64(time.Minute)
	run := func(knownCount uint64) []LogTemplateAnomaly {
		t.Helper()
		fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{
			{tplRow("c-user1", userV1, born, 6, "payments-api")},
			{tplRow("k-user2", userV2, since-int64(time.Hour), knownCount, "payments-api")},
		}}
		got, err := detectNewLogTemplates(context.Background(), fake, 10*time.Minute, now)
		if err != nil {
			t.Fatal(err)
		}
		if fake.calls[1].MinTotalCount != 3 {
			t.Errorf("bilinen okuması sayı tabanını SQL'e indirmiyor: %+v", fake.calls[1])
		}
		return got
	}
	if got := run(1); len(got) != 1 || got[0].TemplateID != "c-user1" {
		t.Errorf("blip bilinen sayıldı, gerçek aday bastırıldı: %+v", got)
	}
	if got := run(3); len(got) != 0 {
		t.Errorf("sayısı ≥3 bilinen varyantı bastırmadı: %+v", got)
	}
}

// TestDetectNewLogTemplates_KnownReadFailsClosed — bilinen okuması düşerse
// HİÇBİR aday çıkmaz (hepsini çıkarmak selin kendisi) ve hata kayıtçıya
// döner (tik başına bir log satırı; tik düşmez).
func TestDetectNewLogTemplates_KnownReadFailsClosed(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	born := now.Add(-time.Minute).UnixNano()
	fake := &fakeTemplateLister{
		resp: [][]chstore.LogTemplate{{
			tplRow("c-user2", userV2, born, 7, "payments-api"),
			tplRow("c-order1", orderV1, born, 4, "payments-api"),
		}},
		errs: []error{nil, errors.New("code: 159, timeout exceeded")},
	}
	got, err := detectNewLogTemplates(context.Background(), fake, 10*time.Minute, now)
	if err == nil || !strings.Contains(err.Error(), "fail-closed") {
		t.Errorf("hata dönmeli ve fail-closed demeli: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("bilinen okuması düşünce %d aday çıktı — sel geri gelir", len(got))
	}
}

// TestDetectNewLogTemplates_NoCandidateNoKnownRead — aday yoksa ikinci okuma
// yapılmaz; çıktı boş ama nil değil (eski sözleşme).
func TestDetectNewLogTemplates_NoCandidateNoKnownRead(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	fake := &fakeTemplateLister{resp: [][]chstore.LogTemplate{{
		tplRow("old", userV1, now.Add(-time.Hour).UnixNano(), 90, "payments-api"),
		tplRow("blip", plainNew, now.Add(-time.Minute).UnixNano(), 1, "payments-api"),
	}}}
	got, err := detectNewLogTemplates(context.Background(), fake, 10*time.Minute, now)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
	if len(fake.calls) != 1 {
		t.Errorf("aday yokken bilinen okuması yapıldı: %d çağrı", len(fake.calls))
	}
}

// TestDetectNewLogTemplatesSourcePin — kaynak pini: dışa açık giriş saf
// çekirdeğe gider, çekirdek aile süzgecini ÇAĞIRIR ve bilinen okumasının hata
// dalı adayları değil nil döndürür. Süzgeç silinirse ya da hata dalı
// "hepsini çıkar"a dönerse derleme/test sessiz kalırdı.
func TestDetectNewLogTemplatesSourcePin(t *testing.T) {
	src, err := os.ReadFile("log_templates.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	i := strings.Index(body, "func DetectNewLogTemplates(")
	if i < 0 {
		t.Fatal("DetectNewLogTemplates bulunamadı")
	}
	exported := body[i:]
	exported = exported[:strings.Index(exported, "\n}\n")]
	if !strings.Contains(exported, "return detectNewLogTemplates(ctx, store, window, time.Now())") {
		t.Error("DetectNewLogTemplates saf çekirdeğe gitmiyor")
	}
	j := strings.Index(body, "func detectNewLogTemplates(")
	if j < 0 {
		t.Fatal("detectNewLogTemplates bulunamadı")
	}
	core := body[j:]
	core = core[:strings.Index(core, "\n}\n")]
	for _, w := range []string{
		"store.ListLogTemplates(ctx, candidateTemplatesFilter(sinceNs))",
		"filterNewTemplateFamilies(cands, parseLogTemplates(usableKnownTemplates(known, sinceNs)))",
	} {
		if !strings.Contains(core, w) {
			t.Errorf("çekirdekte yok: %s", w)
		}
	}
	k := strings.Index(core, "known, err := store.ListLogTemplates(ctx, knownTemplatesFilter(")
	if k < 0 {
		t.Fatal("bilinen okuması bulunamadı")
	}
	errBranch := core[k:]
	errBranch = errBranch[:strings.Index(errBranch, "\n\t}\n")]
	if !strings.Contains(errBranch, "if err != nil {") || !strings.Contains(errBranch, "return nil, fmt.Errorf(") {
		t.Error("bilinen okumasının hata dalı nil döndürmüyor (fail-closed değil)")
	}
	// Kayıtçı hâlâ bu dedektörü çağırıyor.
	rec, err := os.ReadFile("recorder.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rec), "DetectNewLogTemplates(ctx, r.store, 2*r.window)") {
		t.Error("recorder DetectNewLogTemplates'i çağırmıyor")
	}
}
