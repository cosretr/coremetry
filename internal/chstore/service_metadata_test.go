package chstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// TestMergeTeams pins the auto-derive ownership rule (v0.8.95 fill, v0.8.100
// rename-propagation): the deriver owns a field while it's empty OR equals the
// value it last auto-wrote (*_team_auto); a human edit (value != auto) pins it.
// When owned the field tracks the derived value (rename propagates); manual
// edits and the other metadata fields are never touched.
func TestMergeTeams(t *testing.T) {
	cases := []struct {
		name                       string
		md                         ServiceMetadata
		derived                    ServiceTeams
		wantOwner, wantSRE         string
		wantOwnerAuto, wantSREAuto string
		wantChanged                bool
	}{
		{"fill both empty", ServiceMetadata{Service: "s"}, ServiceTeams{"ug", "sy"}, "ug", "sy", "ug", "sy", true},
		{"manual owner (no auto) pinned, fill sre", ServiceMetadata{Service: "s", OwnerTeam: "manual"}, ServiceTeams{"ug", "sy"}, "manual", "sy", "", "sy", true},
		{"manual both pinned, no change", ServiceMetadata{Service: "s", OwnerTeam: "mo", SRETeam: "ms"}, ServiceTeams{"ug", "sy"}, "mo", "ms", "", "", false},
		{"derived empty owner only fills sre", ServiceMetadata{Service: "s"}, ServiceTeams{"", "sy"}, "", "sy", "", "sy", true},
		{"derived both empty, no change", ServiceMetadata{Service: "s"}, ServiceTeams{"", ""}, "", "", "", "", false},
		// v0.8.100 — auto-owned field tracks a rename in the span attrs.
		{"auto-owned updates on rename", ServiceMetadata{Service: "s", OwnerTeam: "old", OwnerTeamAuto: "old", SRETeam: "sold", SRETeamAuto: "sold"}, ServiceTeams{"new", "snew"}, "new", "snew", "new", "snew", true},
		{"auto-owned same value, no change", ServiceMetadata{Service: "s", OwnerTeam: "x", OwnerTeamAuto: "x", SRETeam: "y", SRETeamAuto: "y"}, ServiceTeams{"x", "y"}, "x", "y", "x", "y", false},
		// Human edited owner away from its auto value → pinned, deriver backs off.
		{"manual edit (owner != auto) pins, sre still owned", ServiceMetadata{Service: "s", OwnerTeam: "manual", OwnerTeamAuto: "derivedX", SRETeam: "sy", SRETeamAuto: "sy"}, ServiceTeams{"derivedY", "sy2"}, "manual", "sy2", "derivedX", "sy2", true},
	}
	for _, c := range cases {
		got, changed := mergeTeams(c.md, c.derived)
		if got.OwnerTeam != c.wantOwner || got.SRETeam != c.wantSRE || changed != c.wantChanged ||
			got.OwnerTeamAuto != c.wantOwnerAuto || got.SRETeamAuto != c.wantSREAuto {
			t.Errorf("%s: got owner=%q sre=%q ownerAuto=%q sreAuto=%q changed=%v; want %q/%q/%q/%q/%v",
				c.name, got.OwnerTeam, got.SRETeam, got.OwnerTeamAuto, got.SRETeamAuto, changed,
				c.wantOwner, c.wantSRE, c.wantOwnerAuto, c.wantSREAuto, c.wantChanged)
		}
	}

	// Non-team fields must survive the merge (UpsertServiceMetadata is a
	// full-row replace, so a dropped field would clobber a manual edit).
	md := ServiceMetadata{Service: "s", Description: "keep", Repository: "repo", ChatChannel: "chan", RunbookURL: "rb"}
	got, _ := mergeTeams(md, ServiceTeams{"ug", "sy"})
	if got.Description != "keep" || got.Repository != "repo" || got.ChatChannel != "chan" || got.RunbookURL != "rb" {
		t.Errorf("mergeTeams must preserve non-team fields, got %+v", got)
	}
}

// TestMergeNamespace — v0.8.436. Ownership/pin semantics must stay
// byte-identical to mergeTeams: deriver owns empty-or-own fields,
// manual edits pin, renames propagate while owned.
func TestMergeNamespace(t *testing.T) {
	tests := []struct {
		name    string
		md      ServiceMetadata
		ns      string
		want    string
		changed bool
	}{
		{"fills empty", ServiceMetadata{}, "payments", "payments", true},
		{"rename propagates while deriver-owned",
			ServiceMetadata{Namespace: "old", NamespaceAuto: "old"}, "payments", "payments", true},
		{"manual edit pins (value != auto)",
			ServiceMetadata{Namespace: "curated", NamespaceAuto: "old"}, "payments", "curated", false},
		{"same value is a no-op",
			ServiceMetadata{Namespace: "payments", NamespaceAuto: "payments"}, "payments", "payments", false},
		{"empty derive never clears",
			ServiceMetadata{Namespace: "payments", NamespaceAuto: "payments"}, "", "payments", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := mergeNamespace(tc.md, tc.ns)
			if got.Namespace != tc.want || changed != tc.changed {
				t.Fatalf("ns=%q changed=%v, want %q/%v", got.Namespace, changed, tc.want, tc.changed)
			}
			if changed && got.NamespaceAuto != tc.ns {
				t.Fatalf("provenance not re-stamped: %q", got.NamespaceAuto)
			}
		})
	}
}

// TestDeriveNamespaceSQLShape — house bounds + the two attribute
// spellings in preference order (resource scope before span scope).
func TestDeriveNamespaceSQLShape(t *testing.T) {
	for _, frag := range []string{
		"FROM spans",
		"time >= ? AND time <= ?",
		"LIMIT 2000000",
		// v0.9.715 sonrası birleşik SQL: dış LIMIT kombo tablosuna (50000)
		"LIMIT 50000",
		"SETTINGS max_execution_time = 25",
		"has(res_keys, 'service.namespace')",
		"has(res_keys, 'k8s.namespace.name')",
		"has(attr_keys, 'service.namespace')",
		"has(attr_keys, 'k8s.namespace.name')",
	} {
		if !strings.Contains(deriveMetadataAllSQL, frag) {
			t.Errorf("missing %q", frag)
		}
	}
	// resource spelling must be checked BEFORE the span-scope fallback
	// (multiIf order is the preference order).
	if strings.Index(deriveMetadataAllSQL, "has(res_keys, 'service.namespace')") >
		strings.Index(deriveMetadataAllSQL, "has(attr_keys, 'service.namespace')") {
		t.Fatal("resource scope must precede span scope in the multiIf")
	}
}

// TestNamespaceAutoSurvivesUnrelatedEdit — v0.8.438 regression.
// Review-confirmed: putServiceMetadata copied only the team *_auto
// provenance; namespace_auto zeroed on ANY catalog edit, so
// mergeNamespace treated the derived value as a human pin forever.
// The pure invariant pinned here: a row whose Namespace equals its
// (correctly carried) NamespaceAuto stays deriver-owned; the same row
// with a ZEROED auto is treated as pinned — the bug's exact mechanism.
func TestNamespaceAutoSurvivesUnrelatedEdit(t *testing.T) {
	derived := ServiceMetadata{Namespace: "payments", NamespaceAuto: "payments"}
	if got, changed := mergeNamespace(derived, "payments-v2"); !changed || got.Namespace != "payments-v2" {
		t.Fatalf("carried auto must stay deriver-owned: %+v changed=%v", got, changed)
	}
	zeroedAuto := ServiceMetadata{Namespace: "payments", NamespaceAuto: ""}
	if _, changed := mergeNamespace(zeroedAuto, "payments-v2"); changed {
		t.Fatal("zeroed auto reads as a human pin — the handler MUST carry NamespaceAuto forward")
	}
}

// ── HATA ≠ BOŞ (v0.9.1236) ───────────────────────────────────────

// fakeRow / fakeConn — driver.Row + QueryRow'u sahteleyen minimum yüzey.
// driver.Conn GÖMÜLÜ: bu test yalnız QueryRow'u kullanıyor, başka bir
// metoda düşerse nil panic'i testi patlatır (sessizce geçmez).
type fakeRow struct{ err error }

func (r fakeRow) Err() error                { return r.err }
func (r fakeRow) Scan(dest ...any) error    { return r.err }
func (r fakeRow) ScanStruct(dest any) error { return r.err }

type fakeConn struct {
	driver.Conn
	row driver.Row
}

func (c fakeConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return c.row
}

// TestGetServiceMetadataStrictSeparatesErrorFromEmpty — v0.9.1236.
//
// Eskiden Scan'in HER hatası "henüz küratörlenmemiş" (nil, nil) diye
// okunuyordu, yani geçici bir CH arızası ile "satır yok" ayırt
// edilemiyordu. Bunun en pahalı sonucu AI kod bağlamındaydı: okunamayan
// bir depo PİNİ sessizce konvansiyona düşüyor ve YANLIŞ DEPONUN kodu
// kanıt diye gösterilebiliyordu (internal/api/copilot_code.go,
// pinReadDecision).
//
// Bu test BAĞLANMAYI ölçüyor: saf karar fonksiyonunun testi, hata hiç
// yukarı çıkmazsa yeşil kalır ve düzeltme kendini sessizce iptal eder.
func TestGetServiceMetadataStrictSeparatesErrorFromEmpty(t *testing.T) {
	ctx := context.Background()
	transport := errors.New("read: i/o timeout")

	cases := []struct {
		name      string
		scanErr   error
		wantErr   bool
		strictNil bool
	}{
		{name: "satır yok (sentinel)", scanErr: sql.ErrNoRows, strictNil: true},
		{
			name:      "satır yok (sürücü metni)",
			scanErr:   errors.New("sql: no rows in result set"),
			strictNil: true,
		},
		{name: "taşıma hatası", scanErr: transport, wantErr: true, strictNil: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{conn: fakeConn{row: fakeRow{err: tc.scanErr}}}

			md, err := s.GetServiceMetadataStrict(ctx, "svc")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Strict err=%v, hata beklendi mi: %v", err, tc.wantErr)
			}
			if tc.strictNil && md != nil {
				t.Fatalf("Strict md=%+v, nil beklenirdi", md)
			}

			// Yumuşak ikiz ESKİ SÖZLEŞMEDE kalır: onlarca çağıran
			// (chip'ler, bildirim zenginleştirmesi) bu davranışa
			// yaslanıyor ve orada bedeli yalnız boş bir alan.
			md2, err2 := s.GetServiceMetadata(ctx, "svc")
			if err2 != nil || md2 != nil {
				t.Fatalf("yumuşak ikiz değişti: md=%+v err=%v", md2, err2)
			}
		})
	}

	t.Run("boş servis adı sunucuya hiç gitmez", func(t *testing.T) {
		// conn nil: bir sorgu denenirse panic olur.
		s := &Store{}
		if md, err := s.GetServiceMetadataStrict(ctx, ""); md != nil || err != nil {
			t.Fatalf("md=%+v err=%v", md, err)
		}
	})
}
