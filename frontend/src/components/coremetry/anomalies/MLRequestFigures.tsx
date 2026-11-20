// MLRequestFigures — v0.10.2380 (triage mockup "Şimdi ve normalde"): hata
// oranı olayının üç rakamı, istek grafiğinin hemen altında: başarısız (şimdi /
// normalde), başarılı (şimdi / normalde), toplam. Rakamlar SAF çekirdekten
// (mlRequests.requestFigures); istek yoksa satır hiç çizilmez. Renk yalnız
// sapan değerde (başarısız, olağanın üstündeyse) — tablo standardının kuralı.
import { tf, useLang, useT } from '@/lib/i18n';
import type { MLRecordView } from '@/lib/types';
import { fmtRecordNum } from './mlRecord';
import { requestFigures } from './mlRequests';

export function MLRequestFigures({ rec }: { rec: MLRecordView }) {
  const t = useT();
  const lang = useLang();
  const f = requestFigures(rec);
  if (!f) return null;
  const n = (v: number) => tf('mlReq.perMin', lang, { n: fmtRecordNum(v, lang, 0) });
  const normal = (v: number | null) => (v == null ? t('mlReq.noNormal') : tf('mlReq.normal', lang, { n: n(v) }));
  const when = t(f.at === 'now' ? 'mlReq.now' : 'mlReq.atDetection');
  const up = f.normalFailed == null || f.failed > f.normalFailed;
  const cells = [
    { id: 'failed', k: `${t('mlReq.failedShort')}, ${when}`, v: n(f.failed), sub: normal(f.normalFailed), err: up },
    { id: 'ok', k: `${t('mlReq.okShort')}, ${when}`, v: n(f.ok), sub: normal(f.normalOk), err: false },
    { id: 'total', k: t('mlReq.total'), v: n(f.total), sub: normal(f.normalTotal), err: false },
  ];
  return (
    <dl className="m-0 mt-2.5 grid grid-cols-1 gap-2 border-t border-border pt-2.5 sm:grid-cols-3" data-testid="ml-request-figures" aria-label={t('mlReq.figures')}>
      {cells.map(c => (
        <div key={c.id} className="min-w-0" data-testid={`ml-request-${c.id}`}>
          <dt className="text-xs text-muted-foreground">{c.k}</dt>
          <dd className={c.err ? 'm-0 font-mono text-base font-semibold text-[color:var(--err)]' : 'm-0 font-mono text-base font-semibold'}>{c.v}</dd>
          <dd className="m-0 text-xs text-muted-foreground">{c.sub}</dd>
        </div>
      ))}
    </dl>
  );
}
