// @vitest-environment jsdom
//
// v0.10.1145 — akıllı yapıştırma: kod sezgileri (olumlu / olumsuz, tablo) ve
// yapıştırma kararı (planPaste). jsdom: HTML dalı DOMParser kullanır. Adlar sentetik.
import { describe, it, expect } from 'vitest';
import { detectCodeLang, detectCodePaste } from './codeDetect';
import { planPaste } from './composerPaste';
import { applyEdit } from './composerEdit';

const JAVA_STACK = [
  'java.lang.IllegalStateException: pool exhausted',
  '    at com.example.orders.Pool.acquire(Pool.java:42)',
  '    at com.example.orders.Api.place(Api.java:17)',
  '    ... 12 more',
].join('\n');

describe('detectCodePaste — olumlu', () => {
  it.each([
    ['JSON nesnesi', '{\n  "service": "svc-orders",\n  "p95": 480\n}', 'json'],
    ['NDJSON log satırları', '{"level":"error","msg":"a"}\n{"level":"info","msg":"b"}\n{"level":"warn","msg":"c"}', 'json'],
    ['kırpık JSON', '{\n  "a": 1,\n  "b": [\n    2,', 'json'],
    ['YAML manifest', 'apiVersion: v1\nkind: Service\nmetadata:\n  name: svc-orders\n  namespace: shop', 'yaml'],
    ['YAML ---', '---\nreplicas: 3\nimage: example.test/svc-orders:1.2', 'yaml'],
    ['SQL', 'SELECT service, count()\nFROM spans\nWHERE ts > now() - 60\nGROUP BY service', 'sql'],
    ['küçük harf SQL', 'select *\nfrom spans\nwhere status = 2', 'sql'],
    ['XML', '<config>\n  <item id="1">a</item>\n</config>', 'xml'],
    ['zaman damgalı log', '2026-10-09T10:00:00Z INFO svc-orders started\n2026-10-09T10:00:01Z ERROR svc-orders timeout\n2026-10-09T10:00:02Z WARN retry', 'log'],
    ['seviye önekli log', 'ERROR pool exhausted\nWARN retrying in 5s\nINFO recovered', 'log'],
    ['Java stack trace', JAVA_STACK, 'text'],
    ['Python traceback', 'Traceback (most recent call last):\n  File "app.py", line 3, in <module>\n    main()\nValueError: boom', 'text'],
    ['Go panic', 'panic: runtime error: index out of range\n\ngoroutine 1 [running]:\nmain.main()\n\t/app/main.go:12 +0x1d', 'text'],
    ['Go kodu', 'func main() {\n\tx := 1\n\tfmt.Println(x)\n}', 'text'],
    ['JS kodu', 'const a = 1;\nif (a === 1) {\n  console.log(a);\n}', 'text'],
  ])('%s → %s', (_n, text, want) => {
    expect(detectCodePaste(text)).toBe(want);
  });
});

describe('detectCodePaste — olumsuz (düzyazı çite hapsedilmez)', () => {
  it.each([
    ['düz Türkçe paragraf', 'svc-orders dün akşam yavaşladı.\nDeploy sonrası başladı galiba.\nNe yapmalıyım?'],
    ['anahtar: değer düzyazısı', 'Servis: svc-orders\nDurum: yavaş\nEtki: ödeme akışı'],
    ['markdown liste', '- svc-orders\n- svc-payments\n- svc-cart'],
    ['numaralı liste', '1. aç\n2. kontrol et\n3. kapat'],
    ['iki satır kod (≤2 satır)', 'const a = 1;\nconst b = 2;'],
    ['zaten çitli', '```\nSELECT 1\nFROM t\nWHERE x\n```'],
    ['SELECT ile başlayan cümle', 'Select the service from the list\nthen open the traces tab\nand check the errors'],
    ['virgülle biten satırlar', 'elma,\narmut,\nkiraz'],
    ['parantezle biten notlar', 'svc-a yavaş (p95 2s)\nsvc-b normal (dün)\nsvc-c bilinmiyor (veri yok)'],
    ['@-anmalı satırlar', '@svc-orders neden yavaş\n@svc-cart hata veriyor mu\n@svc-pay durum'],
    ['ok işaretli olay notu', 'svc-a -> svc-b\nsvc-b -> db\nyavaşlık burada'],
    ['markdown tablo', '| a | b |\n|---|---|\n| 1 | 2 |'],
    ['tek satır', 'SELECT 1 FROM t'],
  ])('%s → null', (_n, text) => {
    expect(detectCodePaste(text)).toBeNull();
  });
});

describe('detectCodeLang — <pre> etiketi (satır şartı yok)', () => {
  it('tek satırlık JSON / SQL', () => {
    expect(detectCodeLang('{"a":1}')).toBe('json');
    expect(detectCodeLang('SELECT 1')).toBe('sql');
    expect(detectCodeLang('merhaba dünya')).toBe('');
  });
});

describe('planPaste — yapıştırma kararı', () => {
  const at = (value: string, start: number, end = start) => ({ value, start, end });

  it('seçimin üstüne URL → [seçim](url)', () => {
    const s = at('bkz runbook lütfen', 4, 11);
    const p = planPaste(s, { text: 'https://wiki.example.test/r?id=1' });
    expect(p?.kind).toBe('link');
    expect(applyEdit(s.value, p!.edit).value).toBe('bkz [runbook](https://wiki.example.test/r?id=1) lütfen');
  });

  it('seçim yokken / seçim URL iken URL düz yapışır', () => {
    expect(planPaste(at('a ', 2), { text: 'https://wiki.example.test' })).toBeNull();
    const s = at('https://a.example.test', 0, 22);
    expect(planPaste(s, { text: 'https://b.example.test' })).toBeNull();
  });

  it('javascript: "URL" bağlantıya dönmez', () => {
    expect(planPaste(at('tıkla', 0, 5), { text: 'javascript:alert(1)' })).toBeNull();
  });

  it('kod gibi çok satır → dil etiketli çit; satır ortasında blok kendi satırına iner', () => {
    const s = at('bak: ', 5);
    const p = planPaste(s, { text: JAVA_STACK + '\n' });
    expect(p?.kind).toBe('code');
    const out = applyEdit(s.value, p!.edit);
    expect(out.value).toBe('bak: \n```text\n' + JAVA_STACK + '\n```');
    expect(out.start).toBe(out.value.length);
    if (p?.kind === 'code') expect(p.raw).toBe(JAVA_STACK + '\n');
  });

  it('imleç çitin İÇİNDEyse hiçbir dönüşüm yok (ham yapıştırma)', () => {
    const v = '```\n\n```';
    expect(planPaste(at(v, 4), { text: JAVA_STACK })).toBeNull();
    expect(planPaste(at(v, 4), { html: '<b>x</b>', text: 'x' })).toBeNull();
  });

  it('düz tek satır / düzyazı → null (tarayıcının yapıştırması)', () => {
    expect(planPaste(at('', 0), { text: 'merhaba' })).toBeNull();
    expect(planPaste(at('', 0), { text: 'bir.\niki.\nüç.' })).toBeNull();
  });

  it('biçimli HTML → markdown (raw = düz metin, Geri al için)', () => {
    const p = planPaste(at('', 0), { html: '<ul><li><b>svc-orders</b></li><li>svc-cart</li></ul>', text: 'svc-orders\nsvc-cart' });
    expect(p?.kind).toBe('html');
    expect(applyEdit('', p!.edit).value).toBe('- **svc-orders**\n- svc-cart');
    if (p?.kind === 'html') expect(p.raw).toBe('svc-orders\nsvc-cart');
  });

  it('biçimi düz metinle aynı çıkan HTML → kod/düz yoluna düşer', () => {
    const html = '<b style="font-weight:normal"><span>merhaba</span></b>';
    expect(planPaste(at('', 0), { html, text: 'merhaba' })).toBeNull();
  });

  it('tavanı aşan HTML düz metne düşer (kod sezgisi yine çalışır)', () => {
    const html = '<pre>' + 'x'.repeat(210 * 1024) + '</pre>';
    const p = planPaste(at('', 0), { html, text: '{\n  "a": 1,\n  "b": 2\n}' });
    expect(p?.kind).toBe('code');
    expect(applyEdit('', p!.edit).value.startsWith('```json\n')).toBe(true);
  });
});
