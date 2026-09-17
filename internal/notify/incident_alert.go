package notify

// incident_alert.go — v0.10.748: incident açılış/çözüm bildirimi
// (operatör: "anomali, incident ve problems ayrı ayrı gelsin", Dilim 2).
//
// Yeni bir bildirim hattı YAZILMADI: incident, Problem şekline sokulup
// SendProblemAlert'ten geçer — ciddiyet süzgeci, kanal eşleşmesi
// (v0.10.747 tür süzgeci "incident"), sessiz saat, bakım penceresi,
// ekip-maili, iki katmanlı tekrar tabanı (incident id + durum) ve
// notification_log hepsi bedava. exceptionAsProblem ile aynı desen.
//
// Kimlik grameri: ID = incident id (tekrar tabanı ve related_id),
// RuleID = "incident:<id>" (chstore.ProblemNotifyKind → incident;
// computePriority → critical P1 / warning P2), Kind = "incident"
// (konu linki /incident?id=, related_kind). Aktör ve olay gövdesi
// açıklamada — operatör kararı: manuel açılış da bildirir, kim açtığı
// yazar.

import (
	"context"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// IncidentLifecycle — chstore.IncidentLifecycleHook imzası; boot'ta
// store.SetIncidentLifecycleHook(notifier.IncidentLifecycle). Çağıranı
// bloklamaz: diğer açılış yollarıyla aynı `go` + Background disiplini
// (istek bağlamı kapanınca bildirim yarım kalmasın).
func (n *Notifier) IncidentLifecycle(_ context.Context, inc chstore.Incident, ev chstore.IncidentEvent) {
	go n.SendProblemAlert(context.Background(), incidentAsProblem(inc, ev))
}

// incidentAsProblem — SAF. created → open, resolved → resolved; boş
// ciddiyet warning (store varsayılanıyla aynı); boş aktör "system".
func incidentAsProblem(inc chstore.Incident, ev chstore.IncidentEvent) chstore.Problem {
	status := "open"
	if ev.Kind == "resolved" {
		status = "resolved"
	}
	sev := inc.Severity
	if sev == "" {
		sev = "warning"
	}
	actor := ev.Actor
	if actor == "" {
		actor = "system"
	}
	desc := inc.Title
	if inc.Summary != "" {
		desc += " — " + inc.Summary
	}
	if ev.Body != "" {
		desc += " · " + ev.Body
	}
	desc += " · actor: " + actor
	return chstore.Problem{
		ID:          inc.ID,
		RuleID:      "incident:" + inc.ID,
		RuleName:    "Incident · " + inc.Title,
		Severity:    sev,
		Service:     inc.Service,
		Kind:        chstore.NotifyKindIncident,
		Metric:      "incident",
		Status:      status,
		Description: desc,
		Assignee:    inc.Assignee,
		StartedAt:   inc.StartedAt,
		ResolvedAt:  inc.ResolvedAt,
		Clusters:    inc.Clusters,
	}
}

// subjectURL — bildirimdeki "Open in Coremetry" linki konuya göre:
// incident → /incident?id= (Incidents sayfasının kendi linkiyle aynı),
// problem → problemURL. Yedi şablon (e-posta/slack/webhook/zoom/...)
// bunu çağırır; ham problemURL(p.ID) çağrısı kalmadı (pin testte).
func (n *Notifier) subjectURL(p chstore.Problem) string {
	if p.Kind == chstore.NotifyKindIncident {
		base := n.PublicURL()
		if base == "" {
			return ""
		}
		return base + "/incident?id=" + p.ID
	}
	return n.problemURL(p.ID)
}
