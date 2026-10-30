package api

// ai_evalset_agent_scope.go — CoSRE v2 araç seti dilim 2 (docs/cosre/tools-audit.md
// §10 G8): ajan kipi vakası kullanıcı kapsamını ölçer.
//
// Vaka `userTeam` taşırsa koşucu v2 döngüsünü işaretler (withCosreV2Loop) ve
// kapsam users tablosu okunmadan o fikstür takımından çözülür
// (chatEvalMode.UserTeam). `expect.servicesInScope` true ise yürüyen HER
// çağrının servis argümanları (services[] ve tekil service) çözülen kapsamın
// alt kümesi olmalı; kapsam dışı ad = fail. Fikstür takımı bu kurulumun
// kataloğunda servis sahibi değilse (kapsam çözülemedi) vaka model hatası
// değil kurulum farkıdır → atlanır.

import (
	"encoding/json"
	"fmt"
	"strings"

	agenttools "github.com/cilcenk/coremetry/internal/ai/agent/tools"
)

// evalScopeCall — yürüyen bir çağrının servis argümanları.
type evalScopeCall struct {
	Tool     string
	Services []string
}

// evalCallServices — SAF: argümandaki servis adları (services[] + service).
func evalCallServices(args json.RawMessage) []string {
	var a struct {
		Services []string `json:"services"`
		Service  string   `json:"service"`
	}
	if json.Unmarshal(args, &a) != nil {
		return nil
	}
	out := make([]string, 0, len(a.Services)+1)
	for _, s := range a.Services {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if s := strings.TrimSpace(a.Service); s != "" {
		out = append(out, s)
	}
	return out
}

func (c *evalAgentCollector) onScope(u userScope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.st.Scope = &u
}

// noteScopeCall — onTool'dan (kilit altında) yürüyen çağrının servisleri.
func (c *evalAgentCollector) noteScopeCall(name string, args json.RawMessage, oc agenttools.Outcome) {
	if !oc.Executed {
		return
	}
	if svcs := evalCallServices(args); len(svcs) > 0 {
		c.st.ScopeCalls = append(c.st.ScopeCalls, evalScopeCall{Tool: name, Services: svcs})
	}
}

// evalScopeHarnessGap — SAF: servicesInScope vakasında kapsam çözülemediyse neden.
func evalScopeHarnessGap(c evalCase, st evalAgentStats) string {
	if !c.Expect.ServicesInScope {
		return ""
	}
	switch {
	case st.Scope == nil:
		return "kullanıcı kapsamı çözülmedi (v2 döngüsü koşmadı)"
	case st.Scope.Unscoped:
		return fmt.Sprintf("fikstür takımı %q kapsam vermedi (%s) — kurulumun kataloğunda servis sahibi değil", c.UserTeam, st.Scope.Reason)
	}
	return ""
}

// evalScopeFails — SAF: kapsam dışına çıkan her çağrı bir ihlal (G8).
func evalScopeFails(c evalCase, st evalAgentStats) []string {
	if !c.Expect.ServicesInScope || st.Scope == nil || st.Scope.Unscoped {
		return nil
	}
	in := make(map[string]bool, len(st.Scope.Services))
	for _, s := range st.Scope.Services {
		in[s] = true
	}
	var fails []string
	for _, call := range st.ScopeCalls {
		for _, s := range call.Services {
			if !in[s] {
				fails = append(fails, fmt.Sprintf("service outside user scope: %s (%s)", s, call.Tool))
			}
		}
	}
	return fails
}
