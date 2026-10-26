package api

// chat_exception_groups.go — v0.10.1331 (operatör: "CoSRE doğrudan operasyonu
// sorunca cevabı yeterli değil, ancak Exceptions'ta sorunca iyi tespit
// ediyor. Operasyon ismi BSA için verilince CoSRE de Exceptions'a bakabilir,
// son geleni ve yorumu oradan yapabilir.")
//
// Sohbetin Exceptions gruplarını okuyan PAYLAŞILAN dikişi: operasyon
// ön-getirmesi (chat_operation_prefetch.go) Messages = [operasyon kodu] ile
// çağırır (Oracle grubunda ex_message = operasyon kodu, ex_type = hata kodu);
// servis düzeyi okuma (Service) aynı yolu kullanacak. Yeni MCP aracı YOK
// (araç sayısı pinleri ve kısa açıklama bütçesi değişmez).
//
// Okumalar sınırlı ve best-effort: TEK ListExceptionGroups (FINAL, LIMIT 20,
// max_execution_time; pencere örtüşmesi + son görülme sırası), saatlik
// toplamlar enjekte edilen Oracle istatistik önbelleğinden (CH okuması yok),
// en çok exGroupSampleGroups grup için GetExceptionGroupSamples(3) — örnek
// mesaj ve örnek trace id'leri. Liste okunamazsa "OKUNAMADI", okunur ama boş
// gelirse "grup yok" — ikisi asla karışmaz.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

const (
	exGroupListLimit     = 20 // CH okuması tavanı (kesilme sayısını bilmek için gösterilenden geniş)
	exGroupRenderMax     = 5  // modele / karta giden grup tavanı
	exGroupTeamRenderMax = 10 // v0.10.1385 — takım kapsamında gösterilen grup tavanı
	exGroupSampleGroups  = 2  // örnek okunan grup sayısı
	exGroupSampleLimit   = 3  // grup başına örnek
	exGroupSampleRunes   = 160
	exGroupTraceIDsMax   = 2
	exGroupServicesMax   = 3
	exGroupCodeRunes     = 40 // hata kodu / exception tipi
	exGroupOpRunes       = 80 // operasyon kodu
	exGroupSvcRunes      = 40
	exGroupReasonRunes   = 60
	exGroupBurstFactor   = 3 // oraclePriorityAt / oracleExplainBurst ile AYNI kat
	exGroupListTimeout   = 5 * time.Second
	exGroupSampleTimeout = 4 * time.Second
)

// exScope — okuma kapsamı. Messages (birebir ex_message kümesi) ya da
// Service; From/To pencere örtüşmesi (last_seen ≥ From, first_seen ≤ To).
type exScope struct {
	Service  string
	Messages []string
	From, To time.Time
	Max      int // 0 = exGroupRenderMax
	// v0.10.1385 — takım kapsamı (copilot_team_exceptions.go): Services
	// `service IN (…)` (Exceptions sayfasının owner/sre süzgeciyle aynı
	// yol), State durum kovası ("open" = new/acknowledged/regressed; boş =
	// ignored hariç hepsi), MinOcc/FloorExempt/FloorExemptRegressed sayfanın
	// varsayılan tabanı. Takım kapsamında gösterim tavanı exGroupTeamRenderMax.
	Services             []string
	State                string
	MinOcc               uint64
	FloorExempt          []string
	FloorExemptRegressed bool
	// Search — v0.10.1553 kaynak keşfi: exception tipi / hata kodu araması
	// (Exceptions sayfasının arama kutusuyla aynı süzgeç).
	Search string
}

// exGroupLine — bir grubun modele ve karta giden kompakt hâli.
type exGroupLine struct {
	Fingerprint    string   `json:"fingerprint"`
	Type           string   `json:"type"`
	Message        string   `json:"message"`
	Service        string   `json:"service,omitempty"`
	State          string   `json:"state"`
	Priority       string   `json:"priority,omitempty"`
	PriorityReason string   `json:"priorityReason,omitempty"`
	Oracle         bool     `json:"oracle"`
	Code           string   `json:"code,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	HourKnown      bool     `json:"hourKnown"`
	LastHour       uint64   `json:"lastHour"`
	PrevHour       uint64   `json:"prevHour"`
	Occurrences    uint64   `json:"occurrences"`
	FirstSeen      int64    `json:"firstSeen"` // unix ns
	LastSeen       int64    `json:"lastSeen"`  // unix ns
	Services       []string `json:"services,omitempty"`
	ServiceCount   int      `json:"serviceCount,omitempty"`
	SampleTraceIDs []string `json:"sampleTraceIds,omitempty"`
	Sample         string   `json:"sample,omitempty"`
	Href           string   `json:"href"`
	ExplainHref    string   `json:"explainHref"`
}

// exceptionGroupsEvidence — okumanın sonucu. Err doluysa Groups anlamsız
// (OKUNAMADI); Err boş + Groups boş = gerçekten grup yok.
type exceptionGroupsEvidence struct {
	Groups    []exGroupLine `json:"groups"`
	More      int           `json:"more,omitempty"`      // gösterilmeyen grup sayısı (alt sınır)
	MoreAtMin bool          `json:"moreAtMin,omitempty"` // liste tavanı doldu: More bir alt sınır
	Err       string        `json:"error,omitempty"`
	LookbackS int64         `json:"lookbackS"`
	at        time.Time     // "şimdi" (sc.To) — göreli yaş metni için
}

// exGroupsReader — okuma yüzü (*chstore.Store; testte sahte).
type exGroupsReader interface {
	ListExceptionGroups(ctx context.Context, f chstore.ExceptionGroupFilter) ([]chstore.ExceptionGroup, error)
	GetExceptionGroupSamples(ctx context.Context, fingerprint string, limit int) (chstore.ExceptionSamples, error)
}

// exGroupHooks — CH dışı zenginleştirme (öncelik merdiveni, Oracle istatistik
// önbelleği); testte sabit.
type exGroupHooks struct {
	priority   func(g chstore.ExceptionGroup) (string, string)
	oracleInfo func(g chstore.ExceptionGroup) *chstore.OracleGroupInfo
}

// exceptionGroupsEvidence — sunucunun gerçek okuyucusu ve kancalarıyla.
func (s *Server) exceptionGroupsEvidence(ctx context.Context, sc exScope) exceptionGroupsEvidence {
	if s == nil || s.store == nil {
		return exceptionGroupsEvidence{Err: "exception deposu yok", LookbackS: lookbackSeconds(sc), at: sc.To}
	}
	// v0.10.1369 — örnekler Tempo kararından geçer (exception_sample_tempo.go):
	// CH'de olmayıp Tempo'da bulunan id artık "Coremetry'de yok" sayılmaz.
	return collectExceptionGroups(ctx, tempoMarkedSamples{exGroupsReader: s.store, mark: s.withTempoSampleTraces}, sc, serverExGroupHooks())
}

// serverExGroupHooks — sunucunun gerçek kancaları (öncelik merdiveni + Oracle
// istatistik önbelleği; ikisi de CH okuması yapmaz). v0.10.1411 — açıklama
// aday havuzu (chat_operation_exception_explain.go) aynı kancaları kullanır.
func serverExGroupHooks() exGroupHooks {
	return exGroupHooks{
		priority: exceptionPriority,
		oracleInfo: func(g chstore.ExceptionGroup) *chstore.OracleGroupInfo {
			st, ok := oracleGroupStats(g)
			return oracleGroupInfoFrom(g, st, ok, exGroupServicesMax)
		},
	}
}

func lookbackSeconds(sc exScope) int64 {
	if sc.From.IsZero() || sc.To.IsZero() || !sc.To.After(sc.From) {
		return 0
	}
	return int64(sc.To.Sub(sc.From) / time.Second)
}

// collectExceptionGroups — okuma + kompaktlama (sahte okuyucuyla test edilir).
func collectExceptionGroups(ctx context.Context, rd exGroupsReader, sc exScope, h exGroupHooks) exceptionGroupsEvidence {
	ev := exceptionGroupsEvidence{Groups: []exGroupLine{}, LookbackS: lookbackSeconds(sc), at: sc.To}
	if len(sc.Messages) == 0 && strings.TrimSpace(sc.Service) == "" && len(sc.Services) == 0 && strings.TrimSpace(sc.Search) == "" {
		ev.Err = "kapsam yok"
		return ev
	}
	maxShow := exGroupRenderMax
	if len(sc.Services) > 0 { // v0.10.1385 — takım listesi
		maxShow = exGroupTeamRenderMax
	}
	show := sc.Max
	if show <= 0 || show > maxShow {
		show = maxShow
	}
	f := chstore.ExceptionGroupFilter{Service: sc.Service, Messages: sc.Messages, Sort: "lastSeen", Dir: "desc", Limit: exGroupListLimit,
		Services: sc.Services, State: sc.State, MinOccurrences: sc.MinOcc, Search: strings.TrimSpace(sc.Search)}
	if sc.MinOcc > 0 { // v0.10.1385 — taban istisnaları yalnız tabanla anlamlı
		f.FloorExempt, f.FloorExemptRegressed = sc.FloorExempt, sc.FloorExemptRegressed
	}
	if !sc.From.IsZero() {
		f.ActiveFromNs = sc.From.UnixNano()
	}
	if !sc.To.IsZero() {
		f.ActiveToNs = sc.To.UnixNano()
	}
	lctx, cancel := context.WithTimeout(ctx, exGroupListTimeout)
	rows, err := rd.ListExceptionGroups(lctx, f)
	cancel()
	if err != nil {
		ev.Err = err.Error()
		return ev
	}
	if len(rows) > show {
		ev.More = len(rows) - show
		ev.MoreAtMin = len(rows) >= exGroupListLimit
		rows = rows[:show]
	}
	for i, g := range rows {
		line := exGroupLineFrom(g, h)
		if i < exGroupSampleGroups {
			sctx, scancel := context.WithTimeout(ctx, exGroupSampleTimeout)
			smp, serr := rd.GetExceptionGroupSamples(sctx, g.Fingerprint, exGroupSampleLimit)
			scancel()
			if serr == nil {
				line.Sample, line.SampleTraceIDs = exGroupSampleFacts(smp.Samples)
			}
		}
		ev.Groups = append(ev.Groups, line)
	}
	return ev
}

// exGroupLineFrom — SAF: grup satırı + kancalar → kompakt satır.
func exGroupLineFrom(g chstore.ExceptionGroup, h exGroupHooks) exGroupLine {
	href := "/problems?exc=" + url.QueryEscape(g.Fingerprint)
	l := exGroupLine{
		Fingerprint: g.Fingerprint, Type: g.Type, Message: g.Message, State: g.State,
		Occurrences: g.Occurrences, FirstSeen: g.FirstSeen, LastSeen: g.LastSeen,
		Oracle: chstore.IsOracleGroup(g.Fingerprint), Href: href, ExplainHref: href + "&ai=exception",
	}
	if !strings.HasPrefix(g.Service, chstore.OracleGroupServicePrefix) {
		l.Service = g.Service
	}
	if h.priority != nil {
		l.Priority, l.PriorityReason = h.priority(g)
	}
	if l.Oracle {
		l.Code, l.Operation = g.Type, g.Message
		if h.oracleInfo != nil {
			if info := h.oracleInfo(g); info != nil {
				l.HourKnown = info.Known
				l.LastHour, l.PrevHour = info.LastHour, info.PrevHour
				for _, sv := range info.Services {
					if len(l.Services) < exGroupServicesMax && sv.Name != "" {
						l.Services = append(l.Services, sv.Name)
					}
				}
				l.ServiceCount = info.ServiceCount
			}
		}
	}
	if len(l.Services) == 0 && l.Service != "" {
		l.Services, l.ServiceCount = []string{l.Service}, 1
	}
	return l
}

// exGroupSampleFacts — SAF: en yeni örneğin mesajı (≤160 rune) + Coremetry'de
// olmadığı BİLİNMEYEN örnek trace id'leri (≤2, tekil). v0.10.1369 — Tempo'da
// bulunan id TraceInCoremetry=true (+TraceSource="tempo") gelir → kullanılır.
func exGroupSampleFacts(samples []chstore.ExceptionSample) (string, []string) {
	msg := ""
	var ids []string
	seen := map[string]bool{}
	for _, sm := range samples {
		if msg == "" {
			if m := strings.TrimSpace(sm.Message); m != "" {
				msg = clipRunes(oneLine(m), exGroupSampleRunes)
			} else if m := strings.TrimSpace(sm.StatusMsg); m != "" {
				msg = clipRunes(oneLine(m), exGroupSampleRunes)
			}
		}
		id := strings.TrimSpace(sm.TraceID)
		if id == "" || seen[id] || (sm.TraceInCoremetry != nil && !*sm.TraceInCoremetry) || len(ids) >= exGroupTraceIDsMax {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return msg, ids
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// exGroupTrendTR — SAF: son 1 sa ↔ önceki 1 sa okuması (patlama ≥3×).
func exGroupTrendTR(l exGroupLine) string {
	if !l.Oracle {
		return fmt.Sprintf("saatlik sayı yok (span grubu; ömür boyu toplam %s)", fmtThousands(l.Occurrences))
	}
	if !l.HourKnown {
		return "saatlik sayı bilinmiyor (istatistik önbelleği okunamadı)"
	}
	last, prev := fmtThousands(l.LastHour), fmtThousands(l.PrevHour)
	verdict := "sürekli akış"
	switch {
	case l.LastHour == 0 && l.PrevHour == 0:
		verdict = "son iki saatte yok (durmuş)"
	case l.LastHour == 0:
		verdict = "son saatte yok (duruyor)"
	case l.PrevHour == 0:
		verdict = "yeni akış (önceki saatte yoktu)"
	case l.LastHour >= exGroupBurstFactor*l.PrevHour:
		verdict = fmt.Sprintf("patlama (≥%d×)", exGroupBurstFactor)
	case l.LastHour*2 < l.PrevHour:
		verdict = "azalıyor"
	}
	return fmt.Sprintf("son 1 sa %s · önceki 1 sa %s → %s", last, prev, verdict)
}

// renderExceptionGroupsTR — SAF: modele giden bölüm (golden testli). title
// kapsamın adı ("X operasyonu").
func renderExceptionGroupsTR(title string, ev exceptionGroupsEvidence) string {
	var b strings.Builder
	win := "pencere"
	if ev.LookbackS > 0 {
		win = "son " + fmtAgoTR(ev.LookbackS)
	}
	fmt.Fprintf(&b, "Exceptions teknik hata grupları — %s (%s, en yeni önce):\n", clipRunes(title, exGroupOpRunes+12), win)
	if ev.Err != "" {
		fmt.Fprintf(&b, "Exceptions grupları OKUNAMADI (%s); grup olup olmadığı bilinmiyor — \"grup yok\" deme.\n", clipRunes(oneLine(ev.Err), 120))
		return b.String()
	}
	if len(ev.Groups) == 0 {
		fmt.Fprintf(&b, "Grup yok: %s içinde Exceptions'ta bu kapsamda teknik hata grubu bulunmadı.\n", win)
		return b.String()
	}
	for i, l := range ev.Groups {
		head := clipRunes(l.Type, exGroupOpRunes)
		if l.Oracle {
			head = clipRunes(l.Code, exGroupCodeRunes) + " · " + clipRunes(l.Operation, exGroupOpRunes)
		}
		fmt.Fprintf(&b, "%d. %s", i+1, head)
		detailed := i < exGroupSampleGroups // örnek okunan gruplar ayrıntılı, kalanı tek satır (bütçe)
		if l.Priority != "" {
			fmt.Fprintf(&b, " · %s", l.Priority)
			if l.PriorityReason != "" && detailed {
				fmt.Fprintf(&b, " (%s)", clipRunes(l.PriorityReason, exGroupReasonRunes))
			}
		}
		fmt.Fprintf(&b, " · durum %s", l.State)
		if l.LastSeen > 0 {
			ls := time.Unix(0, l.LastSeen).UTC()
			if !ev.at.IsZero() {
				fmt.Fprintf(&b, " · son görülme %s önce (%s UTC)", fmtAgoTR(int64(ev.at.Sub(ls)/time.Second)), ls.Format("01-02 15:04"))
			} else {
				fmt.Fprintf(&b, " · son görülme %s UTC", ls.Format("01-02 15:04"))
			}
		}
		if l.FirstSeen > 0 && detailed {
			fmt.Fprintf(&b, " · ilk %s UTC", time.Unix(0, l.FirstSeen).UTC().Format("01-02 15:04"))
		}
		fmt.Fprintf(&b, " · toplam %s", fmtThousands(l.Occurrences))
		if !detailed {
			fmt.Fprintf(&b, " · %s · Exceptions: %s\n", exGroupTrendTR(l), l.Href)
			continue
		}
		b.WriteString("\n")
		switch {
		case l.Sample != "":
			fmt.Fprintf(&b, "   ne demek: örnek satır mesajı %q\n", l.Sample)
		case !l.Oracle && l.Message != "":
			fmt.Fprintf(&b, "   ne demek: mesaj %q\n", clipRunes(oneLine(l.Message), exGroupSampleRunes))
		default:
			b.WriteString("   ne demek: örnek mesaj okunmadı\n")
		}
		// Operasyon + kod başlıkta; burada yalnız servis kırılımı (tekrar = token).
		if len(l.Services) > 0 {
			sv := make([]string, len(l.Services))
			for j, s := range l.Services {
				sv[j] = clipRunes(s, exGroupSvcRunes)
			}
			svc := "servis " + strings.Join(sv, ", ")
			if l.ServiceCount > len(l.Services) {
				svc += fmt.Sprintf(" (toplam %d servis)", l.ServiceCount)
			}
			fmt.Fprintf(&b, "   nerede yoğunlaşıyor: başlıktaki operasyon/kod · %s\n", svc)
		} else {
			b.WriteString("   nerede yoğunlaşıyor: servis kırılımı yok\n")
		}
		fmt.Fprintf(&b, "   patlama mı sürekli akış mı: %s\n", exGroupTrendTR(l))
		if len(l.SampleTraceIDs) > 0 {
			fmt.Fprintf(&b, "   örnek trace: %s\n", strings.Join(l.SampleTraceIDs, ", "))
		}
		fmt.Fprintf(&b, "   Exceptions: %s\n", l.Href)
	}
	if ev.More > 0 {
		more := fmt.Sprintf("%d", ev.More)
		if ev.MoreAtMin {
			more = "en az " + more
		}
		fmt.Fprintf(&b, "(+%s grup daha gösterilmedi — tümü Exceptions'ta)\n", more)
	}
	return b.String()
}
