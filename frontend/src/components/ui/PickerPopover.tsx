import { useEffect, useLayoutEffect, useRef, type ReactNode, type RefObject } from 'react';
import { createPortal } from 'react-dom';
import { placePickerPop, pickerOptionId } from '@/lib/pickerPopover';

// PickerPopover — v0.10.1089 (operatör, prod, Services "Filter services…" ve
// Traces "Filter by service…": "Search daha iyi bir deneyim sunabilir. Şu an
// sanki geçici bir menü açılmış gibi hissiyat var, iframe içinde geliyor.")
//
// Seçici ailesinin (ServicePicker / OperationPicker / MetricNamePicker ve
// Combobox üstüne kurulu her alan) TEK açılır listesi. Eskiden Combobox
// listeyi girdinin yanına `position: absolute` çiziyordu: kart/tablo/yapışkan
// barın `overflow`u onu kesiyor, uzun adlar yatay kaydırma çubuğu açıyor,
// satırlar çıplak, klavye satırı belirsizdi — operatörün "iframe" dediği.
//
// Neden `ui/Popover` DEĞİL: Popover odağı İÇİNE alır (menü/diyalog). Seçicide
// odak GİRDİDE kalmak zorunda (operatör yazmaya devam ediyor); satırlar
// `aria-activedescendant` ile işaret edilir (CommandPalette deseni). Yerleşim
// de farklı: ⋯ menüsü sağ kenara, seçici listesi sol kenara hizalanır ve
// girdiyi asla örtmez (lib/pickerPopover.ts).
//
//   • body'ye PORTAL, `position: fixed` (SparkReadout v0.10.1059 deseni) —
//     tablo/kart `overflow: hidden`ı kırpamaz. Konum her çizimde ve
//     kaydırma/boyutta yeniden hesaplanır; alta sığmazsa üste çevrilir,
//     yatayda viewport'a kıstırılır, yükseklik o tarafın boşluğuna iner.
//   • Çapa bir diyaloğun (Modal / Drawer, `[role="dialog"]`) içindeyse liste
//     onun üstündeki rung'a çıkar (`data-layer="over"`); sayfadaysa
//     `--z-popover`. Portal edilen bir liste aksi hâlde çekmecenin ALTINDA
//     kalırdı.
//   • Yüzeyde mousedown: preventDefault (odak girdide kalır, kaydırma
//     çubuğuna basmak bile blur üretmez) + stopPropagation (sayfadaki
//     "dışarı tık" dinleyicileri — PageControls dar paneli gibi — portal
//     edilmiş bir satır tıklamasını dışarı sanmasın).
//   • Görünüm: kart yüzeyi (--bg, --border, --radius, --shadow-pop), başlıkta
//     "N sonuç" ya da durum, isteğe bağlı "Son kullanılan" grubu, satırlarda
//     ad (sığmazsa üç nokta + tam ad `title`da) ve soluk ek etiket. Yatay
//     kaydırma YOK; dikey kaydırma yalnız gerektiğinde, ince.

export interface PickerItem {
  value: string;
  /** Satır sonunda soluk ek etiket (birim/tip, runtime · span sayısı). */
  meta?: string;
  /** 'recent' = "Son kullanılan" grubu (listenin başında). */
  group?: 'recent';
}

export type PickerStatus = 'loading' | 'error';

export interface PickerPopoverProps {
  anchorRef: RefObject<HTMLElement | null>;
  /** listbox id'si; input `aria-controls` ile buna bağlanır. */
  listId: string;
  /** Düz sıra: önce 'recent' grubundakiler, sonra sonuçlar. */
  items: PickerItem[];
  /** Klavye/hover ile işaretli satır (-1 = yok). */
  highlight: number;
  onHighlight: (i: number) => void;
  onPick: (value: string) => void;
  /** Eşleşen parçayı vurgulamak için yazılan metin. */
  query: string;
  /** Alanın şu anki değeri — o satır işaretlenir. */
  current?: string;
  /** Başlıktaki "N sonuç" (sunucu toplamı ya da süzülen sayı). */
  count: number;
  status?: PickerStatus;
  /** Liste boşken gösterilen ek ipucu ("Enter yazılanı kullanır"). */
  emptyHint?: string;
  /** Listenin dibinde tıklanamaz not (ör. "… +N more — refine search"). */
  footer?: ReactNode;
  ariaLabel?: string;
}

export function PickerPopover({
  anchorRef, listId, items, highlight, onHighlight, onPick, query, current,
  count, status, emptyHint, footer, ariaLabel,
}: PickerPopoverProps) {
  const popRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  // Yerleşim doğrudan DOM'a (Popover/SparkReadout deseni): konum için ikinci
  // bir React çizimi yok, boyamadan önce oturur. Önce yükseklik sınırı
  // sıfırlanır ki ölçülen boy bir önceki tarafın kısıtı olmasın.
  const place = () => {
    const pop = popRef.current;
    const a = anchorRef.current;
    if (!pop || !a) return;
    const r = a.getBoundingClientRect();
    pop.style.minWidth = `${Math.round(r.width)}px`;
    pop.style.maxHeight = '';
    const pos = placePickerPop(
      { left: r.left, top: r.top, width: r.width, height: r.height },
      { width: pop.offsetWidth, height: pop.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
    );
    pop.style.left = `${pos.left}px`;
    pop.style.top = `${pos.top}px`;
    if (pop.offsetHeight > pos.maxHeight) pop.style.maxHeight = `${pos.maxHeight}px`;
    pop.dataset.side = pos.side;
    if (a.closest('[role="dialog"]')) pop.dataset.layer = 'over';
    else delete pop.dataset.layer;
  };
  const placeRef = useRef(place);
  placeRef.current = place;

  useLayoutEffect(() => { placeRef.current(); });

  useEffect(() => {
    let raf = 0;
    const onMove = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => { raf = 0; placeRef.current(); });
    };
    window.addEventListener('resize', onMove);
    document.addEventListener('scroll', onMove, true);
    return () => {
      if (raf) cancelAnimationFrame(raf);
      window.removeEventListener('resize', onMove);
      document.removeEventListener('scroll', onMove, true);
    };
  }, []);

  // Klavyeyle görünür alanın dışına inilince satırı görünür kıl.
  useEffect(() => {
    if (highlight < 0) return;
    const row = listRef.current?.querySelector<HTMLElement>(`[data-i="${highlight}"]`);
    row?.scrollIntoView?.({ block: 'nearest' });
  }, [highlight]);

  const recentCount = items.filter(it => it.group === 'recent').length;
  const head = status === 'loading' ? 'aranıyor…'
    : status === 'error' ? 'arama başarısız'
    : `${count.toLocaleString('tr-TR')} sonuç`;

  const row = (it: PickerItem, i: number) => (
    <div key={`${it.group ?? 'r'}:${it.value}`}
      id={pickerOptionId(listId, i)}
      role="option"
      aria-selected={i === highlight}
      data-i={i}
      className={`pick-opt${i === highlight ? ' is-on' : ''}${it.value === current ? ' is-cur' : ''}`}
      title={it.meta ? `${it.value} · ${it.meta}` : it.value}
      onMouseDown={e => { e.preventDefault(); onPick(it.value); }}
      onMouseEnter={() => onHighlight(i)}>
      <span className="pick-name">{renderMatch(it.value, query)}</span>
      {it.meta && <span className="pick-meta">{it.meta}</span>}
    </div>
  );

  return createPortal(
    <div ref={popRef} className="pick-pop"
      onMouseDown={e => { e.preventDefault(); e.stopPropagation(); }}>
      <div className="pick-head" aria-live="polite">{head}</div>
      <div ref={listRef} id={listId} className="pick-list" role="listbox" aria-label={ariaLabel ?? 'Sonuçlar'}>
        {recentCount > 0 && (
          <div role="group" aria-labelledby={`${listId}-recent`}>
            <div id={`${listId}-recent`} className="pick-group" role="presentation">Son kullanılan</div>
            {items.slice(0, recentCount).map((it, i) => row(it, i))}
          </div>
        )}
        {recentCount > 0 && items.length > recentCount && (
          <div className="pick-group" role="presentation">Tümü</div>
        )}
        {items.slice(recentCount).map((it, i) => row(it, i + recentCount))}
        {items.length === 0 && status === 'error' && (
          <div className="pick-state is-err" role="presentation">Arama başarısız — yeniden deneyin</div>
        )}
        {items.length === 0 && status !== 'error' && status !== 'loading' && (
          <div className="pick-state" role="presentation">
            eşleşme yok{emptyHint && <span className="pick-state-hint"> — {emptyHint}</span>}
          </div>
        )}
      </div>
      {footer && items.length > 0 && <div className="pick-foot">{footer}</div>}
    </div>,
    document.body,
  );
}

// renderMatch — eşleşen ilk parçayı (büyük/küçük harf duyarsız) vurgular ki
// operatör satırın NEDEN listede olduğunu görsün. Joker karakterli sorguda
// (`pay*`) düz parça bulunmaz, satır sade kalır.
function renderMatch(option: string, query: string): ReactNode {
  const q = query.trim();
  if (!q) return option;
  const i = option.toLowerCase().indexOf(q.toLowerCase());
  if (i < 0) return option;
  return (
    <>
      {option.slice(0, i)}
      <b className="pick-hit">{option.slice(i, i + q.length)}</b>
      {option.slice(i + q.length)}
    </>
  );
}
