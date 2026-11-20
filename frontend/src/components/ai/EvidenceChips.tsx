import { lazy, Suspense, useMemo, useState } from 'react';
import { Link } from 'react-router';
import { Badge } from '@/components/ui/Badge';
import { Tooltip } from '@/components/ui/Tooltip';
import { DisclosureButton } from '@/components/ui/DisclosureButton';
import { CopyButton } from '@/components/CopyButton';
import { Spinner } from '@/components/Spinner';
import type { ChatQueryEvidence, ChatEvidenceChartPayload, ChatEvidenceTablePayload, Panel } from '@/lib/types';
import { chartBlockToPanel } from '@/lib/chatPanelSpec'; // v0.10.1443
import { AddToDashboardButton } from './AddToDashboard'; // v0.10.1443
import { useChatDashOrigin } from './chatDash'; // v0.10.1443
import type { EvidenceBlockView } from '@/lib/chatBlocks';
import { traceHref } from '@/lib/traceHref';
import { fmtClock, fmtDateTime } from '@/lib/utils';
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget';
import { ChatTraceList } from './ChatTraceList';
import { RowExplainCell, RowExplainHead } from './RowExplainCell'; // v0.10.1291
import { rowSubjectOf } from './rowExplain';
import {
  evidenceChipParts, evidenceChipText, evidenceHref, evidenceKindLabel, evidenceWindowFull,
  evidenceChartGroups, evidenceDefaultOpen, evidenceCellText, evidenceSeverityTone,
  evidenceLevelTone, evidenceLevelLabel, evidenceRegions,
} from './evidenceChip';

// EvidenceChips — v0.10.1272 (CoSRE adım 3/8): kanıt çipleri + sohbet içi veri
// + "Explore'da aç".
//
// query_metrics / query_traces / query_logs'un okuduğu sorgunun künyesi:
// `[Kaynak] · pencere · env/cluster · N satır · 123 ms`. İpucu tam sorguyu
// (monospace) + tam pencereyi gösterir; ⧉ sorguyu kopyalar (ipucu etkileşimsiz
// kalır — ui/Tooltip sözleşmesi).
//
// Operatör eki (2026-10-17, "chat içerisine de getirebilirsin"): çipin sohbet
// içi bloğu varsa (sunucu aynı sonuçtan chart / trace_list / table kurar,
// `evidenceRef` = `ref`) çip bir açılır düğmedir — bloğu çipin altında açar /
// kapatır. Varsayılan: cevabın SON araç sonucu açık, öncekiler kapalı
// (`defaultOpen="last"`); adım panelinde hepsi kapalı. "Explore'da aç" her
// zaman ayrı, küçük, GERÇEK bir bağlantı (yeni sekme çalışır; /cosre'de yeni
// sekmede açılır). href yoksa bağlantı yok (uydurma hedef yok); blok yoksa
// (arşivden geri yüklenen cevap — bloklar arşive girmez) çip düz etikettir.
// Kanıt yoksa hiçbir şey çizilmez.

// CorePanel @grafana/data'ya bağlı; statik bağ vendor'ı şişirir (CosreChart emsali).
const CorePanelMulti = lazy(() =>
  import('@/components/chart/corePanelEntry').then(m => ({ default: m.CorePanelMulti })));

export function EvidenceChips({ items, blocks, defaultOpen = 'none', className }: {
  items?: readonly ChatQueryEvidence[];
  /** evidenceRef → blok (lib/chatBlocks evidenceBlocksByRef). */
  blocks?: ReadonlyMap<string, EvidenceBlockView>;
  /** 'last' — bloklu son kanıt açık başlar (cevap altı); 'none' — hepsi kapalı (adım paneli). */
  defaultOpen?: 'last' | 'none';
  className?: string;
}) {
  const [open, setOpen] = useState<Record<string, boolean>>({});
  if (!items?.length) return null;
  const initial = defaultOpen === 'last' ? evidenceDefaultOpen(items, blocks) : null;
  const isOpen = (ref?: string) => !!ref && (open[ref] ?? ref === initial);
  return (
    <div className={['cm-evidence-wrap', className].filter(Boolean).join(' ')}>
      <div className="cm-evidence" role="group" aria-label="Kanıt">
        {items.map((ev, i) => (
          <EvidenceChip key={ev.ref ?? `${ev.tool}-${i}`} ev={ev} block={ev.ref ? blocks?.get(ev.ref) : undefined}
            expanded={isOpen(ev.ref)} onToggle={() => ev.ref && setOpen(o => ({ ...o, [ev.ref!]: !isOpen(ev.ref) }))} />
        ))}
      </div>
      {items.map((ev, i) => {
        const b = ev.ref ? blocks?.get(ev.ref) : undefined;
        if (!b || !isOpen(ev.ref)) return null;
        return <EvidenceBlock key={`blk-${ev.ref ?? i}`} id={blockDomId(ev.ref!)} block={b} ev={ev} />;
      })}
    </div>
  );
}

function blockDomId(ref: string): string {
  return `cm-evblk-${ref.replace(/[^a-zA-Z0-9_-]/g, '')}`;
}

export function EvidenceChip({ ev, block, expanded, onToggle }: {
  ev: ChatQueryEvidence; block?: EvidenceBlockView; expanded: boolean; onToggle: () => void;
}) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  const href = evidenceHref(ev);
  const parts = evidenceChipParts(ev);
  const text = evidenceChipText(ev);
  const tip = (
    <span className="cm-evidence__tip">
      <span className="cm-evidence__tip-h">{ev.source} · {evidenceKindLabel(ev.queryKind)}{ev.tool ? ` · ${ev.tool}` : ''}</span>
      {ev.query && <span className="cm-evidence__q mono">{ev.query}</span>}
      {evidenceWindowFull(ev.window) && <span className="cm-evidence__tip-w">{evidenceWindowFull(ev.window)}</span>}
    </span>
  );
  const body = (
    <>
      <Badge tone="info" className="cm-evidence__src">{ev.source}</Badge>
      {parts.map((p, i) => <span key={i} className="cm-evidence__part">{p}</span>)}
    </>
  );
  return (
    <span className="cm-evidence__chip">
      <Tooltip content={tip}>
        {block ? (
          <DisclosureButton anatomy="row" expanded={expanded} onClick={onToggle} className="cm-evidence__btn"
            aria-controls={expanded && ev.ref ? blockDomId(ev.ref) : undefined}
            aria-label={`${expanded ? 'Veriyi gizle' : 'Veriyi sohbette göster'}: ${text}`}>
            {body}
          </DisclosureButton>
        ) : (
          <span className="cm-evidence__btn is-static" tabIndex={0} aria-label={`Kanıt: ${text}`}>{body}</span>
        )}
      </Tooltip>
      {href && (
        <Link to={href} className="cm-evidence__go" aria-label={`Explore'da aç: ${text}`} {...linkProps}>
          Explore'da aç ↗
        </Link>
      )}
      {ev.query && <CopyButton value={ev.query} title="Sorguyu kopyala" />}
    </span>
  );
}

function EvidenceBlock({ id, block, ev }: { id: string; block: EvidenceBlockView; ev: ChatQueryEvidence }) {
  return (
    <div id={id} className="cm-evblk" role="region" aria-label={`Veri: ${evidenceChipText(ev)}`}>
      {block.type === 'chart' && <EvidenceChart p={block.payload} ev={ev} />}
      {block.type === 'table' && <EvidenceTable p={block.payload} />}
      {/* trace_list: mevcut ChatTraceList (aynı yük şekli); "Daha fazla → Traces" = Explore'da aç. */}
      {block.type === 'trace_list' && <ChatTraceList tl={block.payload} />}
    </div>
  );
}

// EvidenceChart — veri taşıyan chart bloğu: birim başına bir CorePanel (uPlot),
// yeniden sorgu YOK. Seri/nokta tavanı sunucuda (≤20 × ≤120).
// v0.10.1415 — dışa açık: servis raporu kartı VM HTTP grafiklerini AYNI çizimle basar.
// v0.10.1443 — "Dashboard'a ekle": `ev` VM PromQL kanıtıysa (lib/chatPanelSpec)
// ya da çağıran paneli verdiyse (`dashPanel`, servis raporu); düğme yalnız
// sohbet içinde (AddToDashboard ChatDashContext) ve çevrilebilen grafikte.
export function EvidenceChart({ p, ev, dashPanel }: { p: ChatEvidenceChartPayload; ev?: ChatQueryEvidence; dashPanel?: Panel | null }) {
  const groups = evidenceChartGroups(p);
  // v0.10.1284 — isteğe bağlı x-bölgeleri (explain_log_spike: taban / artış).
  const regions = evidenceRegions(p.regions);
  const origin = useChatDashOrigin();
  const panel = useMemo(
    () => (origin ? (dashPanel !== undefined ? dashPanel : chartBlockToPanel(p, ev, origin)) : null),
    [origin, dashPanel, p, ev],
  );
  return (
    <div className="cm-evblk__chart">
      {panel && <div className="cm-evblk__dash"><AddToDashboardButton panels={[panel]} /></div>}
      <Suspense fallback={<Spinner />}>
        {groups.map(g => (
          <CorePanelMulti key={g.unit || '_'} title={groups.length > 1 ? `${p.title} · ${g.unit}` : p.title}
            height={160} storageKey={`cosre-ev:${p.title}:${g.unit}`} items={g.items} unit={g.unit}
            regions={regions.length ? regions : undefined} />
        ))}
      </Suspense>
      {p.truncated && <div className="cell-hint">seri/nokta tavanı uygulandı (≤20 seri, ≤120 nokta)</div>}
    </div>
  );
}

// EvidenceTable — sohbet KANIT tablosu: tablo standardının muaf türü (CLAUDE.md
// dört tür satırı + /frontend-design-system §5 "sohbet markdown/kanıt tablosu
// muaf, kendi kapsamlı sınıfı"). Neden DataTable değil: sıralama/yeniden
// boyutlama/kalıcı genişlik bir cevabın içindeki ≤50 satırlık sabit kanıtta
// anlamsız (ChatTraceList / ToolEvidence emsali); görünüm sohbetin tek tablo
// dili `.cm-md-table`. Satır tıklanmaz; yalnız trace hücresi gerçek bağlantı.
// v0.10.1284 — dışa açık: log artışı çekmecesi katkı tablosunu AYNI kanıt
// tablosuyla çizer (sohbetteki blokla bayt-aynı görünüm; yeni tablo türü yok).
export function EvidenceTable({ p }: { p: ChatEvidenceTablePayload }) {
  const linkProps = chatLinkTargetProps(useChatLinkNewTab());
  // v0.10.1291 — satır ✨ (log satırı açıkla): yalnız sunucu özne verdiyse ve
  // AI çekmecesinin açıklama kipinde değilken (RowExplainCell).
  const rowAi = !!p.rowSubjects?.some(Boolean); // v0.10.1407 — sohbet içinde de (tık sohbete tur gönderir)
  return (
    <div className="cm-md-tw cm-evblk__table">
      <table className="cm-md-table">
        <thead>
          <tr>{p.columns.map(c => <th key={c.key} className={c.kind === 'num' ? 'num' : undefined}>{c.label}</th>)}{rowAi && <RowExplainHead />}</tr>
        </thead>
        <tbody>
          {p.rows.map((r, i) => (
            <tr key={i}>
              {p.columns.map((c, j) => {
                const v = r[j];
                if (c.kind === 'trace') {
                  const id = typeof v === 'string' ? v : '';
                  return <td key={c.key} className="mono">{id ? <Link to={traceHref(id)} title={id} {...linkProps}>{id.slice(0, 8)}</Link> : '—'}</td>;
                }
                if (c.kind === 'time' && typeof v === 'number') {
                  return <td key={c.key} className="mono" title={fmtDateTime(v)}>{fmtClock(v)}</td>;
                }
                if (c.kind === 'level') {
                  // v0.10.1275 — crit/warn rozet; ok düz metin (yalnız sapan değer renk alır).
                  const raw = String(v ?? '');
                  const tone = evidenceLevelTone(raw);
                  return <td key={c.key}>{tone ? <Badge tone={tone}>{evidenceLevelLabel(raw)}</Badge> : (raw ? evidenceLevelLabel(raw) : '—')}</td>;
                }
                if (c.kind === 'severity') {
                  const tone = evidenceSeverityTone(String(v ?? ''));
                  return <td key={c.key}>{tone ? <Badge tone={tone}>{String(v)}</Badge> : String(v ?? '—')}</td>;
                }
                return (
                  <td key={c.key} className={c.kind === 'num' ? 'num' : c.key === 'body' ? 'cm-evblk__body' : undefined}>
                    {evidenceCellText(v, c.kind)}
                  </td>
                );
              })}
              {rowAi && <RowExplainCell subject={rowSubjectOf(p.rowSubjects?.[i])} title="Bu log satırını AI ile açıkla" />}
            </tr>
          ))}
        </tbody>
      </table>
      {p.truncated && <div className="cell-hint">ilk {p.rows.length} satır — tamamı Explore'da</div>}
    </div>
  );
}
