package sre

// queue.go — v0.10.1282 — kuyruğa alma. Tek giriş noktası Enqueue: WORKER'daki
// üretici (otomatik) ve POST /api/problems/{id}/investigation (elle) aynı
// kapılardan geçer. Problem satırına ASLA dokunmaz.

import (
	"context"
	"errors"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// QueueStore — kuyruğun dört okuması/yazması (*chstore.Store karşılar).
// Bilerek DAR: problems / hipotez yazıcısı bu arayüzde yok, yani paket
// onları çağıramaz (TestEnqueueNeverUpsertsProblems).
type QueueStore interface {
	GetSREInvestigation(ctx context.Context, problemID string) (*chstore.SavedView, error)
	PutSREInvestigation(ctx context.Context, problemID, state, body string, createdAt int64) error
	ListSREInvestigations(ctx context.Context, states []string, limit int) ([]chstore.SavedView, error)
	CountSREQueued(ctx context.Context) (int, error)
}

var (
	// ErrDisabled — sre_agent.enabled=false.
	ErrDisabled = errors.New("AI SRE kapalı")
	// ErrQueueFull — kuyruk MaxQueue'da.
	ErrQueueFull = errors.New("kuyruk dolu")
	// ErrInvalid — problemId / servis eksik.
	ErrInvalid = errors.New("problem kimliği ve servis zorunlu")
)

// EnqueueRequest — kuyruğa alma isteği.
type EnqueueRequest struct {
	ProblemID        string
	DisplayID        string
	Service          string
	Env              string
	Kind             string // auto | manual | …
	Priority         string
	ProblemStartedAt int64 // unix ns
	By               string
	// Hypothesis — v0.10.1288: Kind=verify iken doğrulanacak hipotez türü
	// (IsHypothesisKind; yoksa ErrInvalid).
	Hypothesis string
	// SharedFrom — doluysa kayıt kuyruğa GİRMEZ: aynı servisin yakın tarihli
	// araştırmasına işaret eden "paylaşılan" satır yazılır.
	SharedFrom string
	Now        time.Time
}

// EnqueueResult — sonuç.
type EnqueueResult struct {
	State   string `json:"state"`
	Created bool   `json:"created"`
	Reason  string `json:"reason,omitempty"`
}

// save — kaydı kodlayıp yazar (createdAt = EnqueuedAt, her yazımda taşınır).
func save(ctx context.Context, st QueueStore, inv Investigation) error {
	body, err := Encode(inv)
	if err != nil {
		return err
	}
	return st.PutSREInvestigation(ctx, inv.QueueKey(), inv.State, body, inv.EnqueuedAt)
}

// Load — kuyruk anahtarının kaydı (yoksa ya da sahteyse ok=false). Canlı
// kayıt anahtarı problemId'dir; tekrar işi QueueKey (v0.10.1301).
func Load(ctx context.Context, st QueueStore, problemID string) (Investigation, bool, error) {
	v, err := st.GetSREInvestigation(ctx, problemID)
	if err != nil || v == nil {
		return Investigation{}, false, err
	}
	inv, ok := Decode(*v)
	return inv, ok, nil
}

// Enqueue — kayıt açar ya da elle istekte yeniden kuyruğa alır.
//
//   - queued / running kayıt → dokunulmaz (çift kuyruk yok).
//   - bitmiş kayıt + otomatik istek → dokunulmaz (otomatik ASLA yeniden koşmaz).
//   - bitmiş kayıt + elle istek → Run++ ile taze koşu (adımlar temizlenir).
//   - v0.10.1288: bitmiş kayıt + doğrulama (Kind=verify, Hypothesis) → Run++;
//     önceki adımlar + gerçekler taşınır, yalnız hipotezin adımları yeniden
//     okunur. Her yeniden koşuda operatör işaretleri (HumanState) taşınır.
//   - SharedFrom → kuyruğa girmeyen "shared" satır.
func Enqueue(ctx context.Context, st QueueStore, req EnqueueRequest, cfg Settings) (EnqueueResult, error) {
	if !cfg.IsEnabled() {
		return EnqueueResult{}, ErrDisabled
	}
	if req.ProblemID == "" || req.Service == "" {
		return EnqueueResult{}, ErrInvalid
	}
	if req.Kind == "" {
		req.Kind = KindAuto
	}
	if req.Kind == KindVerify && !IsHypothesisKind(req.Hypothesis) {
		return EnqueueResult{}, ErrInvalid
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	run := 1
	cur, ok, err := Load(ctx, st, req.ProblemID)
	if err != nil {
		return EnqueueResult{}, err
	}
	if ok {
		switch cur.State {
		case StateQueued, StateRunning:
			return EnqueueResult{State: cur.State, Reason: "zaten kuyrukta"}, nil
		}
		if req.Kind != KindManual && req.Kind != KindVerify {
			return EnqueueResult{State: cur.State, Reason: "zaten araştırıldı"}, nil
		}
		run = cur.Run + 1
	}
	inv := Investigation{
		ProblemID: req.ProblemID, DisplayID: req.DisplayID, Service: req.Service, Env: req.Env,
		Kind: req.Kind, Priority: req.Priority, Run: run, EnqueuedAt: now.UnixNano(),
		RequestBy: req.By, ProblemAt: req.ProblemStartedAt,
		Anchor: anchorFor(req.ProblemStartedAt, now),
	}
	if ok && cur.State != StateShared {
		// v0.10.1288 — operatör işaretleri (HumanState) yeniden koşudan sağ
		// çıkar; doğrulama ayrıca önceki koşunun adımlarını + gerçeklerini
		// taşır: yalnız hipotezin adımları yeniden okunur (verifyForced).
		inv.Hypotheses = cur.Hypotheses
		inv.Recommendations = cur.Recommendations     // v0.10.1293 — öneri işaretleri (ack / dismissed) de sağ çıkar
		inv.FirstHypothesisAt = cur.FirstHypothesisAt // v0.10.1301 — ilk hipoteze varış ilk koşunun anıdır
		if req.Kind == KindVerify {
			inv.Hypothesis = req.Hypothesis
			inv.Steps, inv.Findings, inv.Facts = cur.Steps, cur.Findings, cur.Facts
			inv.Timeline, inv.Summary, inv.SynthRef, inv.Warnings = cur.Timeline, cur.Summary, cur.SynthRef, cur.Warnings
		}
	} else if req.Kind == KindVerify {
		inv.Hypothesis = req.Hypothesis
	}
	if req.SharedFrom != "" && req.SharedFrom != req.ProblemID {
		inv.State = StateShared
		inv.SharedFrom = req.SharedFrom
		inv.FinishedAt = now.UnixNano()
	} else {
		n, err := st.CountSREQueued(ctx)
		if err != nil {
			return EnqueueResult{}, err
		}
		if n >= cfg.MaxQueue {
			return EnqueueResult{}, ErrQueueFull
		}
		inv.State = StateQueued
	}
	if err := save(ctx, st, inv); err != nil {
		return EnqueueResult{}, err
	}
	return EnqueueResult{State: inv.State, Created: true}, nil
}

// anchorFor — SAF: [başlangıç − 15 dk, min(now, başlangıç + 30 dk)]. Başlangıç
// yoksa now'a göre son 45 dk; ters pencere en az 1 dk'ya açılır.
func anchorFor(startNs int64, now time.Time) Anchor {
	start := time.Unix(0, startNs)
	if startNs <= 0 {
		start = now.Add(-30 * time.Minute)
	}
	from := start.Add(-15 * time.Minute)
	to := start.Add(30 * time.Minute)
	if now.Before(to) {
		to = now
	}
	if !to.After(from) {
		to = from.Add(time.Minute)
	}
	return Anchor{From: from.UnixNano(), To: to.UnixNano()}
}

// replayAsOfDelay — v0.10.1301: tekrarın "şimdi"si problem başlangıcından bu
// kadar sonra. Gerekçe: otomatik üretici yalnız ≤15 dk'lık problemi kuyruğa
// alır (candidateMaxAge) — canlı bir araştırmanın görebileceği en geç an.
// Daha geç bir an tekrara canlının asla görmediği veriyi verirdi (iyimser
// ölçüm); başlangıcın kendisi ise belirtinin ilk dakikalarını keserdi.
const replayAsOfDelay = candidateMaxAge

// ReplayRequest — bir değerlendirme vakasının tekrar işi.
type ReplayRequest struct {
	RunID            string
	CaseID           string
	ProblemID        string
	DisplayID        string
	Service          string
	Env              string
	Priority         string
	ProblemStartedAt int64 // unix ns — çıpa (problemin StartedAt'i)
	By               string
	Now              time.Time
	// ContextPack — v0.10.1494: tekrar Context Pack okuyucularıyla koşar
	// (Investigation.Pack; canlı bayrak tekrarı etkilemez).
	ContextPack bool
	// Plain — CoSRE Faz B kapalı (oyun kitabı / hipotez durumu / kanıt kapısı).
	Plain bool
	// NoMemory — CoSRE Faz E: ekip notları kapalı (Investigation.NoMemory).
	NoMemory bool
	// NoKnown — v0.10.2477: platformun bildiği adımlar kapalı (Investigation.NoKnown).
	NoKnown bool
}

// replayInvestigation — SAF: tekrar işinin kuyruk kaydı. Çıpa problemin
// başlangıcına göre [başlangıç − 15 dk, başlangıç + replayAsOfDelay].
func replayInvestigation(req ReplayRequest) Investigation {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	asOf := time.Unix(0, req.ProblemStartedAt).Add(replayAsOfDelay)
	return Investigation{
		ProblemID: req.ProblemID, DisplayID: req.DisplayID, Service: req.Service, Env: req.Env,
		Kind: KindReplay, State: StateQueued, Priority: req.Priority, Run: 1,
		EnqueuedAt: now.UnixNano(), RequestBy: req.By, ProblemAt: req.ProblemStartedAt,
		EvalRun: req.RunID, CaseID: req.CaseID, AsOf: asOf.UnixNano(),
		Anchor: anchorFor(req.ProblemStartedAt, asOf), Pack: req.ContextPack,
		Plain: req.Plain, NoMemory: req.NoMemory, NoKnown: req.NoKnown,
	}
}

// EnqueueReplay — v0.10.1301: değerlendirme vakasını Kind=replay olarak
// kuyruğa alır. Satır KENDİ anahtarında (QueueKey) — canlı kayda dokunmaz.
// Aynı (koşu, problem) zaten varsa no-op. Kuyruk payını (MaxQueue/2) çağıran
// (StartEvalRun) hesaplar; burada yalnız açık/kapalı kapısı.
func EnqueueReplay(ctx context.Context, st QueueStore, req ReplayRequest, cfg Settings) (bool, error) {
	if !cfg.IsEnabled() {
		return false, ErrDisabled
	}
	if req.RunID == "" || req.ProblemID == "" || req.Service == "" || req.ProblemStartedAt <= 0 {
		return false, ErrInvalid
	}
	inv := replayInvestigation(req)
	if _, ok, err := Load(ctx, st, inv.QueueKey()); err != nil {
		return false, err
	} else if ok {
		return false, nil
	}
	if err := save(ctx, st, inv); err != nil {
		return false, err
	}
	return true, nil
}
