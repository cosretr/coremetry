// ProblemInvestigationCards — v0.10.1288 (AI SRE Faz 2). AI SRE araştırmasının
// hipotez kartları + zaman çizelgesi (InvestigationResult çağırır):
//   - en olası ≤3 hipotez: bant + KOD skoru (öncül + kontrol ağırlıkları; LLM,
//     oy ya da operatör işareti skora girmez), her kontrolün sonucu ✓/✗/? +
//     gerekçe + kanıt çipi;
//   - editor: "Doğrula" (hipotezin derin okumalarıyla yeniden koşu, POST
//     investigation {kind:'verify'}), "Ele" / "Onayla" (+ isteğe bağlı not;
//     işaret yalnız etiket, skoru DEĞİŞTİRMEZ). Viewer kartları GÖRÜR, düğmeyi
//     görmez;
//   - kök-neden sentezleyicisi (korelatör) AYRI motor olarak ayrı kutuda —
//     iki motorun sonucu birbirine karıştırılmaz;
//   - zaman çizelgesi ≤30 olay (sıralı liste; tablo değil — tek sütunlu olay
//     akışı, tablo standardının "kayıt listesi" sınıfına girmez).
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { EvidenceChips } from '@/components/ai/EvidenceChips';
import { Badge, Button, Field, LinkButton } from '@/components/ui';
import { api } from '@/lib/api';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { fmtClock, tsLong } from '@/lib/utils';
import { t, tf, useLang } from '@/lib/i18n'; // v0.10.2288 — metinler arayüz dilinde (TR aynı)
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import {
  ROOT_CAUSE_ANCHOR, bandLabel, bandTone, canAct, checkEvidence, checkTitle, fmtScore, humanLabel, hypCategoryLabel, hypKindName, hypLabel,
  outcomeView, queueReasonText, shownHypotheses, timelineKindLabel,
} from './hypothesisView';
import { OrigLang, MsgText } from './OrigLang';

const muted = { color: 'var(--text3)' } as const;

export function ProblemInvestigationCards({ inv, canEdit }: { inv: SREInvestigation; canEdit: boolean }) {
  const lang = useLang();
  const hyps = shownHypotheses(inv);
  const timeline = inv.timeline ?? [];
  const synth = inv.synthRef;
  if (hyps.length === 0 && timeline.length === 0 && !synth?.topSuspect) return null;
  return (
    <div style={{ display: 'grid', gap: 10 }}>
      {hyps.length > 0 && (
        <div>
          <div style={{ ...muted, fontSize: 12, marginBottom: 6 }}>
            {t('hyp.head', lang)}
            {inv.kind === 'verify' && inv.hypothesis ? tf('hyp.head.verify', lang, { h: hypKindName(inv.hypothesis, lang) }) : ''}
          </div>
          <div style={{ display: 'grid', gap: 8 }}>
            {hyps.map(h => <HypothesisCard key={h.kind} inv={inv} h={h} canEdit={canEdit} />)}
          </div>
        </div>
      )}
      {synth?.topSuspect && (
        <div className="cm-callout" style={{ margin: 0 }}>
          <b>{t('hyp.synth', lang)}</b>{t('hyp.synth.note', lang)}{' '}
          {t('hyp.synth.suspect', lang)} <span className="mono">{synth.topSuspect}</span> {tf('hyp.synth.conf', lang, { p: Math.round(synth.confidence * 100) })}{' '}
          <LinkButton onClick={() => document.getElementById(ROOT_CAUSE_ANCHOR)?.scrollIntoView({ behavior: 'smooth', block: 'start' })}>
            {t('hyp.synth.go', lang)}
          </LinkButton>
        </div>
      )}
      {timeline.length > 0 && (
        <div>
          <div style={{ ...muted, fontSize: 12, marginBottom: 4 }}>{tf('hyp.tl.head', lang, { n: timeline.length })}</div>
          <ol aria-label={t('hyp.tl.aria', lang)} style={{ margin: 0, paddingLeft: 0, listStyle: 'none', display: 'grid', gap: 2 }}>
            {timeline.map((e, i) => (
              <li key={`${e.kind}:${e.at}:${i}`} style={{ display: 'grid', gridTemplateColumns: '72px 96px 1fr', gap: 8 }}>
                <span className="num" style={muted} title={tsLong(e.at)}>{fmtClock(e.at / 1e6)}</span>
                <span style={muted}>{timelineKindLabel(e.kind, lang)}</span>
                <span style={{ fontWeight: e.kind === 'problem_start' ? 600 : undefined }}><MsgText msg={e.labelMsg} text={e.label} /></span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </div>
  );
}

function HypothesisCard({ inv, h, canEdit }: { inv: SREInvestigation; h: SREHypothesis; canEdit: boolean }) {
  const qc = useQueryClient();
  const lang = useLang();
  const [busy, setBusy] = useState<'' | 'verify' | 'mark'>('');
  const [marking, setMarking] = useState<'' | 'dismissed' | 'confirmed'>('');
  const [note, setNote] = useState('');
  const human = humanLabel(h, lang);
  const actionable = canEdit && canAct(inv);

  const refresh = async () => {
    await qc.invalidateQueries({ queryKey: keys.problems.investigation(inv.problemId) });
    await qc.invalidateQueries({ queryKey: keys.sre.all });
  };
  const verify = async () => {
    setBusy('verify');
    try {
      const r = await api.enqueueProblemInvestigation(inv.problemId, { kind: 'verify', hypothesis: h.kind });
      if (r.created) toast.success(tf('hyp.toast.queued', lang, { h: hypLabel(h, lang) }));
      else toast.info(r.reason ? tf('hyp.toast.already', lang, { r: queueReasonText(r.state, r.reason, lang) }) : t('hyp.toast.inQueue', lang));
      await refresh();
    } catch (e) {
      toast.error(tf('hyp.toast.queueErr', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy('');
    }
  };
  const mark = async () => {
    if (!marking) return;
    setBusy('mark');
    try {
      await api.markProblemHypothesis(inv.problemId, h.kind, { state: marking, note: note.trim() || undefined });
      toast.success(t(marking === 'dismissed' ? 'hyp.toast.dismissed' : 'hyp.toast.confirmed', lang));
      setMarking('');
      setNote('');
      await refresh();
    } catch (e) {
      toast.error(tf('hyp.toast.markErr', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy('');
    }
  };

  return (
    <div data-hypothesis={h.kind} style={{
      border: '1px solid var(--border)', borderRadius: 'var(--radius-sm)', padding: '8px 10px',
      opacity: h.humanState?.state === 'dismissed' ? 0.7 : undefined,
    }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 8 }}>
        <b>{hypLabel(h, lang)}</b>
        <Badge tone={bandTone(h.band)}>{bandLabel(h.band, lang)}</Badge>
        <span className="num" title={tf('hyp.score.title', lang, { p: fmtScore(h.prior) })}>{fmtScore(h.score)}</span>
        <span style={{ ...muted, fontSize: 12 }}>{tf('hyp.category', lang, { c: hypCategoryLabel(h.category, lang) })}</span>
        {human && (
          <Badge tone={h.humanState?.state === 'confirmed' ? 'success' : 'neutral'} title={h.humanState?.at ? tsLong(h.humanState.at) : undefined}>
            {human}
          </Badge>
        )}
      </div>
      {h.humanState?.note && <div style={{ ...muted, fontSize: 12, marginTop: 2 }}>{tf('hyp.note', lang, { n: h.humanState.note })}</div>}
      <ul style={{ margin: '6px 0 0', paddingLeft: 0, listStyle: 'none', display: 'grid', gap: 3 }}>
        {h.checks.map(c => {
          const v = outcomeView(c.outcome, lang);
          const title = checkTitle(c.name, lang);
          return (
            <li key={c.name} style={{ display: 'grid', gridTemplateColumns: '18px 1fr', gap: 4 }}>
              <span aria-label={v.label} title={tf('hyp.check.title', lang, { l: v.label, w: c.weight })}
                style={{ color: c.outcome > 0 ? 'var(--ok)' : c.outcome < 0 ? 'var(--err)' : 'var(--text3)' }}>{v.glyph}</span>
              <span style={c.outcome === 0 ? muted : undefined}>
                {title && <>{title}: </>}<OrigLang>{c.reason}</OrigLang>
                <EvidenceChips items={checkEvidence(inv, c.evidenceRef)} />
              </span>
            </li>
          );
        })}
      </ul>
      {actionable && !marking && (
        <div style={{ display: 'flex', gap: 6, marginTop: 8, flexWrap: 'wrap' }}>
          <Button variant="secondary" size="sm" loading={busy === 'verify'} disabled={!!busy} onClick={() => { void verify(); }}>
            {t('hyp.verify', lang)}
          </Button>
          <Button variant="ghost" size="sm" disabled={!!busy} onClick={() => setMarking('dismissed')}>{t('hyp.dismiss', lang)}</Button>
          <Button variant="ghost" size="sm" disabled={!!busy} onClick={() => setMarking('confirmed')}>{t('hyp.confirm', lang)}</Button>
        </div>
      )}
      {actionable && marking && (
        <div style={{ display: 'grid', gap: 6, marginTop: 8 }}>
          <Field label={t(marking === 'dismissed' ? 'hyp.note.dismiss' : 'hyp.note.confirm', lang)} value={note} maxLength={500}
            onChange={e => setNote(e.target.value)} hint={t('hyp.note.hint', lang)} />
          <div style={{ display: 'flex', gap: 6 }}>
            <Button variant="primary" size="sm" loading={busy === 'mark'} onClick={() => { void mark(); }}>
              {t(marking === 'dismissed' ? 'hyp.dismiss' : 'hyp.confirm', lang)}
            </Button>
            <Button variant="ghost" size="sm" disabled={busy === 'mark'} onClick={() => { setMarking(''); setNote(''); }}>{t('hyp.cancel', lang)}</Button>
          </div>
        </div>
      )}
    </div>
  );
}
