package ml

// v0.10.1816 — servis profili sınıflandırıcısı (audit §4.4): sıralı kurallar
// (batch → az çağrılan → mesai → 7/24), ilk tutan; satırsız servis
// bilinmiyor; değişim iki ardışık haftalık değerlendirme ister; elle sabitlenen
// ezilmez.

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

var profileEnd = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC) // pazartesi

// callsWeeks — son `weeks` hafta, gün d (0=pzt) saat h → çağrı; 0 → satır YOK.
func callsWeeks(service string, weeks int, f func(d, h int) uint32) []chstore.MLBaselineObs {
	var out []chstore.MLBaselineObs
	for w := 1; w <= weeks; w++ {
		ws := profileEnd.AddDate(0, 0, -7*w)
		for d := 0; d < 7; d++ {
			for h := 0; h < 24; h++ {
				c := f(d, h)
				if c == 0 {
					continue
				}
				out = append(out, chstore.MLBaselineObs{Signal: "trace", Service: service, Part: baselineCallsPart, Slot: uint8(d*24 + h),
					WeekStart: ws, Class: "normal", Calls: c, Buckets: 12})
			}
		}
	}
	return out
}

func isBatchSuffix(s string) bool { return strings.HasSuffix(s, "-batch") }

func TestClassifyProfile(t *testing.T) {
	biz := func(d, h int) uint32 {
		switch {
		case d >= 5:
			return 50
		case h >= 8 && h <= 17:
			return 10000
		}
		return 100
	}
	cases := []struct {
		name, service string
		obs           []chstore.MLBaselineObs
		want          string
	}{
		{"7/24", "orders-api", callsWeeks("orders-api", 4, func(int, int) uint32 { return 5000 }), ProfileAlwaysOn},
		{"mesai/şube", "branch-teller", callsWeeks("branch-teller", 4, biz), ProfileBusinessHours},
		{"az çağrılan: günlük medyan < 2000", "payments-mobile", callsWeeks("payments-mobile", 4, func(int, int) uint32 { return 50 }), ProfileLowTraffic},
		{"az çağrılan: mesaide boş saat > %50", "payments-mobile", callsWeeks("payments-mobile", 4, func(d, h int) uint32 {
			if d < 5 && h >= 8 && h <= 11 {
				return 20000
			}
			return 0
		}), ProfileLowTraffic},
		{"batch adı ilk kural", "orders-batch", callsWeeks("orders-batch", 4, func(int, int) uint32 { return 5000 }), ProfileBatch},
		{"satır yok → bilinmiyor", "orders-api", nil, ProfileUnknown},
		{"pencere dışı satır sayılmaz", "orders-api", func() []chstore.MLBaselineObs {
			o := callsWeeks("orders-api", 1, func(int, int) uint32 { return 5000 })
			for i := range o {
				o[i].WeekStart = profileEnd // bu hafta: pencere [end−4h, end) dışında
			}
			return o
		}(), ProfileUnknown},
		// mesai profili ama hafta sonu da yoğun → 7/24 (iki oranın İKİSİ şart)
		{"hafta sonu yoğun mesai dışı sessiz", "branch-teller", callsWeeks("branch-teller", 4, func(d, h int) uint32 {
			if h >= 8 && h <= 17 {
				return 10000
			}
			return 100
		}), ProfileAlwaysOn},
	}
	for _, c := range cases {
		got, ev := ClassifyProfile(c.service, c.obs, profileEnd, isBatchSuffix)
		if got != c.want {
			t.Errorf("%s: %s, want %s (kanıt %+v)", c.name, got, c.want, ev)
		}
	}
	// kanıt: mesai örneğinde oranlar tanımlı ve eşik altında
	_, ev := ClassifyProfile("branch-teller", callsWeeks("branch-teller", 4, biz), profileEnd, nil)
	if ev.OffOnRatio == nil || *ev.OffOnRatio >= profileOffOnMax || ev.WeekendRatio == nil || *ev.WeekendRatio >= profileWeekendMax || ev.Weeks != 4 || ev.EmptyBizShare != 0 {
		t.Fatalf("kanıt %+v", ev)
	}
}

func TestProfileBusinessHour(t *testing.T) {
	for h, want := range map[int]bool{7: false, 8: true, 12: true, 17: true, 18: false} {
		if profileBusinessHour(h) != want {
			t.Errorf("saat %d", h)
		}
	}
}

func TestProfileDecisionHysteresis(t *testing.T) {
	cases := []struct {
		name            string
		cur             string
		pinned          bool
		now, prev, want string
		write           bool
	}{
		{"tek hafta farklı: değişmez", ProfileAlwaysOn, false, ProfileBusinessHours, ProfileAlwaysOn, ProfileAlwaysOn, false},
		{"iki ardışık aynı: değişir", ProfileAlwaysOn, false, ProfileBusinessHours, ProfileBusinessHours, ProfileBusinessHours, true},
		{"ilk atama da iki hafta ister", "", false, ProfileLowTraffic, ProfileUnknown, "", false},
		{"ilk atama iki hafta", "", false, ProfileLowTraffic, ProfileLowTraffic, ProfileLowTraffic, true},
		{"kayıtsızken bilinmiyor yazılmaz", "", false, ProfileUnknown, ProfileUnknown, "", false},
		{"aynı sınıf: yazım yok", ProfileBatch, false, ProfileBatch, ProfileBatch, ProfileBatch, false},
		{"sabitlenen ezilmez", ProfileAlwaysOn, true, ProfileLowTraffic, ProfileLowTraffic, ProfileAlwaysOn, false},
	}
	for _, c := range cases {
		got, w := profileDecision(c.cur, c.pinned, c.now, c.prev)
		if got != c.want || w != c.write {
			t.Errorf("%s: %q write=%v", c.name, got, w)
		}
	}
}

type fakeProfileStore struct {
	obs     map[string][]chstore.MLBaselineObs
	latest  []chstore.MLLearnedRow
	written []chstore.MLLearnedRow
}

func (f *fakeProfileStore) MLProfileCalls(_ context.Context, from, to time.Time, slice, slices int, fn func(string, []chstore.MLBaselineObs) error) error {
	if slice != 0 { // tek dilim yeter: dilimleme CH'de
		return nil
	}
	names := make([]string, 0, len(f.obs))
	for k := range f.obs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		var in []chstore.MLBaselineObs
		for _, o := range f.obs[n] {
			if !o.WeekStart.Before(from) && o.WeekStart.Before(to) {
				in = append(in, o)
			}
		}
		if err := fn(n, in); err != nil {
			return err
		}
	}
	return nil
}
func (f *fakeProfileStore) MLLearnedLatest(context.Context, chstore.MLLearnedQuery) ([]chstore.MLLearnedRow, int, error) {
	return f.latest, len(f.latest), nil
}
func (f *fakeProfileStore) InsertMLLearned(_ context.Context, rows []chstore.MLLearnedRow) error {
	f.written = append(f.written, rows...)
	return nil
}

func TestRunProfilesWritesOnlyChanges(t *testing.T) {
	always := func(int, int) uint32 { return 5000 }
	now := time.Date(2026, 11, 3, 4, 0, 0, 0, time.UTC)
	pinned := PinProfileRow("branch-teller", chstore.MLLearnedRow{}, false, ProfileBusinessHours, "şube servisi", "ops@example.com", now.Add(-time.Hour))
	same := profileRow("orders-api", 0, ProfileParams{Profile: ProfileAlwaysOn, Auto: ProfileAlwaysOn}, "", profileAuthorAuto, now.Add(-time.Hour))
	st := &fakeProfileStore{
		obs: map[string][]chstore.MLBaselineObs{
			"orders-api":      callsWeeks("orders-api", 5, always),      // zaten 7/24 → yazım yok
			"branch-teller":   callsWeeks("branch-teller", 5, always),   // sabitli → ezilmez
			"payments-mobile": callsWeeks("payments-mobile", 5, always), // kayıtsız, iki hafta 7/24 → yazılır
			"orders-batch":    callsWeeks("orders-batch", 1, always),    // yalnız son hafta: önceki hafta bilinmiyor → bekler
		},
		latest: []chstore.MLLearnedRow{same, pinned},
	}
	res, err := runProfiles(context.Background(), st, profileEnd, isBatchSuffix, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Services != 4 || res.Changed != 1 || len(st.written) != 1 {
		t.Fatalf("res=%+v written=%+v", res, st.written)
	}
	w := ServiceProfileFromRow(st.written[0])
	if w.Service != "payments-mobile" || w.Profile != ProfileAlwaysOn || w.Pinned || w.Source != "auto" || w.Evidence == nil || st.written[0].CreatedBy != profileAuthorAuto {
		t.Fatalf("yazılan %+v", w)
	}
}

func TestPinUnpinProfileRows(t *testing.T) {
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	auto := profileRow("orders-api", 0, ProfileParams{Profile: ProfileLowTraffic, Auto: ProfileLowTraffic}, `{"dailyMedianCalls":1200}`, profileAuthorAuto, now.Add(-24*time.Hour))
	pin := PinProfileRow("orders-api", auto, true, ProfileAlwaysOn, "gece işi yok", "ops@example.com", now)
	p := ServiceProfileFromRow(pin)
	if p.Profile != ProfileAlwaysOn || !p.Pinned || p.Source != "manual" || p.By != "ops@example.com" || p.Auto != ProfileLowTraffic || p.Reason != "gece işi yok" || pin.Rev <= auto.Rev || p.Evidence == nil {
		t.Fatalf("sabitleme %+v rev=%d", p, pin.Rev)
	}
	un := ServiceProfileFromRow(UnpinProfileRow("orders-api", pin, "ops@example.com", now.Add(time.Minute)))
	if un.Profile != ProfileLowTraffic || un.Pinned || un.Source != "auto" {
		t.Fatalf("kaldırınca son otomatik karara döner: %+v", un)
	}
	// otomatik kararı olmayan servis: kaldırınca bilinmiyor
	fresh := PinProfileRow("branch-teller", chstore.MLLearnedRow{}, false, ProfileBatch, "x", "ops@example.com", now)
	if un := ServiceProfileFromRow(UnpinProfileRow("branch-teller", fresh, "ops@example.com", now)); un.Profile != ProfileUnknown {
		t.Fatalf("kayıtsız servis: %+v", un)
	}
	if got := ServiceProfileFromRow(chstore.MLLearnedRow{Key: "x"}); got.Profile != ProfileUnknown {
		t.Fatalf("boş satır bilinmiyor olmalı: %+v", got)
	}
}

func TestProfileDueWeek(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		last, done string
		due        bool
		week       string
	}{
		{"", "", false, ""},
		{"2026-11-01", "", true, "2026-10-26"},           // pazar işlendi: o haftanın koşusu (önceki 4 hafta)
		{"2026-11-02", "2026-10-26", true, "2026-11-02"}, // pazartesi işlendi: yeni hafta
		{"2026-11-04", "2026-11-02", false, ""},          // bu hafta yapıldı
	}
	for _, c := range cases {
		w, due := profileDueWeek(c.last, c.done, loc)
		if due != c.due || (due && w.Format("2006-01-02") != c.week) {
			t.Errorf("%q/%q: %v %v", c.last, c.done, w, due)
		}
	}
}
