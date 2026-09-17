/**
 * Exceptions sayfası sekmeleri (v0.10.751; operatör: "Inbox ignore hariç
 * hepsi statüsüyle gözüksün, ignore edilenler sekmede").
 *
 * Inbox = ignored hariç HER durum (new, acknowledged, regressed, resolved)
 * — satır durum rozetiyle ayrışır; sunucu kovası `state=inbox`. Diğer
 * sekmeler daraltıcı süzgeç. `?tab=open` eski Inbox adresi (v0.6-v0.10.750):
 * ayrıştırıcı onu inbox'a çevirir ki kayıtlı görünüm / paylaşılan link
 * bir sekmeye düşmeye devam etsin.
 */
export interface ExceptionTab {
  key: string;
  label: string;
  hint: string;
}

export const EXCEPTION_TABS: ExceptionTab[] = [
  { key: 'inbox',        label: 'Inbox',        hint: 'Ignored hariç her durum: new + acknowledged + regressed + resolved' },
  { key: 'new',          label: 'Open',         hint: 'Untouched since first occurrence' }, // v0.8.382: NEW is the first-seen badge
  { key: 'acknowledged', label: 'Acknowledged', hint: 'Someone is on it' },
  { key: 'regressed',    label: 'Regressed',    hint: 'Resolved but happening again' },
  { key: 'resolved',     label: 'Resolved',     hint: 'Closed out' },
  { key: 'ignored',      label: 'Ignored',      hint: 'Permanently silenced' },
];

export const EXCEPTION_TAB_DEFAULT = 'inbox';

/** URL `?tab=` → sekme anahtarı. Boş/bilinmeyen → inbox; eski `open` → inbox. */
export function resolveExceptionTab(raw: string | null | undefined): string {
  const v = (raw ?? '').trim();
  if (!v || v === 'open') return EXCEPTION_TAB_DEFAULT;
  return EXCEPTION_TABS.some(t => t.key === v) ? v : EXCEPTION_TAB_DEFAULT;
}
