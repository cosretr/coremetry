import { useEffect, useState, type FormEvent } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { ButtonGroup } from '@/components/shadcn/button-group';
import { Input } from '@/components/shadcn/input';
import { NativeSelect } from '@/components/shadcn/native-select';
import { Checkbox } from '@/components/shadcn/checkbox';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/coremetry/overlay';
import { DataTable, type ColumnDef, type DataTableStateInput } from '@/components/coremetry/DataTable';
import { cn } from '@/lib/cn';
import { api, type PipelineRule } from '@/lib/api';
import type { MetricExclusionRule } from '@/lib/types';
import { FlashBox, humanize } from './shared';
import { buildPipelineRuleBody, describeCondition } from './pipelineRuleBody';

// Faz 2 dilim 1 R14 — inline stil → Tailwind utility; ui/ DataTable kancası
// → coremetry DataTable (renderCell), Modal → coremetry/overlay Dialog,
// shared Field + elle yazılmış etiketler → coremetry Field ailesi. Kolon
// kümesi/sırası, kural gövdesi (buildPipelineRuleBody) ve metinler aynı.
const LABEL = 'text-[length:var(--fs-sm)] leading-tight text-(--text2)';
const HINT = 'text-[length:var(--fs-xs)] leading-snug text-(--text3)';
const CTL = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

// v0.9.875 (tutarlılık denetimi MT6) — kural tablosu paylaşılan primitife.
// v0.9.797'den beri türetilmiş-metrik kuralları da buraya yazılıyor ve liste
// SINIRSIZ büyüyor; sıralama yoktu.
//
// Predicate kolonu birleşik bir gösterim (when + AND'ler + setAttributes /
// rate). Sıralama görüneni takip etsin diye anahtar+op+değer üzerinden
// yapılıyor — aynı attr'ı hedefleyen kurallar yan yana geliyor, ki bu
// listedeki en sık soru ("bu key'e dokunan kaç kural var").
// v0.10.942 (tablo standardı dilim 3) — yüklem kodu `mono` bayrağıyla (11px
// punto düştü; iç tonlar değer/AND ayrımını taşıyor); eylemler `kind:
// 'actions'` kolonu, `minWidth = width` eski sabit `trailing` genişliği.
const RULE_COLS: ColumnDef<PipelineRule>[] = [
  { id: 'name',      label: 'Name',      sortValue: r => r.name,   naturalDir: 'asc', width: 200 },
  { id: 'kind',      label: 'Kind',      sortValue: r => r.kind,   naturalDir: 'asc', width: 110 },
  { id: 'signal',    label: 'Signal',    sortValue: r => r.signal, naturalDir: 'asc', width: 110 },
  { id: 'predicate', label: 'Predicate',
    sortValue: r => `${r.when.key} ${r.when.op} ${r.when.value}`, naturalDir: 'asc', flex: true, mono: true },
  { id: 'enabled',   label: 'Enabled',   sortValue: r => (r.enabled ? 1 : 0), width: 95 },
  { id: 'actions',   label: 'Actions',   kind: 'actions', width: 160, minWidth: 160 },
];

// ── Pipeline tab (v0.5.263; logs + metrics v0.8.282) ────────────────────────
//
// Ingest-time drop / enrich / sample rules for spans, logs, AND metrics.
// Operator picks e.g. "service.name = frontend" and any matching record of
// the chosen signal gets dropped (or enriched / sampled) before the
// consumer sees it — no CH write. Per-signal drop counts are exposed on
// /admin/stats so the effect is observable without log-grepping.
// SPAN_ROUTE_PREFILL — "Metrik dışlamaları" kartındaki span köprüsünün
// açtığı yeni-kural taslağı (v0.9.802). Kart RE2 desenleriyle çalıştığı
// için operatör aynı dilde devam etsin diye `=~` seçili gelir; anahtar
// `http.route`, ki v0.9.802'den beri TÜRETİLMİŞ route alanından eşleşir
// (url.path basan servisler dahil).
const SPAN_ROUTE_PREFILL: Partial<PipelineRule> = {
  kind: 'drop',
  signal: 'spans',
  when: { key: 'http.route', op: '=~', value: '' },
};

export function PipelineTab() {
  const confirm = useConfirm();
  const [rules, setRules] = useState<PipelineRule[] | null | undefined>(undefined);
  const [editing, setEditing] = useState<PipelineRule | null>(null);
  const [creating, setCreating] = useState(false);
  // spanPrefill — creating ile birlikte yaşar; hangi taslakla açıldığını
  // söyler ("+ New rule" boş, dışlama kartının köprüsü span/route dolu).
  const [spanPrefill, setSpanPrefill] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const load = () => {
    setRules(undefined);
    api.listPipelineRules().then(r => setRules(r.rules ?? [])).catch(() => setRules(null));
  };
  useEffect(load, []);

  const toggle = async (r: PipelineRule) => {
    try {
      await api.upsertPipelineRule({ ...r, enabled: !r.enabled });
      setMsg({ kind: 'ok', text: `${r.name}: ${!r.enabled ? 'enabled' : 'disabled'}` });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    }
  };

  const remove = async (r: PipelineRule) => {
    if (!await confirm({
      title: 'Pipeline kuralı silinsin mi?',
      body: <><b>{r.name}</b> kuralı kaldırılacak. Bu kuralla eşleşen kayıtlar
        artık etkilenmeyecek — dışlanan kayıtlar ingest’e girmeye başlar.</>,
      confirmLabel: 'Kuralı sil',
      danger: true,
    })) return;
    try {
      await api.deletePipelineRule(r.id);
      setMsg({ kind: 'ok', text: `Deleted "${r.name}"` });
      load();
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    }
  };

  if (rules === undefined) return <Spinner />;
  if (rules === null) return <Empty icon="!" title="Failed to load pipeline rules" />;

  // v0.10.954 — tablo standardı T12: boş durum tablonun İÇİNDE, başlık
  // durur (sekme düzeyindeki yükleniyor / hata dönüşleri kalır).
  const ruleState: DataTableStateInput<PipelineRule> = {
    kind: 'empty',
    message: "Henüz pipeline kuralı yok — gürültülü span trafiğini ingest'te düşürmek için bir kural oluşturun.",
  };

  const ruleCell = (r: PipelineRule, col: ColumnDef<PipelineRule>) => {
    switch (col.id) {
      case 'name': return <span className="font-semibold text-(--text)">{r.name}</span>;
      case 'kind': return (
        <StatusBadge status={r.kind === 'drop' ? 'critical' : 'info'}>
          {r.kind.toUpperCase()}
        </StatusBadge>
      );
      case 'signal': return <code className="text-[length:var(--fs-xs)]">{r.signal}</code>;
      case 'predicate': return (
        <>
          {r.when.key} <b>{r.when.op}</b>{' '}
          <span className="text-(--text2)">"{r.when.value}"</span>
          {/* v0.9.803 — EK koşullar listede de görünür. Yalnız
              `when`'i basmak, tek-metrik kısıtlı türetilmiş bir
              kuralı "her metrik" gibi okutuyordu. */}
          {(r.and ?? []).map((c, i) => (
            <span key={i}>
              {' '}<b className="text-(--text3)">AND</b>{' '}
              {c.key} <b>{c.op}</b>{' '}
              <span className="text-(--text2)">"{c.value}"</span>
            </span>
          ))}
          {r.kind === 'enrich' && r.setAttributes && Object.entries(r.setAttributes).map(([k, v]) => (
            <span key={k} className="ml-2 text-(--accent2)">
              → {k}=<b>"{v}"</b>
            </span>
          ))}
          {r.kind === 'sample' && r.rate != null && (
            <span className="ml-2 text-(--accent2) tabular-nums">
              keep <b>{(r.rate * 100).toFixed(1)}%</b>
            </span>
          )}
        </>
      );
      case 'enabled': return (
        <Checkbox checked={r.enabled} aria-label={`${r.name} enabled`}
          onCheckedChange={() => { void toggle(r); }} />
      );
      case 'actions': return (
        // R14 (D5): satır eylemleri ghost `sm`, aynı iki eylem aynı sırada.
        <ButtonGroup aria-label={`${r.name} actions`} className="ml-auto">
          <Button variant="ghost" size="sm" onClick={() => setEditing(r)}>
            Edit
          </Button>
          <Button variant="ghost-danger" size="sm" onClick={() => void remove(r)}>
            Delete
          </Button>
        </ButtonGroup>
      );
      default: return null;
    }
  };

  return (
    <div>
      <div className="mb-3 flex items-baseline gap-3">
        <span className="flex-1 text-[length:var(--fs-sm)] text-(--text2)">
          Ingest-time rules evaluated <b>before</b> the store write. A "drop"
          rule that matches removes the record entirely — no CH write. Use
          for noisy health-check spans, DEBUG logs, high-cardinality debug
          metrics, or whole services you've decided to drop for cost.
          Choose the <b>signal</b> (spans / logs / metrics) each rule scopes to.
        </span>
        <Button onClick={() => setCreating(true)}>+ New rule</Button>
      </div>

      {msg && <div className="mb-2.5"><FlashBox kind={msg.kind}>{msg.text}</FlashBox></div>}

      <div className="table-wrap">
        <DataTable<PipelineRule> storageKey="settings-pipeline-rules" columns={RULE_COLS} rows={rules}
          sort={{ initial: { id: 'name', dir: 'asc' } }}
          getRowKey={r => r.id}
          state={ruleState}
          renderCell={ruleCell} />
      </div>

      {(creating || editing) && (
        <PipelineRuleModal
          existing={editing}
          prefill={spanPrefill ? SPAN_ROUTE_PREFILL : null}
          onClose={() => { setCreating(false); setEditing(null); setSpanPrefill(false); }}
          onSaved={() => { setCreating(false); setEditing(null); setSpanPrefill(false); load(); }}
        />
      )}

      {/* v0.9.797 — metrik route dışlamaları. AYNI SEKMEDE, çünkü
          operatör buraya tek bir soruyla geliyor: "bu gürültü ne olsun?"
          Üstteki tablo ham ingest kurallarını yönetiyor; bu kart onun
          metrik-grafik yüzeyindeki hâli — ve dropAtIngest işaretlendiğinde
          ÜSTTEKİ tabloda görünen türetilmiş bir kural yazıyor (tek drop
          motoru). Ayrı bir sekmeye koysaydık iki liste birbirinden
          habersiz görünürdü. */}
      <div className="mt-7 border-t border-(--border) pt-5">
        <MetricExclusionsSection
          onChanged={load}
          onSpanRule={() => { setSpanPrefill(true); setCreating(true); }}
        />
      </div>
    </div>
  );
}

// ── Metrik dışlamaları (v0.9.797) ───────────────────────────────────
//
// İSTEK: healthcheck / probe route'ları metrik grafiklerinden düşsün.
// Yüksek hacimli ve tekdüze oldukları için route kırılımlı panelin ilk
// 10 serisini yiyorlar ve GRUPSUZ toplamda ortalamayı aşağı çekiyorlar —
// "Toplam" çizgisi sağlıklı görünürken gerçek endpoint'ler yavaşlıyor
// olabiliyor.
//
// İKİ KADEME ve farkları operatöre AÇIK YAZILIYOR: okuma filtresi geri
// alınabilir (kural silinince geçmiş veri aynen döner), ingest drop'u
// geri alınamaz (yazılmayan datapoint yok).
function MetricExclusionsSection({ onChanged, onSpanRule }: {
  onChanged: () => void;
  onSpanRule: () => void;
}) {
  const [rules, setRules] = useState<MetricExclusionRule[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [flash, setFlash] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  useEffect(() => {
    api.getMetricExclusions()
      .then(c => setRules(c.rules ?? []))
      .catch(err => setFlash({ kind: 'err', text: humanize(err) }));
  }, []);

  const save = async () => {
    if (!rules) return;
    setBusy(true); setFlash(null);
    try {
      const saved = await api.putMetricExclusions({ rules });
      setRules(saved.rules ?? []);
      setFlash({
        kind: 'ok',
        text: 'Kaydedildi — grafikler bir sonraki okumada (≤30 sn) filtreli gelir.',
      });
      // İngest ikizleri değişmiş olabilir: üstteki Pipeline listesini
      // tazele ki operatör türetilen kuralı GÖRSÜN.
      onChanged();
    } catch (err) {
      setFlash({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const patch = (i: number, p: Partial<MetricExclusionRule>) =>
    setRules(rs => (rs ?? []).map((r, j) => (j === i ? { ...r, ...p } : r)));

  if (rules === null) {
    return flash ? <FlashBox kind={flash.kind}>{flash.text}</FlashBox> : <Spinner />;
  }

  return (
    <div>
      <div className="mb-2 flex items-baseline gap-3">
        <h2 className="flex-1 text-[length:var(--fs-lg)] font-semibold">Metrik dışlamaları</h2>
        <Button variant="outline" size="sm"
          onClick={() => setRules([...(rules ?? []), { metric: '*', pattern: '', dropAtIngest: false }])}>
          + Kural
        </Button>
      </div>
      <p className="mb-3.5 text-[length:var(--fs-sm)] leading-normal text-(--text2)">
        Healthcheck route&apos;larını metrik grafiklerinden düşürmek için. Kural
        eklendiği anda <b>geçmiş dahil</b> tüm metrik okumaları filtrelenir;
        kural silinince veri aynen geri gelir &mdash; hiçbir şey kaybolmaz.
        Desen <b>RE2</b> ve ankorsuz: <code>/health</code> yolun herhangi bir
        yerinde eşleşir, tam eşleşme için <code>^...$</code> yazın. Metrik alanı
        tam ad ya da <code>*</code> (her metrik). Kapsam yalnız <b>metrik
        datapoint&apos;leri</b>: trace&apos;ler, servis RED&apos;i ve hata oranı
        span türevli, onlara dokunulmaz.
      </p>

      {/* v0.9.802 — SPAN KÖPRÜSÜ. Kart yalnız metrik datapoint'lerini
          kapsıyor ve operatör buraya "healthcheck gürültüsü gitsin"
          diye geliyor: span tarafının nerede olduğunu SÖYLEMEZSEK kartı
          yarım bir çözüm sanıp span kuralını hiç kurmuyor.

          Karta span kural TİPİ EKLENMEDİ (yalnız yönlendirme): iki
          motorun sahibi pipeline, ve aynı kuralı iki yerden yazılabilir
          yapmak UI çoğaltması olurdu. Link yukarıdaki tabloya taslak
          açar — tek sahip, tek liste. */}
      <div className="mb-3.5 flex flex-wrap items-baseline gap-2.5 rounded-md border border-(--border) bg-(--bg2) px-3 py-2 text-[length:var(--fs-sm)] leading-normal text-(--text2)">
        <span className="flex-[1_1_340px]">
          <b>Span&apos;ler için:</b> yukarıdaki Pipeline kuralları tablosunda
          {' '}<code>kind=drop</code> bir kural (<code>http.route</code> ya da span
          adı eşleşmesi). Span tarafında OKUMA filtresi <b>yok</b>: dar rollup ve
          {' '}<code>operation_summary_5m</code> route bilmiyor, okuma filtresi
          entry-RED&apos;i her istekte ham <code>spans</code> taramasına düşürürdü.
          Bu yüzden span dışlaması <b>yalnız ingest&apos;te</b> uygulanır &mdash;
          geçmiş veriyi etkilemez, ileriye dönük yazılmaz.
        </span>
        <Button variant="outline" size="sm" onClick={onSpanRule}>
          + Span drop kuralı
        </Button>
      </div>

      {rules.length === 0 ? (
        <Empty icon="⊘" title="Dışlama kuralı yok">
          Healthcheck route&apos;larını grafiklerden düşürmek için bir kural ekleyin.
        </Empty>
      ) : (
        <div className="grid gap-2.5">
          {rules.map((r, i) => (
            <div key={i} role="group" aria-label={`Dışlama kuralı ${i + 1}`}
              className="flex flex-wrap items-start gap-2.5 rounded-md border border-(--border) bg-(--bg2) px-3 py-2.5">
              <Field className="flex-[1_1_220px]" label="Metrik" value={r.metric}
                placeholder="* veya http.server.duration"
                onChange={e => patch(i, { metric: e.target.value })} />
              <Field className="flex-[2_1_280px]" label="http.route deseni (RE2)" value={r.pattern}
                placeholder="^/health"
                onChange={e => patch(i, { pattern: e.target.value })} />
              <CheckboxField className="flex-[1_1_260px] pt-[22px]" label="ingest'te düşür"
                checked={!!r.dropAtIngest}
                onCheckedChange={v => patch(i, { dropAtIngest: v })}
                hint={<>Pipeline kuralı olarak uygulanır &mdash; yukarıdaki listede de
                  görünür. Yazılmayan datapoint <b>geri gelmez</b>.</>} />
              <div className="pt-[18px]">
                <Button variant="outline" size="sm"
                  onClick={() => setRules(rules.filter((_, j) => j !== i))}>Sil</Button>
              </div>
            </div>
          ))}
        </div>
      )}

      <div className="mt-4 flex items-center gap-2">
        <Button onClick={() => { void save(); }} loading={busy}>
          Kaydet
        </Button>
        {flash && <FlashBox kind={flash.kind}>{flash.text}</FlashBox>}
      </div>
    </div>
  );
}

// prefill — YALNIZ yeni kuralda (existing === null) okunur; var olan bir
// kuralı düzenlerken taslağın kaydı ezmesi olurdu (v0.9.802).
function PipelineRuleModal({ existing, prefill, onClose, onSaved }: {
  existing: PipelineRule | null;
  prefill?: Partial<PipelineRule> | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const draft = existing ? null : prefill ?? null;
  const [name,    setName]    = useState(existing?.name ?? '');
  const [kind,    setKind]    = useState<PipelineRule['kind']>(existing?.kind ?? draft?.kind ?? 'drop');
  const [signal,  setSignal]  = useState<PipelineRule['signal']>(existing?.signal ?? draft?.signal ?? 'spans');
  const [enabled, setEnabled] = useState(existing?.enabled ?? true);
  const [whenKey, setWhenKey] = useState(existing?.when.key ?? draft?.when?.key ?? 'service.name');
  const [whenOp,  setWhenOp]  = useState<PipelineRule['when']['op']>(existing?.when.op ?? draft?.when?.op ?? '=');
  const [whenVal, setWhenVal] = useState(existing?.when.value ?? draft?.when?.value ?? '');
  // v0.5.270 — enrich + sample fields. Enrich uses a single
  // key/value pair for the MVP (multi-attr could come later
  // via a chip list — start narrow).
  const [enrichKey, setEnrichKey] = useState<string>(() => {
    const m = existing?.setAttributes ?? {};
    return Object.keys(m)[0] ?? '';
  });
  const [enrichVal, setEnrichVal] = useState<string>(() => {
    const m = existing?.setAttributes ?? {};
    const k = Object.keys(m)[0];
    return k ? m[k] : '';
  });
  const [rate, setRate] = useState<number>(existing?.rate ?? 0.1);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // v0.9.802 — bozuk RE2 deseni backend'de 400 ile reddediliyor
  // (validateCondition, v0.9.797). O hatayı formun DİBİNDEKİ genel
  // kutuya bırakmak, operatörü hatayla düzelteceği alan arasında
  // gezdirir; `=~` seçiliyken desen hatası alanın ALTINA düşer ve
  // genel kutuda TEKRAR gösterilmez.
  const patternError =
    error && whenOp === '=~' && /RE2|pattern|desen/i.test(error) ? error : null;

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setError(null);
    try {
      // v0.9.803 — gövde SAF fonksiyondan (pipelineRuleBody.ts, tablo-testli).
      // Satır içinde kurulduğunda formun bilmediği alanlar sessizce
      // düşüyordu; `and` kaybı tek-metrik dışlamasını bütün metriklere
      // yayan geri alınamaz bir veri kaybıydı.
      const body = buildPipelineRuleBody(existing, {
        name, kind, signal, enabled,
        whenKey, whenOp, whenValue: whenVal,
        enrichKey, enrichValue: enrichVal, rate,
      });
      await api.upsertPipelineRule(body);
      onSaved();
    } catch (err) {
      // v0.9.802 — humanize: "HTTP 400: {"error":"invalid RE2 pattern …"}"
      // yerine sade mesaj. Desen hatasının alan altında okunabilir
      // görünmesi buna bağlı.
      setError(humanize(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    // Faz 2 dilim 1 R14 — ui/Modal (size md) → coremetry/overlay Dialog
    // (D8): başlık, × kapat, odak tuzağı, Esc yığını; alan sırası aynı.
    <Dialog open onOpenChange={o => { if (!o) onClose(); }}>
      <DialogContent aria-describedby={undefined} className="max-h-[calc(100vh-32px)] gap-0 overflow-auto p-0 sm:max-w-[560px]">
        <DialogHeader className="border-b border-(--divider) px-[18px] py-3.5">
          <DialogTitle className="text-[length:var(--fs-lg)]">{existing ? `Edit rule — ${existing.name}` : 'New pipeline rule'}</DialogTitle>
        </DialogHeader>
        <form id="pipeline-form" onSubmit={submit} className="flex flex-col gap-3 px-[18px] py-4">
          <Field label="Rule name" value={name} onChange={e => setName(e.target.value)} required
            placeholder="e.g. drop frontend health-checks" />
          <div className="grid grid-cols-2 gap-2.5">
            <SelectField label="Action" value={kind} onChange={e => setKind(e.target.value as PipelineRule['kind'])}>
              <option value="drop">Drop — discard the matching signal</option>
              <option value="enrich">Enrich — set a resource attribute</option>
              <option value="sample">Sample — keep at probability</option>
            </SelectField>
            <SelectField label="Signal" value={signal} onChange={e => setSignal(e.target.value as PipelineRule['signal'])}>
              <option value="spans">spans</option>
              <option value="logs">logs</option>
              <option value="metrics">metrics</option>
            </SelectField>
          </div>
          <div role="group" aria-labelledby="pipeline-when-label" className="flex flex-col gap-1">
            <span id="pipeline-when-label" className={LABEL}>When (predicate)</span>
            <div className="grid grid-cols-[2fr_1fr_2fr] gap-2">
              <Input value={whenKey} onChange={e => setWhenKey(e.target.value)} required
                aria-label="Predicate key"
                placeholder={
                  signal === 'logs' ? 'service.name | severity_text | body | attr.X'
                  : signal === 'metrics' ? 'metric | unit | instrument | service.name'
                  : 'service.name | name | kind | attr.X | resource.X'}
                className={cn(CTL, 'font-mono')} />
              <NativeSelect size="sm" value={whenOp} aria-label="Predicate operator"
                onChange={e => setWhenOp(e.target.value as PipelineRule['when']['op'])}
                className={cn(CTL, 'font-mono')}>
                <option value="=">=</option>
                <option value="!=">!=</option>
                <option value="contains">contains</option>
                <option value="startsWith">startsWith</option>
                <option value="endsWith">endsWith</option>
                {/* v0.9.802 — motor `=~` destekliyordu (v0.9.797, RE2 +
                    validateCondition) ama dropdown sunmuyordu; operatör
                    contains ile idare ediyordu. */}
                <option value="=~">=~ (regex)</option>
              </NativeSelect>
              <Input value={whenVal} onChange={e => setWhenVal(e.target.value)} required
                aria-label="Predicate value"
                aria-invalid={patternError ? true : undefined}
                placeholder={whenOp === '=~' ? 'RE2 pattern, e.g. ^/health' : 'value'}
                className={cn(CTL, 'font-mono')} />
            </div>
            {/* v0.9.802 — bozuk desen backend'de 400 döner (validateCondition).
                O hata genel kutuda değil DESENİN ALTINDA görünsün: operatör
                düzelteceği alana bakıyor. */}
            {patternError && (
              <div role="alert" className="text-[length:var(--fs-xs)] text-status-critical">{patternError}</div>
            )}
            <div className={HINT}>
              {signal === 'logs' ? (
                <>Well-known log fields branch directly: <code>service.name</code>,
                {' '}<code>severity_text</code>, <code>severity_number</code>,
                {' '}<code>body</code>, <code>host.name</code>, <code>trace_id</code>.</>
              ) : signal === 'metrics' ? (
                <>Well-known metric fields branch directly: <code>metric</code>,
                {' '}<code>instrument</code>, <code>unit</code>, <code>service.name</code>,
                {' '}<code>host.name</code>.</>
              ) : (
                <>Well-known span fields branch directly: <code>service.name</code>,
                {' '}<code>name</code>, <code>kind</code>, <code>status_code</code>,
                {' '}<code>http.route</code>.</>
              )}
              {' '}Custom attributes via <code>attr.foo</code> / <code>resource.foo</code> prefix.
              {' '}<code>=~</code> is an <b>unanchored RE2</b> pattern — <code>/health</code>{' '}
              matches anywhere in the value; use <code>^…$</code> for a full match.
              {/* v0.9.802 — türetilmiş route + trace bütünlüğü. İkisi de
                  spans'e özgü, o yüzden yalnız bu dalda. */}
              {signal === 'spans' && (
                <>
                  <div className="mt-1.5">
                    <code>http.route</code> matches the <b>derived</b> route, not the raw
                    attribute: services that only emit <code>url.path</code> (no route
                    templating) still match, via the id-stripped template
                    (<code>/api/accounts/12345</code> → <code>/api/accounts/:id</code>).
                    Use <code>attr.http.route</code> if you need the raw attribute.
                  </div>
                  <div className="mt-1.5">
                    Trace integrity: a drop rule removes <b>the matching span only</b> —
                    the rest of its trace is still written, so a child can be left without
                    its parent. Match on the entry span of a self-contained trace
                    (health checks / probes) rather than mid-trace spans.
                  </div>
                </>
              )}
            </div>
          </div>

          {/* v0.9.803 — EK KOŞULLAR, salt-okunur.
              Form bunları düzenlemiyor ama kayıtta AYNEN geri yazılıyor
              (buildPipelineRuleBody). Görünür olmaları şart: bir operatör
              "when: http.route =~ ^/health" satırını görüp kuralı "her
              route" sanmamalı — kısıtın ikinci yarısı burada. */}
          {existing?.and && existing.and.length > 0 && (
            <div className="flex flex-col gap-1">
              <span className={LABEL}>Ek koşullar (AND) — salt okunur</span>
              <div className="flex flex-wrap gap-1.5">
                {existing.and.map((c, i) => (
                  <code key={i} className="rounded border border-(--border) bg-(--bg2) px-2 py-[3px] text-[length:var(--fs-xs)]">{describeCondition(c)}</code>
                ))}
              </div>
              <div className={HINT}>
                Kural yalnız <b>hepsi</b> sağlandığında eşleşir. Bu koşullar bu
                formda düzenlenmez ve kaydederken <b>olduğu gibi korunur</b> —
                genelde türetilmiş bir kuralın (<code>metric-excl-…</code>) metrik
                kısıtıdır. Kaldırmak için kuralı üreten kaydı düzenleyin.
              </div>
            </div>
          )}

          {/* v0.5.270 — enrich-only fields */}
          {kind === 'enrich' && (
            <div role="group" aria-labelledby="pipeline-enrich-label" className="flex flex-col gap-1">
              <span id="pipeline-enrich-label" className={LABEL}>Set resource attribute</span>
              <div className="grid grid-cols-[2fr_3fr] gap-2">
                <Input value={enrichKey} onChange={e => setEnrichKey(e.target.value)}
                  aria-label="Attribute key"
                  placeholder="e.g. team, region, cluster"
                  className={cn(CTL, 'font-mono')} />
                <Input value={enrichVal} onChange={e => setEnrichVal(e.target.value)}
                  aria-label="Attribute value"
                  placeholder="value"
                  className={cn(CTL, 'font-mono')} />
              </div>
              <div className={HINT}>
                Sets a resource attribute on every matching record. Existing keys are
                overridden. Multi-attribute support coming later — start with one.
              </div>
            </div>
          )}

          {/* v0.5.270 — sample-only fields. Aralık girdisi native (plan Q3). */}
          {kind === 'sample' && (
            <div className="flex flex-col gap-1">
              <label htmlFor="pipeline-rate" className={LABEL}>
                Keep rate ({(rate * 100).toFixed(1)}%)
              </label>
              <input id="pipeline-rate" type="range" min={0} max={1} step={0.01}
                value={rate} onChange={e => setRate(Number(e.target.value))}
                className="w-full accent-(--accent)" />
              <div className={HINT}>
                Probability of keeping each matching record. 1.0 = no-op; 0.0 = use a
                drop rule instead.
                {signal === 'metrics' && (
                  <span className="text-(--warn)">
                    {' '}Note: sampling metrics makes sums / counts / histograms an
                    estimate — prefer drop for whole noisy series.
                  </span>
                )}
              </div>
            </div>
          )}

          <CheckboxField label="Enabled" checked={enabled} onCheckedChange={setEnabled} />
          {error && !patternError && (
            <div role="alert" className="rounded bg-status-critical-bg px-2 py-1 text-[length:var(--fs-sm)] text-status-critical">{error}</div>
          )}
        </form>
        <DialogFooter className="border-t border-(--divider) bg-(--bg2) px-[18px] py-3">
          <ActionRow
            secondary={<Button variant="outline" onClick={onClose}>Cancel</Button>}
            confirm={<Button type="submit" form="pipeline-form" loading={busy}>Save</Button>} />
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
