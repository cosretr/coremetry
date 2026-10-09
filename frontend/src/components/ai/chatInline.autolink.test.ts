// v0.10.1145 — CommonMark otomatik bağlantısı `<https://…>`: composer'ın
// Ctrl/Cmd+K'si seçili URL'yi böyle sarar; çözücü köşeli ayraçları düşürür ve
// adresi çıplak URL ile AYNI düğüme çevirir (güvenlik kararı chatLinks'te).
import { describe, it, expect } from 'vitest';
import { parseInline } from './chatInline';

describe('chatInline — <url> otomatik bağlantı', () => {
  it('http(s) → link düğümü, ayraçlar düşer', () => {
    expect(parseInline('bkz <https://wiki.example.test/a?b=1> lütfen')).toEqual([
      { t: 'text', v: 'bkz ' },
      { t: 'link', href: 'https://wiki.example.test/a?b=1', label: null },
      { t: 'text', v: ' lütfen' },
    ]);
  });
  it('http(s) olmayan şema ve boşluklu içerik düz metin kalır', () => {
    expect(parseInline('<javascript:alert(1)>')).toEqual([{ t: 'text', v: '<javascript:alert(1)>' }]);
    expect(parseInline('<https://a b>')).toEqual([
      { t: 'text', v: '<' },
      { t: 'link', href: 'https://a', label: null },
      { t: 'text', v: ' b>' },
    ]);
  });
  it('bağlantı etiketinin içinde (noLinks) çözülmez', () => {
    const n = parseInline('[x <https://a.example.test>](https://b.example.test)');
    expect(n).toHaveLength(1);
    expect(n[0]).toMatchObject({ t: 'link', href: 'https://b.example.test' });
  });
});
