// SreReport — CoSRE Faz C2: paylaşılabilir araştırma raporu, SALT-OKUNUR.
// /sre/report/:id (id = "sre-<problemId>"). Normal oturum; viewer okur
// (GET /api/problems/{id}/investigation — sayfa yeni uç açmaz, saklı kayıt).
// Problem bildirimindeki "Rapor" bağlantısı (C1) buraya iner.
//
// Kromsuz kabuk (AppShell .sre-report-bare) + yazdırılabilir: globals.css
// print bloğu kabuğun 100vh/taşma kısıtını kaldırır; eylem satırı print:hidden.
// Sonuç bildirimle aynı kuralla: kanıt kapısı "yetersiz" → neden yazılmaz.
import type { ReactNode } from 'react';
import { Link, useParams } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { Button } from '@/components/shadcn/button';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { Spinner, Empty } from '@/components/Spinner';
import { api } from '@/lib/api';
import { t, tf, useLang } from '@/lib/i18n';
import { fmtDateTime } from '@/lib/utils';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import { checkTitle, fmtScore, hypLabel, outcomeView, timelineKindLabel } from '@/features/anomalies/hypothesisView';
import {
  gapText, playbookLabel, reachSplit, reachStatusLabel, sourceLabel, stateLabel, stateTone,
} from '@/features/anomalies/investigationQualityView';
import { orderedSteps, stepLabel, stepStateView } from '@/features/anomalies/investigationResultView';
import { MsgText, OrigLang } from '@/features/anomalies/OrigLang';
import { reportConclusion, reportProblemId } from './sreReportView';

const SECTION = 'mb-5 break-inside-avoid';
const H2 = 'mb-2 text-[length:var(--fs-sm)] font-semibold';
const MUTED = 'text-(--text3)';
const SMALL = 'text-[length:var(--fs-xs)]';

const nsToMs = (ns?: number) => (ns ? Math.floor(ns / 1e6) : 0);

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={SECTION}>
      <h2 className={H2}>{title}</h2>
      {children}
    </section>
  );
}

function HypothesisRow({ h }: { h: SREHypothesis }) {
  const lang = useLang();
  return (
    <li className="break-inside-avoid py-1" data-state={h.state}>
      <div className="flex flex-wrap items-baseline gap-2">
        <span className={h.state === 'ruled_out' || h.state === 'untested' ? MUTED : 'font-medium'}>{hypLabel(h, lang)}</span>
        {h.state && <StatusBadge status={stateTone(h.state)} size="sm">{stateLabel(h.state, lang)}</StatusBadge>}
        <span className={`num ${SMALL} ${MUTED}`}>{fmtScore(h.score)}</span>
      </div>
      {h.checks.length > 0 && (
        <ul className={`m-0 list-none pl-4 ${SMALL}`}>
          {h.checks.map(c => {
            const o = outcomeView(c.outcome, lang);
            const title = checkTitle(c.name, lang);
            return (
              <li key={c.name}>
                <span aria-label={o.label}>{o.glyph}</span>{' '}
                {title && <span className="font-medium">{title}: </span>}
                <OrigLang>{c.reason || '—'}</OrigLang>
              </li>
            );
          })}
        </ul>
      )}
    </li>
  );
}

function Report({ inv }: { inv: SREInvestigation }) {
  const lang = useLang();
  const conc = reportConclusion(inv);
  const { checked, blind } = reachSplit(inv.reach);
  const pb = playbookLabel(inv, lang);
  const steps = orderedSteps(inv.steps ?? []);
  return (
    <article>
      <Section title={t('srr.summary', lang)}>
        {conc.kind === 'cause' && (
          <p className="m-0" data-testid="srr-cause">
            <span className={MUTED}>{t('srr.cause', lang)}: </span>
            <b>{hypLabel(conc.top, lang)}</b> <span className={`num ${SMALL} ${MUTED}`}>{fmtScore(conc.top.score)}</span>
          </p>
        )}
        {conc.kind === 'insufficient' && (
          <div className="cm-callout is-caution m-0" role="status" data-testid="srr-insufficient">
            <b>{t('iq.gate.head', lang)}</b> — {t('iq.gate.body', lang)}
            {(inv.coverage?.missing ?? []).length > 0 && (
              <ul className="m-0 mt-1 pl-5">
                {(inv.coverage?.missing ?? []).map((m, i) => <li key={`${m.code}:${m.source ?? ''}:${i}`}>{gapText(m, lang)}</li>)}
              </ul>
            )}
          </div>
        )}
        {conc.kind === 'none' && <p className={`m-0 ${MUTED}`}>{t('srr.noHyp', lang)}</p>}
        {inv.summary?.text && !inv.summary.gated && (
          <div className="mt-2 whitespace-pre-wrap" data-testid="srr-narration">
            <OrigLang block>{inv.summary.text}</OrigLang>
          </div>
        )}
      </Section>

      {(inv.hypotheses ?? []).length > 0 && (
        <Section title={t('iq.tree.head', lang) + (pb ? ` · ${pb}` : '')}>
          <ul className="m-0 list-none p-0" aria-label={t('iq.tree.aria', lang)}>
            {(inv.hypotheses ?? []).map(h => <HypothesisRow key={h.kind} h={h} />)}
          </ul>
        </Section>
      )}

      {steps.length > 0 && (
        <Section title={t('srr.evidence', lang)}>
          <ul className="m-0 list-none p-0">
            {steps.map(s => {
              const st = stepStateView(s.state, lang);
              return (
                <li key={s.name} className="break-inside-avoid py-1">
                  <span className="font-medium">{stepLabel(s.name, lang)}</span>{' '}
                  <span className={`${SMALL} ${MUTED}`}>· {st.label}{s.evidence?.length ? ` · ${tf('iq.tree.refs', lang, { n: s.evidence.length })}` : ''}</span>
                  {s.summary && <div className={SMALL}><MsgText msg={s.summaryMsg} text={s.summary} /></div>}
                </li>
              );
            })}
          </ul>
        </Section>
      )}

      {(checked.length > 0 || blind.length > 0) && (
        <Section title={t('iq.reach.head', lang)}>
          <p className="m-0">
            <span className={MUTED}>{t('iq.reach.checked', lang)}: </span>
            {checked.length ? checked.map(r => sourceLabel(r.source, lang)).join(', ') : t('iq.reach.none', lang)}
          </p>
          {blind.length > 0 && (
            <p className="m-0" data-testid="srr-blind">
              <span className={MUTED}>{t('iq.reach.blind', lang)}: </span>
              {blind.map(r => `${sourceLabel(r.source, lang)} (${reachStatusLabel(r, lang)})`).join(', ')}
            </p>
          )}
        </Section>
      )}

      {(inv.timeline ?? []).length > 0 && (
        <Section title={t('srr.timeline', lang)}>
          <ul className="m-0 list-none p-0">
            {(inv.timeline ?? []).map((e, i) => (
              <li key={`${e.at}:${i}`} className="break-inside-avoid">
                <span className={`num ${SMALL} ${MUTED}`}>{fmtDateTime(nsToMs(e.at))}</span>{' '}
                <span className="font-medium">{timelineKindLabel(e.kind, lang)}</span>{' '}
                <MsgText msg={e.labelMsg} text={e.label} />
              </li>
            ))}
          </ul>
        </Section>
      )}
    </article>
  );
}

export default function SreReport() {
  const { id = '' } = useParams();
  const lang = useLang();
  const problemId = reportProblemId(id);
  const q = useQuery({
    queryKey: ['sre-report', problemId],
    queryFn: ({ signal }) => api.problemInvestigation(problemId, signal),
    enabled: !!problemId,
    retry: false,
  });
  const inv = q.data?.investigation;
  return (
    <main className="h-full overflow-auto bg-(--bg) print:h-auto print:overflow-visible">
      <div className="mx-auto max-w-[860px] px-4 py-6">
        <header className="mb-5 border-b border-(--divider) pb-3">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h1 className="m-0 text-[length:var(--fs-lg)] font-semibold">{t('srr.title', lang)}</h1>
            <div className="flex gap-2 print:hidden">
              <Button variant="outline" size="sm" asChild>
                <Link to={`/problems?problem=${encodeURIComponent(problemId)}`}>{t('srr.back', lang)}</Link>
              </Button>
              <Button size="sm" onClick={() => window.print()} disabled={!inv}>{t('srr.print', lang)}</Button>
            </div>
          </div>
          {inv && (
            <div className={`${SMALL} ${MUTED} mt-1`} data-testid="srr-meta">
              {inv.service}
              {inv.displayId ? ` · ${inv.displayId}` : ` · ${inv.problemId}`}
              {inv.priority ? ` · ${inv.priority}` : ''}
              {inv.finishedAt ? ` · ${tf('srr.finished', lang, { t: fmtDateTime(nsToMs(inv.finishedAt)) })}` : ` · ${inv.state}`}
            </div>
          )}
        </header>
        {!problemId ? (
          <Empty icon="🔎" title={t('srr.notFound', lang)} />
        ) : q.isPending ? (
          <Spinner />
        ) : q.isError || !inv ? (
          <Empty icon="🔎" title={t('srr.notFound', lang)}>{t('srr.notFoundBody', lang)}</Empty>
        ) : inv.state !== 'done' && inv.state !== 'partial' ? (
          <Empty icon="⏳" title={t('srr.pending', lang)}>{tf('srr.pendingBody', lang, { s: inv.state })}</Empty>
        ) : (
          <Report inv={inv} />
        )}
      </div>
    </main>
  );
}
