import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import { Badge, Drawer } from '@/components/ui';
import { ClusterChips } from '@/components/ClusterChips';
import { CopilotExplain } from '@/components/CopilotExplain';
import { RootCauseRibbon } from '@/components/RootCauseRibbon';
import { tsLong } from '@/lib/utils';
import type { AnomalyEvent } from '@/lib/types';
import { serviceHref } from '@/lib/serviceHref';
import { anomalyDetailHref } from '@/lib/inboxHref';
import {
  ANOMALY_KIND_LABEL, anomalyChartWindow, anomalySignalHrefs, behaviorDetailsOf, isLogAnomalyKind,
} from './anomalyDetail';
import { AnomalyLogVolume, AnomalySample, BehaviorDetailsBox, SpikeFacts } from './anomalyDetailParts';

// AnomalyDetailDrawer — v0.8.267, operator-requested: "Anomalies
// sayfasında üzerine tıklayınca ne zaman spike oldu ve benzeri
// detay görmek iyi olurdu, problems gibi." Right-side slide-in
// mirroring the Problems TriageDrawer shell: spike timeline facts
// (started / last seen / duration / peak ×), the service's log
// volume around the spike, deploy chip, root-cause ribbon, AI
// explain, and the cross-signal deep links.
//
// ES-cost contract (operator: "log anomalies elastic backend
// kullanıldığında çok fazla sorgu yapmasın"): the ONLY backend
// fetch this drawer triggers is ONE bounded /api/logs/timeseries
// call, and only (a) when the drawer is actually open and (b) for
// log-shaped kinds. It rides the endpoint's existing 30s server
// cache; trace_op anomalies fetch nothing at all. Rows in the
// table never prefetch.
//
// v0.10.1032 (operatör: "Anomali ve alert rule'lara girdiğimde drawer
// çıkıyor. Exception gibi detay gözükmüyor.") — gövdenin parçaları (spike
// olguları, davranış kanıtı, ham örnek, log hacmi) ve saf kararları (tür
// etiketi, grafik penceresi, pivot linkleri) anomalyDetailParts.tsx /
// anomalyDetail.ts'e çıkarıldı: Problems kuyruğundaki TAM SAYFA anomali
// detayı (AnomalyEventDetail) aynı parçalardan çiziyor, iki ekran ayrışamaz.
// Çekmece yerinde kalıyor (/anomalies + servis Overview) ve bir bağlantı
// kazandı: "Tam detay →" (/inbox?anomaly=<id>).

export function AnomalyDetailDrawer({ event, onClose }: {
  event: AnomalyEvent;
  onClose: () => void;
}) {
  const isLogKind = isLogAnomalyKind(event.kind);

  // v0.9.936 — davranış olayının yapılandırılmış kanıtı. Memo: her
  // render'da JSON.parse etmek gereksiz, ve yeni bir nesne kimliği
  // aşağıdaki koşulu her seferinde yeniden değerlendirtirdi.
  const behaviorDetails = useMemo(
    () => behaviorDetailsOf({ kind: event.kind, sample: event.sample }),
    [event.kind, event.sample],
  );

  // Chart window: 3× the spike duration of lead-in (min 30 min) so
  // the baseline is visible left of the spike, plus a 10-minute
  // tail. Memoised — a fresh object each render would refire the
  // histogram fetch (v0.5.184 class). v0.10.1032 — formül
  // anomalyDetail.anomalyChartWindow'da (tam sayfa ile ortak).
  const win = useMemo(
    () => anomalyChartWindow({ startedAt: event.startedAt, lastSeen: event.lastSeen }),
    [event.startedAt, event.lastSeen],
  );

  // /logs + hatalı-trace pivotları spike penceresiyle (v0.9.213 / v0.9.1348
  // / v0.9.1381 dersleri üreticilerde: logsHref service= + floor/ceil
  // pencere, tracesPivotHref hasError → rootOnly kapalı). v0.10.1032 —
  // üreticiler anomalyDetail.anomalySignalHrefs'te, tam sayfa ile ortak.
  const hrefs = useMemo(
    () => anomalySignalHrefs({ kind: event.kind, service: event.service, pattern: event.pattern }, win),
    [event.kind, event.service, event.pattern, win],
  );

  // v0.8.499 (sadeleştirme #2, 5/5) — kabuk ui/Drawer'a taşındı:
  // overlay/Esc/✕ tek evden; başlık ve gövde (ES-cost sözleşmesi
  // dahil — histogram yalnız açıkken, tek 30s-cache'li çağrı) birebir.
  return (
    <Drawer onClose={onClose} width={560} header={
      <>
        {/* v0.10.929 (K5) — ACTIVE normal durum → nötr; CLEARED geçiş, yeşil kalır. */}
        <Badge tone={event.status === 'active' ? 'neutral' : 'success'} style={{ fontSize: 10 }}>
          {event.status === 'active' ? 'ACTIVE' : 'CLEARED'}
        </Badge>
        <span className="badge b-gray" style={{ fontSize: 10 }}>{ANOMALY_KIND_LABEL[event.kind]}</span>
        {event.service && (
          /* v0.9.860 (UX denetimi K1) — kardeş logs/traces linkleri (yukarıda)
             spike penceresini v0.9.213'ten beri taşırken servis linki
             taşımıyordu: aynı bileşende iki standart. */
          <Link to={serviceHref(event.service, { range: win })}
            style={{ fontWeight: 700, fontSize: 14 }}>
            {event.service}
          </Link>
        )}
        <ClusterChips clusters={event.clusters} />
      </>
    }>
        <div style={{ paddingTop: 10 }}>
          <div style={{
            fontWeight: 700, fontSize: 14, marginBottom: 10,
            overflowWrap: 'anywhere',
          }} title={event.pattern}>{event.pattern}</div>

          {/* Spike timeline — the "ne zaman spike oldu" answer. */}
          <SpikeFacts event={event} />

          {event.recentDeploy && (
            <div style={{
              fontSize: 12, padding: '8px 12px', marginBottom: 12,
              borderRadius: 6,
              background: 'color-mix(in srgb, var(--warn) 10%, transparent)',
              border: '1px solid color-mix(in srgb, var(--warn) 35%, transparent)',
            }}>
              ⬇ Deploy <b className="mono">{event.recentDeploy.version}</b> landed{' '}
              <b>{Math.max(1, Math.round(event.recentDeploy.ageSeconds / 60))}m before</b> the spike
              ({tsLong(event.recentDeploy.timeUnixNs)}) — likely-cause window ≤ 5m.
            </div>
          )}

          {/* v0.9.936 — davranış olayının kanıtı yapılandırılmış; ham
              JSON yerine okunur kutu. Ayrıştırılamazsa aşağıdaki <pre>
              devreye girer (şekil değişse bile kanıt kaybolmaz). */}
          {behaviorDetails && <BehaviorDetailsBox d={behaviorDetails} />}

          {event.sample && !behaviorDetails && <AnomalySample sample={event.sample} />}

          {/* Service log volume around the spike — mounted only while
              the drawer is open, one 30s-cached timeseries call, log
              kinds only (ES-cost contract in the header comment). */}
          {isLogKind && event.service && <AnomalyLogVolume service={event.service} win={win} />}

          {/* Root cause + AI — same affordances the row had, in situ. */}
          <div style={{ marginBottom: 12 }}>
            <RootCauseRibbon anchor="anomaly" id={event.id} summary={event.rootCause}
          window={win} />
          </div>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', flexWrap: 'wrap' }}>
            {/* v0.10.1032 — tam sayfa detay (Problems kuyruğunda yerinde açılır):
                kök neden açık, ilgili sinyaller, Seyir grafiği, Mute… ve
                "Gerçek problem / Problem değil" orada. */}
            <Link to={anomalyDetailHref(event.id)} className="sec"
              style={{ fontSize: 12, padding: '4px 10px', textDecoration: 'none' }}
              title="Bu anomalinin tam sayfa detayını aç (Problems)">
              Tam detay →
            </Link>
            {/* v0.9.477 — BİLEREK satır-içi kaldı (tek istisna). Bu yüzey
                zaten bir ui/Drawer; AI çekmecesi üstüne ikinci bir çekmece
                açardı ve iki kabuk da window'da Escape dinlediğinden tek
                ESC ikisini birden kapatırdı. Detay çekmecesinde açıklamanın
                yeri zaten burası. */}
            <CopilotExplain kind="anomaly" id={event.id} label="✨ Explain this anomaly" />
            {/* v0.10.6 — v0.9.1372'nin İKİZİ. O sürüm detay sayfalarının
                pivotlarını `.accent`e taşımıştı; bu çekmecedeki aynı türden
                pivot gözden kaçmıştı. */}
            {isLogKind && hrefs && (
              <Link to={hrefs.logs} className="accent"
                style={{ fontSize: 12, padding: '4px 10px', textDecoration: 'none' }}
                title="Open /logs scoped to the service + spike window">
                ≡ Logs in spike window ↗
              </Link>
            )}
            {/* v0.8.585 — Operator-reported: rootOnly default'u TRUE
                olduğundan hata izleri (çoğu non-root span) boş
                listeleniyordu; hasError linki root filtresini kapatır. */}
            {event.kind === 'trace_op' && hrefs && (
              <Link to={hrefs.errorTraces}
                className="sec"
                style={{ fontSize: 12, padding: '4px 10px', textDecoration: 'none' }}
                title="Open error traces for this service, scoped to the spike window">
                ⋮ Error traces ↗
              </Link>
            )}
          </div>
        </div>
    </Drawer>
  );
}
