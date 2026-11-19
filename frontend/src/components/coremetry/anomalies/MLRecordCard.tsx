// MLRecordCard — v0.10.2248 (anomali kartı Dilim D, audit §10 D; mockup
// D0/G0 onaylı "ÖNERİLEN" düzeni): geçerli olay kaydı olan ML olayının kartı.
// Sıra: başlık (servis · operasyon, durum, sınıf) → düz dilde tek cümle +
// zaman satırı → TEK grafik (Dilim G) → ölçülen / beklenen (birimli) → kanıt
// satırları (bağımsız / aynı seri / bağlam, kaynak kayıttan) → eylemler →
// KAPALI teknik ayrıntı (yayın kapıları + seri tanımı). Tüm metin kayıttan
// şablonlanır, render'da sorgu yok. Kaydı olmayan olay eski kartı çizer.
import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { Badge } from '@/components/shadcn/badge';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { serviceHref } from '@/lib/serviceHref';
import { tf, useLang, useT } from '@/lib/i18n';
import { fmtClock } from '@/lib/utils';
import type { MLEvent, MLRecordView } from '@/lib/types';
import { mlEventHref } from '@/lib/anomalyHref';
import { ML_DETECTOR_TR } from '@/lib/mlDetectorLabels';
import { GROUP_MEMBERS_OPEN_MAX, groupMemberRows } from './mlGroupMembers';
import { MLRecordChart, measureLabel, stepText } from './MLRecordChart';
import { fmtRecordNum, fmtRecordValue, sourceLabel } from './mlRecord';
import {
  contextBeforeAfter, evidenceTag, evidenceText, gateThresholdText, gateValueText, headlineValue, independentCount, ingestFactor,
  isTelemetryVolume, recordHeadline, recordOpen, recordStatus, recordTimeline,
} from './mlRecordText';

const hm = (ns: number) => fmtClock(new Date(ns / 1e6)).slice(0, 5);

const CLASS_KEY: Record<string, string> = {
  service_anomaly: 'mlRecord.class.service', telemetry_volume: 'mlRecord.class.telemetry', info: 'mlRecord.class.info',
};
const KIND_KEY: Record<string, string> = {
  series: 'mlRecord.kind.series', confirm: 'mlRecord.kind.confirm', rollout: 'mlRecord.kind.rollout',
  exception: 'mlRecord.kind.exception', endpoint: 'mlRecord.kind.endpoint', pods: 'mlRecord.kind.pods',
  member: 'mlRecord.kind.member', error_rate: 'mlRecord.kind.metric', latency: 'mlRecord.kind.metric',
};
const GATE_KEY: Record<string, string> = {
  series_consistency: 'mlRecord.gate.series_consistency', visible_deviation: 'mlRecord.gate.visible_deviation',
  min_volume: 'mlRecord.gate.min_volume', min_abs_diff: 'mlRecord.gate.min_abs_diff',
  independent_evidence: 'mlRecord.gate.independent_evidence', service_impact: 'mlRecord.gate.service_impact',
};

function Section({ id, title, sub, children }: { id: string; title: string; sub?: string; children: ReactNode }) {
  return (
    <section className="mt-3" data-testid={id}>
      <h3 className="mb-1.5 flex flex-wrap items-center gap-2 text-xs font-semibold">{title}{sub && <span className="font-normal text-muted-foreground">{sub}</span>}</h3>
      {children}
    </section>
  );
}

/** Sayfa bölümlerinin sırası (mockup "ÖNERİLEN"). */
export const ML_RECORD_SECTIONS = ['ml-header', 'ml-summary', 'ml-course', 'ml-measured', 'ml-evidence', 'ml-actions', 'ml-technical'] as const;

/** v0.10.2257 — grubun diğer üye olayları (lider hariç); veri grup sayfasının
 *  liste satırlarından, üye başına sorgu yok. > 5 üyede kapalı açılır. */
function GroupMembers({ members, leadId }: { members: MLEvent[]; leadId: string }) {
  const t = useT();
  const lang = useLang();
  const rows = groupMemberRows(members, leadId, lang);
  if (rows.length === 0) return null;
  return (
    <details className="mt-3 rounded-md border border-border bg-card px-2.5 py-1.5 text-sm" data-testid="ml-group-members" open={rows.length <= GROUP_MEMBERS_OPEN_MAX}>
      <summary className="cursor-pointer text-xs font-semibold">{tf('mlRecord.group.title', lang, { n: rows.length })}</summary>
      <ul className="m-0 mt-1.5 flex list-none flex-col p-0" data-testid="ml-group-member-rows">
        {rows.map(r => (
          <li key={r.id} className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 border-t border-border py-1.5 first:border-t-0">
            <Link to={mlEventHref(r.id)} className="min-w-0 font-mono [overflow-wrap:anywhere]">
              {r.service} · {r.whatIsDetector && lang === 'tr' ? (ML_DETECTOR_TR[r.detector] ?? r.detector) : r.what}
            </Link>
            {r.cls && <Badge variant="outline">{t(CLASS_KEY[r.cls] ?? 'mlRecord.class.info')}</Badge>}
            <span className="font-mono text-xs">{tf('mlRecord.group.peakVs', lang, { peak: r.peak, exp: r.expected })}</span>
            <span className="ml-auto font-mono text-xs text-muted-foreground">{hm(r.at)}</span>
          </li>
        ))}
      </ul>
    </details>
  );
}

export function MLRecordCard({ rec, actions, groupMembers, leadId = '', chart }: { rec: MLRecordView; actions: ReactNode; groupMembers?: MLEvent[]; leadId?: string; chart?: ReactNode }) {
  const t = useT();
  const lang = useLang();
  const measure = measureLabel(rec, t);
  const f = (v: number) => fmtRecordValue(v, rec.series.unit, lang);
  const open = recordOpen(rec);
  const tv = isTelemetryVolume(rec);
  const ev = rec.evidence ?? [];
  const nInd = independentCount(rec);
  const k = ingestFactor(rec);
  const measured = [
    { k: t(open ? 'mlRecord.card.measuredNow' : 'mlRecord.card.measuredAtDetection'), v: f(headlineValue(rec)), mono: true },
    { k: t('mlRecord.card.expected'), v: tf('mlRecord.card.expectedBand', lang, { exp: f(rec.expectedValue), lo: f(rec.baseline.lo), hi: f(rec.baseline.hi) }), mono: true },
    { k: t(rec.direction === 'down' ? 'mlRecord.card.dipLabel' : 'mlRecord.card.peakLabel'), v: f(rec.peakValue), mono: true },
    { k: t('mlRecord.card.volume'), v: rec.volume != null
      ? `${fmtRecordNum(rec.volume, lang, 0)} ${t(rec.detector === 'svc_telemetry_volume' ? 'mlRecord.card.spansUnit' : 'mlRecord.card.callsUnit')}`
      : <span className="text-muted-foreground">{t('mlRecord.card.noVolume')}</span> },
    ...(tv && k != null ? [{ k: t('mlRecord.card.ingest'), v: tf('mlRecord.card.ingestValue', lang, { k: fmtRecordNum(k, lang, 1) }), mono: true }] : []),
    // Telemetri hacmi: değişmeyenler (yan seriler, olay öncesi → olay penceresi).
    ...(rec.context ?? []).flatMap(c => {
      const ba = contextBeforeAfter(rec, c);
      if (!ba) return [];
      const unit = c.unit;
      return [{ k: t(c.measure === 'error_rate' ? 'mlRecord.card.ctxErrorRate' : 'mlRecord.card.ctxLatency'),
        v: tf('mlRecord.card.beforeAfter', lang, { before: fmtRecordValue(ba.before, unit, lang), after: fmtRecordValue(ba.after, unit, lang) }), mono: true }];
    }),
  ];
  return (
    <div data-testid="ml-record-card" className="flex flex-col">
      <div className="mb-2 flex flex-wrap items-center gap-2 text-sm" data-testid="ml-header">
        <Link to={serviceHref(rec.subject.service)} className="font-mono">{rec.subject.service}</Link>
        {rec.subject.operation && <span className="font-mono text-muted-foreground">· {rec.subject.operation}</span>}
        {rec.subject.pod && <span className="font-mono text-muted-foreground">· {rec.subject.pod}</span>}
        <Badge variant="outline" className={open ? 'border-transparent bg-[color:var(--warn-bg)] text-[color:var(--warn)]' : ''} data-testid="ml-record-status">
          {recordStatus(rec, Date.now() * 1e6, lang)}
        </Badge>
        <Badge variant="outline" data-testid="ml-record-class">{t(CLASS_KEY[rec.class] ?? 'mlRecord.class.info')}</Badge>
      </div>
      <div className="mb-2.5" data-testid="ml-summary">
        <p className="m-0 text-sm font-semibold leading-normal" data-testid="ml-record-headline">{recordHeadline(rec, measure, lang)}</p>
        <p className="m-0 mt-0.5 text-xs text-muted-foreground" data-testid="ml-record-timeline">{recordTimeline(rec, lang)}</p>
      </div>
      <Section id="ml-course" title={t('mlRecord.card.course')}>
        {/* v0.10.2370 — çağıran canlı kaynak panelini verebilir; yoksa kayıtlı görüntü. */}
        {chart ?? <MLRecordChart rec={rec} />}
      </Section>
      <Section id="ml-measured" title={t('mlRecord.card.measuredTitle')}>
        <div className="rounded-md border border-border bg-card"><KeyValue labelWidth="wide" items={measured} /></div>
      </Section>
      <Section id="ml-evidence" title={t('mlRecord.card.evidence')}
        sub={tf(nInd === 1 ? 'mlRecord.card.independentOne' : 'mlRecord.card.independentN', lang, { n: nInd }) + (rec.class === 'service_anomaly' && nInd < 2 ? ` · ${t('mlRecord.card.notCandidate')}` : '')}>
        {ev.length === 0 ? <p className="m-0 text-sm text-muted-foreground">{t('mlRecord.card.noEvidence')}</p> : (
          <ul className="m-0 flex list-none flex-col gap-2 p-0" data-testid="ml-evidence-rows">
            {ev.map((e, i) => { const x = evidenceText(e, rec, measure, lang); return (
              <li key={`${e.kind}:${i}`} className="flex flex-wrap items-baseline gap-2 rounded-md border border-border bg-card px-3 py-2 text-sm">
                <Badge variant="outline">{t(KIND_KEY[e.kind] ?? 'mlRecord.kind.other')}</Badge>
                <span className="min-w-0 flex-1 [overflow-wrap:anywhere]">{x.text}</span>
                <span className="text-xs font-medium" data-testid="ml-evidence-tag">{t(`mlRecord.tag.${evidenceTag(e.kind, e.independent)}`)}</span>
                <span className="text-xs text-muted-foreground">{sourceLabel(e.source)}</span>
                {x.href && <Link to={x.href} className="text-xs">{t('mlRecord.card.open')} ↗</Link>}
              </li>
            ); })}
          </ul>
        )}
      </Section>
      {groupMembers && groupMembers.length > 0 && <GroupMembers members={groupMembers} leadId={leadId} />}
      <Section id="ml-actions" title={t('mlRecord.card.actions')}>{actions}</Section>
      <details className="mt-3.5 rounded-md border border-border bg-card px-2.5 py-1.5 text-sm" data-testid="ml-technical">
        <summary className="cursor-pointer text-xs font-semibold">{t('mlRecord.card.technical')}</summary>
        <div className="mt-2 flex flex-col gap-3">
          {(rec.gates ?? []).length > 0 && (
            <div>
              <p className="m-0 mb-1 text-xs text-muted-foreground">{t('mlRecord.card.gatesShadow')}</p>
              <ul className="m-0 flex list-none flex-col p-0 text-xs" data-testid="ml-record-gates">
                {(rec.gates ?? []).map(g => (
                  <li key={g.name} className="grid grid-cols-[minmax(0,2fr)_1fr_1fr_auto] gap-2 border-t border-border py-1 first:border-t-0">
                    <span>{t(GATE_KEY[g.name] ?? g.name)}</span>
                    <span className="font-mono">{gateValueText(g, rec, lang)}</span>
                    <span className="font-mono text-muted-foreground">{gateThresholdText(g, rec, lang)}</span>
                    <span className={!g.passed && !g.notEvaluable ? 'text-[color:var(--err)]' : ''}>
                      {g.notEvaluable ? t('mlRecord.card.gateNotEvaluable') : g.passed ? `✓ ${t('mlRecord.card.gatePassed')}` : `✕ ${t('mlRecord.card.gateFailed')}`}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}
          <KeyValue labelWidth="wide" items={[
            { k: t('mlRecord.card.testedSeries'), v: <span className="select-all [overflow-wrap:anywhere]">{rec.series.query}</span>, mono: true },
            { k: t('mlRecord.card.sourceUnitStep'), v: `${sourceLabel(rec.series.source)} · ${rec.series.unit} · ${stepText(rec.series.stepSec, lang)}` },
            { k: t('mlRecord.card.band'), v: `${f(rec.expectedValue)} (${f(rec.baseline.lo)}–${f(rec.baseline.hi)}) · ${rec.baseline.method}`, mono: true },
            { k: t('mlRecord.card.times'), v: tf('mlRecord.card.timesValue', lang, {
              onset: hm(rec.onsetAt), det: hm(rec.detectedAt), delay: stepText(Math.max(0, Math.round((rec.detectedAt - rec.onsetAt) / 6e10)) * 60, lang),
              end: rec.endedAt ? hm(rec.endedAt) : t('mlRecord.card.ongoing') }) },
            { k: t('mlRecord.card.classPublish'), v: `${rec.class} · ${rec.publishState || 'published'}`, mono: true },
          ]} />
        </div>
      </details>
    </div>
  );
}
