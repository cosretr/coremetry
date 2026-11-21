import { Chip } from '@/components/ui/Chip';
import '@/styles/cosreChat.css';
import type { ContextBarItem, ContextKey } from './chatContextScope';

// ChatContextBar — v0.10.1274 (CoSRE adım 5): composer'daki aktif bağlam şeridi.
// Öğeler chatContextScope.contextBarItems'tan (saf); ✕ öğeyi bu konuşmanın
// isteklerinden çıkarır (durum CopilotChat'te). Öğe yoksa hiçbir şey çizilmez.
export function ChatContextBar({ items, onRemove }: {
  items: readonly ContextBarItem[];
  onRemove: (key: ContextKey) => void;
}) {
  if (items.length === 0) return null;
  return (
    <div className="cm-context-bar" role="group" aria-label="Aktif bağlam">
      <span className="cell-hint" aria-hidden="true">Bağlam:</span>
      {items.map(it => (
        <Chip key={it.key} pill size="sm" title={it.title} onRemove={() => onRemove(it.key)}
          removeLabel={`${it.label} bağlamını bu konuşmadan çıkar`}>{it.label}</Chip>
      ))}
    </div>
  );
}
