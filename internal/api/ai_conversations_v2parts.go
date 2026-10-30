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
