package api

// ai_evalset_runs.go — v0.10.940 (Settings › AI › Değerlendirme paneli;
// operatör onayı 2026-09-26 — mockup coremetry-evalset.html + K1–K3):
// donmuş evalset vakalarını SUNUCUDA, üretimin yapılandırılmış profil(ler)i
// ile koşar ve geçmişi saklar.
//
//	GET  /api/ai/evalset/catalog              yüzeyler + üretimin profili
//	POST /api/ai/evalset/runs                 koşu başlat (202 · 409 · 400 · 503)
//	GET  /api/ai/evalset/runs                 son 20 koşu (sürmekte olan başta)
//	GET  /api/ai/evalset/runs/{id}            koşu + vaka sonuçları (404)
//	POST /api/ai/evalset/runs/{id}/cancel     bu süreçte süren koşuyu durdur (409)
//	GET  /api/ai/evalset/compare?base=&head=  iki bitmiş koşunun evalrubric.Diff'i
//
// Kayıt ai_evalset.go registerAIEvalsetRoutes'tan (tek satır); api.go ve
// ai_routes.go büyümez. Hepsi admin (Settings zaten admin kapılı). POST koşu
// ayrıca requireCopilot (rol sarımı DIŞTA — ai_routes.go sözleşmesi).
//
// Çağrı yolu: s.aiCall(ctx, nil, …) — make audit CHECK 4 doğrudan
// s.copilot.Explain'i yasaklar; aiCall ai_calls satırını, span'ı ve JSON
// kipini kurar. Satır etiketi "evalset-<Yüzey>" (K2: /ai'da AYRI kaynak,
// üretim sayılarını şişirmez). Profil: üretimin o yüzey için seçeceği
// (copilot.SurfaceProfileID + WithProfile) — etiket haritada olmadığından
// sabitlenmezse her şey varsayılana düşerdi. Kalkan AÇIKÇA tohumsuz aiShield
// (CLI ile aynı sayaç; aiCall yalnız nil ise ezer).
//
// İş defteri SÜREÇ YEREL (admin_trace_backfill.go / spool_actions.go
// emsali): api.go'ya alan eklenemez ve Redis kilidi için değer yok — koşu
// ardışık, admin tetikli, en fazla bir tane. Çapraz-pod kapısı CH'deki en
// yeni satır: taze bir "running" varsa 409. Ölü pod'un koşusu 15 dk
// sessizlikten sonra okumada "abandoned" görünür (yazılmaz).
//
// Neden bağlam context.Background(): koşu isteği aşar (sayfadan ayrılmak
// koşuyu durdurmaz — K1 metni), tavanı 60 dk; api'de recover middleware
// yok, kopuk goroutine'de panik süreci öldürür → recover → failed.
//
// Bilinçli YOK: onay adımı / ücretli-sağlayıcı uyarısı (operatör: "yerel
// modele bağlıyız" — koşu üretimle aynı profili kullanır), sıcaklık
// override'ı (üretim eşitliği; CLI özel Service'inde 0 tutar), eşzamanlı
// vaka (yerel uç üretim sohbetiyle paylaşılıyor; ardışık = öngörülebilir yük).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/evalrubric"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

func (s *Server) registerAIEvalsetRunRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/ai/evalset/catalog", auth.RequireRole(auth.RoleAdmin, s.getAIEvalsetCatalog))
	mux.HandleFunc("GET /api/ai/evalset/runs", auth.RequireRole(auth.RoleAdmin, s.listAIEvalsetRuns))
	mux.HandleFunc("POST /api/ai/evalset/runs", auth.RequireRole(auth.RoleAdmin, s.requireCopilot(s.startAIEvalsetRun)))
	mux.HandleFunc("GET /api/ai/evalset/runs/{id}", auth.RequireRole(auth.RoleAdmin, s.getAIEvalsetRun))
	mux.HandleFunc("POST /api/ai/evalset/runs/{id}/cancel", auth.RequireRole(auth.RoleAdmin, s.cancelAIEvalsetRun))
	mux.HandleFunc("GET /api/ai/evalset/compare", auth.RequireRole(auth.RoleAdmin, s.compareAIEvalsetRuns))
}

// evalRunsKeep — K3: panel en yeni 20 koşuyu gösterir (fiziksel silme TTL).
const evalRunsKeep = 20

// evalRunDeadline — koşunun toplam tavanı. 51 vaka × yerel model ~ dakikalar;
// asılı bir uç koşuyu (ve 409 kapısını) sonsuza dek tutmasın. var: test
// dikişi (tavan → failed yolu milisaniyede sınanır).
var evalRunDeadline = 60 * time.Minute

// evalRunIDMax — yol parametresi tavanı (crafted uzun kimlik nokta okumaya gitmesin).
const evalRunIDMax = 64

// evalFinalSaveWaits — v0.10.940: son yazımın (cases'i taşıyan TEK yazım)
// deneme aralıkları; ilki hemen, her deneme kendi 10 sn bağlamıyla. Toplam
// ~22 sn bekleme + 4 × 10 sn tavan: slot bu süre boyunca tutulur. var: test
// dikişi (milisaniyeye iner).
var evalFinalSaveWaits = []time.Duration{0, 2 * time.Second, 5 * time.Second, 15 * time.Second}

// evalRunStore — depolama dikişi: sunucuda *chstore.Store, testte bellek içi sahte.
type evalRunStore interface {
	SaveEvalRun(ctx context.Context, r chstore.EvalRun) error
	ListEvalRuns(ctx context.Context, limit int) ([]chstore.EvalRun, error)
	GetEvalRun(ctx context.Context, id string) (*chstore.EvalRun, error)
}

// evalRunStoreFor — test dikişi. nil = depolama yok (bare sunucu): koşu yine
// koşar, yalnız bellekte görünür.
var evalRunStoreFor = func(s *Server) evalRunStore {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store
}

// evalsetCasesLoader — test dikişi (küçük, denetimli vaka kümesi).
var evalsetCasesLoader = loadEvalsetCases

// ── İş defteri ──────────────────────────────────────────────────────────

// evalJob — bu süreçte koşan (ya da son yazımını yapan) koşu. Alanlar
// evalJobs.mu altında.
type evalJob struct {
	row       chstore.EvalRun // Summary/Cases burada tutulmaz — aşağıdakilerden üretilir
	plan      []evalCase
	cases     []evalCaseResult
	rubric    []evalrubric.CaseResult // atlanmamış vakalar (Summarize girdisi)
	cancel    context.CancelFunc
	cancelled bool          // iptal ucu tetikledi
	deadline  time.Duration // başlangıçta okunan tavan (mesaj için)
	userID    string
	userEmail string
}

type evalJobBook struct {
	mu      sync.Mutex
	running *evalJob
	// last — v0.10.940: son yazımı TÜM denemelerde düşen bitmiş iş. CH'de
	// bayat "running" + boş cases kalır; bu kopya olmasa sahip pod dahil
	// herkes 15 dk 409 alır, sonra "abandoned" ve vaka sonuçları kaybolur.
	// Detay/liste/kıyas buradan, çapraz-pod kapısı bu kimliği engel saymaz.
	// Tek yuva: sonraki koşunun son yazımı tutarsa bu satır bir kez daha
	// yazılır (tutarsa boşalır); sonraki koşu da düşerse yerini o alır.
	last *evalJob
}

var evalJobs evalJobBook

// evalStamp — updated_at KESİN artan (chstore version'ı ondan türetir).
func evalStamp(prev time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	return now
}

// summaryLocked — bellek kopyasının özeti (sahibi bu süreç: türetme yok).
func (j *evalJob) summaryLocked() evalRunSummary {
	sum := evalSummaryFromRow(j.row, time.Now(), true)
	sum.BySurface = evalSurfaceSummaries(j.plan, j.cases)
	return sum
}

// rowLocked — CH satırı; withCases yalnız son yazımda (ilerleme küçük kalsın).
func (j *evalJob) rowLocked(withCases bool) chstore.EvalRun {
	row := j.row
	blob, _ := json.Marshal(evalRunSummaryBlob{BySurface: evalSurfaceSummaries(j.plan, j.cases)})
	row.Summary = string(blob)
	row.Cases = ""
	if withCases {
		cases := j.cases
		if cases == nil {
			cases = []evalCaseResult{}
		}
		b, _ := json.Marshal(cases)
		row.Cases = string(b)
	}
	return row
}

// record — bir vakanın sonucunu işler; ilerleme satırını döner.
func (b *evalJobBook) record(j *evalJob, res evalCaseResult, rub evalrubric.Result) chstore.EvalRun {
	b.mu.Lock()
	defer b.mu.Unlock()
	j.cases = append(j.cases, res)
	switch {
	case res.Skipped:
		j.row.Skipped++
	case res.OK:
		j.row.Pass++
	default:
		j.row.Fail++
	}
	if !res.Skipped {
		j.rubric = append(j.rubric, evalrubric.CaseResult{ID: res.ID, Surface: res.Surface, OK: res.OK, LatencyMs: res.LatencyMs, Fails: res.Fails, Rubric: rub})
	}
	j.row.Done++
	j.row.RubricMean = evalrubric.Summarize(j.rubric, int(j.row.Skipped)).RubricMean
	j.row.UpdatedAt = evalStamp(j.row.UpdatedAt)
	return j.rowLocked(false)
}

// current — bu süreçteki işin özeti (yoksa nil).
func (b *evalJobBook) current() *evalRunSummary {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running == nil {
		return nil
	}
	sum := b.running.summaryLocked()
	return &sum
}

// owned — listeye bellekten giren özetler: koşan iş + (varsa) son yazımı
// düşen iş. İkisi de aynı kimlikli CH satırından tazedir.
func (b *evalJobBook) owned() []evalRunSummary {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []evalRunSummary
	for _, j := range []*evalJob{b.running, b.last} {
		if j != nil {
			out = append(out, j.summaryLocked())
		}
	}
	return out
}

// findLocked — kimlik defterdeki bir işe (koşan ya da son yazımı düşen) aitse o iş.
func (b *evalJobBook) findLocked(id string) *evalJob {
	for _, j := range []*evalJob{b.running, b.last} {
		if j != nil && j.row.ID == id {
			return j
		}
	}
	return nil
}

// snapshot — kimlik bu süreçteki işse özet + o ana dek gelen vakalar.
func (b *evalJobBook) snapshot(id string) (evalRunSummary, []evalCaseResult, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.findLocked(id)
	if j == nil {
		return evalRunSummary{}, nil, false
	}
	cases := append([]evalCaseResult{}, j.cases...)
	return j.summaryLocked(), cases, true
}

// finishedRow — kimlik defterde BİTMİŞ bir işse (son yazım denemesi süren ya
// da düşmüş) cases'li satırı: CH'deki karşılığı bayat "running" olabilir,
// bellek kopyası o yazımın ta kendisi.
func (b *evalJobBook) finishedRow(id string) (chstore.EvalRun, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.findLocked(id)
	if j == nil || j.row.Status == evalStatusRunning {
		return chstore.EvalRun{}, false
	}
	return j.rowLocked(true), true
}

// isLast — kimlik son yazımı düşen işinse true: CH'deki taze "running"
// satırı başka pod'un koşusu değil, bu sürecin BİTMİŞ koşusu.
func (b *evalJobBook) isLast(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.last != nil && b.last.row.ID == id
}

// cancelIfRunning — iş bu süreçte ve hâlâ "running" ise iptal işaretler.
func (b *evalJobBook) cancelIfRunning(id string) (context.CancelFunc, string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	j := b.running
	if j == nil || j.row.ID != id || j.row.Status != evalStatusRunning || j.cancel == nil {
		return nil, "", false
	}
	j.cancelled = true
	return j.cancel, fmt.Sprintf("done=%d/%d", j.row.Done, j.row.Total), true
}

// ── Handler'lar ─────────────────────────────────────────────────────────

func (s *Server) getAIEvalsetCatalog(w http.ResponseWriter, r *http.Request) {
	cat, err := s.evalsetCatalog()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, cat)
}

// writeEvalJSONStatus — 200 dışı kodla JSON gövde (202 / 409 çift gövde).
func writeEvalJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeEvalConflict(w http.ResponseWriter, run evalRunSummary) {
	writeEvalJSONStatus(w, http.StatusConflict, map[string]any{"error": "bir değerlendirme koşusu zaten sürüyor", "run": run})
}

// newEvalRunID — "ev-<base36 ms>-<4 hex>": zaman sıralı okunur, çakışmaz.
func newEvalRunID(now time.Time) string {
	return "ev-" + strconv.FormatInt(now.UnixMilli(), 36) + "-" + newRandID(2)
}

func (s *Server) startAIEvalsetRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Surfaces []string `json:"surfaces"`
	}
	// Boş gövde = tümü (FE düğmesi seçim yokken gövdesiz gönderebilir).
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	uid, email := "", ""
	if c := auth.FromContext(r.Context()); c != nil {
		uid, email = c.UserID, c.Email
	}
	startedBy := email
	if startedBy == "" {
		startedBy = uid
	}
	now := time.Now().UTC()
	pid, _, _, model, _ := s.evalProfileFor("") // haritasız yüzey = varsayılan profil
	row := chstore.EvalRun{
		ID: newEvalRunID(now), StartedAt: now, UpdatedAt: now, Status: evalStatusRunning,
		StartedBy: startedBy, AppVersion: s.evalAppVersion(), PromptVersion: copilot.PromptVersion(),
		Model: model, ProfileID: pid, Surfaces: []string{},
	}
	st := evalRunStoreFor(s)

	all, err := evalsetCasesLoader()
	if err != nil {
		// Koşucu düzeyi hata (spec: failed). Gömülü küme testle doğrulandığı
		// için pratikte ulaşılmaz; ulaşılırsa geçmişte GÖRÜNÜR kalsın.
		row.Status, row.Error, row.FinishedAt = evalStatusFailed, "fikstür yüklenemedi: "+err.Error(), now
		_ = s.persistEvalRun(st, row)
		s.audit(r, "ai.evalset.run", "ai_evalset", row.ID, "surfaces=all cases=0 failed=fixture")
		writeEvalJSONStatus(w, http.StatusAccepted, map[string]any{"run": evalSummaryFromRow(row, now, true)})
		return
	}
	cases, surfaces, err := evalSelectCases(all, in.Surfaces)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	row.Surfaces, row.Total = surfaces, uint32(len(cases))

	// Süreç-içi kapı ÖNCE: kendi koşumuz varsa 409 gövdesi bellekteki taze
	// kopya olsun (CH'deki ilerleme satırı bir vaka geride).
	if sum := evalJobs.current(); sum != nil {
		writeEvalConflict(w, *sum)
		return
	}
	// Çapraz-pod kapısı: en yeni satır taze "running" ise başka pod koşuyor
	// olabilir. Okuma düşerse süreç-içi kapı yine geçerli (en iyi çaba).
	// v0.10.940 — son yazımı düşen KENDİ koşumuzun bayat satırı engel değil.
	if st != nil {
		if latest, lerr := st.ListEvalRuns(r.Context(), 1); lerr != nil {
			log.Printf("[evalset] çapraz-pod kontrolü okunamadı: %v", lerr)
		} else if len(latest) == 1 && evalRunBlocksStart(latest[0], now) && !evalJobs.isLast(latest[0].ID) {
			writeEvalConflict(w, evalSummaryFromRow(latest[0], now, false))
			return
		}
	}

	// Aynı süreçte eşzamanlı iki POST: CH okuması sırasında öteki kaydolmuş
	// olabilir — kontrol-ve-kayıt tek kilit altında.
	evalJobs.mu.Lock()
	if cur := evalJobs.running; cur != nil {
		sum := cur.summaryLocked()
		evalJobs.mu.Unlock()
		writeEvalConflict(w, sum)
		return
	}
	runCtx, cancel := context.WithCancel(context.Background())
	runCtx, release := context.WithTimeout(runCtx, evalRunDeadline)
	job := &evalJob{row: row, plan: cases, cancel: cancel, deadline: evalRunDeadline, userID: uid, userEmail: email}
	evalJobs.running = job
	sum := job.summaryLocked()
	first := job.rowLocked(false)
	evalJobs.mu.Unlock()

	_ = s.persistEvalRun(st, first)
	scope := "all"
	if len(surfaces) > 0 {
		scope = strings.Join(surfaces, ",")
	}
	s.audit(r, "ai.evalset.run", "ai_evalset", row.ID, fmt.Sprintf("surfaces=%s cases=%d", scope, len(cases)))
	go s.runEvalJob(runCtx, func() { release(); cancel() }, job, st)
	writeEvalJSONStatus(w, http.StatusAccepted, map[string]any{"run": sum})
}

func (s *Server) listAIEvalsetRuns(w http.ResponseWriter, r *http.Request) {
	var rows []chstore.EvalRun
	if st := evalRunStoreFor(s); st != nil {
		var err error
		if rows, err = st.ListEvalRuns(r.Context(), evalRunsKeep); err != nil {
			writeErr(w, err)
			return
		}
	}
	writeJSON(w, map[string]any{"runs": evalMergeRunList(rows, evalJobs.owned(), time.Now(), evalRunsKeep)})
}

func (s *Server) getAIEvalsetRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if sum, cases, ok := evalJobs.snapshot(id); ok {
		writeJSON(w, map[string]any{"run": sum, "cases": cases})
		return
	}
	row, err := s.loadEvalRun(r.Context(), id)
	if err != nil {
		writeErr(w, err)
		return
	}
	if row == nil {
		writeJSONError(w, http.StatusNotFound, "koşu bulunamadı")
		return
	}
	writeJSON(w, map[string]any{"run": evalSummaryFromRow(*row, time.Now(), false), "cases": evalCasesFromRow(*row)})
}

// loadEvalRun — nokta okuma; depolama yoksa ya da kimlik geçersizse nil.
func (s *Server) loadEvalRun(ctx context.Context, id string) (*chstore.EvalRun, error) {
	st := evalRunStoreFor(s)
	if st == nil || id == "" || len(id) > evalRunIDMax {
		return nil, nil
	}
	return st.GetEvalRun(ctx, id)
}

func (s *Server) cancelAIEvalsetRun(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	cancel, progress, ok := evalJobs.cancelIfRunning(id)
	if !ok {
		// Başka pod'un koşusu buradan durdurulamaz (defter süreç yerel).
		writeJSONError(w, http.StatusConflict, "koşu bu sunucuda sürmüyor ya da bitti")
		return
	}
	cancel()
	s.audit(r, "ai.evalset.cancel", "ai_evalset", id, progress)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) compareAIEvalsetRuns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	base, head := strings.TrimSpace(q.Get("base")), strings.TrimSpace(q.Get("head"))
	if base == "" || head == "" || base == head {
		writeJSONError(w, http.StatusBadRequest, "base ve head iki farklı koşu kimliği olmalı")
		return
	}
	var rows [2]chstore.EvalRun
	for i, id := range []string{base, head} {
		// v0.10.940 — defterdeki bitmiş iş önce: son yazımı düştüyse (ya da
		// denemesi sürüyorsa) CH satırı bayat "running", cases'i boş.
		row, ok := evalJobs.finishedRow(id)
		if !ok {
			stored, err := s.loadEvalRun(r.Context(), id)
			if err != nil {
				writeErr(w, err)
				return
			}
			if stored != nil {
				row, ok = *stored, true
			}
		}
		// Yalnız BİTMİŞ koşu kıyaslanır: sürenin cases kolonu boş, failed'ın
		// eksik — yarım koşu "yeni FAIL" diye yalan söylerdi.
		if !ok || (row.Status != evalStatusDone && row.Status != evalStatusCancelled) {
			writeJSONError(w, http.StatusNotFound, "koşu bulunamadı ya da bitmedi: "+id)
			return
		}
		rows[i] = row
	}
	writeJSON(w, evalCompareRuns(rows[0], rows[1]))
}

// ── Koşu ────────────────────────────────────────────────────────────────

// persistEvalRun — en iyi çaba: yazım düşerse koşu SÜRER (bellek kopyası
// GET'e hizmet eder), log'a düşer. Kendi bağlamı: iptal edilmiş koşunun
// son "cancelled" yazımı iptal edilen bağlamla gidemez. v0.10.940 — hata
// döner: başlangıç/ilerleme yok sayar, son yazım yeniden dener.
func (s *Server) persistEvalRun(st evalRunStore, row chstore.EvalRun) error {
	if st == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := st.SaveEvalRun(ctx, row)
	if err != nil {
		log.Printf("[evalset] koşu %s yazılamadı (%s): %v", row.ID, row.Status, err)
	}
	return err
}

// persistEvalRunFinal — v0.10.940: son yazım (cases'i taşıyan TEK yazım)
// evalFinalSaveWaits aralıklarıyla yeniden denenir; her deneme
// persistEvalRun'un taze 10 sn bağlamıyla. Son hata döner.
func (s *Server) persistEvalRunFinal(st evalRunStore, row chstore.EvalRun) error {
	waits := evalFinalSaveWaits
	if len(waits) == 0 {
		waits = []time.Duration{0}
	}
	var err error
	for _, wait := range waits {
		if wait > 0 {
			time.Sleep(wait)
		}
		if err = s.persistEvalRun(st, row); err == nil {
			return nil
		}
	}
	log.Printf("[evalset] koşu %s son yazımı %d denemede düştü — sonuçlar bu süreçte bellekte tutuluyor", row.ID, len(waits))
	return err
}

// evalsetServerCall — sunucu koşucusunun evalCallFn'i: tek yol aiCall
// (ai_calls satırı + span + JSON kipi). Şema adı aiCall'da yüzey etiketidir
// ("evalset-X"); üretimde "chat-intent" vb. — ad modele giden bir yönerge
// değil, json_schema.name etiketi.
func (s *Server) evalsetServerCall(ctx context.Context, surface, system, user string, schema map[string]any, json bool) (string, error) {
	return s.aiCall(ctx, nil, system, user, aiCallOpts{surface: evalsetSurfacePrefix + surface, json: json, schema: schema})
}

// runEvalJob — kopuk goroutine. Vakalar ARDIŞIK; her vaka sonrası ilerleme
// yazımı; sonda durum + cases. release bağlamları bırakır.
func (s *Server) runEvalJob(ctx context.Context, release func(), job *evalJob, st evalRunStore) {
	status, errText := evalStatusDone, ""
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[evalset] koşu %s panik: %v", job.row.ID, rec)
			status, errText = evalStatusFailed, "panik — log'a bakın"
		}
		release()
		s.finishEvalJob(job, st, status, errText)
	}()
	for _, c := range job.plan {
		if ctx.Err() != nil {
			break
		}
		pid, _, _, model, _ := s.evalProfileFor(c.Surface)
		cctx := copilot.WithMeta(ctx, copilot.CallMeta{
			Surface: evalsetSurfacePrefix + c.Surface, UserID: job.userID, UserEmail: job.userEmail,
			ExchangeID: job.row.ID + "/" + c.ID, Shield: aiShield,
		})
		if pid != "" {
			cctx = copilot.WithProfile(cctx, pid)
		}
		out := runEvalsetCase(cctx, s.evalsetServerCall, c)
		if out.Err != nil && ctx.Err() != nil {
			break // iptal/tavanla YARIDA kesilen vaka modelin sonucu değil — sayılmaz
		}
		row := evalJobs.record(job, evalCaseResultFrom(c, out, pid, model), out.Rubric)
		_ = s.persistEvalRun(st, row)
	}
	evalJobs.mu.Lock()
	attempted, cancelled := int(job.row.Done) == len(job.plan), job.cancelled
	evalJobs.mu.Unlock()
	switch {
	case attempted:
	case cancelled:
		status = evalStatusCancelled
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		status, errText = evalStatusFailed, fmt.Sprintf("koşu tavanı aşıldı (%d dk) — kalan vakalar koşulmadı", int(job.deadline/time.Minute))
	}
}

// finishEvalJob — son durum bellekte, sonra CH'ye (cases dahil), en son
// defterden düşer: sıralama, yazım sürerken gelen GET'in bayat "running"
// ilerleme satırını görmesini önler.
//
// v0.10.940 — son yazım koşunun vaka sonuçlarını taşıyan TEK yazım: düşerse
// eskiden yalnız log'lanıp defterden atılıyordu → CH'de "running"+boş cases
// kalır, sahip pod dahil 15 dk 409, iptal 409, sonra sonsuza dek
// "abandoned" ve sonuçlar kayıp. Şimdi slot bırakılmadan yeniden denenir
// (iş running'de kalır: detay/409 gövdesi bitmiş bellek kopyasından; durum
// türetilmez, ownedHere=true); hepsi düşerse iş evalJobs.last'a geçer.
// Tutarsa önceki düşmüş işin satırı bir kez daha yazılır (depo az önce
// yazdı) — o da slot bizdeyken, ki last'a başka yazan olmasın.
func (s *Server) finishEvalJob(job *evalJob, st evalRunStore, status, errText string) {
	evalJobs.mu.Lock()
	job.row.Status, job.row.Error = status, errText
	job.row.UpdatedAt = evalStamp(job.row.UpdatedAt)
	job.row.FinishedAt = job.row.UpdatedAt
	row := job.rowLocked(true)
	evalJobs.mu.Unlock()

	err := s.persistEvalRunFinal(st, row)
	log.Printf("[evalset] koşu %s bitti: %s pass=%d fail=%d skipped=%d/%d", row.ID, status, row.Pass, row.Fail, row.Skipped, row.Total)

	var healed *evalJob
	if err == nil {
		evalJobs.mu.Lock()
		prev := evalJobs.last
		var prevRow chstore.EvalRun
		if prev != nil {
			prevRow = prev.rowLocked(true)
		}
		evalJobs.mu.Unlock()
		if prev != nil && s.persistEvalRun(st, prevRow) == nil {
			healed = prev
		}
	}

	evalJobs.mu.Lock()
	if evalJobs.running == job {
		evalJobs.running = nil
	}
	switch {
	case err != nil:
		evalJobs.last = job // tek yuva: önceki düşmüş iş (varsa) yerini yeniye bırakır
	case healed != nil && evalJobs.last == healed:
		evalJobs.last = nil
	}
	evalJobs.mu.Unlock()
}
