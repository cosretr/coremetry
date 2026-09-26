import { useState } from 'react';
import { Modal } from '@/components/ui';
import { listShortcuts, useShortcuts, type Shortcut } from '@/lib/keyboard';

// Help modal that lists every registered shortcut. The "?"
// global binding toggles it open. Bindings register and
// unregister with their owning components, so the list is
// always live — opening Help on /traces shows the page-local
// 'j' / 'k' bindings, opening it on /services shows whatever
// that page registered.
//
// Grouping: shortcuts can declare a `group` for the help
// modal. Anything missing a group falls under "Global".
// Display order matches the registry's insertion order
// within a group, which is "global first, then per-page" in
// practice.

export function ShortcutsHelp() {
  const [open, setOpen] = useState(false);

  // The "?" binding is itself a shortcut, registered by this
  // component so the modal owns its own opening trigger. We
  // also register Esc as evenInInputs so a focused search can
  // close the modal without losing context.
  useShortcuts(
    [
      {
        keys: '?',
        label: 'Show keyboard shortcuts',
        group: 'Help',
        handler: () => setOpen(true),
      },
      {
        keys: 'shift+?',
        label: 'Show keyboard shortcuts (Shift+?)',
        group: 'Help',
        handler: () => setOpen(true),
      },
    ],
    [],
  );

  if (!open) return null;
  const all = listShortcuts();
  const grouped = groupBy(all, sc => sc.group ?? 'Global');

  return (
    <Modal open={open} onClose={() => setOpen(false)}
           title="Keyboard shortcuts" size="lg">
      <div style={{ display: 'flex', flexDirection: 'column', gap: 18 }}>
        {Object.entries(grouped).map(([group, items]) => (
          <div key={group}>
            <div style={{
              fontSize: 11, fontWeight: 600, letterSpacing: 0.5,
              textTransform: 'uppercase', color: 'var(--text3)',
              marginBottom: 8,
            }}>
              {group}
            </div>
            <div style={{ display: 'grid',
                          gridTemplateColumns: 'auto 1fr',
                          rowGap: 6, columnGap: 16, fontSize: 13 }}>
              {items.map(sc => (
                <span key={sc.keys} style={{ display: 'contents' }}>
                  <KeysPill keys={sc.keys} />
                  <span>{sc.label}</span>
                </span>
              ))}
            </div>
          </div>
        ))}
      </div>
      <div style={{ fontSize: 11, color: 'var(--text3)', marginTop: 18 }}>
        Tip: shortcuts pause while typing in inputs/textareas.
        Press <code>Esc</code> to close any open dialog.
      </div>
    </Modal>
  );
}

function KeysPill({ keys }: { keys: string }) {
  // Render 'mod+k' as ⌘ K, 'g s' as G→S, 'shift+?' as ⇧+?
  const parts = keys.includes(' ')
    ? keys.split(' ').map(k => k.toUpperCase()).join(' then ')
    : keys.split('+').map(formatKey).join(' ');
  return (
    <kbd style={{
      display: 'inline-block', fontFamily: 'var(--font-mono)',
      fontSize: 11, padding: '2px 6px',
      background: 'var(--bg3)',
      border: '1px solid var(--border)',
      borderRadius: 4, color: 'var(--text)',
      minWidth: 60, textAlign: 'center',
    }}>
      {parts}
    </kbd>
  );
}

function formatKey(k: string): string {
  switch (k) {
    case 'mod': return navigator.platform.startsWith('Mac') ? '⌘' : 'Ctrl';
    case 'alt': return navigator.platform.startsWith('Mac') ? '⌥' : 'Alt';
    case 'shift': return '⇧';
    case 'Enter': return '↵';
    case 'Escape': return 'Esc';
    case 'ArrowUp': return '↑';
    case 'ArrowDown': return '↓';
    case 'ArrowLeft': return '←';
    case 'ArrowRight': return '→';
    default: return k.length === 1 ? k.toUpperCase() : k;
  }
}

function groupBy<T>(arr: T[], fn: (x: T) => string): Record<string, T[]> {
  const out: Record<string, T[]> = {};
  for (const x of arr) {
    const k = fn(x);
    (out[k] = out[k] || []).push(x);
  }
  return out;
}
