package cosrelearn

// grounding.go — CoSRE öğrenme Faz 4: çevrimdışı öz-değerlendirme. Gece
// örneklenen cevapların iddiaları, o turda okunan tool çıktılarına karşı
// self-hosted modelle etiketlenir (grounded / ungrounded / no_evidence).
// Dayanaksız iddia taşıyan cevap Faz 2 puanına SigUngrounded olarak girer
// ve en kötüler evalset vakasına dönüşür (api/cosre_quality_evalset.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
)

// Judge — self-hosted modele tek çağrı (sistem + kullanıcı → metin).
type Judge func(ctx context.Context, system, user string) (string, error)

const (
	groundingSample      = 40   // gün başına hakem çağrısı tavanı
	groundingToolOutMax  = 2500 // tool başına hakeme giden çıktı
	groundingTotalOutMax = 9000 // tur başına toplam
	groundingAnswerMax   = 4000
	groundingCallTimeout = 90 * time.Second
)

// GroundingClaim — hakemin tek iddia kararı.
type GroundingClaim struct {
	Claim string `json:"claim"`
	Label string `json:"label"`
	Fact  string `json:"fact"`
}

// BuildGroundingUser — SAF: hakemin kullanıcı bloğu (tool çıktıları kırpılmış).
func BuildGroundingUser(in chstore.CosreGroundingInput) string {
	var b strings.Builder
	b.WriteString("QUESTION:\n")
	b.WriteString(clip(in.Question, 600))
	b.WriteString("\n\nTOOL OUTPUTS:\n")
	total, n := 0, 0
	for _, t := range in.Tools {
		if !t.OK || strings.TrimSpace(t.Output) == "" {
			continue
		}
		out := clip(t.Output, groundingToolOutMax)
		if total+len(out) > groundingTotalOutMax {
			b.WriteString("[further tool outputs omitted]\n")
			break
		}
		total += len(out)
		n++
		fmt.Fprintf(&b, "--- %s ---\n%s\n", t.Name, out)
	}
	if n == 0 {
		b.WriteString("(none — the answer was produced without reading any tool output)\n")
	}
	b.WriteString("\nANSWER:\n")
	b.WriteString(clip(in.Answer, groundingAnswerMax))
	return b.String()
}

// ParseGrounding — SAF: model çıktısından iddia listesi. Kod bloğu / önsöz
// tolere edilir (ilk '{' … son '}'); bilinmeyen etiket no_evidence sayılır.
func ParseGrounding(out string) ([]GroundingClaim, error) {
	i, j := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if i < 0 || j <= i {
		return nil, fmt.Errorf("hakem JSON döndürmedi")
	}
	var v struct {
		Claims []GroundingClaim `json:"claims"`
	}
	if err := json.Unmarshal([]byte(out[i:j+1]), &v); err != nil {
		return nil, fmt.Errorf("hakem JSON'u çözülemedi: %w", err)
	}
	claims := v.Claims[:0]
	for _, c := range v.Claims {
		c.Label = strings.ToLower(strings.TrimSpace(c.Label))
		switch c.Label {
		case "grounded", "ungrounded", "no_evidence":
		default:
			c.Label = "no_evidence"
		}
		c.Claim, c.Fact = clip(strings.TrimSpace(c.Claim), 300), clip(strings.TrimSpace(c.Fact), 80)
		if c.Claim != "" {
			claims = append(claims, c)
		}
		if len(claims) == 12 {
			break
		}
	}
	return claims, nil
}

// SummarizeGrounding — SAF: iddialardan satır; tek dayanaksız iddia hükmü belirler.
func SummarizeGrounding(in chstore.CosreGroundingInput, claims []GroundingClaim, model string) chstore.CosreGrounding {
	g := chstore.CosreGrounding{ExchangeID: in.ExchangeID, CreatedAt: in.CreatedAt, Model: model, Claims: uint16(len(claims))}
	for _, c := range claims {
		switch c.Label {
		case "grounded":
			g.Grounded++
		case "ungrounded":
			g.Ungrounded++
		default:
			g.NoEvidence++
		}
	}
	switch {
	case g.Ungrounded > 0:
		g.Verdict = "ungrounded"
	case g.Claims == 0:
		g.Verdict = "no_claims"
	case g.NoEvidence > g.Grounded:
		g.Verdict = "no_evidence"
	default:
		g.Verdict = "grounded"
	}
	if b, err := json.Marshal(claims); err == nil {
		g.Details = string(b)
	}
	return g
}

// GroundingStore — Faz 4 G/Ç yüzeyi.
type GroundingStore interface {
	ListCosreForGrounding(ctx context.Context, from, to time.Time, limit int) ([]chstore.CosreGroundingInput, error)
	InsertCosreGrounding(ctx context.Context, rows []chstore.CosreGrounding) error
	UngroundedExchanges(ctx context.Context, from, to time.Time) (map[string]bool, error)
}

// RunGrounding — günün örneklemini hakeme verir. Hakem hatası satırı
// judge_error olarak yazar (denetlenmedi ≠ dayanaklı); toplam hata → err.
func RunGrounding(ctx context.Context, st GroundingStore, judge Judge, model string, from, to time.Time) (judged, ungrounded int, err error) {
	if judge == nil {
		return 0, 0, nil
	}
	ins, err := st.ListCosreForGrounding(ctx, from, to, groundingSample)
	if err != nil {
		return 0, 0, err
	}
	var rows []chstore.CosreGrounding
	sys := copilot.SystemPromptGroundingJudge()
	for _, in := range ins {
		jctx, cancel := context.WithTimeout(ctx, groundingCallTimeout)
		out, jerr := judge(jctx, sys, BuildGroundingUser(in))
		cancel()
		if ctx.Err() != nil {
			break
		}
		// v0.10.2114 — hakem koruması (LocalOnlyJudge): profil yerel değil →
		// günün hakemi atlanır; judge_error satırı YAZILMAZ (model çağrılmadı).
		if errors.Is(jerr, ErrJudgeNotLocal) {
			log.Printf("[cosre-grounding] %s — gerekçe loglandı, hakem bu gün atlandı", jerr)
			break
		}
		var claims []GroundingClaim
		if jerr == nil {
			claims, jerr = ParseGrounding(out)
		}
		if jerr != nil {
			rows = append(rows, chstore.CosreGrounding{ExchangeID: in.ExchangeID, CreatedAt: in.CreatedAt, Verdict: "judge_error",
				Details: clip(jerr.Error(), 300), Model: model})
			continue
		}
		g := SummarizeGrounding(in, claims, model)
		if g.Ungrounded > 0 {
			ungrounded++
		}
		judged++
		rows = append(rows, g)
	}
	if len(rows) == 0 {
		return judged, ungrounded, nil
	}
	return judged, ungrounded, st.InsertCosreGrounding(ctx, rows)
}

// ErrJudgeNotLocal — v0.10.2114 (kalite döngüsü audit §8 soru 5, operatör
// onayı): hakemin kullanacağı model profili "Yerel (self-hosted) model"
// işaretli değil. Hakem cevap metnini ve araç çıktılarını (kod dahil) modele
// taşır; kod içeren istek yalnız self-hosted modele gider.
var ErrJudgeNotLocal = errors.New("hakem atlandı: model profili yerel (self-hosted) değil")

// LocalOnlyJudge — v0.10.2114: local(ctx) false ise j ÇAĞRILMAZ, ErrJudgeNotLocal
// döner. local, çağrının çözülen profilini copilot.LocalOnlyAllowed ile denetler
// (D4 günlük koşusunun yerel-profil kuralıyla aynı ölçüt). j nil → nil.
func LocalOnlyJudge(j Judge, local func(ctx context.Context) bool) Judge {
	if j == nil {
		return nil
	}
	return func(ctx context.Context, system, user string) (string, error) {
		if local == nil || !local(ctx) {
			return "", ErrJudgeNotLocal
		}
		return j(ctx, system, user)
	}
}
