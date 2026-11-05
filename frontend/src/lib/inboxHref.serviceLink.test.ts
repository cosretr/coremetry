// v0.10.1508 regresyon (operator-reported, prod): servis şeridi "4 açık konu"
// derken "Inbox'ta gör →" boş kuyruk açıyordu. Link ↔ Inbox süzgeci
// gidiş-dönüşü: linkin parametreleri Inbox'ın KENDİ codec'leriyle (aynı
// sabitler Inbox.tsx'ten okunur) çözülünce şeridin isteğiyle aynı kapsam
// çıkmalı — servis, open, TÜM öncelikler, TÜM türler, env YOK — ve bu,
// localStorage'da yapışkan bir env seçimi varken de doğru olmalı.
import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { serviceInboxHref } from './inboxHref';
import { decodeCsvSet } from './inboxUrl';
import { resolveEnv } from './useUrlEnv';

const inboxSrc = readFileSync(resolve(__dirname, '../pages/Inbox.tsx'), 'utf8');
function arr(name: string): string[] {
  const m = new RegExp(`const ${name}[^=]*=\\s*\\[([^\\]]*)\\]`).exec(inboxSrc);
  if (!m) throw new Error(`${name} bulunamadı`);
  return m[1].split(',').map(s => s.trim().replace(/['"]/g, '')).filter(Boolean);
}
const PRIO_ALL = arr('PRIO_ALL');
const PRIO_DEFAULT = arr('PRIO_DEFAULT');
const KIND_ALL = arr('KIND_ALL');
const KIND_DEFAULT = arr('KIND_DEFAULT');

// Inbox.tsx'in URL → istek çözümü (statusFilter / prioSet / kindSet /
// serviceFilter / useUrlEnv) — aynı codec'ler, aynı sabitler.
function inboxScope(href: string, storedEnv: string | null) {
  const sp = new URL(href, 'http://x').searchParams;
  const st = sp.get('status');
  return {
    path: new URL(href, 'http://x').pathname,
    status: st === 'all' ? 'all' : st === 'ignored' ? 'ignored' : 'open',
    service: sp.get('service') ?? '',
    prio: decodeCsvSet(sp.get('prio'), PRIO_ALL, PRIO_DEFAULT),
    kind: decodeCsvSet(sp.get('kind'), KIND_ALL, KIND_DEFAULT),
    env: resolveEnv(sp.get('env'), storedEnv),
    q: sp.get('q') ?? '', since: sp.get('since') ?? '', minOcc: sp.get('minOcc'),
  };
}

// Şeridin isteği: api.inbox({ service, status: 'open', limit: 20 }) — sunucu
// varsayılanları: tüm öncelikler/türler, env yok, varsayılan taban.
const stripScope = (service: string) => ({
  path: '/inbox', status: 'open', service,
  prio: ['P1', 'P2', 'P3'], kind: [...KIND_ALL], env: '', q: '', since: '', minOcc: null,
});

describe('serviceInboxHref ↔ Inbox süzgeci gidiş-dönüşü', () => {
  it.each([
    ['yapışkan env yok', null],
    ['yapışkan env seçili (ör. uat)', 'uat'],
    ['yapışkan env prod', 'production'],
  ])('%s → şeridin kapsamı', (_n, stored) => {
    for (const svc of ['orders-api', 'callcenter-ivr-prod', 'a b/c?d&e']) {
      expect(inboxScope(serviceInboxHref(svc), stored)).toEqual(stripScope(svc));
    }
  });

  it('eski link (?service=&status=open) bu kapsamı VERMİYORDU — hata sınıfı pinli', () => {
    const old = `/inbox?service=orders-api&status=open`;
    const s = inboxScope(old, 'uat');
    expect(s.prio).toEqual(PRIO_DEFAULT); // yalnız P1 → P2 satırı düşer
    expect(s.kind).toEqual(KIND_DEFAULT); // v0.10.1796: exception satırı düşer
    expect(s.env).toBe('uat');            // yapışkan env → servis elenir
  });

  it('şerit ve servis haritası çekmecesi linki tek yardımcıdan kurar', () => {
    const strip = readFileSync(resolve(__dirname, '../components/ServiceAttentionStrip.tsx'), 'utf8');
    const drawer = readFileSync(resolve(__dirname, '../components/ServiceMapNodeDrawer.tsx'), 'utf8');
    expect(strip).toContain('const inboxHref = serviceInboxHref(service);');
    expect(drawer).toContain('to={serviceInboxHref(service)}');
    expect(strip + drawer).not.toMatch(/`\/inbox\?service=/);
    // Şerit Inbox'ın triage görünümünü uygular (noise imzaları gizli).
    expect(strip).toContain("filterByVerdictView(q.data.items, verdictIndex(verdictsQ.data?.verdicts), 'triage')");
  });
});
