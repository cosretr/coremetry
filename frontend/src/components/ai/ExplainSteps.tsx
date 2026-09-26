// ExplainSteps — v0.10.948 (CoSRE Faz B): "CoSRE'ye sor" incelemesinin
// canlı ilerleme listesi. Satırlar YALNIZ sunucunun step / step-result
// olaylarından doğar (investigationSteps.ts): sabit ya da tahmini bir "loglar
// taranıyor…" metni yok — sunucu bir okumayı başlatmadıysa satırı da yok.
//
// İki hâl: inceleme sürerken (`live`) liste açık ve aria-live; cevap metni
// gelmeye başlayınca tek satırlık özet ("⚙ 5 okuma · 1 hata · en uzun 2,1 s"),
// tıklayınca aynı liste. Önbellekten gelen cevapta hiç çizilmez (hiçbir şey
// koşmadı — çağıran listeyi boşaltır).
import { useState } from 'react';
import type { ChatStepDetail } from '@/lib/types';
import { DisclosureButton } from '@/components/ui/DisclosureButton';
import { fmtMs } from './toolSteps';
import { explainStepRows, summarizeExplainSteps, type ExplainStepRow, type ExplainStepsSummary } from './investigationSteps';
import { StateBadges } from './StateBadges';

function SummaryText({ sum }: { sum: ExplainStepsSummary }) {
  return (<>
    ⚙ {sum.count} okuma
    {sum.running > 0 && <> · {sum.running} sürüyor</>}
    {sum.errors > 0 && <> · <span className="cell-err">{sum.errors} hata</span></>}
    {sum.skipped > 0 && <> · {sum.skipped} yürütülmedi</>}
    {sum.degraded > 0 && <> · {sum.degraded} eksik kapsam</>}
    {/* v0.10.948 — okumalar PARALEL koşar: Σ değil, en uzun tek okuma (sunucu ölçümü) */}
    {sum.running === 0 && <> · {sum.longestMs === null ? '—' : `en uzun ${fmtMs(sum.longestMs)}`}</>}
  </>);
}

function StepStatus({ r }: { r: ExplainStepRow }) {
  switch (r.status) {
    case 'running':
      return <span className="badge b-gray">çalışıyor…</span>;
    case 'no-result':
      return <span className="badge b-gray" title="Sunucu bu okumanın sonucunu yayınlamadı">sonuç gelmedi</span>;
    case 'skipped':
      return <span className="badge b-gray" title="Sunucu bu çağrıyı yürütmedi (süre ölçüm değildir)">yürütülmedi</span>;
    case 'error':
      return <span className="badge b-err">⚠ {r.errorLabel ?? 'hata'}</span>;
    default:
      if (r.states.length > 0) return <StateBadges states={r.states} />;
      if (r.unknownState) return <span className="badge b-warn" title="önizleme kırpık; kaynak durumu önizlemede yok">durum okunamadı</span>;
      return <span className="badge b-gray">ok</span>;
  }
}

export function ExplainSteps({ steps, live }: { steps: ChatStepDetail[]; live: boolean }) {
  const [open, setOpen] = useState(false);
  const rows = explainStepRows(steps, live);
  if (rows.length === 0) return null;
  const sum = summarizeExplainSteps(rows);
  const expanded = live || open;
  return (
    <div className="cx-steps">
      <div className="cm-steps-sum">
        {live ? (
          <span aria-live="polite"><SummaryText sum={sum} /></span>
        ) : (
          <DisclosureButton anatomy="row" expanded={open} onClick={() => setOpen(v => !v)}
            title="Sunucunun bu cevap için yürüttüğü okumalar (süre: sunucu ölçümü, model süresi hariç; okumalar paralel koşar — süreler toplanmaz, özet en uzun tek okumayı gösterir)">
            <SummaryText sum={sum} />
          </DisclosureButton>
        )}
      </div>
      {expanded && (
        <ul className="cx-step-list" aria-label="Yürütülen okumalar">
          {rows.map(r => (
            <li key={r.i} className="cx-step">
              <span className="cx-step-tool">{r.tool}</span>
              <StepStatus r={r} />
              {typeof r.durationMs === 'number' && <span className="cx-step-ms">{fmtMs(r.durationMs)}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
