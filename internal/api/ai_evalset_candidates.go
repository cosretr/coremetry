package api

// ai_evalset_candidates.go — v0.10.2111 (CoSRE kalite döngüsü D6,
// docs/cosre/quality-loop-audit.md §3.5; operatör kararları 2026-11-13).
//
// Gece kalite işi bir günü puanlayınca o günün en kötü K=5 cevabı aday vaka
// olarak saved_views(page='cosre_evalset_candidate') satırına yazılır (yeni
// tablo yok; sistem sayfası — genel /api/views ucu yazamaz). Admin satır
// başına tek eylemle ("Kabul") onaylar; audit evalset.candidate.accept.
// Otomatik kabul YOK (en kötü cevap ≠ iyi vaka; soru PII taşıyabilir,
// maskeleme yok → insan görüp onaylar).
//
// Aday → vaka (operatör kararı 1): ajan kipi (ChatLoop, canlı v2 döngüsü),
// soru user'da. Beklenti yalnız veriden BAĞIMSIZ alanlar:
//   - anyOfTools: o turda başarıyla yürüyen araçlardan en az biri (yalnız
//     v2 kataloğunda olan adlar — v1'e özgü ad v2 koşusunda hiç yürümez);
//   - mustNotContain: hakemin "ungrounded" bulduğu ifadeler;
//   - requireEvidence: en az bir araç başarıyla yürümeli.
// Tarihli olgular (hakemin "grounded" olguları, keyFacts) DÜŞER — o günün
// verisine bağlı, ertesi gün koşusunda sahte FAIL olurdu.
//
// Ömür (operatör kararı 2): kabul edilmemiş aday 14 gün sonra listeden düşer
// — OKUMA tarafında süzülür (TTL / şema yok). Kabul edilen aday kalır, günlük
// koşuya girer (RunDailyEvalset) ve GET /api/ai/quality/evalset indirme
// dosyasına eklenir. Adaylar CI sözleşme kapısına GİRMEZ (gömülü küme
// değişmez; repoya gömme elle).
//
//	GET  /api/ai/evalset/candidates              admin
//	POST /api/ai/evalset/candidates/{id}/accept  admin + requireCopilot, audit

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/modelcaps"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func init() { registerRoutesExtra("ai-evalset-candidates", (*Server).registerEvalsetCandidateRoutes) }

func (s *Server) registerEvalsetCandidateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/evalset/candidates", auth.RequireRole(auth.RoleAdmin, s.listEvalsetCandidates))
	mux.HandleFunc("POST /api/ai/evalset/candidates/{id}/accept", auth.RequireRole(auth.RoleAdmin, s.requireCopilot(s.acceptEvalsetCandidate)))
}

const (
	// evalCandidateK — gece başına aday sayısı (operatör kararı: K=5).
	evalCandidateK = 5
	// evalCandidateTTL — kabul edilmemiş adayın listede kalma süresi.
	evalCandidateTTL = 14 * 24 * time.Hour
	// evalCandidateScan — günün en kötülerini bulmak için okunan satır
	// (WorstCosreAnswers üst sınırı yok; sonraki günlerin satırları süzülür).
	evalCandidateScan       = 20
	evalCandidateIDPrefix   = "evc-"
	evalCandidateCasePrefix = "cand-"
	evalCandidateNameMax    = 120
	evalCandidateAnswerMax  = 2000
)

// evalsetCandidate — saved_views.query_string JSON gövdesi (ID satırdan).
type evalsetCandidate struct {
	ID             string   `json:"id"`
	ExchangeID     string   `json:"exchangeId"`
	Day            string   `json:"day"`
	AnsweredAt     string   `json:"answeredAt"`
	Question       string   `json:"question"`
	Answer         string   `json:"answer"`
	Score          float64  `json:"score"`
	Signals        []string `json:"signals"`
	Tier           string   `json:"tier"`
	Model          string   `json:"model"`
	AnyOfTools     []string `json:"anyOfTools"`
	MustNotContain []string `json:"mustNotContain"`
	CreatedAt      string   `json:"createdAt"`
	Accepted       bool     `json:"accepted"`
	AcceptedBy     string   `json:"acceptedBy,omitempty"`
	AcceptedAt     string   `json:"acceptedAt,omitempty"`
}

// evalCandidateStore — G/Ç dikişi (sunucuda *chstore.Store).
type evalCandidateStore interface {
	WorstCosreAnswers(ctx context.Context, from time.Time, limit int) ([]chstore.CosreWorstAnswer, error)
	CosreInteractionsForEval(ctx context.Context, ids []string, from time.Time) (map[string]chstore.CosreInteraction, error)
	CosreGroundingByExchange(ctx context.Context, ids []string, from time.Time) (map[string]chstore.CosreGrounding, error)
	UpsertSavedView(ctx context.Context, v chstore.SavedView) error
	GetSavedView(ctx context.Context, id string) (*chstore.SavedView, error)
	ListSavedViews(ctx context.Context, ownerID, page string) ([]chstore.SavedView, error)
}

// evalCandidateStoreFor — test dikişi; nil = depolama yok.
var evalCandidateStoreFor = func(s *Server) evalCandidateStore {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// evalV2ToolNames — v2 sohbet kataloğunun araç adları (aday anyOfTools süzgeci).
func evalV2ToolNames() map[string]bool {
	out := map[string]bool{}
	var names mcptools.Deps // yalnız adlar: boş bağımlılık (fikstür doğrulamasıyla aynı katalog)
	for _, tl := range mcptools.V2ToolList(names, modelcaps.ToolsetFull) {
		out[tl.Name] = true
	}
	return out
}

// evalCandidateFrom — SAF: günün en kötü cevabı + tur + hakem → aday.
// Soru boşsa aday yok.
func evalCandidateFrom(w chstore.CosreWorstAnswer, inter chstore.CosreInteraction, g *chstore.CosreGrounding, v2Tools map[string]bool, day, now time.Time) (evalsetCandidate, bool) {
	q := strings.TrimSpace(inter.Question)
	if q == "" {
		q = strings.TrimSpace(w.Question)
	}
	if q == "" || w.ExchangeID == "" {
		return evalsetCandidate{}, false
	}
	answer := inter.Answer
	if answer == "" {
		answer = w.Answer
	}
	tools, _, ungrounded := qualityTurnEvidence(inter, g)
	anyOf := []string{}
	for _, t := range tools {
		if v2Tools[t] {
			anyOf = append(anyOf, t)
		}
	}
	signals := append([]string{}, w.Reasons...)
	return evalsetCandidate{
		ExchangeID: w.ExchangeID, Day: day.UTC().Format("2006-01-02"),
		AnsweredAt: w.CreatedAt.UTC().Format(time.RFC3339), Question: q,
		Answer: truncRunes(answer, evalCandidateAnswerMax), Score: float64(w.Score), Signals: signals,
		Tier: w.Tier, Model: w.Model, AnyOfTools: anyOf, MustNotContain: ungrounded,
		CreatedAt: now.UTC().Format(time.RFC3339),
	}, true
}

// evalCandidateCase — SAF: kabul edilmiş aday → koşulacak ajan vakası.
func evalCandidateCase(c evalsetCandidate) evalCase {
	return evalCase{
		ID: evalCandidateCasePrefix + c.ExchangeID, Surface: evalAgentLoopSurface, Mode: evalModeAgent,
		User: c.Question,
		Why:  fmt.Sprintf("aday vaka (%s): örtük kalite puanı %.2f; sinyaller: %s", c.Day, c.Score, strings.Join(c.Signals, ", ")),
		Expect: evalExpect{
			AnyOfTools: c.AnyOfTools, MustNotContain: c.MustNotContain, RequireEvidence: true,
		},
		Provenance: &evalProvenance{ExchangeID: c.ExchangeID},
		file:       "aday",
	}
}

// evalCandidateExportCase — SAF: kabul edilmiş aday → indirme dosyası vakası
// (gömülünce fikstür doğrulamasından geçen ajan biçimi).
func evalCandidateExportCase(c evalsetCandidate) evalsetExportCase {
	ec := evalCandidateCase(c)
	expect := map[string]any{"requireEvidence": true}
	if len(c.AnyOfTools) > 0 {
		expect["anyOfTools"] = c.AnyOfTools
	}
	if len(c.MustNotContain) > 0 {
		expect["mustNotContain"] = c.MustNotContain
	}
	return evalsetExportCase{
		ID: ec.ID, Surface: ec.Surface, Why: ec.Why, User: ec.User, Mode: ec.Mode, Expect: expect,
		Provenance: evalsetProvenance{
			ExchangeID: c.ExchangeID, SurfaceLabel: "cosre-turn:" + c.Tier, CreatedAt: c.AnsweredAt, Model: c.Model,
			PromptChars: uint32(len(c.Question)), ResponseChars: uint32(len(c.Answer)),
		},
	}
}

// evalCandidateVisible — SAF: kabul edilmiş ya da 14 günden genç.
func evalCandidateVisible(c evalsetCandidate, rowCreated, now time.Time) bool {
	return c.Accepted || now.Sub(rowCreated) < evalCandidateTTL
}

// decodeEvalCandidate — SAF: satır → aday (bozuk gövde ya da başka sayfa → false).
func decodeEvalCandidate(v chstore.SavedView) (evalsetCandidate, bool) {
	if v.Page != chstore.EvalsetCandidatePage || v.Name == "" {
		return evalsetCandidate{}, false
	}
	var c evalsetCandidate
	if err := json.Unmarshal([]byte(v.QueryString), &c); err != nil || c.ExchangeID == "" {
		return evalsetCandidate{}, false
	}
	c.ID = v.ID
	return c, true
}

func evalCandidateRow(c evalsetCandidate, createdAt int64) (chstore.SavedView, error) {
	id := c.ID
	c.ID = ""
	blob, err := json.Marshal(c)
	if err != nil {
		return chstore.SavedView{}, err
	}
	return chstore.SavedView{
		ID: id, OwnerID: "", Name: truncRunes(c.Question, evalCandidateNameMax), Page: chstore.EvalsetCandidatePage,
		QueryString: string(blob), Pinned: c.Accepted, CreatedAt: createdAt,
	}, nil
}

// WriteEvalsetCandidates — gece kalite işinin gün sonu kancası
// (cosrelearn.StartQualityJob afterDay). Var olan aday YENİDEN YAZILMAZ
// (kabul durumu ezilmesin). Dönen değer yazılan aday sayısı.
func (s *Server) WriteEvalsetCandidates(ctx context.Context, day time.Time) (int, error) {
	st := evalCandidateStoreFor(s)
	if st == nil {
		return 0, nil
	}
	day = day.UTC()
	worst, err := st.WorstCosreAnswers(ctx, day, evalCandidateScan)
	if err != nil {
		return 0, err
	}
	var pick []chstore.CosreWorstAnswer
	for _, w := range worst {
		if !w.CreatedAt.Before(day) && w.CreatedAt.Before(day.Add(24*time.Hour)) {
			pick = append(pick, w)
		}
	}
	if len(pick) == 0 {
		return 0, nil
	}
	ids := make([]string, 0, len(pick))
	for _, w := range pick {
		ids = append(ids, w.ExchangeID)
	}
	inter, err := st.CosreInteractionsForEval(ctx, ids, day.Add(-time.Hour))
	if err != nil {
		return 0, err
	}
	ground, err := st.CosreGroundingByExchange(ctx, ids, day.Add(-time.Hour))
	if err != nil {
		return 0, err
	}
	v2 := evalV2ToolNames()
	now := time.Now()
	written := 0
	for _, w := range pick {
		if written >= evalCandidateK {
			break
		}
		var gp *chstore.CosreGrounding
		if g, ok := ground[w.ExchangeID]; ok {
			gp = &g
		}
		c, ok := evalCandidateFrom(w, inter[w.ExchangeID], gp, v2, day, now)
		if !ok {
			continue
		}
		c.ID = evalCandidateIDPrefix + c.ExchangeID
		if cur, gerr := st.GetSavedView(ctx, c.ID); gerr != nil {
			return written, gerr
		} else if cur != nil {
			written++ // bu gün için zaten aday — K'ya sayılır, yeniden yazılmaz
			continue
		}
		row, merr := evalCandidateRow(c, now.UnixNano())
		if merr != nil {
			return written, merr
		}
		if uerr := st.UpsertSavedView(ctx, row); uerr != nil {
			return written, uerr
		}
		written++
	}
	return written, nil
}

// listVisibleEvalCandidates — süzülmüş adaylar, yeniden eskiye.
func listVisibleEvalCandidates(ctx context.Context, st evalCandidateStore, now time.Time) ([]evalsetCandidate, error) {
	rows, err := st.ListSavedViews(ctx, "", chstore.EvalsetCandidatePage)
	if err != nil {
		return nil, err
	}
	type item struct {
		c  evalsetCandidate
		at int64
	}
	var items []item
	for _, v := range rows {
		c, ok := decodeEvalCandidate(v)
		if !ok || !evalCandidateVisible(c, time.Unix(0, v.CreatedAt), now) {
			continue
		}
		items = append(items, item{c, v.CreatedAt})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].at > items[j].at })
	out := make([]evalsetCandidate, 0, len(items))
	for _, it := range items {
		out = append(out, it.c)
	}
	return out, nil
}

// acceptedEvalCandidates — kabul edilmiş adaylar (günlük koşu + indirme).
func (s *Server) acceptedEvalCandidates(ctx context.Context) ([]evalsetCandidate, error) {
	st := evalCandidateStoreFor(s)
	if st == nil {
		return nil, nil
	}
	all, err := listVisibleEvalCandidates(ctx, st, time.Now())
	if err != nil {
		return nil, err
	}
	var out []evalsetCandidate
	for _, c := range all {
		if c.Accepted {
			out = append(out, c)
		}
	}
	return out, nil
}

// evalDailyCandidateCases — günlük koşu planına eklenecek vakalar; okuma
// düşerse yalnız gömülü küme koşar (log).
func (s *Server) evalDailyCandidateCases(ctx context.Context) []evalCase {
	acc, err := s.acceptedEvalCandidates(ctx)
	if err != nil {
		log.Printf("[cosre-evalset-daily] aday vakalar okunamadı, yalnız gömülü küme: %v", err)
		return nil
	}
	out := make([]evalCase, 0, len(acc))
	for _, c := range acc {
		out = append(out, evalCandidateCase(c))
	}
	return out
}

func (s *Server) listEvalsetCandidates(w http.ResponseWriter, r *http.Request) {
	st := evalCandidateStoreFor(s)
	out := []evalsetCandidate{}
	if st != nil {
		list, err := listVisibleEvalCandidates(r.Context(), st, time.Now())
		if err != nil {
			writeErr(w, err)
			return
		}
		out = append(out, list...)
	}
	writeJSON(w, map[string]any{"candidates": out, "ttlDays": int(evalCandidateTTL / (24 * time.Hour))})
}

func (s *Server) acceptEvalsetCandidate(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	st := evalCandidateStoreFor(s)
	if st == nil || id == "" || len(id) > 256 || !strings.HasPrefix(id, evalCandidateIDPrefix) {
		writeJSONError(w, http.StatusNotFound, "aday bulunamadı")
		return
	}
	row, err := st.GetSavedView(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	var c evalsetCandidate
	ok := false
	if row != nil {
		c, ok = decodeEvalCandidate(*row)
	}
	now := time.Now()
	if !ok || !evalCandidateVisible(c, time.Unix(0, row.CreatedAt), now) {
		writeJSONError(w, http.StatusNotFound, "aday bulunamadı ya da süresi doldu")
		return
	}
	if c.Accepted {
		writeJSON(w, map[string]any{"candidate": c})
		return
	}
	who := ""
	if cl := auth.FromContext(r.Context()); cl != nil {
		who = cl.Email
		if who == "" {
			who = cl.UserID
		}
	}
	c.Accepted, c.AcceptedBy, c.AcceptedAt = true, who, now.UTC().Format(time.RFC3339)
	next, err := evalCandidateRow(c, row.CreatedAt)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := st.UpsertSavedView(r.Context(), next); err != nil {
		writeErr(w, err)
		return
	}
	s.audit(r, "evalset.candidate.accept", "ai_evalset_candidate", id, "exchange="+c.ExchangeID)
	writeJSON(w, map[string]any{"candidate": c})
}
