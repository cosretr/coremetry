package api

// cosre_memory_prompt.go — CoSRE Faz E (hafıza): ekip notlarının sohbet
// çağrısına girdiği yer. Blok cosreRulesBlock'ta (cosre_rules_prompt.go)
// kural bloğunun ARKASINA eklenir; böylece aynı iki yüzeye (serbest döngü
// sistem mesajı, guided VERİ bölümü) ve aynı sıraya girer — router, niyet
// sınıflandırıcısı, araç argümanları ve tek-atış ✨ explain yüzeyleri notları
// GÖRMEZ (TestRulesPromptSitesPinned bu dosyayı da kapsar). Tavan sert:
// ≤5 not, satırlar ≤900 B (cosrememory.Relevant). Not yoksa prompt bayt bayt
// eski.

import (
	"context"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/ai/cosrememory"
	"github.com/cilcenk/coremetry/internal/chstore"
	"github.com/cilcenk/coremetry/internal/copilot"
	"github.com/cilcenk/coremetry/internal/tzdefault"
)

// cosreMemorySource — test dikişi (üretimde süreç-içi son yüklenen notlar).
var cosreMemorySource = chstore.CurrentCosreMemory

// cosreMemoryContext — notların bağlamı; takım okumaları YALNIZ takım
// kapsamlı not varken.
func (s *Server) cosreMemoryContext(ctx context.Context, service string, items []cosrememory.Item) cosrememory.Context {
	c := cosrememory.Context{Service: strings.TrimSpace(service)}
	for _, it := range items {
		if it.Scope.Kind == cosrememory.ScopeTeam {
			c.Team = s.serviceOwnerTeam(ctx, c.Service)
			if u := s.userByID(ctx, copilot.MetaFromContext(ctx).UserID); u != nil {
				c.UserTeam = strings.TrimSpace(u.Team)
			}
			break
		}
	}
	return c
}

// cosreMemoryBlock — bağlama uyan canlı notların çitli bloğu ve not sayısı.
func (s *Server) cosreMemoryBlock(ctx context.Context, service string) (string, int) {
	items := cosreMemorySource()
	if len(items) == 0 {
		return "", 0
	}
	picked, omitted := cosrememory.Relevant(items, s.cosreMemoryContext(ctx, service, items),
		cosrememory.Filter{Now: time.Now().UnixNano()}, cosrememory.PromptMaxItems, cosrememory.PromptMaxBytes)
	return cosrememory.BlockTR(picked, omitted, tzdefault.Location()), len(picked)
}

// hasMemoryBlock — SAF: blok bir ekip notu bloğu taşıyor mu.
func hasMemoryBlock(block string) bool {
	return strings.Contains(block, cosrememory.BlockHeader+"\n"+cosrememory.FenceOpen)
}
