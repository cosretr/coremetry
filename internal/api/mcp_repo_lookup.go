package api

// mcp_repo_lookup.go — v0.10.2449: repo_lookup'un api dikişleri
// (mcptools.Deps.RepoSources + PodServices). Yeni dış entegrasyon ya da
// kimlik ayarı YOK; mevcut kaynaklar okunur:
//   - servis kataloğu pini (service_metadata.repository; okuma hatası
//     fail-closed — pinReadDecision, kod okuyucularıyla aynı karar),
//   - Azure DevOps bağlantısı (devops.ResolveDryRun: pin ya da konvansiyon →
//     proje → branş → ağaç; doğrulanan depo için devops.BrowseURL),
//   - Argo CD (yalnız bağlıysa; operatör 2026-11-22: prod'da Argo YOK):
//     servisin uygulamaları (mcpArgoApps) → argocd_sync_events.repo_url.
// Pod → servis: entity.Resolver.PodsNamed (tam ad; önek tahmini yok).

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/devops"
	"github.com/cilcenk/coremetry/internal/mcptools"
)

func (s *Server) mcpRepoSources(ctx context.Context, service string) mcptools.RepoSourceReport {
	var rep mcptools.RepoSourceReport
	pin, abort := "", ""
	if s.store != nil {
		md, err := s.store.GetServiceMetadataStrict(ctx, service)
		mdRepo := ""
		if md != nil {
			mdRepo = md.Repository
		}
		pin, abort = pinReadDecision(mdRepo, md != nil, err)
		rep = repoPinCandidate(rep, pin, abort)
	}
	rep = s.repoDevOpsCandidate(ctx, rep, service, pin, abort)
	return s.repoArgoCandidate(ctx, rep, service)
}

// repoPinCandidate — SAF: katalog pini → aday / kontrol satırı.
func repoPinCandidate(rep mcptools.RepoSourceReport, pin, abort string) mcptools.RepoSourceReport {
	const src = "catalog_pin"
	switch {
	case abort != "":
		rep.Checked = append(rep.Checked, mcptools.RepoSourceCheck{Source: src, Status: mcptools.RepoStatusError, Detail: abort})
	case strings.TrimSpace(pin) == "":
		rep.Checked = append(rep.Checked, mcptools.RepoSourceCheck{Source: src, Status: mcptools.RepoStatusAbsent, Detail: "service catalog Repository field is empty"})
	default:
		c := mcptools.RepoCandidate{Source: src, Detail: "service_metadata.repository"}
		if strings.Contains(pin, "://") || strings.HasPrefix(pin, "git@") {
			c.URL, c.Verified = mcptools.RepoWebURL(pin), true // operatörün yazdığı link
		} else {
			c.Repo = strings.TrimSpace(pin) // yalnız ad: link DevOps adımından
		}
		rep.Candidates = append(rep.Candidates, c)
		rep.Checked = append(rep.Checked, mcptools.RepoSourceCheck{Source: src, Status: mcptools.RepoStatusFound, Detail: strings.TrimSpace(pin)})
	}
	return rep
}

func (s *Server) repoDevOpsCandidate(ctx context.Context, rep mcptools.RepoSourceReport, service, pin, abort string) mcptools.RepoSourceReport {
	const src = "azure_devops"
	add := func(status, detail string) {
		rep.Checked = append(rep.Checked, mcptools.RepoSourceCheck{Source: src, Status: status, Detail: detail})
	}
	if s.devops == nil || !s.devops.Configured() {
		add(mcptools.RepoStatusNotConfigured, "no Azure DevOps connection (Settings › Code integration); no other git provider integration exists")
		return rep
	}
	if abort != "" {
		add(mcptools.RepoStatusError, "catalog pin could not be read: "+abort)
		return rep
	}
	if ok, _ := s.sourceRateAllow(ctx, sourceRateRead); !ok {
		add(mcptools.RepoStatusError, "per-user git call limit reached; try again shortly")
		return rep
	}
	dr := s.devops.ResolveDryRun(ctx, service, devops.PinRead{Repo: pin})
	if dr.Repo == "" || dr.Project == "" {
		add(mcptools.RepoStatusAbsent, lastDryRunDetail(dr))
		return rep
	}
	cfg := s.devops.CurrentSettings()
	cfg.Project = dr.Project
	c := mcptools.RepoCandidate{Source: src, Repo: dr.Repo, Project: dr.Project, Branch: dr.Branch,
		URL: devops.BrowseURL(cfg, dr.Repo, dr.Branch), Verified: dr.Branch != "", Detail: "repo source: " + dr.Source}
	if !c.Verified {
		c.Detail += "; not verified on the server: " + lastDryRunDetail(dr)
	}
	rep.Candidates = append(rep.Candidates, c)
	add(mcptools.RepoStatusFound, c.Detail)
	return rep
}

// lastDryRunDetail — SAF: kuru koşunun son (başarısız) adımının açıklaması.
func lastDryRunDetail(dr devops.DryRunResult) string {
	for i := len(dr.Steps) - 1; i >= 0; i-- {
		if !dr.Steps[i].OK {
			return dr.Steps[i].Label + ": " + dr.Steps[i].Detail
		}
	}
	if n := len(dr.Steps); n > 0 {
		return dr.Steps[n-1].Label + ": " + dr.Steps[n-1].Detail
	}
	return "repo could not be resolved"
}

func (s *Server) repoArgoCandidate(ctx context.Context, rep mcptools.RepoSourceReport, service string) mcptools.RepoSourceReport {
	const src = "argocd"
	add := func(status, detail string) {
		rep.Checked = append(rep.Checked, mcptools.RepoSourceCheck{Source: src, Status: status, Detail: detail})
	}
	if argocdSettingsSvc.Load() == nil || s.store == nil {
		add(mcptools.RepoStatusNotConfigured, "Argo CD is not connected (optional source)")
		return rep
	}
	apps, err := s.mcpArgoApps(ctx, service)
	if err != nil {
		add(mcptools.RepoStatusError, err.Error())
		return rep
	}
	var names []string
	for _, a := range apps {
		names = append(names, a.App)
	}
	if len(names) == 0 {
		add(mcptools.RepoStatusAbsent, "no Argo CD application mapped to this service")
		return rep
	}
	urls, err := s.store.ArgoAppRepoURLs(ctx, names)
	if err != nil {
		add(mcptools.RepoStatusError, "argocd_sync_events read failed")
		return rep
	}
	sort.Strings(names)
	found := false
	for _, n := range names {
		if u := urls[n]; u != "" {
			rep.Candidates = append(rep.Candidates, mcptools.RepoCandidate{Source: src, URL: mcptools.RepoWebURL(u), Verified: true, Detail: "Argo CD app " + n})
			found = true
		}
	}
	if found {
		add(mcptools.RepoStatusFound, strings.Join(names, ", "))
	} else {
		add(mcptools.RepoStatusAbsent, "Argo CD apps have no recorded source repo")
	}
	return rep
}

// mcpPodServices — pod adı → sahibi servis(ler), entity modeli (tam ad).
func (s *Server) mcpPodServices(ctx context.Context, pod string) ([]string, error) {
	cids := s.entityClusterIDs()
	if len(cids) == 0 {
		return nil, errors.New("no entity clusters configured")
	}
	pods, err := s.entityResolver().PodsNamed(ctx, strings.TrimSpace(pod), cids, time.Time{})
	seen := map[string]bool{}
	var out []string
	for _, p := range pods {
		for _, svc := range p.Services {
			if svc != "" && !seen[svc] {
				seen[svc] = true
				out = append(out, svc)
			}
		}
	}
	sort.Strings(out)
	return out, err
}
