package api

// rollouts_v2_probe.go — v0.10.979 — ROLLOUTS v2 §11 SORGU PAKETİ: ADMIN PROBE
// (docs/rollouts/v2-audit.md §11.0–11.9; docs/rollouts/v2-probe-runbook.md;
// operatör onayı 2026-09-27 "önerin → 1").
//
// api.go BÜYÜMEZ (TestApiGoDoesNotGrow): iki rota burada, kayıt init()'te
// route_registry.go defterine ("rollouts-v2-probe" adıyla).
//
//   POST /api/admin/rollouts-v2/probe   admin + audit rollouts_v2.probe — ASENKRON, salt-okunur koşu başlatır (202)
//   GET  /api/admin/rollouts-v2/probe   admin — koşu durumu (202) ya da son rapor (200; ?format=md indirme)
//
// ── NEDEN ASENKRON, "keşif gibi tek HTTP çağrısı" DEĞİL ─────────────────
//
// Koşu ~330 çağrı (iki hedef + iki hub); H6 `[24h]` sorguları tek başına
// 10–30 s. Senkron cevap OpenShift Route'un 30 s varsayılanını aşar. POST
// hemen 202 döner, goroutine context.WithoutCancel(r.Context()) + bütçe ile
// koşar (argocd_settings_routes.go SavePersisted emsali), operatör GET ile
// yoklar. Rapor bu POD'un belleğinde 1 saat yaşar (çok replikalı api'de
// başka pod 404 `none` + `pod` alanı; runbook pod sabitlemeyi anlatır).
//
// ── NEDEN ADMIN PROBE, SHELL SCRIPT DEĞİL ────────────────────────────────
//
// Operatörün eline Thanos token'ı geçmez: Remote Cluster kimliği (TokenRef,
// WorkerQuery'de fail-closed) süreç içinde kullanılır; konsol ailesi fail-
// closed değil (console.go), bu yüzden guardrail çözülmemiş TokenRef'i
// koşudan ÖNCE reddeder. Bütün DEĞERLER v2probe jetonlayıcısından geçer;
// eşleme goroutine'i terk etmez, hiçbir yerde loglanmaz; rapor sohbete
// yapıştırılabilir. GET de admin: rapor upstream uyarı metnini harfi
// harfine taşır ve bu veri için viewer yüzeyi yok — "okuma kardeşi kapısız"
// kuralının belgeli istisnası.
//
// ── OKUYUCULAR (yeni taşıma YOK) ─────────────────────────────────────────
//
//   anlık (hedef K/D/R, hub H/N/R matcher'lı) → thanos.WorkerQuery
//     (dedup=true, partial_response=false, timeout=30s, fail-closed token);
//   K0.4b / H0.2b → WorkerQuery Dedup=&false (bilinçli HA-ham okuma);
//   hub H0–H1 MATCHER'SIZ → thanos.ConsoleQuery etiketi silinmiş kopyaya,
//     Dedup=&true (thanos v0.10.979 ek alanı): çift YALNIZ matcher'da ayrışır;
//   injectClusterLabel=false hub → bütün anlık çağrılar aynı kopyayla
//     (WorkerQuery kayıt kopyasını enjekte ederdi); ayrı matcher'sız geçiş yok;
//   etiket adları / __name__ değerleri → ConsoleLabels / ConsoleLabelValues
//     (start/end her zaman, limit 500, EffectiveMatchers enjekte eder);
//   ConsoleSeries BİLEREK yok (§11.0: name/repo/dest_server gezilmez);
//   T → chstore.RolloutProbeSpanCoverage + EntitySeenClusterValues (test dikişi).
//
// Erken durma (§11 "tekrarlanan unauthorized/unreachable'da dur"): birimde
// ardışık 2 unauthorized|unreachable ya da 3 timeout → o birimin kalanı
// "skipped (streak)", öteki birimler sürer. Koşu bütçesi (bağlam ya da 400
// çağrı) → kalan her sorgu "skipped: run budget exhausted", durum
// budget_exhausted. Panik → failed; audit satırı yine yazılır — döngü İÇİ
// panik kısmi sonuçları yine Finalize eder, döngü DIŞI panik (Finalize /
// audit) fonksiyon düzeyi defer'da asgari raporla yakalanır; busy bayrağı
// ve done kanalı her çıkışta tam bir kez bırakılır (v0.10.979). Her koşu
// için TAM BİR audit satırı (sonuç/değer/jeton taşımaz).

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cilcenk/coremetry/internal/argocd"
	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/rollout/v2probe"
	"github.com/cilcenk/coremetry/internal/sourcestate"
	"github.com/cilcenk/coremetry/internal/thanos"
)

func init() { registerRoutesExtra("rollouts-v2-probe", (*Server).registerRolloutsV2ProbeRoutes) }

func (s *Server) registerRolloutsV2ProbeRoutes(mux *http.ServeMux) {
	mux.Handle("POST /api/admin/rollouts-v2/probe",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.startRolloutsV2Probe)))
	mux.Handle("GET /api/admin/rollouts-v2/probe",
		auth.RequireRole(auth.RoleAdmin, http.HandlerFunc(s.getRolloutsV2Probe)))
}

const (
	rolloutsV2ProbeReqMaxBody    = 8 << 10
	rolloutsV2ProbeMaxCalls      = 400
	rolloutsV2ProbeCallTimeout   = 30 * time.Second
	rolloutsV2ProbeTTL           = time.Hour
	rolloutsV2ProbeMaxSeries     = 2000 // §11 sonuçları ön-toplanmış (en büyüğü onlarca satır): sızıntı kalkanı
	rolloutsV2ProbeMaxBodyMiB    = 8
	rolloutsV2ProbeMetaBodyB     = int64(4 << 20)
	rolloutsV2ProbeMetaLimit     = 500
	rolloutsV2ProbeBudgetMinS    = 60
	rolloutsV2ProbeBudgetMaxS    = 600
	rolloutsV2ProbeAuditClip     = 4 << 10
	rolloutsV2ProbeAuthStreak    = 2
	rolloutsV2ProbeTimeoutStreak = 3
	rolloutsV2ProbeTWindow       = 15 * time.Minute
	rolloutsV2ProbeT3Since       = 7 * 24 * time.Hour
	rolloutsV2ProbeAction        = "rollouts_v2.probe"
)

// Paket düzeyi durum (Server alanı yok — api.go büyümez; argocdDiscoverBusy emsali).
var (
	rolloutsV2ProbeBusy  atomic.Bool
	rolloutsV2ProbeState struct {
		mu  sync.Mutex
		cur *rolloutsV2ProbeRun
	}
	rolloutsV2ProbeNow             = time.Now        // test dikişi (TTL)
	rolloutsV2ProbeBudget          = 5 * time.Minute // gövde budgetS vermezse
	rolloutsV2ProbeSecondSampleGap = 30 * time.Second
	rolloutsV2ProbeSpanCoverage    = (*Server).rolloutsV2ProbeSpanCoverageCH // T1/T2 dikişi (chstore canlı CH ister)
	rolloutsV2ProbeSeenClusters    = (*Server).rolloutsV2ProbeSeenClustersCH // T3 dikişi
	rolloutsV2ProbeFinalize        = v2probe.Finalize                        // test dikişi (Finalize/Render paniği)
	rolloutsV2ProbePod             = func() string {
		h, _ := os.Hostname()
		if h == "" {
			return "unknown"
		}
		return h
	}()
)

type rolloutsV2ProbeRequest struct {
	Targets    []string `json:"targets"`
	Hubs       []string `json:"hubs"`
	Packs      []string `json:"packs"`
	EnvList    []string `json:"envList"`
	SuffixList []string `json:"suffixList"`
	Options    struct {
		WithoutMatcher *bool           `json:"withoutMatcher"`
		DedupCheck     *bool           `json:"dedupCheck"`
		SecondSample   *bool           `json:"secondSample"`
		HubInject      map[string]bool `json:"hubInject"`
		BudgetS        int             `json:"budgetS"`
	} `json:"options"`
}

// rolloutsV2ProbeUnit — plandaki bir hedef ya da hub.
type rolloutsV2ProbeUnit struct {
	ID, Token, Role string
	Cfg             thanos.ClusterConfig // kayıt kopyası; inject=false hub'da etiket silinmiş
	Inject          bool
	WithoutMatcher  bool
	Params          v2probe.Params
	// koşu durumu
	calls        int
	durationMs   int64
	streak       int
	tstreak      int
	stopped      sourcestate.State
	earlyStop    string
	firstSample  time.Time
	stateSummary string
}

type rolloutsV2ProbePlan struct {
	Units        []*rolloutsV2ProbeUnit
	Packs        map[v2probe.Pack]bool
	PackList     []v2probe.Pack
	Budget       time.Duration
	RunT         bool
	DedupCheck   bool
	SecondSample bool
	Planned      int
	Seeds        v2probe.Seeds
	Notes        []string
	EnvN, SfxN   int
	targetIDs    []string
	hubIDs       []string
}

type rolloutsV2ProbeRun struct {
	ID        string
	StartedAt time.Time
	plan      *rolloutsV2ProbePlan
	done      chan struct{}

	mu         sync.Mutex
	calls      int
	unitToken  string
	pack       string
	report     *v2probe.Report
	status     string
	finishedAt time.Time
	expiresAt  time.Time
}

func writeRolloutsV2Guardrail(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg, "errorType": "guardrail"})
}

// ── Plan çözümü ─────────────────────────────────────────────────────────

var rolloutsV2ProbePackSet = map[string]v2probe.Pack{"K": v2probe.PackK, "D": v2probe.PackD, "R": v2probe.PackR, "H": v2probe.PackH, "N": v2probe.PackN, "T": v2probe.PackT}

// rolloutsV2ProbeResolve — istek → plan; hata metni guardrail (yalnız id
// taşır, ad/URL/token değil). Upstream'e İSTEK YOK.
func (s *Server) rolloutsV2ProbeResolve(req rolloutsV2ProbeRequest) (*rolloutsV2ProbePlan, string) {
	cfg := s.thanos.CurrentSettings()
	snap := s.thanos.Snapshot()
	enabled := map[string]thanos.ClusterConfig{}
	for _, c := range cfg.Clusters {
		if c.Enabled {
			enabled[c.EffectiveID()] = c
		}
	}
	var argo argocd.Settings
	if svc := argocdSettingsSvc.Load(); svc != nil {
		argo = svc.Current()
	}
	plan := &rolloutsV2ProbePlan{Packs: map[v2probe.Pack]bool{}, DedupCheck: true, SecondSample: true}

	// Paketler.
	if len(req.Packs) == 0 {
		for _, p := range v2probe.AllPacks {
			plan.Packs[p] = true
		}
	}
	for _, p := range req.Packs {
		pk, ok := rolloutsV2ProbePackSet[strings.ToUpper(strings.TrimSpace(p))]
		if !ok {
			return nil, "bilinmeyen paket: " + strings.TrimSpace(p) + " (K, D, R, H, N, T)"
		}
		plan.Packs[pk] = true
	}
	for _, p := range v2probe.AllPacks {
		if plan.Packs[p] {
			plan.PackList = append(plan.PackList, p)
		}
	}
	plan.RunT = plan.Packs[v2probe.PackT]

	// Hub'lar: gövde ya da argocd hubs[] (etkin olanlar).
	hubIDs := []string{}
	if len(req.Hubs) > 0 {
		for _, id := range req.Hubs {
			id = strings.TrimSpace(id)
			if _, ok := enabled[id]; !ok {
				return nil, "hub Remote Cluster bilinmiyor ya da devre dışı: " + id
			}
			hubIDs = append(hubIDs, id)
		}
	} else {
		for _, h := range argo.Hubs {
			if _, ok := enabled[h.ClusterID]; ok {
				hubIDs = append(hubIDs, h.ClusterID)
			}
		}
	}
	hubSet := map[string]bool{}
	for _, id := range hubIDs {
		if hubSet[id] {
			return nil, "hub iki kez verildi: " + id
		}
		hubSet[id] = true
	}
	// Hedefler: gövde ya da etkin kümeler − hub'lar; EffectiveID sıralı.
	targetIDs := []string{}
	if len(req.Targets) > 0 {
		for _, id := range req.Targets {
			id = strings.TrimSpace(id)
			if _, ok := enabled[id]; !ok {
				return nil, "hedef Remote Cluster bilinmiyor ya da devre dışı: " + id
			}
			if hubSet[id] {
				return nil, "bir küme hem hub hem hedef olamaz: " + id
			}
			targetIDs = append(targetIDs, id)
		}
	} else {
		for id := range enabled {
			if !hubSet[id] {
				targetIDs = append(targetIDs, id)
			}
		}
	}
	sort.Strings(targetIDs)
	seen := map[string]bool{}
	for _, id := range targetIDs {
		if seen[id] {
			return nil, "hedef iki kez verildi: " + id
		}
		seen[id] = true
	}
	if len(targetIDs) == 0 && len(hubIDs) == 0 && !plan.RunT {
		return nil, "boş plan: hedef yok, hub yok, T paketi istenmedi"
	}
	// Fail-closed token: seçilen her küme için çözülmemiş TokenRef → istek YOK.
	for _, id := range append(append([]string{}, targetIDs...), hubIDs...) {
		for _, c := range snap.Clusters {
			if c.ID == id && c.TokenRef != "" && !c.TokenResolved {
				return nil, "Remote Cluster tokenRef'i çözülemedi — kimliksiz istek gönderilmez: " + id
			}
		}
	}
	plan.targetIDs, plan.hubIDs = targetIDs, hubIDs

	// envList / suffixList / pairGroups.
	envList := req.EnvList
	if len(envList) == 0 {
		envList = argo.EnvList
	}
	envList = rolloutsV2ProbeCleanList(envList)
	suffixList := req.SuffixList
	if len(suffixList) == 0 {
		for _, c := range enabled {
			if c.ArgoSuffix != "" {
				suffixList = append(suffixList, c.ArgoSuffix)
			}
		}
		sort.Strings(suffixList) // map sırası rastgele; şablon (ve jeton indeksi) deterministik olsun
	}
	suffixList = rolloutsV2ProbeCleanList(suffixList)
	plan.EnvN, plan.SfxN = len(envList), len(suffixList)
	groups := map[string][]string{}
	for _, c := range enabled {
		if c.PairGroup != "" && c.ArgoSuffix != "" {
			groups[c.PairGroup] = append(groups[c.PairGroup], c.ArgoSuffix)
		}
	}
	groupNames := make([]string, 0, len(groups))
	for g, sfx := range groups {
		if len(sfx) >= 2 {
			groupNames = append(groupNames, g)
		}
	}
	sort.Strings(groupNames)
	pairGroups := map[string][]string{}
	for i, g := range groupNames {
		sfx := append([]string(nil), groups[g]...)
		sort.Strings(sfx)
		pairGroups["pair-"+strconv.Itoa(i+1)] = sfx // sentetik anahtar: ham pairGroup adı rapora girmez
	}

	// Seçenekler.
	withoutMatcher := req.Options.WithoutMatcher == nil || *req.Options.WithoutMatcher
	if req.Options.DedupCheck != nil {
		plan.DedupCheck = *req.Options.DedupCheck
	}
	if req.Options.SecondSample != nil {
		plan.SecondSample = *req.Options.SecondSample
	}
	plan.Budget = rolloutsV2ProbeBudget
	if b := req.Options.BudgetS; b > 0 {
		if b < rolloutsV2ProbeBudgetMinS {
			b = rolloutsV2ProbeBudgetMinS
		}
		if b > rolloutsV2ProbeBudgetMaxS {
			b = rolloutsV2ProbeBudgetMaxS
		}
		plan.Budget = time.Duration(b) * time.Second
	}

	// Birimler + tohumlar.
	seeds := v2probe.Seeds{APIHosts: map[string]string{}, EnvList: envList, SuffixList: suffixList}
	addSeed := func(token string, c thanos.ClusterConfig) {
		raw := append([]string{c.EffectiveID(), c.Name}, c.SpanClusterKeys()...)
		if _, v := c.EffectiveThanosLabel(); v != "" {
			raw = append(raw, v)
		}
		seeds.Clusters = append(seeds.Clusters, v2probe.Alias{Token: token, Raw: raw})
		for _, u := range c.APIServerURLs {
			if pu, err := url.Parse(strings.TrimSpace(u)); err == nil && pu.Host != "" {
				seeds.APIHosts[strings.ToLower(pu.Host)] = token
				seeds.APIHosts[strings.ToLower(pu.Hostname())] = token
			}
		}
	}
	for i, id := range targetIDs {
		c := enabled[id]
		token := "cluster-" + rolloutsV2ProbeLetters(i)
		addSeed(token, c)
		plan.Units = append(plan.Units, &rolloutsV2ProbeUnit{ID: id, Token: token, Role: "target", Cfg: c, Inject: true,
			Params: v2probe.Params{NSMatcher: thanos.NamespaceMatcher(c.NamespaceFilter), K5Window: "6h"}})
	}
	for i, id := range hubIDs {
		c := enabled[id]
		token := "hub-" + strconv.Itoa(i+1)
		addSeed(token, c)
		inject := true
		if h, ok := argo.HubByID(id); ok {
			inject = h.Inject()
		}
		if v, ok := req.Options.HubInject[id]; ok {
			inject = v
		}
		u := &rolloutsV2ProbeUnit{ID: id, Token: token, Role: "hub", Cfg: c, Inject: inject}
		if !inject {
			u.Cfg.ThanosLabelName, u.Cfg.ThanosLabelValue = "", ""
			plan.Notes = append(plan.Notes, token+": injectClusterLabel=false — cluster matcher never injected; without-matcher pass skipped (operator already decided injection is wrong)")
		} else if c.ThanosLabelName != "" && withoutMatcher {
			u.WithoutMatcher = true
		}
		var inst []string
		for _, in := range argo.Instances {
			if in.HubClusterID == id && in.Enabled && in.HubNamespace != "" {
				inst = append(inst, in.HubNamespace)
			}
		}
		u.Params = v2probe.Params{EnvList: envList, SuffixList: suffixList, PairGroups: pairGroups, InstanceNS: inst, K5Window: "6h", WithoutMatcher: u.WithoutMatcher}
		plan.Units = append(plan.Units, u)
	}
	plan.Seeds = seeds
	if len(hubIDs) == 0 && (plan.Packs[v2probe.PackH] || plan.Packs[v2probe.PackN]) {
		plan.Notes = append(plan.Notes, "H/N packs: skipped — no Argo hub configured (argocd settings hubs[] empty or disabled)")
	}
	if plan.Packs[v2probe.PackN] && len(hubIDs) > 0 && (plan.EnvN == 0 || plan.SfxN == 0) {
		plan.Notes = append(plan.Notes, "N pack: skipped — envList/suffixList empty; pass envList and suffixList in the body")
	}
	plan.Planned = plan.countPlanned()
	return plan, ""
}

func rolloutsV2ProbeCleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func rolloutsV2ProbeLetters(i int) string {
	s := ""
	for {
		s = string(rune('a'+i%26)) + s
		i = i/26 - 1
		if i < 0 {
			return s
		}
	}
}

// packsFor — birimin rolünde koşacak paketler, yürütme sırasında.
func (p *rolloutsV2ProbePlan) packsFor(u *rolloutsV2ProbeUnit) []v2probe.Pack {
	var order []v2probe.Pack
	if u.Role == "target" {
		order = []v2probe.Pack{v2probe.PackK, v2probe.PackD, v2probe.PackR}
	} else {
		order = []v2probe.Pack{v2probe.PackH, v2probe.PackN, v2probe.PackR}
	}
	out := order[:0:0]
	for _, pk := range order {
		if p.Packs[pk] {
			out = append(out, pk)
		}
	}
	return out
}

// instances — bir satırın bu birimdeki çağrıları (seçenek süzgeci dahil);
// skip: satır koşmaz, sebep.
func (p *rolloutsV2ProbePlan) instances(u *rolloutsV2ProbeUnit, q v2probe.Query) ([]v2probe.Instance, string) {
	insts, err := v2probe.Expand(q, u.Params)
	if err != nil {
		if errors.Is(err, v2probe.ErrPlaceholderEmpty) {
			return nil, "skipped: envList/suffixList empty — pass envList and suffixList in the body"
		}
		return nil, "skipped: " + err.Error()
	}
	if len(insts) == 0 {
		return nil, "skipped: no pair group with ≥ 2 suffixes (Remote Cluster pairGroup/argoSuffix)"
	}
	out := insts[:0:0]
	for _, in := range insts {
		switch in.Variant {
		case "dedup_off":
			if !p.DedupCheck {
				continue
			}
		case "second_sample":
			if !p.SecondSample {
				continue
			}
		}
		out = append(out, in)
	}
	return out, ""
}

func (p *rolloutsV2ProbePlan) countPlanned() int {
	n := 0
	if p.RunT {
		n += 3
	}
	for _, u := range p.Units {
		for _, pk := range p.packsFor(u) {
			for _, q := range v2probe.ByPack(pk) {
				if !q.Runs(u.Role) || q.FallbackFor != "" {
					continue
				}
				insts, _ := p.instances(u, q)
				n += len(insts)
			}
		}
	}
	return n
}

// ── POST ─────────────────────────────────────────────────────────────────

func (s *Server) startRolloutsV2Probe(w http.ResponseWriter, r *http.Request) {
	if s.thanos == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "Remote Cluster servisi bağlı değil")
		return
	}
	var req rolloutsV2ProbeRequest
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, rolloutsV2ProbeReqMaxBody))
	if err != nil {
		writeRolloutsV2Guardrail(w, "gövde okunamadı ya da çok büyük")
		return
	}
	if len(strings.TrimSpace(string(body))) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeRolloutsV2Guardrail(w, "geçersiz JSON: "+err.Error())
			return
		}
	}
	plan, msg := s.rolloutsV2ProbeResolve(req)
	if msg != "" {
		writeRolloutsV2Guardrail(w, msg)
		return
	}
	if !rolloutsV2ProbeBusy.CompareAndSwap(false, true) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "bir Rollouts v2 sorgu paketi zaten koşuyor", "errorType": "busy"})
		return
	}
	var idb [6]byte
	_, _ = rand.Read(idb[:])
	run := &rolloutsV2ProbeRun{ID: hex.EncodeToString(idb[:]), StartedAt: rolloutsV2ProbeNow(), plan: plan, done: make(chan struct{}), status: "running"}
	rolloutsV2ProbeState.mu.Lock()
	rolloutsV2ProbeState.cur = run // önceki rapor düşer
	rolloutsV2ProbeState.mu.Unlock()

	claims := auth.FromContext(r.Context())
	ip := clientIP(r)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), plan.Budget)
	go func() {
		defer cancel()
		s.runRolloutsV2Probe(ctx, run, claims, ip)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"runId": run.ID, "status": "running", "pod": rolloutsV2ProbePod, "startedAt": run.StartedAt.UnixMilli(),
		"budgetS": int(plan.Budget / time.Second), "targets": plan.targetIDs, "hubs": plan.hubIDs,
		"packs": plan.PackList, "planned": plan.Planned,
	})
}

// ── GET ──────────────────────────────────────────────────────────────────

type rolloutsV2ProbeResponse struct {
	*v2probe.Report
	ExpiresAt int64 `json:"expiresAt"`
}

func (s *Server) getRolloutsV2Probe(w http.ResponseWriter, r *http.Request) {
	rolloutsV2ProbeState.mu.Lock()
	run := rolloutsV2ProbeState.cur
	rolloutsV2ProbeState.mu.Unlock()
	none := func() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "none", "pod": rolloutsV2ProbePod})
	}
	if run == nil {
		none()
		return
	}
	run.mu.Lock()
	report, status, expires := run.report, run.status, run.expiresAt
	calls, unit, pack := run.calls, run.unitToken, run.pack
	run.mu.Unlock()
	if report == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": status, "runId": run.ID, "pod": rolloutsV2ProbePod, "startedAt": run.StartedAt.UnixMilli(),
			"budgetS":  int(run.plan.Budget / time.Second),
			"progress": map[string]any{"calls": calls, "planned": run.plan.Planned, "unit": unit, "pack": pack},
		})
		return
	}
	if !rolloutsV2ProbeNow().Before(expires) {
		rolloutsV2ProbeState.mu.Lock()
		if rolloutsV2ProbeState.cur == run {
			rolloutsV2ProbeState.cur = nil
		}
		rolloutsV2ProbeState.mu.Unlock()
		none()
		return
	}
	if r.URL.Query().Get("format") == "md" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="rollouts-v2-probe-`+run.ID+`.md"`)
		_, _ = io.WriteString(w, report.Markdown)
		return
	}
	writeJSON(w, rolloutsV2ProbeResponse{Report: report, ExpiresAt: expires.UnixMilli()})
}

// ── Koşu ─────────────────────────────────────────────────────────────────

type rolloutsV2ProbeRunner struct {
	s         *Server
	ctx       context.Context
	run       *rolloutsV2ProbeRun
	plan      *rolloutsV2ProbePlan
	raw       *v2probe.RawRun
	calls     int
	exhausted bool
	now       time.Time
	// T sözde birimi
	tCalls      int
	tDurationMs int64
	tState      string
}

func (s *Server) runRolloutsV2Probe(ctx context.Context, run *rolloutsV2ProbeRun, claims *auth.Claims, ip string) {
	plan := run.plan
	started := time.Now()
	raw := &v2probe.RawRun{RunID: run.ID, Pod: rolloutsV2ProbePod, StartedAt: run.StartedAt, BudgetS: int(plan.Budget / time.Second),
		Planned: plan.Planned, Packs: plan.PackList, Skipped: append([]string{}, plan.Notes...), EnvListN: plan.EnvN, SuffixListN: plan.SfxN}
	rn := &rolloutsV2ProbeRunner{s: s, ctx: ctx, run: run, plan: plan, raw: raw, now: run.StartedAt}
	status := "done"
	// v0.10.979 — fonksiyon düzeyi tek defer: busy bayrağı ve done kanalı
	// HER çıkışta tam bir kez bırakılır. Döngü dışı panik (Finalize/Render/
	// Assess upstream verisi üzerinde, auditAs) süreci düşürmez: durum
	// `failed`, asgari rapor (tam rapor kaydedildiyse korunur), audit satırı
	// (yazımı da panikleyebilir — iç recover; busy yine bırakılır).
	defer func() {
		if p := recover(); p != nil {
			log.Printf("[rollouts-v2-probe] sonlandırma panikledi: %v", p)
			status = "failed"
			now := rolloutsV2ProbeNow()
			run.mu.Lock()
			if run.report == nil { // auditAs sonrası panikte tam rapor korunur
				run.report = &v2probe.Report{RunID: run.ID, Pod: rolloutsV2ProbePod, Status: status,
					StartedAt: run.StartedAt.UnixMilli(), FinishedAt: now.UnixMilli(), Planned: plan.Planned, Packs: plan.PackList,
					Calls: rn.calls, BudgetS: int(plan.Budget / time.Second),
					Units: []v2probe.Unit{}, Warnings: []string{}, Assumptions: []v2probe.Assumption{}, Decisions: []v2probe.Decision{},
					Results: []v2probe.Result{}, Skipped: []string{}, DeniedLabels: []string{},
					Markdown: "# Rollouts v2 §11 sorgu paketi — " + v2probe.Version + "\n\nstatus: failed (panic during finalize)\n"}
				run.finishedAt, run.expiresAt = now, now.Add(rolloutsV2ProbeTTL)
			}
			run.status = status
			run.mu.Unlock()
			func() { // audit yazımı da panikleyebilir (çift hata): busy yine bırakılır
				defer func() { _ = recover() }()
				details, _ := json.Marshal(map[string]any{"runId": run.ID, "targets": plan.targetIDs, "hubs": plan.hubIDs,
					"packs": plan.PackList, "status": status, "calls": rn.calls, "planned": plan.Planned,
					"budgetS": int(plan.Budget / time.Second), "errorType": "failed"})
				s.auditAs(claims, ip, rolloutsV2ProbeAction, "settings", "rollouts_v2_probe", promqlClip(string(details), rolloutsV2ProbeAuditClip))
			}()
		}
		rolloutsV2ProbeBusy.Store(false)
		close(run.done)
	}()
	func() {
		defer func() {
			if p := recover(); p != nil {
				log.Printf("[rollouts-v2-probe] koşu panikledi: %v", p)
				status = "failed"
			}
		}()
		if plan.RunT {
			rn.runT()
		}
		for _, u := range plan.Units {
			if u.Role == "target" {
				rn.runTarget(u)
			}
		}
		for _, u := range plan.Units {
			if u.Role == "hub" {
				rn.runHub(u)
			}
		}
	}()
	if status == "done" && (rn.exhausted || ctx.Err() != nil) {
		status = "budget_exhausted"
	}
	raw.Status = status
	raw.Calls = rn.calls
	raw.FinishedAt = run.StartedAt.Add(time.Since(started))
	if plan.RunT {
		raw.Units = append(raw.Units, v2probe.RawUnit{ID: v2probe.ClickHouseUnit, Token: v2probe.ClickHouseUnit, Role: v2probe.ClickHouseUnit,
			Calls: rn.tCalls, DurationMs: rn.tDurationMs, State: rn.tState})
	}
	unitsAudit := make([]map[string]any, 0, len(plan.Units))
	for _, u := range plan.Units {
		st := u.stateSummary
		if st == "" {
			st = "ok"
		}
		raw.Units = append(raw.Units, v2probe.RawUnit{ID: u.ID, Token: u.Token, Role: u.Role, WithoutMatcher: u.WithoutMatcher,
			NSFilter: u.Params.NSMatcher != "", Calls: u.calls, DurationMs: u.durationMs, State: st, EarlyStop: u.earlyStop})
		unitsAudit = append(unitsAudit, map[string]any{"id": u.ID, "role": u.Role, "calls": u.calls, "durationMs": u.durationMs, "state": st, "earlyStop": u.earlyStop != ""})
	}
	report := rolloutsV2ProbeFinalize(*raw, plan.Seeds)
	raw = nil // ham sonuçlar bu goroutine'de ölür
	now := rolloutsV2ProbeNow()
	run.mu.Lock()
	run.report, run.status = &report, status
	run.finishedAt, run.expiresAt = now, now.Add(rolloutsV2ProbeTTL)
	run.mu.Unlock()

	errType := ""
	if status != "done" {
		errType = status
	}
	details, _ := json.Marshal(map[string]any{
		"runId": run.ID, "targets": plan.targetIDs, "hubs": plan.hubIDs, "packs": plan.PackList, "status": status,
		"calls": rn.calls, "planned": plan.Planned, "durationMs": report.DurationMs, "budgetS": int(plan.Budget / time.Second),
		"units": unitsAudit, "errorType": errType,
	})
	s.auditAs(claims, ip, rolloutsV2ProbeAction, "settings", "rollouts_v2_probe", promqlClip(string(details), rolloutsV2ProbeAuditClip))
	// busy bayrağı + done kanalı: fonksiyon düzeyi defer (tek yer).
}

func (rn *rolloutsV2ProbeRunner) progress(unit, pack string) {
	rn.run.mu.Lock()
	rn.run.calls, rn.run.unitToken, rn.run.pack = rn.calls, unit, pack
	rn.run.mu.Unlock()
}

func (rn *rolloutsV2ProbeRunner) budgetLeft() bool {
	if rn.exhausted {
		return false
	}
	if rn.ctx.Err() != nil || rn.calls >= rolloutsV2ProbeMaxCalls {
		rn.exhausted = true
		return false
	}
	return true
}

func (rn *rolloutsV2ProbeRunner) add(res v2probe.Result) {
	rn.raw.Results = append(rn.raw.Results, res)
}

func (rn *rolloutsV2ProbeRunner) skip(u *rolloutsV2ProbeUnit, in v2probe.Instance, reason string) {
	rn.add(v2probe.Result{ID: in.ID, Unit: u.ID, Variant: in.Variant, VariantRaw: in.VariantRaw, State: sourcestate.Error, Skipped: true, Detail: reason, SampleAt: time.Now()})
}

// ── Thanos çağrısı ───────────────────────────────────────────────────────

func rolloutsV2ProbeWorkerLimits(dedup *bool) thanos.WorkerLimits {
	return thanos.WorkerLimits{TimeoutS: int(rolloutsV2ProbeCallTimeout / time.Second), MaxSeries: rolloutsV2ProbeMaxSeries,
		MaxBodyMiB: rolloutsV2ProbeMaxBodyMiB, Dedup: dedup, PartialResponse: false}
}

func rolloutsV2ProbeConsoleLimits(body int64) thanos.ConsoleLimits {
	return thanos.ConsoleLimits{Timeout: rolloutsV2ProbeCallTimeout, MaxSeries: rolloutsV2ProbeMaxSeries, MaxBodyBytes: body, PartialResponse: false}
}

// classify — thanos hatası → durum. Bağlam bütçesi → skipped.
func (rn *rolloutsV2ProbeRunner) classify(err error) (sourcestate.State, bool, string) {
	switch {
	case errors.Is(err, thanos.ErrWorkerClusterUnavailable):
		st, _ := v2probe.ClassifyError(v2probe.ErrKindClusterUnavailable, 0)
		return st, false, err.Error()
	case errors.Is(err, thanos.ErrWorkerTokenUnresolved):
		st, _ := v2probe.ClassifyError(v2probe.ErrKindTokenUnresolved, 0)
		return st, false, "cluster token reference does not resolve"
	}
	var ce *thanos.ConsoleError
	if errors.As(err, &ce) {
		if ce.Type == thanos.ConsoleErrCanceled || (ce.Type == thanos.ConsoleErrTimeout && rn.ctx.Err() != nil) {
			st, sk := v2probe.ClassifyError(v2probe.ErrKindDeadline, 0)
			return st, sk, "skipped: run budget exhausted"
		}
		st, sk := v2probe.ClassifyError(ce.Type, ce.UpstreamStatus)
		switch ce.Type {
		case thanos.ConsoleErrBadData, thanos.ConsoleErrExecution:
			return st, sk, ce.Type + ": " + ce.Message
		case thanos.ConsoleErrResponseTooLarge:
			return st, sk, "response_too_large"
		}
		return st, sk, ce.Message
	}
	if rn.ctx.Err() != nil {
		st, sk := v2probe.ClassifyError(v2probe.ErrKindDeadline, 0)
		return st, sk, "skipped: run budget exhausted"
	}
	return sourcestate.Classify(err), false, err.Error()
}

// exec — tek çağrı: bütçe + erken durma kapıları, okuyucu seçimi, sonuç.
func (rn *rolloutsV2ProbeRunner) exec(u *rolloutsV2ProbeUnit, in v2probe.Instance) *v2probe.Result {
	if u.stopped != "" {
		rn.skip(u, in, "skipped: unit "+string(u.stopped)+" (streak)")
		return nil
	}
	if !rn.budgetLeft() {
		rn.skip(u, in, "skipped: run budget exhausted")
		return nil
	}
	rn.progress(u.Token, string(in.Pack))
	rn.calls++
	u.calls++
	start := time.Now()
	res := v2probe.Result{ID: in.ID, Unit: u.ID, Variant: in.Variant, VariantRaw: in.VariantRaw, SampleAt: start}
	var (
		raw v2probe.Raw
		err error
	)
	s := rn.s
	switch in.Kind {
	case v2probe.KindInstant:
		var dedup *bool
		if in.Variant == "dedup_off" {
			f := false
			dedup = &f
		}
		nomatch := in.Variant == "nomatch" || !u.Inject
		var cr *thanos.ConsoleResult
		if nomatch {
			c := u.Cfg
			c.ThanosLabelName, c.ThanosLabelValue = "", ""
			on := true
			if dedup == nil {
				dedup = &on
			}
			cr, err = s.thanos.ConsoleQuery(rn.ctx, c, thanos.ConsoleInstantQuery{Query: in.Expr, Time: start, Dedup: dedup},
				rolloutsV2ProbeConsoleLimits(int64(rolloutsV2ProbeMaxBodyMiB)<<20))
		} else {
			cr, err = s.thanos.WorkerQuery(rn.ctx, u.ID, in.Expr, rolloutsV2ProbeWorkerLimits(dedup))
		}
		if err == nil {
			raw = v2probe.Raw{ResultType: cr.ResultType, Result: cr.Result, Warnings: cr.Warnings, Infos: cr.Infos, Total: cr.TotalSeries, Truncated: cr.Truncated}
		}
	case v2probe.KindLabels, v2probe.KindLabelValues:
		q := thanos.ConsoleMetaQuery{Match: []string{in.Expr}, Start: start.Add(-in.Window), End: start, Limit: rolloutsV2ProbeMetaLimit}
		lim := rolloutsV2ProbeConsoleLimits(rolloutsV2ProbeMetaBodyB)
		var lr *thanos.ConsoleLabelsResult
		if in.Kind == v2probe.KindLabels {
			lr, err = s.thanos.ConsoleLabels(rn.ctx, u.Cfg, q, lim)
		} else {
			lr, err = s.thanos.ConsoleLabelValues(rn.ctx, u.Cfg, in.Label, q, lim)
		}
		if err == nil {
			raw = v2probe.Raw{Values: lr.Values, Warnings: lr.Warnings, Infos: lr.Infos, Total: lr.Total, Truncated: lr.Truncated}
		}
	default:
		err = fmt.Errorf("unsupported kind")
	}
	res.DurationMs = time.Since(start).Milliseconds()
	u.durationMs += res.DurationMs
	if err == nil {
		parsed, perr := v2probe.FromRaw(in.Query, raw)
		if perr != nil {
			err = &thanos.ConsoleError{Type: thanos.ConsoleErrInternal, Message: "malformed result: " + perr.Error()}
		} else {
			parsed.ID, parsed.Unit, parsed.Variant, parsed.VariantRaw = res.ID, res.Unit, res.Variant, res.VariantRaw
			parsed.SampleAt, parsed.DurationMs = res.SampleAt, res.DurationMs
			res = parsed
		}
	}
	if err != nil {
		res.State, res.Skipped, res.Detail = rn.classify(err)
		if res.Skipped {
			rn.exhausted = rn.exhausted || rn.ctx.Err() != nil
		}
	}
	rn.streak(u, &res)
	rn.add(res)
	return &rn.raw.Results[len(rn.raw.Results)-1]
}

// streak — erken durma sayacı.
func (rn *rolloutsV2ProbeRunner) streak(u *rolloutsV2ProbeUnit, res *v2probe.Result) {
	if res.Skipped {
		return
	}
	switch res.State {
	case sourcestate.Unauthorized, sourcestate.Unreachable:
		u.streak++
		u.tstreak = 0
		u.stateSummary = string(res.State)
		if u.streak >= rolloutsV2ProbeAuthStreak {
			u.stopped = res.State
			u.earlyStop = string(res.State) + " streak after " + res.ID + " (" + res.Detail + ")"
		}
	case sourcestate.Timeout:
		u.tstreak++
		u.streak = 0
		if u.stateSummary == "" || u.stateSummary == "ok" {
			u.stateSummary = "partial"
		}
		if u.tstreak >= rolloutsV2ProbeTimeoutStreak {
			u.stopped = res.State
			u.earlyStop = "timeout streak after " + res.ID
		}
	default:
		u.streak, u.tstreak = 0, 0
		if !res.Usable() && u.stateSummary != string(sourcestate.Unauthorized) {
			u.stateSummary = "partial"
		}
	}
}

// ── Hedef ────────────────────────────────────────────────────────────────

func (rn *rolloutsV2ProbeRunner) runTarget(u *rolloutsV2ProbeUnit) {
	for _, pk := range rn.plan.packsFor(u) {
		var deferred []v2probe.Instance
		for _, q := range v2probe.ByPack(pk) {
			if !q.Runs(u.Role) || q.FallbackFor != "" {
				continue
			}
			insts, reason := rn.plan.instances(u, q)
			if reason != "" {
				rn.skip(u, v2probe.Instance{Query: q}, reason)
				continue
			}
			for _, in := range insts {
				if in.Variant == "second_sample" {
					deferred = append(deferred, in)
					continue
				}
				res := rn.exec(u, in)
				if res == nil {
					continue
				}
				if q.ID == "K0.6b" && in.Variant == "" {
					u.firstSample = res.SampleAt
				}
				if q.ID == "K3.7" && in.Variant == "" {
					if v, ok := res.ScalarOrZero(); ok && v > 5000 {
						u.Params.K5Window = "1h" // §11.0 K5 kuralı
					}
				}
			}
		}
		for _, in := range deferred {
			rn.waitSecondSample(u)
			rn.exec(u, in)
		}
	}
}

// waitSecondSample — K0.6b ikinci örneği ilkinden ≥ gap sonra (bağlam altında).
func (rn *rolloutsV2ProbeRunner) waitSecondSample(u *rolloutsV2ProbeUnit) {
	if u.firstSample.IsZero() || u.stopped != "" {
		return
	}
	rest := rolloutsV2ProbeSecondSampleGap - time.Since(u.firstSample)
	if rest <= 0 {
		return
	}
	t := time.NewTimer(rest)
	defer t.Stop()
	select {
	case <-t.C:
	case <-rn.ctx.Done():
	}
}

// ── Hub ──────────────────────────────────────────────────────────────────

func (rn *rolloutsV2ProbeRunner) runHub(u *rolloutsV2ProbeUnit) {
	for _, pk := range rn.plan.packsFor(u) {
		queries := v2probe.ByPack(pk)
		if pk == v2probe.PackH && u.WithoutMatcher {
			// 1. geçiş: H0–H1 matcher'sız.
			for _, q := range queries {
				if q.Repeat&v2probe.RepeatNoMatcher == 0 {
					continue
				}
				insts, reason := rn.plan.instances(u, q)
				if reason != "" {
					continue // 2. geçişte kaydedilir
				}
				for _, in := range insts {
					if in.Variant == "nomatch" {
						rn.exec(u, in)
					}
				}
			}
		}
		for _, q := range queries {
			if !q.Runs(u.Role) {
				continue
			}
			if q.FallbackFor != "" {
				if !rn.badData(q.FallbackFor, u.ID) {
					continue
				}
			}
			insts, reason := rn.plan.instances(u, q)
			if reason != "" {
				rn.skip(u, v2probe.Instance{Query: q}, reason)
				continue
			}
			for _, in := range insts {
				if in.Variant == "nomatch" {
					continue
				}
				rn.exec(u, in)
			}
		}
	}
}

func (rn *rolloutsV2ProbeRunner) badData(id, unit string) bool {
	for i := range rn.raw.Results {
		r := &rn.raw.Results[i]
		if r.ID == id && r.Unit == unit && r.Variant == "" {
			return r.IsBadData()
		}
	}
	return false
}

// ── T (ClickHouse) ───────────────────────────────────────────────────────

func (s *Server) rolloutsV2ProbeSpanCoverageCH(ctx context.Context, from, to time.Time) (*chstore.RolloutProbeSpans, error) {
	if s.store == nil {
		return nil, sourcestate.ErrNotConfigured
	}
	return s.store.RolloutProbeSpanCoverage(ctx, from, to)
}

func (s *Server) rolloutsV2ProbeSeenClustersCH(ctx context.Context, since time.Time) (chstore.SeenClusterValues, error) {
	if s.store == nil {
		return chstore.SeenClusterValues{}, sourcestate.ErrNotConfigured
	}
	return s.store.EntitySeenClusterValues(ctx, since)
}

func (rn *rolloutsV2ProbeRunner) tResult(id, variant string, err error, rows []v2probe.Row, total int, detail string, dur time.Duration) {
	res := v2probe.Result{ID: id, Unit: v2probe.ClickHouseUnit, Variant: variant, SampleAt: time.Now(), DurationMs: dur.Milliseconds(), Detail: detail, Total: total}
	if err != nil {
		if rn.ctx.Err() != nil {
			res.State, res.Skipped, res.Detail = sourcestate.Error, true, "skipped: run budget exhausted"
		} else {
			res.State = sourcestate.Classify(err)
			res.Detail = err.Error()
		}
		if res.State != sourcestate.NotConfigured && !res.Skipped {
			rn.tState = "partial"
		} else if res.State == sourcestate.NotConfigured {
			rn.tState = string(sourcestate.NotConfigured)
		}
	} else {
		res.Rows = rows
		if len(rows) == 0 {
			res.State = sourcestate.Empty
		} else {
			res.State = sourcestate.OK
		}
	}
	rn.add(res)
}

func (rn *rolloutsV2ProbeRunner) runT() {
	rn.tState = "ok"
	rn.progress(v2probe.ClickHouseUnit, "T")
	if !rn.budgetLeft() {
		for _, id := range []string{"T1", "T2", "T3"} {
			rn.add(v2probe.Result{ID: id, Unit: v2probe.ClickHouseUnit, State: sourcestate.Error, Skipped: true, Detail: "skipped: run budget exhausted", SampleAt: time.Now()})
		}
		return
	}
	now := time.Now()
	start := now
	rn.calls += 2
	rn.tCalls += 2
	cov, err := rolloutsV2ProbeSpanCoverage(rn.s, rn.ctx, now.Add(-rolloutsV2ProbeTWindow), now)
	dur := time.Since(start)
	rn.tDurationMs += dur.Milliseconds()
	if err != nil {
		rn.tResult("T1", "", err, nil, 0, "", dur)
		rn.tResult("T2", "", err, nil, 0, "", dur)
	} else {
		t1 := make([]v2probe.Row, 0, len(cov.Coverage))
		for _, r := range cov.Coverage {
			t1 = append(t1, v2probe.Row{Labels: map[string]string{
				"cluster": r.Cluster, "sampled": u64(r.Sampled), "svc_version": u64(r.SvcVersion), "img_tag": u64(r.ImgTag),
				"container_id": u64(r.ContainerID), "depl": u64(r.Depl), "rs": u64(r.RS), "sts": u64(r.STS), "ds": u64(r.DS),
				"env_name": u64(r.EnvName), "k8s_cluster": u64(r.K8sCluster), "ocp_cluster": u64(r.OcpCluster),
			}, Value: u64(r.Sampled)})
		}
		detail := fmt.Sprintf("window %ds, sample cap %d", cov.WindowSec, cov.SampleRows)
		rn.tResult("T1", "", nil, t1, len(t1), detail, dur)
		t2 := make([]v2probe.Row, 0, len(cov.Env))
		for _, r := range cov.Env {
			t2 = append(t2, v2probe.Row{Labels: map[string]string{"deploy_env": r.DeployEnv, "cluster": r.Cluster}, Value: u64(r.N)})
		}
		rn.tResult("T2", "", nil, t2, len(t2), "", dur)
		if cov.FallbackUsed {
			rn.calls++
			rn.tCalls++
			rn.add(v2probe.Result{ID: "T1", Unit: v2probe.ClickHouseUnit, Variant: "fallback", State: sourcestate.OK, SampleAt: time.Now(),
				Detail: "T0 fallback: `cluster` column missing — 0011 not applied; T1/T2 read res_values[indexOf(res_keys, 'k8s.cluster.name')]"})
		}
	}
	if !rn.budgetLeft() {
		rn.add(v2probe.Result{ID: "T3", Unit: v2probe.ClickHouseUnit, State: sourcestate.Error, Skipped: true, Detail: "skipped: run budget exhausted", SampleAt: time.Now()})
		return
	}
	start = time.Now()
	rn.calls++
	rn.tCalls++
	seen, err := rolloutsV2ProbeSeenClusters(rn.s, rn.ctx, now.Add(-rolloutsV2ProbeT3Since))
	dur = time.Since(start)
	rn.tDurationMs += dur.Milliseconds()
	if err != nil {
		rn.tResult("T3", "", err, nil, 0, "", dur)
		return
	}
	cfg := rn.s.thanos.CurrentSettings()
	rows := []v2probe.Row{}
	unmapped := 0
	for _, v := range seen.Rows {
		if _, ok := thanos.SpanClusterOwner(cfg, v.Value); ok {
			continue
		}
		unmapped++
		if len(rows) < 50 {
			rows = append(rows, v2probe.Row{Labels: map[string]string{"span_cluster": v.Value, "owner": ""}, Value: strconv.FormatInt(v.Spans, 10)})
		}
	}
	rn.tResult("T3", "", nil, rows, unmapped, fmt.Sprintf("unmapped=%d total=%d source=%s", unmapped, len(seen.Rows), seen.Source), dur)
}

func u64(v uint64) string { return strconv.FormatUint(v, 10) }
