// incidentsRetired — v0.10.1828 (Incidents emekliliği E3/E6; operatör kararı
// 2026-11-05: "Incident sekmesi yerine Anomalies olsun"). Saf görünürlük
// kuralı + menü / ⌘K / kısayol / rota / Gruplar kablolama pinleri (iki durum).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { incidentsNavVisible, INCIDENTS_RETIRED_HOME } from './incidentsRetired';
import { localizePages } from './palette/pages';

const read = (rel: string) => readFileSync(resolve(__dirname, rel), 'utf8');

describe('incidentsNavVisible', () => {
  it('yalnız /incidents anahtara bağlı', () => {
    expect(incidentsNavVisible('/incidents', true)).toBe(true);
    expect(incidentsNavVisible('/incidents', false)).toBe(false);
    expect(incidentsNavVisible('/incidents?status=open', false)).toBe(false);
    expect(incidentsNavVisible('/incident', false)).toBe(true); // arşiv detay sayfası
    expect(incidentsNavVisible('/anomalies', false)).toBe(true);
    expect(INCIDENTS_RETIRED_HOME).toBe('/anomalies?tab=groups');
  });
});

describe('⌘K sayfaları', () => {
  it('emekliyken Incidents önerilmez, açıkken aynen', () => {
    const on = localizePages(k => k, false, true).map(p => p.to);
    const off = localizePages(k => k, false, false).map(p => p.to);
    expect(on).toContain('/incidents');
    expect(off).not.toContain('/incidents');
    expect(off).toContain('/anomalies');
    expect(off.length).toBe(on.length - 1);
  });
});

describe('kablolama', () => {
  // v0.10.2024 — operatör 2026-11-12: kayıtlı anahtar açık kalmış kurulumda
  // menü Incidents gösterip Anomalies'i gizliyordu. Menü artık bayrağa hiç
  // bakmaz: Incidents girişi yok, Anomalies her zaman.
  it('menü: Incidents girişi yok, Anomalies koşulsuz', () => {
    const src = read('../components/Sidebar.tsx');
    expect(src).toContain("{ href: '/anomalies', label: 'nav.anomalies', icon: Activity },");
    expect(src).not.toContain("href: '/incidents'");
    expect(src).not.toContain('features?.incidents');
    expect(src).not.toMatch(/incidentsOnly|retiredOnly/);
  });
  it('Settings: Incidents ana anahtarı kutusu ve PUT istemcisi yok', () => {
    expect(read('../pages/settings/AnomalyTab.tsx')).not.toContain('IncidentsLegacySection');
    expect(read('./api.ts')).not.toMatch(/putIncidentsSettings|getIncidentsSettings/);
  });
  // Incidents ölü kod 5: /api/incidents* her metotta 410; istemci çağırmaz.
  it('istemci /api/incidents çağırmaz', () => {
    expect(read('./api.ts')).not.toContain('/api/incidents');
    expect(read('../pages/Inbox.tsx')).not.toContain('ackIncident');
  });
  it('"g i" kısayolu anahtara bağlı', () => {
    const src = read('../components/GlobalShortcuts.tsx');
    expect(src).toContain('PAGES.filter(p => incidentsNavVisible(p.to, incidentsOn))');
  });
  // Incidents ölü kod dilimi 2: liste + arşiv detay sayfası silindi; iki eski
  // adres de koşulsuz Anomalies'e iner (boş 404 yok).
  it('/incidents ve /incident Anomalies\'e yönlenir; sayfa modülü yok', () => {
    const src = read('../App.tsx');
    expect(src).toContain('<Route path="/incidents"      element={<Navigate to={INCIDENTS_RETIRED_HOME} replace />} />');
    expect(src).toContain('<Route path="/incident"       element={<Navigate to={INCIDENTS_RETIRED_HOME} replace />} />');
    expect(src).not.toMatch(/import\('\.\/pages\/Incidents?'\)/);
  });
  // v0.10.2027 — Gruplar sekmesi (veri okumuyordu) kalktı; /anomalies tek sayfa
  // ve ?tab=groups aynı sayfayı açar (eski bağlantılar bozulmaz).
  it('Anomalies tek sayfa: Gruplar sekmesi yok, ?tab= yok sayılır', () => {
    const page = read('../features/anomalies/AnomalyStreamsPage.tsx');
    expect(page).not.toContain('GroupsTab');
    expect(page).not.toMatch(/sp\.get\('tab'\)/);
  });
});
