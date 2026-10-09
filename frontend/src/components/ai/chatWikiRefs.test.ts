import { describe, expect, it } from 'vitest';
import type { ChatTurn } from '../../lib/types';
import { prevWikiRefs } from './chatWikiRefs';

// v0.10.1134 — wiki takip sorusu: son asistan turunun wiki href'leri sunucuya
// context.wikiRefs olarak gider (geçmiş yalnız {role,text} taşıyor).
const A = 'https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=/A';
const B = 'https://devops.example.test/Platform/_wiki/wikis/Platform.wiki?pagePath=/B';

describe('prevWikiRefs', () => {
  it('son asistan turunun wiki çip ve kaynaklarını sırayla, tekil toplar', () => {
    const turns: ChatTurn[] = [
      { role: 'user', text: 'namespace nasıl oluşturabilirim' },
      {
        role: 'assistant', text: 'cevap',
        links: [{ label: 'Wiki · A', href: A }, { label: 'svc-orders', href: '/services/svc-orders' }],
        sources: [{ doc: 'Wiki · A', ref: A, chunk: 1, score: 1 }, { doc: 'Wiki · B', ref: B, chunk: 1, score: 1 }, { doc: 'a.pdf', chunk: 1, score: 1 }],
      },
    ];
    expect(prevWikiRefs(turns)).toEqual([A, B]);
  });

  it('son asistan turu wiki değilse boş (önceki wiki turuna bakmaz)', () => {
    const turns: ChatTurn[] = [
      { role: 'assistant', text: 'wiki', links: [{ label: 'Wiki · A', href: A }] },
      { role: 'user', text: 'svc-orders hata oranı' },
      { role: 'assistant', text: '%2', links: [{ label: 'svc-orders', href: '/services/svc-orders' }] },
    ];
    expect(prevWikiRefs(turns)).toEqual([]);
  });

  it('hatalı/bekleyen turu atlar; http dışı href almaz', () => {
    const turns: ChatTurn[] = [
      { role: 'assistant', text: 'wiki', links: [{ label: 'Wiki · A', href: A }, { label: 'Wiki · X', href: 'javascript:alert(1)' }] },
      { role: 'assistant', text: '', error: 'kopuk' },
    ];
    expect(prevWikiRefs(turns)).toEqual([A]);
    expect(prevWikiRefs([])).toEqual([]);
  });
});
