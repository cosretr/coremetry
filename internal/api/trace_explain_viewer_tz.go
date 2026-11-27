package api

// trace_explain_viewer_tz.go — v0.10.2483 (operatör onaylı): anlatım
// biçiminde LOGLAR bloğunun saatleri İZLEYİCİNİN diliminde. Öncesinde model
// girdisi UTC taşıyordu, UI (Loglar sekmesi / Window çipi, FE
// formatTraceClock) yerel saati gösteriyordu: KARAR'da alıntılanan saat
// ekrandakiyle tutmuyordu.
//
// Merdiven: istemcinin gönderdiği dilim (sohbet bağlamı tz/tzOffsetMin ya da
// Explain gövdesi/sorgusu → chatLocationNamed) > uygulamanın gösterim dilimi
// (tzdefault, COREMETRY_TZ). Bölümlü biçim UTC kalır (bayt bayt).

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/tzdefault"
)

type viewerLocKey struct{}
type traceLogLocKey struct{}

// withViewerLoc — isteğin izleyici dilimi (nil = yok).
func withViewerLoc(ctx context.Context, loc *time.Location) context.Context {
	if loc == nil {
		return ctx
	}
	return context.WithValue(ctx, viewerLocKey{}, loc)
}

func viewerLocFrom(ctx context.Context) *time.Location {
	loc, _ := ctx.Value(viewerLocKey{}).(*time.Location)
	return loc
}

// traceLogLocFor — SAF: LOGLAR saatlerinin dilimi. Bölümlü → nil (UTC);
// anlatım → izleyici, yoksa uygulamanın gösterim dilimi.
func traceLogLocFor(narrative bool, viewer *time.Location) *time.Location {
	if !narrative {
		return nil
	}
	if viewer != nil {
		return viewer
	}
	return tzdefault.Location()
}

// withTraceLogLoc / traceLogLocFrom — kurucunun seçtiği dilim log okumasına.
func withTraceLogLoc(ctx context.Context, loc *time.Location) context.Context {
	return context.WithValue(ctx, traceLogLocKey{}, loc)
}

func traceLogLocFrom(ctx context.Context) *time.Location {
	loc, _ := ctx.Value(traceLogLocKey{}).(*time.Location)
	return loc
}

// traceLogZoneLabel — LOGLAR künyesindeki dilim adı. UTC → "UTC" (bölümlü
// künye bayt bayt); diğerleri adıyla + ekranla aynı olduğu notu.
func traceLogZoneLabel(loc *time.Location) string {
	if loc == nil || loc == time.UTC || loc.String() == "UTC" {
		return "UTC"
	}
	return loc.String() + ", ekrandaki saatle aynı"
}
