import { lazy, Suspense, useMemo, type ReactNode } from 'react';
import { Link } from 'react-router';
import { Chip } from '@/components/ui/Chip';
import { DrawerSection } from '@/components/ui/Drawer';
import { Spinner } from '@/components/Spinner';
import { logsHref } from '@/lib/logsUrl';
import { fmtClock } from '@/lib/utils';
import type { LogSpikeBlockPayload } from '@/lib/types';
import { EvidenceTable } from './EvidenceChips';
import { logSpikeChart, logSpikeContributorTable, logSpikeDimNotes, logSpikeSummary } from './logSpikeView';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';

// v0.10.1284 — tembel: grafik (uPlot) yalnız log-spike kartı çizilince yüklenir;
// sohbetin öteki turları ve jsdom testleri uPlot'u hiç yüklemez.
const TimeChart = lazy(() => import('@/components/charts/TimeChart').then(m => ({ default: m.TimeChart })));

// LogSpikeCard — v0.10.1407 (Tek CoSRE adım 4; operatör: "Trace ve exception
// üzerinden çalışan Ask CoSRE açıklamaları güzel ama sanki ayrı drawer hissi,
// CoSRE bütünselliğine uymuyor."). Logs histogramının ✨ "bu log artışını ne
// açıklıyor"u artık CoSRE sohbetinin bir turu; bu kart o turun gövdesi (künye
// renderer 'log-spike', ChatBubble dağıtır). v0.10.1284 çekmece gövdesinin
// (LogSpikeExplainBody, özne kipiyle silindi) SUNUMU — veri çekmez: KANIT
// (grafik + taban/artış bölgeleri, katkı tablosu, boyut notları) cevabın
// `log_spike` bloğundan, ANLATIM `children` (balonun ExplainBody'si). Model
// düşse de kanıt doğru kalır.
export function LogSpikeCard({ p, children }: { p: LogSpikeBlockPayload; children?: ReactNode }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const res = p.spike;
  const chart = useMemo(() => logSpikeChart(res), [res]);
  const table = useMemo(() => logSpikeContributorTable(res), [res]);
  const notes = useMemo(() => logSpikeDimNotes(res), [res]);
  const summary = logSpikeSummary(res, fmtClock);
  const sp = res.spike;
  const q = p.q ?? '';

  return (
    <div className="ls-explain" data-log-spike-card="">
      <div className="ls-explain__meta">
        <Chip pill>{p.service || 'tüm servisler'}</Chip>
        <Chip pill>{`${fmtClock(p.fromNs / 1e6)} – ${fmtClock(p.toNs / 1e6)}`}</Chip>
        <Chip pill>{p.mode === 'sel' ? 'seçili aralık' : 'otomatik tespit'}</Chip>
        {p.env && <Chip pill>{p.env}</Chip>}
        {p.level === 'error' && <Chip pill>yalnız ERROR</Chip>}
        {q && <Chip pill title={q}>{q.length > 40 ? `${q.slice(0, 40)}…` : q}</Chip>}
      </div>

      <DrawerSection title="Artış">
        {summary && <div className="ls-explain__summary">{summary}</div>}
        {res.message && <div className="field-hint">{res.message}</div>}
        {chart.times.length >= 2
          ? (
            <Suspense fallback={<Spinner />}>
              <TimeChart times={chart.times} series={chart.series} regions={chart.regions} height={130} hideLegend leftInteger />
            </Suspense>
          )
          : <div className="field-hint">grafik için veri yok</div>}
      </DrawerSection>

      <DrawerSection title="Kaynak">
        {table
          ? <EvidenceTable p={table} />
          : <div className="field-hint">{sp ? 'artış tek bir boyutta yoğunlaşmıyor (eşik: artışın ≥%10’u, ≥20 satır)' : 'karşılaştırılacak artış yok'}</div>}
        {notes.length > 0 && (
          <ul className="ls-explain__notes">
            {notes.map(n => <li key={n} className="field-hint">{n}</li>)}
          </ul>
        )}
        {(res.warnings ?? []).map(w => <div key={w} className="field-hint ls-explain__warn">⚠ {w}</div>)}
      </DrawerSection>

      <DrawerSection title="Anlatım">{children}</DrawerSection>

      {sp && (
        <DrawerSection title="Sonraki adım">
          <Link className="ls-explain__cta" {...linkProps} to={logsHref({
            window: { fromNs: sp.fromMs * 1e6, toNs: sp.toMs * 1e6 }, service: p.service || undefined,
            env: p.env || undefined, q: q || undefined, severity: p.level === 'error' ? 17 : undefined,
          })}>Artış penceresinin loglarını aç →</Link>
        </DrawerSection>
      )}
    </div>
  );
}
