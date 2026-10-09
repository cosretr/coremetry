import { useCallback, useEffect, useState } from 'react';

// composerDraft — v0.10.1145: composer taslağı KONUŞMA BAŞINA sessionStorage'da.
//
// Konuşma değiştirmek (geçmişten başka thread, "+ Yeni konuşma", ?chat=)
// ya da sayfayı yenilemek yazılanı kaybettirmez; gönderim taslağı siler.
// sessionStorage: sekme ömrü (operatörün yarım sorusu başka sekmeye/oturuma
// taşınmaz); her erişim try/catch — gizli pencere / engelli depolama / kota
// aşımında taslak yalnız bellekte yaşar, composer çalışmaya devam eder.
//
// Anahtar geçişi iki türlü:
//   - AÇIK geçiş (kabuk `expect(hedef)` dedi: geçmişten açma, yeni konuşma,
//     ?chat=): hedefin kayıtlı taslağı yüklenir, eskisi kendi anahtarında kalır;
//   - ÖRTÜK geçiş (sunucu yeni konuşmaya kimlik bastı: null → "C1"): ekrandaki
//     metin AYNEN taşınır — operatör akış sürerken sonraki sorusunu yazıyor
//     olabilir, kimlik gelince silinmemeli. Eski anahtar boşaltılır.

const PREFIX = 'cosre.draft.v1:';
/** Bundan büyük taslak saklanmaz (bellekte kalır) — sekme kotası tek taslağa gitmesin. */
export const DRAFT_MAX_CHARS = 100_000;

export function draftKey(surface: string, conv: string | null | undefined): string {
  return `${PREFIX}${surface}:${conv || 'new'}`;
}

export function readDraft(key: string): string {
  try {
    return window.sessionStorage.getItem(key) ?? '';
  } catch {
    return '';
  }
}

export function writeDraft(key: string, value: string): void {
  try {
    if (!value || value.length > DRAFT_MAX_CHARS) window.sessionStorage.removeItem(key);
    else window.sessionStorage.setItem(key, value);
  } catch { /* depolama yok / kota — taslak bellekte */ }
}

export function clearDraft(key: string): void {
  try { window.sessionStorage.removeItem(key); } catch { /* yok */ }
}

// ── Araç çubuğu açık/kapalı (kullanıcı başına, localStorage) ──────────
// Operatör kararı (2026-10-09): araç çubuğu VARSAYILAN KAPALI; composer düz bir
// sohbet kutusu gibi görünür (metin + "Aa" + model hapı + Gönder). "Aa" açar /
// kapatır, seçim kullanıcı başına hatırlanır (chatProfileStore ile aynı anahtar
// biçimi). Depolama yoksa / atarsa: kapalı.
const TOOLS_PREFIX = 'cosre.composerTools.';

export function toolsOpenKey(user: string | undefined | null): string {
  return TOOLS_PREFIX + (user || 'anon');
}

export function readToolsOpen(user: string | undefined | null): boolean {
  try {
    return window.localStorage.getItem(toolsOpenKey(user)) === '1';
  } catch {
    return false;
  }
}

export function writeToolsOpen(user: string | undefined | null, open: boolean): void {
  try { window.localStorage.setItem(toolsOpenKey(user), open ? '1' : '0'); } catch { /* yok — bellekte */ }
}

interface DraftState { key: string; value: string; from?: string }

export interface ComposerDraft {
  value: string;
  setValue: (v: string | ((prev: string) => string)) => void;
  /** Kabuk bu konuşmaya GEÇECEK (açık geçiş): anahtar ona dönünce kayıtlı taslak yüklenir. */
  expect: (conv: string | null) => void;
}

export function useComposerDraft(surface: string, conv: string | null | undefined): ComposerDraft {
  const key = draftKey(surface, conv);
  const [st, setSt] = useState<DraftState>(() => ({ key, value: readDraft(key) }));
  const [expected, setExpected] = useState<string | null>(null);
  let cur = st;
  if (st.key !== key) {
    // Render sırasında türetilen geçiş (React "önceki prop'u sakla" deseni).
    const explicit = expected === key;
    cur = explicit ? { key, value: readDraft(key) } : { key, value: st.value, from: st.key };
    setSt(cur);
    if (explicit) setExpected(null);
  }
  useEffect(() => {
    if (cur.from) clearDraft(cur.from);
    writeDraft(cur.key, cur.value);
  }, [cur.key, cur.value, cur.from]);
  const setValue = useCallback((v: string | ((prev: string) => string)) => {
    setSt(prev => ({ key: prev.key, value: typeof v === 'function' ? v(prev.value) : v }));
  }, []);
  const expect = useCallback((c: string | null) => setExpected(draftKey(surface, c)), [surface]);
  return { value: cur.value, setValue, expect };
}
