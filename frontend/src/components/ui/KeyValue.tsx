import type { HTMLAttributes, ReactNode } from 'react';

// KeyValue — v0.10.939 (tablo standardı T1, dilim 2: primitif katmanı).
//
// Öznitelik panelinin (span, log, rollout, pod …) TEK atomu. Operatör onayı
// 2026-09-26, mockup "Coremetry Tablo Standardı" → "Anahtar/değer: bugün 6
// ayrı yazım, önerisi tek atom". Envanter 21 yer saydı: 15 `<table>`
// (`.ps-kv`, `.kv-table`, başlıklı "Anahtar/Değer", `<th>` satırlı), 4 div
// ızgarası (auto/92/110px etiket), 2 flex yardımcı (biri hâlâ büyük harf).
// Etiket genişliği her yerde başkaydı (auto/92/110/140/180px), her değer
// monospace'ti, uzun kimlik harf ortasından kırılıyordu (`break-all`).
// Göç dilim 3'te; bu dilim yalnız atomu ve sözleşmesini getirir — bugün
// hiçbir sayfa bunu çizmiyor, yani görünür fark YOK.
//
// ── NEDEN TABLO DEĞİL ──────────────────────────────────────────────────
// Öznitelik bir KAYIT değil: sıralanmaz, yeniden boyutlanmaz, sırası anlam
// taşır (§5 "Öznitelik (anahtar/değer) → KeyValue atomu — tablo değil").
// Semantik karşılığı `<dl>`: `<dt>` etiket, `<dd>` değer. Her çift bir
// `<div>` içinde (WHATWG HTML `dl > div > dt + dd` gruplamasına izin verir)
// — satır ayracı, hover/odak açığa çıkarması ve etiket genişliği tek bir
// satır kutusuna asılabilsin diye.
//
// ── GÖRÜNÜM SÖZLEŞMESİ (CSS: globals.css `.keyval` ailesi) ─────────────
//   • TEK etiket genişliği: `--kv-label-w` (varsayılan 150px, mockup);
//     `labelWidth="wide"` genişletir. Dar kapta etiket %40'ı aşmaz.
//   • Etiket --text2, büyük harf YOK; değer --text.
//   • Monospace YALNIZ kimlikte: satır `mono` işaretiyle ister (trace/span
//     id, pod adı, hash, SQL). Etiket hiçbir zaman mono değil.
//   • Uzun değer `overflow-wrap: anywhere` ile sarar — ASLA `break-all`.
//     `white-space: pre-wrap`: çok satırlı öznitelik (SQL, komut satırı)
//     yapısını korur (öznitelikler olduğu gibi — tam doğruluk).
//   • Satırlar --divider ile ayrılır; zebra yok; DIŞ ÇERÇEVE YOK (T10:
//     çerçeveyi çağıranın kabı çizer). Satır tıklanmaz → hover zemini ve
//     el imleci YOK (T2).
//   • Boş değer (`null`/`undefined`/`''`/`false`) soluk "—" çizer. `0`
//     bir değerdir ve AYNEN çizilir.
//   • Satır sonu eylem yuvası (`actions`, ör. ⊕/⊖ filtre IconButton'ları)
//     satır hover'ında ve `:focus-within`de belirir (klavye odağı gizli
//     düğmeye inmesin); dokunmatikte (hover: none) hep görünür.
//     `.kv-actions` deseninin ikizi.
//
// ── SINIF LİSTESİ: ev deseni `const classes = [...]` ────────────────────
// v0.10.939 (tablo standardı T1) — taban `keyval` dizinin İLK literali:
// `primitiveClasses.test.ts`in sahiplik kapısı onu bu atomun tabanı sayar ve
// ui/ dışında elle `className="keyval"` yazanı işaretler (göç dilim 3'te
// 21 elle yazım bu atoma geçerken ikinci bir `.keyval` doğmasın). Aynı
// kapının ":hover arka plan kaçağı" iddiası yalnız KÖKÜ <button> olan
// atomlara bakar — kaçağın kaynağı element-seviyesi `button:hover`. KV'nin
// kökü <dl>: satırının hover zemini YOK (T2) ve `.keyval__row:hover
// .keyval__acts { opacity: 1 }` açığa çıkarma kuralı kaçak sayılmaz.
// Değiştirici sınıflar `Record<…, string>` haritalarında: kapının "CSS
// karşılığı var mı" yarısı onları da görür.

/** Etiket sütunu genişliği basamağı. */
export type KeyValueLabelWidth = 'default' | 'wide';

// v0.10.939 (tablo standardı T1) — genişlik sınıfı; `default` sınıf basmaz
// (`.keyval` tabanı `--kv-label-w`yi tanımlar).
const labelWidthClass: Record<KeyValueLabelWidth, string> = {
  default: '',
  wide: 'keyval--wide',
};

type ValueKind = 'plain' | 'mono' | 'empty';

// v0.10.939 (tablo standardı T1) — değer türü → değiştirici. `empty` mono'yu
// da ezer: "—" bir kimlik değil.
const valueKindClass: Record<ValueKind, string> = {
  plain: '',
  mono: 'keyval__val--mono',
  empty: 'keyval__val--empty',
};

/** Boş sayılan değerler. `0` ve `'0'` BOŞ DEĞİL. `false` boş: React onu
 *  hiç çizmez, `{cond && x}` ile geçilen değer boş bir `<dd>` bırakırdı. */
function isEmptyValue(v: ReactNode): boolean {
  return v === null || v === undefined || v === false || v === '';
}

export interface KeyValueRowProps {
  /** Etiket — arayüz fontunda, --text2. Öznitelik anahtarı da (ör.
   *  `k8s.pod.name`) etikettir; mono DEĞİL (mockup). */
  k: ReactNode;
  /** Değer. Boşsa (`null`/`undefined`/`''`/`false`) soluk "—". */
  v?: ReactNode;
  /** Değer bir KİMLİK mi (trace/span id, pod adı, hash, SQL)? Yalnız o
   *  zaman monospace. Ad, sayı, süre → işaretleme. */
  mono?: boolean;
  /** Değerin tam hâli / açıklaması (yerel ipucu). Boş değerde basılmaz. */
  title?: string;
  /** Satır sonu eylem yuvası (ör. ⊕/⊖ `IconButton`'lar). Hover ve
   *  `:focus-within`de belirir; dokunmatikte hep görünür. */
  actions?: ReactNode;
}

/** Bir öznitelik satırı: `<div><dt/><dd/></div>`. `KeyValue` içinde kullanılır. */
export function KeyValueRow({ k, v, mono, title, actions }: KeyValueRowProps) {
  const empty = isEmptyValue(v);
  const kind: ValueKind = empty ? 'empty' : mono ? 'mono' : 'plain';
  const valCls = ['keyval__val', valueKindClass[kind]].filter(Boolean).join(' ');
  return (
    <div className="keyval__row">
      <dt className="keyval__k">{k}</dt>
      <dd className="keyval__v">
        <span className={valCls} title={empty ? undefined : title}>{empty ? '—' : v}</span>
        {!isEmptyValue(actions) && <span className="keyval__acts">{actions}</span>}
      </dd>
    </div>
  );
}

/** `items` biçiminin bir satırı. `id` React anahtarıdır (verilmezse sıra). */
export interface KeyValueItem extends KeyValueRowProps {
  id?: string;
}

export interface KeyValueProps extends HTMLAttributes<HTMLDListElement> {
  /** Veri biçimi: satırlar dizisi. `children` ile birlikte verilirse önce
   *  `items` çizilir. */
  items?: readonly KeyValueItem[];
  /** Kompozisyon biçimi: `<KeyValueRow>` çocukları (koşullu satırlar için). */
  children?: ReactNode;
  /** Etiket sütunu: `default` 150px, `wide` uzun OTel anahtarları için. */
  labelWidth?: KeyValueLabelWidth;
}

/**
 * Öznitelik paneli: `<dl class="keyval">`. Çerçeve çizmez — çağıranın kabı
 * (kart, çekmece bölümü) çizer.
 *
 *   <KeyValue items={[
 *     { k: 'Service', v: svc },
 *     { k: 'Trace ID', v: traceId, mono: true, actions: <IconButton … /> },
 *   ]} />
 */
export function KeyValue({ items, children, labelWidth = 'default', className, ...rest }: KeyValueProps) {
  const classes = [
    'keyval',
    labelWidthClass[labelWidth],
    className,
  ].filter(Boolean).join(' ');
  return (
    <dl className={classes} {...rest}>
      {items?.map((it, i) => (
        <KeyValueRow key={it.id ?? i} k={it.k} v={it.v} mono={it.mono} title={it.title} actions={it.actions} />
      ))}
      {children}
    </dl>
  );
}
