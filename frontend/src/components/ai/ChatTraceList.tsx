import { Link } from 'react-router';
import { traceHref } from '@/lib/traceHref';
import { tsShort } from '@/lib/utils';
import type { ChatTraceListPayload } from '@/lib/types';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget'; // v0.10.1125 — /cosre yeni sekme
import { RowExplainCell, RowExplainHead } from './RowExplainCell'; // v0.10.1291 — satır ✨ trace
import { shortTraceId, traceTableShape } from './chatTraceListView';

// ChatTraceList — v0.10.688 (endpoint_traces.go trace_list bloğu; D6 "trace
// listesi cevabı"): LLM anlatımı yok, deterministik tablo. Satır = trace
// linki (traceHref üreticisi), altta "Daha fazla → Traces" aynı süzgeçle
// (deepLink sunucudan). Markdown tablo dili (.cm-md-table) — ikinci tablo
// görünümü olmasın.
//
// v0.10.2213 (operatör onayı 2026-11-15) — sohbetin TEK trace tablosu:
// Başlangıç · Trace (kısa kimlik, link) · Süre · Durum; bütün satırlarda aynı
// olan Servis / İşlem kolonu çizilmez, ortak değer başlıkta bir kez
// (chatTraceListView.traceTableShape). Hücreler sarmaz (çekmece genişliğinde
// yatay kaydırma yok; uzun ad ellipsis + title). Span sayısı kolonu kalktı.
// Operasyon kartı da trace'lerini bu tabloyla çizer (opTraceListPayload).
export function ChatTraceList({ tl, more = true }: { tl: ChatTraceListPayload; more?: boolean }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  if (tl.traces.length === 0) return null;
  const shape = traceTableShape(tl.traces);
  const dur = (ms: number) => (ms >= 1000 ? (ms / 1000).toFixed(2) + ' s' : ms.toFixed(0) + ' ms');
  return (
    <div className="cm-trace-list" data-trace-table="">
      {shape.caption && <div className="cm-trace-list__cap mono" title={shape.caption}>{shape.caption}</div>}
      <table className="cm-md-table cm-trace-table">
        <thead>
          <tr>
            <th>Başlangıç</th><th>Trace</th>
            {shape.showService && <th>Servis</th>}
            {shape.showOp && <th>İşlem</th>}
            <th className="num">Süre</th><th>Durum</th><RowExplainHead />
          </tr>
        </thead>
        <tbody>
          {tl.traces.map(t => (
            <tr key={t.traceId}>
              <td className="mono">{t.startTime ? tsShort(t.startTime) : '—'}</td>
              <td><Link className="mono" to={traceHref(t.traceId)} title={t.traceId} {...linkProps}>{shortTraceId(t.traceId)}</Link></td>
              {shape.showService && <td title={t.serviceName}>{t.serviceName}</td>}
              {shape.showOp && <td title={t.rootName}>{t.rootName || '—'}</td>}
              <td className="num">{dur(t.durationMs)}</td>
              {/* v0.10.929 (K5) — sağlıklı trace nötr: yalnız ERROR rozeti; OK ekran okuyucuya (Traces emsali). */}
              <td>{t.hasError ? <span className="badge b-err">ERROR</span> : <span className="sr-only">OK</span>}</td>
              <RowExplainCell subject={{ kind: 'trace', id: t.traceId }} title="Bu trace'i AI ile açıkla" />
            </tr>
          ))}
        </tbody>
      </table>
      {more && (
        <div className="cm-trace-list__more">
          <Link to={tl.deepLink} {...linkProps}>Daha fazla → Traces</Link>
          {tl.truncated && <span className="cell-hint"> · ilk {tl.traces.length} satır</span>}
        </div>
      )}
    </div>
  );
}
