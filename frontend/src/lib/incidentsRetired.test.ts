// incidentsRetired — v0.10.1828 (Incidents emekliliği E3/E6; operatör kararı
// 2026-11-05: "Incident sekmesi yerine Anomalies olsun"). Saf görünürlük
// kuralı + menü / ⌘K / kısayol / rota / Gruplar kablolama pinleri (iki durum).
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { incidentsNavVisible, INCIDENTS_RETIRED_HOME, INCIDENTS_ARCHIVE_BANNER } from './incidentsRetired';
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
    expect(INCIDENTS_ARCHIVE_BANNER).toBe("Incidents akışı emekli (arşiv). Yeni olaylar Anomalies'te.");
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
  it('menü: Incidents yuvası emekliyken Anomalies', () => {
    const src = read('../components/Sidebar.tsx');
    expect(src).toContain("{ href: '/incidents', label: 'nav.incidents', icon: TriangleAlert, incidentsOnly: true },");
    expect(src).toContain("{ href: '/anomalies', label: 'nav.anomalies', icon: Activity, retiredOnly: true },");
    expect(src).toContain('if (n.incidentsOnly && !incidentsOn) return false;');
    expect(src).toContain('if (n.retiredOnly && incidentsOn) return false;');
  });
  it('"g i" kısayolu anahtara bağlı', () => {
    const src = read('../components/GlobalShortcuts.tsx');
    expect(src).toContain('PAGES.filter(p => incidentsNavVisible(p.to, incidentsOn))');
  });
  it('/incidents emekliyken Anomalies › Gruplar\'a yönlenir; /incident arşivi açık', () => {
    const src = read('../App.tsx');
    expect(src).toContain('<Route path="/incidents"      element={<IncidentsRoute />} />');
    expect(src).toContain('if (!incidentsOn) return <Navigate to={INCIDENTS_RETIRED_HOME} replace />;');
    expect(src).toContain('<Route path="/incident"       element={<Incident />} />');
  });
  it('Anomalies sayfasında Gruplar segmenti ve boş durumu', () => {
    const bar = read('../components/coremetry/anomalies/MLTab.tsx');
    expect(bar).toContain("{ value: 'groups', label: 'Gruplar' }");
    const page = read('../features/anomalies/AnomalyStreamsPage.tsx');
    expect(page).toContain("tab === 'groups' ? <GroupsTab />");
    const groups = read('../components/coremetry/anomalies/GroupsTab.tsx');
    expect(groups).toContain('Anomali grupları yakında burada: aynı zamanda başlayan, birbirini çağıran servislerdeki anomaliler tek grupta toplanır.');
    expect(groups).toContain('<Empty ');
  });
});
