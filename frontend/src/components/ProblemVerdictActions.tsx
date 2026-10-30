import { useAuth } from '@/components/AuthProvider';
import { Button } from '@/components/ui';
import { useProblemVerdicts, useSetProblemVerdict } from '@/lib/queries';
import { inboxVerdictSubject, noiseReasonLabel, verdictCompactHint, verdictIndex, verdictNotifyHint, type VerdictSubject } from '@/lib/problemVerdict';
import { NoiseReasonButton, type NoiseReasonChoice } from '@/components/NoiseReasonButton'; // v0.10.1531 (R3)
import type { InboxItem, ProblemVerdictKind } from '@/lib/types';
import { verdictInput, type VerdictRowContext } from '@/components/useVerdictRowContext'; // v0.10.1626
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
// v0.10.1318 (K3) — işaretli karar bir DURUM: `variant="secondary" active`
// (accent tint + aria-pressed), birincil dolgu değil. Seçili seçenek yine
// disabled (aynı kararı yeniden yazmaz) ama `button.is-active:disabled`
// onu %40'a soldurmaz — seçim okunur kalır.
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
  const isAdmin = user?.role === 'admin';
  const q = useProblemVerdicts();
  const set = useSetProblemVerdict();
  const subj = item ? inboxVerdictSubject(item) : subject;
  const sig = subj?.signature ?? null;
  const current = useMemo(() => (sig ? verdictIndex(q.data?.verdicts).get(sig) : undefined), [q.data, sig]);
  if (!sig || !subj) return null;

  // v0.10.1531 (R3) — "problem değil" gerekçeyle (NoiseReasonButton); işaret
  // anındaki öncelik / büyüklük sunucudaki baypas kıyası için gider.
  const write = (verdict: ProblemVerdictKind | '', noise?: NoiseReasonChoice) => {
    set.mutate(verdictInput(subj, verdict, noise), { onSuccess: () => onDone?.(verdict) });
  };
  const noiseDetail = current?.verdict === 'noise'
    ? [noiseReasonLabel(current.reason), current.permanent ? 'kalıcı' : ''].filter(Boolean).join(' · ')
    : '';
  const stateText = current?.verdict === 'real' ? 'Gerçek problem olarak işaretli'
    : current?.verdict === 'noise' ? `Problem değil olarak işaretli${noiseDetail ? ` (${noiseDetail})` : ''}${current.note ? ` — ${current.note}` : ''}`
    : 'Henüz işaretlenmedi';

  if (compact) {
    // Durum kısa: düğmenin kendisi seçili görünür; metin yalnız kim/ne
    // olduğunu söyler (viewer düğme görmediği için durumu buradan okur).
    const shortState = current?.verdict === 'real' ? 'gerçek problem'
      : current?.verdict === 'noise' ? `problem değil${noiseDetail ? ` · ${noiseDetail}` : ''}`
      : 'işaretlenmedi';
    return (
      <span className="pv-compact">
        <span className="pv-compact__q">Gerçek problem mi?</span>
        {(!isEditor || current) && (
          <span className="pv-compact__state">{shortState}{current?.by ? ` · ${current.by}` : ''}</span>
        )}
        {isEditor && (
          <>
            <Button size="sm" variant="secondary" active={current?.verdict === 'real'}
              loading={set.isPending} disabled={current?.verdict === 'real'} onClick={() => write('real')}>
              Gerçek problem
            </Button>
            <NoiseReasonButton label="Problem değil" isAdmin={isAdmin}
              active={current?.verdict === 'noise'} loading={set.isPending} disabled={current?.verdict === 'noise'}
              onConfirm={c => write('noise', c)} />
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
          <Button size="sm" variant="secondary" active={current?.verdict === 'real'}
            loading={set.isPending} disabled={current?.verdict === 'real'} onClick={() => write('real')}>
            Gerçek problem
          </Button>
          <NoiseReasonButton label="Problem değil" isAdmin={isAdmin}
            active={current?.verdict === 'noise'} loading={set.isPending} disabled={current?.verdict === 'noise'}
            onConfirm={c => write('noise', c)} />
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

// ProblemVerdictRow — v0.10.1529 (Problem standardı R2, D1) liste satırının
// Karar hücresi: yalnız iki düğme (seçili olan `active`). Satır hover/focus'ta
// görünür (globals.css `.pv-row`); işaretli satırda her zaman. Viewer yalnız
// işaretli durumu okur. Satır tıkı (detay açma) burada tetiklenmez.
//
// v0.10.1626 (perf denetimi §10 i) — eskiden ProblemVerdictActions `row` kipi:
// her satır kendi karar sorgusu + mutation örneğini açıp verdictIndex'i satır
// başına yeniden kuruyordu (300 satır = 300 harita). Artık dizin, sorgu ve
// mutation tablo düzeyinde bir kez (useVerdictRowContext); satır yalnız bakar.
export function ProblemVerdictRow({ item, ctx }: { item: InboxItem; ctx: VerdictRowContext }) {
  const subj = inboxVerdictSubject(item);
  if (!subj) return null;
  const sig = subj.signature;
  const current = ctx.index.get(sig);
  const pending = ctx.pendingSig === sig;
  const write = (verdict: ProblemVerdictKind | '', noise?: NoiseReasonChoice) => ctx.write(verdictInput(subj, verdict, noise));
  const noiseDetail = current?.verdict === 'noise'
    ? [noiseReasonLabel(current.reason), current.permanent ? 'kalıcı' : ''].filter(Boolean).join(' · ')
    : '';
  const label = item.title ?? subj.label;
  return (
    <span className={current ? 'pv-row is-set' : 'pv-row'} onClick={e => e.stopPropagation()}>
      {ctx.isEditor ? (
        <>
          <Button size="sm" variant="secondary" active={current?.verdict === 'real'}
            aria-label={`Gerçek problem: ${label}`}
            loading={pending} disabled={current?.verdict === 'real'} onClick={() => write('real')}>
            Gerçek problem
          </Button>
          <NoiseReasonButton label="Problem değil" ariaLabel={`Problem değil: ${label}`} isAdmin={ctx.isAdmin}
            active={current?.verdict === 'noise'} loading={pending} disabled={current?.verdict === 'noise'}
            onConfirm={c => write('noise', c)} />
          {noiseDetail && <span className="pv-compact__state" title={current?.note || undefined}>{noiseDetail}</span>}
        </>
      ) : current ? (
        <span className="pv-compact__state">{current.verdict === 'real' ? 'gerçek problem' : `problem değil${noiseDetail ? ` · ${noiseDetail}` : ''}`}</span>
      ) : null}
      {ctx.errorSig === sig && <span role="alert" className="field-error">Yazılamadı</span>}
    </span>
  );
}
