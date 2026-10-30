import type { CSSProperties, ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Chip } from '@/components/ui/Chip';
import { DrawerSection } from '@/components/ui/Drawer';
import { KeyValue, KeyValueRow } from '@/components/coremetry/KeyValue';
import { CHART_SCOPES, chartScopeLabel, type ChartScope } from '@/lib/aiSubject';
import { operationTracesHref, tracesPivotHref } from '@/lib/pivotHref';
import { fmtClock } from '@/lib/utils';
import { logsHref } from '@/lib/logsUrl';
import type { ChartsSignalsBlockPayload, OpDelta } from '@/lib/types';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';

// ChartsSignalsCard — v0.10.1407 (Tek CoSRE adım 4; operatör: "Trace ve
// exception üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı
// drawer hissi, CoSRE bütünselliğine uymuyor."). Service → Details grafik
// ✨'ının cevabı artık CoSRE sohbetinin bir turu; bu kart o turun gövdesi
// (künye renderer 'charts', ChatBubble dağıtır). v0.9.1033 çekmece gövdesinin
// (ServiceChartsExplainBody, özne kipiyle silindi) SUNUMU — veri çekmez:
// ölçülmüş sinyaller cevabın `charts_signals` bloğundan, anlatım `children`
// (balonun ExplainBody'si). Üç bölüm aynen: Ne oldu · İlişkili sinyaller ·
// Sonraki adım. Kanıt anlatımdan BAĞIMSIZ: tablo ve pivot linkleri bloktan
// kurulur (link uydurulamaz), model düşse de doğru kalır.
export function ChartsSignalsCard({ p, children }: { p: ChartsSignalsBlockPayload; children?: ReactNode }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const sg = p.signals;
  const scope: ChartScope = (CHART_SCOPES as readonly string[]).includes(p.scope) ? (p.scope as ChartScope) : 'all';
  const hasSignals = !!sg.deploy || !!sg.problems?.length
    || !!sg.anomalies?.length || !!sg.opDeltas?.length || sg.otherOps > 0;
  // İlk (en kötü) operasyon "Sonraki adım"ın hedefi: hatalı izleri
  // operasyona daraltmak, servis geneline bakmaktan çok daha hızlı.
  const topOp = sg.opDeltas?.[0]?.name;
  const { service, fromNs, toNs } = p;

  return (
    <div data-charts-card="">
      {/* Meta şeridi — hangi servis, hangi pencere, hangi kapsam. */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8 }}>
        <Chip pill>{service}</Chip>
        <Chip pill>{`${fmtClock(fromNs / 1e6)} – ${fmtClock(toNs / 1e6)}`}</Chip>
        <Chip pill>{chartScopeLabel(scope)}</Chip>
      </div>

      <DrawerSection title="Ne oldu">{children}</DrawerSection>

      {hasSignals && (
        <DrawerSection title="İlişkili sinyaller">
          {/* v0.10.973 — tablo standardı T1: etiket → değer satırları bir
              öznitelik paneli → KeyValue (<dl>). */}
          <KeyValue>
            {sg.deploy && (
              <KeyValueRow k={sg.deploy.kind === 'deploy' ? 'deploy' : 'rollout'} v={<>
                {fmtClock(sg.deploy.timeUnixNs / 1e6)}
                {sg.deploy.versionAfter
                  ? ` · ${sg.deploy.versionBefore || '?'} → ${sg.deploy.versionAfter}`
                  : ''}
                {` · ${sg.deploy.podsReplaced} pod`}
              </>} />
            )}
            {sg.problems?.map(pr => (
              <KeyValueRow key={pr.id} k="problem" v={<>
                <Link style={linkStyle} to={`/inbox?problem=${encodeURIComponent(pr.id)}`} {...linkProps}>
                  {pr.priority || pr.severity.toUpperCase()} · {pr.title}
                </Link>
                <span style={{ color: 'var(--text3)' }}>
                  {' '}— {pr.metric} {pr.value.toFixed(2)} / eşik {pr.threshold.toFixed(2)}
                </span>
              </>} />
            ))}
            {sg.anomalies?.map(a => (
              <KeyValueRow key={a.id} k="anomali" v={<>
                {a.pattern}{' '}
                <span className="mono" style={{ color: 'var(--err)' }}>
                  {a.peakRatio.toFixed(1)}×
                </span>
                <span style={{ color: 'var(--text3)' }}> · {a.kind} · {a.status}</span>
              </>} />
            ))}
            {(sg.opDeltas?.length || sg.otherOps > 0) && (
              <KeyValueRow k="operasyon" v={<>
                {sg.opDeltas?.map(d => <OpDeltaRow key={d.name} d={d} />)}
                {sg.otherOps > 0 && (
                  <div style={{ color: 'var(--text3)' }}>
                    diğer {sg.otherOps} operasyon: kayda değer değişim yok
                  </div>
                )}
              </>} />
            )}
          </KeyValue>
        </DrawerSection>
      )}

      <DrawerSection title="Sonraki adım">
        <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
          {/* Pivot linkleri pencereyi HER ZAMAN taşır (pivotHref'in var
              oluş sebebi): penceresiz bir pivot boş liste döndürür ve bu
              "böyle bir trace yok" gibi okunur. */}
          <CtaLink linkProps={linkProps}
            to={topOp
              ? operationTracesHref({
                  window: { fromNs, toNs }, operation: topOp,
                  service, hasError: true,
                })
              : tracesPivotHref({
                  window: { fromNs, toNs }, service, hasError: true,
                  view: 'list', rootOnly: false,
                })}
            title="Hatalı izleri aç"
            note={`/traces · ${topOp ?? 'servis geneli'} · hasError`} />
          <CtaLink linkProps={linkProps}
            to={logsHref({ window: { fromNs, toNs }, service, severity: 13 })}
            title="Logları aç"
            note="/logs · ≥warn · aynı pencere" />
          {sg.problems?.[0] && (
            <CtaLink linkProps={linkProps}
              to={`/inbox?problem=${encodeURIComponent(sg.problems[0].id)}`}
              title="Problemi aç"
              note={`/inbox · ${sg.problems[0].title}`} />
          )}
        </div>
      </DrawerSection>
    </div>
  );
}

// Link görsel dili: ev konvansiyonu inline accent rengi (DBQueriesPanel emsali).
const linkStyle: CSSProperties = {
  color: 'var(--accent2)', textDecoration: 'none',
};

// OpDeltaRow — p95Ratio === 0 "ölçülemedi" demektir, "0×" DEĞİL
// (backend ratioText'i ile aynı kural; sessiz sıfır "gecikme sıfıra
// düştü" diye okunur).
function OpDeltaRow({ d }: { d: OpDelta }) {
  return (
    <div style={{ display: 'flex', gap: 6, alignItems: 'baseline', flexWrap: 'wrap' }}>
      <span style={{
        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
        maxWidth: '100%',
      }} title={d.name}>{d.name}</span>
      {d.isNew ? (
        <span className="badge b-info">yeni</span>
      ) : (
        <span className="mono" style={{ fontSize: 11, color: 'var(--err)' }}>
          {d.p95Ratio > 0 ? `p95 ${d.p95Ratio.toFixed(2)}×` : 'p95 ölçülemedi'}
          {d.errDeltaPp > 0 ? ` err +${d.errDeltaPp.toFixed(2)}pp` : ''}
        </span>
      )}
      <span style={{ fontSize: 11, color: 'var(--text3)' }}>{d.calls} çağrı</span>
    </div>
  );
}

function CtaLink({ to, title, note, linkProps }: { to: string; title: string; note: string; linkProps: ReturnType<typeof chatLinkTargetProps> }) {
  return (
    <Link to={to} {...linkProps} style={{
      display: 'flex', justifyContent: 'space-between', gap: 8, alignItems: 'baseline',
      padding: '6px 9px', border: '1px solid var(--border)',
      borderRadius: 'var(--radius-sm)', background: 'var(--bg)',
      color: 'var(--text)', textDecoration: 'none', fontSize: 12,
    }}>
      <span>{title}</span>
      <span style={{
        color: 'var(--text3)', fontSize: 11, overflow: 'hidden',
        textOverflow: 'ellipsis', whiteSpace: 'nowrap',
      }} title={note}>{note}</span>
    </Link>
  );
}
