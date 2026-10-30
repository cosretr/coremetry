// emptyAreaClose.ts — v0.10.1296 (operatör 2026-10-17: "CoSRE açıldığında
// illa çarpıya değil, ekranda solda boş bir yere basınca da kapansın").
//
// CoSRE çekmecesi bilinçli olarak PERDESİZ (backdrop=false): sohbet açıkken
// sayfayla çalışılır (tablo kaydırma, trace açma). Bu yüzden "dışarı tıkla →
// kapat" HER tıklamada değil, yalnız BOŞ alana yapılan tıklamada çalışır:
// etkileşimli bir öğeye (link, düğme, alan, tablo satırı, grafik, menü,
// başka bir katman) yapılan tıklama sohbeti AÇIK bırakır.

/** Tıklandığında çekmeceyi kapatmayan (sayfayla çalışma) öğeler. */
export const KEEP_OPEN_SELECTOR = [
  'a', 'button', 'input', 'textarea', 'select', 'label', 'summary', 'option',
  '[role="button"]', '[role="link"]', '[role="menu"]', '[role="menuitem"]',
  '[role="option"]', '[role="listbox"]', '[role="tab"]', '[role="dialog"]',
  '[role="checkbox"]', '[role="switch"]', '[role="slider"]', '[role="tooltip"]',
  '[contenteditable=""]', '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
  'tr', 'canvas', 'svg', '[data-keep-drawer]',
].join(',');

/**
 * Saf karar: `target` çekmece panelinin DIŞINDA ve etkileşimli bir öğenin
 * içinde DEĞİLSE true (= boş alana tıklandı, çekmece kapanmalı).
 */
export function isEmptyAreaClick(target: EventTarget | null, panel: Element | null): boolean {
  if (!(target instanceof Element)) return false;
  if (!target.isConnected) return false; // tıklama sonrası DOM'dan kalkmış öğe (menü kapandı vb.)
  if (panel && panel.contains(target)) return false;
  return target.closest(KEEP_OPEN_SELECTOR) === null;
}
