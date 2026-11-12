package mcptools

// service_healthcheck_cluster.go — v0.10.2042 (servis sağlık kontrolü,
// dilim 2): kontrol 9 hazır replika + kontrol 10 cluster sağlığı. Okuma pod
// adaptöründe (api/mcp_service_pods.go, ServicePodsOpts.Replicas /
// ClusterHealth), yalnız servisin pod'u bulunan cluster'larda; burada SAF
// sınıflandırma.
//
// Cluster sağlığı kuralı (spec "servis namespace'i önce"): NotReady node ya
// da servisin namespace'inde critical alert → Sorun; başka namespace'te
// critical alert ya da servis namespace'inde warning → Dikkat; başka
// namespace'teki warning'ler İyi'yi bozmaz, sayısı yazılır (OpenShift
// cluster'larında hep birkaç warning yanar — her servisi "Dikkat"e çekmesin).

import (
	"fmt"
	"sort"
	"strings"
)

// hcReplicas — kontrol 9. Hazır < istenen → Dikkat; hazır = 0 < istenen →
// Sorun; 0'a ölçeklenmiş iş yükü bilgi (İyi). İş yükü bulunamazsa / hazır
// serisi yoksa Ölçülemedi (DeploymentConfig gibi kube-state dışı sahip).
func hcReplicas(service, cluster string, cl ServicePodsCluster) HealthcheckRow {
	row := hcBase(service, HCCheckReplicas, cluster)
	switch {
	case cl.WorkloadsErr != "":
		row.State, row.Seen = HCUnknown, "okunamadı: "+truncateRunes(cl.WorkloadsErr, hcErrRunes)
		return row
	case !cl.WorkloadsOK:
		row.State, row.Seen = HCUnknown, "replika okunmadı"
		return row
	case len(cl.Workloads) == 0:
		row.State = HCUnknown
		row.Seen = "Deployment / StatefulSet bulunamadı"
		if len(cl.WorkloadNames) > 0 {
			row.Seen += " (aranan: " + strings.Join(cl.WorkloadNames, ", ") + ")"
		}
		return row
	}
	ws := append([]WorkloadReplicas(nil), cl.Workloads...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Namespace+"/"+ws[i].Name < ws[j].Namespace+"/"+ws[j].Name })
	row.State = HCOK
	var parts, bad []string
	unknown := 0
	for _, w := range ws {
		label := w.Name
		if w.Namespace != "" {
			label += " (" + w.Namespace + ")"
		}
		if !w.DesiredOK || !w.ReadyOK {
			unknown++
			parts = append(parts, label+": hazır sayısı okunamadı")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %.0f/%.0f hazır", label, w.Ready, w.Desired))
		switch {
		case w.Desired == 0:
			// 0'a ölçeklenmiş — bilgi.
		case w.Ready == 0:
			row.State = hcWorse(row.State, HCCrit)
			bad = append(bad, w.Name)
		case w.Ready < w.Desired:
			row.State = hcWorse(row.State, HCWarn)
			bad = append(bad, w.Name)
		}
	}
	if unknown == len(ws) {
		row.State = HCUnknown
	}
	row.Seen = strings.Join(parts, "; ")
	if len(bad) > 0 {
		row.Short = "hazır replika eksik"
		if row.State == HCCrit {
			row.Short = "hazır replika yok"
		}
	}
	return row
}

// hcServiceNamespaces — SAF: servis pod'larının namespace kümesi.
func hcServiceNamespaces(cl ServicePodsCluster) map[string]bool {
	out := map[string]bool{}
	for _, p := range cl.Rows {
		if p.Namespace != "" {
			out[p.Namespace] = true
		}
	}
	return out
}

// hcClusterHealth — kontrol 10.
func hcClusterHealth(service, cluster string, cl ServicePodsCluster) HealthcheckRow {
	row := hcBase(service, HCCheckCluster, cluster)
	if !cl.NodeReadyOK && !cl.AlertsOK {
		row.State, row.Seen = HCUnknown, "node durumu ve alert'ler okunamadı"
		return row
	}
	own := hcServiceNamespaces(cl)
	var parts []string
	state := HCOK
	var shorts []string
	if cl.NodeReadyOK {
		if cl.NodesNotReady > 0 {
			state = HCCrit
			parts = append(parts, fmt.Sprintf("%d node hazır değil (%d hazır)", cl.NodesNotReady, cl.NodesReady))
			shorts = append(shorts, "node hazır değil")
		} else {
			parts = append(parts, fmt.Sprintf("%d node hazır", cl.NodesReady))
		}
	} else {
		parts = append(parts, "node durumu okunamadı")
	}
	if cl.AlertsOK {
		var ownCrit, ownWarn, otherCrit []string
		otherWarn := 0
		for _, a := range cl.Alerts {
			mine := own[a.Namespace]
			switch {
			case a.Severity == "critical" && mine:
				ownCrit = append(ownCrit, a.Name)
			case a.Severity == "critical":
				otherCrit = append(otherCrit, a.Name)
			case mine:
				ownWarn = append(ownWarn, a.Name)
			default:
				otherWarn += max(a.Count, 1)
			}
		}
		uniq := func(xs []string) string {
			sort.Strings(xs)
			out := xs[:0]
			for i, x := range xs {
				if i == 0 || x != xs[i-1] {
					out = append(out, x)
				}
			}
			if len(out) > 3 {
				return strings.Join(out[:3], ", ") + fmt.Sprintf(" +%d", len(out)-3)
			}
			return strings.Join(out, ", ")
		}
		if len(ownCrit) > 0 {
			state = hcWorse(state, HCCrit)
			parts = append(parts, "servis namespace'inde critical: "+uniq(ownCrit))
			shorts = append(shorts, "critical alert")
		}
		if len(otherCrit) > 0 {
			state = hcWorse(state, HCWarn)
			parts = append(parts, "cluster'da critical: "+uniq(otherCrit))
			shorts = append(shorts, "cluster'da critical alert")
		}
		if len(ownWarn) > 0 {
			state = hcWorse(state, HCWarn)
			parts = append(parts, "servis namespace'inde warning: "+uniq(ownWarn))
			shorts = append(shorts, "warning alert")
		}
		if len(ownCrit)+len(otherCrit)+len(ownWarn) == 0 {
			parts = append(parts, "servis namespace'inde firing alert yok")
		}
		if otherWarn > 0 {
			parts = append(parts, fmt.Sprintf("cluster genelinde %d warning", otherWarn))
		}
	} else {
		parts = append(parts, "alert'ler okunamadı")
	}
	row.Seen = strings.Join(parts, "; ")
	// Bir yarı okunamadıysa ve okunan yarı temizse hüküm "İyi" DEĞİL.
	if state == HCOK && (!cl.NodeReadyOK || !cl.AlertsOK) {
		state = HCUnknown
	}
	row.State = state
	if state == HCCrit || state == HCWarn {
		row.Short = shorts[0]
	}
	return row
}
