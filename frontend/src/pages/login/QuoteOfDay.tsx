// QuoteOfDay — v0.10.1911 (Login günün sözü, audit §5.2). Giriş formunun
// altında, üstte ince çizgiyle: tırnaklı söz + alt satır. `branding.loginQuote`
// yalnız "on" iken çizilir (operatör kararı: varsayılan KAPALI); kapalıyken
// söz verisi (data/quotes.json) için dinamik `import()` de YAPILMAZ. Seçim
// `pickQuote(bugün, uzunluk)` = yılın günü % uzunluk (sunucu söz döndürmez).
// `attributed` true ise alt satır kaynak metnidir (birincil kaynağı olmayan
// "attributed to …" notu); değilse "— yazar".
import { useEffect, useState } from 'react';
import { useBranding } from '@/lib/branding';
import { pickQuote } from '@/lib/loginSummary';

interface Quote { text: string; author: string; source: string; attributed: boolean }

export function QuoteOfDay() {
  const on = useBranding().loginQuote === 'on';
  const [quote, setQuote] = useState<Quote | null>(null);
  useEffect(() => {
    if (!on) { setQuote(null); return; }
    let alive = true;
    import('@/data/quotes.json')
      .then(m => {
        const list = m.default as Quote[];
        if (alive && list.length) setQuote(list[pickQuote(new Date(), list.length)]);
      })
      .catch(() => { /* süs: yüklenemezse söz çizilmez */ });
    return () => { alive = false; };
  }, [on]);
  if (!on || !quote) return null;
  return (
    <figure className="m-0 mt-4 border-t border-border pt-4">
      <blockquote className="m-0 text-[length:var(--fs-md)] italic leading-normal text-foreground">
        “{quote.text}”
      </blockquote>
      <figcaption className="mt-1 text-[length:var(--fs-xs)] text-muted-foreground">
        {quote.attributed ? quote.source : `— ${quote.author}`}
      </figcaption>
    </figure>
  );
}
