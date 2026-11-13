package api

// ai_conversations_v2parts.go — CoSRE v2 araç seti dilim 1 (tools-audit §10
// G1/G2, G5): data-tool-result ve data-stop parçalarının arşiv kaydı
// (saved_views page='ai-chat' blobu; yeni şema YOK). İstemciden gelir →
// sanitize: kapalı kind/neden kümeleri, kök-göreli page_url, tavanlar.
//
// Blob 64 KB'tır ve fitChatBlob sığmayan ESKİ mesajları atar: araç sonucu
// geçmişi yememeli. Bu yüzden arşiv KOMPAKT: `text` (ham çıktı önizlemesi —
// v1 arşivi de hiç tutmadı) düşer, mesaj başına ≤ 12 kayıt ve ≤ 4 KB.

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/cilcenk/coremetry/internal/cosre/uistream"
)

// aiChatToolResult — data-tool-result{id, data}.
type aiChatToolResult struct {
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data"`
}

// aiChatStop — data-stop{reason, calls, rounds, elapsed_ms}.
type aiChatStop struct {
	Reason    string `json:"reason"`
	Calls     int    `json:"calls"`
	Rounds    int    `json:"rounds"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

const (
	aiChatToolResultsMax      = 12
	aiChatToolResultsMaxBytes = 4 << 10
)

var aiChatToolResultIDRe = regexp.MustCompile(`^s[1-9][0-9]{0,4}$`)

// sanitizeChatToolResults — SAF: geçersiz kayıt düşer, `text` atılır,
// page_url yalnız kök-göreli, tavanlar sırayla uygulanır.
func sanitizeChatToolResults(in []aiChatToolResult) []aiChatToolResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]aiChatToolResult, 0, min(len(in), aiChatToolResultsMax))
	total := 0
	for _, r := range in {
		if len(out) >= aiChatToolResultsMax {
			break
		}
		if !aiChatToolResultIDRe.MatchString(r.ID) || len(r.Data) == 0 || len(r.Data) > uistream.ToolResultMaxBytes {
			continue
		}
		var d map[string]any
		if json.Unmarshal(r.Data, &d) != nil || d == nil {
			continue
		}
		kind, _ := d["kind"].(string)
		if !slices.Contains(uistream.ToolResultKinds, kind) {
			continue
		}
		delete(d, "text")
		if u, has := d["page_url"]; has {
			s, _ := u.(string)
			if !strings.HasPrefix(s, "/") || strings.HasPrefix(s, "//") || strings.HasPrefix(s, "/\\") || len(s) > aiChatEvidenceHrefMax {
				delete(d, "page_url")
			}
		}
		b, err := json.Marshal(d)
		if err != nil || total+len(b) > aiChatToolResultsMaxBytes {
			break
		}
		total += len(b)
		out = append(out, aiChatToolResult{ID: r.ID, Data: b})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeChatStop — SAF: bilinmeyen neden düşer; sayılar negatif olamaz.
func sanitizeChatStop(in *aiChatStop) *aiChatStop {
	if in == nil || !slices.Contains(uistream.StopReasons, in.Reason) {
		return nil
	}
	return &aiChatStop{Reason: in.Reason, Calls: max(in.Calls, 0), Rounds: max(in.Rounds, 0), ElapsedMs: max(in.ElapsedMs, 0)}
}

// v0.10.2104 (docs/cosre/experience-audit.md D5) — data-links (özet kartı +
// link çipleri) ve data-question arşivi; aynı blob, yeni şema YOK. Linkler
// kaydedildiği andaki hedefle döner (yeniden hesaplanmaz). Yalnız v2 istemcisi
// yazar; v1 persist / restore bu alanları bilmez (davranışı değişmez).

// aiChatLinkItem — data-links.links[] öğesi.
type aiChatLinkItem struct {
	Label string `json:"label"`
	Href  string `json:"href"`
}

// aiChatLinksPart — data-links{links, title?, summary?} (api chat_summary_card.go).
type aiChatLinksPart struct {
	Links   []aiChatLinkItem `json:"links"`
	Title   string           `json:"title,omitempty"`
	Summary string           `json:"summary,omitempty"`
}

// aiChatQuestion — data-question + istemcinin kaydettiği seçim (Chosen).
type aiChatQuestion struct {
	Kind      string           `json:"kind"`
	Text      string           `json:"text"`
	Options   []questionOption `json:"options"`
	AllowFree bool             `json:"allowFree"`
	Chosen    string           `json:"chosen,omitempty"`
}

const (
	aiChatLinksMax           = 12
	aiChatLinksMaxBytes      = 4 << 10
	aiChatLinkLabelMax       = 120
	aiChatLinkCardTitleMax   = 120
	aiChatLinkCardSummaryMax = 400
	aiChatQuestionOptIDMax   = 32
	aiChatQuestionOptLblMax  = 80
)

// chatLinkHrefRel — SAF: kök-göreli iç yol mu (// ve /\ hariç).
func chatLinkHrefRel(h string) bool {
	return strings.HasPrefix(h, "/") && !strings.HasPrefix(h, "//") && !strings.HasPrefix(h, "/\\")
}

// sanitizeChatLinksPart — SAF: geçersiz link düşer (href kök-göreli ya da
// http(s), ≤ aiChatEvidenceHrefMax; etiket dolu), ≤12 link / 4 KB. Kart
// alanları yalnız 1–3 kök-göreli linkle kalır (FE parseDataLinksCard kuralı);
// değilse çipler kalır. Link kalmazsa nil.
func sanitizeChatLinksPart(in *aiChatLinksPart) *aiChatLinksPart {
	if in == nil {
		return nil
	}
	out := &aiChatLinksPart{}
	total := 0
	allRel := true
	for _, l := range in.Links {
		if len(out.Links) >= aiChatLinksMax {
			break
		}
		label := clampChatRunes(l.Label, aiChatLinkLabelMax)
		h := l.Href
		rel := chatLinkHrefRel(h)
		abs := strings.HasPrefix(h, "https://") || strings.HasPrefix(h, "http://")
		if label == "" || len(h) > aiChatEvidenceHrefMax || (!rel && !abs) {
			continue
		}
		if total += len(label) + len(h); total > aiChatLinksMaxBytes {
			break
		}
		allRel = allRel && rel
		out.Links = append(out.Links, aiChatLinkItem{Label: label, Href: h})
	}
	if len(out.Links) == 0 {
		return nil
	}
	title := clampChatRunes(in.Title, aiChatLinkCardTitleMax)
	summary := clampChatRunes(in.Summary, aiChatLinkCardSummaryMax)
	if title != "" && summary != "" && allRel && len(out.Links) <= summaryCardMaxLinks {
		out.Title, out.Summary = title, summary
	}
	return out
}

// sanitizeChatQuestion — SAF: kapalı kind, dolu metin (≤600 rune), 1–3 tekil
// dolu seçenek; Chosen yalnız seçeneklerden biriyse. Geçersizse nil.
func sanitizeChatQuestion(in *aiChatQuestion) *aiChatQuestion {
	if in == nil || !slices.Contains(uistream.QuestionKinds, in.Kind) {
		return nil
	}
	text := clampChatRunes(in.Text, chatPendingQuestionMax)
	if text == "" || len(in.Options) == 0 || len(in.Options) > uistream.QuestionMaxOptions {
		return nil
	}
	out := &aiChatQuestion{Kind: in.Kind, Text: text, AllowFree: in.AllowFree}
	for _, o := range in.Options {
		id, label := clampChatRunes(o.ID, aiChatQuestionOptIDMax), clampChatRunes(o.Label, aiChatQuestionOptLblMax)
		if id == "" || label == "" || slices.ContainsFunc(out.Options, func(x questionOption) bool { return x.ID == id }) {
			return nil
		}
		out.Options = append(out.Options, questionOption{ID: id, Label: label})
		if id == in.Chosen {
			out.Chosen = id
		}
	}
	return out
}
