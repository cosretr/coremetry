import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

// k5PagesAM.pin.test.ts — v0.10.929 (K5 artıkları, üst düzey sayfalar A–M).
//
// KURAL (operatör kararı K5, v0.10.920/922): sağlıklı/normal durum NÖTR.
// Yeşil yalnız bir GEÇİŞ (resolved / tamamlandı / kullanıcının az önce
// yaptığı eylemin başarı geri bildirimi) ve VERİ (grafik serisi) için.
// Renk bunun dışında yalnız sapma (warn/err) ve seçim (accent).
// Normal bir durum için kırmızı/amber de aynı hatadır (OPEN kırmızı, ACK
// amber, sonuçsuz monitör amber PENDING).
//
// Kaynak-okuma pini (servicesPalette deseni): yorumlar soyulur ki gerekçe
// metnindeki eski sınıf adları testi yanıltmasın.
function stripComments(src: string): string {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, m => m.replace(/[^\n]/g, ' '))
    .replace(/^\s*\/\/.*$/gm, '');
}
const read = (rel: string) => stripComments(readFileSync(resolve(__dirname, rel), 'utf8'));
// v0.10.929 (K5) — incelemesi: `var(--ok, …)` geri-dönüş biçimi ve rgba yeşil
// literalleri de yakalanır (literal desenleri k5Residue.pin.test.ts ile aynı:
// 46,160,67 / 63,185,80 = eski GitHub yeşilleri #2ea043 / #3fb950 —
// k5Residue ile aynı literal desenler).
const GREEN = /\bb-ok\b|var\(--ok\s*[,)]|--ok-bg|tone="success"|46,\s*160,\s*67|63,\s*185,\s*80/;

describe('v0.10.929 — Incident / Incidents durum rozeti STATUS_TONE ile aynı', () => {
  const detail = read('./Incident.tsx');
  const list = read('./Incidents.tsx');

  // v0.10.929 (K5) — yerel kopya kural (s === 'resolved' ? 'b-ok' : 'b-gray')
  // kaldırıldı: iki sayfa sözlüğü hafif yaprak modülden (features/anomalies/
  // statusTone) içe aktarır. Sözlüğün İÇERİĞİ yalnız statusPalette.pin'de
  // çivilenir; burada yalnız bağlantı.
  it('detay ve liste durum rozetini tek sözlükten basar', () => {
    for (const src of [detail, list]) {
      expect(src).toContain("import { TriageStatusBadge } from '@/features/anomalies/statusTone';");
      expect(src).toContain('return <TriageStatusBadge s={s} label={label} />;');
      expect(src).not.toContain("'resolved' ? 'b-ok'");
    }
  });

  it('OPEN kırmızı / ACK amber el-yapımı ton geri gelmez; liste .status-pill-* kullanmaz', () => {
    for (const src of [detail, list]) {
      expect(src).not.toMatch(/=== 'open' \? '(b-err|outage)'/);
      expect(src).not.toMatch(/=== 'acknowledged' \? '(b-warn|degraded)'/);
    }
    expect(list).not.toContain('status-pill');
  });

  it('başlık sayacı nötr: üç sayı da --text', () => {
    expect(list).toContain("<b style={{ color: 'var(--text)' }}>{counts.open}</b> open");
    expect(list).toContain("<b style={{ color: 'var(--text)' }}>{counts.acknowledged}</b> ack");
    expect(list).toContain("<b style={{ color: 'var(--text)' }}>{counts.resolved}</b> resolved");
  });

  it('zaman çizelgesi: ack nötr, resolved / problem_resolved geçiş olarak yeşil kalır', () => {
    expect(detail).toContain("case 'ack':              return { icon: <Bell size={16} />, token: '--text3' };");
    expect(detail).toContain("case 'resolved':         return { icon: <Check size={16} />, token: '--ok' };");
    expect(detail).toContain("case 'problem_resolved': return { icon: <Check size={16} />, token: '--ok' };");
  });

  // v0.10.929 (K5) — RESOLVED'ın yeşili artık sözlükten gelir; liste
  // dosyasında elle yazılmış hiçbir yeşil kalmaz.
  it('liste dosyasında elle yazılmış yeşil yok', () => {
    expect(list).not.toMatch(GREEN);
  });
});

describe('v0.10.929 — sağlıklı durum yeşil almaz (A–M sayfaları)', () => {
  // Bu dosyalarda hiçbir yeşil kalmamalı: hepsi durum/normal gösteriyordu.
  const noGreen = [
    'AIObservability.tsx', 'AdminCluster.tsx', 'AdminElastic.tsx', 'AdminK8sCoverage.tsx',
    'AdminStats.tsx', 'AdminStatusPage.tsx', 'Alerts.tsx', 'Endpoints.tsx', 'EntityDetail.tsx',
    'Events.tsx', 'External.tsx', 'Hosts.tsx', 'Login.tsx', 'MessagingTopic.tsx', 'Monitors.tsx',
  ];
  it.each(noGreen)('%s: b-ok / var(--ok) / --ok-bg / tone="success" yok', f => {
    expect(read(`./${f}`)).not.toMatch(GREEN);
  });

  it('hata oranı: eşikler aynı, yalnız sağlıklı dal nötr', () => {
    expect(read('./Endpoints.tsx')).toContain("const errCls = r.errorRate >= 5 ? 'b-err' : r.errorRate >= 1 ? 'b-warn' : 'b-gray';");
    expect(read('./MessagingTopic.tsx')).toContain("const errCls = o.errorRate > 5 ? 'err' : o.errorRate > 0 ? 'warn' : 'gray';");
  });

  it('Endpoints: 2xx nötr rozet, iyileşme deltası --text2 (kötüleşme kırmızı kalır)', () => {
    const src = read('./Endpoints.tsx');
    expect(src).toContain('<span className="badge b-gray" title={`${s2.toLocaleString()} 2xx responses`}>');
    expect(src).toContain("const cls = d.pct > 5 ? 'var(--err)' : d.pct < -5 ? 'var(--text2)' : 'var(--text3)';");
  });

  it('External: kategori rozeti tek nötr ton (renkli sözlük yok)', () => {
    const src = read('./External.tsx');
    expect(src).not.toContain('CATEGORY_TONE');
    expect(src).toContain('return <span className="badge b-gray">{category}</span>;');
  });

  it('Events: deploy kategorisi --text2 (AnnotationLane ile aynı)', () => {
    expect(read('./Events.tsx')).toContain("deploy:      'var(--text2)',");
  });

  it('Monitors: sonuçsuz (PENDING) nötr sınıf, gerçek degraded amber kalır', () => {
    const src = read('./Monitors.tsx');
    expect(src).toContain("const cls = status === 'down' ? 'outage' : status === 'degraded' ? 'degraded' : 'operational';");
    expect(src).toContain("{status === 'unknown' ? 'PENDING' : status.toUpperCase()}");
    expect(src).toContain("const c = r.status === 'up' ? 'var(--text3)' : r.status === 'down' ? 'var(--err)' : 'var(--warn)';");
  });

  it('AIObservability KPI: ok tonu nötr metin, warn/err renkli', () => {
    const src = read('./AIObservability.tsx');
    expect(src).toContain("const color = cls === 'err' ? 'var(--err)'\n    : cls === 'warn' ? 'var(--warn)' : 'var(--text)';");
  });

  it('Hosts: up nötr, stale bir sapma → amber', () => {
    // v0.10.929 (K5) — incelemesi: bayat host sağlıklı değil; nötr gri onu 'up' ile aynı gösteriyordu.
    expect(read('./Hosts.tsx')).toContain("<span className={`badge ${r.up ? 'b-gray' : 'b-warn'}`}>{r.up ? 'up' : 'stale'}</span>");
  });

  it('GREEN deseni geri-dönüş ve rgba literal biçimlerini de yakalar', () => {
    // v0.10.929 (K5) — desen gevşerse noGreen listesi sessizce yeşil geçerdi.
    const hits = [
      "color: 'var(--ok)'", "color: 'var(--ok, #3fb950)'", "background: 'var(--ok-bg)'",
      "background: 'rgba(63,185,80,0.08)'", "'rgba(46, 160, 67, .15)'",
      'className="badge b-ok"', 'tone="success"',
    ];
    for (const s of hits) expect(s, s).toMatch(GREEN);
    for (const s of ["color: 'var(--okish)'", "color: 'var(--text2)'", 'className="b-okay"']) {
      expect(s, s).not.toMatch(GREEN);
    }
  });

  it('Login demo notu: yeşil literal yok, nötr token', () => {
    const src = read('./Login.tsx');
    expect(src).not.toMatch(/rgba\(63,\s*185,\s*80/);
    expect(src).toContain("<b style={{ color: 'var(--text)' }}>Demo mode</b>");
  });
});

describe('v0.10.929 — AdminClickhouse: yeşil yalnız ön kontrol ve eylem sonucunda', () => {
  const src = read('./AdminClickhouse.tsx');

  it('sağlıklı durum tonları nötr', () => {
    expect(src).toContain("if (v <= 1.25) return 'b-gray';");
    expect(src).toContain("healthy: 'b-gray', single_node: 'b-gray',");
    expect(src).toContain("return maxPP >= 1000 ? 'b-err' : maxPP >= 300 ? 'b-warn' : 'b-gray';");
    expect(src).toContain("function rootTone(pct: number): string { return pct >= 90 ? 'b-gray' : pct >= 50 ? 'b-warn' : 'b-err'; }");
    expect(src).toContain('<span className="badge b-gray">MV&apos;ler sağlıklı');
    expect(src.match(/<span className="badge b-gray">VAR<\/span>/g)).toHaveLength(5);
    expect(src.match(/<span className="badge b-gray">TAM<\/span>/g)).toHaveLength(4);
  });

  it('kalan her yeşil satır ön kontrol kutusu ya da az önceki eylemin sonucu', () => {
    // Operatör kararı: ön kontrol 'UYGULANABİLİR' kullanıcı eylemi geri
    // bildirimi → yeşil KALIR; uygula/geri al TAMAM ve adım ✓ da öyle.
    // v0.10.929 (K5) — lider kararı: kutuda YALNIZ hüküm rozeti + çerçeve
    // yeşil; PreRow ✓ ve MV KAPISI AÇIK durum kontrolleri → nötr (izinli değil).
    const allowed = [
      /pre\.supported \?/,                          // ön kontrol kutusu çerçevesi + UYGULANABİLİR
      /action\.res\.ok \?/,                         // uygula / geri al TAMAM
      /s\.ok \? <span style=\{\{ color: 'var\(--ok\)' \}\}>✓/, // uygulama adımı ✓
    ];
    const greens = src.split('\n').filter(l => GREEN.test(l));
    expect(greens.length).toBeGreaterThan(0);
    for (const l of greens) {
      expect(allowed.some(r => r.test(l)), l.trim()).toBe(true);
    }
    // Geçiş tarafı gerçekten yeşil kaldı (yanlışlıkla nötrlenmedi).
    expect(src.match(/pre\.supported \? 'b-ok' : 'b-warn'/g)).toHaveLength(5);
    expect(src.match(/border: `1px solid \$\{pre\.supported \? 'var\(--ok\)' : 'var\(--warn\)'\}`/g)).toHaveLength(5);
  });

  it('ön kontrol satırları durum kontrolü: ✓ nötr --text2, ✗ kırmızı; MV KAPISI AÇIK gri', () => {
    // v0.10.929 (K5) — lider kararı: ✓ bir geçiş değil, o anki durumun okunuşu.
    expect(src).toContain("<span style={{ color: ok ? 'var(--text2)' : neutral ? 'var(--text3)' : 'var(--err)' }}>");
    expect(src).not.toMatch(/color: ok \? 'var\(--ok/);
    expect(src).toContain("<span className={`badge ${pre.mvGate ? 'b-gray' : 'b-warn'}`}");
    expect(src).not.toMatch(/pre\.mvGate \? 'b-ok'/);
  });

  it('runbook adımı renge bağlı değil (sağlıklı hâl artık yeşil değil)', () => {
    // v0.10.929 (K5) — "yeşile döndükten sonra" nötr rozette imkânsız bir koşuldu.
    expect(src).not.toContain('yeşile döndükten sonra');
    expect(src).toContain('<li>Kartı yeniden <b>Ölç</b>; &quot;MV&apos;ler sağlıklı&quot; rozeti görünüp satır listeden düşünce yeniden adlandırılan kopyayı düşür.</li>');
  });
});
