// HypothesisRuling — hipotez ağacında ekip kararı (Doğrulandı / Reddedildi).
// Karar araştırmanın kendi kaydında (HumanState; POST / DELETE
// /api/problems/{id}/investigation/hypotheses/{kind}, editor+, audit).
// Otomatik durum yine hesaplanır ve kararın yanında "otomatik: …" diye
// görünür; karar onu ezer (Go hypothesis_ruling.go). Viewer kararı GÖRÜR,
// düğmeleri görmez. Oy / triyaj düğmeleri karar değildir.
import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { Button } from '@/components/shadcn/button';
import { Field } from '@/components/coremetry/Field';
import { api } from '@/lib/api';
import { t, tf, useLang } from '@/lib/i18n';
import { keys } from '@/lib/queries';
import { toast } from '@/lib/toast';
import { tsLong } from '@/lib/utils';
import type { SREHypothesis, SREInvestigation } from '@/lib/types';
import { canAct } from './hypothesisView';
import { rulingLabel, stateLabel, type Ruling } from './investigationQualityView';

const muted = { color: 'var(--text3)', fontSize: 12 } as const;
const RULINGS: readonly Ruling[] = ['confirmed', 'dismissed'];

export function HypothesisRuling({ inv, h, canEdit }: { inv: SREInvestigation; h: SREHypothesis; canEdit: boolean }) {
  const lang = useLang();
  const cur = h.humanState?.state;
  const who = h.humanState?.by ? ` · ${h.humanState.by}` : '';
  return (
    <>
      {cur && (
        <StatusBadge status={cur === 'confirmed' ? 'ok' : 'unknown'} size="sm"
          title={(h.humanState?.at ? tsLong(h.humanState.at) : '') + who}>
          {rulingLabel(h, lang)}
        </StatusBadge>
      )}
      {cur && h.state && <span style={muted} data-testid="iq-auto">{tf('iq.ruling.auto', lang, { s: stateLabel(h.state, lang) })}</span>}
      {canEdit && canAct(inv) && <RulingActions inv={inv} h={h} />}
      {h.humanState?.note && <span style={{ ...muted, flexBasis: '100%', paddingLeft: 12 }}>{tf('hyp.note', lang, { n: h.humanState.note })}</span>}
    </>
  );
}

function RulingActions({ inv, h }: { inv: SREInvestigation; h: SREHypothesis }) {
  const lang = useLang();
  const qc = useQueryClient();
  const [pending, setPending] = useState<'' | Ruling>('');
  const [note, setNote] = useState('');
  const [busy, setBusy] = useState(false);
  const cur = h.humanState?.state;

  const run = async (fn: () => Promise<unknown>, ok: string) => {
    setBusy(true);
    try {
      await fn();
      toast.success(ok);
      setPending('');
      setNote('');
      await qc.invalidateQueries({ queryKey: keys.problems.investigation(inv.problemId) });
    } catch (e) {
      toast.error(tf('hyp.toast.markErr', lang, { e: e instanceof Error ? e.message : String(e) }));
    } finally {
      setBusy(false);
    }
  };
  const save = (r: Ruling) => run(
    () => api.markProblemHypothesis(inv.problemId, h.kind, { state: r, note: note.trim() || undefined }),
    t(`iq.ruling.toast.${r}`, lang));
  const clear = () => run(() => api.clearProblemHypothesis(inv.problemId, h.kind), t('iq.ruling.toast.cleared', lang));

  if (pending) {
    return (
      <span style={{ display: 'flex', gap: 6, flexBasis: '100%', paddingLeft: 12, alignItems: 'flex-end' }}>
        <Field small label={t('iq.ruling.note', lang)} value={note} maxLength={500}
          onChange={e => setNote(e.target.value)} />
        <Button size="xs" disabled={busy} onClick={() => { void save(pending); }}>{t(`iq.ruling.set.${pending}`, lang)}</Button>
        <Button variant="ghost" size="xs" disabled={busy} onClick={() => { setPending(''); setNote(''); }}>{t('hyp.cancel', lang)}</Button>
      </span>
    );
  }
  return (
    <>
      {RULINGS.map(r => (
        <Button key={r} variant="ghost" size="xs" disabled={busy} aria-pressed={cur === r} data-ruling-action={r}
          onClick={() => { if (cur === r) void clear(); else setPending(r); }}>
          {t(cur === r ? 'iq.ruling.clear' : `iq.ruling.set.${r}`, lang)}
        </Button>
      ))}
    </>
  );
}
