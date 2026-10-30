import { Combobox } from '@/components/Combobox';
import { usePickerSearch } from '@/components/usePickerSearch';
import { api } from '@/lib/api';
import { fmtNum } from '@/lib/utils';

// ExchangeUserPicker — v0.10.1646: /ai "CoSRE etkileşimleri" kullanıcı
// süzgeci. Picker = sunucu araması (ServicePicker deseni): yazılan değer
// durulunca GET /api/ai/exchanges/users?q= — pencerede soru sormuş,
// sorguya uyan ilk 20 kullanıcı; tam kullanıcı listesi ASLA önden çekilmez.
// Değer taslaktır: süzgeç yalnız seçimde / Enter'da (onCommit) uygulanır,
// alan boşaltılınca süzgeç kalkar.
export function ExchangeUserPicker({ value, onChange, onCommit, from, to }: {
  value: string;
  onChange: (v: string) => void;
  onCommit: (v: string) => void;
  from: number;
  to: number;
}) {
  const search = usePickerSearch(value, `${from}:${to}`, q =>
    api.aiExchangeUsers({ from, to, q, limit: 20 }).then(r => {
      const items = (r ?? []).map(u => u.user);
      return { items, total: items.length };
    }));
  return (
    <Combobox
      value={value}
      onChange={v => { onChange(v); if (v === '') onCommit(''); }}
      onPick={v => onCommit(v)}
      onEnter={() => onCommit(value.trim())}
      options={search.items}
      serverFiltered
      resultCount={search.total}
      status={search.status}
      placeholder="Kullanıcı…"
      ariaLabel="Kullanıcı süzgeci"
      width={200}
      title="Pencerede soru sormuş kullanıcılar — yazdıkça sunucuda aranır"
      footer={search.items.length >= 20 ? `ilk ${fmtNum(20)} — daraltmak için yazın` : undefined}
    />
  );
}
