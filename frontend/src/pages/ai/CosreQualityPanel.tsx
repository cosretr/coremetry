// CosreQualityPanel — CoSRE öğrenme Faz 2: haftanın en kötü 20 cevabı ve en
// kötü tool'ları (gece puanı, GET /api/ai/quality). Kullanıcı 👍/👎'si seyrek;
// puan çoğunlukla örtük sinyallerden (yeniden sorma, iptal, kanıtsız cevap,
// tool hatası / kopya, kanıt açma, probleme not, problem çözülmesi) gelir.
// Sunucu LIMIT 20: kayıt listesi, useDataTable; initialSort yok (sunucu sırası).
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { Button } from '@/components/ui';
import { tsLong, fmtNum } from '@/lib/utils';
import type { CosrePrefixStats, CosreWorstAnswer, CosreWorstTool } from '@/lib/types';
import { useDataTable, DataTableHead, DataTableColgroup, DataTableCell, DataTableState, type ColumnDef, type DataTableStateProps } from '@/components/ui/DataTable';
import { reasonLabel, reasonIsPositive, pct, scoreTone, prefixStatsLine } from './cosreQualityView';

const ANSWER_COLS: ColumnDef<CosreWorstAnswer>[] = [
  { id: 'score',    label: 'Puan',     sortValue: r => r.score, numeric: true, width: 70, tone: r => scoreTone(r.score) },
  { id: 'question', label: 'Soru',     sortValue: r => r.question, naturalDir: 'asc', flex: true },
  { id: 'reasons',  label: 'Sinyaller', sortValue: r => r.reasons.join(','), naturalDir: 'asc', width: 260 },
  { id: 'tier',     label: 'Kademe',   sortValue: r => r.tier, naturalDir: 'asc', width: 90, tone: () => 'faint' },
  { id: 'tools',    label: "Tool'lar", sortValue: r => r.tools.length, numeric: true, width: 80 },
  { id: 'when',     label: 'Ne zaman', sortValue: r => r.createdAt, width: 165, tone: () => 'faint' },
  { id: 'who',      label: 'Kim',      sortValue: r => r.userEmail ?? '', naturalDir: 'asc', width: 175, tone: () => 'faint' },
];

const TOOL_COLS: ColumnDef<CosreWorstTool>[] = [
  { id: 'tool',     label: 'Tool',            sortValue: r => r.tool, naturalDir: 'asc', flex: true, mono: true },
  { id: 'score',    label: 'Puan',            sortValue: r => r.score, numeric: true, width: 80, tone: r => scoreTone(r.score) },
  { id: 'calls',    label: 'Çağrı',           sortValue: r => r.calls, numeric: true, width: 80 },
  { id: 'success',  label: 'Başarı',          sortValue: r => r.successRate, numeric: true, width: 80, tone: r => (r.successRate < 0.9 ? 'warn' : undefined) },
  { id: 'rephrase', label: 'Sonra yeniden soruldu', sortValue: r => r.rephraseRate, numeric: true, width: 170, tone: r => (r.rephraseRate > 0.3 ? 'warn' : undefined) },
  { id: 'avg',      label: 'Ort. ms',         sortValue: r => r.avgMs, numeric: true, width: 90 },
  { id: 'p95',      label: 'p95 ms',          sortValue: r => r.p95Ms, numeric: true, width: 90 },
];

export function CosreQualityPanel() {
  const [answers, setAnswers] = useState<CosreWorstAnswer[] | null | undefined>(undefined);
  const [tools, setTools] = useState<CosreWorstTool[] | null | undefined>(undefined);
  const [prefix, setPrefix] = useState<CosrePrefixStats | undefined>(undefined); // v0.10.1637
  useEffect(() => {
    let cancelled = false;
    api.cosreQuality(7)
      .then(r => { if (!cancelled) { setAnswers(r.answers); setTools(r.tools); setPrefix(r.prefix); } })
      .catch(() => { if (!cancelled) { setAnswers(null); setTools(null); } });
    return () => { cancelled = true; };
  }, []);
  const aDt = useDataTable<CosreWorstAnswer>({ storageKey: 'ai-cosre-worst-answers', columns: ANSWER_COLS, rows: answers ?? [] });
  const tDt = useDataTable<CosreWorstTool>({ storageKey: 'ai-cosre-worst-tools', columns: TOOL_COLS, rows: tools ?? [] });
  const aState: Omit<DataTableStateProps<CosreWorstAnswer>, 'dt'> =
    answers === undefined ? { kind: 'loading' }
    : answers === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Henüz puanlanmış cevap yok (gece işi, gün kapandıktan ~2 saat sonra)' };
  const tState: Omit<DataTableStateProps<CosreWorstTool>, 'dt'> =
    tools === undefined ? { kind: 'loading' }
    : tools === null ? { kind: 'error' }
    : { kind: 'empty', message: "Son 7 günde ≥5 çağrılı tool yok" };
  return (
    <div className="card" style={{ marginTop: 16 }}>
      <div className="ov-card-h">
        <h3>En kötü cevaplar</h3>
        <span className="ov-sub">son 7 gün — örtük sinyallerden gece puanı (👍/👎 yalnız ek ağırlık, "dayanaksız iddia" gece hakeminden)</span>
        {answers && answers.length > 0 && (
          <Button variant="secondary" size="sm" style={{ marginLeft: 'auto' }}
            onClick={() => { api.cosreQualityEvalset(7).catch(() => { /* hata: indirme olmaz, sayfa bozulmaz */ }); }}
            title="Bu cevapları coremetry.evalset/1 regresyon vakası olarak indir (soru, beklenen tool'lar, anahtar olgular — repoya almadan önce gözden geçir)">
            ↓ Evalset (JSON)
          </Button>
        )}
      </div>
      <div className="ov-card-b">
        <div className="table-wrap">
          <table {...aDt.tableProps}>
            <DataTableColgroup dt={aDt} />
            <DataTableHead dt={aDt} />
            <tbody>
              {aDt.sortedRows.length === 0 ? <DataTableState dt={aDt} {...aState} /> : aDt.sortedRows.map(r => (
                <tr key={r.exchangeId}>
                  <DataTableCell dt={aDt} col="score" row={r} value={pct(r.score)} />
                  <DataTableCell dt={aDt} col="question" row={r} value={r.question || null}
                    title={r.answer ? `Cevap: ${r.answer}` : undefined} />
                  <DataTableCell dt={aDt} col="reasons" row={r}>
                    {r.reasons.length === 0 ? '—' : r.reasons.map(k => (
                      <span key={k} className={`badge ${reasonIsPositive(k) ? 'b-ok' : 'b-err'}`} style={{ marginRight: 4 }}>{reasonLabel(k)}</span>
                    ))}
                  </DataTableCell>
                  <DataTableCell dt={aDt} col="tier" row={r} value={r.tier || null} />
                  <DataTableCell dt={aDt} col="tools" row={r} value={fmtNum(r.tools.length)}
                    title={r.tools.length ? r.tools.join(', ') : undefined} />
                  <DataTableCell dt={aDt} col="when" row={r} value={tsLong(Date.parse(r.createdAt) * 1e6)} className="mono" />
                  <DataTableCell dt={aDt} col="who" row={r} value={r.userEmail || null} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
      {tools !== undefined && tools !== null && ( /* v0.10.1637 — tek satır; yükleme/hata tablo durumlarında */
        <div className="ov-card-b ov-sub"
          title="Trace açıklamasında en büyük paylı kritik yol adımı dış bağımlılık beklemesiyse sunucu cevabın ilk satırını kanıttan kurar; model yazmadıysa eklenir, yanlış değerle yazdıysa düzeltilir. Oran payda = kuralın uygulandığı üretilmiş açıklamalar (önbellek isabetleri sayılmaz).">
          {prefixStatsLine(prefix)}
        </div>
      )}
      <div className="ov-card-h" style={{ marginTop: 12 }}>
        <h3>En kötü tool'lar</h3>
        <span className="ov-sub">puan = başarı × (1 − sonrasında yeniden sorma oranı)</span>
      </div>
      <div className="ov-card-b">
        <div className="table-wrap">
          <table {...tDt.tableProps}>
            <DataTableColgroup dt={tDt} />
            <DataTableHead dt={tDt} />
            <tbody>
              {tDt.sortedRows.length === 0 ? <DataTableState dt={tDt} {...tState} /> : tDt.sortedRows.map(r => (
                <tr key={r.tool}>
                  <DataTableCell dt={tDt} col="tool" row={r} value={r.tool} />
                  <DataTableCell dt={tDt} col="score" row={r} value={pct(r.score)} />
                  <DataTableCell dt={tDt} col="calls" row={r} value={fmtNum(r.calls)} />
                  <DataTableCell dt={tDt} col="success" row={r} value={pct(r.successRate)} />
                  <DataTableCell dt={tDt} col="rephrase" row={r} value={pct(r.rephraseRate)} />
                  <DataTableCell dt={tDt} col="avg" row={r} value={fmtNum(Math.round(r.avgMs))} />
                  <DataTableCell dt={tDt} col="p95" row={r} value={fmtNum(r.p95Ms)} />
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}
