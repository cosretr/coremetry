// apiErrorDetail.test.ts — v0.10.835: request() hata gövdesini
// `HTTP 409: {json}` METNİNE gömer, yani yarım kalan bir yönetim işleminin
// KOŞAN DDL adımları erişilemez hâlde kalıyordu (MV hedef uuid onarımı).
//
// Sözleşme: gövde JSON ve `error` taşıyorsa mesaj O'dur (ham HTTP gürültüsü
// değil) ve `steps` dizisi çıkar; gövde JSON değilse ya da beklenmedik
// şekilliyse HAM mesaj aynen döner — hiçbir hata YUTULMAZ.
import { describe, expect, it } from 'vitest';
import { apiErrorDetail } from './api';

describe('apiErrorDetail', () => {
  it('409 gövdesinden mesajı ve KOŞAN adımları çıkarır', () => {
    const body = JSON.stringify({
      error: 'YARIM — 2. şart TUTMADI: düğüm-yerel okuma hâlâ düşüyor',
      steps: ['DROP TABLE IF EXISTS `.inner_id.x` SYNC', 'CREATE TABLE `.inner_id.x` UUID \'y\' …'],
    });
    const d = apiErrorDetail(new Error(`HTTP 409: ${body}`));
    expect(d.message).toBe('YARIM — 2. şart TUTMADI: düğüm-yerel okuma hâlâ düşüyor');
    expect(d.message).not.toContain('HTTP 409');
    expect(d.steps).toHaveLength(2);
    expect(d.steps?.[0]).toContain('DROP TABLE');
  });

  it('adımsız hata gövdesinde yalnız mesaj döner', () => {
    const d = apiErrorDetail(new Error('HTTP 400: {"error":"confirm:true zorunlu — bu uç DDL koşar"}'));
    expect(d.message).toBe('confirm:true zorunlu — bu uç DDL koşar');
    expect(d.steps).toBeUndefined();
  });

  it('boş steps dizisi undefined olur (boş liste çizilmesin)', () => {
    const d = apiErrorDetail(new Error('HTTP 409: {"error":"olmaz","steps":[]}'));
    expect(d.message).toBe('olmaz');
    expect(d.steps).toBeUndefined();
  });

  it('steps içindeki metin OLMAYAN öğeler elenir', () => {
    const d = apiErrorDetail(new Error('HTTP 409: {"error":"x","steps":["DROP …",null,7]}'));
    expect(d.steps).toEqual(['DROP …']);
  });

  // Hiçbir hata YUTULMAZ: ayrıştırılamayan gövde HAM hâliyle görünür,
  // yoksa operatör "boş hata" görür ve olay kaybolur.
  it('JSON olmayan gövde HAM mesajla döner', () => {
    const raw = 'HTTP 502: <html>bad gateway</html>';
    expect(apiErrorDetail(new Error(raw))).toEqual({ message: raw });
  });

  it('bozuk JSON HAM mesajla döner', () => {
    const raw = 'HTTP 409: {"error":';
    expect(apiErrorDetail(new Error(raw))).toEqual({ message: raw });
  });

  it('HTTP öneki olmayan hata (ağ/iptal) olduğu gibi kalır', () => {
    expect(apiErrorDetail(new Error('Failed to fetch')).message).toBe('Failed to fetch');
    expect(apiErrorDetail('düz metin').message).toBe('düz metin');
  });

  it('error alanı boşsa ham mesaja düşer (sessiz boş mesaj YOK)', () => {
    const raw = 'HTTP 409: {"error":"","steps":["a"]}';
    expect(apiErrorDetail(new Error(raw)).message).toBe(raw);
  });
});
