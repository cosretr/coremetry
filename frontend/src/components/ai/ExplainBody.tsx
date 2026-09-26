// ExplainBody — v0.10.165: CoSRE cevap kartının SABİT gövde anatomisi (tasarım
// etüdü «cevap paneli» seçenek A «Yapılandırılmış kart», iki yargıçta birinci;
// dilim 1, sıfır backend). Sıra her türde aynı:
//   Karar (Kök Neden / Olası neden bölümünün ilk cümlesi; bölüm tek cümleyse
//   ÇİZİLMEZ — kopya olurdu; çizilince aynı cümle gövdeden DÜŞER; kod kartı
//   açıkken ilk kart Karar çizmez — iki rakip Karar olmasın, `verdict`) →
//   Kanıt (span/trace kimlik sayısı; listeleri AIDrawer'ın altındaki bölümler
//   taşır — yalnız gerçekten listelenenler sayılır) → kod alıntıları (oluklu
//   çitler gövdeden buraya hoist; Markdown CodeBlock anatomy: satır numarası
//   oluğu, dosya başlığı, mapper etiketi) → bölümler (Stacktrace Detayı'nın
//   stack çiti yerinde kalır, katlanır) → dipnot/geri bildirim (çağıran çizer).
// Akış SÜRERKEN ham metin aynen akar (yarım çit titremesin); anatomi yalnız
// bitmiş metne uygulanır. Güven puanı çizilmez (böyle bir alan yok — brief §5).
// v0.10.948 (CoSRE Faz B) — gövdenin ALTINA iki deterministik satır: sunucunun
// `id`siz kanıt linkleri (ExplainEvidenceLinks) ve `sources` kaynak durumu
// dipnotu (ExplainSourceFooter). Yapısal `sources` varken sunucunun metne
// eklediği "Kaynak durumu" bloğu gövdeden ayrılır (splitSourceFooter) — aynı
// bilgi iki kez basılmaz; `sources` yoksa metin aynen kalır.
// v0.10.948 — beş başlıklı inceleme cevabında (**Bulgu** … **Sonraki kontrol**)
// Karar ÇİZİLMEZ: «Olası neden» hipotezdir; verdictLine metnin şeklinden
// null döner (`sources`suz önbellek isabetinde de), bölüm bütün kalır.
import { useMemo } from 'react';
import { RenderedMarkdown, CodeBlock } from '@/components/Markdown';
import type { IdLink } from '@/components/ai/inlineIdLinks';
import type { ExplainSourceStatus } from '@/lib/types';
import { verdictLine, hoistCodeQuotes, dropVerdictSentence, splitSourceFooter } from './explainAnatomy';
import { ExplainEvidenceLinks, ExplainSourceFooter } from './ExplainEvidence';

// oracle — v0.10.921 (Kademe A): Explain'e giren Oracle hata satırı sayısı.
export interface ExplainEvidence { spans: number; traces: number; oracle?: number }

export function ExplainBody({ text: raw, busy, links, evidence, verdict: wantVerdict = true, sources }: {
  text: string;
  busy: boolean;
  links?: IdLink[];
  /** ilk cevabın kanıt kimlik sayıları (exception/trace); yoksa satır çizilmez */
  evidence?: ExplainEvidence;
  /** false → Karar satırı çizilmez (kod kartı açıkken ilk kart) */
  verdict?: boolean;
  /** v0.10.948 — cevap çerçevesinin kaynak durumları (explain-trace); yoksa dipnot yok */
  sources?: ExplainSourceStatus[];
}) {
  const hasSources = !!sources?.length;
  // v0.10.948 — sunucunun metin dipnotu yalnız yapısal kopyası VARKEN ayrılır.
  const text = useMemo(() => (busy || !hasSources ? raw : splitSourceFooter(raw).body), [busy, hasSources, raw]);
  const verdict = useMemo(() => (busy || !wantVerdict ? null : verdictLine(text)), [busy, text, wantVerdict]);
  const hoisted = useMemo(() => {
    if (busy) return { quotes: [], rest: text };
    const h = hoistCodeQuotes(text);
    return verdict ? { quotes: h.quotes, rest: dropVerdictSentence(h.rest, verdict) } : h;
  }, [busy, text, verdict]);
  const ev = evidence && (evidence.spans > 0 || evidence.traces > 0 || (evidence.oracle ?? 0) > 0) ? evidence : null;
  return (
    <>
      {verdict && (
        <div className="cx-verdict"><span className="cx-verdict-k">Karar</span>{verdict}</div>
      )}
      {!busy && ev && (
        <div className="cx-evidence">
          Kanıt: {[
            ev.spans > 0 ? `${ev.spans} span` : '',
            ev.traces > 0 ? `${ev.traces} trace` : '',
            (ev.oracle ?? 0) > 0 ? `${ev.oracle} Oracle satırı` : '',
          ].filter(Boolean).join(' · ')}
          <span className="field-hint"> · kimlikler çekmecenin altında, satır satır</span>
        </div>
      )}
      {hoisted.quotes.map((q, i) => <CodeBlock key={`q${i}`} lang={q.lang} lines={q.lines} anatomy />)}
      <RenderedMarkdown text={hoisted.rest} idLinks={links} anatomy />
      {!busy && <ExplainEvidenceLinks links={links} />}
      {!busy && <ExplainSourceFooter sources={sources} />}
    </>
  );
}
