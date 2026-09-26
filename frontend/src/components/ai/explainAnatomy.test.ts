// explainAnatomy.test.ts — v0.10.165 sözleşmesi (explainAnatomy.ts başlığı).
import { describe, it, expect } from 'vitest';
import { verdictLine, hoistCodeQuotes, dropVerdictSentence, splitSourceFooter } from './explainAnatomy';

const ANSWER = [
  '**Hata ve Anlamı**',
  '- `PSQLException`: column "settlement_batch_id" does not exist.',
  '',
  '**Kök Neden ve Sonraki Adım**',
  '- **LedgerEntryDao.insertBatch** 246. satırda `settlement_batch_id` kolonuna yazıyor; migrasyon kolonu yeniden adlandırdı. Kontrol: `LedgerEntryMapper.xml:34`.',
  '  ```java',
  '  // src/main/java/com/payments/ledger/dao/LedgerEntryDao.java:240-252',
  '  240| public List<LedgerEntry> insertBatch(List<LedgerEntry> entries) {',
  '  >>> 246|   m.insert(e);',
  '  252| }',
  '  ```',
  '- Sonraki adım: mapper ve migrasyonu eşle.',
  '',
  '**Stacktrace Detayı**',
  '```',
  'org.postgresql.util.PSQLException: ERROR: column',
  '\tat org.postgresql.core.v3.QueryExecutorImpl.receiveErrorResponse(QueryExecutorImpl.java:2733)',
  '```',
].join('\n');

describe('verdictLine', () => {
  it('Kök Neden bölümünün ilk cümlesi, markdown soyulmuş', () => {
    expect(verdictLine(ANSWER)).toBe('LedgerEntryDao.insertBatch 246. satırda settlement_batch_id kolonuna yazıyor; migrasyon kolonu yeniden adlandırdı.');
  });
  it('düz «Olası neden:» (problem türü, prompts.go:172 — kalın DEĞİL) tanınır; bölüm tek cümleyse KOPYA → null', () => {
    expect(verdictLine('Olası neden: ledger-writer v2.14.0 deploy sonrası hata oranı arttı. Kanıt: deploy 13:52.')).toBe('ledger-writer v2.14.0 deploy sonrası hata oranı arttı.');
    expect(verdictLine('Özet: x.\nOlası neden: ledger-writer deploy sonrası hata oranı arttı.')).toBeNull();
    expect(verdictLine('**Olası neden:** ledger-writer deploy sonrası hata oranı arttı. Kanıt: deploy 13:52.')).toBe('ledger-writer v2.14.0 deploy sonrası hata oranı arttı.'.replace('v2.14.0 ', ''));
    // başlıksız türler (span/incident/anomaly/service-health: "no headers") → Karar yok
    expect(verdictLine('- gecikme 3× arttı.\n- db_query yavaş.')).toBeNull();
    expect(verdictLine('hiç başlık yok')).toBeNull();
    expect(verdictLine(null)).toBeNull();
  });
  // v0.10.948 — beş başlıklı inceleme cevabında «Olası neden» HİPOTEZ: iki
  // cümle olsa da Karar şeridine çıkmaz (sıra Bulgu → Kanıt → Olası neden
  // korunur; «ilişki, neden değil» kaydı iddiasından kopmaz). Yukarıdaki
  // inline «**Olası neden:**» ve Kök Neden örnekleri **Bulgu** taşımaz → Karar sürer.
  it('inceleme şekli (**Bulgu** başlığı) → Karar YOK (v0.10.948)', () => {
    const inv = [
      '**Bulgu**', '- checkout POST /orders 1840 ms.', '',
      '**Kanıt**', '- [T1] payments öz süre 1620 ms', '',
      '**Olası neden**', '- payments bağlantı havuzu dolu görünüyor. Aynı pencerede deploy var (ilişki, neden değil).', '',
    ].join('\n');
    expect(verdictLine(inv)).toBeNull();
    expect(verdictLine(inv.replace('**Bulgu**', '**Bulgu:**'))).toBeNull();
    // aynı Olası neden bölümü **Bulgu**'suz → klasik Karar (kontrol)
    expect(verdictLine(inv.split('\n').slice(6).join('\n'))).toBe('payments bağlantı havuzu dolu görünüyor.');
  });
});

describe('dropVerdictSentence', () => {
  it('madde içindeki ilk cümle düşer, kalan (kalın/kod işaretleriyle) madde olarak kalır', () => {
    const v = verdictLine(ANSWER)!;
    const out = dropVerdictSentence(ANSWER, v);
    expect(out).not.toContain('kolonu yeniden adlandırdı.');
    expect(out).toContain('- Kontrol: `LedgerEntryMapper.xml:34`.');
    expect(out).toContain('**Hata ve Anlamı**');
    expect(out).toContain('  ```java'); // çit dokunulmadı
  });
  it('satır cümleden ibaretse satır gider; inline «Olası neden:» başlığı kalan cümleyle sürer; bulunamazsa aynen', () => {
    expect(dropVerdictSentence('**Kök Neden**\n- **X** çöktü.\n- Sonraki adım: y.', 'X çöktü.')).toBe('**Kök Neden**\n- Sonraki adım: y.');
    expect(dropVerdictSentence('Olası neden: deploy sonrası arttı. Kanıt: 13:52.', 'deploy sonrası arttı.')).toBe('Olası neden: Kanıt: 13:52.');
    expect(dropVerdictSentence('başlık yok', 'x.')).toBe('başlık yok');
  });
});

describe('hoistCodeQuotes', () => {
  it('oluklu/başlıklı çit Kanıt altına taşınır, yerinde işaret kalır; stack çiti yerinde kalır', () => {
    const { quotes, rest } = hoistCodeQuotes(ANSWER);
    expect(quotes.length).toBe(1);
    expect(quotes[0].lang).toBe('java');
    expect(quotes[0].lines[0]).toBe('// src/main/java/com/payments/ledger/dao/LedgerEntryDao.java:240-252');
    expect(quotes[0].lines[2]).toBe('>>> 246|   m.insert(e);');
    expect(rest).not.toContain('246|');
    expect(rest).toContain('  *↑ kod alıntısı yukarıda*');
    expect(rest).toContain('**Stacktrace Detayı**');
    expect(rest).toContain('QueryExecutorImpl.java:2733');
    expect(rest).toContain('- Sonraki adım: mapper ve migrasyonu eşle.');
  });
  it('kapanmamış çit taşınmaz; çitsiz metin aynen', () => {
    const t = 'metin\n```java\n240| a\n246| b';
    expect(hoistCodeQuotes(t)).toEqual({ quotes: [], rest: t });
    expect(hoistCodeQuotes('düz')).toEqual({ quotes: [], rest: 'düz' });
  });
});

// v0.10.948 (CoSRE Faz B) — sunucunun "Kaynak durumu" metin dipnotu, yapısal
// `sources` varken gövdeden ayrılır (kart rozetle çizer); sayı denetimi
// uyarısı ve sonraki bölümler gövdede KALIR.
describe('splitSourceFooter (v0.10.948)', () => {
  const FIVE = [
    '**Bulgu**', '- checkout POST /orders 1840 ms; payments çağrısı hata.', '',
    '**Kanıt**', '- [T1] payments self 1620 ms', '',
    '**Olası neden**', '- payments bağlantı havuzu; aynı pencerede deploy var (ilişki).', '',
    '**Eksik veri**', '- logs: erişilemedi', '',
    '**Sonraki kontrol**', '- /service?name=payments&env=prod', '',
  ].join('\n');
  it('sondaki blok (--- ayracıyla) ayrılır; beş bölüm aynen', () => {
    const r = splitSourceFooter(FIVE + '---\n**Kaynak durumu**\n- traces/clickhouse: başarılı\n- logs/elasticsearch: kaynağa erişilemedi (eksik veri)');
    expect(r.footer).toContain('logs/elasticsearch');
    expect(r.footer!.startsWith('**Kaynak durumu**')).toBe(true);
    expect(r.body).toContain('**Sonraki kontrol**');
    expect(r.body).not.toContain('Kaynak durumu');
    expect(r.body).not.toMatch(/---\s*$/);
  });
  it('sayı denetimi uyarısı (⚠) gövdede kalır — önce ya da sonra', () => {
    const after = splitSourceFooter(FIVE + '**Kaynak durumu**\n- traces: başarılı\n\n⚠ Kanıtta bulunamayan sayı(lar): 1900 ms');
    expect(after.body).toContain('⚠ Kanıtta bulunamayan sayı(lar): 1900 ms');
    expect(after.footer).not.toContain('⚠');
    const before = splitSourceFooter(FIVE + '⚠ Kanıtta bulunamayan sayı(lar): 12%\n\nKaynak durumu: traces ok · logs erişilemedi');
    expect(before.body).toContain('⚠ Kanıtta bulunamayan');
    expect(before.footer).toBe('Kaynak durumu: traces ok · logs erişilemedi');
  });
  it('SON başlık alınır; bloktan sonraki cevap başlığı gövdede kalır', () => {
    const r = splitSourceFooter('**Kaynak durumu** (model)\nmetin\n\n**Kaynak durumu**\n- logs: boş\n**Sonraki kontrol**\n- sonra');
    expect(r.footer).toBe('**Kaynak durumu**\n- logs: boş');
    expect(r.body).toContain('**Kaynak durumu** (model)');
    expect(r.body).toContain('**Sonraki kontrol**\n- sonra');
  });
  it('kalın madde satırları ("**logs/elasticsearch**: …") blokta kalır', () => {
    const r = splitSourceFooter('**Bulgu**\n- x\n\n**Kaynak durumu**\n**traces/clickhouse**: başarılı\n**logs/elasticsearch**: erişilemedi — eksik veri');
    expect(r.body).toBe('**Bulgu**\n- x');
    expect(r.footer).toContain('**logs/elasticsearch**: erişilemedi');
  });
  it('blok yoksa metin AYNEN (null footer)', () => {
    expect(splitSourceFooter(FIVE)).toEqual({ body: FIVE, footer: null });
  });
  it('başlık biçimleri: ## ve düz satır', () => {
    expect(splitSourceFooter('a\n## Kaynak durumu\n- x').footer).toBe('## Kaynak durumu\n- x');
    expect(splitSourceFooter('a\nKAYNAK DURUMU: x').body).toBe('a');
  });
});
