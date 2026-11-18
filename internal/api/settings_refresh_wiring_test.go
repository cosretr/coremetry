package api

import (
	"os"
	"strings"
	"testing"
)

// v0.10.2349 — srv'nin ayar okuyucuları + özel roller + thanos etiket
// denetimleri ayrı 30 s tikleri yerine tek anlık görüntü yenileyicisinde
// (cfgRefresh: tik başına tek system_settings FINAL okuması). Kayıtlar
// cfgRefresh.Start'tan ÖNCE; ayrı ticker geri gelmez.
func TestSettingsLoadersOnSnapshotRefresher(t *testing.T) {
	b, err := os.ReadFile("../../main.go")
	if err != nil {
		t.Fatal(err)
	}
	mainGo := stripGoComments(string(b))
	start := strings.Index(mainGo, "go cfgRefresh.Start(")
	if start < 0 {
		t.Fatal("main.go: go cfgRefresh.Start( yok")
	}
	for key, load := range map[string]string{
		"exception-triage":    "srv.LoadExceptionTriage(ctx)",
		"problem-priority":    "srv.LoadProblemPriority(ctx)",
		"notify-policy":       "srv.LoadNotifyPolicy(ctx)",
		"ai-chat-retention":   "srv.LoadAIChatRetention(ctx)",
		"metric-exclusions":   "srv.LoadMetricExclusions(ctx)",
		"trace-root-def":      "srv.LoadTraceRootDef(ctx)",
		"promql-console":      "srv.LoadPromQLConsoleSettings(ctx)",
		"logs-search":         "srv.LoadLogsSearchSettings(ctx)",
		"anomaly-tracked":     "srv.LoadAnomalyTracked(ctx)",
		"anomaly-sensitivity": "srv.LoadAnomalySensitivity(ctx)",
		"custom-roles":        "authSvc.LoadPersistedCustomRoles(ctx, store)",
		"thanos-label-checks": "thanosSvc.LoadLabelChecks(ctx, store)",
	} {
		i := strings.Index(mainGo, `cfgRefresh.Add("`+key+`", func(ctx context.Context) error {`)
		if i < 0 || !strings.Contains(mainGo[i:i+200], load) {
			t.Errorf("main.go: cfgRefresh.Add(%q) → %s yok", key, load)
			continue
		}
		if i > start {
			t.Errorf("cfgRefresh.Add(%q) cfgRefresh.Start'tan sonra", key)
		}
	}
	for _, gone := range []string{"StartCustomRoleRefresh", "StartLabelCheckRefresh", "srv.StartExceptionTriageRefresh", "srv.StartProblemPriorityRefresh", "srv.StartNotifyPolicyRefresh", "srv.StartAIChatRetentionRefresh", "srv.StartMetricExclusionsRefresh", "srv.StartTraceRootDefRefresh", "srv.StartPromQLConsoleSettingsRefresh", "srv.StartLogsSearchSettingsRefresh", "srv.StartAnomalyTrackedRefresh", "srv.StartAnomalySensitivityRefresh"} {
		if strings.Contains(mainGo, gone) {
			t.Errorf("main.go ayrı 30 s ayar tiki içeriyor: %q", gone)
		}
	}
}
