import { describe, it, expect } from 'vitest';
import { parseDashboardImport, serializeDashboard } from './dashboardIO';

// Dashboards Faz 3 dilim 3.1 — export etiketleri atlıyordu (audit §5.5).
describe('dashboardIO etiketleri taşır', () => {
  it('serialize → parse etiketleri korur', () => {
    const json = serializeDashboard({ id: 'x', name: 'A', description: '', tags: ['red', 'kafka'], createdAt: 1, updatedAt: 1, panels: [], variables: [] });
    expect(JSON.parse(json).tags).toEqual(['red', 'kafka']);
    expect(parseDashboardImport(json).tags).toEqual(['red', 'kafka']);
  });
  it('etiketsiz eski dosya boş listeyle okunur, bozuk öğeler atılır', () => {
    expect(parseDashboardImport(JSON.stringify({ schema: 'coremetry.dashboard/v1', name: 'A', panels: [] })).tags).toEqual([]);
    expect(parseDashboardImport(JSON.stringify({ name: 'A', panels: [], tags: [' a ', 3, ''] })).tags).toEqual(['a']);
  });
});
