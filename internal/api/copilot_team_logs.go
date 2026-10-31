package api

// copilot_team_logs.go — v0.10.1473 (operatör 2026-10-23: "Elastic loglarında
// da takım bilgisi var; 'takımımın servisleri nasıl' derken logları da buna
// göre inceleyebilir, ve Elastic'e çok yük getirmeden son logları kontrol edip
// CoSRE dönebilir.").
//
// Takım cevabına (guidedTeamServicesBundle — team_services VE my_services aynı
// gövde) "Son loglar (takım)" bölümü: son 15 dk'nın takım etiketli HATA
// logları — top servis, örneklem desenleri, en yeni 3 satır (trace_id ile).
// Okuma logstore.Store.TeamRecentLogs (ES: TEK size:0 _search, maliyet
// sözleşmesi team_logs.go); detektör/handler ES'e doğrudan GİTMEZ.
//
// Açma / kapama: Settings → Elasticsearch → "Team field" (ESFieldMap.Team).
// BOŞ = kapalı: bölüm hiç yazılmaz, hiçbir okuma başlamaz (sıfır ES çağrısı —
// TestTeamLogsDisabledZeroCalls). Geri alma: alanı temizle.
//
// Takım DEĞERLERİ eşleme ayarı istemez: Coremetry'nin takım adları zaten aynı
// pod etiketinden türetiliyor (chstore deriveMetadataAllSQL: span resource
// takım etiketleri → owner/sre takımı, otomatik yazım *_auto'da). Sorgu,
// kanonik takıma (alias + katlama, TeamAliases.CanonTeam) düşen TÜM ham
// yazımları OR'lar — katalog adı ile etiket değeri harf farkıyla ayrışsa da
// eşleşir (teamLogLabelValues).
//
// Bütçe: okuma bundle'ın başında PARALEL başlar (RED + takım özeti okunurken
// koşar); bundle sonunda en çok teamLogsWait bekler. Zaman aşımı / hata /
// desteklenmeyen backend → cevap yine gider, bölüm tek dürüst satır der
// ("OKUNAMADI — 'log yok' DEME"). Metin deterministik; model yalnız anlatır.
// Başarılı sonuç 60 s Redis'te (girdilerin TAMAMI anahtarda); hata önbelleğe
// yazılmaz.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/logstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

const (
	teamLogsWindow  = logstore.TeamLogsDefaultWindow
	teamLogsTimeout = 3 * time.Second         // okuma bağlamı (ES yumuşak timeout'u da 3 s)
	teamLogsWait    = 3500 * time.Millisecond // bundle'ın en çok bekleyeceği (başlangıçtan)
	teamLogsTTL     = 60 * time.Second
	teamLogsTool    = "team_recent_logs"
	teamLogsChip    = "Son loglar (takım)"
)

// Bölüm durumları.
const (
	teamLogsOK          = "ok"
	teamLogsUnsupported = "unsupported"
	teamLogsNoValues    = "no_values"
	teamLogsTimedOut    = "timeout"
	teamLogsFailed      = "failed"
)

// teamLogsOutcome — okumanın sonucu (önbelleğe yalnız State=ok girer).
type teamLogsOutcome struct {
	State  string                   `json:"state"`
	Res    *logstore.TeamLogsResult `json:"res,omitempty"`
	Err    string                   `json:"err,omitempty"`
	Fields []string                 `json:"fields"`
	Values []string                 `json:"values,omitempty"`
	FromMs int64                    `json:"fromMs"`
	ToMs   int64                    `json:"toMs"`
	DurMs  int64                    `json:"durMs"`
	// issued — v0.10.1728: bu çağrı logstore'a gitti (önbellek isabeti değil);
	// briefing tik maliyeti ES/CH sayımı. JSON'a girmez.
	issued bool
}

// teamLogsPending — başlamış okuma: sonuç kanalı + bekleme sınırı.
type teamLogsPending struct {
	ch       <-chan teamLogsOutcome
	deadline time.Time
	fields   []string
	from, to time.Time
}

// teamLogFields — yapılandırılmış takım alanları; nil = özellik kapalı.
func (s *Server) teamLogFields() []string {
	if s == nil || s.logsMgr == nil {
		return nil
	}
	f, err := logstore.ParseTeamFields(s.logsMgr.CurrentSettings().Fields.Team)
	if err != nil {
		return nil
	}
	return f
}

// teamLogsCfgDigest — sorguyu etkileyen backend yapılandırmasının özeti
// (sırsız Snapshot: backend, adresler, indeks, şablon, alan haritası).
func (s *Server) teamLogsCfgDigest() string {
	if s.logsMgr == nil {
		return ""
	}
	snap := s.logsMgr.Snapshot()
	b, _ := json.Marshal(snap.Fields)
	return fnvStr(snap.Backend, strings.Join(snap.Addresses, ","), snap.Index, snap.IndexTemplate, string(b))
}

// teamLogLabelValues — SAF: kanonik takıma düşen ham yazımlar. Önce
// çağıranın adı, sonra katalog (owner/sre + otomatik türetilen) ve alias
// tablosundaki yazımlar, sıralı; tavan logstore.TeamLogsMaxValues.
func teamLogLabelValues(ta chstore.TeamAliases, mds map[string]chstore.ServiceMetadata, team string) []string {
	canon := ta.CanonTeam(team)
	if canon == "" {
		return nil
	}
	seen := map[string]bool{}
	var rest []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] || ta.CanonTeam(v) != canon {
			return
		}
		seen[v] = true
		rest = append(rest, v)
	}
	first := strings.TrimSpace(team)
	seen[first] = true
	for _, md := range mds {
		add(md.OwnerTeam)
		add(md.OwnerTeamAuto)
		add(md.SRETeam)
		add(md.SRETeamAuto)
	}
	for k, v := range ta.Aliases {
		add(k)
		add(v)
	}
	sort.Strings(rest)
	out := append([]string{first}, rest...)
	if len(out) > logstore.TeamLogsMaxValues {
		out = out[:logstore.TeamLogsMaxValues]
	}
	return out
}

// teamLogsKey — SAF (hash-all-inputs): yapılandırma özeti + alanlar + SIRALI
// değerler + pencere (uzunluk + bitiş dakikası). Parçalar fnvStr ayırıcılı.
func teamLogsKey(cfg string, fields, values []string, from, to time.Time) string {
	sv := append([]string(nil), values...)
	sort.Strings(sv)
	parts := []string{cfg, strings.Join(fields, "\x01"),
		strconv.FormatInt(int64(to.Sub(from)/time.Second), 10),
		strconv.FormatInt(to.Unix()/60, 10)}
	parts = append(parts, sv...)
	return "copilot:teamlogs:v1:" + fnvStr(parts...)
}

// teamLogsWindowAt — SAF: dakikaya oturtulmuş [to−15dk, to] (ES request_cache
// aynı gövdeyi görür; önbellek anahtarıyla aynı kova).
func teamLogsWindowAt(now time.Time) (time.Time, time.Time) {
	to := now.Truncate(time.Minute)
	return to.Add(-teamLogsWindow), to
}

// startTeamLogs — özellik açıksa okumayı ARKA PLANDA başlatır; kapalıysa nil
// (hiçbir okuma yok). values nil → katalogdan çözülür (goroutine içinde).
func (s *Server) startTeamLogs(ctx context.Context, team string, values []string, now time.Time) *teamLogsPending {
	fields := s.teamLogFields()
	if len(fields) == 0 || s.logs == nil {
		return nil
	}
	from, to := teamLogsWindowAt(now)
	ch := make(chan teamLogsOutcome, 1)
	p := &teamLogsPending{ch: ch, deadline: time.Now().Add(teamLogsWait), fields: fields, from: from, to: to} // bütçe gerçek saatle; now yalnız pencere
	cfg := s.teamLogsCfgDigest()
	go func() {
		c, cancel := context.WithTimeout(ctx, teamLogsTimeout)
		defer cancel()
		vals := values
		if vals == nil {
			vals = s.resolveTeamLogValues(c, team)
		}
		ch <- s.readTeamLogs(c, cfg, fields, vals, from, to)
	}()
	return p
}

// resolveTeamLogValues — katalog + alias okuması (ikisi de süreç-içi cache'li).
// Okuma düşerse yalnız çağıranın adı (en iyi tahmin; boş değil).
func (s *Server) resolveTeamLogValues(ctx context.Context, team string) []string {
	if s.store == nil {
		return []string{strings.TrimSpace(team)}
	}
	mds, err := s.store.ListServiceMetadata(ctx)
	if err != nil {
		return []string{strings.TrimSpace(team)}
	}
	ta, _ := s.store.GetTeamAliases(ctx)
	return teamLogLabelValues(ta, mds, team)
}

// readTeamLogs — önbellek → TeamRecentLogs. Durum sınıflandırması burada.
func (s *Server) readTeamLogs(ctx context.Context, cfg string, fields, values []string, from, to time.Time) teamLogsOutcome {
	o := teamLogsOutcome{Fields: fields, Values: values, FromMs: from.UnixMilli(), ToMs: to.UnixMilli()}
	if len(values) == 0 {
		o.State = teamLogsNoValues
		return o
	}
	key := teamLogsKey(cfg, fields, values, from, to)
	if s.cache != nil {
		if b, ok, _ := s.cache.Get(ctx, key); ok && len(b) > 0 {
			var cached teamLogsOutcome
			if json.Unmarshal(b, &cached) == nil && cached.State == teamLogsOK {
				return cached
			}
		}
	}
	t0 := time.Now()
	res, err := s.logs.TeamRecentLogs(ctx, logstore.TeamLogsQuery{Values: values, From: from, To: to})
	o.DurMs, o.issued = time.Since(t0).Milliseconds(), true
	switch {
	case errors.Is(err, logstore.ErrTeamLogsUnsupported), errors.Is(err, logstore.ErrTeamLogsDisabled):
		o.State = teamLogsUnsupported
		return o
	case err != nil && (errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil):
		o.State = teamLogsTimedOut
		return o
	case err != nil:
		o.State, o.Err = teamLogsFailed, truncate(err.Error(), 160)
		return o
	case res == nil:
		o.State, o.Err = teamLogsFailed, "boş cevap"
		return o
	}
	o.State, o.Res = teamLogsOK, res
	if s.cache != nil {
		if b, merr := json.Marshal(o); merr == nil {
			_ = s.cache.Set(ctx, key, b, teamLogsTTL)
		}
	}
	return o
}

// wait — sonucu deadline'a kadar bekler; geçerse timeout sonucu (okuma kendi
// 3 s bağlamıyla zaten biter, kanal tamponlu — goroutine sızmaz).
func (p *teamLogsPending) wait(now time.Time) teamLogsOutcome {
	timeout := teamLogsOutcome{State: teamLogsTimedOut, Fields: p.fields, FromMs: p.from.UnixMilli(), ToMs: p.to.UnixMilli()}
	d := p.deadline.Sub(now)
	if d <= 0 {
		select {
		case o := <-p.ch:
			return o
		default:
			return timeout
		}
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case o := <-p.ch:
		return o
	case <-t.C:
		return timeout
	}
}

// teamLogsQueryString — SAF: /logs serbest metin kutusuna giden sorgu
// (alan × değer OR'u, Lucene query_string). Alan adındaki `-` terim ortasında
// geçerli; değer tırnaklı, tırnak/ters bölü kaçışlı.
func teamLogsQueryString(fields, values []string) string {
	esc := func(v string) string {
		v = strings.ReplaceAll(v, `\`, `\\`)
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	var parts []string
	for _, f := range fields {
		for _, v := range values {
			parts = append(parts, f+":"+esc(v))
		}
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " OR ") + ")"
}

// teamLogsHref — SAF: /logs ön-süzgeçli bağlantı (takım + hata seviyesi
// tabanı 17 + mutlak pencere). Değer yoksa "".
// v0.10.1477 — takım artık birinci sınıf `team=` süzgeci (sunucu aynı
// genişletmeyi ve exact-term yan tümceyi uygular; /logs çipi "Takım: <ad>").
// team boşsa eski ham `q=` sorgusu (geriye uyum; eski bağlantılar da `q=`
// olarak çalışmaya devam eder — serbest metin kutusu).
func teamLogsHref(team string, fields, values []string, fromMs, toMs int64) string {
	if len(fields) == 0 || len(values) == 0 || toMs <= fromMs {
		return ""
	}
	q := url.Values{}
	if t := strings.TrimSpace(team); t != "" {
		q.Set("team", t)
	} else {
		q.Set("q", teamLogsQueryString(fields, values))
	}
	q.Set("severity", "17")
	q.Set("range", fmt.Sprintf("custom:%d-%d", fromMs, toMs))
	return "/logs?" + q.Encode()
}

// renderTeamLogsTR — SAF: kanıt bölümü. Her durum kendi cümlesi; okunamayan
// veri "log yok" diye ANLATILMAZ. now satır yaşları için (test sabitler).
func renderTeamLogsTR(team string, o teamLogsOutcome, now time.Time) string {
	var b strings.Builder
	win := fmtAgoTR(int64(teamLogsWindow / time.Second))
	field := strings.Join(o.Fields, ", ")
	switch o.State {
	case teamLogsUnsupported:
		fmt.Fprintf(&b, "- Son loglar (takım): kontrol edilemedi — takım log kontrolü yalnız Elasticsearch log backend'inde çalışır (alan: %s). \"Log yok\" DEME.\n", field)
		return b.String()
	case teamLogsNoValues:
		fmt.Fprintf(&b, "- Son loglar (takım): kontrol edilemedi — %q için etiket değeri çözülemedi. \"Log yok\" DEME.\n", team)
		return b.String()
	case teamLogsTimedOut:
		fmt.Fprintf(&b, "- Son loglar (takım): OKUNAMADI — log okuması %d sn bütçede bitmedi. \"Log yok\" DEME; okunamadığını söyle.\n", int(teamLogsTimeout/time.Second))
		return b.String()
	case teamLogsFailed:
		fmt.Fprintf(&b, "- Son loglar (takım): OKUNAMADI (%s). \"Log yok\" DEME; okunamadığını söyle.\n", o.Err)
		return b.String()
	}
	r := o.Res
	if r == nil {
		b.WriteString("- Son loglar (takım): OKUNAMADI. \"Log yok\" DEME.\n")
		return b.String()
	}
	honest := ""
	switch {
	case r.Partial:
		honest = " (KISMİ — ES zaman aşımı ya da düşen shard; sayılar alt sınır)"
	case r.CountIsLowerBound:
		honest = " (en az — sayım tavanında kesildi)"
	}
	if r.ErrorCount == 0 && len(r.Lines) == 0 {
		fmt.Fprintf(&b, "- Son loglar (takım, son %s, %s = %s): hata seviyesinde log yok%s.\n", win, field, strings.Join(o.Values, " | "), honest)
		return b.String()
	}
	fmt.Fprintf(&b, "- Son loglar (takım, son %s, %s): %d hata logu%s.\n", win, field, r.ErrorCount, honest)
	if len(r.Services) > 0 {
		parts := make([]string, 0, len(r.Services))
		for _, sv := range r.Services {
			parts = append(parts, fmt.Sprintf("%s (%d)", sv.Service, sv.Count))
		}
		src := ""
		if r.ServicesFromSample {
			src = fmt.Sprintf(" — servis alanı boş, %d satırlık örneklemden", r.Sampled)
		}
		fmt.Fprintf(&b, "  En çok hata üreten servisler%s: %s.\n", src, strings.Join(parts, ", "))
	}
	if len(r.Patterns) > 0 {
		fmt.Fprintf(&b, "  Mesaj desenleri (en yeni %d satırlık örneklemde):\n", r.Sampled)
		for _, p := range r.Patterns {
			svc := ""
			if p.Service != "" {
				svc = p.Service + " · "
			}
			fmt.Fprintf(&b, "  - %s%d× %s\n", svc, p.Count, p.Template)
		}
	}
	if len(r.Lines) > 0 {
		b.WriteString("  En yeni satırlar:\n")
		for _, l := range r.Lines {
			ago := "zaman bilinmiyor"
			if l.TimestampNs > 0 {
				ago = fmtAgoTR(int64(now.Sub(time.Unix(0, l.TimestampNs))/time.Second)) + " önce"
			}
			svc := l.Service
			if svc == "" {
				svc = "servis bilinmiyor"
			}
			fmt.Fprintf(&b, "  - [%s · %s] %s", ago, svc, l.Line)
			if l.TraceID != "" {
				fmt.Fprintf(&b, " (trace_id=%s)", l.TraceID)
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("KURAL: son logları 1-2 cümleyle özetle (en çok hata üreten servis + baskın desen); satırları AYNEN tekrar yazma, " +
		"desen/servis UYDURMA. \"Son loglar (takım)\" kanıt çipi /logs'u bu süzgeçle açar.\n")
	return b.String()
}

// teamLogsBlock — bundle kancası: bekler, ⚙ adımını + kanıt çipini yayar,
// bölüm metnini döndürür. p nil (kapalı) → "" ve hiçbir olay yok.
func (s *Server) teamLogsBlock(emit func(string, any), team string, p *teamLogsPending) string {
	if p == nil {
		return ""
	}
	n := emitGuidedStep(emit, teamLogsTool, fmt.Sprintf(`{"team":%s,"windowMin":%d}`, jsonStr(team), int(teamLogsWindow/time.Minute)))
	o := p.wait(time.Now())
	blk := renderTeamLogsTR(team, o, time.Now())
	if o.State != teamLogsOK {
		emitGuidedStepResult(emit, n, teamLogsTool, blk, nil)
		return blk
	}
	ev := mcptools.ChatEvidence{
		Tool: teamLogsTool, Source: teamLogsChip, QueryKind: "lucene",
		Query:      teamLogsQueryString(o.Fields, o.Values),
		Window:     mcptools.EvidenceWindow{FromMs: o.FromMs, ToMs: o.ToMs},
		DurationMs: o.DurMs,
		Href:       teamLogsHref(team, o.Fields, o.Values, o.FromMs, o.ToMs),
	}
	if o.Res != nil {
		rows := len(o.Res.Lines)
		ev.Rows = &rows
	}
	emitGuidedEvidenceResult(emit, n, teamLogsTool, blk, ev, nil)
	return blk
}
