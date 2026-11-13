// TraceLogTable — v0.10.1940 (operatör: "yeni log yapısı trace log sayfasında da
// geçerli olsun" → "Yap"). Trace sayfasının (ve kiosk'un) log sekmesi Log
// Search'ün tarayıcı-yanı parçalarını alır; AÇILIŞA yeni ES isteği EKLENMEZ:
//   - exception rozeti + katlı stack (LogTable `exceptions`, dilim 3.3),
//   - satır tıklaması eskisi gibi tablonun İÇİNDE açılır (v0.10.1941, operatör:
//     "çekmece yandan açılmasın, eski şekilde inline olsun" — yan çekmece YOK),
//   - görünüm seçenekleri (satır yüksekliği / yoğunluk / sayfa başına satır):
//     Logs sayfasıyla AYNI kullanıcı tercihi (useDisplayPref('logs')).
// Histogram, desenler ve alan istatistikleri bilinçli YOK (tek trace için anlamsız).
import { useMemo, useState, type ReactNode } from 'react';
import { LogTable } from '@/components/LogTable';
import { Pager } from '@/components/Pager';
import { DisplayOptions } from '@/components/coremetry/logs/DisplayOptions';
import { useDisplayPref } from '@/lib/queries/prefs';
import { LOGS_DEFAULT_DISPLAY, pageSlice } from '@/lib/logsDisplay';
import { mergeBase, prefToView, viewToPref } from '@/lib/logsDisplayPrefs';
import { LOGS_SAMPLE_DEFAULT } from '@/lib/logsSample';
import type { LogRow, LogsDisplay } from '@/lib/types';

// v0.10.2094 (operatör, prod trace log sekmesi: "Cluster ile servis yer
// değiştirsin") — Time · Service · Message · Cluster · Pod. Yalnız trace log
// sekmesi ve kiosk; /logs varsayılanı (DEFAULT_LOG_COLUMNS) değişmedi.
export const TRACE_LOG_COLUMNS = ['service', 'message', 'cluster', 'pod'];

export function TraceLogTable({ rows, state, head }: {
  /** Kronolojik satırlar (log + span event + Oracle). */
  rows: LogRow[];
  state?: Parameters<typeof LogTable>[0]['state'];
  /** Üst satırın sol tarafı (sayaç, çipler); Görünüm ▾ sağda. */
  head?: ReactNode;
}) {
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
  return (
    <>
      {/* pages/trace Tailwind @source dışında: satır içi stil (panelin eski üst satırıyla aynı ölçüler). */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', padding: '6px 10px', fontSize: 11, color: 'var(--text3)' }}>
        <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', flex: 1, minWidth: 0 }}>{head}</div>
        <DisplayOptions value={display} onChange={setDisplay} />
      </div>
      <LogTable logs={paged.rows} columns={TRACE_LOG_COLUMNS} hideTraceColumn state={state} exceptions display={display} />
      {rows.length > display.pageSize && (
        <Pager mode="offset" count="exact" total={rows.length} page={paged.page} pageSize={display.pageSize}
          hasMore={paged.page < paged.pages - 1} lastReachablePage={paged.pages - 1} onPage={setPage} />
      )}
    </>
  );
}
