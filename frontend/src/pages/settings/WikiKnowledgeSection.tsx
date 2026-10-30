import { useCallback, useEffect, useState } from 'react';
import { Spinner } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Field, TextareaField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { SegmentedControl } from '@/components/coremetry/SegmentedControl';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { api } from '@/lib/api';
import { tsLong } from '@/lib/utils';
import { cn } from '@/lib/cn';
import type { WikiConfigView, WikiMode, WikiSyncStatus } from '@/lib/types';
import { FlashBox } from './shared';
import {
  contextCharsError, effectiveMode, listText, searchLabel, skippedReasonText, skippedSummary, syncPending,
  WIKI_CONTEXT_MAX, WIKI_CONTEXT_MIN, WIKI_MODES, wikiBody, wikiReadingFields, wikiStatusSummary,
} from './wikiKnowledge';
import { WikiPagesTable } from './WikiPagesTable';
import { WikiTestSearch } from './WikiTestSearch';

// WikiKnowledgeSection — v0.10.1122 ("karma"): Bilgi (RAG) sekmesinin
// "Azure DevOps Wiki" bölümü. CoSRE sohbeti kurumun on-prem Azure DevOps
// wiki'lerinden kaynak atıflı cevap verir. Kimlik bilgisi BURADA YOK: bağlantı
// (sunucu URL'i + PAT + TLS) Ayarlar → Kod entegrasyonu'ndaki DevOps ayarından
// gelir; PAT'in Wiki (Read) kapsamı olmalı. Senkron lider pod'da koşar;
// "Şimdi senkronize et" isteği ≤15 sn içinde alınır. Durum kartı yalnız
// senkron sürerken / istek beklerken 10 sn'de bir yenilenir (sekme gizliyken durur).
// v0.10.1124: mod seçimi (Karma / Yalnız canlı arama / Yalnız senkron),
// "Aramayı test et" tanısı (yönetici) ve indeksteki sayfalar listesi.

const POLL_MS = 10_000;

// Faz 2 dilim 1 R12 — inline stil → Tailwind utility; Field2/Row → coremetry
// Field ailesi + flex (Field2 düzeni: geniş flex 1 / min 200, small 0 1 180 /
// min 140). Ton hâli sınıfla (yalnız sapma renklenir — settingsPalette.pin).
const WIDE = 'min-w-[200px] flex-1';
const SMALL = 'min-w-[140px] flex-[0_1_180px]';
const ROW = 'flex flex-wrap items-start gap-3';
const TONE_CLASS = { err: 'text-(--err)', warn: 'text-(--warn)' } as const;

export function WikiKnowledgeSection({ canEdit = true }: { canEdit?: boolean }) {
  const [view, setView] = useState<WikiConfigView | null | undefined>(undefined);
  const [enabled, setEnabled] = useState(false);
  const [projects, setProjects] = useState('');
  const [wikis, setWikis] = useState('');
  const [interval, setIntervalText] = useState('');
  const [maxPages, setMaxPages] = useState('');
  const [mode, setMode] = useState<WikiMode>('hybrid');
  // v0.10.1136 — sohbetin wiki okuma ayarları: bağlam boyutu (boş = otomatik)
  // ve iki aşamalı okuma (sayfa seçimi; varsayılan açık).
  const [contextChars, setContextChars] = useState('');
  const [pageSelect, setPageSelect] = useState(true);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const confirm = useConfirm();

  const apply = useCallback((v: WikiConfigView) => {
    setView(v);
    const c = v.config;
    setEnabled(!!c?.enabled);
    setProjects(listText(c?.projects));
    setWikis(listText(c?.wikis));
    setIntervalText(c?.intervalMin ? String(c.intervalMin) : '');
    setMaxPages(c?.maxPages ? String(c.maxPages) : '');
    setMode(effectiveMode(c));
    setContextChars(c?.contextChars ? String(c.contextChars) : '');
    setPageSelect(!c?.disablePageSelect);
  }, []);

  useEffect(() => {
    api.getWikiConfig().then(apply).catch(() => setView(null));
  }, [apply]);

  const status = view?.status;
  const active = !!status && (status.running || syncPending(status));
  useEffect(() => {
    if (!active) return;
    const id = window.setInterval(() => {
      if (document.hidden) return;
      api.getWikiStatus()
        .then((st: WikiSyncStatus) => setView(v => (v ? { ...v, status: st } : v)))
        .catch(() => { /* bir sonraki tikte yeniden denenir */ });
    }, POLL_MS);
    return () => window.clearInterval(id);
  }, [active]);

  if (view === undefined) return <Spinner />;
  if (view === null) return <FlashBox kind="err">Wiki ayarları yüklenemedi.</FlashBox>;
  if (!view.available) return null;

  const ctxMin = view.defaults?.contextCharsMin ?? WIKI_CONTEXT_MIN;
  const ctxMax = view.defaults?.contextCharsMax ?? WIKI_CONTEXT_MAX;
  const ctxErr = contextCharsError(contextChars, ctxMin, ctxMax);

  const save = async () => {
    if (ctxErr) { setMsg({ kind: 'err', text: `Wiki bağlam boyutu: ${ctxErr}` }); return; }
    setBusy(true); setMsg(null);
    try {
      const next = await api.putWikiConfig({
        ...wikiBody(enabled, projects, wikis, interval, maxPages, mode),
        ...wikiReadingFields(contextChars, pageSelect),
      });
      apply(next);
      // v0.10.1124 — canlı mod Search uzantısı ister: sunucu uyarısı kayıtta gösterilir.
      setMsg(next.modeWarning
        ? { kind: 'err', text: `Kaydedildi — uyarı: ${next.modeWarning}` }
        : { kind: 'ok', text: 'Kaydedildi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const syncNow = async () => {
    setBusy(true); setMsg(null);
    try {
      const r = await api.syncWiki();
      setView(v => (v ? { ...v, status: r.status } : v));
      setMsg({ kind: 'ok', text: 'Senkron istendi — lider pod 15 sn içinde başlatır.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const savedMode = view.mode ?? effectiveMode(view.config);
  // v0.10.1124 (inceleme F4) — "İndeksi temizle": canlı moda geçen operatör
  // eski indeksi siler (yönetici, onaylı, audit'li; senkron sürerken 409).
  const purge = async () => {
    const ok = await confirm({
      title: 'Wiki indeksi temizlensin mi?',
      body: <>İndeksteki <b>{status?.indexedPages ?? 0} sayfa</b> ve tüm parçaları Coremetry'den silinir. Karma / senkron modunda bir sonraki senkron indeksi baştan kurar.</>,
      confirmLabel: 'Temizle', danger: true,
    });
    if (!ok) return;
    setBusy(true); setMsg(null);
    try {
      const r = await api.purgeWikiIndex();
      setView(v => (v ? { ...v, status: r.status } : v));
      setMsg({ kind: 'ok', text: 'İndeks temizlendi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally { setBusy(false); }
  };

  const summary = wikiStatusSummary(status, savedMode);
  const skipped = savedMode !== 'live' ? skippedSummary(status) : '';
  const skippedList = status?.skipped ?? [];
  const skippedTotal = (status?.skippedEmpty ?? 0) + (status?.skippedLarge ?? 0);
  const modeHelp = WIKI_MODES.find(m => m.value === mode)?.help ?? '';
  // Ayar hâli NÖTR (settingsPalette.pin, v0.10.929): yalnız sapma renklenir.
  const toneClass = summary.tone === 'err' || summary.tone === 'warn' ? TONE_CLASS[summary.tone] : 'text-(--text2)';
  const defInterval = view.defaults?.intervalMin ?? 60;
  const minInterval = view.defaults?.minIntervalMin ?? 15;
  const defPages = view.defaults?.maxPages ?? 5000;

  return (
    <section aria-label="Azure DevOps Wiki" className="mt-4">
      <h3 className="mb-1.5 flex flex-wrap items-center gap-2 text-[length:var(--fs-md)] font-semibold">
        Azure DevOps Wiki
        {!view.config?.enabled
          ? <StatusBadge status="unknown">kapalı</StatusBadge>
          : view.embedding
            ? <StatusBadge status="unknown">aktif · hibrit</StatusBadge>
            : <StatusBadge status="unknown">aktif · keyword</StatusBadge>}
      </h3>
      <p className="mb-2 text-[length:var(--fs-sm)] text-(--text2)">
        Kurumun on-prem Azure DevOps wiki'leri Coremetry içine indekslenir; CoSRE runbook,
        nasıl yapılır, mimari ve sahiplik sorularını bu sayfalardan <b>kaynak bağlantılı</b> cevaplar.
        Bağlantı ve PAT <b>Kod entegrasyonu</b> ayarından gelir (PAT kapsamı: <code>Wiki (Read)</code>;
        canlı arama için Search uzantısı opsiyonel). Wiki içeriği Coremetry dışına çıkmaz.
      </p>
      {!view.devopsConfigured && (
        <FlashBox kind="err">Azure DevOps bağlantısı yapılandırılmamış — önce Ayarlar → Kod entegrasyonu.</FlashBox>
      )}

      <CheckboxField className="mt-2" label="Wiki bilgisi aktif" checked={enabled} disabled={!canEdit}
        onCheckedChange={setEnabled} />
      <div className="mt-2">
        <div className="mb-1 text-[length:var(--fs-sm)] font-semibold">Mod</div>
        <SegmentedControl<WikiMode> aria-label="Wiki modu" value={mode} activation="manual"
          onChange={v => { if (canEdit) setMode(v); }}
          options={WIKI_MODES.map(m => ({ value: m.value, label: m.label, disabled: !canEdit }))} />
        <p data-testid="wiki-mode-help" className="mt-1 text-[length:var(--fs-sm)] text-(--text2)">{modeHelp}</p>
      </div>
      <div className={cn(ROW, 'mt-3')}>
        <TextareaField className={WIDE} controlClassName="w-[260px] max-w-full" label="Projeler (izin listesi)"
          hint="satır başına bir proje; boş = PAT'in gördüğü tümü"
          value={projects} rows={3} disabled={!canEdit} spellCheck={false}
          onChange={e => setProjects(e.target.value)} />
        <TextareaField className={WIDE} controlClassName="w-[260px] max-w-full" label="Wiki'ler (izin listesi)"
          hint="WikiAdı ya da Proje/WikiAdı; boş = tümü"
          value={wikis} rows={3} disabled={!canEdit} spellCheck={false}
          onChange={e => setWikis(e.target.value)} />
      </div>
      <div className={cn(ROW, 'mt-3')}>
        <Field className={SMALL} label="Senkron aralığı (dk)" hint={`boş = ${defInterval}; en az ${minInterval}`}
          type="number" min={minInterval} value={interval} disabled={!canEdit}
          placeholder={String(defInterval)} onChange={e => setIntervalText(e.target.value)} />
        <Field className={SMALL} label="Sayfa tavanı" hint={`boş = ${defPages}`}
          type="number" min={1} value={maxPages} disabled={!canEdit}
          placeholder={String(defPages)} onChange={e => setMaxPages(e.target.value)} />
        <Field className={SMALL} label="Wiki bağlam boyutu (karakter)"
          hint={`boş = otomatik (modelden; şu an ${view.defaults?.contextCharsAuto ?? '—'}); ${ctxMin}–${ctxMax}; model penceresi biliniyorsa onunla kapaklanır`}
          type="number" min={ctxMin} max={ctxMax} step={1000} value={contextChars} disabled={!canEdit}
          data-testid="wiki-context-chars" aria-invalid={!!ctxErr}
          placeholder="otomatik" onChange={e => setContextChars(e.target.value)} />
      </div>
      {!!ctxErr && <p data-testid="wiki-context-chars-error" className="mt-1 text-[length:var(--fs-sm)] text-(--err)">{ctxErr}</p>}
      <div data-testid="wiki-page-select" className="mt-2"
        title="Model önce aday sayfaların başlık ve kısa kesitlerinden okunacak 1–5 sayfayı seçer, sonra yalnız onları tam okur. Bir ek AI çağrısı (~1–3 sn); başarısızsa skor sırası kullanılır.">
        <CheckboxField checked={pageSelect} disabled={!canEdit} onCheckedChange={setPageSelect}
          label={<>İki aşamalı okuma (sayfa seçimi) <span className="text-[length:var(--fs-sm)] text-(--text3)">— bir ek AI çağrısı, ~1–3 sn</span></>} />
      </div>
      {canEdit && (
        <ActionRow
          destructive={
            <Button variant="destructive" size="sm" disabled={busy || !status?.indexedPages}
              title="İndeksteki tüm wiki sayfalarını ve parçalarını sil"
              onClick={() => { void purge(); }}>
              İndeksi temizle
            </Button>}
          secondary={
            <Button variant="outline" size="sm"
              disabled={busy || !view.config?.enabled || !view.devopsConfigured || syncPending(status) || savedMode === 'live'}
              title="Lider pod'a senkron isteği gönder (değişmeyen sayfalar yeniden okunmaz)"
              onClick={() => { void syncNow(); }}>
              ⟳ Şimdi senkronize et
            </Button>}
          confirm={
            <Button size="sm" loading={busy} onClick={() => { void save(); }}>
              Kaydet
            </Button>} />
      )}
      {savedMode === 'live' && !!status?.indexedPages && (
        <p data-testid="wiki-live-leftover" className="mt-1.5 text-[length:var(--fs-sm)] text-(--text2)">
          Canlı modda yeni içerik yazılmaz, ama daha önce indekslenen {status.indexedPages} sayfa siz
          temizleyene dek Coremetry'de kalır ("İndeksi temizle").
        </p>
      )}

      <div data-testid="wiki-status" className="mt-2.5 rounded-md border border-(--border) px-2.5 py-2 text-[length:var(--fs-sm)]">
        <div className={cn('font-semibold', toneClass)}>{summary.text}</div>
        {savedMode !== 'live' && !!status?.lastFinishedAt && (
          <div className="mt-0.5 text-(--text3)">
            Son senkron: {tsLong(status.lastFinishedAt * 1e6)}
            {status.durationMs ? ` · ${(status.durationMs / 1000).toFixed(1)} sn` : ''}
            {` · ${status.projects} proje · ${status.wikis} wiki`}
          </div>
        )}
        {syncPending(status) && (
          <div className="mt-0.5 text-(--text3)">
            Senkron istendi{status?.requestedBy ? ` (${status.requestedBy})` : ''} — lider pod bekleniyor…
          </div>
        )}
        <div className="mt-0.5 text-(--text3)">{searchLabel(status?.search, status?.searchLast, savedMode)}</div>
        {!!view.modeWarning && (
          <div data-testid="wiki-mode-warning" className="mt-0.5 text-(--warn)">{view.modeWarning}</div>
        )}
        {/* v0.10.1129 — atlanan sayfalar hata DEĞİL: nötr gri satır + açılır liste (≤5). */}
        {!!skipped && (
          <details data-testid="wiki-skipped" className="mt-1 text-(--text3)">
            <summary className="cursor-pointer">{skipped}</summary>
            <ul className="mt-1 mb-0 pl-[18px]">
              {skippedList.slice(0, 5).map((s, i) => (
                <li key={i} className="[word-break:break-word]">{s.page} — {skippedReasonText(s.reason)}</li>
              ))}
              {skippedTotal > Math.min(5, skippedList.length) && <li>… {skippedTotal - Math.min(5, skippedList.length)} sayfa daha</li>}
            </ul>
          </details>
        )}
        {!!status?.errors?.length && (
          <ul className="mt-1.5 mb-0 pl-[18px] text-(--err)">
            {status.errors.slice(0, 5).map((e, i) => <li key={i} className="[word-break:break-word]">{e}</li>)}
            {status.errors.length > 5 && <li>… {status.errors.length - 5} hata daha</li>}
          </ul>
        )}
      </div>
      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      {canEdit && !!view.config?.enabled && <WikiTestSearch onStatus={st => setView(v => (v ? { ...v, status: st } : v))} />}
      {!!view.config?.enabled && <WikiPagesTable status={status} mode={savedMode} />}
    </section>
  );
}
