package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// GET /api/ai/exchanges — v0.10.1153 (operatör, prod: "/ai her CoSRE
// etkileşimini göstermiyor"). /ai'ın "CoSRE etkileşimleri" paneli: her
// kullanıcı turu TEK satır (soru, kademe/rota, Derin, profil, süre, token —
// LLM kullanıldıysa —, sonuç, 👍/👎) ve altında turun model çağrıları
// (anlatım, sayfa seçimi, sınıflandırıcı …). LLM'siz cevaplar llmCalls=0.
// Yazım tarafı chat_exchange.go, okuma chstore/ai_exchanges.go.
//
// Admin kapılı (kardeş /api/ai/* okumaları gibi: soru/cevap örnekleri
// başka kullanıcıların sohbetidir).
//
// v0.10.1646 (operatör onaylı) — pencerenin TÜM soruları: LIMIT 200 eski
// turları gizliyordu. Artık sunucu sayfası (`limit`/`offset`) + kesin
// toplam (`total`), soru metninde arama (`q`), kullanıcı (`user`) ve kademe
// (`tier`) süzgeci; yanıt {rows,total,hasMore}. Kısa TTL'li serveCached —
// anahtar normalize edilmiş TÜM girdilerin FNV özeti (aiExchangesCacheKey);
// 10 sn bayatlık "az önce sorduğum tur" beklentisini bozmaz (istemci
// staleTime 15 sn zaten). Kardeş uçlar: /users (sunucu taraflı kullanıcı
// seçicisi) ve /csv (aynı süzgeçlerle dışa aktarım, aiExchangesCSVMaxRows).

const (
	aiExchangesCacheTTL = 10 * time.Second
	// aiExchangesCSVMaxRows — CSV'nin sert satır tavanı (UI'da yazılı).
	aiExchangesCSVMaxRows = 50_000
	// aiExchangesCSVBatch — CSV okuma partisi (chstore aiExchangesMaxLimit).
	aiExchangesCSVBatch = 500
)

// AIExchangesPage — /api/ai/exchanges yanıtı (lib/types.ts AIExchangesPage).
type AIExchangesPage struct {
	Rows    []chstore.AIExchange `json:"rows"`
	Total   uint64               `json:"total"`
	HasMore bool                 `json:"hasMore"`
}

// parseAIExchangesParams — SAF: sorgu dizesi → normalize parametreler.
// Liste, CSV ve önbellek anahtarı aynı değerleri görür.
func parseAIExchangesParams(q url.Values) chstore.ListAIExchangesParams {
	return chstore.NormalizeAIExchangeParams(chstore.ListAIExchangesParams{
		From:   parseTime(q.Get("from")),
		To:     parseTime(q.Get("to")),
		Limit:  parseInt(q.Get("limit"), 100),
		Offset: parseInt(q.Get("offset"), 0),
		// v0.10.1280 (CoSRE adım 8) — 👍/👎 süzgeci (down | up | rated; başka
		// değer = hepsi). Operatörün "yalnız 👎" inceleme görünümü.
		Feedback: q.Get("feedback"),
		Q:        q.Get("q"),
		User:     q.Get("user"),
		Tier:     q.Get("tier"),
	})
}

// aiExchangesCacheKey — SAF: normalize parametrelerin TÜMÜ (serbest
// metinler ayrı ayrı FNV, fnvStr — "a|b"+"c" ≠ "a"+"b|c").
func aiExchangesCacheKey(p chstore.ListAIExchangesParams) string {
	return "ai:exchanges:" + fnvStr(
		strconv.FormatInt(p.From.UnixNano(), 10), strconv.FormatInt(p.To.UnixNano(), 10),
		strconv.Itoa(p.Limit), strconv.Itoa(p.Offset),
		p.Feedback, p.Q, p.User, p.Tier)
}

// aiExchangesHasMore — SAF: bu sayfadan sonra satır var mı.
func aiExchangesHasMore(p chstore.ListAIExchangesParams, total uint64) bool {
	return uint64(p.Offset+p.Limit) < total
}

func (s *Server) listAIExchanges(w http.ResponseWriter, r *http.Request) {
	p := parseAIExchangesParams(r.URL.Query())
	s.serveCached(w, r, aiExchangesCacheKey(p), aiExchangesCacheTTL, func(ctx context.Context) (any, error) {
		total, err := s.store.CountAIExchanges(ctx, p)
		if err != nil {
			return nil, err
		}
		rows := []chstore.AIExchange{}
		if uint64(p.Offset) < total {
			if rows, err = s.store.ListAIExchanges(ctx, p); err != nil {
				return nil, err
			}
			if rows == nil {
				rows = []chstore.AIExchange{}
			}
		}
		return AIExchangesPage{Rows: rows, Total: total, HasMore: aiExchangesHasMore(p, total)}, nil
	})
}

// GET /api/ai/exchanges/users?q=&from=&to= — v0.10.1646: kullanıcı
// seçicisi (Picker = sunucu araması; tam kullanıcı listesi istemciye gitmez).
func (s *Server) listAIExchangeUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := parseAIExchangesParams(q)
	search := strings.TrimSpace(q.Get("q"))
	limit := parseInt(q.Get("limit"), 20)
	key := "ai:exchanges:users:" + fnvStr(
		strconv.FormatInt(p.From.UnixNano(), 10), strconv.FormatInt(p.To.UnixNano(), 10),
		search, strconv.Itoa(limit))
	s.serveCached(w, r, key, aiExchangesCacheTTL, func(ctx context.Context) (any, error) {
		return s.store.ListAIExchangeUsers(ctx, p.From, p.To, search, limit)
	})
}

// GET /api/ai/exchanges/csv — v0.10.1646: süzgeçli etkileşimlerin CSV'si
// (aynı parametreler; limit/offset YOK SAYILIR). Partiler hâlinde okunur ve
// akıtılır; sert tavan aiExchangesCSVMaxRows. Toplam X-Total-Count'ta,
// tavana dayandıysa X-Truncated: 1. Okuma — audit yok.
func (s *Server) exportAIExchangesCSV(w http.ResponseWriter, r *http.Request) {
	p := parseAIExchangesParams(r.URL.Query())
	total, err := s.store.CountAIExchanges(r.Context(), p)
	if err != nil {
		writeErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="coremetry-ai-exchanges-%s.csv"`,
		time.Now().UTC().Format("20060102-150405")))
	w.Header().Set("X-Total-Count", strconv.FormatUint(total, 10))
	if total > aiExchangesCSVMaxRows {
		w.Header().Set("X-Truncated", "1")
	}
	cw := csv.NewWriter(w)
	_ = cw.Write(aiExchangesCSVHeader)
	written := 0
	fl, _ := w.(http.Flusher)
	for written < aiExchangesCSVMaxRows && uint64(written) < total {
		bp := p
		bp.Offset, bp.Limit = written, min(aiExchangesCSVBatch, aiExchangesCSVMaxRows-written)
		rows, err := s.store.ListAIExchanges(r.Context(), bp)
		if err != nil {
			// Başlık gitti; durum kodu değişemez — dosya kesik biter, sonuna not.
			_ = cw.Write([]string{"# error: " + err.Error()})
			break
		}
		if len(rows) == 0 {
			break
		}
		writeAIExchangesCSV(cw, rows)
		written += len(rows)
		cw.Flush()
		if fl != nil {
			fl.Flush()
		}
	}
	cw.Flush()
}

var aiExchangesCSVHeader = []string{
	"time", "exchange_id", "tier", "route", "deep", "status", "duration_ms",
	"models", "llm_calls", "input_tokens", "output_tokens", "feedback", "user",
	"question", "answer", "error",
}

// writeAIExchangesCSV — SAF (io): satırları CSV'ye yazar. Serbest metin
// hücreleri csvSafeCell'den geçer (tablolama programı formül sanmasın).
func writeAIExchangesCSV(cw *csv.Writer, rows []chstore.AIExchange) {
	for _, x := range rows {
		user := x.UserEmail
		if user == "" {
			user = x.UserID
		}
		_ = cw.Write([]string{
			time.Unix(0, x.CreatedAt).UTC().Format(time.RFC3339),
			x.ExchangeID, x.Tier, x.Route, strconv.FormatBool(x.Deep), x.Status,
			strconv.FormatUint(uint64(x.DurationMs), 10),
			strings.Join(x.Models, " "), strconv.Itoa(x.LLMCalls),
			strconv.FormatUint(x.InputTokens, 10), strconv.FormatUint(x.OutputTokens, 10),
			strconv.Itoa(int(x.Feedback)), csvSafeCell(user),
			csvSafeCell(x.Question), csvSafeCell(x.Answer), csvSafeCell(x.ErrorMsg),
		})
	}
}

// csvSafeCell — SAF: = + - @ \t \r ile başlayan hücreye ' öneki (CSV
// formül enjeksiyonu; soru metni başka kullanıcının yazdığıdır). İçerik
// değişmez, yalnız tablolama programı onu formül diye çalıştırmaz.
func csvSafeCell(v string) string {
	if v == "" {
		return v
	}
	switch v[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return "'" + v
	}
	return v
}
