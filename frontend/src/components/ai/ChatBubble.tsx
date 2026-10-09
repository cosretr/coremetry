import { Component, Fragment, createContext, memo, useContext, useMemo, useState, type ErrorInfo, type ReactNode } from 'react';
import { chatErrorText } from './chatErrorText';
import { Link, useLocation, useNavigate } from 'react-router-dom';
import { AIFeedbackButtons } from './AIFeedbackButtons';
import { chartBlocks, mergeBlockLinks, traceListBlocks } from '@/lib/chatBlocks';
import { ChatTraceList } from './ChatTraceList'; // v0.10.688 — trace_list bloğu
import { evidenceBlocks } from '@/lib/chatEvidence'; // v0.10.558
import { EvidenceCard } from './EvidenceCard';
import { parseAction, actionVisible, applyActionHref } from '@/lib/pageActions';
import { Button } from '@/components/ui/Button';
import type { ChatTurn, ChatStepDetail, ChatTypedBlock } from '@/lib/types';
import { traceHref } from '@/lib/traceHref';
import { isPlainLeftClick } from '@/lib/a11y'; // v0.10.1105
import { CosreChart, type CosreChartSpec } from '@/components/CosreChart';
import {
  chatPlainText, parseChatBlocks, codeLineCount, definitionRows, diffLineKind, sanitizeFileName,
  CALLOUT_LABEL, CODE_COLLAPSE_LINES, type CalloutVariant, type ChatBlock,
} from './chatMarkdown';
import { parseInline, inlinePlain, type InlineNode } from './chatInline'; // v0.10.1137
import { buildLinkPolicy, classifyLink, EMPTY_POLICY, hostOf, toAppPath, UNVERIFIED_TITLE, type LinkPolicy } from './chatLinks'; // v0.10.1137
import { parseStepPreview, fmtPreviewBytes } from './stepPreview';
import { DisclosureButton } from '@/components/ui/DisclosureButton';
import { Chip } from '@/components/ui/Chip';
import { summarizeSteps, parseToolError, previewFirstLine, visibleRows, isDeadlineError, fmtMs, VISIBLE_ROWS, sourceStates, stateUnknown, stepRunning, toolErrorLabel } from './toolSteps';
import { StateBadges } from './StateBadges'; // v0.10.948 — paylaşılan durum rozetleri
import { chatLinkTargetProps, useChatLinkNewTab } from './chatLinkTarget'; // v0.10.1125 — /cosre yeni sekme
import { sourceChips, type SourceChip } from './sourceChips'; // v0.10.1127 — hedef başına tek kaynak çipi

// ChatBubble — bir sohbet turunun ÇİZİMİ. v0.9.479'da CopilotChat.tsx'ten
// buraya taşındı: AI çekmecesi içindeki sohbet (AIDrawer) aynı balonu
// kullanır — ikinci bir chat implementasyonu YOK.
//
// v0.10.1137 (operatör: "URL'ler tıklanabilir olsun", "Claude gibi profesyonel
// bir sohbet") — üç değişiklik:
//   1. SATIR İÇİ ÇİZİM innerHTML DEĞİL. mdLite (escapeHTML → regex → HTML
//      dizesi) yerini saf düğüm çözücüsüne (chatInline.ts) bıraktı; düğümler
//      React çocukları olarak basılıyor, yani balonda dangerouslySetInnerHTML
//      HİÇ yok ve kaçış React'in kendisi. Ham HTML hiçbir koşulda geçmez.
//   2. LİNKLER güvenlik kararından geçer (chatLinks.ts): aynı-köken yol ya da
//      sunucunun "kaynakta vardı" dediği adres tıklanır; gerisi tam adresiyle
//      düz metin + "doğrulanmamış bağlantı". Görsel asla çizilmez.
//   3. GÖRÜNÜM: asistan cevabı kart değil sayfadaki düz metin (Claude gibi);
//      [n] atıfları kaynak hapı; adımlar tek "Nasıl cevapladım" açılırında;
//      kaynaklar altta numaralı liste; eylemler (kopyala / 👍👎) üstüne
//      gelince.

// MdCtx — satır içi çizimin bağlamı: link politikası, atıf → kaynak eşlemesi,
// /cosre yeni-sekme kipi, aynı köken. Bağlamsız çizim (Explain/test) güvenli
// varsayılanla: hiçbir dış link doğrulanmış sayılmaz.
interface MdCtx { policy: LinkPolicy; cites: readonly SourceChip[]; newTab: boolean; origin: string }
const ChatMdContext = createContext<MdCtx>({ policy: EMPTY_POLICY, cites: [], newTab: false, origin: '' });

function currentOrigin(): string {
  return typeof window !== 'undefined' && window.location ? window.location.origin : '';
}

function renderNodes(nodes: readonly InlineNode[], ctx: MdCtx, kp: string): ReactNode[] {
  return nodes.map((n, i) => {
    const k = `${kp}${i}`;
    switch (n.t) {
      case 'text': return <Fragment key={k}>{n.v}</Fragment>;
      case 'code': return <code key={k}>{n.v}</code>;
      case 'strong': return <b key={k}>{renderNodes(n.c, ctx, k + '.')}</b>;
      case 'em': return <em key={k}>{renderNodes(n.c, ctx, k + '.')}</em>;
      case 'del': return <del key={k}>{renderNodes(n.c, ctx, k + '.')}</del>;
      case 'kbd': return <kbd key={k} className="cm-kbd">{n.v}</kbd>;
      case 'trace':
        // v0.9.419 — href traceHref üreticisinden; data-nav ile SPA içi gezinme
        // (ChatBubble onBodyClick). /cosre'de yeni sekme (v0.10.1125).
        return <a key={k} href={traceHref(n.id)} data-nav="1" {...chatLinkTargetProps(ctx.newTab)}>{n.id}</a>;
      case 'cite': return <CitePill key={k} n={n.n} ctx={ctx} />;
      case 'link': return <MdLink key={k} href={n.href} label={n.label} ctx={ctx} k={k} />;
    }
    return null;
  });
}

// MdLink — v0.10.1137: bağlantının tek çizim noktası; karar chatLinks.classifyLink'te.
function MdLink({ href, label, ctx, k }: { href: string; label: InlineNode[] | null; ctx: MdCtx; k: string }) {
  const cls = classifyLink(href, ctx.policy);
  const labelText = label ? inlinePlain(label) : '';
  if (cls === 'relative') {
    // v0.10.1137 inceleme — güvenli SPA yolu (köken WHATWG URL ile karşılaştırılır,
    // "//evil" / "/\evil" / %2F%2F / %5C reddedilir); çıkmazsa doğrulanmamış metin.
    const to = toAppPath(href, ctx.origin);
    if (to) {
      return (
        <a href={to} data-nav="1" className="cm-md-a" title={to} {...chatLinkTargetProps(ctx.newTab)}>
          {label ? renderNodes(label, ctx, k + '.') : to}
        </a>
      );
    }
    return <span className="cm-md-unverified" title={UNVERIFIED_TITLE}>{label && labelText !== href ? `${labelText} (${href})` : href}</span>;
  }
  if (cls === 'allowed') {
    // Tam adres sunucunun doğrulama listesinde: etiket metni kalır, host ipucunda.
    const host = hostOf(href);
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className="cm-md-a is-ext"
        title={label && labelText !== href ? `${host} — ${href}` : href}>
        {label ? renderNodes(label, ctx, k + '.') : href}
      </a>
    );
  }
  if (cls === 'allowed-host') {
    // Yalnız host (+ çok kiracılıda ilk yol parçası) tuttu: etiket adresi
    // GİZLEMEZ — etiketin yanında host ↗ görünür, tam adres ipucunda.
    const host = hostOf(href);
    return (
      <a href={href} target="_blank" rel="noopener noreferrer" className="cm-md-a is-host" title={href}>
        {label && labelText !== href ? <>{renderNodes(label, ctx, k + '.')} <span className="cm-md-a-host">({host})</span></> : href}
      </a>
    );
  }
  if (cls === 'unverified') {
    // TAM adres görünür: doğrulanmamış bir host için yanıltıcı çapa metni yok.
    return (
      <span className="cm-md-unverified" title={UNVERIFIED_TITLE}>
        {label && labelText !== href ? `${labelText} (${href})` : href}
      </span>
    );
  }
  // blocked (javascript:, data:, //host …) — asla link; yalnız etiket metni.
  return <>{label ? renderNodes(label, ctx, k + '.') : href}</>;
}

// CitePill — [n] atfı: kaynak listesindeki n. kaynağa eşlenir (sunucu bağlam
// numarasını "Kaynak n" sırasıyla verir, chat_sources.go sourceNumbers).
// Eşleşme yoksa işaret olduğu gibi metin kalır (uydurma numara hap olmaz).
function CitePill({ n, ctx }: { n: number; ctx: MdCtx }) {
  const src = n >= 1 ? ctx.cites[n - 1] : undefined;
  if (!src) return <>[{n}]</>;
  const tip = `${src.name}${src.host ? ` · ${src.host}` : ''}`;
  return (
    <sup className="cm-cite">
      {src.href ? (
        <a href={src.href} target="_blank" rel="noopener noreferrer" data-tip={tip}
          aria-label={`Kaynak ${n}: ${src.name}`}>{n}</a>
      ) : (
        <span tabIndex={0} data-tip={tip} aria-label={`Kaynak ${n}: ${src.name}`}>{n}</span>
      )}
    </sup>
  );
}

// MdInline — satır içi işaretlemenin TEK basım noktası. Satır içi SPAN: akış
// imleci metne yapışık durur (ChatBubble.render.test.tsx).
function MdInline({ text }: { text: string }) {
  const ctx = useContext(ChatMdContext);
  const nodes = useMemo(() => parseInline(text), [text]);
  return <span>{renderNodes(nodes, ctx, 'i')}</span>;
}

// CodeBlock (v0.9.1148; v0.10.1137 dosya bloğu) — ``` fence'inin çizimi.
// Gövde React ÇOCUĞU olarak basılıyor, satır içi çözücüden GEÇMİYOR: kod
// literaldir. v0.10.1137: başlık şeridinde dosya adı (bilgi dizesinin
// title=/lang:yol kısmı) + dil + Kopyala + İndir; satır numaraları CSS
// sayacıyla (kopyalanan/seçilen metne karışmaz); 25 satırı aşan blok
// "Tümünü göster (N satır)" arkasında; diff satırları +/- renkli; uzun
// satırlar düğmeyle sarılır, yoksa yatay kayar.
//
// Kopyala/İndir yalnız çit KAPANDIYSA görünür: yarım bir bloğu kopyalatmak,
// operatörün sessizce eksik bir komut çalıştırması demek. Başlık şeridi
// durumu YAZIYOR: akarken "yazılıyor…", akış bitmiş ama çit hiç
// kapanmamışsa "kesildi".
function CodeBlock({ lang, code, open, streaming, title }: {
  lang: string; code: string; open: boolean; streaming: boolean; title?: string;
}) {
  const [copied, setCopied] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [wrap, setWrap] = useState(false);
  const copy = () => {
    navigator.clipboard?.writeText(code).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    }).catch(() => {});
  };
  const download = () => {
    try {
      const url = URL.createObjectURL(new Blob([code], { type: 'text/plain;charset=utf-8' }));
      const a = document.createElement('a');
      a.href = url;
      a.download = sanitizeFileName(title, lang);
      a.rel = 'noopener noreferrer';
      document.body.appendChild(a);
      a.click();
      a.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch { /* indirme desteklenmiyor — sessiz */ }
  };
  const total = codeLineCount(code);
  const collapsible = !open && total > CODE_COLLAPSE_LINES;
  const lines = code.split('\n');
  const shown = collapsible && !expanded ? lines.slice(0, CODE_COLLAPSE_LINES) : lines;
  const isDiff = lang === 'diff' || lang === 'patch';
  const numbered = total > 1;
  return (
    <div className={`cm-md-code${title ? ' is-file' : ''}`}>
      <div className="cm-md-code-h">
        {title && <span className="cm-md-code-file" title={title}>{title}</span>}
        <span className="cm-md-code-lang">{lang || (title ? '' : 'kod')}</span>
        {open && <span className="cm-md-code-st">{streaming ? 'yazılıyor…' : 'kesildi'}</span>}
        {!open && (
          <span className="cm-md-code-acts">
            <Button variant="ghost" size="xs" onClick={() => setWrap(w => !w)} aria-pressed={wrap}
              title={wrap ? 'Uzun satırları kaydır' : 'Uzun satırları sar'}>{wrap ? '↔' : '↩'}</Button>
            {(title || total >= 10) && (
              <Button variant="ghost" size="xs" onClick={download}
                title={`İndir: ${sanitizeFileName(title, lang)}`} aria-label="Kod bloğunu indir">⤓ İndir</Button>
            )}
            <Button variant="ghost" size="xs" onClick={copy}
              title="Kodu kopyala" aria-label="Kod bloğunu kopyala"
              className={copied ? 'is-ok' : undefined}>
              {copied ? '✓' : '⧉ Kopyala'}
            </Button>
          </span>
        )}
      </div>
      {numbered || isDiff ? (
        <pre className={[numbered ? 'is-num' : '', wrap ? 'is-wrap' : ''].filter(Boolean).join(' ') || undefined}><code>
          {shown.map((l, i) => {
            const dk = isDiff ? diffLineKind(l) : null;
            return <span key={i} className={dk ? `cm-cl is-${dk}` : 'cm-cl'}>{l}{i < shown.length - 1 ? '\n' : ''}</span>;
          })}
        </code></pre>
      ) : (
        <pre className={wrap ? 'is-wrap' : undefined}>{code}</pre>
      )}
      {collapsible && (
        <div className="cm-md-code-more">
          <DisclosureButton anatomy="row" expanded={expanded} onClick={() => setExpanded(v => !v)}>
            {expanded ? 'Daralt' : `Tümünü göster (${total} satır)`}
          </DisclosureButton>
        </div>
      )}
    </div>
  );
}

// MdTable (v0.9.1148) — markdown tablosu GERÇEK tablo (sohbet markdown'ı
// tablo standardından muaf: kolonlar modelin o cevapta uydurduğu başlıklar).
// Kaydırma kabı `.table-wrap` (yatay taşma TABLONUN kabında), hizalama
// `.num`, zebra + kenarlık `.cm-md-table`.
function MdTable({ block }: { block: Extract<ChatBlock, { kind: 'table' }> }) {
  const cls = (i: number) => (block.align[i] === 'right' ? 'num' : block.align[i] === 'center' ? 'ta-c' : undefined);
  // 100+ satır kuralı (ev kısıtı): content-visibility bedelsiz.
  const heavy = block.rows.length > 100;
  const rowStyle = heavy
    ? { contentVisibility: 'auto' as const, containIntrinsicSize: '26px' }
    : undefined;
  return (
    <div className="table-wrap cm-md-tw">
      <table className="cm-md-table">
        <thead>
          <tr>{block.head.map((h, i) => <th key={i} className={cls(i)}><MdInline text={h} /></th>)}</tr>
        </thead>
        <tbody>
          {block.rows.map((r, k) => (
            <tr key={k} style={rowStyle}>
              {r.map((c, i) => <td key={i} className={cls(i)}><MdInline text={c} /></td>)}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

// v0.10.1137 — dört kademe; etiket h4–h6 (cevap sayfanın başlık
// hiyerarşisini ele geçirmez), görsel ölçek sınıftan (.cm-md-h1…h4).
const H_TAG = { 1: 'h4', 2: 'h5', 3: 'h6', 4: 'h6' } as const;
const CALLOUT_ICON: Record<CalloutVariant, string> = {
  summary: '≡', note: 'ℹ', tip: '💡', warning: '⚠', important: '❗', caution: '⛔',
};
const Caret = () => <span className="cm-ai-cursor" aria-hidden="true" />;

// Paragraflar: boş satırla ayrılan koşular <p>; tek satır sonu pre-wrap ile
// korunur. İmleç SON paragrafın içinde, metne yapışık.
function renderText(text: string, key: string, caret: boolean): ReactNode {
  const paras = text.split(/\n[ \t]*\n+/);
  return (
    <Fragment key={key}>
      {paras.map((p, i) => {
        // v0.10.1137 — "**Anahtar:** değer" satırları tanım listesi.
        const rows = !(caret && i === paras.length - 1) ? definitionRows(p) : null;
        if (rows) {
          return (
            <dl key={i} className="cm-md-dl">
              {rows.map((r, k) => (
                <Fragment key={k}><dt><MdInline text={r.key} /></dt><dd><MdInline text={r.value} /></dd></Fragment>
              ))}
            </dl>
          );
        }
        return (
        <p key={i} className="cm-md-p">
          <MdInline text={p} />
          {caret && i === paras.length - 1 && <Caret />}
        </p>
        );
      })}
    </Fragment>
  );
}

function renderBlocks(blocks: readonly ChatBlock[], streaming: boolean, typedCharts: unknown[], caret: boolean, kp: string): ReactNode[] {
  const out: ReactNode[] = [];
  blocks.forEach((b, idx) => {
    const i = `${kp}${idx}`;
    const last = idx === blocks.length - 1;
    switch (b.kind) {
      case 'text':
        out.push(renderText(b.text, i, caret && last));
        if (caret && last) caret = false;
        break;
      case 'heading': {
        // h1-h3 DEĞİL: cevap sayfanın içinde bir bölüm, başlık hiyerarşisini
        // ele geçirmemeli (ekran okuyucu için sayfa özeti bozulur).
        const H = H_TAG[b.level];
        out.push(<H key={i} className={`cm-md-h cm-md-h${b.level}`}><MdInline text={b.text} /></H>);
        break;
      }
      case 'hr':
        out.push(<hr key={i} className="cm-md-hr" />);
        break;
      case 'callout':
        out.push(
          <div key={i} className={`cm-callout is-${b.variant}`} role={b.variant === 'warning' || b.variant === 'caution' ? 'note' : undefined}>
            <div className="cm-callout__h">
              <span className="cm-callout__i" aria-hidden="true">{CALLOUT_ICON[b.variant]}</span>
              <span>{CALLOUT_LABEL[b.variant]}</span>
              {b.title && <span className="cm-callout__t"><MdInline text={b.title} /></span>}
            </div>
            <div className="cm-callout__b">{renderBlocks(b.blocks, streaming, typedCharts, caret && last, i + '.')}</div>
          </div>
        );
        if (caret && last) caret = false;
        break;
      case 'details':
        out.push(
          <details key={i} className="cm-md-details" open={b.open || (streaming && !b.closed) || undefined}>
            <summary><MdInline text={b.summary} /></summary>
            <div className="cm-md-details__b">{renderBlocks(b.blocks, streaming, typedCharts, false, i + '.')}</div>
          </details>
        );
        break;
      case 'quote':
        out.push(<blockquote key={i} className="cm-md-quote">{renderBlocks(b.blocks, streaming, typedCharts, false, i + '.')}</blockquote>);
        break;
      case 'list': {
        const L = b.ordered ? 'ol' : 'ul';
        out.push(
          <L key={i} className="cm-md-list" start={b.ordered ? b.start : undefined}>
            {b.items.map((it, k) => (
              <li key={k} className={b.tasks?.[k] != null ? 'is-task' : undefined}>
                {b.tasks?.[k] != null && (
                  // Salt-okunur görev kutusu (GFM): tıklanamaz, durum aria'da.
                  <input type="checkbox" className="cm-task" checked={!!b.tasks[k]} readOnly disabled
                    aria-label={b.tasks[k] ? 'tamamlandı' : 'yapılacak'} />
                )}
                <MdInline text={it} />
                {b.nested?.[k] && renderBlocks(b.nested[k] as ChatBlock[], streaming, typedCharts, false, `${i}.${k}.`)}
              </li>
            ))}
          </L>
        );
        break;
      }
      case 'code': {
        // v0.10.1145 — NO_CHARTS (kullanıcı turu / önizleme): ```chart çiti kod
        // bloğu olarak kalır; kullanıcının yazdığı metin grafik sorgusu tetiklemez.
        if (b.lang === 'chart' && typedCharts !== NO_CHARTS) {
          if (b.open) {
            if (streaming) {
              out.push(<span key={i} className="cm-md-wait">grafik hazırlanıyor…</span>);
              break;
            }
          } else {
            if (typedCharts.length > 0) break; // v0.10.541 — tipli blok kazanır
            try {
              const spec = JSON.parse(b.code.trim()) as CosreChartSpec;
              if (spec && typeof spec.service === 'string' && typeof spec.agg === 'string') {
                out.push(<CosreChart key={i} spec={spec} />);
              }
            } catch { /* bozuk spec — atla (asla crash) */ }
            break;
          }
        }
        out.push(<CodeBlock key={i} lang={b.lang} code={b.code} open={b.open} streaming={streaming} title={b.title} />);
        break;
      }
      case 'table':
        out.push(<MdTable key={i} block={b} />);
        break;
    }
  });
  if (caret) out.push(<Caret key={`${kp}caret`} />);
  return out;
}

// renderMessage (v0.9.183, Faz 4.2'de blok çözücüye geçti) — asistan
// metnini çizer: paragraflar, başlık/liste (iç içe)/alıntı/çizgi/tablo/kod
// blokları, ```chart``` blokları canlı <CosreChart> ile. `streaming` =
// tur akıyor (turn.pending): yarım satır kararı çözücüde, imleç burada
// (son metnin içinde).
// Chart bloğunun akış davranışı:
//   akıyor + kapanmamış → "grafik hazırlanıyor" satırı;
//   bitti + kapanmamış (yanıt KESİLDİ) → kod bloğu (içerik yutulmaz);
//   kapandı + geçerli spec → grafik; kapandı + bozuk → atlanır (v0.9.183).
// v0.10.1145 — `opts.charts: false`: ```chart çitleri grafik değil kod (kullanıcı metni).
const NO_CHARTS: unknown[] = [];
export function renderMessage(text: string, streaming = false, typed?: ChatTypedBlock[], opts?: { charts?: boolean }) {
  // v0.10.541 — tipli chart blokları varsa fence grafikleri ÇİZİLMEZ.
  const typedCharts = opts?.charts === false ? NO_CHARTS : chartBlocks(typed);
  const blocks = parseChatBlocks(text, streaming);
  // Yedek dal İÇERİK YOKLUĞUNA bakıyor (bozuk chart bloğu bilinçli atlanır).
  if (blocks.length === 0) return <>{streaming && <Caret />}</>;
  return <>{renderBlocks(blocks, streaming, typedCharts, streaming, 'b')}</>;
}


// useChatNavClick — data-nav linkleri (trace id'ler, aynı-köken yollar) SPA
// içinde gider (v0.9.419); yalnız DÜZ sol tık (v0.10.1105); /cosre'de (yeni
// sekme kipi) yakalanmaz (v0.10.1125). Balon, kullanıcı turu ve önizleme ortak.
function useChatNavClick(newTab: boolean) {
  const navigate = useNavigate();
  return (e: React.MouseEvent) => {
    const a = (e.target as HTMLElement).closest?.('a[data-nav]');
    if (a && !newTab && isPlainLeftClick(e)) {
      e.preventDefault();
      navigate(a.getAttribute('href') ?? '/');
    }
  };
}

// ChatMarkdown — v0.10.1145: KULLANICI metninin çizimi (sohbetteki kullanıcı
// turu + composer önizlemesi). Cevaplarla AYNI çözücü ve çizici (renderMessage);
// fark yalnız güven: bağlantı politikası BOŞ (sunucu doğrulaması yok) → dış
// adresler "doğrulanmamış bağlantı" düz metni, yalnız aynı köken / uygulama
// yolu tıklanır; ```chart çiti kod bloğu kalır (sorgu tetiklemez).
export function ChatMarkdown({ text }: { text: string }) {
  const newTab = useChatLinkNewTab();
  const onClick = useChatNavClick(newTab);
  const ctx = useMemo<MdCtx>(() => {
    const origin = currentOrigin();
    return { policy: buildLinkPolicy({ origin }), cites: [], newTab, origin };
  }, [newTab]);
  const body = useMemo(() => renderMessage(text, false, undefined, { charts: false }), [text]);
  return (
    <ChatMdContext.Provider value={ctx}>
      <div className="cm-md-user" onClick={onClick}>{body}</div>
    </ChatMdContext.Provider>
  );
}

// ToolChips — ⚙ ilerleme çipleri + tıklayınca açılan KANIT bloğu
// (v0.9.1181, AI Faz 4.3).
//
// Neden gerekliydi: çip yalnız tool ADINI gösteriyordu. Model "topolojiye
// baktım, şu servis suçlu" dediğinde operatörün o iddiayı sınayacak hiçbir
// şeyi yoktu — veriyi gördü mü, ne gördü, kırpıldı mı, hepsi görünmezdi.
// Bir APM'de "modele güven" kabul edilebilir bir cevap değil.
//
// Çip yalnız DETAY VARSA düğmeye dönüşür. Arşivden geri yüklenen konuşmalar
// detay taşımaz (chatPersist yalnız {role,text} saklar) ve eski bir sunucuya
// karşı akan FE de taşımaz; ikisinde de çip eskisi gibi düz bir etiket kalır.
// Boş açılan bir "veriyi göster" ölü affordance olurdu (v0.9.592 dersi).
// v0.10.944 (CoSRE Faz A) — DÜRÜST İLERLEME: çip, sonucu gelene dek
// "çalışıyor…" der (sunucu çipi aracı çalıştırmadan ÖNCE yayınlar; çip
// "denendi" demektir, "yürütüldü" değil); sonuç `source.state` taşıyorsa
// durum rozeti (boş · erişilemedi · yetki yok · zaman aşımı · kısmi ·
// gecikmeli · limitli), `skipped:true` ise "yürütülmedi". Bağlam etiketleri
// (araç adı olmayan) "çalışıyor…" demez — onlara hiç sonuç gelmez.
// v0.10.948 — StateBadges (kaynak öneki yalnız birden çok kaynakta, pencere
// öneki detail'den) ./StateBadges.tsx'e taşındı — tek yazım.

function ToolChips({ steps, details, hasText, turnDone, evId, setEvId }: {
  steps: string[];
  details?: ChatStepDetail[];
  hasText: boolean;
  /** v0.10.944 — tur bitti: sonucu gelmemiş çip artık "çalışıyor…" demez. */
  turnDone: boolean;
  /** v0.10.161 — açık kanıtın adım kimliği (d.i), şeffaflık paneliyle PAYLAŞILIR: aynı kanıt iki kez açılmaz. */
  evId: number | null;
  setEvId: (i: number | null) => void;
}) {
  const openIdx = evId == null ? null : (details ?? []).findIndex(d => d.i === evId);
  const setOpenIdx = (i: number | null) => setEvId(i == null ? null : (details?.[i]?.i ?? null));
  // Çip sırası ile detay sırası aynı: ikisi de `step` olayında birlikte
  // büyüyor. Yine de indeksle DEĞİL, dizideki konumla eşliyoruz ve detayın
  // varlığını ayrıca kontrol ediyoruz — kısa detay dizisi (rolling deploy)
  // patlamamalı.
  const detailAt = (i: number) => details?.[i];
  const open = openIdx == null ? undefined : detailAt(openIdx);
  return (
    <div style={{ marginBottom: hasText ? 6 : 0 }}>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 4 }}>
        {steps.map((s, i) => {
          const d = detailAt(i);
          const ready = !!d && d.preview !== undefined;
          const isOpen = openIdx === i;
          const chipStyle: React.CSSProperties = {
            fontSize: 10, fontFamily: 'var(--font-mono)', // v0.10.973 — tablo standardı T5: tek monospace yığını
            padding: '1px 6px', borderRadius: 8,
            background: isOpen ? 'var(--accent-bg)' : 'var(--bg3)',
            color: isOpen ? 'var(--accent2)' : 'var(--text3)',
            border: '1px solid transparent',
          };
          if (!ready) {
            return (
              <span key={i} style={chipStyle}>
                ⚙ {s}{stepRunning(d, turnDone) && <span> · çalışıyor…</span>}
              </span>
            );
          }
          const states = d?.skipped ? [] : sourceStates(d?.preview, d?.sources); // v0.10.944 — yapısal durum önce
          // v0.10.924 — buton bütünlüğü Faz 2: elle boyanmış çip → Chip atomu.
          // Açıklık `tone` ile boyanır, `active` ile DEĞİL: durum aria-expanded'da;
          // `active` bir de aria-pressed basar ve ekran okuyucu iki durum duyardı.
          return (
            <Fragment key={i}>
              <Chip size="xs" pill tone={isOpen ? 'accent' : 'neutral'}
                onClick={() => setOpenIdx(isOpen ? null : i)}
                aria-expanded={isOpen}
                title={d?.skipped ? 'Sunucu bu çağrıyı yürütmedi — nedenini göster' : d?.ok === false ? 'Tool hata döndürdü — veriyi göster' : 'Bu adımın verisini göster'}
                style={{ fontFamily: chipStyle.fontFamily }}>
                ⚙ {s} {d?.skipped ? '· yürütülmedi ' : d?.ok === false ? '⚠' : ''}{isOpen ? '▾' : '▸'}
              </Chip>
              <StateBadges states={states} />
            </Fragment>
          );
        })}
      </div>
      {open && <ToolEvidence d={open} />}
    </div>
  );
}

// ToolEvidence — açılan blok. Modelin GÖRDÜĞÜ metnin kendisi; kırpma
// İLAN EDİLİR (sessiz kırpma, eksik kanıta tam kanıt sanıp bakmak demek).
function ToolEvidence({ d }: { d: ChatStepDetail }) {
  const view = parseStepPreview(d.preview ?? '', d.truncated);
  return (
    <div style={{
      marginTop: 6, padding: '6px 8px', borderRadius: 6,
      background: 'var(--bg1)', border: '1px solid var(--border)',
      fontSize: 11, whiteSpace: 'normal',
    }}>
      {d.args && d.args !== '{}' && (
        <div className="mono" style={{ fontSize: 10, color: 'var(--text3)', marginBottom: 4, wordBreak: 'break-all' }}>
          {d.tool}({d.args})
        </div>
      )}
      {/* v0.9.1228 — çağrının ürün görünümü: sunucunun K4-denetimli
          haritasından gelir (model metninden asla), yoksa çizilmez. */}
      {d.href && (
        <a className="sec" href={d.href} target="_blank" rel="noopener noreferrer"
          style={{ fontSize: 10.5, padding: '2px 8px', marginBottom: 4, display: 'inline-block' }}>
          ↗ Üründe aç
        </a>
      )}
      {d.truncated && (
        <div className="badge b-warn" style={{ marginBottom: 4 }}>
          kırpıldı · {fmtPreviewBytes(d.bytes ?? 0)} içinden ilk {fmtPreviewBytes(4096)}
        </div>
      )}
      {view.kind === 'table' ? (
        // Kanıt bakışı, veri sayfası DEĞİL — bu yüzden useDataTable yok
        // (sıralama/yeniden boyutlandırma/kalıcı genişlik burada anlamsız).
        // Görünüm dili sohbetin markdown tablosuyla aynı: .cm-md-table.
        <div className="cm-md-tw" style={{ overflowX: 'auto' }}>
          <table className="cm-md-table">
            <thead><tr>{view.cols.map(c => <th key={c}>{c}</th>)}</tr></thead>
            <tbody>
              {view.rows.map((r, i) => (
                <tr key={i}>{r.map((c, j) => <td key={j}>{c}</td>)}</tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <pre className="mono" style={{
          margin: 0, maxHeight: 220, overflow: 'auto',
          // v0.10.79 — break-word uzun TEK token'da (nitelikli Java adı)
        // flex min-content'e yenilebiliyor; anywhere kırmayı garantiler.
        // Sarma YENİ bir düğümle değil mevcut stile eklendi: araya blok
        // kutu koymak akış imlecini alt satıra atıyordu ve
        // ChatBubble.render.test.tsx bunu yakaladı (imleç SPAN'a yapışık).
        whiteSpace: 'pre-wrap', wordBreak: 'break-word', overflowWrap: 'anywhere',
          fontSize: 10, color: 'var(--text2)',
        }}>{view.text || '(boş)'}</pre>
      )}
    </div>
  );
}

// ToolStepsPanel — v0.10.161 (tasarım etüdü seçenek A «Adım listesi»).
// Çip şeridi KAPALI hâl olarak aynen kalır (regresyon yok); altına özet
// satırı («⚙ N araç · M hata · Σ s») ve açılınca balon içi tablo gelir:
// Araç · Argümanlar · Süre · Sonuç ilk satırı · Durum. Kanıt bakışı, veri
// sayfası DEĞİL — useDataTable yok (ToolEvidence gerekçesi aynen).
//   - Süre çubuğu NÖTR (--accent), yalnız hatalı satır kırmızı — araç
//     gecikmesi için üründe eşik/rampa yok (yargıç must-fix).
//   - Toplam süre bir adımın durationMs'i yoksa «—» (ölçüm gibi çizilmez).
//   - «ön-yükleme» rozeti tek, özet satırında; step.origin'den.
//   - 10+ çağrı: ilk 5 satır + «▸ N daha».
//   - Bütçe aşımı (chatDeadlineMessageTR) → özet satırında rozet (metnin
//     kendisi zaten hata balonunda; tavan MODEL turunda düşer, "N. çağrıdan
//     sonra" iddiası yapılmaz).
// Arşivden geri yüklenen turlarda detay yok → panel çizilmez (chatPersist
// yalnız {role,text} saklar; çipler eskisi gibi düz etiket).
export function ToolStepsPanel({ details: allDetails, error, turnDone, evId, setEvId, initialOpen = false }: {
  details: ChatStepDetail[]; error?: string; turnDone: boolean;
  evId: number | null; setEvId: (i: number | null) => void;
  /** test dikişi — tablo gövdesini kapalı DisclosureButton'a tıklamadan çizdirir */
  initialOpen?: boolean;
}) {
  const [open, setOpen] = useState(initialOpen);
  const [all, setAll] = useState(false);
  // Yalnız ARAÇ satırları (inceleme blocker): `tool`suz etiket adımları
  // (ekran bağlamı, pencere çapası, taşma yeniden denemesi) araç değil —
  // sayılsalar «N araç» künyeyle çelişir ve Σ süre hep «—» kalırdı.
  const details = allDetails.filter(d => !!d.tool);
  const sum = summarizeSteps(details, turnDone);
  const budget = isDeadlineError(error);
  const rows = visibleRows(details, all);
  const maxMs = details.reduce((m, d) => Math.max(m, d.durationMs ?? 0), 0);
  const ev = evId == null ? undefined : details.find(d => d.i === evId);
  if (details.length === 0) return null;
  return (
    <div className="cm-steps">
      <div className="cm-steps-sum">
        <DisclosureButton anatomy="row" expanded={open} onClick={() => setOpen(v => !v)}
          title={sum.totalMs === null ? 'Toplam süre: en az bir adımın süresi ölçülmedi' : 'Araç sürelerinin toplamı (model süresi hariç)'}>
          ⚙ {sum.count} araç
          {sum.errors > 0 && <> · <span style={{ color: 'var(--err)' }}>{sum.errors} hata</span></>}
          {sum.skipped > 0 && <> · {sum.skipped} yürütülmedi</>}
          {sum.pending > 0 && <> · {sum.pending} sürüyor</>}
          {sum.noEvidence > 0 && <> · {sum.noEvidence} kanıtsız</>}
          {' · '}{sum.totalMs === null ? '—' : fmtMs(sum.totalMs)}
        </DisclosureButton>
        {sum.guided && <span className="badge b-info" title="Rehberli kip: model araç çağırmadı, kanıt sunucuda önceden toplandı">ön-yükleme</span>}
        {sum.intent && <span className="badge b-info" title="Serbest soru tek JSON çağrısıyla kılavuz niyetine sınıflandırıldı; kanıt sunucuda toplandı (v0.10.172)">niyet</span>}
        {budget && <span className="badge b-warn" title={error}>⏱ bütçe aşıldı</span>}
      </div>
      {open && (
        <div className="cm-steps-tw">
          <table className="cm-steps-t">
            <thead><tr><th>#</th><th>Araç</th><th>Argümanlar</th><th className="num">Süre</th><th>Sonuç</th><th>Durum</th></tr></thead>
            <tbody>
              {rows.map(d => {
                const err = d.ok === false && !d.skipped ? parseToolError(d.preview) : null;
                const states = d.skipped ? [] : sourceStates(d.preview, d.sources); // v0.10.944 — yapısal durum önce
                const isOpen = evId === d.i;
                const settled = d.preview !== undefined;
                const noEv = !settled && turnDone; // kanıt hiç yayınlanmadı (boş metin)
                const first = err ? `${err.cls}${err.hint ? ` — ${err.hint}` : ''}` : previewFirstLine(d.preview, 90);
                return (
                  <Fragment key={d.i}>
                    <tr className={[d.ok === false && !d.skipped ? 'is-err' : '', isOpen ? 'is-open' : ''].filter(Boolean).join(' ')}>
                      <td className="num">{d.i}</td>
                      <td className="cm-steps-tool">
                        {/* Gerçek düğme (aria-expanded) — çiplerle aynı çıta; DisclosureButton ATOMU
                            (taban sınıfları ui/ dışında elle yazılmaz — primitiveClasses gate'i). */}
                        <DisclosureButton anatomy="row" expanded={isOpen} disabled={!settled}
                          onClick={() => setEvId(isOpen ? null : d.i)}
                          title={settled ? 'Kanıtı göster' : noEv ? 'Bu adım için kanıt yayınlanmadı' : 'Sonuç bekleniyor'}>
                          {d.tool}
                        </DisclosureButton>
                      </td>
                      <td className="cm-steps-args" title={d.args}>{d.args && d.args !== '{}' ? d.args : '—'}</td>
                      <td className="num">
                        {typeof d.durationMs === 'number' ? fmtMs(d.durationMs) : (settled || noEv ? '—' : '…')}
                        {typeof d.durationMs === 'number' && maxMs > 0 && (
                          <span className="cm-steps-bar" aria-hidden="true"><i style={{ width: `${Math.max(2, (100 * d.durationMs) / maxMs)}%` }} /></span>
                        )}
                      </td>
                      <td className="cm-steps-res" title={err?.detail ?? d.preview}>
                        {settled ? first : noEv ? 'kanıt yok (boş sonuç)' : 'sürüyor…'}
                      </td>
                      {/* v0.10.973 — tablo standardı T5: satır içi `whiteSpace: nowrap` silindi; `tbody td` zaten tek satır (T11). */}
                      <td>
                        {!settled ? <span className="badge b-gray">{noEv ? 'kanıt yok' : '…'}</span>
                          : d.skipped ? <span className="badge b-gray" title="Sunucu bu çağrıyı yürütmedi (süre ölçüm değildir)">yürütülmedi</span>
                          : d.ok === false ? <span className="badge b-err" title={`${err ? toolErrorLabel(err.cls) : 'hata'}${err?.retryable ? ' · tekrar denenebilir' : ''}`}>⚠ {err?.cls === 'unauthorized' ? 'yetki yok' : 'hata'}{err?.retryable ? ' · tekrar' : ''}</span>
                          : states.length > 0 ? null
                          // v0.10.944 — kırpık JSON'da durum görünmüyorsa nötr «ok» YALAN olurdu (Go map sırası: veri anahtarları `source`tan önce)
                          : stateUnknown(d) ? <span className="badge b-warn" title="önizleme kırpık; kaynak durumu önizlemede yok">durum okunamadı</span>
                          : <span className="badge b-gray">ok</span> /* v0.10.929 (K5) — sohbet geçmişinde kalıcı: nötr */}
                        {settled && <StateBadges states={states} />}
                        {d.truncated && <span className="badge b-warn" style={{ marginLeft: 4 }} title={`önizleme 4 KB'a kırpıldı; gerçek boy ${fmtPreviewBytes(d.bytes ?? 0)}`}>kırpık</span>}
                      </td>
                    </tr>
                    {isOpen && ev && (
                      <tr className="cm-steps-ev"><td colSpan={6}><ToolEvidence d={ev} /></td></tr>
                    )}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
          {details.length > VISIBLE_ROWS && (
            <div className="cm-steps-more">
              <DisclosureButton anatomy="row" expanded={all} onClick={() => setAll(v => !v)}>
                {all ? 'daha az' : `${details.length - VISIBLE_ROWS} daha`}
              </DisclosureButton>
            </div>
          )}
          <div className="field-hint" style={{ marginTop: 4 }}>
            Sıra: step/step-result olayları · süre: sunucu ölçümü (araç başına; model süresi dâhil değil) · önizleme 4 KB tel tavanı, «bytes» gerçek boy · sayfa yenilenince panel gider (arşiv yalnız metin saklar).
          </div>
        </div>
      )}
    </div>
  );
}


// StepsDisclosure — v0.10.1137: adım çipleri + şeffaflık paneli TEK, sessiz
// bir "Nasıl cevapladım · N adım" açılırında (Claude'un "düşünce" şeridi gibi).
// Akış sürerken AÇIK (ilerleme görünür), tur bitince KENDİLİĞİNDEN kapanır —
// operatör elle açtı/kapattıysa onun seçimi kalır. `initialOpen` test dikişi.
function StepsDisclosure({ turn, evId, setEvId, initialOpen }: {
  turn: ChatTurn; evId: number | null; setEvId: (i: number | null) => void; initialOpen?: boolean;
}) {
  const [userOpen, setUserOpen] = useState<boolean | null>(initialOpen ?? null);
  const open = userOpen ?? !!turn.pending;
  const n = turn.steps?.length ?? 0;
  return (
    <div className="cm-how">
      <DisclosureButton anatomy="row" expanded={open} onClick={() => setUserOpen(!open)}
        className="cm-how__btn" title="Bu cevap için çalıştırılan adımlar ve kanıtları">
        Nasıl cevapladım · {n} adım{turn.pending ? ' · sürüyor…' : ''}
      </DisclosureButton>
      {open && (
        <div className="cm-how__body">
          {n > 0 && (
            <ToolChips steps={turn.steps ?? []} details={turn.stepDetails} hasText={!!turn.text} turnDone={!turn.pending} evId={evId} setEvId={setEvId} />
          )}
          {/* v0.10.161 — şeffaflık paneli yalnız detay (i'li step) varken; açık kanıt çiplerle ortak. */}
          {!!turn.stepDetails?.length && (
            <ToolStepsPanel details={turn.stepDetails} error={turn.error} turnDone={!turn.pending} evId={evId} setEvId={setEvId} />
          )}
        </div>
      )}
    </div>
  );
}

// SourceList — v0.10.1137: "Kaynak n" çip satırı yerine cevabın altında
// numaralı "Kaynaklar" listesi (başlık + host); 3'ten fazlası katlanır.
// Numara = metindeki [n] atfı = sunucunun bağlam numarası.
function SourceList({ chips }: { chips: readonly SourceChip[] }) {
  const [all, setAll] = useState(false);
  const shown = all || chips.length <= 3 ? chips : chips.slice(0, 3);
  return (
    <div className="cm-sources" role="group" aria-label="Kaynaklar">
      <div className="cm-sources__h">Kaynaklar</div>
      <ol className="cm-sources__list">
        {shown.map(c => (
          <li key={c.key} title={c.title}>
            <span className="cm-sources__n">{c.n}</span>
            {c.href ? (
              <a href={c.href} target="_blank" rel="noopener noreferrer" className="cm-sources__t">{c.name}</a>
            ) : (
              <span className="cm-sources__t">{c.name}</span>
            )}
            {c.host && <span className="cm-sources__host">{c.host}</span>}
          </li>
        ))}
      </ol>
      {chips.length > 3 && (
        <DisclosureButton anatomy="row" expanded={all} onClick={() => setAll(v => !v)} className="cm-sources__more">
          {all ? 'daha az' : `${chips.length - 3} kaynak daha`}
        </DisclosureButton>
      )}
    </div>
  );
}

/** v0.10.1138 — son cevabın sürüm geçişi ("önceki cevap (1/2)"). */
export interface ChatBubbleVersions { index: number; total: number; onSelect: (i: number) => void }

interface ChatBubbleProps {
  turn: ChatTurn; onRetry?: () => void;
  /** v0.10.1138 — "↻ Yeniden üret" (yalnız SON asistan cevabında). */
  onRegenerate?: () => void;
  /** v0.10.1138 — akış sürerken yeniden üret / düzenle kapalı. */
  busy?: boolean;
  versions?: ChatBubbleVersions;
  /** v0.10.1138 — son kullanıcı mesajını düzenle → kırp + yeniden koş. */
  onEdit?: (text: string) => void;
  /** test dikişi — "Nasıl cevapladım" açılırını tıklamadan açık çizer (statik render). */
  stepsOpen?: boolean;
}

// BubbleBoundary — v0.10.1137 inceleme: tek bir cevabın çiziminde beklenmedik
// bir hata (bozuk/kötü niyetli markdown, özyineleme) bütün sohbet ağacını
// söküp boş ekran bırakmasın — o balon DÜZ METİN olarak çizilir. Tur değişince
// (yeni delta / yeni cevap) sınır sıfırlanır ve zengin çizim yeniden denenir.
interface BoundaryState { hasError: boolean; turn: ChatTurn }
class BubbleBoundary extends Component<{ turn: ChatTurn; children: ReactNode }, BoundaryState> {
  state: BoundaryState = { hasError: false, turn: this.props.turn };
  static getDerivedStateFromError(): Partial<BoundaryState> { return { hasError: true }; }
  static getDerivedStateFromProps(props: { turn: ChatTurn }, state: BoundaryState): Partial<BoundaryState> | null {
    if (props.turn !== state.turn) return { hasError: false, turn: props.turn };
    return null;
  }
  componentDidCatch(e: Error, _info: ErrorInfo) {
    if (typeof console !== 'undefined') console.warn('[cosre] cevap çizimi düz metne düştü:', e?.message);
  }
  render() {
    if (!this.state.hasError) return this.props.children;
    const t = this.props.turn;
    return (
      <div className={t.role === 'user' ? 'cm-msg cm-msg--user' : 'cm-msg cm-msg--ai'} data-fallback="1">
        <div className={t.role === 'user' ? 'cm-msg-user' : 'cm-msg-ai cm-md-p'}>{t.text ?? ''}</div>
      </div>
    );
  }
}

// v0.10.1137 inceleme — React.memo: akış sırasında yalnız SON tur değişir
// (useChatThread patchLast diğer turların kimliğini korur); tamamlanmış
// balonlar yeniden çizilmez, çözücü yalnız akan mesajda koşar.
export const ChatBubble = memo(function ChatBubble(props: ChatBubbleProps) {
  return <BubbleBoundary turn={props.turn}><ChatBubbleView {...props} /></BubbleBoundary>;
});

function ChatBubbleView({ turn, onRetry, stepsOpen, onRegenerate, busy, versions, onEdit }: ChatBubbleProps) {
  const effLinks = mergeBlockLinks(turn.links, turn.blocks); // v0.10.541 — link blokları çiplere katılır
  const isUser = turn.role === 'user';
  const navigate = useNavigate();
  const loc = useLocation(); // v0.10.542 — aksiyon görünürlüğü/uygulaması açık sayfaya göre
  const [copied, setCopied] = useState(false);
  // v0.10.161 — açık kanıt kimliği (d.i): çip şeridi ve şeffaflık paneli paylaşır.
  const [evId, setEvId] = useState<number | null>(null);
  // v0.9.419 — data-nav linkleri (trace id'ler, aynı-köken yollar) SPA içi
  // gider: tam sayfa yenilenmesi efemer chat'i sıfırlardı. v0.10.1105 — yalnız
  // DÜZ sol tık yakalanır. v0.10.1125 — /cosre'de (yeni sekme kipi) yakalanmaz.
  const newTab = useChatLinkNewTab();
  const linkProps = chatLinkTargetProps(newTab);
  const onBodyClick = useChatNavClick(newTab);
  // v0.10.1137 — kopya DÜZ METİN: markdown işaretleri düşer, linkler adresleriyle.
  const copy = () => {
    navigator.clipboard?.writeText(chatPlainText(turn.text ?? '')).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1400);
    }).catch(() => {});
  };
  const cites = useMemo(() => sourceChips(turn.sources), [turn.sources]);
  const mdCtx = useMemo<MdCtx>(() => {
    const origin = currentOrigin();
    return {
      policy: buildLinkPolicy({ allowedLinks: turn.allowedLinks, sources: turn.sources, links: effLinks, origin }),
      cites, newTab, origin,
    };
    // effLinks her render'da yeni dizi; kimliği turn.links/blocks'tan türer.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [turn.allowedLinks, turn.sources, turn.links, turn.blocks, cites, newTab]);
  // Çözüm + çizim metin/akış/blok kimliğine bağlı (chatMarkdown ayrıca önbellekli).
  const body = useMemo(
    () => (turn.text && !isUser ? renderMessage(turn.text, turn.pending, turn.blocks) : null),
    [turn.text, turn.pending, turn.blocks, isUser],
  );
  const done = !isUser && !turn.pending && !turn.error && !!turn.text;
  const hasSteps = !isUser && (!!turn.steps?.length || !!turn.stepDetails?.length);

  if (isUser) {
    // Operatörün mesajı: sağa yaslı kompakt hap; v0.10.1145'ten beri markdown (ChatMarkdown).
    // v0.10.1138 — son mesajda ✎ (üstüne gelince / dokunmatikte hep): satır-içi
    // düzenleyici; "Gönder" sonrasını kırpar ve yeniden koşar.
    return <UserBubble turn={turn} onEdit={onEdit} busy={busy} />;
  }

  return (
    <ChatMdContext.Provider value={mdCtx}>
    <div className="cm-msg cm-msg--ai">
      {/* v0.10.1137 — asistan cevabı kart DEĞİL: sayfadaki düz metin (Claude gibi),
          küçük marka etiketi. Explain paneli kendi .ai-answer-card'ını korur. */}
      <div className="cm-msg-ai__who" aria-hidden="true">
        <img src="/favicon.svg" width={14} height={14} alt="" draggable={false} /> CoSRE
      </div>
      <div onClick={onBodyClick} className="cm-msg-ai" style={{ wordBreak: 'break-word', overflowWrap: 'anywhere' }}>
        {/* v0.10.1137 — adımlar tek açılırda (akarken açık, bitince kapanır). */}
        {hasSteps && <StepsDisclosure turn={turn} evId={evId} setEvId={setEvId} initialOpen={stepsOpen} />}
        {turn.text ? (
          // `turn.pending` çözücüye GEÇİYOR: yarım tablo satırı / kapanmamış
          // çit kararı ona bağlı (chatMarkdown.ts); imleç de son metnin içinde.
          <>
            {body}
            {/* v0.10.541 — tipli chart blokları (mutlak pencere; CosreChart fromNs/toNs'i önceler) */}
            {!turn.pending && chartBlocks(turn.blocks).map((spec, i) => <CosreChart key={`blk-${i}`} spec={spec as CosreChartSpec} />)}
            {/* v0.10.558 — kök-neden rotasının yapısal kanıt kartı (operatör mockup onayı) */}
            {!turn.pending && evidenceBlocks(turn.blocks).map((ev, i) => <EvidenceCard key={`ev-${i}`} ev={ev} />)}
            {/* v0.10.688 — endpoint trace listesi: deterministik tablo (D6), satır = trace linki */}
            {!turn.pending && traceListBlocks(turn.blocks).map((tl, i) => <ChatTraceList key={`tl-${i}`} tl={tl} />)}
            {/* v0.10.542 — action bloğu: yalnız tool sonucundan (sunucu chat_actions.go),
                yalnız hedef sayfa açıkken; URL birleşimi + replace:true, yeni sekme yok. */}
            {!turn.pending && (turn.blocks ?? []).filter(b => b.type === 'action').map(b => {
              const a = parseAction(b.payload);
              if (!a || !actionVisible(a, loc.pathname)) return null;
              return (
                <div key={b.id} style={{ marginTop: 6 }}>
                  <Button variant="secondary" size="sm" title={a.href}
                    onClick={() => { const to = applyActionHref(a, loc.pathname, loc.search); if (to) navigate(to, { replace: true }); }}>
                    ⚡ {a.label}
                  </Button>
                </div>
              );
            })}
            {/* v0.10.63 — YARIM CEVAP TAM GİBİ OKUNMASIN (operatörün kararı, kırmızı değil). */}
            {turn.stopped && !turn.pending && (
              <div className="cm-msg-note">
                ⏹ Durduruldu — bu cevap <b>yarım</b> kaldı.
              </div>
            )}
          </>
        ) : null}
        {/* v0.10.649 — akış ortası `error` AKAN METNİ GİZLEMEZ; ⚠ altına iner. */}
        {turn.error && (
          <div style={{ marginTop: turn.text ? 6 : 0 }}><ChatErrorLine error={turn.error} isUser={false} /></div>
        )}
        {/* v0.10.650 — hata sonrası soru kaybolmasın: yalnız son hatalı turda (kap geçirir). */}
        {turn.error && !turn.pending && onRetry && (
          <div style={{ marginTop: 6 }}><Button variant="secondary" size="sm" onClick={onRetry}>↺ Yeniden dene</Button></div>
        )}
        {/* v0.10.1137 — ilk token gelene dek "Düşünüyor…" parıltısı. */}
        {!turn.text && !turn.error && turn.pending && (
          <span className="cm-thinking">Düşünüyor…</span>
        )}
      </div>

      {/* v0.10.1137 — Kaynaklar: numaralı liste ([n] atıflarıyla aynı numara). */}
      {!!turn.sources?.length && !turn.pending && !turn.error && cites.length > 0 && (
        <SourceList chips={cites} />
      )}

      {/* Derin-link çipleri (v0.9.419; v0.10.546 etiketli "Aç →" satırı). Dış
          URL <a target=_blank> (v0.9.709), iç link SPA <Link>. */}
      {!!effLinks.length && !turn.pending && !turn.error && (
        <div className="ai-links" role="group" aria-label="İlgili sayfalar">
          <span className="ai-links__cap">Aç →</span>
          {effLinks.map((l, i) => (
            /^https?:/i.test(l.href) ? (
              <a key={i} href={l.href} target="_blank" rel="noopener noreferrer" className="ai-link" title={l.href}>
                🔗 {l.label}
              </a>
            ) : (
              <Link key={i} to={l.href} className="ai-link" title={l.href} {...linkProps}>
                ↗ {l.label}
              </Link>
            )
          ))}
        </div>
      )}

      {/* Eylem satırı — kopyala + 👍/👎 (tamamlanmış cevap). v0.10.1137: üstüne
          gelince / odakta görünür, dokunmatikte hep görünür (globals.css). */}
      {done && (
        <div className="cm-msg-actions" role="toolbar" aria-label="Cevap eylemleri">
          <Button variant="ghost" size="sm" onClick={copy}
            title="Kopyala (düz metin)" aria-label="Cevabı kopyala"
            className={copied ? 'is-ok' : undefined}>
            {copied ? '✓ Kopyalandı' : '⧉ Kopyala'}
          </Button>
          {/* v0.10.537 — paylaşılan atom: 👎'de yorum kutusu (arşivden gelen
              turn exchangeId taşımaz → atom hiç çizilmez, chatPersist sözleşmesi). */}
          {!!turn.exchangeId && <AIFeedbackButtons exchangeId={turn.exchangeId} key={turn.exchangeId} />}
          {/* v0.10.1138 — yeniden üret: yalnız son cevapta, akarken kapalı. */}
          {onRegenerate && (
            <Button variant="ghost" size="sm" onClick={onRegenerate} disabled={busy}
              title="Aynı soruyu yeniden sor; bu cevap 'önceki cevap' olarak saklanır" aria-label="Cevabı yeniden üret">
              ↻ Yeniden üret
            </Button>
          )}
          {versions && versions.total > 1 && (
            <span className="cm-msg-versions" role="group" aria-label="Cevap sürümleri">
              <Button variant="ghost" size="sm" disabled={versions.index === 0 || busy}
                onClick={() => versions.onSelect(versions.index - 1)} aria-label="Önceki cevap">‹</Button>
              <span style={{ fontSize: 11, color: 'var(--text3)' }}>
                {versions.index < versions.total - 1 ? 'önceki cevap' : 'cevap'} ({versions.index + 1}/{versions.total})
              </span>
              <Button variant="ghost" size="sm" disabled={versions.index === versions.total - 1 || busy}
                onClick={() => versions.onSelect(versions.index + 1)} aria-label="Sonraki cevap">›</Button>
            </span>
          )}
        </div>
      )}
    </div>
    </ChatMdContext.Provider>
  );
}


// UserBubble — v0.10.1138: kullanıcı mesajı + (son mesajda) satır-içi düzenleyici.
function UserBubble({ turn, onEdit, busy }: { turn: ChatTurn; onEdit?: (text: string) => void; busy?: boolean }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(turn.text ?? '');
  if (editing && onEdit) {
    const submit = () => { const v = draft.trim(); if (!v || busy) return; setEditing(false); onEdit(v); };
    return (
      <div className="cm-msg cm-msg--user">
        <form className="cm-msg-edit" style={{ display: 'flex', flexDirection: 'column', gap: 6, width: '100%', maxWidth: 560 }}
          onSubmit={e => { e.preventDefault(); submit(); }}>
          <textarea value={draft} onChange={e => setDraft(e.target.value)} rows={3} aria-label="Mesajı düzenle" autoFocus
            onKeyDown={e => {
              if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); submit(); }
            }} />
          <div style={{ display: 'flex', gap: 6, justifyContent: 'flex-end' }}>
            <Button variant="secondary" size="sm" type="button" onClick={() => { setEditing(false); setDraft(turn.text ?? ''); }}>Vazgeç</Button>
            <Button variant="primary" size="sm" type="submit" disabled={!draft.trim() || busy}>Gönder</Button>
          </div>
        </form>
      </div>
    );
  }
  return (
    <div className="cm-msg cm-msg--user">
      {/* v0.10.1145 — kullanıcı turu da markdown (aynı çizici; dış link doğrulanmamış metin). */}
      <div className="cm-msg-user">
        {turn.error ? <ChatErrorLine error={turn.error} isUser /> : <ChatMarkdown text={turn.text ?? ''} />}
      </div>
      {onEdit && !turn.error && (
        <div className="cm-msg-actions cm-msg-actions--user" role="toolbar" aria-label="Mesaj eylemleri">
          <Button variant="ghost" size="sm" disabled={busy} onClick={() => { setDraft(turn.text ?? ''); setEditing(true); }}
            title="Mesajı düzenle — sonrası silinir ve yeniden sorulur" aria-label="Mesajı düzenle">✎ Düzenle</Button>
        </div>
      )}
    </div>
  );
}

/**
 * ChatErrorLine — v0.10.22 — HAM SAĞLAYICI METNİ DEĞİL. Operatör
 * `dial tcp …: connection refused` blob'undan ne yapacağını çıkaramıyordu;
 * aiErrorHint v0.9.200'den beri duruyor ama yalnız AIAnalysisPanel
 * kullanıyordu. Ham metin SİLİNMİYOR, tooltip'e iniyor (chatErrorText.ts).
 * v0.10.649'da bileşene çıkarıldı: metinle birlikte de çizilir.
 */
function ChatErrorLine({ error, isUser }: { error: string; isUser: boolean }) {
  const ev = chatErrorText(error);
  return (
    <span style={{ color: isUser ? 'var(--on-accent)' : 'var(--err)' }} title={ev.raw ?? undefined}>
      ⚠ {ev.text}
    </span>
  );
}
