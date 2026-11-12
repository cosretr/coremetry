import { describe, it, expect } from 'vitest';
import { MULTI, plainVars, regexEscapeValue, resolveVarValues, writeVarValues } from './dashboardVars';
import { expandPromqlVars } from '@/components/dashboard/PanelRenderer';
import type { DashboardVariable } from './types';

// v0.10.2001 (Dashboards Faz 3 dilim 3.8) — değişken URL aynası + çoklu seçim.
const defs: DashboardVariable[] = [
  { name: 'service', type: 'service' },
  { name: 'env', type: 'label', labelName: 'deployment_environment', multi: true },
  { name: 'db', type: 'database', defaultValue: 'orders' },
];

describe('resolveVarValues', () => {
  it('tekli, çoklu (nöbetçili regex), varsayılan; tanımsız param yok sayılır', () => {
    const sp = new URLSearchParams('service=api&env=prod&env=dr.1&foo=bar');
    const v = resolveVarValues(defs, sp);
    expect(v.service).toBe('api');
    expect(v.env).toBe(MULTI + 'prod|dr\\.1');
    expect(v.db).toBe('orders');
    expect(v.foo).toBeUndefined();
  });
  it('multi kapalı değişkende çoklu değer ilkine iner', () => {
    const v = resolveVarValues([{ name: 'service', type: 'service' }], new URLSearchParams('service=a&service=b'));
    expect(v.service).toBe('a');
  });
});

describe('writeVarValues', () => {
  it('yalnız o değişkene dokunur, yabancı paramlar kalır', () => {
    const next = writeVarValues(new URLSearchParams('id=d1&ai=x&env=old'), 'env', ['a', 'b']);
    expect(next.getAll('env')).toEqual(['a', 'b']);
    expect(next.get('ai')).toBe('x');
    expect(next.get('id')).toBe('d1');
    expect(writeVarValues(next, 'env', []).has('env')).toBe(false);
  });
});

describe('MetricsQL genişletmesi', () => {
  it('çoklu değer =~ olur, != !~ olur; tekli = kalır; boş eşleyici düşer', () => {
    const vars = { env: MULTI + 'prod|dr', service: 'api', empty: '' };
    expect(expandPromqlVars('up{env="${env}",service_name="${service}"}', vars))
      .toBe('up{env=~"prod|dr",service_name="api"}');
    expect(expandPromqlVars('up{env!="${env}"}', vars)).toBe('up{env!~"prod|dr"}');
    expect(expandPromqlVars('up{x="${empty}",service_name="${service}"}', vars)).toBe('up{service_name="api"}');
  });
  it('plainVars ilk değeri kaçışsız verir', () => {
    expect(plainVars({ env: MULTI + 'dr\\.1|prod', s: 'a' })).toEqual({ env: 'dr.1', s: 'a' });
    const same = { s: 'a' };
    expect(plainVars(same)).toBe(same);
  });
  it('regex kaçışı', () => {
    expect(regexEscapeValue('a.b|c(d)')).toBe('a\\.b\\|c\\(d\\)');
  });
});
