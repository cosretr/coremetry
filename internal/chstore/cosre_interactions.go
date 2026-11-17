package chstore

// cosre_interactions.go — CoSRE öğrenme Faz 1: tur başına etkileşim satırı
// (DDL: store.go "cosre_interactions"). Yazıcı api katmanındaki asenkron
// kuyruk (internal/api/cosre_interactions.go); burada yalnız toplu INSERT.

import (
	"context"
	"sync/atomic"
	"time"
)

// CosreTool — turda çalışan bir tool / kanıt okuması. Output, modelin
// gördüğü metnin önizlemesi (çağıran kırpar); Bytes kırpılmamış boy.
type CosreTool struct {
	Name   string
	Origin string // "" = modelin çağrısı, "guided" = sunucu ön-yüklemesi
	Args   string
	Ms     uint32
	OK     bool
	Error  string
	Bytes  uint32
	Output string
	// Evidence (v0.10.1280) — çipin kanıt ÖZETİ: "kaynak[#hop]:satır,…"
	// (api stepEvidenceSummary). Hacimli kanıt gövdesi değil.
	Evidence string
}

// CosreInteraction — bir CoSRE turu.
type CosreInteraction struct {
	ExchangeID     string
	ConversationID string
	Turn           uint16
	CreatedAt      time.Time
	UserID         string
	UserEmail      string
	Tier           string
	Surface        string
	Page           string
	Service        string
	ProblemID      string
	TraceID        string
	ExceptionID    string
	Question       string
	Answer         string
	AnswerChars    uint32
	Tools          []CosreTool
	Model          string
	ModelCalls     uint16
	InputTokens    uint32
	OutputTokens   uint32
	TTFTMs         uint32
	TotalMs        uint32
	Status         string
	ErrorMsg       string
	Attempt        string // "" | retry | regenerate | edit (istemci beyanı)
	Deep           bool
	EmptyAnswer    bool
	NoEvidence     bool
	// Toolset — v0.10.1302: serbest döngünün araç seti (full | simple); guided /
	// niyet / LLM'siz kademeler "". Kolon iki-boot sözleşmesiyle (probe).
	Toolset string
	// Blocks — v0.10.1444: turun zengin bloklarının sürümlü zarfı (JSON; api
	// chat_turn_blocks.go compactTurnBlocks, ≤256 KiB). "" = blok yok (eski
	// satır, eval, bütçe aşımı). Kolon iki-boot sözleşmesiyle (probe).
	Blocks string
	// Verify — v0.10.2327 (CoSRE E1, gölge doğrulayıcı): turun sayaçları
	// "v1 tier=… tür=denetlenen/doğrulanan/türetilen/uyuşmayan" (≤ ~200 B);
	// "" = doğrulama koşmadı (v1, kapalı, anlatımsız kademe). Kolon iki-boot
	// sözleşmesiyle (probe).
	Verify string
}

const cosreInteractionsBaseCols = `exchange_id, conversation_id, turn, created_at, user_id, user_email,
	tier, surface, page, service, problem_id, trace_id, exception_id,
	question, answer, answer_chars,
	tool_name, tool_origin, tool_args, tool_ms, tool_ok, tool_error, tool_bytes, tool_output,
	model, model_calls, input_tokens, output_tokens, ttft_ms, total_ms,
	status, error_msg, attempt, deep, empty_answer, no_evidence`

// cosreInteractionsEvidenceCol — v0.10.1280 tool_evidence kolonu INSERT
// hedefinde var mı (boot probe; küme kipinde ALTER ertelenir → ilk boot
// false okur ve INSERT kolonu ANMAZ — iki-boot sözleşmesi, ai_calls emsali).
var cosreInteractionsEvidenceCol atomic.Bool

// cosreInteractionsToolsetCol — v0.10.1302 toolset kolonu (aynı sözleşme).
var cosreInteractionsToolsetCol atomic.Bool

// cosreInteractionsBlocksCol — v0.10.1444 blocks kolonu (aynı sözleşme). Hem
// INSERT hem okuma (TurnBlocksByExchange çağıranı) bu bayrağa bağlı: küme
// kipinde ilk boot kolonsuz yazar, okuma hiç denenmez.
var cosreInteractionsBlocksCol atomic.Bool

// cosreInteractionsVerifyCol — v0.10.2327 verify kolonu (aynı sözleşme; yalnız INSERT).
var cosreInteractionsVerifyCol atomic.Bool

// CosreInteractionsEvidenceCol — kolon bayrağı (test/teşhis).
func CosreInteractionsEvidenceCol() bool { return cosreInteractionsEvidenceCol.Load() }

// CosreInteractionsToolsetCol — v0.10.1302 kolon bayrağı (test/teşhis).
func CosreInteractionsToolsetCol() bool { return cosreInteractionsToolsetCol.Load() }

// CosreInteractionsBlocksCol — v0.10.1444 kolon bayrağı (yazım + Geçmiş okuması).
func CosreInteractionsBlocksCol() bool { return cosreInteractionsBlocksCol.Load() }

// CosreInteractionsVerifyCol — v0.10.2327 kolon bayrağı (test/teşhis).
func CosreInteractionsVerifyCol() bool { return cosreInteractionsVerifyCol.Load() }

// cosreInteractionsOptionalCols — iki-boot sözleşmeli kolonlar (probe sırası).
var cosreInteractionsOptionalCols = []string{"tool_evidence", "toolset", "blocks", "verify"}

// probeCosreInteractionsColumns — INSERT hedefi cosre_interactions'ta (küme
// kipinde Distributed) iki-boot kolonları var mı; v0.10.1302'den beri TEK
// system.columns sorgusu, kolon başına bir bayrak. Hata → hepsi false
// (INSERT kolonları anmaz; güvenli yön).
func (s *Store) probeCosreInteractionsColumns(ctx context.Context) {
	have := map[string]bool{}
	rows, err := s.conn.Query(ctx,
		`SELECT name FROM system.columns
		 WHERE database = currentDatabase() AND table = 'cosre_interactions' AND name IN ('tool_evidence', 'toolset', 'blocks', 'verify')`)
	if err == nil {
		for rows.Next() {
			var name string
			if rows.Scan(&name) == nil {
				have[name] = true
			}
		}
		if rows.Err() != nil {
			have = map[string]bool{}
		}
		_ = rows.Close()
	}
	cosreInteractionsEvidenceCol.Store(have["tool_evidence"])
	cosreInteractionsToolsetCol.Store(have["toolset"])
	cosreInteractionsBlocksCol.Store(have["blocks"]) // v0.10.1444
	cosreInteractionsVerifyCol.Store(have["verify"]) // v0.10.2327
}

// cosreInteractionCols — INSERT'in iki-boot kolon bayrakları.
type cosreInteractionCols struct {
	Evidence bool // v0.10.1280 tool_evidence
	Toolset  bool // v0.10.1302 toolset
	Blocks   bool // v0.10.1444 blocks
	Verify   bool // v0.10.2327 verify
}

func loadCosreInteractionCols() cosreInteractionCols {
	return cosreInteractionCols{Evidence: cosreInteractionsEvidenceCol.Load(), Toolset: cosreInteractionsToolsetCol.Load(),
		Blocks: cosreInteractionsBlocksCol.Load(), Verify: cosreInteractionsVerifyCol.Load()}
}

// cosreInteractionsInsertSQL — SAF: kolon listesi bayraklara göre (sıra:
// tool_evidence, toolset, blocks, verify — cosreInteractionArgs ile aynı).
func cosreInteractionsInsertSQL(c cosreInteractionCols) string {
	cols := cosreInteractionsBaseCols
	if c.Evidence {
		cols += ", tool_evidence"
	}
	if c.Toolset {
		cols += ", toolset"
	}
	if c.Blocks {
		cols += ", blocks"
	}
	if c.Verify {
		cols += ", verify"
	}
	return "INSERT INTO cosre_interactions (\n\t" + cols + ")"
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// cosreInteractionArgs — SAF: satırın INSERT argümanları; tool dizileri
// paralel ve her zaman aynı uzunlukta.
// v0.10.1280 — Evidence iken paralel tool_evidence dizisi; v0.10.1302 —
// Toolset iken toolset dizgesi; v0.10.1444 — Blocks iken EN SONDA blok zarfı.
func cosreInteractionArgs(r CosreInteraction, c cosreInteractionCols) []any {
	n := len(r.Tools)
	names, origins, args := make([]string, n), make([]string, n), make([]string, n)
	ms, bytes := make([]uint32, n), make([]uint32, n)
	oks := make([]uint8, n)
	errs, outs, evs := make([]string, n), make([]string, n), make([]string, n)
	for i, t := range r.Tools {
		names[i], origins[i], args[i] = t.Name, t.Origin, t.Args
		ms[i], bytes[i], oks[i] = t.Ms, t.Bytes, b2u8(t.OK)
		errs[i], outs[i], evs[i] = t.Error, t.Output, t.Evidence
	}
	created := r.CreatedAt
	if created.IsZero() {
		created = time.Now()
	}
	status := r.Status
	if status == "" {
		status = "ok"
	}
	out := []any{
		r.ExchangeID, r.ConversationID, r.Turn, created.UTC(), r.UserID, r.UserEmail,
		r.Tier, r.Surface, r.Page, r.Service, r.ProblemID, r.TraceID, r.ExceptionID,
		r.Question, r.Answer, r.AnswerChars,
		names, origins, args, ms, oks, errs, bytes, outs,
		r.Model, r.ModelCalls, r.InputTokens, r.OutputTokens, r.TTFTMs, r.TotalMs,
		status, r.ErrorMsg, r.Attempt, b2u8(r.Deep), b2u8(r.EmptyAnswer), b2u8(r.NoEvidence),
	}
	if c.Evidence {
		out = append(out, evs)
	}
	if c.Toolset {
		out = append(out, r.Toolset)
	}
	if c.Blocks {
		out = append(out, r.Blocks)
	}
	if c.Verify {
		out = append(out, r.Verify)
	}
	return out
}

// InsertCosreInteractions — toplu INSERT (tek batch). Boş dilim no-op.
func (s *Store) InsertCosreInteractions(ctx context.Context, rows []CosreInteraction) error {
	if s == nil || len(rows) == 0 {
		return nil
	}
	ev := loadCosreInteractionCols()
	batch, err := s.conn.PrepareBatch(ctx, cosreInteractionsInsertSQL(ev))
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := batch.Append(cosreInteractionArgs(r, ev)...); err != nil {
			return err
		}
	}
	return batch.Send()
}

// cosreTurnBlocksSQL — v0.10.1444: Geçmiş'ten açılan konuşmanın turlarının
// blok zarfları. exchange_id bloom indeksli + created_at (DateTime64(3))
// penceresi aylık bölümleri budar; alt sorgu yok (küme kipinde GLOBAL derdi
// yok), literal `IN (?)` listesi (≤40 kimlik). Bind: from, to, ids, limit.
const cosreTurnBlocksSQL = `SELECT exchange_id, blocks
		FROM cosre_interactions
		WHERE created_at >= toDateTime64(?, 3, 'UTC')
		  AND created_at <  toDateTime64(?, 3, 'UTC')
		  AND exchange_id IN (?)
		  AND blocks != ''
		ORDER BY created_at DESC
		LIMIT ?
		SETTINGS max_execution_time = 5`

// cosreTurnBlocksMaxIDs — okuma başına kimlik tavanı (konuşma blob'u ≤40 mesaj).
const cosreTurnBlocksMaxIDs = 40

// dedupTurnBlockIDs — SAF: boşları atar, tekilleştirir, tavanlar (sıra korunur).
func dedupTurnBlockIDs(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= cosreTurnBlocksMaxIDs {
			break
		}
	}
	return out
}

// TurnBlocksByExchange — v0.10.1444: exchange_id → blok zarfı (yalnız dolu
// satırlar; aynı kimlikte birden çok satır varsa EN YENİ). Kolon yoksa
// (CosreInteractionsBlocksCol false) çağıran hiç çağırmaz.
func (s *Store) TurnBlocksByExchange(ctx context.Context, ids []string, from, to time.Time) (map[string]string, error) {
	out := map[string]string{}
	ids = dedupTurnBlockIDs(ids)
	if s == nil || len(ids) == 0 || !to.After(from) {
		return out, nil
	}
	rows, err := s.conn.Query(ctx, cosreTurnBlocksSQL, chDateTime64Arg(from), chDateTime64Arg(to), ids, len(ids))
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, env string
		if err := rows.Scan(&id, &env); err != nil {
			return out, err
		}
		if _, ok := out[id]; !ok {
			out[id] = env
		}
	}
	return out, rows.Err()
}
