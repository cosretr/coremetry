package devops

// search_service_code.go — v0.10.1208: CoSRE search_code aracı. Servisin
// deposu (ResolveRepo — adlandırma kuralı / service_metadata) içinde Code
// Search; dosya + (sunucu verirse) satır + kısa parça. Ardından
// read_source_code o dosyayı okur. Settings.CodeSearch kapalıysa çağrılmaz.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ServiceCodeHit — depo-içi bir arama isabeti.
type ServiceCodeHit struct {
	Path    string `json:"file"`
	Branch  string `json:"branch,omitempty"`
	Line    int    `json:"line,omitempty"`
	Snippet string `json:"snippet,omitempty"`
}

// CodeSearchEnabled — ayar + yapılandırma birlikte.
func (s *Service) CodeSearchEnabled() bool {
	return s != nil && s.Configured() && s.CurrentSettings().CodeSearch
}

// serviceSearchWideTop — proje bilinmiyorken (süzgeçsiz arama) istenen isabet
// sayısı: sonuçlar koleksiyon genelinden gelir, depo elemesi yanıtta yapılır.
const serviceSearchWideTop = 200

// serviceSearchFilters — SAF: depo-içi aramanın `filters` gövdesi.
//
// v0.10.1885 (operatör-raporlu): Azure DevOps Code Search, "Repository"
// süzgecini YALNIZ "Project" süzgeciyle birlikte kabul eder; tek başına
// gönderilince HTTP 400 "Filter [Repository] is found but filter [Project] is
// not." döner ve Endpoint / operasyon kod kartı "rota araması başarısız" ile
// düşerdi. Proje biliniyorsa ikisi birlikte; bilinmiyorsa süzgeç HİÇ
// gönderilmez (arama koleksiyon geneli, depo elemesi parseServiceCodeHits'te).
func serviceSearchFilters(project, repo string) map[string]any {
	if strings.TrimSpace(project) == "" {
		return nil
	}
	return map[string]any{"Project": []string{strings.TrimSpace(project)}, "Repository": []string{repo}}
}

// serviceSearchTop — SAF: süzgeçsiz aramada depo elemesine malzeme kalsın.
func serviceSearchTop(project string, top int) int {
	if strings.TrimSpace(project) == "" && top < serviceSearchWideTop {
		return serviceSearchWideTop
	}
	return top
}

// EffectiveProject — yürürlükteki DevOps projesi (Ayarlar'daki açık Project
// kazanır; boşsa öneri — pickProject sırası). Boş = çözülemedi.
func (s *Service) EffectiveProject(hint ProjectHint) string {
	if s == nil {
		return ""
	}
	p, _, _ := pickProject(s.CurrentSettings(), hint)
	return p
}

// SearchServiceCode — depo içinde Code Search, ≤top isabet. project: deponun
// DevOps projesi (çağıranın çözdüğü — pickProject); boşsa süzgeçsiz aranır.
func (s *Service) SearchServiceCode(ctx context.Context, project, repo, text string, top int) ([]ServiceCodeHit, error) {
	text, repo = strings.TrimSpace(text), strings.TrimSpace(repo)
	if text == "" || repo == "" {
		return nil, fmt.Errorf("arama metni ve depo gerekli")
	}
	if !s.CodeSearchEnabled() {
		return nil, fmt.Errorf("kod arama kapalı (Ayarlar → Kod entegrasyonu → Code Search)")
	}
	cfg := s.CurrentSettings()
	cli := s.clientFor(cfg.InsecureSkipVerify)
	body, err := json.Marshal(searchCodeBody{SearchText: text, Top: serviceSearchTop(project, top), Filters: serviceSearchFilters(project, repo)})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL(cfg), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "coremetry-devops/1.0")
	if cfg.PAT != "" || cfg.Username != "" {
		req.SetBasicAuth(cfg.Username, cfg.PAT)
	}
	resp, err := cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, fileBodyCap))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("kod arama http %d: %s", resp.StatusCode, firstLine(string(raw)))
	}
	return parseServiceCodeHits(raw, repo, top)
}

// parseServiceCodeHits — SAF: yanıt → isabetler; başka depo / TFVC elenir,
// parça tek satıra ve 200 karaktere kırpılır.
func parseServiceCodeHits(raw []byte, repo string, top int) ([]ServiceCodeHit, error) {
	var out searchCodeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("kod arama yanıtı çözümlenemedi: %w", err)
	}
	hits := []ServiceCodeHit{}
	for _, r := range out.Results {
		if !strings.EqualFold(r.Repository.Name, repo) || strings.EqualFold(r.Repository.Type, "tfvc") {
			continue
		}
		h := ServiceCodeHit{Path: strings.TrimPrefix(r.Path, "/")}
		if len(r.Versions) > 0 {
			h.Branch = strings.TrimPrefix(r.Versions[0].BranchName, "refs/heads/")
		}
		for _, m := range r.Matches.Content {
			if m.Line > 0 {
				h.Line = m.Line
				sn := strings.TrimSpace(strings.ReplaceAll(m.CodeSnippet, "\n", " "))
				if len([]rune(sn)) > 200 {
					sn = string([]rune(sn)[:200]) + "…"
				}
				h.Snippet = sn
				break
			}
		}
		hits = append(hits, h)
		if len(hits) == top {
			break
		}
	}
	return hits, nil
}
