// ExplainBody.test.tsx — v0.10.165: kart anatomisi çalışma zamanında —
// Karar satırı, Kanıt satırı, oluklu kod bloğu (satır numarası sütunu, hata
// satırı vurgusu, dosya başlığı, mapper etiketi) gövdeden ÖNCE; stack çiti
// yerinde ve katlı; akış sürerken anatomi yok.
import { describe, it, expect } from 'vitest';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
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
    expect(html).toContain('LedgerEntryDao.insertBatch 246. satırda eski kolona yazıyor.');
    expect(html.split('246. satırda eski kolona yazıyor.').length - 1).toBe(1);
    expect(html).toContain('Kontrol: mapper.');
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

// v0.10.948 (CoSRE Faz B) — gövdenin altındaki iki deterministik satır:
// `id`siz kanıt linkleri ve `sources` kaynak dipnotu. Akış SÜRERKEN ikisi de
// yok (yarım cevabın altında "kaynak durumu" erken olurdu); `sources` varken
// sunucunun metin bloğu gövdeden ayrılır.
describe('ExplainBody — kanıt linkleri + kaynak dipnotu (v0.10.948)', () => {
  const BODY = '**Bulgu**\n- checkout yavaş.\n\n**Kaynak durumu**\n- traces: başarılı';
  const links = [{ label: 'Servis', href: '/service?name=checkout&env=prod' }];
  const sources = [{ source: 'traces', state: 'ok' }, { source: 'logs', state: 'timeout' }];
  const render = (busy: boolean) => renderToStaticMarkup(
    <MemoryRouter><ExplainBody text={BODY} busy={busy} links={links} sources={sources} /></MemoryRouter>);
  it('bitmiş cevap: link satırı + dipnot; metin bloğu tek kez', () => {
    const h = render(false);
    expect(h).toContain('aria-label="Kanıt linkleri"');
    expect(h).toContain('href="/service?name=checkout&amp;env=prod"');
    expect(h).toContain('cx-sources');
    expect(h).toContain('logs · zaman aşımı');
    expect(h).not.toContain('traces: başarılı');
  });
  it('akış sürerken ikisi de YOK, metin ham akar', () => {
    const h = render(true);
    expect(h).not.toContain('Kanıt linkleri');
    expect(h).not.toContain('cx-sources');
    expect(h).toContain('traces: başarılı');
  });
});

// v0.10.972 — operatör: "Kök neden olsun yine de" + "Stacktrace detayı bölümü
// de geri gelsin". İnceleme cevabının «Kök neden»i "Güven: kesin" ise Karar
// şeridi kök neden cümlesiyle çizilir (cümle gövdede tekrarlanmaz, güven
// satırı bölümde kalır); "olası" ise şerit yok, bölüm bütün ve yerinde.
describe('ExplainBody — inceleme «Kök neden» güveni (v0.10.972)', () => {
  const answer = (guven: string) => [
    '**Bulgu**', '- checkout POST /orders 1840 ms; payments hata döndü [T1].', '',
    '**Kanıt**', '- [L1] PoolExhaustedException', '',
    '**Stacktrace detayı**', '- Sınıf/metot: com.example.pay.PoolClient.acquire [L1]', '- Katman: backend [L1]', '',
    '**Kök neden**', guven, '- payments-db bağlantı havuzu tükendi [L1]. Havuz 50/50 dolu [P1].', '',
    '**Eksik veri**', '- kanıt yok', '',
    '**Sonraki kontrol**', '- payments-db havuz metriği.',
  ].join('\n');
  it('kesin → Karar şeridi kök neden cümlesiyle; cümle bir kez; güven satırı ve Stacktrace detayı gövdede', () => {
    const h = renderToStaticMarkup(<ExplainBody text={answer('Güven: kesin')} busy={false} />);
    expect(h).toContain('cx-verdict');
    expect(h).toMatch(/Karar<\/span>payments-db bağlantı havuzu tükendi \[L1\]\./);
    expect(h.split('bağlantı havuzu tükendi').length - 1).toBe(1);
    expect(h).toContain('Güven: kesin');
    expect(h).toContain('Havuz 50/50 dolu [P1].');
    expect(h).toContain('Stacktrace detayı');
    expect(h.indexOf('Stacktrace detayı')).toBeLessThan(h.indexOf('Güven: kesin'));
  });
  it('olası → Karar YOK; bölüm bütün (cümle gövdede, güven satırıyla)', () => {
    const h = renderToStaticMarkup(<ExplainBody text={answer('Güven: olası — pod metriği okunamadı')} busy={false} />);
    expect(h).not.toContain('cx-verdict');
    expect(h).toContain('Güven: olası — pod metriği okunamadı');
    expect(h).toContain('payments-db bağlantı havuzu tükendi [L1]. Havuz 50/50 dolu [P1].');
    // v0.10.972 — eksik halkada "kesintisiz" geçse de olası; olumsuzlanan kesin de Karar değil
    for (const g of ['Güven: olası — zincir kesintisiz değil', 'Güven: kesin değil — log yok']) {
      const h2 = renderToStaticMarkup(<ExplainBody text={answer(g)} busy={false} />);
      expect(h2).not.toContain('cx-verdict');
      expect(h2).toContain('payments-db bağlantı havuzu tükendi [L1]. Havuz 50/50 dolu [P1].');
    }
  });
  it('v0.10.972 — istemin «**Kök neden** — Güven: kesin» biçimi de Karar çizer', () => {
    const h = renderToStaticMarkup(<ExplainBody text={answer('').replace('**Kök neden**\n', '**Kök neden** — Güven: kesin')} busy={false} />);
    expect(h).toContain('cx-verdict');
    expect(h).toMatch(/Karar<\/span>payments-db bağlantı havuzu tükendi \[L1\]\./);
    expect(h.split('bağlantı havuzu tükendi').length - 1).toBe(1);
  });
});
