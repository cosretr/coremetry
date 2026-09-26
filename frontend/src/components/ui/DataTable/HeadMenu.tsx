import {
  useCallback, useId, useLayoutEffect, useRef, useState,
  type KeyboardEvent as ReactKeyboardEvent, type SyntheticEvent,
} from 'react';
import { IconButton } from '../IconButton';
import { MenuItem } from '../Menu';
import { useEscLayer } from '@/lib/escLayer';
import { useOutsideClose } from '@/lib/useOutsideClose';
import { placeMenu } from './menuPlacement';
import type { DataTable } from './DataTable';

// DataTableHeadMenu — v0.10.939 (tablo standardı S8, operatör cevabı:
// "Kolonları sıfırla başlık satırının ⋯ menüsünde").
//
// Önceden sıfırlama sayfa başına bir `ResetLayoutButton`du: 123 tablonun
// 18'inde, her sayfada başka bir yerde (araç çubuğu, dipnot, bölüm başlığı)
// ve kalıcı genişlik yokken GİZLİ. Kalan ~105 tabloda sürüklenmiş bir
// genişliğin tek kaçışı tutamağa çift tıktı — keşfedilmeyen bir affordance.
// Artık DataTableHead her tabloda TEK yere basar: son görünür başlık
// hücresinin sağ ucunda ⋯. Yeni bir kolon/hücre EKLENMEZ (kolon
// genişlikleri ve yapışkan başlık aynen kalır).
//
// v0.10.939 (tablo standardı S8, inceleme) — tetik YER AYIRMAZ: host `th`ye
// kalıcı sağ dolgu vermek son kolonun etiketini/okunu her tabloda sola
// itiyordu. Tetik `th`nin sağ ucuna (6 px'lik boyut tutamağının SOLUNA)
// mutlak konumla biner, zemini başlık yüzeyi (--bg1); dururken görünmez
// (opacity 0 — `.sort-arrow` fikri), başlık satırının üstüne gelince /
// host odakta / menü açıkken belirir (globals.css `.dt-menu`). `visibility:
// hidden` DEĞİL: o, düğmeyi Tab sırasından düşürür ve `:focus-within` onu
// hiç açığa çıkaramazdı. Dokunmatikte (hover yok) hep görünür ve YALNIZ orada
// host dolgusu ayrılır. Host `th`nin adı kolon etiketidir (DataTableHead
// aria-label) — "N Tablo seçenekleri" değil.
//
// Davranış:
//   • Tık / Enter / Boşluk / ↓ açar; ilk ETKİN öğe odak alır (preventScroll;
//     hepsi devre dışıysa menü kabı — Tab/Esc yine çalışsın).
//   • ↑/↓/Home/End öğeler arasında gezer; Tab menüyü kapatır ve odağı
//     tetiğe geri verir (tarayıcı sonra bir sonraki öğeye geçer).
//   • Esc `useEscLayer` ile (tek Esc kanalı — dosyada Escape karşılaştırması
//     YOK, escLayer.test bunu tüm ağaçta tarar); dışarı tık kapatır.
//   • Kaydırma / yeniden boyut menüyü kapatır: `fixed` menü çapasından
//     kopmasın. Odak menünün içindeyse tetiğe döner (belgeye düşmesin).
//   • Tetikte `tooltip` YOK: aria-label ad için yeter; ipucunun kendi Esc
//     katmanı menününkiyle yarışıyordu (Esc önce ipucunu kapatıyordu).
//   • "Kolonları sıfırla" kalıcı genişlik yokken DEVRE DIŞI (sıfırlanacak
//     bir şey yok; `dt.colWidths` boş).
//   • Sarmalayıcı tık ve Enter/Boşluk/ok tuşlarını `th`ye KABARCIKLATMAZ:
//     menü sıralamayı tetiklemez, Shift+← → son kolonu daraltmaz. Esc ve
//     diğer tuşlar kabarcıklanır (Esc katmanı belge dinleyicisinde).
//
// YERLEŞİM: `position: fixed`, koordinat tetiğin dikdörtgeninden
// (menuPlacement.ts). `th`nin ve `.table-wrap`ın `overflow`u kırpamaz.
// `fixed`i yakalayan bir ata (`contain: strict` — sayısal yükseklikli
// VirtualTable; transform'lu çekmece) varsa konum ölçülüp FARK kadar
// düzeltilir; o atanın kırpması kalır (menü tablonun içine açılır).

interface HeadMenuItem { key: string; label: string; onSelect: () => void; disabled?: boolean }

export function DataTableHeadMenu<T>({ dt }: { dt: DataTable<T> }) {
  const [open, setOpen] = useState(false);
  const wrapRef = useRef<HTMLSpanElement>(null);
  const btnRef = useRef<HTMLButtonElement>(null);
  const popRef = useRef<HTMLDivElement>(null);
  const menuId = useId();

  const close = useCallback((refocus: boolean) => {
    setOpen(false);
    if (refocus) btnRef.current?.focus({ preventScroll: true });
  }, []);
  useEscLayer(open, () => close(true));
  const closeOutside = useCallback(() => close(false), [close]);
  useOutsideClose(wrapRef, open, closeOutside);

  const items: HeadMenuItem[] = [
    // v0.10.939 (tablo standardı S8) — kalıcı (sürüklenmiş) genişlikleri
    // temizler; kolon tanımının genişlikleri geri gelir. Sıralama ve
    // kolon görünürlüğü etkilenmez. Kayıtlı genişlik yoksa devre dışı.
    { key: 'reset', label: 'Kolonları sıfırla', onSelect: dt.resetLayout, disabled: Object.keys(dt.colWidths).length === 0 },
  ];

  useLayoutEffect(() => {
    if (!open) return;
    const btn = btnRef.current;
    const pop = popRef.current;
    if (!btn || !pop) return;
    const a = btn.getBoundingClientRect();
    const want = placeMenu(
      { left: a.left, top: a.top, width: a.width, height: a.height },
      { width: pop.offsetWidth, height: pop.offsetHeight },
      { width: window.innerWidth, height: window.innerHeight },
    );
    // Ölçülen yerleşim: React stili her render'da ezmesin diye doğrudan DOM.
    pop.style.left = `${want.left}px`;
    pop.style.top = `${want.top}px`;
    const got = pop.getBoundingClientRect();
    const dx = got.left - want.left;
    const dy = got.top - want.top;
    if (dx || dy) {
      pop.style.left = `${want.left - dx}px`;
      pop.style.top = `${want.top - dy}px`;
    }
    // v0.10.939 (tablo standardı S8) — tüm öğeler devre dışıysa kap odak alır
    // (tabIndex -1): Tab kapatma ve ok tuşları menüde kalsın.
    (pop.querySelector<HTMLElement>('[role="menuitem"]') ?? pop).focus({ preventScroll: true });
    // v0.10.939 (tablo standardı S8) — kaydırma/boyut kapatması odağı
    // menünün İÇİNDEYSE tetiğe geri verir; yoksa sökülen öğeyle birlikte
    // odak belgeye (body) düşerdi. Odak başka yerdeyse dokunulmaz.
    const drop = () => close(!!popRef.current?.contains(document.activeElement));
    window.addEventListener('resize', drop);
    document.addEventListener('scroll', drop, true);
    return () => {
      window.removeEventListener('resize', drop);
      document.removeEventListener('scroll', drop, true);
    };
  }, [open, close]);

  const onMenuKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === 'Tab') { close(true); return; }
    const list = Array.from(e.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]'));
    if (list.length === 0) return;
    const at = list.indexOf(document.activeElement as HTMLElement);
    let next = -1;
    if (e.key === 'ArrowDown') next = at < 0 ? 0 : (at + 1) % list.length;
    else if (e.key === 'ArrowUp') next = at < 0 ? list.length - 1 : (at - 1 + list.length) % list.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = list.length - 1;
    if (next >= 0) {
      e.preventDefault();
      list[next].focus({ preventScroll: true });
    }
  };

  // Başlık hücresine kabarcıklanmasın: tık sıralar, Enter/Boşluk sıralar,
  // Shift+←/→ son kolonu boyutlandırır. Esc KABARCIKLANIR (katman dinleyicisi).
  const stopClick = (e: SyntheticEvent) => e.stopPropagation();
  const stopKeys = (e: ReactKeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ' || e.key.startsWith('Arrow') || e.key === 'Home' || e.key === 'End') {
      e.stopPropagation();
    }
  };

  return (
    <span ref={wrapRef} className="dt-menu" onClick={stopClick} onDoubleClick={stopClick} onKeyDown={stopKeys}>
      {/* v0.10.939 (tablo standardı S8) — tooltip YOK (aria-label yeter; ipucu
          katmanı Esc'i menüden önce yutuyordu). */}
      <IconButton ref={btnRef} size="xs"
        aria-label="Tablo seçenekleri"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen(o => !o)}
        onKeyDown={e => { if (e.key === 'ArrowDown' && !open) { e.preventDefault(); setOpen(true); } }}
        icon="⋯" />
      {open && (
        <div ref={popRef} id={menuId} role="menu" aria-label="Tablo seçenekleri"
          tabIndex={-1} className="dt-menu-pop" onKeyDown={onMenuKeyDown}>
          {items.map(it => (
            // aria-disabled (yerel disabled değil): öğe odak alır ve okunur —
            // tek öğeli menü klavye/ekran okuyucu için boş kalmasın.
            <MenuItem key={it.key} aria-disabled={it.disabled || undefined}
              onClick={() => { if (it.disabled) return; it.onSelect(); close(true); }}>{it.label}</MenuItem>
          ))}
        </div>
      )}
    </span>
  );
}
