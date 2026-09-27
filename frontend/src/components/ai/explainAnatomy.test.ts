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

// v0.10.972 — operatör: "Kök neden olsun yine de" + "Stacktrace detayı bölümü
// de geri gelsin". Beş başlıklı cevabın «Olası neden»i «Kök neden» oldu; ilk
// satırı güven. Karar şeridi YALNIZ "Güven: kesin"de (kök neden cümlesiyle);
// "olası" bugünkü hipotez gibi (şerit yok, bölüm bütün). Güven satırı yoksa da
// şerit yok (kesinlik söylenmediyse hüküm çizilmez).
const KOK = (guven: string, body = '- payments-db bağlantı havuzu tükendi [L1][T2]. Havuz 50/50 dolu [P1].') => [
  '**Bulgu**', '- checkout POST /orders 1840 ms; payments hata döndü [T1].', '',
  '**Kanıt**', '- [T1] payments öz süre 1620 ms', '- [L1] PoolExhaustedException', '',
  '**Stacktrace detayı**', '- Sınıf/metot: `com.example.pay.PoolClient.acquire` [L1]', '- Tip: PoolExhaustedException [L1]', '',
  '**Kök neden**', guven, body, '',
  '**Eksik veri**', '- kanıt yok', '',
  '**Sonraki kontrol**', '- payments-db havuz metriği.',
].join('\n');

describe('verdictLine — inceleme cevabının «Kök neden»i (v0.10.972)', () => {
  it('Güven: kesin → Karar = kök neden cümlesi (güven satırı değil)', () => {
    expect(verdictLine(KOK('Güven: kesin'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
  });
  it('Güven: olası — <eksik halka> → Karar YOK (hipotez)', () => {
    expect(verdictLine(KOK('Güven: olası — pod metriği okunamadı, havuzun dolduğu doğrulanamadı'))).toBeNull();
  });
  it('güven satırı yoksa Karar YOK', () => {
    expect(verdictLine(KOK('- payments-db bağlantı havuzu tükendi [L1]. Havuz dolu [P1].', ''))).toBeNull();
  });
  it('yazım toleransı: madde/kalın güven, büyük harf, «**Kök neden:**», güven başlık satırında, boş satır', () => {
    expect(verdictLine(KOK('- **Güven:** kesin'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('Güven: Kesin'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('Güven: kesin').replace('**Kök neden**', '**Kök neden:**'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('').replace('**Kök neden**\n', '**Kök neden** Güven: kesin'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('Güven: kesin\n'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('*Güven:* olası — log yok'))).toBeNull();
    // v0.10.972 — istemin kendi başlık biçimi «**X** — …»: tire aynı satırda ya da sarkan
    expect(verdictLine(KOK('').replace('**Kök neden**\n', '**Kök neden** — Güven: kesin'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('Güven: kesin').replace('**Kök neden**', '**Kök neden** —'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
    expect(verdictLine(KOK('').replace('**Kök neden**\n', '**Kök neden** — Güven: olası — log yok'))).toBeNull();
    expect(verdictLine(KOK('Güven: olası — log yok').replace('**Kök neden**', '**Kök neden** —'))).toBeNull();
  });
  it('v0.10.972 — olumsuzlanan kesin («kesin değil» / «kesin olmayan») → Karar YOK', () => {
    expect(verdictLine(KOK('Güven: kesin değil — log yok'))).toBeNull();
    expect(verdictLine(KOK('**Güven:** kesin değil'))).toBeNull();
    expect(verdictLine(KOK('- **Güven:** kesin **değil**'))).toBeNull();
    expect(verdictLine(KOK('Güven: kesin olmayan'))).toBeNull();
    // gerekçeli kesin hâlâ Karar
    expect(verdictLine(KOK('Güven: kesin — zincir kesintisiz [T1][L1]'))).toBe('payments-db bağlantı havuzu tükendi [L1][T2].');
  });
  it('v0.10.972 — olası satırında "kesin"/"kesintisiz" geçse de Karar YOK (güven ilk kelimeden okunur)', () => {
    expect(verdictLine(KOK('Güven: olası — deploy [D1] ile hata arasında kesin bağ yok'))).toBeNull();
    expect(verdictLine(KOK('Güven: olası — zincir kesintisiz değil, pod metriği okunamadı'))).toBeNull();
  });
  it('v0.10.972 — güven kuralı şekilden bağımsız: **Bulgu** farklı yazılsa da güven satırı Karar olmaz', () => {
    const off = (head: string, guven: string) => [head, '- x.', '', '**Kök neden**', guven, '- a [L1]. B [P1].'].join('\n');
    expect(verdictLine(off('**Bulgular**', 'Güven: olası — y'))).toBeNull();
    expect(verdictLine(off('## Bulgu', 'Güven: olası — pod metriği yok'))).toBeNull();
    expect(verdictLine(off('- **Bulgu**: x [T1].', 'Güven: olası — pod metriği yok'))).toBeNull();
    expect(verdictLine(off('**Bulgular**', 'Güven: kesin değil'))).toBeNull(); // tanınmayan güven değeri de Karar değil
    expect(verdictLine(off('**Bulgular**', 'Güven: kesin'))).toBe('a [L1].');
    expect(verdictLine(off('## Bulgu', '- **Güven:** kesin'))).toBe('a [L1].');
  });
  it('kesin ama bölüm tek cümle → kopya olur, Karar YOK; bölüm yalnız güven satırıysa Karar YOK', () => {
    expect(verdictLine(KOK('Güven: kesin', '- payments-db bağlantı havuzu tükendi [L1].'))).toBeNull();
    expect(verdictLine(KOK('Güven: kesin', ''))).toBeNull();
  });
  it('eski önbellek cevabı («**Olası neden**» + güvensiz) → Karar YOK (v0.10.948 davranışı)', () => {
    expect(verdictLine(KOK('Güven: kesin').replace('**Kök neden**\nGüven: kesin', '**Olası neden**'))).toBeNull();
  });
});

describe('dropVerdictSentence — inceleme «Kök neden» (v0.10.972)', () => {
  it('kesin: cümle bölümden düşer, güven satırı ve kalan cümle yerinde; öteki bölümler aynen', () => {
    const t = KOK('Güven: kesin');
    const out = dropVerdictSentence(t, verdictLine(t)!);
    expect(out).toContain('**Kök neden**\nGüven: kesin\n- Havuz 50/50 dolu [P1].');
    expect(out).not.toContain('bağlantı havuzu tükendi');
    expect(out).toContain('**Stacktrace detayı**\n- Sınıf/metot: `com.example.pay.PoolClient.acquire` [L1]');
    expect(out).toContain('**Bulgu**\n- checkout POST /orders 1840 ms; payments hata döndü [T1].');
  });
  it('olası: metin AYNEN (çağrılsa bile)', () => {
    const t = KOK('Güven: olası — log yok');
    expect(dropVerdictSentence(t, 'payments-db bağlantı havuzu tükendi [L1][T2].')).toBe(t);
    // v0.10.972 — olası satırında "kesintisiz" geçse de; olumsuzlanan kesin de
    const t2 = KOK('Güven: olası — zincir kesintisiz değil');
    expect(dropVerdictSentence(t2, 'payments-db bağlantı havuzu tükendi [L1][T2].')).toBe(t2);
    const t3 = KOK('Güven: kesin değil — log yok');
    expect(dropVerdictSentence(t3, 'payments-db bağlantı havuzu tükendi [L1][T2].')).toBe(t3);
  });
  it('v0.10.972 — «**Kök neden** — Güven: kesin» başlık satırı yerinde, cümle düşer', () => {
    const t = KOK('').replace('**Kök neden**\n', '**Kök neden** — Güven: kesin');
    const out = dropVerdictSentence(t, verdictLine(t)!);
    expect(out).toContain('**Kök neden** — Güven: kesin\n- Havuz 50/50 dolu [P1].');
    expect(out).not.toContain('tükendi');
  });
  it('v0.10.972 — şekil dışı cevapta kesin: güven satırı kalır, kök neden cümlesi düşer; olası: AYNEN', () => {
    const k = '**Bulgular**\n- x.\n\n**Kök neden**\nGüven: kesin\n- a [L1]. B [P1].';
    expect(dropVerdictSentence(k, verdictLine(k)!)).toBe('**Bulgular**\n- x.\n\n**Kök neden**\nGüven: kesin\n- B [P1].');
    const o = '**Bulgular**\n- x.\n\n**Kök neden**\nGüven: olası — y\n- a [L1]. B [P1].';
    expect(dropVerdictSentence(o, 'Güven: olası — y')).toBe(o);
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
  it('v0.10.972 — bloktan sonra gelen «Kök neden» / «Stacktrace detayı» başlığı gövdede kalır', () => {
    const r = splitSourceFooter('**Bulgu**\n- x\n\n**Kaynak durumu**\n- logs: boş\n**Stacktrace detayı**\n- a [L1]\n**Kök neden**\nGüven: kesin');
    expect(r.footer).toBe('**Kaynak durumu**\n- logs: boş');
    expect(r.body).toContain('**Stacktrace detayı**\n- a [L1]\n**Kök neden**\nGüven: kesin');
  });
  it('blok yoksa metin AYNEN (null footer)', () => {
    expect(splitSourceFooter(FIVE)).toEqual({ body: FIVE, footer: null });
  });
  it('başlık biçimleri: ## ve düz satır', () => {
    expect(splitSourceFooter('a\n## Kaynak durumu\n- x').footer).toBe('## Kaynak durumu\n- x');
    expect(splitSourceFooter('a\nKAYNAK DURUMU: x').body).toBe('a');
  });
});
