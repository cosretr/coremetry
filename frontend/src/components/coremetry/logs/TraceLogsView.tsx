// coremetry/logs/TraceLogsView — v0.10.1889 (Log Search Faz 3 dilim 3.6; audit
// §4 "Birleşik görünüm", §6 "Trace + log"; mockup görünüm 3). /logs'un
// `panel=trace` görünümü: trace şelalesi + o trace'in logları span satırının
// altında (`TraceWaterfall` `logRows`), span_id'siz / eşleşmeyen loglar ayrı
// grup ("Span'e bağlanamayan loglar").
//
// Veri TEK istek: GET /api/traces/{id}/bundle?hint=<log zamanı> (span + log
// + Oracle bacakları; log penceresi sunucuda span'lerden ±1 dk, ES 3 sn pivot
// bütçesi). İstek yalnız operatör görünümü açınca (çip menüsü / t / detay
// çekmecesi / yapıştırılan trace_id) gider; sayfa açılışına yeni ES isteği
// eklenmez. Trace bulunamazsa (örneklenmiş / henüz yazılmadı) sunucu log
// bacağını HİÇ koşmaz (pencere span'lerden kurulur) → ES isteği yok.
//
// Mockup sapmaları: "N servis" sayısı (MV'de yok — 3.9) ve kapsam genişletme +
// "kapsam dışı" etiketi (3.7) yok.
//
// Log Search Faz 6 dilim 6.5 — "Bu trace’te ne oldu?": MEVCUT trace açıklaması
// (AIExplainButton, `trace` öznesi → CoSRE sohbet turu; sunucu
// explain_trace_input.go span'leri ve trace'in loglarını okur). Yeni uç ve
// yeni ES yolu yok; düğme yalnız tıkla çalışır, copilot kapalıyken çizilmez.
import { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { Button } from '@/components/shadcn/button';
import { Toggle } from '@/components/shadcn/toggle';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { CopyButton } from '@/components/CopyButton';
import { AIExplainButton } from '@/components/ai/AIExplainButton';
import { IconSparkles } from '@/components/icons';
import { Spinner, Empty } from '@/components/Spinner';
import { TraceWaterfall } from '@/components/TraceWaterfall';
import { LogTable } from '@/components/LogTable';
import { useTraceBundle } from '@/lib/queries';
import { groupTraceLogs, spansWithLogsAndAncestors } from '@/lib/traceLogRows';
import { traceHref } from '@/lib/traceHref';
import { fmtClock } from '@/lib/utils';
import { fmtTraceDuration } from './TraceChip';
import type { LogRow, SpanRow } from '@/lib/types';

export interface TraceLogsViewProps {
  traceId: string;
  /** log zamanı ipucu (unix sn; 0 = yok). */
  hintSec: number;
  /** "Bu trace'e filtrele" — Logs görünümüne `traceId=` süzgeciyle döner. */
  onFilter(traceId: string): void;
  /** ✕ — birleşik görünümü kapatır (Logs görünümü). */
  onClose(): void;
}

const NO_ROWS: LogRow[] = [];

function rootOf(spans: SpanRow[]): SpanRow | undefined {
  const ids = new Set(spans.map(s => s.spanId));
  let root: SpanRow | undefined;
  for (const s of spans) {
    if (s.parentSpanId && ids.has(s.parentSpanId)) continue;
    if (!root || s.startTime < root.startTime) root = s;
  }
  return root;
}

export function TraceLogsView({ traceId, hintSec, onFilter, onClose }: TraceLogsViewProps) {
  const q = useTraceBundle(traceId, { hint: hintSec });
  const [selected, setSelected] = useState<string | null>(null);
  const [logSpansOnly, setLogSpansOnly] = useState(true);
  const [errorsOnly, setErrorsOnly] = useState(false);
  const data = q.data;
  const spans = useMemo(() => data?.spans ?? [], [data]);
  const logs = useMemo(() => {
    const all = data?.logs?.logs ?? NO_ROWS;
    return errorsOnly ? all.filter(l => l.severity >= 17) : all;
  }, [data, errorsOnly]);
  const groups = useMemo(() => groupTraceLogs(logs, spans), [logs, spans]);
  const focusIds = useMemo(
    () => (logSpansOnly && groups.bySpan.size > 0 ? spansWithLogsAndAncestors(spans, new Set(groups.bySpan.keys())) : undefined),
    [logSpansOnly, groups, spans]);
  const root = useMemo(() => rootOf(spans), [spans]);
  const summary = useMemo(() => {
    if (spans.length === 0) return null;
    let lo = Infinity, hi = -Infinity, errs = 0;
    for (const s of spans) {
      lo = Math.min(lo, s.startTime); hi = Math.max(hi, s.endTime);
      if (s.statusCode === 'error') errs++;
    }
    return { durMs: (hi - lo) / 1e6, errs, startNs: lo };
  }, [spans]);

  const head = (
    <div className="ld-trace-head">
      <span className="ld-trace-pill" data-testid="logs-trace-pill">
        <span className="ld-trace-tag">trace</span>
        <span className="ld-trace-id" title={traceId}>{traceId}</span>
        <CopyButton value={traceId} title="trace_id kopyala" />
        <Button variant="ghost" size="xs" aria-label="Birleşik görünümü kapat" onClick={onClose}>✕</Button>
      </span>
      <span className="flex-1" />
      <AIExplainButton subject={{ kind: 'trace', id: traceId }} size="sm"
        title="CoSRE bu trace’in hikâyesini anlatır: span’ler, hatalar ve trace’in logları (sohbette açılır)"
        label={<><IconSparkles /> <span>Bu trace’te ne oldu?</span></>} />
      <Button variant="outline" size="sm" onClick={() => onFilter(traceId)}>Bu trace'e filtrele</Button>
      <Button variant="outline" size="sm" asChild>
        <Link to={traceHref(traceId)}>Trace sayfasında aç ↗</Link>
      </Button>
    </div>
  );

  if (q.isPending) return <>{head}<Spinner label="Trace ve loglar yükleniyor…" /></>;
  if (q.isError) {
    return (
      <>{head}
        <Empty icon="⚠" title="Trace okunamadı"
          action={<Button variant="outline" size="sm" onClick={() => void q.refetch()}>Yeniden dene</Button>}>
          ClickHouse ya da log backend'i zamanında cevap vermedi — trace'in olmadığı anlamına gelmez.
        </Empty>
      </>
    );
  }
  if (!data || spans.length === 0) {
    return (
      <>{head}
        <Empty icon="○" title={data?.source === 'mv_only' ? 'Trace ham span olarak artık yok' : 'Trace ClickHouse’ta yok'}>
          {data?.source === 'mv_only'
            ? 'Bu trace yalnız özet olarak duruyor (ham span saklama süresi dolmuş); şelale çizilemez.'
            : 'Bu trace_id loglarda geçiyor ama trace yazılmamış: Collector örneklemesi dışarıda bırakmış ya da henüz gelmedi. Şelale çizilemez; logları görmek için "Bu trace\'e filtrele".'}
        </Empty>
      </>
    );
  }

  const logsSlot = data.logs;
  const total = data.logs?.logs?.length ?? 0;
  return (
    <>
      {head}
      <div className="card" data-testid="logs-trace-view">
        <div className="ld-trace-bar">
          <StatusBadge size="sm" status={summary && summary.errs > 0 ? 'critical' : 'ok'}>
            {summary && summary.errs > 0 ? '✕ hata var' : '✓ hata yok'}
          </StatusBadge>
          <b>{spans.length.toLocaleString('tr-TR')} span · {fmtTraceDuration(summary?.durMs ?? 0)}</b>
          {root && (
            <span className="text-(--text2)">kök <span className="font-mono">{root.name}</span> · {fmtClock((summary?.startNs ?? 0) / 1e6)}</span>
          )}
          <span className="flex-1" />
          <Toggle variant="outline" size="sm" pressed={logSpansOnly} onPressedChange={setLogSpansOnly} className="px-3"
            title="Logu olan span'ler ve ataları vurgulu, diğerleri soluk">Yalnız loglu span'ler</Toggle>
          <Toggle variant="outline" size="sm" pressed={errorsOnly} onPressedChange={setErrorsOnly} className="px-3"
            title="Yalnız ERROR ve üstü loglar">Yalnız hatalar</Toggle>
        </div>
        <p className="ld-trace-note" data-testid="logs-trace-note">
          {logsSlot?.degraded
            ? `Log backend'i yanıt veremedi (${logsSlot.reason || 'yavaş / erişilemez'}) — şelale tam, loglar eksik.`
            : `${total.toLocaleString('tr-TR')} log · span'e bağlı: ${groups.linked.toLocaleString('tr-TR')}`
              + (data.truncated?.logs ? ` · ilk ${total} log gösteriliyor (sınır)` : '')
              + (errorsOnly ? ' · yalnız hatalar' : '')}
        </p>
        <TraceWaterfall spans={spans} selectedId={selected} onSelect={id => setSelected(s => (s === id ? null : id))}
          analysis={data.analysis} focusIds={focusIds} logRows={groups.bySpan} />
        {groups.orphans.length > 0 && (
          <section className="ld-trace-orphans" aria-label="Span'e bağlanamayan loglar">
            <h4>Span'e bağlanamayan loglar ({groups.orphans.length.toLocaleString('tr-TR')}) <span className="text-(--text3)">span_id yok — zaman sırasıyla</span></h4>
            <LogTable logs={groups.orphans} hideTraceColumn />
          </section>
        )}
      </div>
    </>
  );
}
