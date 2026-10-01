import { useAuth } from '@/components/AuthProvider';
import { Button } from '@/components/ui';
import { useProblemVerdicts, useSetProblemVerdict } from '@/lib/queries';
import { inboxSignature, verdictIndex, verdictNotifyHint } from '@/lib/problemVerdict';
import type { InboxItem, ProblemVerdictKind } from '@/lib/types';
import { useMemo } from 'react';

// ProblemVerdictActions — v0.10.1015 — Problems sekmesinde ÖĞRETME'nin
// düğmeleri (operatör: "ben hangisi gerçek problem hangisi değil zamanla
// öğretelim"). Triage çekmecesinde, mevcut eylemlerin altında.
//
// Karar satırın İMZASINA yazılır (lib/problemVerdict inboxSignature): aynı
// kural + servis / aynı exception grubu yeniden geldiğinde kendiliğinden aynı
// sınıfa düşer. Yaşam döngüsü değişmez; bildirim yalnız yönetici politikayı
// açtıysa susar (v0.10.1016) — açıklama hangi durumda olduğunu ekranda söyler.
// Viewer kararı GÖRÜR, düğmeleri görmez (sunucu da kapalı).
// İmzası olmayan satırda (olay) hiçbir şey çizilmez.
export function ProblemVerdictActions({ item, onDone }: {
  item: InboxItem;
  /** "Problem değil" satırı varsayılan görünümden çıkarır — çekmece kapanır. */
  onDone?: (next: ProblemVerdictKind | '') => void;
}) {
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const q = useProblemVerdicts();
  const set = useSetProblemVerdict();
  const sig = inboxSignature(item);
  const current = useMemo(() => (sig ? verdictIndex(q.data?.verdicts).get(sig) : undefined), [q.data, sig]);
  if (!sig) return null;

  const write = (verdict: ProblemVerdictKind | '') => {
    set.mutate(
      { signature: sig, verdict, label: item.title, kind: item.kind, service: item.service },
      { onSuccess: () => onDone?.(verdict) },
    );
  };
  const stateText = current?.verdict === 'real' ? 'Gerçek problem olarak işaretli'
    : current?.verdict === 'noise' ? 'Problem değil olarak işaretli'
    : 'Henüz işaretlenmedi';

  return (
    <div className="stack gap-2" style={{ marginTop: 14 }}>
      <div style={{ fontSize: 10.5, fontWeight: 700, letterSpacing: 0.4, textTransform: 'uppercase', color: 'var(--text3)' }}>
        Bu gerçek bir problem mi?
      </div>
      <div style={{ fontSize: 12, color: 'var(--text2)' }}>
        {stateText}{current?.by ? ` · ${current.by}` : ''}
      </div>
      {isEditor && (
        <div className="row gap-2 row-wrap">
          <Button size="sm" variant={current?.verdict === 'real' ? 'primary' : 'secondary'}
            loading={set.isPending} disabled={current?.verdict === 'real'} onClick={() => write('real')}>
            Gerçek problem
          </Button>
          <Button size="sm" variant={current?.verdict === 'noise' ? 'primary' : 'secondary'}
            loading={set.isPending} disabled={current?.verdict === 'noise'} onClick={() => write('noise')}>
            Problem değil
          </Button>
          {current && (
            <Button size="sm" variant="ghost" loading={set.isPending} onClick={() => write('')}>
              İşareti kaldır
            </Button>
          )}
        </div>
      )}
      <div className="field-hint">
        Karar bu satırın imzasına yazılır: aynı kural + servis (ya da aynı exception grubu) yeniden geldiğinde
        kendiliğinden aynı sınıfa düşer. “Problem değil” satırı varsayılan listeden çıkarır ve “Problem değil”
        görünümünde toplar; {verdictNotifyHint(sig, q.data?.policy)}
      </div>
      {set.isError && <div role="alert" className="field-error">Karar yazılamadı — yeniden deneyin.</div>}
    </div>
  );
}
