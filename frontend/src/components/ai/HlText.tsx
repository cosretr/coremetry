// HlText / ChainFlow — v0.10.2374: otomatik vurgunun (autoHighlight.ts) ve
// çağrı zincirinin (callChain.ts) TEK çizim noktası. İki çizici paylaşır:
// sohbet balonu (ChatBubble renderNodes) ve ✨ Explain gövdesi
// (Markdown.tsx `rich` + ExplainBody Karar şeridi). Yalnız asistan cevabının
// DÜZ METİN parçaları gelir; kod, bağlantı ve kullanıcı metni buraya girmez.
// Stil globals.css `.cm-hl*` / `.cm-chain*` — kapsayıcıdan bağımsız.
import { Fragment, type ReactNode } from 'react';
import { highlightParts, type HlPart } from './autoHighlight';
import { isErrStep, type Chain } from './callChain';

function renderParts(parts: readonly HlPart[]): ReactNode[] {
  return parts.map((p, j) => {
    switch (p.kind) {
      case undefined: return <Fragment key={j}>{p.v}</Fragment>;
      // Tırnaklar metinde kalır; içteki tanınan parçalar çip.
      case 'msg': return <span key={j} className="cm-hl-msg">{p.c ? renderParts(p.c) : p.v}</span>;
      case 'dur': return <span key={j} className="cm-hl-dur">{p.v}</span>;
      case 'kv': {
        const eq = p.v.indexOf('=') + 1;
        return <code key={j} className="cm-hl cm-hl-kv"><span className="cm-hl-k">{p.v.slice(0, eq)}</span>{p.v.slice(eq)}</code>;
      }
      case 'err': return <code key={j} className="cm-hl cm-hl-err">{p.v}</code>;
      // Servis-benzeri ad: YALNIZ stil, link yok (entity modeli kuralı).
      case 'ent': return <code key={j} className="cm-hl cm-hl-ent">{p.v}</code>;
      default: return <code key={j} className="cm-hl">{p.v}</code>;
    }
  });
}

/** Düz metin parçası → vurgulu satır içi düğümler. */
export function HlText({ text }: { text: string }) {
  return <>{renderParts(highlightParts(text))}</>;
}

/**
 * Çağrı zinciri: sıralı liste, her adım kendi satırında. Ok metni DOM'da
 * kalır (seçip kopyalayınca özgün "a → b → c"), görselde yerini ray + nokta
 * alır; sırayı ekran okuyucuya <ol> söyler. Hata işaretli adımın noktası kırmızı.
 */
export function ChainFlow({ chain, render }: { chain: Chain; render: (step: string, i: number) => ReactNode }) {
  return (
    <ol className="cm-chain">
      {chain.steps.map((s, i) => (
        <li key={i} className={isErrStep(s) ? 'cm-chain__s cm-chain__s--err' : 'cm-chain__s'}>
          {i > 0 && <span className="cm-chain__sep" aria-hidden="true">{chain.seps[i - 1]}</span>}
          {render(s, i)}
        </li>
      ))}
    </ol>
  );
}
