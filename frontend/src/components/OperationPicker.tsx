import { useRef } from 'react';
import { api } from '@/lib/api';
import { Combobox } from '@/components/Combobox';
import { shouldAutoCommit } from '@/components/ServicePicker';
import { usePickerSearch } from '@/components/usePickerSearch';
import { getPickerRecents } from '@/lib/pickerRecents';

/**
 * OperationPicker — operations-picker counterpart to ServicePicker
 * (v0.5.180). Same debounced server-side search + wildcard
 * semantics. Drop-in replacement for `<Combobox options={ops}>`
 * which eager-loaded the top-500 ops per service — long-tail
 * operations on a 10k-op service were unreachable from the
 * picker without this.
 *
 * Service filter is recommended (and usually present in the
 * parent context — Traces page, etc.) — without it the picker
 * lists every op across every service which is rarely useful
 * past tens of thousands of operations.
 */
export function OperationPicker({
  service, value, onChange, placeholder, width, onEnter, onPick,
}: {
  // Scope the search to one service. Pass undefined / empty to
  // search across every service (cardinality permitting).
  service?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  width?: number | string;
  onEnter?: (value?: string) => void;
  // v0.10.752 — değer listedeki bir operasyonla BİREBİR eşleşince (seçim)
  // çağrılır; çağıran bunu alt-dizge arama yerine tam eşleşme yapar.
  onPick?: (v: string) => void;
}) {
  // v0.10.1089 — ortak debounce kancası (180 ms, /api/operation-names,
  // servis kapsamı, 200 satır — davranış aynı).
  const search = usePickerSearch(value, service ?? '', q =>
    api.operationNames(service || undefined, q, 200).then(r => ({ items: r.names, total: r.total })));
  const opts = search.items;
  const total = search.total;
  const lastValueRef = useRef(value);
  const optsRef = search.itemsRef;
  // "Son kullanılan" servis BAŞINA: başka servisin operasyonunu önermek
  // boş sonuçlu bir süzgeç üretirdi.
  const recentKey = `operation:${service || '*'}`;

  const handleChange = (next: string) => {
    const prev = lastValueRef.current;
    lastValueRef.current = next;
    onChange(next);
    // v0.9.1024 — ServicePicker'ın SAF fonksiyonu. Buradaki kopya da
    // eski (v0.7.27 öncesi) ifadeydi: ilk tuş vuruşunda ve çok
    // karakterli SİLME sıçramalarında yanlış commit ediyordu.
    const isPick = optsRef.current.includes(next) || getPickerRecents(recentKey).includes(next);
    if (isPick && onPick) onPick(next); // v0.10.752
    if (shouldAutoCommit(prev, next, isPick) && onEnter) {
      setTimeout(() => onEnter(next), 0);
    }
  };

  const truncated = total > opts.length;

  return (
    // v0.9.1024 — native <datalist> → ev Combobox'ı. Sunucu arama
    // katmanı (debounce + /api/operation-names, servis kapsamı) aynen
    // duruyor; `serverFiltered` joker karakterleri korumak için şart.
    <Combobox
      value={value}
      onChange={handleChange}
      options={opts}
      serverFiltered
      resultCount={total}
      status={search.status}
      recentKey={recentKey}
      placeholder={placeholder}
      width={width}
      onEnter={() => onEnter?.(undefined)}
      footer={truncated
        ? `… +${total - opts.length} more — refine search`
        : undefined}
      title={
        truncated
          ? `Showing ${opts.length} of ${total} operations — type to refine. Wildcards: pay*, *pay*, p?y`
          : 'Type to filter. Wildcards: pay*, *pay*, p?y'
      }
    />
  );
}
