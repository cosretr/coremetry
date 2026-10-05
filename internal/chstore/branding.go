package chstore

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
)

// BrandingSettings — admin-customisable strings + logo rendered
// across the public surface (login + the browser tab title) and
// the chrome on logged-in pages. Stored in system_settings under
// the "branding" key as a JSON blob. Empty / zero-value fields
// fall back to the Coremetry defaults so a fresh install reads
// as plain "Coremetry" without an admin filling the form.
//
// Logo is a base64 data URI (capped at ~200KB at the API layer)
// so we don't introduce a separate blob/object-store dependency
// for what's typically a 30KB PNG. The same rendering path serves
// the login page (where the SPA isn't authed yet) and the
// in-app header.
type BrandingSettings struct {
	AppName           string `json:"appName,omitempty"`
	BrowserTitle      string `json:"browserTitle,omitempty"`
	LoginTitle        string `json:"loginTitle,omitempty"`
	LoginSubtitle     string `json:"loginSubtitle,omitempty"`
	SignInButtonLabel string `json:"signInButtonLabel,omitempty"`
	UsernameLabel     string `json:"usernameLabel,omitempty"`
	FooterText        string `json:"footerText,omitempty"`
	// LogoDataURI is a "data:image/png;base64,..." string. Empty
	// → the UI renders the built-in Telescope mark.
	LogoDataURI string `json:"logoDataUri,omitempty"`
	// PrimaryColor overrides the --accent CSS var when set.
	// Optional; empty keeps the bundled theme.
	PrimaryColor string `json:"primaryColor,omitempty"`
	// Language: "en" (default) or "tr". Drives the i18n catalog
	// the SPA uses to render sidebar labels, page titles, common
	// buttons, login strings, and empty/error states.
	Language string `json:"language,omitempty"`
	// OracleGroupLabel — v0.10.1108 (operatör: "Oracleden gelen problemlerde
	// exceptionsta Oracle yazıyor … Teknik Hata gibi mesela"). `ora:` hata
	// tablosu gruplarının kullanıcıya görünen adı (satır rozeti, soluk satır,
	// çip, AI satırı, öncelik gerekçesi). Boş = DefaultOracleGroupLabel.
	// Kırpılır, ≤ OracleGroupLabelMaxRunes (NormalizeBranding).
	OracleGroupLabel string `json:"oracleGroupLabel,omitempty"`
}

// v0.10.1108 — Oracle hata grubu etiketi: varsayılan + uzunluk tavanı (rozet /
// çip tek satır; FE lib/branding.ts aynı sabitleri taşır).
const (
	DefaultOracleGroupLabel  = "Teknik hata"
	OracleGroupLabelMaxRunes = 40
)

// NormalizeBranding — SAF (v0.10.1108): etiketi kırpar ve rune tavanına keser.
// Yazışta (PutBranding) ve okuyuşta (GetBranding) uygulanır — elle yazılmış
// eski bir blob da aynı biçimde okunur.
func NormalizeBranding(b BrandingSettings) BrandingSettings {
	l := strings.TrimSpace(b.OracleGroupLabel)
	if r := []rune(l); len(r) > OracleGroupLabelMaxRunes {
		l = strings.TrimSpace(string(r[:OracleGroupLabelMaxRunes]))
	}
	b.OracleGroupLabel = l
	return b
}

// ResolvedOracleGroupLabel — boş etiket varsayılana düşer.
func (b BrandingSettings) ResolvedOracleGroupLabel() string {
	if l := NormalizeBranding(b).OracleGroupLabel; l != "" {
		return l
	}
	return DefaultOracleGroupLabel
}

// oracleGroupLabel — v0.10.1108 süreç-geneli son okunan etiket. Öncelik
// gerekçesi (api.oraclePriorityAt) satır BAŞINA kurulur; oradan CH okuması
// olmasın diye GetBranding/PutBranding her başarılı okuma/yazmada yayınlar
// (giriş sayfası + SPA açılışı + api'nin 30 sn triyaj yenilemesi hidrate eder).
var oracleGroupLabel atomic.Pointer[string]

// CurrentOracleGroupLabel — hiç okunmamışsa varsayılan.
func CurrentOracleGroupLabel() string {
	if p := oracleGroupLabel.Load(); p != nil {
		return *p
	}
	return DefaultOracleGroupLabel
}

func publishOracleGroupLabel(b BrandingSettings) {
	l := b.ResolvedOracleGroupLabel()
	oracleGroupLabel.Store(&l)
}

// encodeBranding / decodeBranding — SAF blob çevrimi (v0.10.1108); ikisi de
// NormalizeBranding'den geçer. Boş blob = sıfır değer (varsayılanlar).
func encodeBranding(b BrandingSettings) ([]byte, error) {
	return json.Marshal(NormalizeBranding(b))
}

func decodeBranding(raw []byte) (BrandingSettings, error) {
	var b BrandingSettings
	if len(raw) == 0 {
		return b, nil
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, err
	}
	return NormalizeBranding(b), nil
}

const brandingKey = "branding"

// GetBranding returns the saved branding overlay (or an empty
// struct if unset — caller applies defaults). The endpoint that
// serves this is public, since the login page renders the result
// before the operator has a session.
func (s *Store) GetBranding(ctx context.Context) (BrandingSettings, error) {
	raw, err := s.GetSetting(ctx, brandingKey)
	if err != nil {
		return BrandingSettings{}, err
	}
	b, err := decodeBranding(raw)
	if err != nil {
		return b, err
	}
	publishOracleGroupLabel(b)
	return b, nil
}

// PutBranding overwrites the saved overlay. Admin-gated at the
// HTTP layer; the store side is unguarded so the boot-time
// seeder (future) can also call it.
func (s *Store) PutBranding(ctx context.Context, b BrandingSettings) error {
	raw, err := encodeBranding(b)
	if err != nil {
		return err
	}
	if err := s.PutSetting(ctx, brandingKey, raw); err != nil {
		return err
	}
	publishOracleGroupLabel(NormalizeBranding(b))
	return nil
}
