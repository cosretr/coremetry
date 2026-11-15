import type { ClusterAlertRow } from '@/lib/types';
import { ClusterCard } from './ClusterCard';

// AlertsPanel — Overview'un açık alarmları (v0.9.36, design handoff).
// Clusters S7 (plan §0 D2, v0.10.2175): Clusters.tsx'ten taşındı; kritikler
// önce (backend sıralı, burada da sıralanır — sıra sözleşmesi tek yerde
// değil), "critical only" kutusu KALKTI. Satır hover'ı JS değil CSS
// (`.cl-alert:hover`), satır içi stil yok.

// fmtAgeShort — saniye → "Nm"/"Nh"/"Nd" (design handoff alert "Nm ago").
function fmtAgeShort(sec?: number): string {
  if (!sec || sec <= 0) return '';
  if (sec < 3600) return `${Math.round(sec / 60)}m`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h`;
  return `${Math.round(sec / 86400)}d`;
}

/** Kritikler önce; aynı düzeyde gelen sıra korunur. */
function criticalFirst(alerts: readonly ClusterAlertRow[]): ClusterAlertRow[] {
  const crit = alerts.filter(a => a.severity === 'critical');
  return [...crit, ...alerts.filter(a => a.severity !== 'critical')];
}

export function AlertsPanel({ alerts, title = 'Firing alerts' }: { alerts: ClusterAlertRow[]; title?: string }) {
  return (
    <ClusterCard header={
      <span>{title} <span className="badge b-err cl-alerts-count">{alerts.length}</span></span>
    }>
      <div className="cl-alerts">
        {criticalFirst(alerts).map((a, i) => {
          const age = fmtAgeShort(a.ageSec);
          return (
            <div key={`${a.alertName}|${a.namespace}|${a.pod}|${i}`} className="cl-alert">
              <span className={`badge ${a.severity === 'critical' ? 'b-err' : 'b-warn'}`}>{a.severity}</span>
              <span className="cl-alert__name">{a.alertName}</span>
              {(a.namespace || a.pod) && (
                <span className="mono cl-alert__where">{a.namespace}{a.pod ? `/${a.pod}` : ''}</span>
              )}
              {age && <span className="cl-alert__age">{age} ago</span>}
            </div>
          );
        })}
      </div>
    </ClusterCard>
  );
}
