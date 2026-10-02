import { useAuth } from '@/components/AuthProvider';
import { Button } from '@/components/ui';
import { useProblemVerdicts, useSetProblemVerdict } from '@/lib/queries';
import { inboxVerdictSubject, verdictCompactHint, verdictIndex, verdictNotifyHint, type VerdictSubject } from '@/lib/problemVerdict';
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
//
// v0.10.1032 (operatör: "Anomali ve alert rule'lara girdiğimde drawer çıkıyor.
// Exception gibi detay gözükmüyor.") — alarm kuralı ve anomali satırları artık
// TAM SAYFA detay açıyor; çekmece atlanınca bu düğmeler kaybolmasın diye bileşen
// bir InboxItem OLMADAN da sürülebilir: `subject` (imza + üst veri) doğrudan
// verilir (lib/problemVerdict problemVerdictSubject / exceptionVerdictSubject /
// anomalyVerdictSubject). Çekmece eskisi gibi `item` verir.
type Props = {
  /** "Problem değil" satırı varsayılan görünümden çıkarır — çağıran kapanır / geri döner. */
  onDone?: (next: ProblemVerdictKind | '') => void;
  /** Üst boşluksuz (detay sayfasının Triyaj bölümü kendi kabını çiziyor). */
  flush?: boolean;
  /** v0.10.1032 (operatör: "Çok detay verince daha anlaşılır olmuyor") — TEK
   *  satır: soru + durum + iki düğme (+ işareti kaldır) + tek kısa cümle. Üç
   *  detay sayfası bunu kullanır; triyaj çekmecesi uzun biçimi korur. */
  compact?: boolean;
} & ({ item: InboxItem; subject?: undefined } | { subject: VerdictSubject | null; item?: undefined });

export function ProblemVerdictActions({ item, subject, onDone, flush, compact }: Props) {
  const { user } = useAuth();
  const isEditor = user?.role === 'admin' || user?.role === 'editor';
  const q = useProblemVerdicts();
  const set = useSetProblemVerdict();
  const subj = item ? inboxVerdictSubject(item) : subject;
  const sig = subj?.signature ?? null;
  const current = useMemo(() => (sig ? verdictIndex(q.data?.verdicts).get(sig) : undefined), [q.data, sig]);
  if (!sig || !subj) return null;

  const write = (verdict: ProblemVerdictKind | '') => {
    set.mutate(
      { signature: sig, verdict, label: subj.label, kind: subj.kind, service: subj.service },
      { onSuccess: () => onDone?.(verdict) },
    );
  };
  const stateText = current?.verdict === 'real' ? 'Gerçek problem olarak işaretli'
    : current?.verdict === 'noise' ? 'Problem değil olarak işaretli'
    : 'Henüz işaretlenmedi';

  if (compact) {
    // Durum kısa: düğmenin kendisi seçili görünür; metin yalnız kim/ne
    // olduğunu söyler (viewer düğme görmediği için durumu buradan okur).
    const shortState = current?.verdict === 'real' ? 'gerçek problem'
      : current?.verdict === 'noise' ? 'problem değil'
      : 'işaretlenmedi';
    return (
      <span className="pv-compact">
        <span className="pv-compact__q">Gerçek problem mi?</span>
        {(!isEditor || current) && (
          <span className="pv-compact__state">{shortState}{current?.by ? ` · ${current.by}` : ''}</span>
        )}
        {isEditor && (
          <>
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
          </>
        )}
        <span className="pv-compact__hint">{verdictCompactHint(sig, q.data?.policy)}</span>
        {set.isError && <span role="alert" className="field-error">Karar yazılamadı — yeniden deneyin.</span>}
      </span>
    );
  }

  return (
    <div className="stack gap-2" style={{ marginTop: flush ? 0 : 14 }}>
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
