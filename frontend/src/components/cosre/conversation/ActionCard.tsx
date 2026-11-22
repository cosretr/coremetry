// ActionCard — v0.10.2353 (docs/cosre/interaction-audit.md §6 I5). v2
// sohbetinde sunucu kuralının önerdiği onaylı aksiyon kartı (data-action):
// paylaşım bağlantısı ya da probleme not. Görünüş CosreRuleConfirm ile
// aynı (cosre-rule-card sınıfları; yeni CSS yok).
//   • Alanlar sunucudan dolu gelir, formda düzenlenir; Onayla'ya basmadan
//     hiçbir şey yazılmaz. Onay POST /api/cosre/actions/confirm (imzalı jeton +
//     kartın ömrü boyunca sabit idemKey: çift tık / yeniden gönderim ikinci
//     yazım yapmaz). Sonuç data-action-result olarak mesaja eklenir (onResult).
//   • needs_save: konuşma henüz kaydedilmemiş — kart açık kalır, yeniden onaylanabilir.
//   • İptal yalnız action_cancel sinyali yazar (sunucu ucu), kartı kapatır.
//   • Jira yok (açık soru 2); ara çözüm "Metni kopyala" (istemci, sunucu yazımı yok).
import { useRef, useState } from 'react';
import '@/styles/cosreChat.css';
import { Button } from '@/components/shadcn/button';
import { Field, TextareaField } from '@/components/coremetry/Field';
import { api } from '@/lib/api';
import { parseDataActionResult } from '@/lib/cosre/protocol';
import { isSafeAppPath } from '@/lib/safeAppPath';
import type { CosreActionCard, CosreActionFields, CosreActionResult } from '@/lib/types';
import { COSRE_V2_STRINGS as T } from './strings';

export interface ActionCardProps {
  card: CosreActionCard;
  /** Mesajdaki data-action-result (varsa kart sonuç satırına döner). */
  result?: CosreActionResult | null;
  /** Gönderim anındaki konuşma kimliği (paylaşım bağlantısı için). */
  conversationId?: () => string | null;
  /** Onayın data-action-result parçası (mesaja eklenir). Yoksa kart salt okur. */
  onResult?: (part: { type: string; id?: string; data?: unknown }) => void;
  /** Test dikişi; üretimde Date.now. */
  now?: () => number;
}

function newIdemKey(): string {
  const c = globalThis.crypto as Crypto | undefined;
  if (c?.randomUUID) return c.randomUUID();
  return `idem-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

/** Jira vb. için düz metin (istemci; sunucu yazımı yok). */
export function actionCardText(f: CosreActionFields, link?: string): string {
  const meta = [f.service && `servis ${f.service}`, f.window && `pencere ${f.window}`].filter(Boolean).join(', ');
  return [f.title + (meta ? ` (${meta})` : ''), f.summary, f.evidence?.length ? `${T.actionEvidence}: ${f.evidence.join('; ')}` : '', link ?? '']
    .filter(Boolean).join('\n');
}

export function ActionCard({ card, result, conversationId, onResult, now = Date.now }: ActionCardProps) {
  const [fields, setFields] = useState<CosreActionFields>(card.fields);
  const [state, setState] = useState<'idle' | 'busy' | 'cancelled' | 'needs_save'>('idle');
  const [err, setErr] = useState<string | null>(null);
  const idem = useRef<string>(newIdemKey());
  const done = result?.status === 'done';
  const expired = !done && card.expiresAt < now();
  const title = card.kind === 'problem_note' ? T.actionNoteTitle : T.actionShareTitle;

  if (state === 'cancelled') return <div className="cm-msg-note" data-testid="cosre-action-cancelled">{T.actionCancelled}</div>;

  const set = (k: keyof CosreActionFields) => (e: { target: { value: string } }) => setFields(f => ({ ...f, [k]: e.target.value }));
  const submit = async () => {
    setState('busy');
    setErr(null);
    try {
      const part = await api.confirmCosreAction({
        token: card.token, kind: card.kind, idemKey: idem.current, fields,
        ...(card.kind === 'share_link' ? { conversationId: conversationId?.() ?? undefined } : {}),
      });
      const r = parseDataActionResult(part);
      if (!r) throw new Error(T.actionBadResult);
      if (r.status === 'needs_save') {
        setState('needs_save');
        return;
      }
      setState('idle');
      onResult?.(part);
    } catch (e) {
      setState('idle');
      setErr(e instanceof Error ? e.message : String(e));
    }
  };
  const cancel = () => {
    setState('cancelled');
    void api.cancelCosreAction({ token: card.token, kind: card.kind }).catch(() => { /* yalnız sinyal */ });
  };
  const copy = () => { void navigator.clipboard?.writeText(actionCardText(fields, result?.link)).catch(() => {}); };
  const busy = state === 'busy';
  const locked = done || expired || busy || !onResult;
  return (
    <section className="cosre-rule-card" aria-label={title} data-testid="cosre-action-card" data-kind={card.kind}>
      <div className="cosre-rule-card__title">{title}</div>
      <Field label={T.actionFieldTitle} value={fields.title} disabled={locked} onChange={set('title')} />
      <div className="cosre-rule-card__actions">
        <Field small label={T.actionFieldService} value={fields.service ?? ''} disabled={locked} onChange={set('service')} />
        <Field small label={T.actionFieldWindow} value={fields.window ?? ''} disabled={locked} onChange={set('window')} />
      </div>
      <TextareaField label={T.actionFieldSummary} rows={3} value={fields.summary} disabled={locked} onChange={set('summary')} />
      {!!fields.evidence?.length && (
        <div className="cosre-rule-card__reason">{T.actionEvidence}: {fields.evidence.join(' · ')}</div>
      )}
      <div className="cosre-rule-card__actions">
        {!done && (
          <>
            <Button size="sm" disabled={locked} aria-busy={busy || undefined} onClick={() => { void submit(); }}>
              {card.label || T.actionConfirm}
            </Button>
            <Button variant="outline" size="sm" disabled={busy || expired} onClick={cancel}>{T.actionCancel}</Button>
          </>
        )}
        {done && result?.link && isSafeAppPath(result.link) && <a className="cm-md-a" href={result.link} data-nav="1">{card.kind === 'share_link' ? T.actionOpenLink : T.actionOpenProblem}</a>}
        <Button variant="ghost" size="sm" onClick={copy}>{T.actionCopy}</Button>
        <span className="cosre-rule-card__reason" role={err || state === 'needs_save' ? 'alert' : undefined}>
          {err ? `${T.actionFailed}: ${err}`
            : state === 'needs_save' ? T.actionNeedsSave
              : done ? T.actionDone
                : expired ? T.actionExpired : T.actionHint}
        </span>
      </div>
    </section>
  );
}
