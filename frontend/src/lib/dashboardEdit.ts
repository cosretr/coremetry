// lib/dashboardEdit — v0.10.2000 (Dashboards Faz 3 dilim 3.10; audit §5.5
// "Sürüm / geri al").
//
// İki ucuz şey, kalıcı sürüm geçmişi YOK (şema ister):
//   (a) düzenleme kipinde oturum içi geri al: taslağın önceki hâlleri bir
//       yığında; aynı anahtarlı ardışık değişiklik (ör. aynı panelin başlığını
//       yazmak) TEK adım sayılır, yoksa her tuş bir geri-al adımı olurdu.
//   (b) PUT'ta `ifUpdatedAt`: sunucu başka biri kaydettiyse 409 döner
//       (internal/api/dashboard_update.go). isDashboardConflict o hatayı tanır.

export const UNDO_MAX = 50;

export interface UndoStack<T> {
  past: T[];
  /** Son itilen değişikliğin birleştirme anahtarı (aynıysa yeni adım açılmaz). */
  lastKey: string | null;
}

export function emptyUndo<T>(): UndoStack<T> {
  return { past: [], lastKey: null };
}

/** SAF: değişiklikten ÖNCEKİ hâli yığına it. `key` boş değilse ve son
 *  anahtarla aynıysa birleştirilir (yığın değişmez). */
export function undoPush<T>(st: UndoStack<T>, before: T, key: string | null, max = UNDO_MAX): UndoStack<T> {
  if (key !== null && key === st.lastKey && st.past.length > 0) return st;
  const past = [...st.past, before];
  return { past: past.length > max ? past.slice(past.length - max) : past, lastKey: key };
}

/** SAF: son hâli geri ver; yığın boşsa null. */
export function undoPop<T>(st: UndoStack<T>): { state: T; stack: UndoStack<T> } | null {
  if (st.past.length === 0) return null;
  return { state: st.past[st.past.length - 1], stack: { past: st.past.slice(0, -1), lastKey: null } };
}

/** api.request hatası 409 mu ("HTTP 409: …")? */
export function isDashboardConflict(e: unknown): boolean {
  return e instanceof Error && /^HTTP 409\b/.test(e.message);
}
