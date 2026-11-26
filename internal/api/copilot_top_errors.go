package api

// copilot_top_errors.go — v0.10.1256 (operatör: "en yavaş trace'ler tüm
// trace'ler arasında anlamlı değil; takımla ilgili ya da hatalı olanlar, en
// hatalı servisler, sonra onların operasyonları"). İki adımlı akış:
//   "(takımımın) en hatalı servisleri?" → hata sayısına göre ilk 10 servis
//   "X en hatalı operasyonları?"         → o servisin hatalı operasyonları
// İkisi de MV'den (service_summary_5m / operation_summary_5m).

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const topErrorRows = 10

// hasTopErrorSignal — SAF: "en/çok/top" + "hatal" ("en hatalı servisler",
// "en çok hata alan operasyonlar"). Sıralama isteği; düz "hatalar" değil.
func hasTopErrorSignal(toks []string) bool {
	rank := false
	for _, t := range toks {
		if t == "en" || t == "top" {
			rank = true
		}
	}
	if !rank && !tokenHasPrefix(toks, "çok") {
		return false
	}
	return tokenHasPrefix(toks, "hatal", "hata")
}

func hasOperationWord(toks []string) bool {
	return tokenHasPrefix(toks, "operasyon", "işlem", "endpoint", "uç")
}

func hasServiceWord(toks []string) bool { return tokenHasPrefix(toks, "servis", "service") }

// renderTopErrorsTR — SAF.
func renderTopErrorsTR(title string, rows []chstore.ErrorRank, rangeS int64, noun string) string {
	var b strings.Builder
	if len(rows) == 0 {
		fmt.Fprintf(&b, "%s: son %s içinde hata yok.\n", title, fmtAgoTR(rangeS))
		return b.String()
	}
	fmt.Fprintf(&b, "%s (son %s, hata sayısına göre, ilk %d %s):\n", title, fmtAgoTR(rangeS), len(rows), noun)
	for _, r := range rows {
		rate := 0.0
		if r.Spans > 0 {
			rate = float64(r.Errors) / float64(r.Spans) * 100
		}
		fmt.Fprintf(&b, "- %s: %d hata / %d istek (%%%.2f)\n", r.Name, r.Errors, r.Spans, rate)
	}
	return b.String()
}

func (s *Server) guidedTopErrorServicesFleet(ctx context.Context, emit func(string, any), from, to time.Time) (string, string, []chstore.ErrorRank, error) {
	n := emitGuidedStep(emit, "top_error_services", `{"scope":"fleet"}`)
	rows, err := s.store.TopErrorServices(ctx, from, to, nil, topErrorRows)
	ev := ""
	if err == nil {
		ev = renderTopErrorsTR("Filoda en hatalı servisler", rows, int64(to.Sub(from).Seconds()), "servis")
	}
	emitGuidedStepResult(emit, n, "top_error_services", ev, err)
	return ev, "servis hata sıralaması (service_summary_5m)", rows, err
}

func (s *Server) guidedTopErrorOpsBundle(ctx context.Context, emit func(string, any), service string, from, to time.Time) (string, string, error) {
	n := emitGuidedStep(emit, "top_error_operations", `{"service":`+jsonStr(service)+`}`)
	rows, err := s.store.TopErrorOperations(ctx, service, from, to, topErrorRows)
	if err != nil {
		emitGuidedStepResult(emit, n, "top_error_operations", "", err)
		return "", "", err
	}
	ev := renderTopErrorsTR(service+" en hatalı operasyonları", rows, int64(to.Sub(from).Seconds()), "operasyon")
	stateTurnFrom(ctx).noteRows(operationRows(service, errorRankNames(rows))) // v0.10.2473 — "ikincisini aç" (chat_turn_rows.go)
	emitGuidedStepResult(emit, n, "top_error_operations", ev, nil)
	return ev, "operasyon hata sıralaması (operation_summary_5m)", nil
}

// guidedTeamTopErrorsBundle — v0.10.1402: ADI GEÇEN takımın en hatalı
// servisleri ("platform takımının en hatalı servisleri?" — CoSRE Takımım
// kartının çipi). Takım → servis çözümü team_services ile AYNI katalog
// kuralı (mcptools.ReadTeamServiceNames, alias farkındalıklı, 100'lük
// tavan); sıralama my_top_errors ile AYNI okuma (TopErrorServices,
// service_summary_5m). İki takım cevabı ayrışmasın diye ikinci uygulama yok.
func (s *Server) guidedTeamTopErrorsBundle(ctx context.Context, emit func(string, any), route *guidedRoute, from, to time.Time, rangeS int64) (string, string, error) {
	team := route.Team
	if team == "" {
		return "", "", errors.New("team_top_errors: takım adı boş")
	}
	nResolve := emitGuidedStep(emit, "resolve_team_services", `{"team":`+jsonStr(team)+`}`)
	svcs, _, err := mcptools.ReadTeamServiceNames(ctx, s.mcpDeps(), team)
	if err != nil {
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", "", err)
		return "", "", err
	}
	if len(svcs) == 0 {
		ev := fmt.Sprintf("%q takımı hiçbir serviste ownerTeam/sreTeam olarak geçmiyor (Service Catalog). "+
			"Kullanıcıya söyle: katalogda takım ataması yapılmalı ya da başka bir takım adı denemeli.\n", team)
		emitGuidedStepResult(emit, nResolve, "resolve_team_services", ev, nil)
		return ev, fmt.Sprintf("servis kataloğu (takım: %s)", team), nil
	}
	emitGuidedStepResult(emit, nResolve, "resolve_team_services",
		fmt.Sprintf("%q takımına eşleşen servisler (%d):\n- %s\n", team, len(svcs), strings.Join(svcs, "\n- ")), nil)
	n := emitGuidedStep(emit, "top_error_services", fmt.Sprintf(`{"team":%s,"teamServices":%d}`, jsonStr(team), len(svcs)))
	rows, err := s.store.TopErrorServices(ctx, from, to, svcs, topErrorRows)
	if err != nil {
		emitGuidedStepResult(emit, n, "top_error_services", "", err)
		return "", "", err
	}
	ev := renderTopErrorsTR(team+" takımının en hatalı servisleri", rows, rangeS, "servis")
	emitGuidedStepResult(emit, n, "top_error_services", ev, nil)
	route.TeamServices = errorRankNames(rows) // takip çipleri için sıralı
	return ev, fmt.Sprintf("takım: %s — servis hata sıralaması (service_summary_5m)", team), nil
}

// topErrorFollowups — SAF: ilk üç servisin operasyon çipi.
func topErrorFollowups(names []string) []string {
	var out []string
	for i, n := range names {
		if i >= 3 {
			break
		}
		out = append(out, n+" en hatalı operasyonları?")
	}
	return out
}
