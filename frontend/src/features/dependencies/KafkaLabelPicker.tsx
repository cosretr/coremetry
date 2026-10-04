// KafkaLabelPicker.tsx — v0.10.1097. "Kafka istemcileri" sekmesinin topic /
// client_id seçicisi. OperationPicker ailesi: Combobox (ui/PickerPopover
// listesi, v0.10.1089) + usePickerSearch (180 ms debounce, sıra bekçisi) +
// sunucu araması (/api/messaging/kafka-label-values, limit 50). Tam katalog
// ÖNDEN ÇEKİLMEZ; seçim örneklenmiş bir alt kümeye karşı DOĞRULANMAZ
// (v0.8.265) — Enter yazılanı olduğu gibi uygular.
//
// Taslak / uygulanmış ayrımı: yazmak URL'e dokunmaz (her tuş vuruşu VM
// sorgusu demek olurdu); listeden seçim, Enter ya da temizleme (✕) uygular.
// Odaktan çıkışta taslak uygulanmış değere döner — ekranda uygulanmamış bir
// süzgeç metni kalmaz.
import { useEffect, useState } from 'react';
import { api } from '@/lib/api';
import { Combobox } from '@/components/Combobox';
import { usePickerSearch } from '@/components/usePickerSearch';

export function KafkaLabelPicker({ label, value, onCommit, fromNs, toNs, placeholder }: {
  label: 'topic' | 'client_id';
  /** Uygulanmış değer (URL'den). */
  value: string;
  onCommit: (v: string) => void;
  fromNs: number;
  toNs: number;
  placeholder?: string;
}) {
  const [draft, setDraft] = useState(value);
  // URL dışarıdan değişirse (geri tuşu, paylaşılan link) taslak izler.
  useEffect(() => { setDraft(value); }, [value]);
  const search = usePickerSearch(draft, `${label}:${fromNs}:${toNs}`, q =>
    api.kafkaLabelValues(label, q, fromNs, toNs).then(r => ({ items: r.values, total: r.values.length })));
  const commit = (v: string) => {
    const t = v.trim();
    setDraft(t);
    if (t !== value) onCommit(t);
  };
  return (
    <Combobox
      value={draft}
      onChange={next => {
        setDraft(next);
        // Temizleme (✕) ve listeden seçim ANINDA uygulanır.
        if (!next.trim() || search.itemsRef.current.includes(next)) commit(next);
      }}
      onEnter={() => commit(draft)}
      onBlurCommit={() => setDraft(value)}
      options={search.items}
      serverFiltered
      resultCount={search.total}
      status={search.status}
      recentKey={`kafka-${label}`}
      ariaLabel={`${label} süzgeci`}
      placeholder={placeholder}
      width={200}
      title={`${label} — yazınca sunucuda aranır (ilk 50); Enter yazılanı uygular`}
    />
  );
}
