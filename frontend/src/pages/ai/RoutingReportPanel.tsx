// RoutingReportPanel — v0.10.1464 (operatör, 2026-10-23: yanlış yönlendirmeler
// ekran görüntüsüyle bulunuyordu). Haftanın şüpheli CoSRE turları
// (GET /api/ai/routing-report): deterministik nedenler — doküman kademesinin
// "bilgi yok"u, serbest döngünün "veri bulunamadı"sı, guided'a oturmayan
// telemetri sorusu, hemen yeniden sorma, hata / boş cevap. LLM yok. 👎 yalnız
// ikincil kolon (puana girmez — operatör kuralı). Satırdaki "arşivi aç" turu
// sohbet arşivinde açar; gerçekten yanlış yönlendirmeyse vaka
// internal/api/chat_routing_regression_test.go'ya satır olur.
//
// Sunucu LIMIT 50: kayıt listesi, useDataTable; initialSort yok (sunucu sırası
// = puan ↓, zaman ↓). Kademe özeti ≤20 sabit satır → metin satırı, tablo değil.
// v0.10.2448 (operatör, 2026-11-22: "kimler yazmış göremiyorum") — "Kim" kolonu
// yalnız admin'de; sunucu editöre e-postayı hiç göndermez (ai_routing_report.go
// routingReportShowsUser). E-postasız eski tur "—".
import { useEffect, useState } from 'react';
import { Link } from 'react-router';
import { api } from '@/lib/api';
import { SegmentedControl } from '@/components/ui';
import { tsLong } from '@/lib/utils';
import { useAuthRole } from '@/components/AuthProvider';
import type { RoutingReport, RoutingReportItem } from '@/lib/types';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import { routingReasonLabel, routingReasonTitle, routingTierLabel, routingArchiveHref, routingSummary, routingScoreTone } from './routingReportView';

const COLS: ColumnDef<RoutingReportItem>[] = [
  { id: 'score',    label: 'Puan',     sortValue: r => r.score, numeric: true, width: 70, tone: r => routingScoreTone(r.score) },
  { id: 'question', label: 'Soru',     sortValue: r => r.question, naturalDir: 'asc', flex: true },
  { id: 'reasons',  label: 'Nedenler', sortValue: r => r.reasons.join(','), naturalDir: 'asc', width: 280 },
  { id: 'tier',     label: 'Kademe · rota', sortValue: r => routingTierLabel(r), naturalDir: 'asc', width: 200 },
  { id: 'who',      label: 'Kim',      sortValue: r => r.userEmail ?? '', naturalDir: 'asc', width: 170, tone: () => 'faint' },
  { id: 'service',  label: 'Servis',   sortValue: r => r.service ?? '', naturalDir: 'asc', width: 140, tone: () => 'faint' },
  { id: 'when',     label: 'Ne zaman', sortValue: r => r.createdAt, width: 165, tone: () => 'faint' },
  { id: 'vote',     label: '👎',       sortValue: r => (r.downvote ? 1 : 0), numeric: true, width: 56, tone: () => 'faint' },
  { id: 'archive',  label: 'Tur',      sortValue: r => r.conversationId ?? '', naturalDir: 'asc', width: 110 },
];
const COLS_NO_WHO = COLS.filter(c => c.id !== 'who');

export function RoutingReportPanel() {
  const isAdmin = useAuthRole() === 'admin';
  const [days, setDays] = useState<1 | 7 | 30>(7);
  const [data, setData] = useState<RoutingReport | null | undefined>(undefined);
  useEffect(() => {
    let cancelled = false;
    setData(undefined);
    api.aiRoutingReport(days)
      .then(r => { if (!cancelled) setData(r); })
      .catch(() => { if (!cancelled) setData(null); });
    return () => { cancelled = true; };
  }, [days]);
  const dt = useDataTable<RoutingReportItem>({ storageKey: 'ai-routing-report', columns: isAdmin ? COLS : COLS_NO_WHO, rows: data?.items ?? [] });
  const state: Omit<DataTableStateProps<RoutingReportItem>, 'dt'> =
    data === undefined ? { kind: 'loading' }
    : data === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Bu pencerede şüpheli yönlendirme yok.' };
  const topTiers = (data?.tiers ?? []).filter(t => t.suspicious > 0).slice(0, 5);
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>Şüpheli yönlendirmeler</h3>
        <span className="ov-sub">
          kayıtlı turlardan deterministik — doküman «bilgi yok», döngü «veri bulunamadı», guided'a oturmayan soru, hemen yeniden sorma (👎 yalnız bilgi)
        </span>
        <span style={{ marginLeft: 'auto' }}>
          <SegmentedControl size="sm" aria-label="Gün penceresi" value={String(days)}
            onChange={v => setDays(v === '30' ? 30 : v === '1' ? 1 : 7)}
            options={([1, 7, 30] as const).map(d => ({ value: String(d), label: `${d}g` }))} />
        </span>
      </div>
      <div className="ov-card-b">
        {data && (
          <div style={{ fontSize: 11.5, color: 'var(--text2)', marginBottom: 8 }}>
            {routingSummary(data)}
            {topTiers.length > 0 && <> · en çok: {topTiers.map(t => `${routingTierLabel(t)} (${t.suspicious}/${t.turns})`).join(', ')}</>}
          </div>
        )}
        <div className="table-wrap">
          <table {...dt.tableProps}>
            <DataTableColgroup dt={dt} />
            <DataTableHead dt={dt} />
            <tbody>
              {dt.sortedRows.length === 0 ? <DataTableState dt={dt} {...state} /> : dt.sortedRows.map(r => {
                const href = routingArchiveHref(r);
                return (
                  <tr key={r.exchangeId}>
                    <DataTableCell dt={dt} col="score" row={r} value={r.score} />
                    <DataTableCell dt={dt} col="question" row={r} value={r.question || null}
                      title={r.answer ? `Cevap: ${r.answer}` : undefined} />
                    <DataTableCell dt={dt} col="reasons" row={r}>
                      {r.reasons.map(k => (
                        <span key={k} className="badge b-err" style={{ marginRight: 4 }} title={routingReasonTitle(k)}>{routingReasonLabel(k)}</span>
                      ))}
                    </DataTableCell>
                    <DataTableCell dt={dt} col="tier" row={r} value={routingTierLabel(r)} />
                    {isAdmin && <DataTableCell dt={dt} col="who" row={r} value={r.userEmail || null} title={r.userEmail || undefined} />}
                    <DataTableCell dt={dt} col="service" row={r} value={r.service || null} />
                    <DataTableCell dt={dt} col="when" row={r} value={tsLong(Date.parse(r.createdAt) * 1e6)} className="mono" />
                    <DataTableCell dt={dt} col="vote" row={r} value={r.downvote ? '👎' : null} />
                    <DataTableCell dt={dt} col="archive" row={r}>
                      {href ? <Link to={href} title="Konuşma arşivi yalnız sahibine açılır">{r.turn ? `${r.turn}. tur · arşivi aç` : 'arşivi aç'}</Link> : '—'}
                    </DataTableCell>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
