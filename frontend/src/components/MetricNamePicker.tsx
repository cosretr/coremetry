import { useEffect, useRef } from 'react';
import { api } from '@/lib/api';
import { Combobox } from '@/components/Combobox';
import { shouldAutoCommit } from '@/components/ServicePicker';
import { usePickerSearch } from '@/components/usePickerSearch';
import { getPickerRecents } from '@/lib/pickerRecents';
import type { MetricInfo } from '@/lib/types';

/**
 * MetricNamePicker — metric-names counterpart of ServicePicker /
 * OperationPicker (v0.5.181). Same debounced server-side search
 * + wildcards. Replaces the previous Combobox-with-eager-list
 * pattern in /metrics that fetched every metric name on mount;
 * at 10k+ metric names that round-trip was the main contributor
 * to the page's TTFI.
 *
 * Differs from ServicePicker / OperationPicker in that each
 * option is annotated with unit + instrument type, since
 * operators routinely need to know "is this a counter or a
 * gauge, in seconds or milliseconds" before picking. The
 * v0.9.1024 — bu metadata artık açılır listenin İÇİNDE, satır
 * sonunda soluk bir etiket olarak görünüyor (`optionMeta`). Eskiden
 * datalist'in `label` niteliğine bırakılmıştı: Chromium/Firefox
 * gösteriyor, Safari HİÇ göstermiyordu — yani "birim ve tip
 * görünüyor" iddiası tarayıcıya göre doğru ya da yanlıştı.
 */
export function MetricNamePicker({
  service, value, onChange, placeholder, width, onEnter, onPick,
}: {
  service: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  width?: number | string;
  onEnter?: (value?: string) => void;
  // Fires with the full MetricInfo when an option is picked from
  // the dropdown — gives downstream callers access to unit /
  // type without a second round-trip. Optional.
  onPick?: (m: MetricInfo) => void;
}) {
  // v0.10.1089 — ortak debounce kancası (180 ms, /api/metrics/names,
  // servis kapsamı, 200 satır — davranış aynı).
  const search = usePickerSearch(value, service, q =>
    api.metricNamesSearch(service, q, 200).then(r => ({ items: r.names ?? [], total: r.total })));
  const opts = search.items;
  const total = search.total;
  const lastValueRef = useRef(value);
  const optsRef = search.itemsRef;
  const recentKey = `metric:${service || '*'}`;
  // "Son kullanılan"dan seçilen ad o anki listede olmayabilir; onPick ise
  // TAM MetricInfo ister (birim/tip — uydurulmuş boş birim yanlış eksen
  // biçimi demek). Seçim bekletilir: değer değiştiği için debounce'lu arama
  // zaten o adla koşar, cevapta tam eşleşme gelince commit edilir. Yeni
  // istek YOK — aynı arama.
  const pendingRef = useRef<string | null>(null);

  const handleChange = (next: string) => {
    const prev = lastValueRef.current;
    lastValueRef.current = next;
    pendingRef.current = null;
    onChange(next);
    // v0.9.1024 — ServicePicker'ın saf fonksiyonu (v0.7.27 sözleşmesi).
    const picked = optsRef.current.find(m => m.name === next);
    const recent = !picked && getPickerRecents(recentKey).includes(next);
    if (shouldAutoCommit(prev, next, !!picked || recent)) {
      if (picked) {
        if (onPick) setTimeout(() => onPick(picked), 0);
        if (onEnter) setTimeout(() => onEnter(next), 0);
      } else {
        pendingRef.current = next;
      }
    }
  };

  useEffect(() => {
    const want = pendingRef.current;
    if (!want || search.forQuery !== want || search.status === 'loading') return;
    pendingRef.current = null;
    const m = search.items.find(x => x.name === want);
    if (!m) return;
    onPick?.(m);
    onEnter?.(want);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- yalnız arama cevabına tepki; geri çağrılar her çizimde yeni
  }, [search.items, search.status, search.forQuery]);

  const truncated = total > opts.length;

  // Render tarafı `opts` DURUMUNU okur, ref'i değil: ref güncellemesi
  // yeniden render tetiklemez, dolayısıyla ref'ten okunan bir etiket
  // bir tur geride kalabilirdi (ikisi birlikte yazılıyor ama render
  // sırasında doğrusu durum).
  const names = opts.map(o => o.name);
  const metaOf = (n: string) => {
    const m = opts.find(x => x.name === n);
    return m ? [m.unit, m.type].filter(Boolean).join(' · ') || undefined : undefined;
  };

  return (
    // v0.9.1024 — native <datalist> → ev Combobox'ı; sunucu arama
    // katmanı (debounce + /api/metric-names) aynen duruyor.
    <Combobox
      value={value}
      onChange={handleChange}
      options={names}
      serverFiltered
      resultCount={total}
      status={search.status}
      recentKey={recentKey}
      placeholder={placeholder}
      width={width}
      onEnter={() => onEnter?.(undefined)}
      optionMeta={metaOf}
      footer={truncated
        ? `… +${total - opts.length} more — refine search`
        : undefined}
      title={
        truncated
          ? `Showing ${opts.length} of ${total} metrics — type to refine. Wildcards: http.*, *latency*, p?y`
          : 'Type to filter. Wildcards: http.*, *latency*, p?y'
      }
    />
  );
}
