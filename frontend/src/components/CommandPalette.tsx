import { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { useShortcuts } from '@/lib/keyboard';
import { useEscLayer } from '@/lib/escLayer';

// CommandPalette KAPISI — v0.10.1653 (etkileşim dilimi P1, plan §1.3).
//
// Palet gövdesi (sonuç listesi, eylemler, sunucu aramaları) artık TEMBEL:
// components/coremetry/CommandPalette/CommandPaletteBody.tsx ayrı chunk.
// Kapı küçük ve eager kalır (kabuk import yasağı listesinde) ve bugünkü iki
// tetiği taşır:
//   • ⌘K / Ctrl-K (`evenInInputs` — filtre kutusundayken de açar), "?"
//     yardımında görünür;
//   • openCommandPalette() — Topbar'ın görünür arama kutusu (v0.9.1019 G1).
// İlk açılışta gövde chunk'ı henüz yoksa kapı yakalama fazında bir TUŞ
// TAMPONU kurar (CopilotChatGate kuyruğunun tuş hâli): yazılabilir
// karakterler eklenir, Backspace siler; Esc açmayı iptal eder — o da tek
// Esc yığınından (useEscLayer, gövde canlı olana dek); gövde mount
// olunca tamponu sorgu olarak alır. Sonraki açılışlar senkron (mandal).
// Isınma: kabuk ilk boyamadan sonra boşta chunk'ı önceden çeker — dinamik
// import olduğu için index.html preload kümesine girmez.
// Kapı ui/ import etmez ve <input> çizmez (uiImportRatchet / rawElementRatchet).

const loadBody = () => import('@/components/coremetry/CommandPalette/CommandPaletteBody');
const CommandPaletteBody = lazy(() => loadBody().then(m => ({ default: m.CommandPaletteBody })));

// ——— Görünür kapı kanalı (v0.9.1019, G1) ————————————————————————
// Tetik (Topbar) ile palet (AppShell) kardeş — aralarında prop geçirmek
// gereksiz bir kablo çekerdi; modül düzeyinde abonelik seti. Sentetik ⌘K
// olayı DEĞİL: kayıt defterinin arbitrajını taklit etmek ilk kenar vakada
// ayrışır — bu dikiş paletin KENDİ açma yolunu çağırır.
type PaletteOpener = () => void;
const paletteOpeners = new Set<PaletteOpener>();

/** Görünür kapıdan (Topbar arama kutusu) paleti açar. */
export function openCommandPalette() {
  paletteOpeners.forEach(fn => fn());
}

function subscribeOpenPalette(fn: PaletteOpener): () => void {
  paletteOpeners.add(fn);
  return () => { paletteOpeners.delete(fn); };
}

/** Tampon adımı (saf): yazılabilir karakter ekler, Backspace siler; başka
 *  her tuş (Esc dahil — Esc yığına aittir) dokunulmadan geçer (`null`). */
export function bufferKey(
  buf: string,
  e: Pick<KeyboardEvent, 'key' | 'ctrlKey' | 'metaKey' | 'altKey'>,
): string | null {
  if (e.ctrlKey || e.metaKey || e.altKey) return null;
  if (e.key === 'Backspace') return buf.slice(0, -1);
  if (e.key.length === 1) return buf + e.key;
  return null;
}

type IdleWindow = Window & {
  requestIdleCallback?: (cb: () => void) => number;
  cancelIdleCallback?: (id: number) => void;
};

export function CommandPalette() {
  const [open, setOpen] = useState(false);
  const [mounted, setMounted] = useState(false);
  const [openSeq, setOpenSeq] = useState(0);
  const [bodyLive, setBodyLive] = useState(false);
  const buffer = useRef('');
  const live = useRef(false);

  const doOpen = useCallback(() => {
    setMounted(true);
    setOpen(true);
    setOpenSeq(n => n + 1);
  }, []);

  useShortcuts([{
    keys: 'mod+k',
    label: 'Open command palette',
    group: 'Navigation',
    evenInInputs: true,
    handler: doOpen,
  }], [doOpen]);

  useEffect(() => subscribeOpenPalette(doOpen), [doOpen]);

  // Tuş tamponu: açık ama gövde henüz canlı değilken. Yakalama fazı +
  // stopPropagation — alttaki sayfa girdisine yazılmaz.
  useEffect(() => {
    if (!open || bodyLive) return;
    const h = (e: KeyboardEvent) => {
      if (live.current) return;
      const next = bufferKey(buffer.current, e);
      if (next === null) return;
      e.preventDefault();
      e.stopPropagation();
      buffer.current = next;
    };
    window.addEventListener('keydown', h, true);
    return () => window.removeEventListener('keydown', h, true);
  }, [open, bodyLive]);

  // Gövde yüklenirken Esc = açmayı iptal; en üst katman olarak yalnız bunu
  // kapatır (alttaki çekmece açık kalır). Gövde canlıyken kendi katmanı var.
  useEscLayer(open && !bodyLive, () => { buffer.current = ''; setOpen(false); });

  // Isınma: ilk boyamadan sonra boşta gövde chunk'ını önceden çek.
  useEffect(() => {
    const w = window as IdleWindow;
    const warm = () => { void loadBody().catch(() => {}); };
    if (w.requestIdleCallback) {
      const id = w.requestIdleCallback(warm);
      return () => w.cancelIdleCallback?.(id);
    }
    const id = window.setTimeout(warm, 1500);
    return () => window.clearTimeout(id);
  }, []);

  // Gövde ilk açılışında çağırır: tampon sorguya düşer, kapı dinlemeyi bırakır.
  const takeBufferedQuery = useCallback(() => {
    live.current = true;
    setBodyLive(true);
    const q = buffer.current;
    buffer.current = '';
    return q;
  }, []);

  if (!mounted) return null;
  return (
    <Suspense fallback={null}>
      <CommandPaletteBody open={open} onOpenChange={setOpen} openSeq={openSeq}
        takeBufferedQuery={takeBufferedQuery} />
    </Suspense>
  );
}
