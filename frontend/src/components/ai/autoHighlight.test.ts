// v0.10.1567 — CoSRE cevabında modelden bağımsız otomatik vurgu
// (autoHighlight.ts). Girdiler operatörün prod ekran görüntüsündeki
// qwen3.8-flash cevabından; yanlış-pozitif tablosu Türkçe düzyazıdan.
import { describe, it, expect } from 'vitest';
import { highlightParts } from './autoHighlight';

const hits = (s: string) => highlightParts(s).filter(p => p.kind).map(p => `${p.kind}:${p.v}`);

describe('highlightParts — prod cevabındaki kritik parçalar', () => {
  it.each([
    ['CreditCardDetailsService.java:151\'de NPE', ['code:CreditCardDetailsService.java:151']],
    ['com.acme.wallet.card.service.CreditCardDetails.CreditCardDetailsService (x)', ['code:com.acme.wallet.card.service.CreditCardDetails.CreditCardDetailsService']],
    ['DTOCardDetail.getTdrkLimArr() null olduğu için', ['code:DTOCardDetail.getTdrkLimArr()']],
    ['POST /api/card/crdCard/getCardLimitInfo çağrısı HTTP 500 döndü', ['code:POST /api/card/crdCard/getCardLimitInfo', 'err:HTTP 500']],
    ['NullPointerException üretildi; ACMRuntimeException: "ACM_020"', ['err:NullPointerException', 'err:ACMRuntimeException', 'err:ACM_020']],
    ['error_code=MCA-00029 (Oracle)', ['err:MCA-00029']],
    ['com.acme.cards.util.util.OceanBackendFeignBase$FeignClientExceptionHandler.decode', ['code:com.acme.cards.util.util.OceanBackendFeignBase$FeignClientExceptionHandler.decode']],
  ])('%s', (input, want) => {
    expect(hits(input)).toEqual(want);
  });
});

describe('highlightParts — düzyazıda yanlış çip yok', () => {
  it.each([
    'Window 28.10.2026 19:10:10.443 → 19:10:10.529',
    'sürüm 1.4.2 dağıtıldı, oran %3 arttı',
    'Kritik hata noktası: servis sahibi ile incele.',
    'Error oranı yükseldi ama istek başarılı döndü',
    'Kanıt: 2 span · 1 Oracle satırı',
    // v0.10.2374 — yeni türlerin Türkçe düzyazı negatifleri
    '19.11.2026 tarihinde saat 21:02:24 itibarıyla sürüm 1.4.2 yayında',
    "kök süresinin %67'si, 2 span ve 5 saat içinde 3 servis",
    'e-posta ile al-sat yapan çok-uluslu ekip',
    'end-to-end test ve out-of-memory durumu',
    'değer-a-b-c ve İş-akış-adı gibi Türkçe harfli tireli sözcükler',
    'trace 2d2fe61f-1c5b-478c-b32f-9545375bcbba ve a1b2c3d4-1c5b-478c-b32f-9545375bcbba',
    'p50 4.8, ×1.9 ve 24 saat; a >= b, x == y',
    'tek " işareti ve 5" disk ile 7" disk',
    'adres /api/get-card-limit-info yolunda, --max-old-space bayrağı',
    'Sonuç: A harfi ve B_ ile _C; ID ve URL kısaltmaları',
    'değer=3 ve ölçü=5 gibi Türkçe harfli anahtarlar',
    'dedi ki " arası boşluklu " metin',
    'ab',
  ])('%s', input => {
    expect(hits(input)).toEqual([]);
  });

  it('metni kaybetmez: parçalar birleşince girdinin aynısı', () => {
    const s = 'x CreditCardDetails.java:151 y HTTP 503 z MCA-00029.';
    expect(highlightParts(s).map(p => p.v).join('')).toBe(s);
    expect(highlightParts('')).toEqual([{ v: '' }]);
  });
});

// v0.10.2374 — operatör: trace açıklamasında servis adı, çağrı, anahtar=değer,
// süre ve tırnaklı mesaj düz metindi. Girdiler o cevabın şeklinden (nötr adlarla).
const flat = (s: string) => highlightParts(s).filter(p => p.kind).map(p => `${p.kind}:${p.v}`);

describe('highlightParts — v0.10.2374 yeni türler', () => {
  it.each([
    ['orders-api-prod servisinden çağrı', ['ent:orders-api-prod']],
    ['customer-rel-prod-grpc.customer.svc.cluster.local üzerinden', ['ent:customer-rel-prod-grpc.customer.svc.cluster.local']],
    ["orders-api-prod'un cevabı; sonra checkout-web-prod.", ['ent:orders-api-prod', 'ent:checkout-web-prod']],
    ['CUSTOMER_INFO_GET_RELATIONS için db0p.REL_TABLE_GER tablosu', ['code:CUSTOMER_INFO_GET_RELATIONS', 'code:db0p.REL_TABLE_GER']],
    ['ACM_020 hata kodu, STATUS_CODE_ERROR durumu', ['err:ACM_020', 'err:STATUS_CODE_ERROR']],
    ['requestId=2d2fe61f-1c5b-478c-b32f-9545375bcbba · called=CUSTOMER_INFO_GET_RELATIONS', ['kv:requestId=2d2fe61f-1c5b-478c-b32f-9545375bcbba', 'kv:called=CUSTOMER_INFO_GET_RELATIONS']],
    ['pilot request mbb=null, sube=null.', ['kv:mbb=null', 'kv:sube=null']],
    ['fallback reason=Service gate closed', ['kv:reason=Service']],
    ['(http.status_code=200) error_code=MCA-00029', ['kv:http.status_code=200', 'err:MCA-00029']],
    ['gRPC status error döndü ama HTTP 200 (ok), HTTP 302', ['err:status error', 'code:HTTP 200', 'code:HTTP 302']],
    ['196 ms · öz 131 ms · 9.1 ms · 12 µs · 3 sn · 24 sa · 5 dk · 2 s', ['dur:196 ms', 'dur:131 ms', 'dur:9.1 ms', 'dur:12 µs', 'dur:3 sn', 'dur:24 sa', 'dur:5 dk', 'dur:2 s']],
    ['com.example.core.grpc.BagService/callBagService çağrısı', ['code:com.example.core.grpc.BagService/callBagService']],
    ['BagService/callBagService ve RelationService.getRelations içinde', ['code:BagService/callBagService', 'code:RelationService.getRelations']],
    ['com.example.core.exception.AppException: "Herhangi bir kayıt bulunamadı".', ['code:com.example.core.exception.AppException', 'msg:"Herhangi bir kayıt bulunamadı"']],
    ['“Kayıt yok” dedi, reason="Service gate closed"', ['msg:“Kayıt yok”', 'msg:"Service gate closed"']],
  ])('%s', (input, want) => {
    expect(flat(input)).toEqual(want);
    expect(highlightParts(input).map(p => p.v).join('')).toBe(input);
  });

  it('tırnak içi TEK çipse mesaj değil, çip kazanır; karışık içerikte çip mesajın içinde kalır', () => {
    expect(highlightParts('kod "ACM_020" döndü')).toEqual([
      { v: 'kod ' }, { v: '"' }, { v: 'ACM_020', kind: 'err' }, { v: '"' }, { v: ' döndü' },
    ]);
    const [m] = highlightParts('"orders-api-prod 196 ms sürdü"');
    expect(m.kind).toBe('msg');
    expect(m.c?.filter(p => p.kind).map(p => `${p.kind}:${p.v}`)).toEqual(['ent:orders-api-prod', 'dur:196 ms']);
    expect(m.c?.map(p => p.v).join('')).toBe(m.v);
  });

  it('tırnak sınırı: 3–200 karakter, tek satır', () => {
    expect(flat('"ab" kısa')).toEqual([]);
    expect(flat(`"${'x'.repeat(201)}"`)).toEqual([]);
    expect(flat('"ilk satır\nikinci satır"')).toEqual([]);
    expect(flat('"abc"')).toEqual(['msg:"abc"']);
  });
});
