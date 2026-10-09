import type { CompletionItem } from './chatCompletion';

// NameCompletionPopup — v0.10.687 (D4): girişin üstünde servis adı listesi.
// Combobox'ın .cb-list / .cb-row sınıfları (tek liste dili); mousedown ile
// seçim (textarea odağı kaybolmasın). Klavye: CopilotChat onKeyDown.
// v0.10.1138 — satırlar etiket + ipucu taşır (komut menüsü, anma türleri,
// env/takım adayları aynı liste dilinde).
export function NameCompletionPopup({ id, items, highlight, onPick, onHover }: {
  id: string;
  items: CompletionItem[];
  highlight: number;
  onPick: (item: CompletionItem) => void;
  onHover: (i: number) => void;
}) {
  return (
    <div className="cb-list chat-complete" role="listbox" id={id} aria-label="Tamamlama önerileri">
      {items.map((n, i) => (
        <div key={`${n.insert}-${i}`} id={`${id}-${i}`} role="option" aria-selected={i === highlight}
          className={`cb-row${i === highlight ? ' cb-row-on' : ''}`}
          onMouseDown={e => { e.preventDefault(); onPick(n); }}
          onMouseEnter={() => onHover(i)}>
          <span className="mono">{n.label}</span>
          {n.hint && <span style={{ marginLeft: 8, fontSize: 11, color: 'var(--text3)' }}>{n.hint}</span>}
        </div>
      ))}
      <div className="cb-row cb-row-empty cb-foot">↑↓ seç · Enter/Tab ekle · Esc kapat</div>
    </div>
  );
}
