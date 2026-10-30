import { useEffect, useRef, useState } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Badge } from '@/components/shadcn/badge';
import { Input } from '@/components/shadcn/input';
import { Textarea } from '@/components/shadcn/textarea';
import { Field, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { useConfirm } from '@/components/coremetry/Confirm';
import { cn } from '@/lib/cn';
import { api } from '@/lib/api';
import { tsLong } from '@/lib/utils';
import { FlashBox } from './shared';
import type { RagDocument } from '@/lib/types';
import { WikiKnowledgeSection } from './WikiKnowledgeSection';

// Faz 2 dilim 1 R12 — inline stil → Tailwind utility; Field2/Row → coremetry
// Field + flex; ui/ Button/DataTable/useConfirm → shadcn/coremetry. Alan
// sırası, metinler, kolonlar, kaydetme gövdesi ve secret sözleşmesi aynı
// (KnowledgeTab.secret.test). Field2 düzeni: geniş alan flex 1 / min 200,
// `small` alan 0 1 180 / min 140.
const WIDE = 'min-w-[200px] flex-1';
const SMALL = 'min-w-[140px] flex-[0_1_180px]';
const ROW = 'flex flex-wrap items-start gap-3';
const H3 = 'mb-1.5 text-[length:var(--fs-md)] font-semibold';
const CTL = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

// v0.9.871 (tutarlılık denetimi BT17) — RAG doküman kataloğu paylaşılan
// primitife geçti. Katalog yüzlere büyüyebilir ve bugün sıralanamıyordu.
// Kolon kümesi/sıra/etiket AYNEN korundu; ek olarak mT4'ün başlık hizası
// bedavaya geliyor: sayısal kolonların BAŞLIKLARI da sağa yaslanıyor
// (hücreler zaten sağdaydı, başlıklar solda kalmıştı).
// v0.10.942 (tablo standardı dilim 3) — görünüm kolon bayraklarında; eylem
// `kind: 'actions'` kolonu, `minWidth = width` eski sabit `trailing` genişliği.
const DOC_COLS: ColumnDef<RagDocument>[] = [
  { id: 'docName',    label: 'Doküman',  sortValue: d => d.docName,          naturalDir: 'asc', flex: true, mono: true },
  { id: 'source',     label: 'Kaynak',   sortValue: d => d.source,           naturalDir: 'asc', width: 150 },
  { id: 'chunks',     label: 'Parça',    sortValue: d => d.chunks,           numeric: true,     width: 100 },
  { id: 'bytes',      label: 'Boyut',    sortValue: d => d.bytes,            numeric: true,     width: 110 },
  { id: 'uploadedBy', label: 'Yükleyen', sortValue: d => d.uploadedBy ?? '', naturalDir: 'asc', width: 170, tone: () => 'muted' },
  { id: 'actions',    label: 'Eylemler', kind: 'actions', width: 90, minWidth: 90 },
];

// KnowledgeTab — doküman soru-cevap (RAG) yapılandırması (v0.8.491).
// v0.8.441'de AI Copilot sekmesinin altına RagSection olarak doğdu;
// wiki kaynakları + doküman kataloğu büyüyünce kendi sekmesine ayrıldı
// (operatör onaylı sadeleştirme #5). Davranış birebir aynı — save
// handler'lar, sentinel sır koruması, senkron akışı değişmedi.
//
// Embedding endpoint'i girilmedikçe RAG tamamen kapalı (chat bugünkü
// gibi çalışır); girilince yüklenen dokümanlar + taranan wiki sayfaları
// chat'te kaynak atıflı cevaplara dönüşür.

// SourceRow — kaynak editörünün yerel satır modeli (v0.8.451). Sunucu
// şifreyi asla geri göndermez; hasPassword/hasHeader "kayıtlı" durumunu
// taşır, boş bırakılan alan kayıtlıyı korur (SMTP deseni).
type SourceRow = {
  url: string;
  username: string;
  password: string;   // operatörün BU oturumda yazdığı; '' = korunur
  authHeader: string; // '' = korunur (kayıtlıysa)
  hasPassword: boolean;
  hasHeader: boolean;
};

export function KnowledgeTab() {
  const confirm = useConfirm();
  const [cfg, setCfg] = useState<import('@/lib/types').RagConfigView | null | undefined>(undefined);
  const [docs, setDocs] = useState<import('@/lib/types').RagDocument[] | null | undefined>(undefined);
  // v0.10.1173 — arkada indekslenen / başarısız belgeler (sunucu belleği).
  const [pending, setPending] = useState<import('@/lib/types').RagPendingDoc[]>([]);
  const [apiKey, setApiKey] = useState('');
  const [sources, setSources] = useState<SourceRow[]>([]);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  // Metin yapıştır (v0.9.176) state'i — TÜM hook'lar erken-return'lerden
  // (if cfg===undefined return <Spinner/>) ÖNCE olmalı. v0.9.178 fix
  // (operatör-bildirimi): bu useState'ler return'lerden SONRAYDI → hook-order
  // ihlali → RAG ayarları sekmesi açılınca crash.
  const [pasteOpen, setPasteOpen] = useState(false);
  const [pasteName, setPasteName] = useState('');
  const [pasteText, setPasteText] = useState('');
  const fileRef = useRef<HTMLInputElement>(null);

  const load = () => {
    api.getRagConfig().then(c => {
      setCfg(c);
      setSources((c.sources ?? []).map(s0 => ({
        url: s0.url,
        username: s0.username ?? '',
        password: '',
        authHeader: '',
        hasPassword: s0.password === '********',
        hasHeader: s0.authHeader === '********',
      })));
    }).catch(() => setCfg(null));
    api.listRagDocuments().then(r => { setDocs(r.documents); setPending(r.pending ?? []); }).catch(() => setDocs(null));
  };
  useEffect(load, []);
  // v0.10.1173 — indeksleme sürerken liste 10 sn'de bir yenilenir (sekme gizliyken durur).
  const indexing = pending.some(p => p.state === 'indexing');
  useEffect(() => {
    if (!indexing) return;
    const t = window.setInterval(() => { if (!document.hidden) load(); }, 10_000);
    return () => window.clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- load kararlı değil ama okuduğu her şey setter
  }, [indexing]);

  if (cfg === undefined) return <Spinner />;
  if (cfg === null) return <Empty icon="📄" title="RAG ayarları yüklenemedi" />;
  // v0.10.1172 — yükleme RAG kapalıyken de açık olabilir (wiki tek indeks).
  const uploadOn = cfg.uploadAllowed ?? cfg.enabled;

  // v0.10.954 — tablo standardı T12: doküman listesinin yükleniyor / hata /
  // boş hâli tablonun İÇİNDE, başlık durur (RAG ayar kapısı yukarıda kalır).
  const docsState: DataTableStateInput<RagDocument> =
    docs === undefined ? { kind: 'loading' }
    : docs === null ? { kind: 'error' }
    : { kind: 'empty', message: 'Henüz doküman yok' };

  const save = async () => {
    setBusy(true); setMsg(null);
    try {
      // Boş şifre/header kayıtlıyken '********' sentineliyle gider —
      // sunucu URL eşleşmesinden mevcut değeri devralır; yeni yazılan
      // düz gider (bir daha asla geri dönmez).
      const outSources = sources
        .filter(s0 => s0.url.trim())
        .map(s0 => ({
          url: s0.url.trim(),
          username: s0.username.trim() || undefined,
          password: s0.password ? s0.password : (s0.hasPassword ? '********' : undefined),
          authHeader: s0.authHeader.trim() ? s0.authHeader.trim() : (s0.hasHeader ? '********' : undefined),
        }));
      const next = await api.putRagConfig({
        endpoint: cfg.endpoint, model: cfg.model, enabled: cfg.enabled,
        topK: cfg.topK, apiKey: apiKey || undefined, sources: outSources,
        insecureSkipVerify: cfg.insecureSkipVerify || undefined,
      });
      setCfg(next); setApiKey('');
      setSources(prev => prev.filter(s0 => s0.url.trim()).map(s0 => ({
        ...s0,
        password: '',
        authHeader: '',
        hasPassword: s0.hasPassword || !!s0.password,
        hasHeader: s0.hasHeader || !!s0.authHeader.trim(),
      })));
      setMsg({ kind: 'ok', text: 'Kaydedildi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const upload = async (f: File) => {
    setBusy(true); setMsg(null);
    try {
      const r = await api.uploadRagDocument(f);
      setMsg({ kind: 'ok', text: r.status === 'indexing'
        ? `${f.name} alındı — arkada indeksleniyor; liste bitince güncellenir.`
        : `${f.name}: ${r.chunks} parça indekslendi.` });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); if (fileRef.current) fileRef.current.value = ''; }
  };

  // pasteDoc — yapıştırılan metni doküman olarak ekler ({name,text} JSON
  // yolu; ilgili state yukarıda, erken-return'lerden ÖNCE tanımlı).
  const pasteDoc = async () => {
    const name = pasteName.trim(), text = pasteText.trim();
    if (!name || !text) { setMsg({ kind: 'err', text: 'İsim ve metin zorunlu.' }); return; }
    setBusy(true); setMsg(null);
    try {
      const r = await api.uploadRagText(name, text);
      setMsg({ kind: 'ok', text: r.status === 'indexing'
        ? `${name} alındı — arkada indeksleniyor; liste bitince güncellenir.`
        : `${name}: ${r.chunks} parça indekslendi.` });
      setPasteName(''); setPasteText(''); setPasteOpen(false);
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const syncSources = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await api.syncRagSources();
      setMsg({ kind: 'ok', text: `Senkron: ${r.pages} sayfa · ${r.indexed} indekslendi · ${r.skipped} değişmemiş · ${r.pruned} silindi${r.errors?.length ? ` · ${r.errors.length} hata` : ''}` });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const removeDoc = async (d: RagDocument) => {
    if (!await confirm({
      title: 'Belge silinsin mi?',
      body: <><b>{d.docName}</b> ve ondan üretilmiş
        {' '}{d.chunks} parça bilgi tabanından silinecek;
        CoSRE artık bu belgeden alıntı yapamaz.</>,
      confirmLabel: 'Belgeyi sil',
      danger: true,
    })) return;
    try { await api.deleteRagDocument(d.docId); load(); }
    catch (e) { setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) }); }
  };

  const docCell = (d: RagDocument, col: ColumnDef<RagDocument>) => {
    switch (col.id) {
      case 'docName': return d.docName;
      case 'source': return <Badge variant="secondary">{d.source}</Badge>;
      case 'chunks': return d.chunks;
      case 'bytes': return `${(d.bytes / 1024).toFixed(1)} KB`;
      case 'uploadedBy': return d.uploadedBy;
      case 'actions': return (
        <Button variant="destructive" size="sm" disabled={busy} onClick={() => { void removeDoc(d); }}>
          Sil
        </Button>
      );
      default: return null;
    }
  };

  const setSource = (i: number, patch: Partial<SourceRow>) =>
    setSources(p => p.map((x, j) => j === i ? { ...x, ...patch } : x));

  return (
    <div className="max-w-[640px]">
      <h2 className="mb-1.5 flex flex-wrap items-center gap-2 text-[length:var(--fs-lg)] font-semibold">
        Doküman soru-cevap (RAG)
        {!cfg.enabled
          ? <StatusBadge status="unknown">kapalı</StatusBadge>
          : cfg.endpoint
            ? <StatusBadge status="unknown">aktif · semantik</StatusBadge>
            : <StatusBadge status="warn">aktif · keyword modu</StatusBadge>}
      </h2>
      <p className="mb-3 text-[length:var(--fs-md)] text-(--text2)">
        Runbook / prosedür / mimari dokümanlarını yükle; CoSRE chat sorulara bu
        dokümanlardan <b>kaynak atıflı</b> cevap versin. <b>Embedding endpoint'i
        olmadan da çalışır</b> (keyword/BM25 modu, v0.9.162); OpenAI-uyumlu bir
        <code> /v1/embeddings</code> (vLLM/KServe'de bge-m3) eklersen retrieval
        semantiğe (TR↔EN recall) yükselir.
      </p>

      <div className={ROW}>
        <Field className={WIDE} label="Embedding endpoint" hint="ör. http://bge-m3.ai.svc:8000/v1"
          value={cfg.endpoint} onChange={e => setCfg({ ...cfg, endpoint: e.target.value })}
          placeholder="http://…/v1" />
        <Field className={SMALL} label="Model" hint="ör. BAAI/bge-m3"
          value={cfg.model} onChange={e => setCfg({ ...cfg, model: e.target.value })} />
        <Field className={SMALL} label="Top-K" hint="cevaba girecek parça sayısı (1-20)"
          type="number" min={1} max={20} value={cfg.topK ?? 5}
          onChange={e => setCfg({ ...cfg, topK: Number(e.target.value) })} />
      </div>
      <div className={cn(ROW, 'mt-3')}>
        <Field className={SMALL} label="API key (opsiyonel)"
          hint={cfg.hasKey ? 'kayıtlı — boş bırakırsan korunur' : 'endpoint auth istemiyorsa boş bırak'}
          type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
          placeholder={cfg.hasKey ? '********' : ''} />
        <CheckboxField className="mt-[18px]" label="RAG aktif" checked={cfg.enabled}
          onCheckedChange={v => setCfg({ ...cfg, enabled: v })} />
        {/* v0.9.23 — operatör-raporlu: self-signed embedding/wiki
            sertifikaları upload'ı düşürüyordu. */}
        <CheckboxField className="mt-2 ml-4" checked={!!cfg.insecureSkipVerify}
          label={<>Skip TLS verify <span className="text-[length:var(--fs-xs)] italic text-(--text3)">(self-signed embedding/wiki endpoints)</span></>}
          onCheckedChange={v => setCfg({ ...cfg, insecureSkipVerify: v })} />
        <Button className="mt-3" onClick={() => { void save(); }} loading={busy}>
          Kaydet
        </Button>
      </div>

      {/* Wiki / URL kaynakları (v0.8.442; v0.8.451 yapılandırılmış
          editör) — kaynak başına URL + opsiyonel Basic kimlik
          (on-prem Azure DevOps: kullanıcı boş, PAT şifreye) veya ham
          header. Şifre/header kayıtlıysa boş alan korur; yeni değer
          yazınca değişir, hiçbir sır geri echo edilmez. */}
      <div className="mt-4">
        <h3 className={H3}>Wiki / URL kaynakları</h3>
        <p className="mb-1.5 text-[length:var(--fs-xs)] text-(--text3)">
          Aynı host + path altındaki sayfalar taranır (≤200 sayfa, derinlik 3),
          30 dk'da bir otomatik senkron; değişmeyen sayfa yeniden indekslenmez.
          Kimlik isteyen wiki'de (ör. on-prem Azure DevOps) kullanıcı+şifre gir —
          PAT kullanıyorsan kullanıcıyı boş bırak, PAT'i şifre alanına yaz.
        </p>
        <div className="grid gap-1.5">
          {sources.map((src, i) => (
            <div key={i} role="group" aria-label={`Kaynak ${i + 1}`} className="flex items-center gap-1.5">
              <Input value={src.url} placeholder="https://azuredevops.banka.local/DefaultCollection/Proje/_wiki/…"
                aria-label="Kaynak URL"
                onChange={e => setSource(i, { url: e.target.value })}
                spellCheck={false}
                className={cn(CTL, 'flex-[3] font-mono')} />
              <Input value={src.username} placeholder="kullanıcı (PAT'te boş)"
                aria-label="Kullanıcı"
                autoComplete="off"
                onChange={e => setSource(i, { username: e.target.value })}
                className={cn(CTL, 'flex-1')} />
              <Input type="password" value={src.password}
                aria-label="Şifre / PAT"
                placeholder={src.hasPassword ? '******** (kayıtlı)' : 'şifre / PAT'}
                autoComplete="new-password"
                title={src.hasPassword ? 'Kayıtlı — boş bırakırsan korunur' : ''}
                onChange={e => setSource(i, { password: e.target.value })}
                className={cn(CTL, 'flex-1')} />
              <Input value={src.authHeader}
                aria-label="Ham auth header"
                placeholder={src.hasHeader ? '******** (kayıtlı header)' : 'Header: değer (ops.)'}
                title="Ham auth header — doluysa Basic yerine bu gönderilir"
                spellCheck={false}
                onChange={e => setSource(i, { authHeader: e.target.value })}
                className={cn(CTL, 'flex-1 font-mono text-[length:var(--fs-xs)] md:text-[length:var(--fs-xs)]')} />
              <Button variant="outline" size="sm" aria-label="Kaynağı kaldır"
                title="Kaynağı kaldır (kayıtlı sırrıyla birlikte)"
                onClick={() => setSources(p => p.filter((_, j) => j !== i))}>✕</Button>
            </div>
          ))}
          <div>
            <Button variant="outline" size="sm"
              onClick={() => setSources(p => [...p, {
                url: '', username: '', password: '', authHeader: '',
                hasPassword: false, hasHeader: false,
              }])}>
              ＋ Kaynak ekle
            </Button>
          </div>
        </div>
        <div className="mt-1.5 flex items-center gap-2">
          <Button variant="outline" size="sm" disabled={busy}
            onClick={() => { void save(); }}>
            Kaynakları kaydet
          </Button>
          <Button variant="outline" size="sm"
            disabled={busy || !cfg.enabled}
            title={cfg.endpoint ? 'Tüm kaynakları şimdi tara' : 'Tüm kaynakları tara (keyword modu — embedding yok)'}
            onClick={() => { void syncSources(); }}>
            ⟳ Şimdi senkronize et
          </Button>
        </div>
      </div>

      {/* v0.10.1122 — Azure DevOps wiki bilgisi (DevOps bağlantısıyla; genel
          URL tarayıcısının yanında, ondan bağımsız). */}
      <WikiKnowledgeSection />

      <div className="mt-4 flex flex-wrap items-center gap-2.5">
        <h3 className="m-0 text-[length:var(--fs-md)] font-semibold">Dokümanlar</h3>
        {/* Yerel dosya girdisi bilinçli olarak native (gizli; düğme açar). */}
        <input ref={fileRef} type="file" accept=".md,.txt,.pdf,.xlsx,.csv,.json,.yaml,.yml" hidden
               onChange={e => { const f = e.target.files?.[0]; if (f) void upload(f); }} />
        <Button variant="outline" size="sm"
          disabled={busy || !uploadOn}
          title={cfg.endpoint ? 'md / txt / pdf / xlsx / csv / json / yaml yükle (≤25MB; tabloda her satır, JSON / YAML içinde her kayıt ayrı)' : 'md / txt / pdf / xlsx / csv / json / yaml yükle (≤25MB; tabloda her satır, JSON / YAML içinde her kayıt ayrı) — keyword modu, embedding yok'}
          onClick={() => fileRef.current?.click()}>
          ⬆ Doküman yükle
        </Button>
        <Button variant="outline" size="sm"
          disabled={busy || !uploadOn}
          title="Tarayıcıdan (OneNote / wiki) kopyalanan metni yapıştır — dosya/token gerekmez"
          onClick={() => setPasteOpen(o => !o)}>
          📋 Metin yapıştır
        </Button>
      </div>
      {/* v0.10.1173 — arkada indekslenen / başarısız belgeler. */}
      {pending.length > 0 && (
        <div className="mt-2 grid gap-1 text-[length:var(--fs-sm)]" aria-live="polite">
          {pending.map(p => (
            <div key={p.docId} className={p.state === 'failed' ? 'text-(--err)' : 'text-(--text2)'}>
              {p.state === 'failed' ? `✕ ${p.docName}: indekslenemedi — ${p.error ?? 'bilinmeyen hata'}` : `⏳ ${p.docName}: indeksleniyor…`}
            </div>
          ))}
        </div>
      )}
      {pasteOpen && (
        <div className="mt-2.5 flex max-w-[640px] flex-col gap-2">
          <Input value={pasteName} onChange={e => setPasteName(e.target.value)}
            aria-label="Doküman adı"
            placeholder="Doküman adı (ör. OneNote — DB failover runbook)"
            className={CTL} />
          <Textarea value={pasteText} onChange={e => setPasteText(e.target.value)}
            aria-label="Yapıştırılan metin"
            placeholder="OneNote sayfasında Ctrl+A → Ctrl+C, buraya yapıştır…" rows={8}
            className="field-sizing-fixed min-h-0 resize-y bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field" />
          <div className="flex items-center gap-2">
            <Button size="sm"
              disabled={busy || !uploadOn || !pasteName.trim() || !pasteText.trim()}
              onClick={() => void pasteDoc()}>
              Ekle
            </Button>
            <span className="text-[length:var(--fs-xs)] text-(--text3)">{pasteText.length.toLocaleString()} karakter</span>
          </div>
        </div>
      )}

      <div className="table-wrap mt-2">
        <DataTable<RagDocument> storageKey="settings-rag-documents" columns={DOC_COLS} rows={docs ?? []}
          sort={{ initial: { id: 'docName', dir: 'asc' } }}
          getRowKey={d => d.docId}
          state={docsState}
          renderCell={docCell} />
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}

      <hr className="mt-6 mb-[18px] border-0 border-t border-(--border)" />
      <KBCandidatesSection enabled={cfg.enabled} onCurated={load} />
    </div>
  );
}

// ── KBCandidatesSection (v0.9.1195, AI Faz 5.2) — terfi kuyruğu ─────────
//
// Döngünün kapanan halkası: 👍'lı cevaplar burada aday listelenir; "KB'ye
// ekle" Soru+Cevap'ı rag_chunks'a source='curated' olarak yazar ve RAG bir
// dahaki benzer soruda onu bulur. KB yalnız ONAYLA büyür — 👍'lı her şeyi
// otomatik almak tek yanlış oyla bilgi tabanını zehirlerdi.
//
// Veri sekme AÇILINCA bir kez çekilir (60 sn sunucu cache'li admin
// okuması); poll yok. Terfi eden satır listeden düşer — sunucu tarafında
// da düşmüştür (NOT IN curated), yani yenile aynı sonucu verir.
function KBCandidatesSection({ enabled, onCurated }: { enabled: boolean; onCurated: () => void }) {
  const [rows, setRows] = useState<import('@/lib/types').KBCandidate[] | null | undefined>(undefined);
  const [busy, setBusy] = useState('');
  const [note, setNote] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.listKBCandidates().then(r => setRows(r.rows)).catch(() => setRows(null));
  }, []);

  const promote = async (xid: string) => {
    setBusy(xid); setNote(null);
    try {
      const r = await api.curateKBCandidate(xid);
      setRows(prev => (prev ?? []).filter(c => c.exchangeId !== xid));
      setNote({ kind: 'ok', text: `KB'ye eklendi (${r.chunks} parça).` });
      onCurated(); // doküman kataloğu yukarıda — yeni curated satırı görünsün
    } catch (e) {
      setNote({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(''); }
  };

  return (
    <div>
      <h3 className={H3}>
        Terfi kuyruğu <span className="font-normal text-(--text3)">— 👍 alan cevaplar (son 30 gün)</span>
      </h3>
      <p className="mb-2.5 text-[length:var(--fs-sm)] text-(--text2)">
        Onayladığın soru-cevap çifti bilgi tabanına <code>curated</code> dokümanı
        olarak girer ve CoSRE benzer sorularda kaynak atıflı kullanır.
        {!enabled && <b> RAG kapalı — terfi için önce yukarıdan etkinleştir.</b>}
      </p>
      {rows === undefined && <Spinner />}
      {rows === null && <div className="err text-[length:var(--fs-sm)]">Aday listesi okunamadı.</div>}
      {rows && rows.length === 0 && (
        <div className="text-[length:var(--fs-sm)] text-(--text3)">
          Bekleyen aday yok — 👍 alan yeni cevaplar burada birikir.
        </div>
      )}
      {rows && rows.length > 0 && (
        <div className="flex flex-col gap-2">
          {rows.map(c => (
            <div key={c.exchangeId}
              className="flex items-start gap-2.5 rounded-md border border-(--border) px-2.5 py-2 text-[length:var(--fs-sm)]">
              <div className="min-w-0 flex-1">
                <div className="mb-1 flex items-baseline gap-1.5">
                  <Badge variant="secondary">{c.surface || '—'}</Badge>
                  <span className="text-[length:var(--fs-xs)] text-(--text3)">{tsLong(c.createdAt)}</span>
                  {c.userEmail && <span className="text-[length:var(--fs-xs)] text-(--text3)">👍 {c.userEmail}</span>}
                </div>
                <div className="truncate" title={c.prompt}><b>S:</b> {c.prompt || '—'}</div>
                <div className="truncate text-(--text2)" title={c.response}><b>C:</b> {c.response}</div>
              </div>
              <Button variant="outline" size="sm"
                disabled={!enabled || busy !== ''}
                loading={busy === c.exchangeId}
                onClick={() => void promote(c.exchangeId)}>
                KB&apos;ye ekle
              </Button>
            </div>
          ))}
        </div>
      )}
      {note && <FlashBox kind={note.kind}>{note.text}</FlashBox>}
    </div>
  );
}
