// clearedToggle.pin.test.ts — v0.10.925 (buton bütünlüğü Faz 2 incelemesi).
// (1) "Cleared (N)" başlığı ≤10 satırda grubu KAPATAMIYORDU: açıklık
//     `showCleared || defaultExpanded` idi, varsayılan açıkken tık hiçbir
//     şey değiştirmiyordu. Artık kullanıcı seçimi (null = seçilmedi)
//     varsayılanı ezer.
// (2) Logs seviye çipi render İÇİNDE tanımlı bir BİLEŞENDİ (`const
//     LevelChip = (…) => …` + `<LevelChip>`): her render yeni tip → çip
//     yeniden bağlanıyor, klavyeyle seçince odak kayboluyordu. Render
//     fonksiyonu olarak çağrılır.
// Kaynak pini — bileşenler ağır (sorgu + router); davranış satırları çivili.
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

const streams = readFileSync(resolve(__dirname, 'streams.tsx'), 'utf8');
const logs = readFileSync(resolve(__dirname, '../../pages/Logs.tsx'), 'utf8');

describe('v0.10.925 — Cleared grubu aç/kapa', () => {
  it('kullanıcı seçimi varsayılanı ezer; tık mevcut görünür hâli tersler', () => {
    expect(streams).toContain('useState<boolean | null>(null)');
    expect(streams).toContain('const expanded = showCleared ?? defaultExpanded;');
    expect(streams).toContain('onClick={() => setShowCleared(!expanded)}');
    expect(streams).not.toContain('showCleared || defaultExpanded');
  });
});

describe('v0.10.925 — Logs seviye çipi yeniden bağlanmaz', () => {
  it('render içi bileşen yok; render fonksiyonu anahtarlı Chip döndürür', () => {
    expect(logs).not.toMatch(/const LevelChip\s*=/);
    expect(logs).not.toContain('<LevelChip');
    expect(logs).toContain('const levelChip = (keyName: string, count: number, title: string, children: ReactNode) => {');
    expect(logs).toContain('<Chip key={keyName} pill active={on}');
  });
});
