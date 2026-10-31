// v0.10.1720 — Operatör: CoSRE Explain listesinde "Etiket: değer" maddelerinde
// etiket değerden ayırt edilemiyordu (aynı ağırlık + renk). Çizici etiketi
// splitListLabel ile ayırıp yarı-kalın basar; bu tablo sınırı çiviler.
import { describe, it, expect } from 'vitest';
import { splitListLabel } from './listLabel';

describe('splitListLabel — etiket olan maddeler', () => {
  it.each([
    ['Operasyon: POST /v1/orders orders-api üzerinde', 'Operasyon:'],
    ['Kritik hata noktası: com.acme.orders.OrderService.place()', 'Kritik hata noktası:'],
    ['İş verisi: sipariş tutarı ve müşteri kimliği', 'İş verisi:'],
    ['En yavaş bileşen: orders-db (412 ms)', 'En yavaş bileşen:'],
    ['Hata zinciri: orders-api → payments-api → HTTP 500', 'Hata zinciri:'],
    ['Kritik hata noktası: servis sahibi ile incele.', 'Kritik hata noktası:'],
    ['Kanıt: 2 span · 1 Oracle satırı', 'Kanıt:'],
  ])('%s', (input, label) => {
    const r = splitListLabel(input);
    expect(r?.label).toBe(label);
    // metin kaybolmaz: parçalar birleşince girdinin aynısı
    expect(r && r.label + r.rest).toBe(input);
  });
});

describe('splitListLabel — etiket OLMAYAN maddeler', () => {
  it.each([
    'Stacktrace Detayı',
    'Stacktrace Detayı:',
    'Window 28.10.2026 19:10:10.443 → 19:10:10.529',
    'Window 28.10.2026 19:10: x',
    'sürüm 1.4.2 dağıtıldı, oran %3 arttı',
    'Error oranı yükseldi ama HTTP 200 döndü',
    'orders-api servisinden çağrı',
    'com.acme.Foo.bar: x',
    'GET /v1/orders: 500',
    'request_id=abc: x',
    'https://example.com/x: y',
    '**Operasyon:** zaten kalın',
    '`code`: x',
    'Bu çok uzun bir etiket olur mu acaba: x',
    'Çokuzuntekparçaetiketaaaaaaaaaaaaa: x',
    'Oran (p95): 3 sn',
    'Durum "açık": x',
    'Akış → sonuç: x',
    'Operasyon:değer',
    '',
  ])('%s', input => {
    expect(splitListLabel(input)).toBeNull();
  });
});
