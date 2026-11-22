// KafkaTopicPicker — v0.10.2467 (operatör 2026-11-22: "topic tablosu yerine
// süzgeç satırında Topic seçimi"). Service → Messaging süzgeç satırının Topic
// alanı. Picker = sunucu araması (CLAUDE.md): metrik kaynağında VM
// label-values (`topic`, servis + kafka_* seçicisi, ≤ 50, sunucuda alt-dizge);
// span yedeğinde liste sekmenin ZATEN okuduğu span özetinin topic'leri.
// Seçim (liste / ↵) ya da temizleme (✕) URL'e yazılır; yazarken yazılmaz.
// Parent `key={value}` verir: URL değişince taslak sıfırlanır.
import { useState } from 'react';
import { Combobox } from '@/components/Combobox';
import { usePickerSearch } from '@/components/usePickerSearch';

export function KafkaTopicPicker({ value, onCommit, scope, search, allLabel }: {
  value: string;
  onCommit: (v: string) => void;
  /** Arama kapsamı (servis + kaynak); değişince liste yeniden gelir. */
  scope: string;
  search: (q: string) => Promise<{ items: string[]; total: number }>;
  allLabel: string;
}) {
  const [draft, setDraft] = useState(value);
  const s = usePickerSearch(draft, scope, search);
  const commit = (v: string) => { if (v !== value) onCommit(v); };
  return (
    <div className="field">
      <span className="field-label">Topic</span>
      <Combobox value={draft} options={s.items} serverFiltered resultCount={s.total} status={s.status}
        placeholder={allLabel} ariaLabel="Topic" title={value || allLabel}
        onChange={v => { setDraft(v); if (v === '') commit(''); }}
        onPick={v => commit(v)}
        onEnter={() => commit(draft.trim())} />
    </div>
  );
}
