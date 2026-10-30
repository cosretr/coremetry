// ExternalLinksTab — v0.10.345: trace sayfası dış link şablonları (operatör:
// function_id + channel_code ile içerideki log izleme platformuna tek tıkla).
// Şablon değişkenleri: {{attr.KEY}} · {{attrTime.KEY:FMT}} · {{time:FMT}} · {{endTime:FMT}} ·
// {{traceId}} · {{service}} · {{requestId}} (v0.10.566, log gövdesindeki istek
// kimliği) (FMT: dd MM yyyy yy HH mm ss). Host adı burada yaşar, repoda değil.
// Önizleme örnek değerlerle saf render'ı gösterir.
//
// v0.10.566 — `group`: aynı gruptaki linklerden yalnız ÇÖZÜLEN İLK link trace
// sayfasında çizilir. Operatörün iki yolu (birincil request_id, yedek
// function_id + channel_code) tek düğmede toplanır.
//
// Faz 2 dilim 1 R8 — shadcn/coremetry: statik tablo shadcn Table (T1
// gerekçesi aynı, kolon seti ve sırası aynı), durum satırı coremetry
// DataTableStateRow, coremetry/Field, shadcn Button; Stack/Row → flex
// utility. Kullanıcının girdiği renk inline stil yerine CSS değişkeniyle
// (`Swatch`, ref üstünden `--swatch`).
import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { Button } from '@/components/shadcn/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { Field } from '@/components/coremetry/Field';
import { DataTableStateRow, type DataTableStateStaticProps } from '@/components/coremetry/DataTable';
import { CopyButton } from '@/components/CopyButton';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import type { ExternalLink } from '@/lib/types';
import { renderExternalLink } from '@/lib/externalLinks';
import { FlashBox, humanize } from './shared';

// v0.10.372 — operatör: "şablona ihtiyacım yok, date'i trace'in olduğu dakika
// ilet" → örnek trace başlangıç dakikasını ({{time}}) taşır; kimlik içindeki
// zaman (attrTime) isteğin üretim anıdır ve trace'ten önce kalabilir.
const EXAMPLE = 'https://log-platformu.example/masterlog?date={{time:ddMMyyyyHHmm}}&functionId={{attr.function_id}}&channelCode={{attr.channel_code}}';
// v0.10.566 (operatör) — BİRİNCİL yol: trace'in loglarının gövdesinde
// request_id varsa link onunla üretilir ve channelCode GÖNDERİLMEZ. İkisi
// aynı GRUPTA olduğu için trace sayfasında tek düğme çizilir: request_id
// çözülürse birincil, çözülmezse function_id yedeği.
const EXAMPLE_REQUEST_ID = 'https://log-platformu.example/masterlog?date={{time:ddMMyyyyHHmm}}&requestId={{requestId}}';
const EXAMPLE_GROUP = 'log-platformu';
const PREVIEW_CTX = {
  traceId: '0fcd70a94ba1f695ea079750e71a7c10', service: 'ornek-servis', startMs: Date.now(), endMs: Date.now() + 1_000,
  attrs: { function_id: '060201abcd00136801642026090416144424810', channel_code: '060201' },
  // Önizleme ÖRNEK değerlerle çalışır; gerçek trace'te request_id log
  // gövdesinden gelir ve olmayabilir (o zaman gruptaki yedek çizilir).
  requestId: 'a1b2c3d4-0000-4f00-9c00-000000000042',
  // v0.10.567 — önizleme de kurumun dilimini gösterir (gerçekte reqid.timezone).
  tz: 'Europe/Istanbul',
};

const MONO_12 = 'font-mono text-[length:var(--fs-sm)]';

/** Operatörün girdiği düğme rengi (serbest metin) — değer CSS değişkeniyle
 *  taşınır, inline stil yok (inlineStyleRatchet; coremetry/DataTable menü
 *  çapasıyla aynı yol). */
function Swatch({ color, className }: { color: string; className: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  useLayoutEffect(() => { ref.current?.style.setProperty('--swatch', color); }, [color]);
  return <span ref={ref} aria-hidden className={cn('inline-block rounded-[3px] bg-(--swatch)', className)} />;
}

export function ExternalLinksTab() {
  const [links, setLinks] = useState<ExternalLink[]>([]);
  // v0.10.967 — tablo standardı dilim 5 (P-2): listenin OKUMA hâli tablonun.
  // undefined = yükleniyor, string = okunamadı (sunucu metni; '' = metinsiz),
  // null = okundu. Eskiden `links` [] ile başlıyordu: yüklenirken ve okuma
  // düşünce tablo "Henüz link yok" diyordu (hata ayrıca FlashBox'ta) — boş
  // ile okunamadı aynı satırdaydı. Okuma hatası artık FlashBox'a değil
  // tablonun hata satırına; FlashBox kaydet/sil geri bildirimi olarak kalır.
  const [readErr, setReadErr] = useState<string | null | undefined>(undefined);
  const [draft, setDraft] = useState<{ label: string; urlTemplate: string; color: string; group: string }>({ label: '', urlTemplate: '', color: '', group: '' });
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  useEffect(() => {
    api.getExternalLinks()
      .then(d => { setLinks(d.links ?? []); setReadErr(null); })
      .catch(e => setReadErr(e instanceof Error ? humanize(e) : ''));
  }, []);
  const save = async (next: ExternalLink[]) => {
    setBusy(true); setMsg(null);
    try {
      const d = await api.putExternalLinks(next);
      setLinks(d.links ?? []);
      // Kayıt yanıtı sunucudaki listenin kendisi: tablo artık doğru.
      setReadErr(null);
      setMsg({ kind: 'ok', text: 'Kaydedildi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : 'Kaydedilemedi' });
    } finally { setBusy(false); }
  };
  const preview = draft.urlTemplate ? renderExternalLink(draft.urlTemplate, PREVIEW_CTX) : null;
  // Okuma düşmüşse (ilk okuma; kayıt başarısı readErr'i temizler) eski satır
  // gösterilmez — hata satırı.
  const showRows = readErr === null && links.length > 0;
  const linksState: Omit<DataTableStateStaticProps, 'colSpan'> =
    readErr === undefined ? { kind: 'loading' }
    : readErr !== null ? { kind: 'error', ...(readErr ? { message: `Linkler okunamadı: ${readErr}` } : {}) }
    : { kind: 'empty', message: 'Henüz link yok' };
  return (
    <div className="flex flex-col gap-4">
      <div className="field-hint">
        Trace sayfasında "CoSRE’ye sor" yanında düğme olarak görünür; şablondaki attribute'lar trace'in
        span'lerinde çözülürse etkin, çözülmezse eksikleri söyleyerek pasif. Değişkenler:{' '}
        <code>{'{{attr.KEY}}'}</code> · <code>{'{{attrTime.KEY:FMT}}'}</code> (değerin içindeki yyyyMMddHHmmss, yeniden biçimlenir) ·{' '}
        <code>{'{{time:FMT}}'}</code> (trace başlangıcı, tarayıcı saati — dakika pencereli log platformları için bunu kullanın; kimlik içindeki zaman isteğin üretim anıdır, trace'ten önce kalabilir) · <code>{'{{endTime:FMT}}'}</code> (trace bitişi) · <code>{'{{traceId}}'}</code> · <code>{'{{service}}'}</code> ·{' '}
        <code>{'{{requestId}}'}</code> (trace'in loglarının gövdesindeki istek kimliği; yoksa şablon çözülmez). FMT: dd MM yyyy yy HH mm ss.
      </div>
      {/* v0.10.566 — grup: aynı log platformuna giden iki şablonu (birincil
          requestId + yedek function_id) tek düğmede topla. */}
      <div className="field-hint">
        <b>Grup:</b> aynı gruptaki linklerden, buradaki sırayla, yalnız <b>çözülen ilk</b> link çizilir; boş = link tek başına.
        Hiçbiri çözülmezse grubun ilk linki pasif olarak eksiklerini söyler.
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      {/* v0.10.942 — statik tablo: düzenlenebilir liste; sıra anlamlı (grupta ilk çözülen çizilir), sıralanmaz (T1).
          v0.10.967 (P-2) — durum satırı DataTableStateRow colSpan = thead'deki 6 <th>; boşken başlık kalır (S6).
          R8: shadcn Table; kolonlar ve sıraları aynı. */}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Etiket</TableHead><TableHead>Şablon</TableHead><TableHead>Grup</TableHead>
            <TableHead>Gerekli</TableHead><TableHead>Renk</TableHead><TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {!showRows ? <DataTableStateRow colSpan={6} {...linksState} /> : links.map((l, i) => (
            <TableRow key={l.label}>
              <TableCell>{l.label}</TableCell>
              <TableCell className="whitespace-normal font-mono [overflow-wrap:anywhere]">{l.urlTemplate} <CopyButton value={l.urlTemplate} title="Şablonu kopyala" /></TableCell>
              <TableCell>{l.group?.trim()
                ? <span className={MONO_12}>{l.group.trim()}</span>
                : <span className="field-hint">tek başına</span>}</TableCell>
              <TableCell className="field-hint">{(l.requires ?? []).join(', ') || '—'}</TableCell>
              <TableCell>{l.color
                ? <span className={MONO_12}><Swatch color={l.color} className="mr-1 size-3 align-middle" />{l.color}</span>
                : <span className="field-hint">ikincil</span>}</TableCell>
              <TableCell><Button variant="ghost-danger" size="sm" disabled={busy} onClick={() => save(links.filter((_, j) => j !== i))}>Sil</Button></TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
      <div className="flex flex-wrap items-end gap-3">
        <Field className="w-[180px]" label="Etiket" value={draft.label} onChange={e => setDraft({ ...draft, label: e.target.value })} placeholder="Log İzleme" />
        <Field className="w-[620px] max-w-full" controlClassName="font-mono" label="URL şablonu" value={draft.urlTemplate} onChange={e => setDraft({ ...draft, urlTemplate: e.target.value })} placeholder={EXAMPLE} />
        {/* v0.10.346 — düğme dolgu rengi (aracın marka rengi); boş = ikincil düğme. */}
        <Field className="w-[110px]" controlClassName="font-mono" label="Renk (#rrggbb)" value={draft.color} onChange={e => setDraft({ ...draft, color: e.target.value })} placeholder="#d32f2f"
          hint={draft.color ? <Swatch color={draft.color} className="h-2.5 w-10" /> : 'boş = gri'} />
        {/* v0.10.566 — grup: aynı gruptan yalnız çözülen ilk link çizilir; boş = tek başına. */}
        <Field className="w-[150px]" controlClassName="font-mono" label="Grup" value={draft.group} onChange={e => setDraft({ ...draft, group: e.target.value })} placeholder={EXAMPLE_GROUP}
          hint="aynı gruptan yalnız çözülen ilk link çizilir; boş = tek başına" />
        <Button size="sm" disabled={busy || !draft.label.trim() || !draft.urlTemplate.trim() || (preview?.missing.length ?? 0) > 0}
          onClick={() => save([...links, { label: draft.label.trim(), urlTemplate: draft.urlTemplate.trim(), color: draft.color.trim() || undefined, group: draft.group.trim() || undefined }]).then(() => setDraft({ label: '', urlTemplate: '', color: '', group: '' }))}>
          Ekle
        </Button>
        {/* İki örnek AYNI grupta: birincil request_id, yedek function_id+channel_code. */}
        <Button variant="secondary" size="sm" onClick={() => setDraft({ ...draft, label: draft.label || 'Log İzleme', urlTemplate: EXAMPLE_REQUEST_ID, group: EXAMPLE_GROUP })}>
          Örnek: requestId (birincil)
        </Button>
        <Button variant="secondary" size="sm" onClick={() => setDraft({ ...draft, label: draft.label || 'Log İzleme (functionId)', urlTemplate: EXAMPLE, group: EXAMPLE_GROUP })}>
          Örnek: functionId (yedek)
        </Button>
      </div>
      {preview && (
        <div className="field-hint">
          Önizleme (örnek değerlerle):{' '}
          {preview.url
            ? <span className="font-mono break-all">{preview.url}</span>
            : <span className="text-(--err)">çözülemedi — eksik: {preview.missing.join(', ')}</span>}
        </div>
      )}
    </div>
  );
}
