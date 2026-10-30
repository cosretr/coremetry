// ApprovalCard — agent-core S5 (docs/cosre/agent-core-ideas.md §4.1 adım 5,
// §4.5): v2 sohbetinde onay-kapılı araç çağrısının Onayla / Reddet kartı.
//   • Kart ÇALIŞACAK argümanı gösterir (tool-input-available `input.args` —
//     sunucunun digest'ini bağladığı bayt), model metnini değil.
//   • Süre dolunca (tek setTimeout, yoklama yok) düğmeler kapanır ("Süresi doldu"); hiçbir şey gönderilmez →
//     sunucu tarafında da zaman aşımı = ret (Q10).
//   • Karar verildiyse (parça output-available) yalnız durum rozeti.
// Sunumsal: kararı onAnswer'a bildirir; gönderimi çağıran yapar.
import { useEffect, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { KeyValue } from '@/components/coremetry/KeyValue';
import { fmtClock } from '@/lib/utils';
import { approvalExpired, type ApprovalView } from '@/lib/cosre/approval';
import { COSRE_APPROVAL_TOOL_LABELS, COSRE_V2_STRINGS as T } from './strings';

export interface ApprovalCardProps {
  view: ApprovalView;
  /** Akış sürüyor / başka istek var: düğmeler kapalı. */
  disabled?: boolean;
  onAnswer?: (approved: boolean) => void;
  /** Test dikişi; üretimde Date.now. */
  now?: () => number;
}

function argItems(args: unknown) {
  if (typeof args !== 'object' || args === null || Array.isArray(args)) {
    return [{ id: 'args', k: 'args', v: JSON.stringify(args) }];
  }
  return Object.entries(args as Record<string, unknown>).map(([k, v]) => ({
    id: k, k, v: typeof v === 'string' ? v : JSON.stringify(v), mono: k.toLowerCase().endsWith('id'),
  }));
}

// setTimeout tavanı (2^31−1 ms); jeton ömrü en çok 1 saat.
const MAX_TIMEOUT = 2_147_483_647;

export function ApprovalCard({ view, disabled, onAnswer, now = Date.now }: ApprovalCardProps) {
  const { approval, state } = view;
  const [t, setT] = useState(now);
  const pending = state === 'pending';
  const expired = pending && approvalExpired(approval, t);
  // Yoklama yok: süre dolduğu ANDA bir kez yeniden çizilir (düğmeler kapanır).
  useEffect(() => {
    if (!pending || expired) return;
    const id = setTimeout(() => setT(now()), Math.min(MAX_TIMEOUT, Math.max(0, approval.expiresAt - now()) + 50));
    return () => clearTimeout(id);
  }, [pending, expired, now, approval.expiresAt]);
  const label = COSRE_APPROVAL_TOOL_LABELS[approval.tool] ?? approval.tool;
  return (
    <div className="cm-msg cm-msg--ai" data-cosre-approval={approval.toolCallId} data-state={expired ? 'expired' : state}>
      <div className="rounded-md border border-border bg-card p-3 text-sm" role="group" aria-label={`${T.approvalTitle}: ${label}`}>
        <div className="mb-1 font-medium">{T.approvalTitle} · {label}</div>
        <p className="mb-2 text-muted-foreground">{T.approvalHint}</p>
        <KeyValue items={argItems(view.args)} />
        <div className="mt-2 flex items-center gap-2">
          {pending && !expired && (
            <>
              <Button size="sm" disabled={disabled} onClick={() => onAnswer?.(true)}>{T.approvalApprove}</Button>
              <Button size="sm" variant="secondary" disabled={disabled} onClick={() => onAnswer?.(false)}>{T.approvalDeny}</Button>
              <span className="ml-auto text-muted-foreground">
                {T.approvalExpiresIn} {fmtClock(approval.expiresAt)}
              </span>
            </>
          )}
          {expired && <span className="text-muted-foreground" role="status">{T.approvalExpired}</span>}
          {state === 'approved' && <span role="status">{T.approvalApproved}</span>}
          {state === 'denied' && <span className="text-muted-foreground" role="status">{T.approvalDenied}</span>}
        </div>
      </div>
    </div>
  );
}
