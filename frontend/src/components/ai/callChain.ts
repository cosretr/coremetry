// callChain — v0.10.2374 (operatör: "CoSRE trace açıklamasında … servisler
// arası çağrı gösterimi daha güzel olabilir"). SAF.
//
// Küçük modeller çağrı zincirini tek satıra yazıyor:
//   "orders-api → customer-rel-grpc · BagService/call → status error → …"
// Satır sarınca hangi adımın nerede bittiği okunmuyor. En az İKİ ok (" → " ya
// da " -> ") taşıyan TEK SATIR adımlara bölünür; çizici (HlText.tsx ChainFlow)
// her adımı kendi satırında basar. Metin kaybolmaz: label + lead + adımlar ve
// ayraçlar sırayla birleşince girdinin aynısıdır (DOM metni ve kopyalama
// okları korur).
//
// Bölünmeyenler: tek ok, çok satırlı metin, `kod` içindeki ok, ve bir adımı
// **kalın** / `kod` / [link] ortasından kesecek ok (dengesiz işaret → null).
import { highlightParts, hasErrPart } from './autoHighlight';
import { splitListLabel } from './listLabel';

export interface Chain {
  /** Zincirden önceki "Etiket:" (listLabel.ts); yoksa ''. */
  label: string;
  /** label ile ilk adım arasındaki boşluk. */
  lead: string;
  steps: string[];
  /** steps[i] ile steps[i+1] arasındaki özgün ayraç (boşluklarıyla). */
  seps: string[];
}

const ARROW = /\s+(?:→|->)\s+/g;
const MAX_STEPS = 16;

const count = (s: string, needle: string) => s.split(needle).length - 1;
function balanced(step: string): boolean {
  return count(step, '`') % 2 === 0 && count(step, '**') % 2 === 0 && count(step, '[') === count(step, ']');
}

export function splitChain(text: string): Chain | null {
  if (!text || text.includes('\n')) return null;
  const split = splitListLabel(text);
  const label = split ? split.label : '';
  const rest = split ? split.rest : text;
  const body = rest.trimStart();
  const lead = rest.slice(0, rest.length - body.length);
  const steps: string[] = [];
  const seps: string[] = [];
  let last = 0;
  for (const m of body.matchAll(ARROW)) {
    const at = m.index ?? 0;
    if (count(body.slice(0, at), '`') % 2 === 1) continue; // `kod` içindeki ok
    steps.push(body.slice(last, at));
    seps.push(m[0]);
    last = at + m[0].length;
  }
  steps.push(body.slice(last));
  if (steps.length < 3 || steps.length > MAX_STEPS) return null;
  if (steps.some(s => s.trim() === '' || !balanced(s))) return null;
  return { label, lead, steps, seps };
}

// "hata", "hatası", "Hata:" … ama "hatasız" değil.
const HATA = /(?:^|[^A-Za-zÇĞİÖŞÜçğıöşü])hata(?!sız)/i;
// Paket-nitelikli exception (com.example.AppException) vurguda mavi kod çipi
// (v0.10.1567: paket adı tek çip) — adım için yine de hata işaretidir.
const EXC_TYPE = /[A-Za-z0-9_$](?:Exception|Error)\b/;

/** Adım hata işareti taşıyor mu: err vurgusu (status error, …Exception, HTTP 5xx, hata kodu) ya da "hata" sözü. */
export function isErrStep(step: string): boolean {
  return HATA.test(step) || EXC_TYPE.test(step) || hasErrPart(highlightParts(step));
}
