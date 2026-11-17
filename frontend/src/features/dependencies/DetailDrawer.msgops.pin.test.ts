// DetailDrawer.msgops.pin.test.ts — v0.10.563 (Messaging Faz 4b).
//
// Neden dosya-metni testi: bölümün DOĞRU YERDE olması bir sözleşme ve
// hiçbir saf fonksiyon onu tutmuyor. Aynı sınıfın emsali
// DetailDrawer.kafka.pin.test.ts (v0.10.551) — orada da yerleşim
// operatör onayıyla sabitlenmişti ve tek bir JSX taşımasıyla sessizce
// kayabilirdi ("test edilmiş ama ulaşılamaz" sınıfı: hook yeşil, ekranda
// yok).
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { t } from '@/lib/i18n';

const drawer = readFileSync(resolve(__dirname, 'DetailDrawer.tsx'), 'utf8');

describe('Operasyonlar · MV bölümü (v0.10.563)', () => {
  it('queue dalında: Consumers SONRASI, Kafka topic metrikleri ÖNCESİ (v0.10.1557)', () => {
    const consumers = drawer.indexOf('title={`Consumers ·');
    // v0.10.2311 — başlık katalogda (dd.opsHead; TR değeri aynı).
    const sec = drawer.indexOf("tf('dd.opsHead', lang");
    const kafka = drawer.indexOf('<KafkaTopicMetrics');
    expect(t('dd.opsHead', 'tr')).toBe('Operasyonlar · MV · {n} satır');
    expect(consumers).toBeGreaterThan(-1);
    expect(sec).toBeGreaterThan(consumers);
    expect(sec).toBeLessThan(kafka);
  });

  it('tablo paylaşılan primitifi kullanıyor, storageKey deps-msg-ops', () => {
    expect(drawer).toContain("storageKey: 'deps-msg-ops'");
    expect(drawer).toContain('<DataTableColgroup dt={msgOpsDt} />');
    expect(drawer).toContain('<DataTableHead dt={msgOpsDt} />');
    // Varsayılan sıralama: en çok çağrılan operasyon üstte.
    expect(drawer).toContain("initialSort: { id: 'count', dir: 'desc' }");
    // v0.9.1332 çıkmaz sokağı: sürüklenen genişliğin geri dönüşü —
    // v0.10.939 (tablo standardı S8) DataTableHead'in ⋯ menüsünde; sayfa
    // başına düğme yok (resetLayoutAdoption.test).
    expect(drawer).not.toContain('<ResetLayout' + 'Button');
  });

  it('hook erken dönüşlerden ÖNCE — rules-of-hooks (v0.9.873 tuzağı)', () => {
    const hook = drawer.indexOf("storageKey: 'deps-msg-ops'");
    const earlyReturn = drawer.indexOf('if (data === undefined) return <Spinner />;');
    expect(earlyReturn).toBeGreaterThan(-1);
    expect(hook).toBeLessThan(earlyReturn);
  });

  it("eksik operation '(yaymıyor)' diye yazılır, boş hücre bırakılmaz", () => {
    // v0.10.2311 — çekmece arayüz dilini geçer (TR çıktısı aynı).
    expect(drawer).toContain('opLabelTR(o.operation, lang)');
    expect(drawer).toContain('opMissingTitle(lang)');
    const helper = readFileSync(resolve(__dirname, 'msgOperations.ts'), 'utf8');
    // v0.10.2306 — metinler katalogda (TR değerleri aynı).
    expect(helper).toContain("t('mt.opMissing', 'tr')");
    expect(helper).toContain("t('mt.opMissingTitle', 'tr')");
    expect(t('mt.opMissing', 'tr')).toBe('(yaymıyor)');
    expect(t('mt.opMissingTitle', 'tr')).toBe('SDK messaging.operation.type/.name/.operation yaymıyor');
  });

  it('boş dilimde bölüm GİZLENMEZ — soluk satır yazar', () => {
    // v0.10.954 (tablo standardı T12) — soluk satır artık tablonun İÇİNDE
    // (DataTableState empty); başlık ve sütunlar durur.
    expect(drawer).toContain(`<DataTableState dt={msgOpsDt} kind="empty" message={t('dd.opsEmpty', lang)} />`);
    expect(t('dd.opsEmpty', 'tr')).toBe("Bu pencerede MV'de operasyon satırı yok");
    // <Empty> bloğu DEĞİL: bölüm başlığı ve satır sayısı görünür kalmalı.
    expect(drawer).not.toContain('Operasyon kırılımı yok');
  });

  it('operasyon TÜRÜ için trace pivotu YOK (span adı değil)', () => {
    // messagingTracesHref'in `operation` parametresi span ADINA çevriliyor;
    // buradaki değer operasyon TÜRÜ. Eşitleyen bir link ölü olurdu.
    const sec = drawer.indexOf("tf('dd.opsHead', lang");
    expect(sec).toBeGreaterThan(-1);
    const kafka = drawer.indexOf('<KafkaTopicMetrics');
    const block = drawer.slice(sec, kafka);
    expect(block).not.toContain('messagingTracesHref(');
  });
});
