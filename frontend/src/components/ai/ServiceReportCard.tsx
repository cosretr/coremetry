// ServiceReportCard — v0.10.1337 (Feature D, operatör): guided servis raporunun
// yapısal kartı (internal/api/copilot_service_report.go serviceReportPayload).
// Hüküm satırı (EvidenceCard'ın .ev-verdict'i), en yavaş / en hatalı
// operasyonlar, teknik hata grupları (/problems?exc=…), kırılan trace'ler
// (traceHref) + o trace'lerin logları (mono), link çipleri. Rapor anlatımdan
// bağımsız: model ne yazarsa yazsın kanıt burada sunucu sırasıyla durur.
// Hrefler lib/chatBlocks.ts serviceReportBlocks'ta isSafeAppPath'ten geçti.
// v0.10.1415 (operatör: "… tam rapor çekerken CoSRE VictoriaMetrics'ten HTTP
// grafiklerini getirsin. Response time ve throughput.") — özet satırlarının
// hemen altında VictoriaMetrics HTTP grafikleri (throughput · yanıt süresi ·
// 5xx; kanıt çipinin AYNI çizimi EvidenceChart) + "Metrics'te aç" / PromQL
// kopyala; çizilmediyse sunucunun dürüst notu.
import { Link } from 'react-router';
import { CopyButton } from '@/components/CopyButton';
import type { ChatServiceReportOp, ChatServiceReportPayload } from '@/lib/types';
import { fmtNum } from '@/lib/utils';
import { traceHref } from '@/lib/traceHref';
import { fmtEvidenceTime, fmtRangeTR } from '@/lib/chatEvidence';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';
import { exceptionGroupRows, fmtMs } from './operationCardView';
import { drillLogsNote, verdictBadgeClass } from './serviceReportCardView';
import { EvidenceChart } from './EvidenceChips';
import { useChatDashOrigin } from './chatDash'; // v0.10.1443
import { serviceReportChartToPanel } from '@/lib/chatPanelSpec';

function priorLabel(o: ChatServiceReportOp): string {
  return o.ratio && o.ratio > 0 && o.priorP99Ms ? `${fmtMs(o.priorP99Ms)} · ×${o.ratio.toFixed(1)}` : 'kıyas yok';
}

export function ServiceReportCard({ rep }: { rep: ChatServiceReportPayload }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const slow = rep.ops.slow ?? [];
  const erroring = rep.ops.erroring ?? [];
  const exRows = exceptionGroupRows(rep.exceptions ?? []);
  const drills = rep.drills ?? [];
  const links = rep.links ?? [];
  const httpCharts = rep.httpCharts ?? [];
  const dashOrigin = useChatDashOrigin(); // v0.10.1443
  return (
    <section className="ev-card" aria-label={`Servis raporu · ${rep.service}`}>
      <div className="ev-head">
        Servis raporu · <span className="mono">{rep.service}</span>{rep.env ? ` · ${rep.env}` : ''} · {fmtRangeTR(rep.rangeS)} · teknik hatalar son 3 sa
      </div>
      <div className="ev-verdict">
        <span className={verdictBadgeClass(rep.verdict)}>{rep.verdict}</span>{' '}
        {(rep.reasons ?? []).join(' · ')}
      </div>
      {rep.red && (rep.red.unavailable
        ? <div className="ev-empty">Servis RED okunamadı ({rep.red.unavailable})</div>
        : <div className="ev-foot">
            <span>span {fmtNum(rep.red.spans)}</span>
            <span>hata %{rep.red.errorRate.toFixed(2)} (önce %{rep.red.baseErrorRate.toFixed(2)})</span>
            <span>p99 {fmtMs(rep.red.p99Ms)} (önce {fmtMs(rep.red.baseP99Ms)})</span>
          </div>)}

      {/* v0.10.1415 — VictoriaMetrics HTTP grafikleri (örneklenmemiş), raporun en üstünde. */}
      {httpCharts.length > 0 ? (
        <>
          <div className="ev-sub">HTTP throughput · yanıt süresi — VictoriaMetrics (örneklenmemiş)</div>
          {httpCharts.map(c => (
            <div key={c.kind} data-http-chart={c.kind}>
              {/* v0.10.1443 — "Dashboard'a ekle": grafiğin PromQL'i satır-içi (kaynak VM). */}
              <EvidenceChart p={c} dashPanel={dashOrigin ? serviceReportChartToPanel(rep.service, c, dashOrigin) : null} />
              <div className="ev-foot">
                {c.deepLink && <Link to={c.deepLink} className="ev-link" {...linkProps}>Metrics'te aç ↗</Link>}
                {c.query && <CopyButton value={c.query} title="PromQL kopyala" />}
              </div>
            </div>
          ))}
        </>
      ) : rep.httpChartsNote ? <div className="ev-empty">HTTP grafikleri yok — {rep.httpChartsNote}</div> : null}

      <div className="ev-sub">En yavaş operasyonlar</div>
      {rep.ops.err ? <div className="ev-empty">Operasyonlar okunamadı ({rep.ops.err})</div>
        : slow.length === 0 ? <div className="ev-empty">≥20 çağrılı operasyon yok</div> : (
          // statik tablo (T1): ≤5 sabit satır, sunucu sırası (önceki pencereye kat), sıralanmaz, satır tıklanmaz.
          <table className="ev-table" aria-label="En yavaş operasyonlar">
            <thead><tr><th>Operasyon</th><th className="num">p99</th><th>Önceki</th><th className="num">Çağrı</th></tr></thead>
            <tbody>
              {slow.map(o => (
                <tr key={`s-${o.name}`}>
                  <td className="mono">{o.name}</td>
                  <td className={o.ratio && o.ratio >= 2 ? 'num cell-warn' : 'num'}>{fmtMs(o.p99Ms)}</td>
                  <td>{priorLabel(o)}</td>
                  <td className="num">{fmtNum(o.calls)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      {!rep.ops.err && erroring.length > 0 && (
        <>
          <div className="ev-sub">En çok hata alan operasyonlar</div>
          {/* statik tablo (T1): ≤5 sabit satır, sunucu sırası (hata sayısı), sıralanmaz, satır tıklanmaz. */}
          <table className="ev-table" aria-label="En çok hata alan operasyonlar">
            <thead><tr><th>Operasyon</th><th className="num">Hata</th><th className="num">Oran</th></tr></thead>
            <tbody>
              {erroring.map(o => (
                <tr key={`e-${o.name}`}>
                  <td className="mono">{o.name}</td>
                  <td className="num">{fmtNum(o.errors)}</td>
                  <td className={o.errorRate >= 5 ? 'num cell-err' : 'num'}>%{o.errorRate.toFixed(2)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}

      <div className="ev-sub">Teknik hatalar</div>
      {rep.exceptionsError ? <div className="ev-empty">Exceptions okunamadı — "teknik hata yok" demek değil</div>
        : exRows.length === 0 ? <div className="ev-empty">son 3 saatte teknik hata grubu yok</div> : (
          <ul className="ev-list">
            {exRows.map(r => (
              <li key={r.key} title={r.title}>
                <span className="badge b-err mono">{r.code}</span>{' '}
                {r.priority && <span className="cell-hint">{r.priority} </span>}
                <span className={r.burst ? 'cell-err' : undefined}>{r.count}</span>
                {r.lastSeen && <span className="cell-hint"> · {r.lastSeen}</span>}
                {r.href && <> · <Link to={r.href} className="ev-link" {...linkProps}>Exceptions →</Link></>}
              </li>
            ))}
          </ul>
        )}

      <div className="ev-sub">Trace + log kanıtı</div>
      {drills.length === 0 ? <div className="ev-empty">kırılan trace yok</div> : (
        <ul className="ev-list">
          {drills.map(d => {
            const note = drillLogsNote(d);
            return (
              <li key={d.traceId}>
                <Link to={traceHref(d.traceId)} className="ev-link mono" title={d.traceId} {...linkProps}>{d.traceId.slice(0, 16)}…</Link>
                {' '}<span className="cell-hint">{d.label}</span>
                <div>{d.text}</div>
                {(d.logs ?? []).length > 0 && (
                  <div className="mono" aria-label="trace logları">
                    {(d.logs ?? []).map((l, i) => (
                      <div key={i}>{fmtEvidenceTime(l.ts)} {l.severity} {l.service} {l.body}</div>
                    ))}
                  </div>
                )}
                {note && <div className="ev-empty">{note}</div>}
              </li>
            );
          })}
        </ul>
      )}

      {links.length > 0 && (
        <div className="ev-foot" role="group" aria-label="Rapor sayfaları">
          {links.map(l => <Link key={l.href} to={l.href} className="ev-link" {...linkProps}>{l.label} ↗</Link>)}
        </div>
      )}
    </section>
  );
}
