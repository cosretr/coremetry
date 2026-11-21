// TourOverlay.tsx — v0.10.1334 (rehberli tur, R1). Sunumsal örtü: metinler
// çevrilmiş gelir (TourProvider), örtü yalnız çizer ve klavyeyi yönetir.
//
//   • createPortal(document.body) — Modal.tsx deseni: ebeveynin overflow/
//     transform bağlamına hapsolmaz.
//   • TEK sabit halka hedef dikdörtgende; `box-shadow 0 0 0 100vmax
//     var(--backdrop)` spotlight + karartmanın ikisi (globals.css
//     .tour-ring). Perde tıklamaları yutar; hedef yokken perde karartır.
//   • Koç kartı role="dialog" aria-modal aria-labelledby; başlıkta "Adım N /
//     5" + ilerleme şeridi (kapalı adımlar kesik çizgili); altta Geri
//     (secondary) · Turu atla (ghost) · İleri/Bitir (primary — kartın TEK
//     birincili). Kartın içinde görünmez role=status aria-live=polite duyuru.
//   • useFocusTrap(cardRef) — Tab kartın içinde döner; ilk odak birincil
//     düğme; tur bitince odak açılıştaki elemana döner.
//   • Esc → useEscLayer (lib/escLayer, tek document dinleyicisi) → atla.
//     ←/→ kart kökünün onKeyDown'ı — global kısayol kaydı DEĞİL (tur
//     kapalıyken ok tuşları sayfalarındır).
//   • Kart yerleşimi lib/tipPlacement.placeTip (gap genişletmesi) — ikinci
//     bir yerleştirici yazılmadı.
//   • v0.10.1336 — `restoreFocus=false`: kapanışta odak iadesi ATLANIR (CoSRE
//     composer karesinde Bitir → odak composer'da kalır; TourProvider odaklar).
//     Hedef bulununca odak kart dışına kaçmışsa (ör. çekmece composer'ı
//     autoFocus / prefill rAF'ı ile aldı) bir kare sonra birincile geri
//     çekilir — kart aria-modal; Enter kazara soruyu GÖNDERMESİN.
//   • Esc sırası: Esc LIFO katman yığınının TEPESİNİ kapatır. Tur katmanı tur
//     başında, CoSRE çekmecesinin katmanı (Drawer useEscLayer) çekmece
//     açılınca itilir → ilk Esc çekmeceyi kapatır, ikinci Esc turu atlar.

import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Button } from '@/components/ui/Button';
import { useFocusTrap } from '@/components/ui/useFocusTrap';
import { useEscLayer } from '@/lib/escLayer';
import { placeTip, type TipRect } from '@/lib/tipPlacement';
import type { IndicatorSeg } from './engine';
import type { TourPlacement } from './steps';
import { COACH_GAP, ringRect } from './geometry';

export interface TourOverlayProps {
  stepKey: string;
  stepLabel: string;
  progressLabel: string;
  segments: IndicatorSeg[];
  segmentTitle: (seg: IndicatorSeg) => string;
  title: string;
  body: string;
  notes: string[];
  children?: ReactNode;
  rect: TipRect | null;
  placement: TourPlacement;
  dialogLabel: string;
  labels: { back: string; next: string; finish: string; skip: string };
  canBack: boolean;
  isLast: boolean;
  /** v0.10.1336 — false: kapanışta açılıştaki elemana odak iadesi yapılmaz. Varsayılan true. */
  restoreFocus?: boolean;
  onBack: () => void;
  onNext: () => void;
  onSkip: () => void;
}

export function TourOverlay(p: TourOverlayProps) {
  const cardRef = useRef<HTMLDivElement>(null);
  const primaryRef = useRef<HTMLButtonElement>(null);
  const titleId = useId();
  const bodyId = useId();
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  const restoreRef = useRef(p.restoreFocus !== false);
  restoreRef.current = p.restoreFocus !== false;

  useFocusTrap(cardRef, true);
  useEscLayer(true, p.onSkip);

  // İlk odak birincil düğmede; kapanınca açılıştaki elemana iade
  // (v0.10.1336: restoreFocus=false ise iade yok — son render'ın değeri).
  useEffect(() => {
    const prev = document.activeElement;
    primaryRef.current?.focus({ preventScroll: true });
    return () => {
      if (!restoreRef.current) return;
      if (prev instanceof HTMLElement && prev.isConnected) prev.focus({ preventScroll: true });
    };
  }, []);

  // Adım/kare değişince ya da hedef belirince odak kartta kalsın (önceki düğme
  // devre dışı kalmış, ya da hedefin sahibi odağı almış olabilir — v0.10.1336:
  // bir kare SONRA da bakılır; prefill'in rAF odağı ondan önce sıraya girer).
  const hasRect = !!p.rect;
  useEffect(() => {
    const pull = () => {
      const card = cardRef.current;
      if (card && !card.contains(document.activeElement)) primaryRef.current?.focus({ preventScroll: true });
    };
    pull();
    if (!hasRect || typeof requestAnimationFrame !== 'function') return;
    const id = requestAnimationFrame(pull);
    return () => { if (typeof cancelAnimationFrame === 'function') cancelAnimationFrame(id); };
  }, [p.stepKey, hasRect]);

  const ring = p.rect ? ringRect(p.rect) : null;
  const anchored = !!ring && p.placement !== 'center';
  useLayoutEffect(() => {
    if (!anchored || !ring) { setPos(null); return; }
    const card = cardRef.current;
    const size = { width: card?.offsetWidth ?? 0, height: card?.offsetHeight ?? 0 };
    const vp = { width: window.innerWidth, height: window.innerHeight };
    const t = placeTip(ring, size, vp, p.placement === 'top' ? 'top' : 'bottom', COACH_GAP);
    setPos(cur => (cur && cur.left === t.left && cur.top === t.top ? cur : { left: t.left, top: t.top }));
  }, [anchored, ring?.left, ring?.top, ring?.width, ring?.height, p.placement, p.stepKey]); // eslint-disable-line react-hooks/exhaustive-deps -- ring her render yeni nesne; sayısal alanları izlenir

  const onKeyDown = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.altKey || e.ctrlKey || e.metaKey) return;
    if (e.key === 'ArrowRight') { e.preventDefault(); p.onNext(); }
    else if (e.key === 'ArrowLeft') { e.preventDefault(); if (p.canBack) p.onBack(); }
  };

  return createPortal(
    <>
      <div className={`tour-backdrop${ring ? '' : ' is-dim'}`} aria-hidden="true"
        onMouseDown={e => e.preventDefault()} />
      {ring && (
        <div className="tour-ring" aria-hidden="true" data-testid="tour-ring"
          style={{ left: ring.left, top: ring.top, width: ring.width, height: ring.height }} />
      )}
      <div ref={cardRef}
        className={`tour-card${pos ? '' : ' is-center'}`}
        style={pos ? { left: pos.left, top: pos.top } : undefined}
        role="dialog" aria-modal="true" aria-roledescription={p.dialogLabel}
        aria-labelledby={titleId} aria-describedby={bodyId}
        tabIndex={-1} onKeyDown={onKeyDown}>
        <div className="tour-card__head">
          <span className="tour-card__step">{p.stepLabel}</span>
          <div className="tour-progress" role="img" aria-label={p.progressLabel}>
            {p.segments.map(seg => (
              <span key={seg.id} className={`tour-progress__seg is-${seg.kind}`} title={p.segmentTitle(seg)} />
            ))}
          </div>
          <h2 id={titleId} className="tour-card__title">{p.title}</h2>
        </div>
        <p id={bodyId} className="tour-card__body">{p.body}</p>
        {p.children}
        {p.notes.map(n => <p key={n} className="tour-card__note">{n}</p>)}
        <div className="tour-card__foot">
          <Button variant="secondary" size="sm" onClick={p.onBack} disabled={!p.canBack}>{p.labels.back}</Button>
          <Button variant="ghost" size="sm" onClick={p.onSkip}>{p.labels.skip}</Button>
          <Button ref={primaryRef} variant="primary" size="sm" className="tour-card__primary" onClick={p.onNext}>
            {p.isLast ? p.labels.finish : p.labels.next}
          </Button>
        </div>
        <div className="sr-only" role="status" aria-live="polite">{`${p.stepLabel}: ${p.title}`}</div>
      </div>
    </>,
    document.body,
  );
}
