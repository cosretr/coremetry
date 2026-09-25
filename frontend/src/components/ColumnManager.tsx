import { useEffect, useMemo, useRef, useState } from 'react';
import { attrKeyWindowParams } from '@/lib/attrKeyWindow';
import { useUrlRange } from '@/lib/useUrlRange';
import { api } from '@/lib/api';
import { canAddCustomColumn } from '@/lib/customColumn';
import { Button } from '@/components/ui/Button';

// ColumnManager — "+ Column" affordance shared by trace tables on
// /traces and /explore. Click opens a Combobox-style picker fed by
// /api/attribute-keys (live span + resource attribute keys observed
// in the last hour) merged with a list of common semconv keys; each
// pick fires `onAdd(key)`. Caps at 8 user columns to keep the
// backend SELECT projection bounded.
//
// Performance: the attribute-keys fetch runs once per panel open
// (not on every render); result is cached in state. The trace-list
// refetch the caller triggers on each addition is bounded by the
// 8-col cap.
export function ColumnManager({ cols, onAdd }: {
  cols: string[];
  onAdd: (k: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [keys, setKeys] = useState<string[] | null>(null);
  const [query, setQuery] = useState('');
  const ref = useRef<HTMLDivElement>(null);

  const [range] = useUrlRange();
  useEffect(() => {
    if (!open || keys !== null) return;
    // v0.9.953 (F3/Ö14c) — kolon keşfi de sayfanın penceresinden
    // (basamaklı). Operatör 7 günlük bir pencerede bir attribute'u
    // kolon olarak eklemek isterken, son bir saatte görülmediyse
    // listede bulamıyordu.
    api.attributeKeys(attrKeyWindowParams(range), 500)
      .then(res => {
        const live = (res ?? []).map(r => r.key);
        const seed = [
          'http.method', 'http.route', 'http.status_code', 'http.url',
          'rpc.system', 'rpc.service', 'rpc.method',
          'db.system', 'db.statement', 'db.operation', 'db.name',
          'messaging.system', 'messaging.destination.name', 'messaging.operation',
          'peer.service', 'server.address', 'kind',
          // v0.10.143 (DETAY SAYFALARI adım 6) — Kubernetes bağlamı: terfi kolonlar
          // (k8s_namespace/k8s_pod/k8s_node) + cluster; kolon olarak eklenince
          // hücreler entity sayfalarına link olur (traceK8sLinks).
          'k8s.namespace.name', 'k8s.pod.name', 'k8s.node.name', 'cluster',
        ];
        setKeys([...new Set([...seed, ...live])].sort());
      })
      .catch(() => setKeys([]));
  }, [open, keys]);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener('mousedown', onDoc);
    return () => document.removeEventListener('mousedown', onDoc);
  }, [open]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    const all = keys ?? [];
    const remaining = all.filter(k => !cols.includes(k));
    if (!q) return remaining.slice(0, 50);
    return remaining.filter(k => k.toLowerCase().includes(q)).slice(0, 50);
  }, [keys, query, cols]);

  const atLimit = cols.length >= 8;

  // v0.9.870 (tutarlılık denetimi mK3) — Enter ile "Add custom column"
  // butonunun TEK koşulu. İkisi ayrı yazılsaydı mesajın göründüğü ama
  // tetikleyicinin çalışmadığı bir aralık kalırdı; kırık vaadin kaynağı
  // tam olarak buydu.
  const canAddCustom = canAddCustomColumn({
    query, keysLoaded: keys !== null, filteredCount: filtered.length,
  });
  const addCustom = () => {
    if (!canAddCustom) return;
    onAdd(query.trim());
    setOpen(false);
    setQuery('');
  };

  return (
    <div ref={ref} style={{ position: 'relative', display: 'inline-block' }}>
      {/* v0.8.78 — operator-reported: the picker trigger was too faint.
          v0.10.924 — buton bütünlüğü Faz 2: elle boyanan accent-tint yerine
          `accent` varyantı (tint zemin + accent metin, "primary değil ama
          vurgulu" katmanı) — soluklaşma riski atomda kapalı; limit hâlini
          `disabled` boyar. */}
      <Button variant="accent" size="sm" disabled={atLimit}
        onClick={() => setOpen(o => !o)}
        title={atLimit ? 'Column limit reached (8)' : 'Add an attribute column'}
        style={{ whiteSpace: 'nowrap' }}>
        + Add column
      </Button>
      {open && (
        <div style={{
          // v0.8.76 — operator-reported: the picker "didn't appear" on /traces.
          // It rendered but anchored right:0 (opening LEFTWARD), and since the
          // "+ Column" button sits near the left of the toolbar, the panel body
          // extended under the sidebar — whose stacking context painted over it.
          // Anchor left:0 so it opens rightward into the content area instead.
          position: 'absolute', left: 0, top: 'calc(100% + 4px)', zIndex: 'var(--z-dropdown)',
          minWidth: 280, maxWidth: 360,
          background: 'var(--bg2)', border: '1px solid var(--border)',
          borderRadius: 6, boxShadow: '0 8px 24px rgba(0,0,0,0.30)',
          padding: 6,
        }}>
          <input autoFocus
            value={query} onChange={e => setQuery(e.target.value)}
            // v0.9.870 (tutarlılık denetimi mK3) — buradaki eksik onKeyDown
            // yüzünden panelin kendi "Press Enter to add it as a custom
            // column." vaadi ÖLÜYDÜ. Escape de bedava geliyor: paneli açıp
            // vazgeçmenin klavye yolu yoktu.
            onKeyDown={e => {
              if (e.key === 'Enter') { e.preventDefault(); addCustom(); }
              else if (e.key === 'Escape') { e.preventDefault(); setOpen(false); }
            }}
            placeholder="Filter attribute keys…"
            style={{ width: '100%', marginBottom: 6, fontSize: 12 }} />
          <div style={{ maxHeight: 280, overflowY: 'auto' }}>
            {keys === null && (
              <div style={{ padding: 8, fontSize: 11, color: 'var(--text3)' }}>Loading…</div>
            )}
            {keys && filtered.length === 0 && (
              <div style={{ padding: 8, fontSize: 11, color: 'var(--text3)', fontStyle: 'italic' }}>
                {/* Vaat, tetikleyiciyle AYNI koşula bağlı: Enter'ın
                    çalışmayacağı bir durumda "Press Enter" yazmıyoruz
                    (geçersiz karakterli anahtar, ör. boşluk içeren). */}
                {!query.trim()
                  ? 'No more attribute keys to add.'
                  : canAddCustom
                    ? `No keys match "${query}". Press Enter to add it as a custom column.`
                    : `No keys match "${query}". A custom column key may only contain letters, digits, dot, dash or underscore.`}
              </div>
            )}
            {filtered.map(k => (
              <div key={k}
                onClick={() => { onAdd(k); setOpen(false); setQuery(''); }}
                style={{
                  padding: '5px 8px', fontSize: 12, cursor: 'pointer',
                  fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
                  borderRadius: 3,
                }}
                onMouseEnter={e => (e.currentTarget.style.background = 'var(--bg3)')}
                onMouseLeave={e => (e.currentTarget.style.background = 'transparent')}>
                {k}
              </div>
            ))}
          </div>
          {canAddCustom && (
            <Button variant="primary" size="sm"
              onClick={addCustom}
              style={{ width: '100%', marginTop: 4 }}>
              {/* Tam genişlik: Button'ın iç .row'u içeriği başa yaslar; etiket ortada kalsın. */}
              <span style={{ flex: 1, textAlign: 'center' }}>Add custom column &quot;{query.trim()}&quot;</span>
            </Button>
          )}
          <div style={{ fontSize: 10, color: 'var(--text3)', padding: '6px 8px 0', borderTop: '1px solid var(--border)', marginTop: 6 }}>
            keys from spans seen in the last 1h
          </div>
        </div>
      )}
    </div>
  );
}
