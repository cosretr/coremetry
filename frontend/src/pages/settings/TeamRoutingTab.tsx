import { useEffect, useId, useMemo, useState, type FormEvent } from 'react';
import { Spinner, Empty } from '@/components/Spinner';
import { Button } from '@/components/shadcn/button';
import { Input } from '@/components/shadcn/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/shadcn/table';
import { Field, SelectField, CheckboxField } from '@/components/coremetry/Field';
import { StatusBadge } from '@/components/coremetry/StatusBadge';
import { ActionRow } from '@/components/coremetry/ActionRow';
import { useConfirm } from '@/components/coremetry/Confirm';
import { DataTableStateRow, type DataTableStateStaticProps } from '@/components/coremetry/DataTable';
import { api } from '@/lib/api';
import { useServicesMetadata } from '@/lib/queries';
import { teamOptionsCI } from '@/lib/teamOptions';
import type { TeamContacts } from '@/lib/types';
import { NOTIFY_KIND_OPTIONS, normalizeKinds, toggleKind } from '@/lib/notifyKinds'; // v0.10.814
import { FlashBox, humanize } from './shared';

// Faz 2 dilim 1 R9 (shadcn): inline stil yok (Tailwind utility); coremetry/
// Field ailesi, shadcn Button/Input/Table, StatusBadge, ActionRow. Alan
// sırası, metinler, kolonlar ve kaydetme gövdesi aynı.
const LABEL = 'mb-1 text-[length:var(--fs-sm)] leading-tight text-(--text2)';
// Etiketsiz satır-içi girdi (tablo hücresi, alias formu): Field'ın denetim zemini.
const BARE_INPUT = 'h-8 bg-field border-(--border-control) text-[length:var(--fs-sm)] md:text-[length:var(--fs-sm)] dark:bg-field';

// ── Team routing tab (v0.8.429) ─────────────────────────────────────────────
// Operator ask: "yeni bir problem ilk defa geldiğinde ilgili sy ve ug
// team'e bildirim gönderilsin — mailleri katalogdan alsın." The catalog
// names the teams; this tab maps those names to e-mail addresses and
// arms the automatic problem-open mail (one per problem, notification_log
// dedup, template identical to a hand-configured email channel).
//
// Rows are pre-seeded from the catalog's owner/SRE team sets
// (teamOptionsCI — the same case-insensitive dedup the pickers use) so
// the operator fills blanks instead of retyping team names; teams
// without an address show a warning chip and are skipped silently at
// send time.

export function TeamRoutingTab() {
  const [tc, setTc] = useState<TeamContacts | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [extraTeam, setExtraTeam] = useState('');
  const kindsLabelId = useId();

  const load = () => {
    setTc(undefined);
    api.getTeamContacts().then(setTc).catch(() => setTc(null));
  };
  useEffect(load, []);

  // Catalog-known team names (owner + SRE, case-insensitive dedup).
  const catalogQ = useServicesMetadata();
  const catalogTeams = useMemo(() => {
    const metas = Object.values(catalogQ.data ?? {});
    return teamOptionsCI([
      ...metas.map(m => m.ownerTeam),
      ...metas.map(m => m.sreTeam),
    ]);
  }, [catalogQ.data]);

  // Render rows = union of catalog teams and already-saved keys (a
  // saved contact for a team that later left the catalog must stay
  // visible/editable, not silently vanish).
  const rows = useMemo(() => {
    if (!tc) return [];
    const seen = new Set<string>();
    const out: string[] = [];
    for (const t of [...catalogTeams, ...Object.keys(tc.contacts)]) {
      const key = t.trim().toLowerCase();
      if (!key || seen.has(key)) continue;
      seen.add(key);
      out.push(t);
    }
    return out.sort((a, b) => a.localeCompare(b));
  }, [tc, catalogTeams]);

  if (tc === undefined) return <Spinner />;
  if (tc === null) return <Empty icon="⚠" title="Failed to load team routing settings" />;

  // v0.10.967 — sekmenin kapısı yalnız KAYITLI adres okumasını (tc) bekler;
  // satırların çoğunu veren katalog okuması (useServicesMetadata) ayrı. Katalog
  // yüklenirken ya da düştüğünde tablo "Katalogda takım yok" DEMEZ: durum
  // satırı yükleniyor / hata (ExternalLinksTab emsali). Eskiden de yeniden
  // deneme yoktu → yok (recipe §6).
  const routingState: Omit<DataTableStateStaticProps, 'colSpan'> =
    catalogQ.isPending ? { kind: 'loading' }
    : catalogQ.isError ? { kind: 'error', message: `Servis kataloğu okunamadı: ${humanize(catalogQ.error)}` }
    : { kind: 'empty', message: "Katalogda takım yok — Service catalog'a owner/SRE team girildiğinde takımlar burada listelenir." };

  const contactFor = (team: string): string => {
    for (const [k, v] of Object.entries(tc.contacts)) {
      if (k.trim().toLowerCase() === team.trim().toLowerCase()) return v;
    }
    return '';
  };
  const setContact = (team: string, email: string) => {
    const next = { ...tc.contacts };
    // Rewrite under a single canonical key (the displayed one) so a
    // mixed-case duplicate can't linger with a stale value.
    for (const k of Object.keys(next)) {
      if (k.trim().toLowerCase() === team.trim().toLowerCase()) delete next[k];
    }
    if (email.trim() !== '') next[team] = email;
    setTc({ ...tc, contacts: next });
  };

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true); setMsg(null);
    try {
      const next = await api.putTeamContacts(tc);
      setTc(next);
      setMsg({ kind: 'ok', text: 'Saved.' });
    } catch (err) {
      setMsg({ kind: 'err', text: humanize(err) });
    } finally {
      setBusy(false);
    }
  };

  const missing = rows.filter(t => contactFor(t).trim() === '').length;

  return (
    <>
    <form onSubmit={save} className="max-w-[720px]">
      <p className="mb-4 text-[length:var(--fs-md)] text-(--text2)">
        Yeni bir problem <b>ilk kez</b> açıldığında, servisin katalogdaki owner
        (ug) ve SRE (sy) takımlarına otomatik e-posta gönderilir — problem
        başına bir kez, SMTP ayarları üzerinden. Adresler virgülle
        çoğaltılabilir; adresi boş takımlar sessizce atlanır.
      </p>

      {/* v0.9.828 — DÜRÜSTLÜK NOTU.
          Bu kart "Team routing" adını taşıyor ve operatörün takım-bazlı
          YÖNLENDİRMEYİ buradan kuracağını sanması çok doğal. Oysa burası
          yalnız E-POSTA gönderiyor; Slack/Teams/Zoom/webhook'u takıma
          göre yönlendirme yeteneği BAŞKA bir yerde ve ZATEN ÇALIŞIYOR
          (kanal kurallarındaki "Match SRE/owner teams" alanları). Eksik
          olan yetenek değil, o yeteneğin görünürlüğüydü. */}
      <p className="mb-4 rounded-sm border border-(--border) px-2.5 py-2 text-[length:var(--fs-sm)] leading-[1.55] text-(--text2)">
        <b>Kapsam:</b> bu kart yalnız <b>e-posta</b> gönderir. Slack, Teams, Zoom
        Chat veya webhook&apos;u takıma göre yönlendirmek için
        <b> Bildirim kanalları</b>&apos;nda kanalı düzenleyip <b>Match SRE teams</b> /
        <b> Match owner teams</b> alanlarını doldurun &mdash; o yönlendirme
        katalogdaki takım adlarına göre çalışır ve buradaki adres listesinden
        bağımsızdır.
      </p>

      <div className="mb-3.5 flex flex-wrap items-end gap-4">
        <CheckboxField className="pb-2" checked={tc.enabled}
          onCheckedChange={v => setTc({ ...tc, enabled: v })} label="Team routing aktif" />
        <SelectField small label="Minimum severity:" value={tc.minSeverity ?? 'warning'}
          onChange={e => setTc({ ...tc, minSeverity: e.target.value as TeamContacts['minSeverity'] })}>
          <option value="info">info</option>
          <option value="warning">warning</option>
          <option value="critical">critical</option>
        </SelectField>
        {missing > 0 && (
          <span className="pb-2">
            <StatusBadge status="warn" title="Bu takımlar problem açılışında maillenmez — adres girin.">
              {missing} takımın e-postası eksik
            </StatusBadge>
          </span>
        )}
      </div>

      {/* v0.10.814 (operatör: "anomali mailleri çok false pozitif, kapatabilmeliyim") —
          ekip maili kanal süzgecinden BAĞIMSIZ gidiyordu; kanalda Anomali tiki
          kaldırılsa da owner/SRE mail alıyordu. Aynı gramer, boş = hepsi.
          Exception grupları bu yola hiç girmez (kanal işi), o yüzden seçenekte yok. */}
      <div className="mb-3.5" role="group" aria-labelledby={kindsLabelId}>
        <div id={kindsLabelId} className={LABEL}>Olay türleri (boş = hepsi)</div>
        <div className="flex flex-wrap gap-3.5">
          {NOTIFY_KIND_OPTIONS.filter(o => o.value !== 'exception').map(o => (
            <span key={o.value} title={o.hint}>
              <CheckboxField checked={normalizeKinds(tc.kinds).includes(o.value)}
                onCheckedChange={() => setTc({ ...tc, kinds: toggleKind(normalizeKinds(tc.kinds), o.value) })}
                label={o.label} />
            </span>
          ))}
        </div>
      </div>

      {/* v0.10.967 — katalog düştü ama kayıtlı takımlar var: liste EKSİK, tam
          gibi sunulmasın (tablo içi hâl yalnız satır yokken; bu satırlarla yan
          yana yaşayan not, recipe §2). */}
      {rows.length > 0 && catalogQ.isError && (
        <div className="field-hint" role="status">
          {catalogQ.data === undefined
            ? `Servis kataloğu okunamadı (${humanize(catalogQ.error)}) — liste yalnız kayıtlı takımları gösteriyor.`
            : `Servis kataloğu tazelenemedi (${humanize(catalogQ.error)}) — katalog takımları son okumadan.`}
        </div>
      )}
      <div className="table-wrap mb-3.5">
        {/* v0.10.942 — statik tablo: düzenlenebilir eşleme listesi, satır başına adres girişi (T1). */}
        <Table>
          <TableHeader>
            <TableRow><TableHead>Takım</TableHead><TableHead>E-posta adres(ler)i</TableHead></TableRow>
          </TableHeader>
          <TableBody>
            {/* v0.10.967 (tablo standardı dilim 5, P-2) — statik tablonun durum satırı; colSpan = thead'deki 2 <th>.
                Kayıtlı adres okumasının yükleniyor / hatası sekmenin kapısı (form ona bağlı) — dışarıda;
                katalog okumasının yükleniyor / hatası bu satırda (routingState). */}
            {rows.length === 0 ? (
              <DataTableStateRow colSpan={2} {...routingState} />
            ) : rows.map(team => {
              const v = contactFor(team);
              return (
                <TableRow key={team.toLowerCase()}>
                  <TableCell className="mono">
                    {team}
                    {v.trim() === '' && (
                      <span className="ml-2"><StatusBadge status="warn" size="sm">eksik</StatusBadge></span>
                    )}
                  </TableCell>
                  <TableCell className="w-full">
                    <Input value={v}
                      onChange={e => setContact(team, e.target.value)}
                      placeholder="team@example.com, oncall@example.com"
                      aria-label={`${team} e-posta`}
                      className={BARE_INPUT} />
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      </div>

      {/* Katalog dışı takım ekleme — ör. henüz derive edilmemiş bir takım. */}
      <div className="mb-3.5 flex items-end gap-2">
        <Field className="w-[220px]" label="Katalog dışı takım ekle" value={extraTeam}
          onChange={e => setExtraTeam(e.target.value)} placeholder="takım adı" />
        <Button variant="outline" size="sm"
          disabled={!extraTeam.trim()}
          onClick={() => {
            const t = extraTeam.trim();
            // Seed an empty row — the operator types the address next.
            setTc(prev => prev
              ? { ...prev, contacts: { ...prev.contacts, [t]: prev.contacts[t] ?? '' } }
              : prev);
            setExtraTeam('');
          }}>
          Ekle
        </Button>
      </div>

      {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
      {/* R9 (D4): kaydetme satırı sağa yaslı, birincil en sağda. */}
      <ActionRow confirm={<Button type="submit" loading={busy}>Save</Button>} />
    </form>
    {/* v0.9.427 — takım alias tablosu, routing'in doğal komşusu. */}
    <TeamAliasesCard />
    </>
  );
}

// ── Takım eşleştirme (v0.9.427, operatör istegi) ────────────────────────────
// LDAP takım adı ("SY-Dijital Altyapı") ile telemetri metadata'sındaki
// ad ("dijitalsy", "orionsy") aynı takımın farklı yazımları olabiliyor —
// eşleme operatör tablosudur; my_services/my_problems soruları, inbox ve
// /problems owner/SRE filtreleri bu tablo üzerinden eşleşir. Karşılaştırma
// büyük/küçük harf + Türkçe İ/ı duyarsızdır; buradaki yazım yalnız gösterim.
export function TeamAliasesCard() {
  const confirm = useConfirm();
  const [ta, setTa] = useState<import('@/lib/types').TeamAliases | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [newAlias, setNewAlias] = useState('');
  const [newCanon, setNewCanon] = useState('');

  useEffect(() => {
    api.getTeamAliases().then(setTa).catch(() => setTa(null));
  }, []);

  const save = async (next: Record<string, string>) => {
    setBusy(true); setMsg(null);
    try {
      const saved = await api.putTeamAliases({ aliases: next });
      setTa(saved);
      setMsg({ kind: 'ok', text: 'Kaydedildi.' });
    } catch (e) {
      setMsg({ kind: 'err', text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  };

  const add = (e: FormEvent) => {
    e.preventDefault();
    const a = newAlias.trim(), c = newCanon.trim();
    if (!a || !c || !ta) return;
    void save({ ...ta.aliases, [a]: c });
    setNewAlias(''); setNewCanon('');
  };

  return (
    <div className="card mt-4">
      <div className="ov-card-h">
        <h3>Takım eşleştirme (alias)</h3>
        <span className="ov-sub">LDAP adı ↔ telemetri adı — "dijitalsy" → "SY-Dijital Altyapı"</span>
      </div>
      <div className="ov-card-b">
        {ta === undefined && <Spinner />}
        {ta === null && <Empty icon="✗" title="Alias tablosu okunamadı" />}
        {ta && (
          <>
            {Object.keys(ta.aliases).length === 0 && (
              <div className="mb-2.5 text-[length:var(--fs-sm)] text-(--text3)">
                Henüz eşleme yok. Telemetrideki takım adını kanonik (LDAP) ada bağla.
              </div>
            )}
            {Object.entries(ta.aliases).map(([alias, canon]) => (
              <div key={alias} className="mb-1.5 flex items-center gap-2">
                <span className="mono min-w-[180px] text-[length:var(--fs-sm)]">{alias}</span>
                <span className="text-(--text3)">→</span>
                <span className="mono flex-1 text-[length:var(--fs-sm)]">{canon}</span>
                {/* v0.9.1010 (C4 + O10) — burası ONAYSIZ ve `ghost`tu:
                    tıklama ANINDA persist ediyordu (`void save(next)`),
                    yani tıkla-ve-gitti. Hem onay hem kırmızı dil geldi. */}
                <Button variant="ghost-danger" size="sm" disabled={busy}
                  onClick={async () => {
                    if (!await confirm({
                      title: 'Takım eşlemesi silinsin mi?',
                      body: <><code>{alias}</code> → <code>{canon}</code> eşlemesi
                        kaldırılacak. Telemetride <code>{alias}</code> yazan
                        kayıtlar artık kanonik takıma bağlanmaz ve sahipsiz
                        görünür.</>,
                      confirmLabel: 'Eşlemeyi sil',
                      danger: true,
                    })) return;
                    const next = { ...ta.aliases };
                    delete next[alias];
                    void save(next);
                  }}>Sil</Button>
              </div>
            ))}
            <form onSubmit={add} className="mt-2.5 flex gap-2">
              <Input value={newAlias} onChange={e => setNewAlias(e.target.value)}
                placeholder="telemetri adı (örn. orionsy)" aria-label="telemetri adı" disabled={busy}
                className={`flex-1 ${BARE_INPUT}`} />
              <Input value={newCanon} onChange={e => setNewCanon(e.target.value)}
                placeholder="kanonik ad (örn. SY-Ortak Servisler)" aria-label="kanonik ad" disabled={busy}
                className={`flex-1 ${BARE_INPUT}`} />
              <Button type="submit" disabled={busy || !newAlias.trim() || !newCanon.trim()}>Ekle</Button>
            </form>
            {msg && <FlashBox kind={msg.kind}>{msg.text}</FlashBox>}
          </>
        )}
      </div>
    </div>
  );
}
