// ProblemBreakdownBlocks — v0.10.2388 (triage mockup dilim 3: Problem detayında
// "Hata nerede"). Yalnız çizer; veri GET /api/problems/{id}/breakdown, biçim
// lib/problemBreakdown (SAF). Dört blok, her biri YALNIZ verisi varsa:
// endpoint'e göre · sürüme göre (ya da tek satırlık deploy gerçeği) ·
// exception'a göre · bağımlılığa göre. Verisi olmayan blok çizilmez; okunamayan
// blok başlığıyla birlikte "okunamadı" yazar, ötekiler çizilir. Her blok
// kaynağını başlığının yanında söyler (örneklenmemiş metrik ≠ span kaydı).
//
// Üç tablo da gerekçeli STATİK tablo (tablo standardı T1; shadcn Table): satır
// sayısı sunucuda sabit tavanlı (endpoint ≤ 8 + "Diğer", exception ≤ 5 +
// "Diğer", bağımlılık ≤ 5), sıra sunucunun katkı sırasıdır ve anlamı odur —
// sıralama / süzme denetimi tabloyu yanlış okuturdu. Satırlar tıklanmaz;
// bağlantı yalnız gerçekten giden hücrede (exception grubu, bağımlılık olayı).
// Renk yalnız sapan değerde (.cell-err, T9); grafik yok.
import { Link } from 'react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { mlEventHref } from '@/lib/anomalyHref';
import type { Lang } from '@/lib/i18n';
import { exceptionGroupHref } from '@/lib/mlReplay';
import {
  fmtBreakdownCount, fmtBreakdownRate, fmtDependencyValue, fmtPerMin, type DeployFact,
} from '@/lib/problemBreakdown';
import type { ProblemBreakdownResponse, ProblemBreakdownRoute } from '@/lib/types';

export type BreakdownT = (key: string, vars?: Record<string, string | number>) => string;

const NUM = 'text-right tabular-nums';
const NUM_DEV = 'cell-err text-right font-semibold tabular-nums';
const MONO_WRAP = 'whitespace-normal [overflow-wrap:anywhere] font-mono text-xs';
const MUTED_WRAP = 'whitespace-normal [overflow-wrap:anywhere] text-muted-foreground';
const TEXT_WRAP = 'whitespace-normal [overflow-wrap:anywhere] text-xs';
// Aile etiketi: bilinen üç aile çevrilir, bilinmeyen ham yazılır (anahtar uydurulmaz).
const FAMILY_KEY: Record<string, string> = { db: 'pbd.dep.fam.db', messaging: 'pbd.dep.fam.messaging', http: 'pbd.dep.fam.http' };

function Block({ id, title, source, children }: { id: string; title: string; source?: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2" data-testid={`pbd-${id}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <h3 className="m-0 text-[13px] font-semibold">{title}</h3>
        {source && <span className="text-xs text-muted-foreground">{source}</span>}
      </div>
      {children}
    </section>
  );
}

function Unreadable({ t }: { t: BreakdownT }) {
  return <p className="m-0 text-xs text-muted-foreground" data-testid="pbd-failed">{t('pbd.failed')}</p>;
}

function RouteRow({ r, label, other, t, lang }: { r: ProblemBreakdownRoute; label: string; other?: boolean; t: BreakdownT; lang: Lang }) {
  return (
    <TableRow data-testid={other ? 'pbd-route-other' : 'pbd-route-row'}>
      <TableCell className={other ? MUTED_WRAP : MONO_WRAP}>{label}</TableCell>
      <TableCell className={NUM}>
        {r.normal != null
          ? fmtBreakdownRate(r.normal, lang)
          : <span className="text-xs text-muted-foreground">{t('pbd.normalUnknown')}</span>}
      </TableCell>
      <TableCell className={r.deviates ? NUM_DEV : NUM} data-testid="pbd-route-current">{fmtBreakdownRate(r.current, lang)}</TableCell>
      <TableCell className={NUM}>{fmtPerMin(r.failedPerMin, lang)}</TableCell>
    </TableRow>
  );
}

function Endpoints({ d, open, t, lang }: { d: NonNullable<ProblemBreakdownResponse['endpoints']>; open: boolean; t: BreakdownT; lang: Lang }) {
  if (d.failed) return <Block id="endpoints" title={t('pbd.ep.title')}><Unreadable t={t} /></Block>;
  return (
    <Block id="endpoints" title={t('pbd.ep.title')} source={t('pbd.src.metric')}>
      <Table aria-label={t('pbd.ep.title')}>
        <TableHeader>
          <TableRow>
            <TableHead>{t('pbd.col.route')}</TableHead>
            <TableHead className="w-32 text-right">{t('pbd.col.normal')}</TableHead>
            <TableHead className="w-24 text-right">{t(open ? 'pbd.col.now' : 'pbd.col.during')}</TableHead>
            <TableHead className="w-28 text-right">{t('pbd.col.failed')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {d.rows.map(r => <RouteRow key={r.route} r={r} label={r.route || t('pbd.noRoute')} t={t} lang={lang} />)}
          {d.other && <RouteRow r={d.other} label={t('pbd.other', { n: d.otherCount ?? 0 })} other t={t} lang={lang} />}
        </TableBody>
      </Table>
      {d.normalFailed && <p className="m-0 text-xs text-muted-foreground" data-testid="pbd-normal-failed">{t('pbd.normalFailed')}</p>}
    </Block>
  );
}

function Versions({ d, deploy, t, lang }: { d: ProblemBreakdownResponse['versions']; deploy: DeployFact | null; t: BreakdownT; lang: Lang }) {
  const fact = deploy && <p className="m-0 text-[13px]" data-testid="pbd-deploy">{t('pbd.deploy', { v: deploy.version, m: deploy.minutes })}</p>;
  if (!d) {
    // Sürüm etiketli örneklenmemiş seri yok: sürüm ORANI yazılmaz; en fazla
    // satırın zaten taşıdığı deploy gerçeği.
    return fact ? <Block id="versions" title={t('pbd.ver.only')}>{fact}</Block> : null;
  }
  return (
    <Block id="versions" title={t('pbd.ver.title')} source={t('pbd.ver.src', { l: d.label })}>
      <div className="grid grid-cols-2 gap-3">
        {d.rows.map(v => (
          <div key={v.version} className="flex min-w-0 flex-col gap-1 rounded-md border border-border p-3" data-testid="pbd-version">
            <span className="truncate font-mono text-xs" title={v.version}>{v.version}</span>
            <span className={v.deviates ? 'cell-err text-lg font-semibold tabular-nums' : 'text-lg font-semibold tabular-nums'}>
              {fmtBreakdownRate(v.current, lang)}
            </span>
            <span className="text-xs text-muted-foreground">{t('pbd.ver.rpm', { n: fmtPerMin(v.requestsPerMin, lang) })}</span>
          </div>
        ))}
      </div>
      {!!d.more && <p className="m-0 text-xs text-muted-foreground">{t('pbd.ver.more', { n: d.more })}</p>}
      {fact}
    </Block>
  );
}

function Exceptions({ d, t, lang }: { d: NonNullable<ProblemBreakdownResponse['exceptions']>; t: BreakdownT; lang: Lang }) {
  if (d.failed) return <Block id="exceptions" title={t('pbd.ex.title')}><Unreadable t={t} /></Block>;
  const share = (v: number | undefined) => (v == null ? <span className="text-muted-foreground">—</span> : fmtBreakdownRate(v, lang));
  const source = d.capped ? t('pbd.ex.srcCapped') : t('pbd.ex.src', { n: fmtBreakdownCount(d.total, lang) });
  return (
    <Block id="exceptions" title={t('pbd.ex.title')} source={source}>
      <Table aria-label={t('pbd.ex.title')}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[42%]">{t('pbd.col.exType')}</TableHead>
            <TableHead className="w-20 text-right">{t('pbd.col.share')}</TableHead>
            <TableHead className="w-20 text-right">{t('pbd.col.count')}</TableHead>
            <TableHead>{t('pbd.col.sample')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {d.rows.map(r => (
            <TableRow key={r.type} data-testid="pbd-ex-row">
              <TableCell className={MONO_WRAP}>
                {r.fingerprint ? <Link to={exceptionGroupHref(r.fingerprint)}>{r.type}</Link> : r.type}
                {(r.groups ?? 0) > 1 && <span className="ml-2 font-sans text-muted-foreground">{t('pbd.ex.groups', { n: r.groups ?? 0 })}</span>}
              </TableCell>
              <TableCell className={NUM}>{share(r.share)}</TableCell>
              <TableCell className={NUM}>{fmtBreakdownCount(r.count, lang)}</TableCell>
              <TableCell className={TEXT_WRAP}>
                {r.message
                  ? <span className="line-clamp-2" title={r.message}>{r.message}</span>
                  : <span className="text-muted-foreground">—</span>}
              </TableCell>
            </TableRow>
          ))}
          {!!d.otherTypes && (
            <TableRow data-testid="pbd-ex-other">
              <TableCell className="text-muted-foreground">{t('pbd.ex.other', { n: d.otherTypes })}</TableCell>
              <TableCell className={NUM}>{share(d.otherShare)}</TableCell>
              <TableCell className={NUM}>{fmtBreakdownCount(d.otherCount, lang)}</TableCell>
              <TableCell />
            </TableRow>
          )}
        </TableBody>
      </Table>
    </Block>
  );
}

function Dependencies({ d, t, lang, clock }: {
  d: NonNullable<ProblemBreakdownResponse['dependencies']>; t: BreakdownT; lang: Lang; clock: (ns: number) => string;
}) {
  if (d.failed) return <Block id="dependencies" title={t('pbd.dep.title')}><Unreadable t={t} /></Block>;
  return (
    <Block id="dependencies" title={t('pbd.dep.title')}>
      <Table aria-label={t('pbd.dep.title')}>
        <TableHeader>
          <TableRow>
            <TableHead className="w-[34%]">{t('pbd.col.dep')}</TableHead>
            <TableHead>{t('pbd.col.measure')}</TableHead>
            <TableHead className="w-20 text-right">{t('pbd.col.normal')}</TableHead>
            <TableHead className="w-20 text-right">{t('pbd.col.observed')}</TableHead>
            <TableHead className="w-28">{t('pbd.col.since')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {d.rows.map(r => (
            <TableRow key={r.eventId} data-testid="pbd-dep-row">
              <TableCell className={MONO_WRAP}>
                <Link to={mlEventHref(r.eventId)}>{r.target}</Link>
                <span className="ml-2 font-sans text-muted-foreground">{FAMILY_KEY[r.family] ? t(FAMILY_KEY[r.family]) : r.family}</span>
              </TableCell>
              <TableCell className="whitespace-normal">{t(r.measure === 'error_rate' ? 'pbd.dep.err' : 'pbd.dep.lat')}</TableCell>
              <TableCell className={NUM}>{fmtDependencyValue(r, r.baseline, lang)}</TableCell>
              <TableCell className={NUM_DEV}>{fmtDependencyValue(r, r.observed, lang)}</TableCell>
              <TableCell className="whitespace-normal tabular-nums">
                {clock(r.startedAt)}
                {r.live && <span className="ml-2 text-xs text-muted-foreground">{t('pbd.dep.live')}</span>}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      {!!d.more && <p className="m-0 text-xs text-muted-foreground">{t('pbd.dep.more', { n: d.more })}</p>}
      <p className="m-0 text-xs text-muted-foreground" data-testid="pbd-dep-note">{t('pbd.dep.src')}</p>
    </Block>
  );
}

/** İstek düştüğünde bölümün tek satırı (Tailwind sınıfları yalnız coremetry/ altında taranır). */
export function ProblemBreakdownError({ t }: { t: BreakdownT }) {
  return <p className="m-0 text-xs text-muted-foreground" data-testid="pbd-load-error">{t('pbd.loadError')}</p>;
}

/** Bölümün gövdesi. `deploy`: Problem satırının taşıdığı deploy gerçeği (ya da null). */
export function ProblemBreakdownBlocks({ data, deploy, t, lang, clock }: {
  data: ProblemBreakdownResponse; deploy: DeployFact | null; t: BreakdownT; lang: Lang; clock: (ns: number) => string;
}) {
  return (
    <div className="flex flex-col gap-4" data-testid="pbd-blocks">
      {data.endpoints && <Endpoints d={data.endpoints} open={!!data.open} t={t} lang={lang} />}
      <Versions d={data.versions} deploy={deploy} t={t} lang={lang} />
      {data.exceptions && <Exceptions d={data.exceptions} t={t} lang={lang} />}
      {data.dependencies && <Dependencies d={data.dependencies} t={t} lang={lang} clock={clock} />}
    </div>
  );
}
