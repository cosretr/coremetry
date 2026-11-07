// TraceLogTable — v0.10.1940 (operatör: "yeni log yapısı trace log sayfasında da
// geçerli olsun" → "Yap"). Trace sayfasının (ve kiosk'un) log sekmesi Log
// Search'ün tarayıcı-yanı parçalarını alır; AÇILIŞA yeni ES isteği EKLENMEZ:
//   - exception rozeti + katlı stack (LogTable `exceptions`, dilim 3.3),
//   - satır → detay çekmecesi (LogDetailSheet; URL `logdoc`, Geri kapatır),
//     çevre loglar yalnız çekmecede İSTENİNCE (LogContextSection, dilim 3.2),
//   - görünüm seçenekleri (satır yüksekliği / yoğunluk / sayfa başına satır):
//     Logs sayfasıyla AYNI kullanıcı tercihi (useDisplayPref('logs')).
// Histogram, desenler ve alan istatistikleri bilinçli YOK (tek trace için anlamsız).
import { useMemo, useState, type ReactNode } from 'react';
import { useNavigate } from 'react-router-dom';
import { LogTable } from '@/components/LogTable';
import { Pager } from '@/components/Pager';
import { useSheetParam } from '@/components/coremetry/DetailSheet/useSheetParam';
import { DisplayOptions } from '@/components/coremetry/logs/DisplayOptions';
import { LogDetailSheet } from '@/components/coremetry/logs/LogDetailSheet';
import { LogContextSection } from '@/components/coremetry/logs/LogContextSection';
import { LogExceptionSection } from '@/components/coremetry/logs/StackBlock';
import { useDisplayPref } from '@/lib/queries/prefs';
import { LOGS_DEFAULT_DISPLAY, pageSlice } from '@/lib/logsDisplay';
import { mergeBase, prefToView, viewToPref } from '@/lib/logsDisplayPrefs';
import { LOGS_SAMPLE_DEFAULT } from '@/lib/logsSample';
import { neighborRow } from '@/lib/logDetail';
import { logsHref } from '@/lib/logsUrl';
import type { LogRow, LogsDisplay } from '@/lib/types';

const NO_EXPANDED = new Set<number>();

export function TraceLogTable({ rows, state, head }: {
  /** Kronolojik satırlar (log + span event + Oracle). */
  rows: LogRow[];
  state?: Parameters<typeof LogTable>[0]['state'];
  /** Üst satırın sol tarafı (sayaç, çipler); Görünüm ▾ sağda. */
  head?: ReactNode;
}) {
  const navigate = useNavigate();
  const pref = useDisplayPref('logs');
  const base = useMemo(() => mergeBase(LOGS_DEFAULT_DISPLAY, pref.pref), [pref.pref]);
  const [local, setLocal] = useState<Partial<LogsDisplay>>({});
  const display: LogsDisplay = { ...base, ...local };
  const [page, setPage] = useState(0);
  const setDisplay = (patch: Partial<LogsDisplay>) => {
    if (patch.pageSize !== undefined) setPage(0);
    setLocal(l => ({ ...l, ...patch }));
    pref.save(viewToPref({ ...display, ...patch }, prefToView(pref.pref).sample ?? LOGS_SAMPLE_DEFAULT));
  };
  const paged = pageSlice(rows, page, display.pageSize);
  const [docParam, setDocParam] = useSheetParam('logdoc');
  const docRow = docParam ? rows.find(r => String(r.id) === docParam) ?? null : null;
  const openRow = (r: LogRow) => setDocParam(String(r.id));
  return (
    <>
      {/* pages/trace Tailwind @source dışında: satır içi stil (panelin eski üst satırıyla aynı ölçüler). */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', padding: '6px 10px', fontSize: 11, color: 'var(--text3)' }}>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', flex: 1, minWidth: 0 }}>{head}</div>
        <DisplayOptions value={display} onChange={setDisplay} />
      </div>
      <LogTable logs={paged.rows} hideTraceColumn state={state} exceptions display={display}
        expandedIds={NO_EXPANDED}
        onToggleExpand={id => { const r = rows.find(x => x.id === id); if (r) openRow(r); }} />
      {rows.length > display.pageSize && (
        <Pager mode="offset" count="exact" total={rows.length} page={paged.page} pageSize={display.pageSize}
          hasMore={paged.page < paged.pages - 1} lastReachablePage={paged.pages - 1} onPage={setPage} />
      )}
      <LogDetailSheet row={docRow} onClose={() => setDocParam(null)} resizeKey="trace.logs.detailSheet.w"
        onPrev={() => { if (docRow) { const n = neighborRow(rows, docRow.id, -1); if (n) openRow(n); } }}
        onNext={() => { if (docRow) { const n = neighborRow(rows, docRow.id, 1); if (n) openRow(n); } }}
        exception={docRow?.exception ? <LogExceptionSection row={docRow} /> : undefined}
        context={docRow && !docRow.origin ? (
          <LogContextSection key={docRow.id} pivot={docRow} onOpenRow={openRow}
            onOpenWindow={(f, t, svc) => navigate(logsHref({ window: { fromNs: f, toNs: t + 1_000_000 }, service: svc }))} />
        ) : undefined} />
    </>
  );
}
