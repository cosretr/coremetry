package mcptools

// repo_lookup.go — v0.10.2449 (operatör raporu 2026-11-22: "reponun linkini
// veremez misin", "şu söylediğim podun reposunu getirir misin").
//
// Servis (ya da pod → servis, ENTITY modeli; ad eşlemesi yok) → kaynak depo
// URL'i. Kaynak sırası (operatör 2026-11-22: prod'da Argo CD bağlantısı YOK,
// Argo yalnız isteğe bağlı kaynak):
//  1. span resource öznitelikleri (vcs.repository.url.full, git.repository_url,
//     service.repository … — chstore.RepoAttrKeys), sınırlı ham okuma;
//  2. servis kataloğu pini (service_metadata.repository) — api dikişi;
//  3. Azure DevOps bağlantısı (mevcut kod entegrasyonu): pin ya da ad
//     konvansiyonu (önek + ortam eki soyma, devops.ResolveRepo) → depo/branş
//     sunucuda DOĞRULANIR (ResolveDryRun) → tarayıcı linki;
//  4. Argo CD (yalnız bağlıysa): uygulamanın son senkronundaki repoURL.
// Hiçbiri yoksa cevap dürüstçe "unknown" + neyin bunu açacağı. Yeni dış
// entegrasyon / kimlik ayarı YOK.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcp"
)

// RepoLookupName — aracın adı.
const RepoLookupName = "repo_lookup"

// Kaynak durumları.
const (
	RepoStatusFound         = "found"
	RepoStatusAbsent        = "absent"
	RepoStatusNotConfigured = "not_configured"
	RepoStatusError         = "error"
)

// RepoCandidate — tek kaynaktan bulunan depo.
type RepoCandidate struct {
	Source   string `json:"source"` // resource_attributes | catalog_pin | azure_devops | argocd
	URL      string `json:"url,omitempty"`
	Repo     string `json:"repo,omitempty"`
	Project  string `json:"project,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Revision string `json:"revision,omitempty"`
	Verified bool   `json:"verified"` // depo sunucuda bulundu / değer telemetride saklı
	Detail   string `json:"detail,omitempty"`
}

// RepoSourceCheck — kaynağın denendiği ve sonucu.
type RepoSourceCheck struct {
	Source string `json:"source"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// RepoSourceReport — api dikişinin (pin + DevOps + Argo) sonucu.
type RepoSourceReport struct {
	Candidates []RepoCandidate
	Checked    []RepoSourceCheck
}

// RepoSourcesFunc — api dikişi (Deps.RepoSources).
type RepoSourcesFunc func(ctx context.Context, service string) RepoSourceReport

type repoLookupArgs struct {
	Service string `json:"service,omitempty"`
	Pod     string `json:"pod,omitempty"`
}

type repoAttrReader interface {
	ServiceRepoAttrs(ctx context.Context, service string, from, to time.Time) (map[string]string, error)
}

// repoURLAttrKeys — URL taşıyan anahtarlar (öncelik sırası); ad/ref anahtarları ayrı.
var repoURLAttrKeys = []string{"vcs.repository.url.full", "vcs.repository.url", "git.repository_url", "git.repository.url",
	"git.remote.url", "service.repository", "scm.repository.url", "repository.url"}

// repoFromAttrs — SAF: resource öznitelikleri → aday (yoksa ok=false).
func repoFromAttrs(attrs map[string]string) (RepoCandidate, bool) {
	c := RepoCandidate{Source: "resource_attributes", Verified: true}
	for _, k := range repoURLAttrKeys {
		if v := strings.TrimSpace(attrs[k]); v != "" {
			c.URL, c.Detail = RepoWebURL(v), k
			break
		}
	}
	c.Repo = strings.TrimSpace(attrs["vcs.repository.name"])
	c.Branch = strings.TrimSpace(firstNonEmpty(attrs["vcs.ref.head.name"], attrs["git.branch"]))
	c.Revision = strings.TrimSpace(firstNonEmpty(attrs["vcs.ref.head.revision"], attrs["git.commit.sha"]))
	if c.URL == "" && c.Repo == "" {
		return RepoCandidate{}, false
	}
	return c, true
}

// RepoWebURL — SAF: git@host:org/repo.git → https://host/org/repo; userinfo ve
// sondaki .git atılır. Tanınmayan biçim aynen döner.
func RepoWebURL(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "git@") {
		rest := strings.TrimPrefix(v, "git@")
		if i := strings.IndexByte(rest, ':'); i > 0 {
			v = "https://" + rest[:i] + "/" + rest[i+1:]
		}
	}
	if i := strings.Index(v, "://"); i >= 0 {
		rest := v[i+3:]
		if at := strings.IndexByte(rest, '@'); at >= 0 && at < strings.IndexAny(rest+"/", "/") {
			v = v[:i+3] + rest[at+1:]
		}
	}
	return strings.TrimSuffix(v, ".git")
}

// repoEnableHint — hiçbir kaynak depo vermediğinde neyin açacağı.
const repoEnableHint = "Repo bilgisi yok. Açmak için biri yeterli: (1) servisin OTel resource'una vcs.repository.url.full (ya da git.repository_url) ekleyin; " +
	"(2) servis kataloğunda Repository alanına depo URL'ini yazın; (3) Ayarlar › Kod entegrasyonu'nda Azure DevOps bağlantısını yapılandırın (depo adı servis adından türetilir)."

func runRepoLookup(ctx context.Context, d Deps, r repoAttrReader, a repoLookupArgs) (map[string]any, error) {
	svc := strings.TrimSpace(a.Service)
	pod := strings.TrimSpace(a.Pod)
	out := map[string]any{"tool": RepoLookupName}
	var checked []RepoSourceCheck
	if svc == "" && pod != "" {
		out["pod"] = pod
		if d.PodServices == nil {
			return nil, errors.New("pod → service resolution needs the entity model, which is not available — ask the user for the service name")
		}
		svcs, err := d.PodServices(ctx, pod)
		if err != nil && len(svcs) == 0 {
			return nil, fmt.Errorf("pod lookup failed: %w", err)
		}
		switch len(svcs) {
		case 0:
			return map[string]any{"tool": RepoLookupName, "pod": pod, "found": false,
				"note": "no service is linked to this pod in the entity model — ask the user for the exact pod or service name"}, nil
		case 1:
			svc = svcs[0]
			out["service_from_pod"] = "entity model"
		default:
			sort.Strings(svcs)
			return map[string]any{"tool": RepoLookupName, "pod": pod, "found": false, "candidates_services": svcs,
				"note": "the pod name maps to several services (several clusters) — ask which one"}, nil
		}
	}
	if svc == "" {
		return nil, errors.New("give service or pod — ask the user which service they mean, never guess one")
	}
	out["service"] = svc
	var cands []RepoCandidate
	// 1) resource öznitelikleri
	if r != nil {
		from, to := rangeWindow(ctx, 3600)
		attrs, err := r.ServiceRepoAttrs(ctx, svc, from, to)
		switch {
		case err != nil:
			checked = append(checked, RepoSourceCheck{Source: "resource_attributes", Status: RepoStatusError, Detail: err.Error()})
		default:
			if c, ok := repoFromAttrs(attrs); ok {
				cands = append(cands, c)
				checked = append(checked, RepoSourceCheck{Source: "resource_attributes", Status: RepoStatusFound, Detail: c.Detail})
			} else {
				checked = append(checked, RepoSourceCheck{Source: "resource_attributes", Status: RepoStatusAbsent,
					Detail: "no vcs.* / git.* repository attribute on the service's spans in the last hour"})
			}
		}
	}
	// 2-4) katalog pini, Azure DevOps, Argo CD (api dikişi)
	if d.RepoSources != nil {
		rep := d.RepoSources(ctx, svc)
		cands = append(cands, rep.Candidates...)
		checked = append(checked, rep.Checked...)
	} else {
		checked = append(checked, RepoSourceCheck{Source: "catalog_pin / azure_devops / argocd", Status: RepoStatusNotConfigured, Detail: "not wired in this server"})
	}
	out["sources_checked"] = checked
	best := -1
	for i, c := range cands {
		if c.URL == "" {
			continue
		}
		if best < 0 || (c.Verified && !cands[best].Verified) {
			best = i
		}
	}
	if cands == nil {
		cands = []RepoCandidate{}
	}
	out["candidates"] = cands
	if best < 0 {
		out["found"] = false
		out["repo_url"] = "unknown"
		out["note"] = repoEnableHint
		return out, nil
	}
	out["found"] = true
	out["repo_url"] = cands[best].URL
	out["source"] = cands[best].Source
	out["verified"] = cands[best].Verified
	if !cands[best].Verified {
		out["note"] = "repo derived from a naming convention and not verified on the server — say it is a guess"
	}
	return out, nil
}

func repoLookupTool(d Deps) mcp.Tool {
	return mcp.Tool{
		Name:             RepoLookupName,
		ShortDescription: "Servis/pod repo linki: resource öznitelikleri, katalog, Azure DevOps, Argo; yoksa unknown.",
		Description: "Return the source repository URL for a service, or for a pod (pod → service via the entity model). Sources in order: span resource attributes (vcs.repository.url.full, git.repository_url, service.repository …), " +
			"the service catalog's Repository pin, the configured Azure DevOps connection (pin or naming convention, verified on the server — repo, project, branch and browse URL), and Argo CD application source repo when Argo is connected. " +
			"Returns repo_url (or \"unknown\"), source, verified, candidates and sources_checked (each source with found | absent | not_configured | error). " +
			"When found=false, say the repo is unknown and relay `note` (what would enable it) — never invent a repository URL. COST: one bounded span read (≤1 h, LIMIT 1) plus cached catalog/DevOps reads.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"service": map[string]any{"type": "string", "description": "Exact service name."},
				"pod":     map[string]any{"type": "string", "description": "Pod name when the user names a pod instead of a service."},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var a repoLookupArgs
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &a); err != nil {
					return nil, fmt.Errorf("decode args: %w", err)
				}
			}
			var r repoAttrReader
			if d.Store != nil {
				r = d.Store
			}
			return runRepoLookup(ctx, d, r, a)
		},
	}
}
