import {
  cloneElement, isValidElement, useCallback, useEffect, useId, useLayoutEffect, useRef, useState,
  type FocusEvent, type PointerEvent, type ReactElement, type ReactNode,
} from 'react';
import { useEscLayer } from '@/lib/escLayer';
import { placeTip, type TipPos, type TipSide } from '@/lib/tipPlacement';

// Tooltip — v0.10.919 (buton bütünlüğü, Seçenek B; operatör onayı
// 2026-09-25). Depoda kanonik bir tooltip YOKTU (tasarım sistemi skill'i
// K7: "7 DOM uygulaması, kanonik yok"); butonlarda ~320 yerel `title=`
// var — gecikmesi tarayıcıya ait, klavyeyle hiç açılmaz, dokunmatikte
// yok, stili temaya uymaz.
//
// ── NEDEN PORTAL DEĞİL ──────────────────────────────────────────────────
// `--z-tooltip` (30) nav'ın (40), çekmecenin (61) ve modalın (100)
// ALTINDA — zLayers.test.ts bunu bilerek çiviliyor ("ipucu sayfa içeriğini
// süsler"). body'ye portal edilen bir ipucu çekmece/modal içindeki
// butonlarda ARKADA kalırdı; basamağı yükseltmek kapıyı bozar. Bunun
// yerine ipucu tetiğin KARDEŞİ olarak ağaçta, `position: fixed` çizilir:
//   • `#main`/`#content`in `overflow: auto` kırpmasından kaçar (fixed),
//   • tetiğin yığın bağlamını devralır → çekmece/modal içeriğinin üstünde.
// (Portal ayrıca dropdown panellerindeki (z 50) butonlarda da ipucunu
// panelin altına gömerdi; ağaçta kalan kutu o bağlamı da devralır.)
//
// BİLİNEN SINIRLAR (v0.10.919 inceleme turu, Chromium'da ölçüldü):
//   • `fixed`i yakalayan atalar: `transform`, `filter`, `will-change:
//     transform`, `contain: paint|layout|strict` (VirtualList `contain:
//     strict`), tablo-DIŞI elemanda `content-visibility: auto` (`.wf-row`).
//     Böyle bir atanın içinde ipucu kayar ve kırpılır — oralarda kullanmayın
//     (ya da ipucunu atanın dışındaki bir tetiğe koyun).
//   • Kutu tetiğin SONRAKİ KARDEŞİ olur: açıkken `:last-child`, `+`, `~`
//     gibi kardeş seçicileri değişir. Grup CSS'i `:last-of-type` kullanmalı
//     (`.btn-group` öyle). Yapışkan tablo hücreleri (z-index'li sticky)
//     kendi yığın bağlamını kurduğu için ipucu açıkken hücre globals.css'te
//     `:has(.tip)` ile `--z-tooltip`e yükseltilir.
//
// ── DAVRANIŞ (WCAG 1.4.13) ─────────────────────────────────────────────
//   • Fare: `delay` ms sonra açılır; ayrılınca 100 ms tolerans, ipucunun
//     ÜSTÜNE gelinirse açık kalır (hoverable).
//   • Klavye: `:focus-visible` odakta HEMEN açılır; blur'da kapanır.
//     İki tetik AYRI izlenir: fare ayrılsa da odak sürüyorsa açık kalır
//     (1.4.13 "persistent"), odak gitse de fare üstündeyse açık kalır.
//   • Dokunmatik/kalem: açılmaz — erişilebilir ad `aria-label`de kalır.
//   • Esc: `useEscLayer` (tek belge dinleyicisi, LIFO yığın) — dosyada
//     keydown dinleyicisi YOK (escLayer.test.ts bunu yasaklıyor).
//   • Kaydırma/yeniden boyut: kutu YENİDEN YERLEŞİR; çapa görünür alandan
//     çıkınca kapanır. (İlk sürüm her kaydırmada kapatıyordu: Tab ile
//     ekran dışındaki bir butona gelince tarayıcının odak kaydırması
//     ipucunu aynı karede kapatıyordu — inceleme turunda ölçüldü.)
//   • İçerik açıkken değişirse ("Kopyala" → "Kopyalandı") yeniden ölçülür.
//   • Kutuya tıklamak satıra/kaba KABARCIKLANMAZ: kutu tetiğin DOM kardeşi,
//     yani tıklanabilir satırın torunu; üstü başka satırı örterken o
//     tıklama yanlış satırın çekmecesini açıyordu.
//   • `disabled` butonlar işaretçi olayı almaz → ipucu açılmaz. Devre dışı
//     sebebini göstermek için `title` kalır ya da bir sarmalayıcı kullanın.
//
// ── ARIA ────────────────────────────────────────────────────────────────
// Kutu `role="tooltip"` + `useId`; tetiğe `aria-describedby` enjekte
// edilir (Field.tsx deseni). İçerik tetiğin `aria-label`ine EŞİTSE
// describedby basılmaz (ekran okuyucu aynı cümleyi iki kez okumasın).
// Tetikteki `title` düşürülür: yerel ipucu ile bu ikisi üst üste binerdi.
//
// ÇOCUK: tek bir eleman; DOM olay prop'larını ve `aria-describedby`ı
// kendi `<button>`una geçirmeli (Button, IconButton, Chip, LinkButton
// hepsi `...rest` geçiriyor). Ref gerekmez: çapa olay anında
// `currentTarget`tan alınır.

export interface TooltipProps {
  /** İpucu metni/içeriği. Kısa tutun; etkileşimli içerik koymayın. */
  content: ReactNode;
  children: ReactElement<TooltipTriggerProps>;
  /** Tercih edilen taraf; sığmazsa karşıya çevrilir. */
  side?: TipSide;
  /** Fare ile açılma gecikmesi (ms). Klavye odağı gecikmesiz açar. */
  delay?: number;
}

interface TooltipTriggerProps {
  onPointerEnter?: (e: PointerEvent<HTMLElement>) => void;
  onPointerLeave?: (e: PointerEvent<HTMLElement>) => void;
  onFocus?: (e: FocusEvent<HTMLElement>) => void;
  onBlur?: (e: FocusEvent<HTMLElement>) => void;
  'aria-describedby'?: string;
  'aria-label'?: string;
  title?: string;
}

/** Fare ile açılma gecikmesi varsayılanı (ms). */
export const TOOLTIP_DELAY = 500;
/** Ayrıldıktan sonra kapanma toleransı (ms) — ipucuna geçebilmek için. */
export const TOOLTIP_CLOSE_GRACE = 100;

function isFocusVisible(el: Element): boolean {
  try {
    return el.matches(':focus-visible');
  } catch {
    // Seçiciyi tanımayan ortam (eski tarayıcı / test DOM'u): odak klavyeden
    // gelmiş say — ipucunu göstermek, saklamaktan daha güvenli hata.
    return true;
  }
}

export function Tooltip({ content, children, side = 'top', delay = TOOLTIP_DELAY }: TooltipProps) {
  const id = useId();
  const [open, setOpen] = useState(false);
  const [pos, setPos] = useState<TipPos | null>(null);
  const anchorRef = useRef<HTMLElement | null>(null);
  const tipRef = useRef<HTMLSpanElement | null>(null);
  const openTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // İki tetik ayrı: kapanma yalnız İKİSİ DE gidince (WCAG 1.4.13).
  const hovered = useRef(false);
  const focused = useRef(false);

  const clearTimers = useCallback(() => {
    if (openTimer.current) { clearTimeout(openTimer.current); openTimer.current = null; }
    if (closeTimer.current) { clearTimeout(closeTimer.current); closeTimer.current = null; }
  }, []);
  const show = useCallback(() => { clearTimers(); setOpen(true); }, [clearTimers]);
  const hide = useCallback(() => { clearTimers(); setOpen(false); setPos(null); }, [clearTimers]);
  const hideSoon = useCallback(() => {
    if (focused.current) return; // klavye tetiği sürüyor — blur/Esc kapatır
    if (openTimer.current) { clearTimeout(openTimer.current); openTimer.current = null; }
    if (closeTimer.current) clearTimeout(closeTimer.current);
    closeTimer.current = setTimeout(hide, TOOLTIP_CLOSE_GRACE);
  }, [hide]);

  // Mevcut kutu boyutuyla yeniden yerleştir; çapa görünür alandan çıktıysa
  // ya da DOM'dan düştüyse kapat.
  const place = useCallback(() => {
    const a = anchorRef.current;
    const t = tipRef.current;
    if (!a || !t) return;
    if (!a.isConnected) { hide(); return; }
    const r = a.getBoundingClientRect();
    const vw = window.innerWidth, vh = window.innerHeight;
    if (r.bottom < 0 || r.top > vh || r.right < 0 || r.left > vw) { hide(); return; }
    setPos(placeTip(
      { left: r.left, top: r.top, width: r.width, height: r.height },
      { width: t.offsetWidth, height: t.offsetHeight },
      { width: vw, height: vh },
      side,
    ));
  }, [hide, side]);

  useEffect(() => clearTimers, [clearTimers]);
  useEscLayer(open, hide);

  // Açıkken kaydırma/yeniden boyut YENİDEN YERLEŞTİRİR (kare başına bir
  // kez; capture: iç kaydırıcılar da). Kapatmak yerine izlemek: Tab'ın
  // tetiklediği odak kaydırması ipucunu kapatmamalı.
  useEffect(() => {
    if (!open) return;
    let raf = 0;
    const onMove = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => { raf = 0; place(); });
    };
    window.addEventListener('scroll', onMove, true);
    window.addEventListener('resize', onMove);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      window.removeEventListener('scroll', onMove, true);
      window.removeEventListener('resize', onMove);
    };
  }, [open, place]);

  // İçerik ya da taraf açıkken değişirse temiz ölçüm: `is-measuring`e dön
  // (kıstırılmış eski konum genişliği daraltmasın).
  useLayoutEffect(() => {
    setPos(null);
  }, [content, side]);

  // Ölç ve yerleştir: kutu önce görünmez (`is-measuring`) çizilir, boyutu
  // okunur, sonra tek seferde konumlanır.
  useLayoutEffect(() => {
    if (open && !pos) place();
  }, [open, pos, place]);

  if (!isValidElement<TooltipTriggerProps>(children)) return children;
  const cp = children.props;
  const describes = !(typeof content === 'string' && content === cp['aria-label']);
  const describedBy = open && describes
    ? [cp['aria-describedby'], id].filter(Boolean).join(' ')
    : cp['aria-describedby'];

  const classes = [
    'tip',
    pos ? '' : 'is-measuring',
  ].filter(Boolean).join(' ');

  const trigger = cloneElement(children, {
    title: undefined,
    'aria-describedby': describedBy,
    onPointerEnter: (e: PointerEvent<HTMLElement>) => {
      cp.onPointerEnter?.(e);
      if (e.pointerType !== 'mouse') return;
      hovered.current = true;
      anchorRef.current = e.currentTarget;
      if (closeTimer.current) { clearTimeout(closeTimer.current); closeTimer.current = null; }
      if (open || openTimer.current) return;
      openTimer.current = setTimeout(show, delay);
    },
    onPointerLeave: (e: PointerEvent<HTMLElement>) => {
      cp.onPointerLeave?.(e);
      if (e.pointerType !== 'mouse') return;
      hovered.current = false;
      hideSoon();
    },
    onFocus: (e: FocusEvent<HTMLElement>) => {
      cp.onFocus?.(e);
      if (!isFocusVisible(e.currentTarget)) return;
      focused.current = true;
      anchorRef.current = e.currentTarget;
      show();
    },
    onBlur: (e: FocusEvent<HTMLElement>) => {
      cp.onBlur?.(e);
      focused.current = false;
      if (hovered.current) return; // fare hâlâ üstünde — ayrılınca kapanır
      hide();
    },
  });

  return (
    <>
      {trigger}
      {open && (
        <span
          ref={tipRef}
          id={id}
          role="tooltip"
          className={classes}
          data-side={pos?.side}
          style={pos ? { left: pos.left, top: pos.top } : undefined}
          onClick={e => e.stopPropagation()}
          onPointerEnter={() => {
            hovered.current = true;
            if (closeTimer.current) { clearTimeout(closeTimer.current); closeTimer.current = null; }
          }}
          onPointerLeave={() => { hovered.current = false; hideSoon(); }}>
          {content}
        </span>
      )}
    </>
  );
}
