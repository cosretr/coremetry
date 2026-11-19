// ExplainBody.test.tsx — v0.10.165: kart anatomisi çalışma zamanında —
// Karar satırı, Kanıt satırı, oluklu kod bloğu (satır numarası sütunu, hata
// satırı vurgusu, dosya başlığı, mapper etiketi) gövdeden ÖNCE; stack çiti
// yerinde ve katlı; akış sürerken anatomi yok.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { ExplainBody } from './ExplainBody';

const TEXT = [
  '**Hata ve Anlamı**',
  '- PSQLException: kolon yok.',
  '',
  '**Kök Neden ve Sonraki Adım**',
  '- **LedgerEntryDao.insertBatch** 246. satırda eski kolona yazıyor. Kontrol: mapper.',
  '  ```java',
  '  // src/main/java/com/payments/ledger/dao/LedgerEntryDao.java:244-247',
  '  244| try {',
  '  >>> 246|   m.insert(e);',
  '  247| }',
  '  ```',
  '  ```xml',
  '  // src/main/resources/mappers/LedgerEntryMapper.xml:31-32',
  '  31| <insert id="insert">',
  '  32|   INSERT INTO ledger_entry (settlement_batch_id)',
  '  ```',
  '',
  '**Stacktrace Detayı**',
  '```',
  'org.postgresql.util.PSQLException: ERROR: column',
  ...Array.from({ length: 40 }, (_, i) => `\tat org.springframework.jdbc.core.JdbcTemplate.execute(JdbcTemplate.java:${300 + i})`),
  '```',
].join('\n');

describe('ExplainBody (v0.10.165)', () => {
  const html = renderToStaticMarkup(<ExplainBody text={TEXT} busy={false} evidence={{ spans: 3, traces: 2 }} />);
  it('Karar + Kanıt satırları en üstte; Karar cümlesi gövdede TEKRARLANMAZ, madde kalanı durur', () => {
    expect(html).toContain('cx-verdict');
    // v0.10.2374 — Karar şeridi otomatik vurgulu (LedgerEntryDao.insertBatch çip): METİN karşılaştırılır.
    expect(html.replace(/<[^>]+>/g, '')).toContain('LedgerEntryDao.insertBatch 246. satırda eski kolona yazıyor.');
    expect(html.split('246. satırda eski kolona yazıyor.').length - 1).toBe(1);
    expect(html.replace(/<[^>]+>/g, '')).toContain('Kontrol: mapper.'); // madde etiketi ayrı span (v0.10.2374)
    expect(html).toContain('↑ kod alıntısı yukarıda');
    expect(html).toContain('Kanıt: 3 span · 2 trace');
    expect(html.indexOf('cx-verdict')).toBeLessThan(html.indexOf('cx-evidence'));
  });
  it('oluklu kod bloğu: numara sütunu, hata satırı vurgusu, dosya başlığı, mapper etiketi; önek gövdede yok; gövdeden ÖNCE', () => {
    expect(html).toContain('cm-md-code-no');
    expect(html).toContain('>246<');
    expect(html).toContain('cm-md-code-line hl');
    expect(html).toContain('LedgerEntryDao.java:244-247');
    expect(html).toContain('kaynak penceresi (mapper)');
    expect(html).not.toContain('246|');
    expect(html.indexOf('cm-md-code-no')).toBeLessThan(html.indexOf('Hata ve Anlamı'));
  });
  it('stack çiti yerinde (Stacktrace Detayı) ve katlı: N kare daha', () => {
    const at = html.indexOf('Stacktrace Detayı');
    expect(at).toBeGreaterThan(0);
    expect(html.slice(at)).toMatch(/\d+ kare daha/);
    expect(html.slice(at)).toContain('JdbcTemplate.java:300');
    expect(html.slice(at)).not.toContain('JdbcTemplate.java:339');
  });
  it('akış sürerken anatomi uygulanmaz (ham metin akar)', () => {
    const h = renderToStaticMarkup(<ExplainBody text={TEXT} busy evidence={{ spans: 3, traces: 2 }} />);
    expect(h).not.toContain('cx-verdict');
    expect(h).not.toContain('cx-evidence');
  });
  it('verdict={false} → Karar çizilmez, cümle gövdede kalır (kod kartı açıkken ilk kart)', () => {
    const h = renderToStaticMarkup(<ExplainBody text={TEXT} busy={false} verdict={false} />);
    expect(h).not.toContain('cx-verdict');
    expect(h).toContain('246. satırda eski kolona yazıyor.');
  });
  it('kanıt yoksa satır yok; Karar bölümü tek cümleyse Karar yok', () => {
    const h = renderToStaticMarkup(<ExplainBody text={'**Olası neden:** tek cümle.'} busy={false} evidence={{ spans: 0, traces: 0 }} />);
    expect(h).not.toContain('cx-evidence');
    expect(h).not.toContain('cx-verdict');
  });
  it('v0.10.921 — Oracle satırı sayısı kanıt satırına girer; tek başına da satır çizer', () => {
    const h = renderToStaticMarkup(<ExplainBody text={TEXT} busy={false} evidence={{ spans: 19, traces: 0, oracle: 2 }} />);
    expect(h).toContain('Kanıt: 19 span · 2 Oracle satırı');
    const only = renderToStaticMarkup(<ExplainBody text={TEXT} busy={false} evidence={{ spans: 0, traces: 0, oracle: 1 }} />);
    expect(only).toContain('Kanıt: 1 Oracle satırı');
  });
});

// v0.10.1033 — operatör: "Kanıt span'lere gerek yok." Çekmecedeki "Kanıt
// span'leri" listesi kaldırıldı; ipucu artık olmayan bir listeyi işaret
// etmez. Her varyant yalnız doğru olanı söyler: span → waterfall'daki kutu
// (v0.9.408), trace → exception çekmecesinin altındaki liste (duruyor),
// yalnız Oracle → hiçbir yerde listelenmez, ipucu yok.
describe('ExplainBody — Kanıt satırının ipucu (v0.10.1033)', () => {
  const line = (ev: { spans: number; traces: number; oracle?: number }) => {
    const h = renderToStaticMarkup(<ExplainBody text={TEXT} busy={false} evidence={ev} />);
    const m = h.match(/<div class="cx-evidence">([\s\S]*?)<\/div>/);
    return (m?.[1] ?? '').replace(/<[^>]+>/g, '').replace(/&#x27;|&#39;/g, "'");
  };
  it('trace (yalnız span): "waterfall\'da kutulu"; çekmece listesi denmez', () => {
    expect(line({ spans: 6, traces: 0 })).toBe("Kanıt: 6 span · waterfall'da kutulu");
    expect(line({ spans: 6, traces: 0 })).not.toContain('çekmecenin altında');
  });
  it('trace + Oracle: sayılar aynen, ipucu span\'lere ait', () => {
    expect(line({ spans: 19, traces: 0, oracle: 2 })).toBe("Kanıt: 19 span · 2 Oracle satırı · waterfall'da kutulu");
  });
  it('exception (yalnız trace): liste duruyor, ipucu doğru kalır', () => {
    expect(line({ spans: 0, traces: 2 })).toBe('Kanıt: 2 trace · kimlikler çekmecenin altında, satır satır');
  });
  it('ikisi birden: her kimlik türü kendi yerini söyler', () => {
    expect(line({ spans: 3, traces: 2 })).toBe("Kanıt: 3 span · 2 trace · span'ler waterfall'da kutulu, trace'ler çekmecenin altında");
  });
  it('yalnız Oracle: listelenecek kimlik yok → ipucu yok', () => {
    expect(line({ spans: 0, traces: 0, oracle: 1 })).toBe('Kanıt: 1 Oracle satırı');
  });
});

// v0.10.1396 (operatör: "yine rightarrow konusu") — Karar şeridi düz metin
// çizildiği için "$\rightarrow$" olduğu gibi görünüyordu; metin girişte
// normalize edilir ve Karar cümlesi gövdede tekrarlanmaz.
describe('ExplainBody Karar şeridi LaTeX oku (v0.10.1396)', () => {
  const T = [
    '**Kök Neden ve Sonraki Adım**',
    '- Kök Neden: orders-api $\\rightarrow$ db.example.test (UPDATE) $\\rightarrow$ ORA-00001.',
    '- Sonraki adım: kısıtı kontrol et.',
  ].join('\n');
  const html = renderToStaticMarkup(<ExplainBody text={T} busy={false} />);
  // v0.10.2374 — iki oklu Karar artık adım akışı (ol.cm-chain); ok metni DOM'da
  // durur, karşılaştırma etiketsiz METİN üzerinde.
  const plain = html.replace(/<[^>]+>/g, '');
  it('Karar → içerir, ham LaTeX yok', () => {
    const verdict = html.slice(html.indexOf('cx-verdict'), html.indexOf('</div>', html.indexOf('cx-verdict')));
    expect(verdict.replace(/<[^>]+>/g, '')).toContain('orders-api → db.example.test (UPDATE) → ORA-00001.');
    expect(verdict).toContain('cm-chain__s cm-chain__s--err');
    expect(html).not.toContain('rightarrow');
  });
  it('Karar cümlesi gövdede tekrarlanmaz', () => {
    expect(plain.split('orders-api → db.example.test').length - 1).toBe(1);
  });
});
