import { useEffect, useState } from 'react';
import { useBranding } from './branding';
import { getRaw, setRaw, removeRaw } from './storage';

// Tiny in-repo i18n. No external dependency — the SPA's
// branding settings carry a `language` field that picks one of
// the catalogs below. English is the source of truth + the
// default; Turkish covers the high-traffic surfaces (sidebar,
// login, common buttons, page titles, empty/error states).
//
// Catalog covers ~80% of what an operator sees during morning
// triage. Strings that aren't in the catalog fall back to the
// English key verbatim so a missing translation never renders
// blank.
export type Lang = 'en' | 'tr';

type Catalog = Record<string, string>;

const EN: Catalog = {
  // Sidebar group headings
  'navGroup.triage':      'Triage',
  'navGroup.services':    'Services',
  'navGroup.signals':     'Signals',
  'navGroup.workspaces':  'Workspaces',
  'navGroup.alerting':    'Alerting',
  'navGroup.system':      'System',
  'navGroup.management':  'Management',
  'navGroup.community':   'Community',
  // v0.10.1340 — kabuk atlama bağlantısı (AppShell SkipToContent)
  'shell.skipToContent': 'Skip to content',
  // v0.10.1340 END

  // Sidebar
  // v0.9.323 — triage merge. The merged queue takes the name operators
  // already use for "the thing I work from"; the per-source pages it
  // aggregates are drill-downs now, not competing queues. 'nav.inbox' keeps
  // its key (the route is unchanged) so saved views, notification deep links
  // and dashboard markdown all keep working — only the LABEL moved.
  'nav.inbox':       'Problems',
  'nav.shift':       'Shift summary',
  'nav.incidents':   'Incidents',
  'nav.problems':    'Exceptions/Errors', // v0.10.1107 (operatör) — Oracle hata grupları da burada; menüde iki ad
  'nav.anomalies':   'Anomalies',
  'nav.rollouts':    'Deployment/Rollouts', // v0.10.201 — Deployment Report → Rollouts; v0.10.984 (Rollouts v2 P2.3) — brief adı
  'nav.services':    'Services',
  'nav.endpoints':   'Endpoints',
  'nav.databases':   'Databases',
  'nav.clusters':    'Clusters',
  'nav.messaging':   'Messaging',
  'nav.external':    'External APIs',
  'nav.hosts':       'Hosts',
  'nav.traces':      'Traces',
  'nav.metrics':     'Metrics',
  'nav.logs':        'Logs',
  // v0.10.1335 — birleşik arama sayfası (/search). {q}/{n}/{src}/{reason}
  // yer tutucuları çağıranda doldurulur (pages/search/searchText.ts).
  'nav.search':      'Search',
  'search.placeholder':   'Search services, endpoints, problems, exceptions, traces, logs…',
  'search.minChars':      'Type at least 2 characters',
  'search.minCharsBody':  'One query searches services, endpoints, problems, exceptions, traces and logs in the selected time range.',
  'search.loading':       'Searching…',
  'search.error':         'Search failed',
  'search.noMatch':       'No matches for “{q}”',
  'search.noMatchBody':   'Try a shorter query, a wider time range, or paste a trace id.',
  'search.hidden':        'No results with current filters',
  'search.hiddenN':       '{n} hidden',
  'search.clearAll':      'Clear all',
  'search.results':       '{n} results',
  'search.sort':          'Sort',
  'search.sort.relevance': 'Relevance',
  'search.sort.newest':   'Newest',
  'search.sort.oldest':   'Oldest',
  'search.sort.severity': 'Severity',
  'search.group':         'Group',
  'search.group.list':    'List',
  'search.group.type':    'By type',
  'search.facet.type':    'Type',
  'search.facet.service': 'Service',
  'search.facet.env':     'Environment',
  'search.facet.sev':     'Severity ≥',
  'search.facet.status':  'Problems',
  'search.facet.scope':   'Service, environment and severity counts: within shown results',
  'search.status.open':   'Open',
  'search.status.all':    'All',
  'search.type.service':  'Service',
  'search.type.endpoint': 'Endpoint',
  'search.type.problem':  'Problem',
  'search.type.exception': 'Exception',
  'search.type.trace':    'Trace',
  'search.type.log':      'Log',
  'search.src.unavailable': '{src} unavailable ({reason})',
  'search.src.needsService': '{src}: needs a service or trace id',
  'search.src.skipped':   '{src} skipped ({reason})',
  'search.open.trace':    'Open in Traces',
  'search.open.log':      'Open in Logs',
  'search.open.problem':  'Open in Problems',
  'search.open.exception': 'Open in Exceptions',
  'search.open.service':  'Open in Service',
  'search.open.endpoint': 'Open operation traces',
  'search.askCosre':      'Ask CoSRE',
  'search.seeAll':        'See all results for “{q}” →',
  'nav.explore':     'Explore',
  'nav.runbooks':    'Runbooks',
  'nav.dashboards':  'Dashboards',
  'nav.profiling':   'Profiling',
  'nav.ai':          'AI insights',
  'nav.alerts':      'Alerts',
  'nav.watchers':    'Watchers',
  'nav.topology':    'Topology',
  'nav.slos':        'SLOs',
  'nav.monitors':    'Monitors', // kalır: internal/api/pages.go sayfa defteri bu anahtarla etiketler (RolesTab t(p.label))
  'nav.events':      'Events',
  'nav.system':      'System',
  // v0.10.1426 — nav.analysis / serviceMap / clickhouse / elastic / cardinality /
  // cluster / catalog / audit / sql / query / statusPage silindi: Sidebar,
  // CommandPalette ve pages.go hiçbirini okumuyordu.

  // Login
  'login.signIn':      'Sign in',
  'login.signingIn':   'Signing in…',
  'login.email':       'Email',
  'login.password':    'Password',
  'login.usernameOrEmail': 'Username or email',
  'login.signInWith':  'Sign in with',
  'login.orLocal':     'or sign in locally',
  'login.signInToContinue': 'Sign in to continue',
  'login.invalid':     'Invalid username or password',
  'login.invalidHint': 'If you pasted from a document, a hyphen-minus (-) may have been replaced with a similar character (–, —, hidden whitespace). Try typing the password by hand.',

  // v0.10.1426 — the generic btn.* / state.* / label.* / sev.* keys were
  // removed: no surface ever read them (every button / label / badge carries
  // its own literal or a surface-specific key).

  // User menu
  'user.signOut':         'Sign out',
  'user.changePassword':  'Change password',
  'user.manageUsers':     'Manage users',
  'user.settings':        'Settings',

  // Time range picker (Grafana-parity panel)
  'trp.quickRanges':   'Quick ranges',
  'trp.absoluteRange': 'Absolute time range',
  'trp.from':          'From',
  'trp.to':            'To',
  'trp.applyRange':    'Apply time range',
  'trp.recentRanges':  'Recently used',
  'trp.browserTime':   'Browser time',
  'trp.zoomOut':       'Zoom out (2×)',
  // v0.10.1430 — global auto-refresh picker (Topbar, next to the time picker)
  'rp.label':          'Auto-refresh',
  'rp.off':            'Off',
  'rp.manualOnly':     'Off · manual',
  'rp.manualReason':   'Auto-refresh runs on metric and overview pages (Services, Service, Endpoints, Metrics, Dashboard, Service map, Hosts, Clusters). Here use ↻.',
  'rp.every':          'Auto-refresh every {t} — relative windows slide, hidden tabs pause.',
  'rp.offReason':      'Auto-refresh is off — use ↻ or pick an interval.',
  'rp.refreshNow':     'Refresh now',
  'rp.refreshed':      'Refreshed',
  'rp.pausedAbsolute': 'paused · absolute range',
  'rp.pausedEdit':     'paused · editing',
  'rp.updatedAgo':     'updated {ago} ago',
  'rp.ago.s':          '{n} s',
  'rp.ago.m':          '{n} min',
  'trp.prevMonth':     'Previous month',
  'trp.nextMonth':     'Next month',
  'trp.errFrom':       'Invalid "From" — use YYYY-MM-DD HH:mm:ss',
  'trp.errTo':         'Invalid "To" — use YYYY-MM-DD HH:mm:ss',
  'trp.errOrder':      '"To" must be after "From"',
  'trp.errMax':        'Range too large (max 1 year)',

  // Quick-range labels (keys mirror PRESET_SECONDS in lib/utils.ts)
  'range.5m':  'Last 5 minutes',
  'range.15m': 'Last 15 minutes',
  'range.30m': 'Last 30 minutes',
  'range.1h':  'Last 1 hour',
  'range.3h':  'Last 3 hours',
  'range.6h':  'Last 6 hours',
  'range.12h': 'Last 12 hours',
  'range.24h': 'Last 24 hours',
  'range.2d':  'Last 2 days',
  'range.7d':  'Last 7 days',
  'range.30d': 'Last 30 days',

  // v0.10.944 (operatör) — trace sayfasının ✨ Explain düğmesi "CoSRE'ye sor"
  // adını aldı. Görünen ad + ipucu yalnız ARAYÜZ metni: ?ai=trace:<id>,
  // explain-trace yüzeyi ve AIExplainButton aynı kalır.
  // v0.10.1461 — composer "Kod" hapı (R2, bağımsız sohbette kod turu)
  'ai.codeTurn':            'Code',
  'ai.codeTurnHint':        'Read source code this turn — goes only to the self-hosted model; external tools are off for the turn.',
  'ai.codeTurnUnavailable': 'Code reading is unavailable',
  // v0.10.1465 — kod okunmuş konuşmanın kilidi (chat_code_sticky.go)
  'ai.codeTurnLocked':      'Code was read in this conversation — it stays on the self-hosted model; use Clear for a new conversation',
  'ai.askCosre':          'Ask CoSRE',
  // v0.10.1435 — panelden "Bu grafiği açıkla"
  'ai.askCosreMetricPanelHint': 'Explain this chart in the CoSRE chat (brushed range and selected series included)',
  // v0.10.1435 END
  'ai.askCosreTraceHint': 'Ask CoSRE about this trace.',
  // v0.10.1338 — span düğmesi Trace sayfasında trace düğmesiyle AYNI adı
  // taşır; ikisini ipucu ayırır.
  'ai.askCosreSpanHint':  'Ask CoSRE · span — explain this span.',
  // v0.10.1338 (operatör) — log satırı / log artışı düğmeleri de "Ask CoSRE"; ipucu ayırır.
  'ai.askCosreLogHint':      'Ask CoSRE · log — explain this log line.',
  'ai.askCosreLogSpikeHint': 'Ask CoSRE · log spike — explain this log increase.',
  // v0.10.1367 — Endpoint detay / arama endpoint sonucu: tek tık "Ask CoSRE".
  'ai.askCosreEndpointHint': 'Ask CoSRE · endpoint — what is happening, since when, where the time goes, which errors.',
  // v0.10.1525 — DB span → Databases / ifade detayı (SpanDetail) + ifade listesi adı
  'dbspan.openDatabase':       'Open in Databases',
  'dbspan.openDatabaseHint':   'Open this database on the Databases page (span window)',
  'dbspan.statementDetail':    'Statement detail',
  'dbspan.statementDetailHint': 'Open this statement class (normalised fingerprint) — calls, errors, latency, callers',
  'dbspan.engineHint':         'All databases of this engine on the Databases page',
  'dbspan.tableHint':          'Statements touching this table (statement list, search)',
  'stmt.allStatements':        'All statements',

  // v0.10.944 (CoSRE Faz A) — çekmecenin "Bağlam" şeridi: sohbetin hangi
  // trace/span/servis/ortam/cluster/namespace ve pencereye kapsandığı.
  'ai.ctx.label':     'Context',
  'ai.ctx.aria':      'CoSRE conversation context',
  'ai.ctx.trace':     'Trace',
  'ai.ctx.span':      'Span',
  'ai.ctx.service':   'Service',
  'ai.ctx.env':       'Env',
  'ai.ctx.clusterNs': 'Cluster / namespace',
  'ai.ctx.window':    'Window',

  // v0.10.1128 — empty CoSRE chat: "what I can do" hint (drawer + /cosre).
  // `|` in a prompt marks where the caret lands (the user types the name).
  'cosre.cap.aria':             'What CoSRE can do',
  'cosre.cap.tryHint':          'Fills the box with an example — edit it, then send',
  'cosre.cap.wiki':             'I can search the company wiki — e.g. “@wiki BSA cache refresh most common errors”', // v0.10.1368
  'cosre.cap.wiki.prompt':      '@wiki BSA cache refresh most common errors', // v0.10.1368
  'cosre.cap.service':          'I can look at a service’s traces, errors and latency — type the service name (@ completes it)',
  'cosre.cap.service.prompt':   '@| traces, errors and latency in the last hour?',
  'cosre.cap.operation':        'I can dig into failing technical operations — type the operation name',
  'cosre.cap.operation.prompt': 'Why is the | operation failing?',
  // v0.10.1382 — data-grounded choices under the rows (/api/copilot/starters).
  // v0.10.1404 — the P1 row became the latest-exceptions row.
  'cosre.cap.exceptions':        'I can look at your team’s latest exceptions', // v0.10.1472
  'cosre.cap.exceptions.prompt': 'My team’s exceptions?',
  'cosre.cap.exceptions.searchTitle': 'Pick a team — opens the Exceptions page filtered by that team', // v0.10.1515
  'cosre.cap.choices':          'Ready-made choices',
  'cosre.cap.search.service':   'Search services…', // v0.10.1390
  'cosre.cap.search.operation': 'Search operations…', // v0.10.1390
  // v0.10.1402 — Takımım kartı (en üstte): takım seçici + üç eylem çipi.
  'cosre.cap.team':             'My team — I can check your team’s services and exceptions; pick another team',
  'cosre.cap.team.prompt':      'How are the services of team |?',
  'cosre.cap.search.team':      'Search teams…',
  'cosre.cap.team.searchTitle': 'Pick a team — CoSRE answers for that team right away',
  'cosre.team.ask.services':    'How are the services?',
  'cosre.team.ask.exceptions':  'Exceptions?',
  'cosre.team.ask.topErrors':   'Most failing services?',
  'cosre.chip.wiki':            'Search the wiki: …',
  'cosre.chip.wiki.prompt':     '@wiki ', // v0.10.1378 — kapsam belirteci, dilden bağımsız

  // v0.10.1119 — kök-neden paneli: ayrı bubbleUp isteği düştü (≠ "ayrışma yok").
  'rootCause.bubbleUnavailable': 'Attribute comparison unavailable — the span scan failed or timed out. This does not mean no dimension stood out.',

  // v0.10.1495 — kök-neden paneli: kıyas bölümü problem türünden; bağlantısız
  // co-mover'lar ayrı grup; etkilenen varlıklar yalnız doğrulanmış bağ; alarm
  // grafiğinin kaynağı / minSamples boşlukları.
  'rootCause.bubble.errors.title':  'Where errors concentrate',
  'rootCause.bubble.errors.sub':    'top attribute over the error spans',
  'rootCause.bubble.errors.legend': 'bar = % of error spans with this value · right = error% / baseline%',
  'rootCause.bubble.window.title':  'What stands out in this window',
  'rootCause.bubble.window.sub':    'top attribute vs the previous window of the same length',
  'rootCause.bubble.window.legend': 'bar = % of spans in the window with this value · right = window% / previous window%',
  'rootCause.blast.relation':       'services that call this one (span call edges); the error column is what they see — 0% means linked but not affected',
  'rootCause.coMovers.title':       'Moved in the same window (link not verified)',
  'rootCause.coMovers.note':        'no topology edge to this service — time correlation only, not a cause candidate',
  'rootCause.coMovers.topoUnknown': 'topology unavailable — links could not be checked, time correlation only',
  'affected.title':                 'Affected entities',
  'affected.empty.service':         'no verified link with observed impact in the window',
  'affected.empty.subject':         'subject is not a service',
  'affected.error':                 'Affected entities could not be read.',
  'affected.linked':                'Linked, no impact observed',
  'affected.linked.note':           'these services call this one but saw no errors in the window — not counted as affected',
  'affected.topoUnknown':           'Topology (callers) unavailable — only what can be verified is listed.',
  'affected.podScoped':             'Pod-scoped problem: call edges are service-level, the pod a call reached cannot be told apart.',
  'affected.clustersUnverified':    'The service runs in several clusters — the pod’s cluster could not be verified, none listed.',
  'affected.rel.caller':            'caller',
  'affected.rel.subject-pod':       'problem pod',
  'affected.rel.evidence-pod':      'pod (evidence)',
  'affected.rel.cluster':           'cluster',
  'alertChart.belowFloor':          'points below the rule’s minimum sample count — the evaluator did not measure there (gap)',
  'alertChart.sourceUnknown':       'source: unknown — the server did not report the series source',

  // v0.10.1120 — Settings > SSO: trust unverified email (email_verified=false).
  'sso.trustEmail.label':        'Trust unverified email (not recommended)',
  'sso.trustEmail.warning':      'Logins with email_verified=false are accepted, but only from the allowed domains. Risk: account takeover through an email collision — use only if the corporate IdP does not let users set or change their own email (no self-registration, no email editing, no social/brokered IdPs) and allowed domains are set. Admin and local/LDAP accounts are never linked this way (unless the LDAP account link option below is on). Preferred fix is on the IdP side (Keycloak "Trust Email").',
  'sso.trustEmail.needsDomains': 'Set allowed domains first — unverified email cannot be trusted while the list is empty.',
  // v0.10.1403 — doğrulanmamış e-postalı SSO girişini mevcut LDAP hesabına kullanıcı adıyla bağla.
  'sso.ldapLink.label':          'Link existing LDAP accounts to SSO by username',
  'sso.ldapLink.note':           'For moving from LDAP login to SSO only. A login with an unverified email opens an existing LDAP account only if the username claim in the signed id_token equals the LDAP username Coremetry stored at that user\'s last LDAP login, the email equals the account\'s email, and exactly one active account has that LDAP username. The account then becomes an SSO account (audited). Each user needs at least one earlier LDAP login. Local/password accounts are never linked. Requires "Trust unverified email".',
  'sso.ldapLink.warning':        'Use only if users cannot change their username or email on the IdP (Keycloak LDAP federation read-only, "Edit username" off, no self-registration, no brokered/social IdPs) — whoever can set both opens that account.',
  'sso.ldapLinkAdmin.label':     'Also link admin accounts this way (not recommended)',
  'sso.ldapLinkAdmin.warning':   'An admin account becomes reachable by whoever controls this username and email on the IdP; the check is repeated on every admin login. Leave off unless admins must move to SSO.',

  // v0.10.1121 — Settings > SSO: match by username when the IdP sends no email.
  'sso.usernameFallback.label':   'Match by username when email is missing (AD/LDAP)',
  'sso.usernameFallback.on':      'On: if neither the id_token nor UserInfo carries an email, the username claim from the verified id_token is looked up in AD/LDAP (exact match) and the directory email is used; without LDAP (or no email in the directory) the user is matched to an existing Coremetry user by LDAP username. No new user is created without an email. Allowed domains still apply.',
  'sso.usernameFallback.off':     'Off (default): if neither the id_token nor UserInfo carries an email, the login is rejected (email_missing). UserInfo is always tried. Preferred fix on the IdP: Keycloak LDAP federation mapper mail → email, and the email client scope as Default with "Add to ID token".',
  'sso.usernameFallback.risk':    'Use only if users cannot change this claim on the IdP (LDAP federation read-only, username editing off). Unsafe with brokered (external/social) IdPs, self-registration or an editable username — anyone who can pick the username can open that account. Admin accounts are not opened this way unless explicitly allowed below.',
  'sso.usernameAdmin.label':      'Also allow admin accounts via username matching (not recommended)',
  'sso.usernameAdmin.warning':    'An admin account becomes reachable by whoever controls this username on the IdP. Leave off; admins can sign in with password/LDAP.',
  'sso.usernameClaim.label':      'Username claim',
  'sso.usernameClaim.hint':       'Only this claim is read (default preferred_username = AD sAMAccountName). Letters, digits and _ - . : ; profile/email claims are not allowed.',

  // v0.10.1334 — in-app guided tour (features/tour), release R1: chrome,
  // steps 1–2, declared-but-disabled steps 3–5, notes.
  'tour.chrome.dialog':     'Guided tour',
  'tour.chrome.stepOf':     'Step {n} of {total}',
  'tour.chrome.progress':   'Tour progress',
  'tour.chrome.back':       'Back',
  'tour.chrome.next':       'Next',
  'tour.chrome.finish':     'Finish',
  'tour.chrome.skip':       'Skip tour',
  'tour.chrome.restart':    'Restart tour',
  'tour.chrome.loading':    'Looking for this part of the page…',
  'tour.note.timeout':      'No data yet — this part of the page has not rendered. You can still continue.',
  'tour.note.not_yet':      'Coming in the next release.',
  'tour.note.no_services':  'No services reporting yet — the tour becomes meaningful once OTLP data arrives.',
  'tour.welcome.title':     'Welcome — a two-minute tour',
  'tour.welcome.body':      'This is what this installation sees right now, over the last 5 minutes. Next we walk through the Services list, where most investigations start.',
  'tour.welcome.unavailable': 'Live numbers could not be loaded — the tour still works.',
  'tour.welcome.tile.services': 'Services',
  'tour.welcome.tile.spans':    'Spans · 5 min',
  'tour.welcome.tile.spansTop': 'Spans · 5 min · top 50',
  'tour.welcome.tile.busiest':  'Busiest service',
  'tour.welcome.tile.envs':     'Environments',
  'tour.services.title':    'Services: your starting point',
  'tour.services.body':     'Each row is one service sending OTLP spans. The dot before the name is its health: amber when the error rate is above 1 % (or a warning/info problem is open), red above 5 % (or a critical problem is open). Spans, Error rate and P99 cover the selected time range; turn on "Δ prior" to compare with the previous window. Click a row to open the service.',
  // v0.10.1336 — guided tour R2: steps 3–5 (operations / traces / cosre) + sub-steps.
  'tour.operations.title':  'Operations: where the time goes',
  'tour.operations.header': 'This is the page of the busiest service. The breadcrumb above the name goes back to Services with the same time window, ☆ pins the service to Cmd-K quick access, and these buttons jump to its backtrace, logs, endpoints, problems, anomalies and traces — the service and the window come along.',
  'tour.operations.tab':    'The Operations tab lists every operation (span name) of this service. The tab lives in the URL (?tab=operations), so a shared link opens right here.',
  'tour.operations.impact': 'Rows are ranked by Impact = average duration × call count: the operation that costs your users the most total time comes first — not merely the slowest one. Raw shows span names as they arrive; Normalized (top right) collapses id-bearing names into shapes such as GET /users/:id.',
  'tour.operations.body':   'Each row is one operation with its trend, calls, error rate and latency percentiles. Click a row to open that operation\'s traces — exactly where the next step goes.',
  'tour.traces.title':      'Traces: the slow and failing requests',
  'tour.traces.filters':    'We opened this service\'s traces with the status_code = error quick chip applied. Active filters sit in this bar (click one to edit, ✕ removes it); the quick row below ("Hızlı:") adds the common ones — errors, server spans, POST — in one click.',
  'tour.traces.minms':      'Min ms = 500 hides fast requests, so only slow and failing traces remain. The service picker on the left scopes the list; after changing a field press Search (or Enter).',
  'tour.traces.body':       'Each row is one trace — a single request across services. Click a row to open its waterfall: every span, its timing and its attributes.',
  'tour.cosre.title':       'CoSRE: ask in plain language',
  'tour.cosre.fab':         'This button opens CoSRE, the assistant that reads your telemetry. It knows the page you are on — on a service or a trace it starts from that context. A red badge counts open critical problems.',
  'tour.cosre.body':        'We drafted a question about this service — nothing has been sent. Press Finish, edit it if you like, then send it with Enter. Esc closes the chat drawer first; a second Esc skips the tour.',
  'tour.cosre.prompt':      'Why is {svc} slow?',
  'tour.cosre.promptFleet': 'What should I look at first right now?',
  'tour.note.copilot_disabled': 'CoSRE is turned off on this installation — an admin enables it in Settings › CoSRE.',
  // v0.10.1336 END
  // v0.10.1434 — Settings › CoSRE › Toolsets (AiToolsetsPanel): the 74 tools grouped by data source
  'aiToolsets.tab':          'Source toolsets',
  'aiToolsets.title':        'AI source toolsets',
  'aiToolsets.intro':        'The {n} CoSRE / MCP tools grouped by the data source they read. A disabled toolset\'s tools are no longer offered to the in-app chat, and external MCP calls to them are refused with a named reason. Core telemetry (services, traces, problems) and meta cannot be disabled. Health is derived from configuration — no queries, plus a cached liveness probe for logs.',
  'aiToolsets.col.toolset':  'Toolset',
  'aiToolsets.col.source':   'Source',
  'aiToolsets.col.tools':    'Tools',
  'aiToolsets.col.health':   'Health',
  'aiToolsets.col.status':   'Status',
  'aiToolsets.on':           'On',
  'aiToolsets.off':          'Off',
  'aiToolsets.required':     'required',
  'aiToolsets.enable':       'Enable',
  'aiToolsets.disable':      'Disable',
  'aiToolsets.readOnly':     'Read-only: only admins can enable or disable toolsets.',
  'aiToolsets.lastChange':   'Last change: {by} · {at}',
  'aiToolsets.saved':        'Toolset {name}: {state}',
  'aiToolsets.saveFailed':   'Could not save toolsets: {err}',
  'aiToolsets.loadFailed':   'Toolsets could not be loaded',
  'aiToolsets.empty':        'No toolsets reported by the server',
  'aiToolsets.confirmTitle': 'Disable toolset {name}?',
  'aiToolsets.confirmBody':  'Its {n} tools will no longer be offered to CoSRE chat and external MCP calls to them will be refused. You can re-enable it at any time.',
  'aiToolsets.health.ok':             'healthy',
  'aiToolsets.health.partial':        'partial',
  'aiToolsets.health.not_configured': 'not configured',
  'aiToolsets.health.unreachable':    'unreachable',
  'aiToolsets.health.unauthorized':   'unauthorized',
  'aiToolsets.health.timeout':        'timeout',
  'aiToolsets.health.error':          'error',
  'aiToolsets.src.services':    'ClickHouse service / operation / topology / SLO rollups',
  'aiToolsets.src.traces':      'ClickHouse spans / trace rollups',
  'aiToolsets.src.problems':    'Problems, anomalies, root-cause hypotheses, exception groups, incident cases',
  'aiToolsets.src.changes':     'Deploy events, service versions, workload rollouts',
  'aiToolsets.src.metrics':     'VictoriaMetrics / ClickHouse metric points / Thanos',
  'aiToolsets.src.logs':        'Log store (Elasticsearch or ClickHouse)',
  'aiToolsets.src.k8s':         'Entity catalogue (namespaces, workloads, pods)',
  'aiToolsets.src.oracle':      'Oracle error log copy',
  'aiToolsets.src.devops-code': 'Azure DevOps code search and repositories',
  'aiToolsets.src.devops-wiki': 'Azure DevOps wiki',
  'aiToolsets.src.knowledge':   'Runbooks, document RAG, static product guide',
  'aiToolsets.src.meta':        'Chat state and pure helpers',
  // v0.10.1434 END
  // v0.10.1458 — Settings › CoSRE › provider tab (AiGeneralQuestionsPanel): off-topic questions toggle
  'aiGeneralQ.title':      'General questions',
  'aiGeneralQ.label':      'Answer general questions (non-telemetry questions are answered by the model; when off: a polite boundary + directions)',
  'aiGeneralQ.hint':       'Questions unrelated to telemetry (e.g. "What is the capital of Türkiye?") get a short general-knowledge answer from the model, marked as not coming from Coremetry telemetry, with the usual CoSRE suggestions below. No tools, telemetry or documents are used. Harmful requests are always refused.',
  'aiSourceDisc.label':    'Automatic source discovery (search uploaded tables, wiki, telemetry and Exceptions together; strongest evidence answers)',
  'aiSourceDisc.hint':     'Before answering, CoSRE probes every source in parallel without the model. The source with the strongest evidence answers; other sources with evidence appear as chips below the answer (/excel, /wiki, /telemetri, /exceptions). When two sources are equally strong it asks which one to use. Off: the previous fixed order.',
  'aiSourceDisc.saved':    'Source discovery: {state}',
  'aiGeneralQ.readOnly':   'Read-only: only admins can change this setting.',
  'aiGeneralQ.lastChange': 'Last change: {by} · {at}',
  'aiGeneralQ.saved':      'General questions: {state}',
  'aiGeneralQ.on':         'on',
  'aiGeneralQ.off':        'off',
  'aiGeneralQ.saveFailed': 'Could not save: {err}',
  'aiGeneralQ.loadFailed': 'Setting could not be loaded: {err}',
  // v0.10.1458 END
  // v0.10.1443 — sohbetten dashboard (components/ai/AddToDashboard.tsx, PinToDashboardModal)
  'dash.addFromChat.one':          'Add to dashboard',
  'dash.addFromChat.all':          'Make a dashboard from all',
  'dash.addFromChat.title':        'Add to dashboard',
  'dash.addFromChat.submit':       'Add',
  'dash.addFromChat.viewerReason': 'Adding to dashboards needs the editor or admin role.',
  'dash.addFromChat.added':        '{n} panel(s) added',
  'dash.addFromChat.panels':       '{n} panels',
  'dash.addFromChat.hint':         'Nothing is saved until you click Add. Panels already on the dashboard are skipped; dashboards are shared.',
  'dash.addFromChat.skip.duplicate': 'already on the dashboard',
  'dash.addFromChat.skip.invalid':   'query rejected',
  'dash.addFromChat.skip.cap':       'dashboard panel limit reached',
  'dash.addFromChat.skip.size':      'dashboard size limit reached',
  // v0.10.1443 END
  // v0.10.1477 — /logs takım süzgeci ("Takımım" varsayılanı)
  'logs.team.mine':           'My team',
  'logs.team.label':          'Team',
  'logs.team.change':         'Change',
  'logs.team.pick':           'Pick a team…',
  'logs.team.remove':         'Remove team filter',
  'logs.team.removeTitle':    'Remove the team filter (your team is no longer applied automatically on this browser; use “My team” to turn it back on)',
  'logs.team.applyTitle':     'Filter logs to your team (team label fields from Settings → Elasticsearch)',
  'logs.team.chipTitle':      'Exact match on the configured team field(s), every spelling of this team',
  'logs.team.unapplied':      'team filter not applied — no team field on this log source',
  'logs.team.unappliedTitle': 'The team filter could not be applied: the log backend is ClickHouse or no Team field is set in Settings → Elasticsearch. The rows below are NOT filtered by team.',
  // v0.10.1491 — operasyon kartı (R3 trace yarısı, /endpoint "Kod ve çağrılar")
  'endpoint.opcard.title':      'Code & calls',
  'endpoint.opcard.sub':        'sample · {n} traces',
  'endpoint.opcard.slowest':    'slowest',
  'endpoint.opcard.random':     'random',
  'endpoint.opcard.colTarget':  'Target',
  'endpoint.opcard.colOp':      'Operation',
  'endpoint.opcard.colProtocol': 'Protocol',
  'endpoint.opcard.colCalls':   'Calls',
  'endpoint.opcard.colErr':     'Err %',
  'endpoint.opcard.colSamples': 'Sample traces',
  'endpoint.opcard.error':      'Call-tree query failed — the sections above are still valid.',
  'endpoint.opcard.noSample':   'No trace of this operation in the window — widen the time range or clear the env / cluster filter.',
  'endpoint.opcard.empty':      'No downstream call seen in this window — the operation is a leaf, or its children are not instrumented.',
  'endpoint.opcard.sampleNote': 'Calls, error % and P95 are over the sampled traces, not the whole window. Values (SQL text, URLs, payloads) are never read.',
  'endpoint.opcard.others':     '+{n} more rows not shown (row cap 25).',
  'endpoint.opcard.truncated':  'Some sampled traces were too large to walk completely.',
  'endpoint.opcard.clamped':    'Window clamped to the last 24 hours for this section.',
  'endpoint.opcard.window':     'Whole window (all traces, service→service calls)',
  'endpoint.opcard.opCode':     'Operation code (span attribute)',
  'endpoint.opcard.version':    'Running version (majority of sampled entry spans)',
  'endpoint.opcard.code.title':  'Code',
  'endpoint.opcard.code.hint':   'Entry method, request / response fields and call sites from the repository (Azure DevOps) — read on open; code bodies never reach the browser.',
  'endpoint.opcard.code.entry':  'Entry method',
  'endpoint.opcard.code.request': 'Request fields',
  'endpoint.opcard.code.response': 'Response fields',
  'endpoint.opcard.code.noFields': 'No application type (or not found in the repository).',
  'endpoint.opcard.code.colLoc':  'Location',
  'endpoint.opcard.code.colKind': 'Kind',
  'endpoint.opcard.code.colCallee': 'Call',
  'endpoint.opcard.code.colTarget': 'Literal target',
  'endpoint.opcard.code.colMatch': 'Matched call (trace)',
  'endpoint.opcard.code.unmatched': 'no match',
  'endpoint.opcard.code.unmatchedHint': 'No trace row could be matched unambiguously — the call may not run on this path, or several rows fit.',
  'endpoint.opcard.code.focusRow': 'Show this row in the call tree',
  'endpoint.opcard.code.noCallSites': 'No HTTP / gRPC / DB / messaging call site found in the entry method or its callees.',
  'endpoint.opcard.code.error':  'Code half could not be loaded — the call tree above is still valid.',
  'endpoint.opcard.code.searchOff': 'Code Search is required to find the entry method (Settings → Code integration).',
  'endpoint.opcard.code.noEntry': 'Entry method not found in the repository.',
  'endpoint.opcard.usages.title': 'Who uses this method? — {m}',
  'endpoint.opcard.usages.possible': 'possible',
  'endpoint.opcard.usages.possibleHint': 'Code Search ref: matches by NAME, not type — a different method with the same name also matches.',
  'endpoint.opcard.usages.caller': 'trace caller',
  'endpoint.opcard.usages.callerHint': 'This repository belongs to a service that calls this operation in the traces.',
  'endpoint.opcard.usages.more': '+{n} more not shown (cap 10).',
  'endpoint.opcard.usages.none': 'No other usage found by name.',
  'endpoint.opcard.usages.failed': 'Usage search did not run.',
  'endpoint.opcard.code.matchNote': 'Matches are heuristic: same kind + the literal route / topic / Feign client name in code, or the only call of its kind. Field names and types only — no values.',
  // v0.10.1514 — trace başlığı (A): ölçü kümesi + kimlik satırı.
  'trace.hdr.summary':     'Trace summary',
  'trace.hdr.identity':    'Business identity',
  'trace.hdr.traceStatus': 'Trace id and status',
  'trace.hdr.traceId':     'Trace ID',
  'trace.hdr.measures':    'Trace measures',
  'trace.hdr.duration':    'DURATION',
  'trace.hdr.critical':    'CRITICAL PATH',
  'trace.hdr.spans':       'SPANS',
  'trace.hdr.start':       'START',
  'trace.hdr.serviceOne':  '{n} service',
  'trace.hdr.serviceMany': '{n} services',
  'endpoint.opcard.chip':       'Operation card',
  'endpoint.opcard.chipHint':   'Open this operation\'s call tree (code & calls) on the endpoint page',
  'endpoint.opcard.chipInnerHint': 'The trace enters through {gw}; the card opens {svc}, the first service behind it that handles the request',
  'endpoint.opcard.gatewayChip': 'gateway: {op}',
  'endpoint.opcard.gatewayHint': 'Open the operation card of the gateway ({gw}) instead',
  // v0.10.1491 END
  // v0.10.1498 — /logs Kibana-Discover sorgu çubuğu + süzgeç satırı
  'logs.dv.addFilter':        '+ Filter',
  'logs.dv.addFilterTitle':   'Trace ID, with-trace, my team and field filters',
  'logs.dv.traceId':          'Trace ID (Enter applies)',
  'logs.dv.fieldFilter':      '+ Field filter…',
  'logs.dv.share':            'Share',
  'logs.dv.copyLink':         'Copy link',
  'logs.dv.linkCopied':       'Link copied — the time window is pinned to absolute times',
  'logs.dv.more':             'More actions',
  'logs.dv.exportNone':       'No loaded rows to export yet',
  'logs.dv.appliedSearch':    'Applied search',
  'logs.dv.withTrace':        'with trace',
  'logs.dv.withTraceRemove':  'Remove the with-trace filter',
  'logs.dv.asTrace':          'filter as trace',
  'logs.dv.asTraceTitle':     'The search box holds a bare trace id — filter to that trace instead of a free-text match',
  // v0.10.1498 END
  // v0.10.1502 — /logs Columns ▾ + tablo araç çubuğu
  'logs.dv.columns':          'Columns',
  'logs.dv.columnsTitle':     'Table columns — reorder, remove, add from the loaded fields',
  'logs.dv.columnsShown':     'Shown columns',
  'logs.dv.colUp':            'Move left',
  'logs.dv.colDown':          'Move right',
  'logs.dv.colRemove':        'Remove column',
  'logs.dv.colMessageFixed':  'The Message column can be moved but not removed',
  'logs.dv.colAddPlaceholder': 'Add a column — filter loaded fields…',
  'logs.dv.colNoMatch':       'No matching field in the loaded list',
  'logs.dv.colReset':         'Reset columns',
  // v0.10.1516 — /traces Sütunlar ▾ (sütun sırası).
  'traces.cols.trigger':      'Columns',
  'traces.cols.title':        'Column order — drag a header or use ↑ ↓ here',
  'traces.cols.shown':        'Column order',
  'traces.cols.pinned':       'Start time stays first (pinned while scrolling sideways)',
  'traces.cols.builtinFixed': 'Built-in column — can be moved, not removed',
  'traces.cols.reset':        'Reset order',
  'logs.dv.export':           'Export loaded rows',
  // v0.10.1502 END
  // v0.10.1511 — /traces header column filters (pages/traces/ColumnFilter.tsx)
  'traces.colFilter.filterBy': 'Filter by',
  'traces.colFilter.filtered': 'Filtered',
  'traces.colFilter.clear': 'Clear',
  'traces.colFilter.errorsOnly': 'Only traces with an error span',
  'traces.colFilter.min': 'Min',
  'traces.colFilter.max': 'Max',
  'traces.colFilter.minOverMax': 'Min is greater than max',
  'traces.colFilter.apply': 'Apply',
  'traces.colFilter.search': 'Search values…',
  'traces.colFilter.error': 'Could not load values',
  'traces.colFilter.noMatch': 'No value matches the search in this window',
  'traces.colFilter.empty': 'No values in this window and filters',
  'traces.colFilter.include': 'Include',
  'traces.colFilter.includeTip': 'Only traces with this value (=)',
  'traces.colFilter.exclude': 'Exclude',
  'traces.colFilter.excludeTip': 'Exclude traces that have this value in any span (NOT HAS)',
  'traces.colFilter.topN': 'Top {n} by trace count — search to find others',
  'traces.colFilter.counts': 'Trace counts in this window and filters',
  'traces.colFilter.orBlocked': 'Column filters add AND chips; switch the filter builder back to AND to use them.',
};

const TR: Catalog = {
  // v0.10.1498 — /logs Kibana-Discover sorgu çubuğu + süzgeç satırı
  'logs.dv.addFilter':        '+ Süzgeç',
  'logs.dv.addFilterTitle':   'Trace ID, trace\'li satırlar, takımım ve alan süzgeçleri',
  'logs.dv.traceId':          'Trace ID (Enter uygular)',
  'logs.dv.fieldFilter':      '+ Alan süzgeci…',
  'logs.dv.share':            'Paylaş',
  'logs.dv.copyLink':         'Bağlantıyı kopyala',
  'logs.dv.linkCopied':       'Bağlantı kopyalandı — zaman penceresi mutlak zamana sabitlendi',
  'logs.dv.more':             'Diğer eylemler',
  'logs.dv.exportNone':       'Henüz indirilecek yüklenmiş satır yok',
  'logs.dv.appliedSearch':    'Uygulanan arama',
  'logs.dv.withTrace':        'trace\'li',
  'logs.dv.withTraceRemove':  'Trace\'li satır süzgecini kaldır',
  'logs.dv.asTrace':          'trace olarak süz',
  'logs.dv.asTraceTitle':     'Arama kutusunda çıplak bir trace id var — serbest metin eşleşmesi yerine o trace\'e süz',
  // v0.10.1498 END
  // v0.10.1502 — /logs Columns ▾ + tablo araç çubuğu
  'logs.dv.columns':          'Sütunlar',
  'logs.dv.columnsTitle':     'Tablo sütunları — sırala, kaldır, yüklenmiş alanlardan ekle',
  'logs.dv.columnsShown':     'Görünen sütunlar',
  'logs.dv.colUp':            'Sola taşı',
  'logs.dv.colDown':          'Sağa taşı',
  'logs.dv.colRemove':        'Sütunu kaldır',
  'logs.dv.colMessageFixed':  'Message sütunu taşınabilir ama kaldırılamaz',
  'logs.dv.colAddPlaceholder': 'Sütun ekle — yüklenmiş alanları süz…',
  'logs.dv.colNoMatch':       'Yüklenmiş listede eşleşen alan yok',
  'logs.dv.colReset':         'Sütunları sıfırla',
  'traces.cols.trigger':      'Sütunlar',
  'traces.cols.title':        'Sütun sırası — başlığı sürükle ya da burada ↑ ↓ kullan',
  'traces.cols.shown':        'Sütun sırası',
  'traces.cols.pinned':       'Start time hep ilk sırada (yatay kaydırmada sabit)',
  'traces.cols.builtinFixed': 'Yerleşik sütun — taşınabilir, kaldırılamaz',
  'traces.cols.reset':        'Sırayı sıfırla',
  'logs.dv.export':           'Yüklenen satırları indir',
  // v0.10.1502 END
  // v0.10.1477 — /logs takım süzgeci
  'logs.team.mine':           'Takımım',
  'logs.team.label':          'Takım',
  'logs.team.change':         'Değiştir',
  'logs.team.pick':           'Takım seç…',
  'logs.team.remove':         'Takım süzgecini kaldır',
  'logs.team.removeTitle':    'Takım süzgecini kaldır (takımın bu tarayıcıda artık otomatik uygulanmaz; geri açmak için “Takımım”)',
  'logs.team.applyTitle':     'Logları takımına göre süz (takım etiketi alanları: Ayarlar → Elasticsearch)',
  'logs.team.chipTitle':      'Yapılandırılmış takım alan(lar)ında bu takımın tüm yazımlarıyla tam eşleşme',
  'logs.team.unapplied':      'takım süzgeci uygulanmadı — bu log kaynağında takım alanı yok',
  'logs.team.unappliedTitle': 'Takım süzgeci uygulanamadı: log backend\'i ClickHouse ya da Ayarlar → Elasticsearch\'te Team alanı boş. Aşağıdaki satırlar takıma göre SÜZÜLMEMİŞTİR.',
  // Sidebar group headings
  'navGroup.triage':     'Olay yönetimi',
  'navGroup.services':   'Servisler',
  'navGroup.signals':    'Sinyaller',
  'navGroup.workspaces': 'Çalışma alanları',
  'navGroup.alerting':   'Alarm yönetimi',
  'navGroup.system':     'Sistem',
  'navGroup.management': 'Yönetim',

  // Sidebar
  'nav.inbox':       'Sorunlar',
  'nav.shift':       'Vardiya özeti',
  'nav.incidents':   'Olaylar',
  'nav.problems':    'Exceptions/Errors', // v0.10.1107 (operatör: "menüde Exceptions/Errors olarak gözüksün")
  'nav.anomalies':   'Anomaliler',
  'nav.rollouts':    'Deployment/Rollout’lar', // v0.10.984 (Rollouts v2 P2.3)
  'nav.services':    'Servisler',
  'nav.endpoints':   'Endpoint’ler',
  'nav.databases':   'Veritabanları',
  'nav.clusters':    'Cluster\u2019lar',
  'nav.messaging':   'Mesajlaşma',
  'nav.external':    'Dış API’ler',
  'nav.hosts':       'Host’lar',
  'nav.traces':      'İzler',
  'nav.metrics':     'Metrikler',
  'nav.logs':        'Loglar',
  // v0.10.1335 — birleşik arama sayfası (/search); kaynak adları (Logs,
  // Traces…) iki dilde aynı: ürün yüzeyi adları, operatör onaylı metin.
  'nav.search':      'Arama',
  'search.placeholder':   'Servis, endpoint, problem, exception, trace, log ara…',
  'search.minChars':      'En az 2 karakter yazın',
  'search.minCharsBody':  'Tek sorgu, seçili zaman aralığında servis, endpoint, problem, exception, trace ve logları arar.',
  'search.loading':       'Aranıyor…',
  'search.error':         'Arama başarısız',
  'search.noMatch':       '“{q}” için sonuç yok',
  'search.noMatchBody':   'Daha kısa bir sorgu, daha geniş bir zaman aralığı deneyin ya da bir trace id yapıştırın.',
  'search.hidden':        'Mevcut filtrelerle sonuç yok',
  'search.hiddenN':       '{n} gizli',
  'search.clearAll':      'Tümünü temizle',
  'search.results':       '{n} sonuç',
  'search.sort':          'Sıralama',
  'search.sort.relevance': 'İlgi',
  'search.sort.newest':   'En yeni',
  'search.sort.oldest':   'En eski',
  'search.sort.severity': 'Önem',
  'search.group':         'Gruplama',
  'search.group.list':    'Liste',
  'search.group.type':    'Türe göre',
  'search.facet.type':    'Tür',
  'search.facet.service': 'Servis',
  'search.facet.env':     'Ortam',
  'search.facet.sev':     'Önem ≥',
  'search.facet.status':  'Problemler',
  'search.facet.scope':   'Servis, ortam ve önem sayıları: gösterilen sonuçlar içinde',
  'search.status.open':   'Açık',
  'search.status.all':    'Tümü',
  'search.type.service':  'Servis',
  'search.type.endpoint': 'Endpoint',
  'search.type.problem':  'Problem',
  'search.type.exception': 'Exception',
  'search.type.trace':    'Trace',
  'search.type.log':      'Log',
  'search.src.unavailable': '{src} okunamadı ({reason})',
  'search.src.needsService': '{src}: servis ya da trace id gerekli',
  'search.src.skipped':   '{src} atlandı ({reason})',
  'search.open.trace':    "Traces'te aç",
  'search.open.log':      "Logs'ta aç",
  'search.open.problem':  "Problems'ta aç",
  'search.open.exception': "Exceptions'ta aç",
  'search.open.service':  'Serviste aç',
  'search.open.endpoint': "Operasyon trace'lerini aç",
  'search.askCosre':      "CoSRE'ye sor",
  'search.seeAll':        '“{q}” için tüm sonuçlar →',
  'nav.explore':     'Keşfet',
  'nav.runbooks':    'Runbook\'lar',
  'nav.dashboards':  'Panolar',
  'nav.profiling':   'Profilleme',
  'nav.ai':          'AI gözlem',
  'nav.alerts':      'Alarmlar',
  'nav.watchers':    'Watcher\'lar',
  'nav.topology':    'Topoloji',
  'nav.slos':        'SLO\'lar',
  'nav.monitors':    'Monitörler',
  'nav.events':      'Olaylar',
  'nav.system':      'Sistem',
  'navGroup.community': 'Topluluk',
  // v0.10.1340 — kabuk atlama bağlantısı (AppShell SkipToContent)
  'shell.skipToContent': 'İçeriğe atla',
  // v0.10.1340 END

  // Login
  'login.signIn':      'Giriş yap',
  'login.signingIn':   'Giriş yapılıyor…',
  'login.email':       'E-posta',
  'login.password':    'Parola',
  'login.usernameOrEmail': 'Kullanıcı adı veya e-posta',
  'login.signInWith':  'Şununla giriş yap:',
  'login.orLocal':     'veya yerel hesapla giriş yap',
  'login.signInToContinue': 'Devam etmek için giriş yapın',
  'login.invalid':     'Geçersiz kullanıcı adı veya parola',
  'login.invalidHint': 'Parolayı bir dokümandan kopyaladıysanız tire (-) yerine başka bir karakter (–, —, gizli boşluk) yapışmış olabilir; tekrar elle yazıp deneyin.',

  // User menu
  'user.signOut':        'Çıkış yap',
  'user.changePassword': 'Parolayı değiştir',
  'user.manageUsers':    'Kullanıcıları yönet',
  'user.settings':       'Ayarlar',

  // Time range picker (Grafana-parity panel)
  'trp.quickRanges':   'Hızlı aralıklar',
  'trp.absoluteRange': 'Mutlak zaman aralığı',
  'trp.from':          'Başlangıç',
  'trp.to':            'Bitiş',
  'trp.applyRange':    'Zaman aralığını uygula',
  'trp.recentRanges':  'Son kullanılanlar',
  'trp.browserTime':   'Tarayıcı saati',
  'trp.zoomOut':       'Uzaklaş (2×)',
  // v0.10.1430 — global otomatik yenileme seçicisi (Topbar, zaman seçicinin yanı)
  'rp.label':          'Otomatik yenileme',
  'rp.off':            'Kapalı',
  'rp.manualOnly':     'Kapalı · manuel',
  'rp.manualReason':   'Otomatik yenileme metrik ve genel bakış sayfalarında çalışır (Services, Service, Endpoints, Metrics, Dashboard, Service map, Hosts, Clusters). Burada ↻ kullanın.',
  'rp.every':          'Her {t} otomatik yenileme — göreli pencere kayar, gizli sekmede durur.',
  'rp.offReason':      'Otomatik yenileme kapalı — ↻ kullanın ya da bir aralık seçin.',
  'rp.refreshNow':     'Şimdi yenile',
  'rp.refreshed':      'Yenilendi',
  'rp.pausedAbsolute': 'durakladı · mutlak aralık',
  'rp.pausedEdit':     'durakladı · düzenleme',
  'rp.updatedAgo':     'güncellendi {ago} önce',
  'rp.ago.s':          '{n} sn',
  'rp.ago.m':          '{n} dk',
  'trp.prevMonth':     'Önceki ay',
  'trp.nextMonth':     'Sonraki ay',
  'trp.errFrom':       'Geçersiz "Başlangıç" — YYYY-AA-GG SS:dd:ss kullanın',
  'trp.errTo':         'Geçersiz "Bitiş" — YYYY-AA-GG SS:dd:ss kullanın',
  'trp.errOrder':      '"Bitiş", "Başlangıç"tan sonra olmalı',
  'trp.errMax':        'Aralık çok büyük (en fazla 1 yıl)',

  // Quick-range labels
  'range.5m':  'Son 5 dakika',
  'range.15m': 'Son 15 dakika',
  'range.30m': 'Son 30 dakika',
  'range.1h':  'Son 1 saat',
  'range.3h':  'Son 3 saat',
  'range.6h':  'Son 6 saat',
  'range.12h': 'Son 12 saat',
  'range.24h': 'Son 24 saat',
  'range.2d':  'Son 2 gün',
  'range.7d':  'Son 7 gün',
  'range.30d': 'Son 30 gün',

  // v0.10.1461 — composer "Kod" hapı (R2)
  'ai.codeTurn':            'Kod',
  'ai.codeTurnHint':        'Bu turda kaynak kodu okur — yalnız yerel (self-hosted) modele gider; o turda dış araçlar kapalı.',
  'ai.codeTurnUnavailable': 'Kod okuma kapalı',
  'ai.codeTurnLocked':      'Bu konuşmada kod okundu — konuşma yerel modelde kalır; yeni konuşma için Temizle', // v0.10.1465
  'ai.askCosre':          'CoSRE’ye sor',
  // v0.10.1435 — panelden "Bu grafiği açıkla"
  'ai.askCosreMetricPanelHint': 'Bu grafiği CoSRE sohbetinde açıkla (seçili aralık ve seri dahil)',
  // v0.10.1435 END
  'ai.askCosreTraceHint': 'Bu trace hakkında CoSRE’ye soru sor.',
  'ai.askCosreSpanHint':  'CoSRE’ye sor · span — bu span’i açıkla.',
  // v0.10.1338
  'ai.askCosreLogHint':      'CoSRE’ye sor · log — bu log satırını açıkla.',
  'ai.askCosreLogSpikeHint': 'CoSRE’ye sor · log artışı — bu log artışını açıkla.',
  'ai.askCosreEndpointHint': 'CoSRE’ye sor · endpoint — ne oluyor, ne zamandan beri, süre nereye gidiyor, hangi hatalar.', // v0.10.1367
  'dbspan.openDatabase':       "Database'de aç",
  'dbspan.openDatabaseHint':   'Bu veritabanını Databases sayfasında aç (span penceresi)',
  'dbspan.statementDetail':    'Sorgu detayı',
  'dbspan.statementDetailHint': 'Bu sorgu sınıfını aç (normalize parmak izi) — çağrı, hata, gecikme, çağıranlar',
  'dbspan.engineHint':         'Bu motorun tüm veritabanları (Databases sayfası)',
  'dbspan.tableHint':          'Bu tabloya dokunan sorgular (sorgu listesi, arama)',
  'stmt.allStatements':        'Tüm sorgular',

  'ai.ctx.label':     'Bağlam',
  'ai.ctx.aria':      'CoSRE sohbet bağlamı',
  'ai.ctx.trace':     'Trace',
  'ai.ctx.span':      'Span',
  'ai.ctx.service':   'Servis',
  'ai.ctx.env':       'Ortam',
  'ai.ctx.clusterNs': 'Cluster / namespace',
  'ai.ctx.window':    'Pencere',

  'cosre.cap.aria':             'CoSRE neler yapabilir',
  'cosre.cap.tryHint':          'Kutuya örnek bir soru yazar — düzenleyip gönder',
  // v0.10.1368 (operatör: "wiki cache refresh nasıl yapılır var ama düzgün çalışmıyor") — örnek
  // `@wiki` kapsam önekiyle başlar: wiki katmanı kesin devreye girer (chat_scope), serbest
  // "wikide …" cümlesi guided'a kayabiliyordu.
  'cosre.cap.wiki':             'Kurum wiki’sinde arayabilirim — ör. “@wiki BSA cache refresh en sık karşılaşılan hatalar nelerdir?”',
  'cosre.cap.wiki.prompt':      '@wiki BSA cache refresh en sık karşılaşılan hatalar nelerdir?',
  'cosre.cap.service':          'Bir servisin trace’lerine, hatalarına, gecikmesine bakabilirim — servis adını yaz (@ ile tamamla)',
  'cosre.cap.service.prompt':   '@| servisinin son 1 saatteki trace\'leri, hataları ve gecikmesi?',
  'cosre.cap.operation':        'Hata veren teknik operasyonları inceleyebilirim — operasyon adını yaz',
  'cosre.cap.operation.prompt': '| operasyonu neden hata veriyor?',
  // v0.10.1382 (operatör: "hazır soruları seçilebilir halde getirsek") — satırların
  // altındaki canlı veriden seçenekler (/api/copilot/starters); p1/deploy satırı
  // yalnız o grupta öğe varken.
  // v0.10.1404 (operatör: "P1 yerine en son exception'lara yönlendirsin.") — P1
  // satırı yerine en son exception grupları; şablon takım exception sorusu.
  'cosre.cap.exceptions':        'Takımının son exception\'larına bakabilirim', // v0.10.1472 (operatör)
  'cosre.cap.exceptions.prompt': 'Takımımın exception\'ları?',
  'cosre.cap.exceptions.searchTitle': 'Takım seç — Exceptions sayfası o takımla süzülü açılır', // v0.10.1515 (operatör)
  'cosre.cap.choices':          'Hazır seçenekler',
  'cosre.cap.search.service':   'Servis ara…', // v0.10.1390
  'cosre.cap.search.operation': 'Operasyon ara…', // v0.10.1390
  // v0.10.1402 (operatör: "Alttaki takımımın servisleri exceptionları yukarıda
  // olsun. Takım seçilebilsin.") — Takımım kartı (en üstte).
  'cosre.cap.team':             'Takımım — takımının servislerine ve exception’larına bakabilirim; başka bir takım da seçebilirsin',
  'cosre.cap.team.prompt':      '| takımının servisleri nasıl?',
  'cosre.cap.search.team':      'Takım ara…',
  'cosre.cap.team.searchTitle': 'Takım seç — CoSRE o takımın servislerini hemen getirir',
  'cosre.team.ask.services':    'Servisleri nasıl?',
  'cosre.team.ask.exceptions':  'Exception\'ları?',
  'cosre.team.ask.topErrors':   'En hatalı servisleri?',
  'cosre.chip.wiki':            'Wikide ara: …',
  // v0.10.1378 (operatör onaylı kuyruk) — "wikide " düz cümleydi, kapsam
  // üretmiyordu; `@wiki ` composer'da scope.wiki=true olur (1368'deki örnek soruyla aynı).
  'cosre.chip.wiki.prompt':     '@wiki ',

  'rootCause.bubbleUnavailable': 'Attribute kıyası okunamadı — span taraması düştü ya da zaman aşımına uğradı. Bu, "ayrışan boyut yok" demek değildir.',

  'rootCause.bubble.errors.title':  'Hatalar nerede yoğunlaşıyor',
  'rootCause.bubble.errors.sub':    'hatalı span\'lerde öne çıkan attribute',
  'rootCause.bubble.errors.legend': 'çubuk = bu değeri taşıyan hatalı span payı · sağ = hata% / taban%',
  'rootCause.bubble.window.title':  'Bu pencerede öne çıkan',
  'rootCause.bubble.window.sub':    'önceki eş-boy pencereye göre öne çıkan attribute',
  'rootCause.bubble.window.legend': 'çubuk = penceredeki span\'lerde bu değerin payı · sağ = pencere% / önceki pencere%',
  'rootCause.blast.relation':       'bu servisi çağıran servisler (span çağrı kenarı); hata sütunu onların gördüğü — %0 bağlı ama etkilenmemiş demek',
  'rootCause.coMovers.title':       'Aynı pencerede hareket edenler (bağlantı doğrulanmadı)',
  'rootCause.coMovers.note':        'bu servisle topoloji kenarı yok — yalnız zaman korelasyonu, kök neden adayı değil',
  'rootCause.coMovers.topoUnknown': 'topoloji okunamadı — bağlar denetlenemedi, yalnız zaman korelasyonu',
  'affected.title':                 'Etkilenen varlıklar',
  'affected.empty.service':         'pencerede doğrulanmış bağı ve gözlenen etkisi olan varlık yok',
  'affected.empty.subject':         'özne servis değil',
  'affected.error':                 'Etkilenen varlıklar okunamadı.',
  'affected.linked':                'Bağlı, etki görülmedi',
  'affected.linked.note':           'bu servisler bu servisi çağırıyor ama pencerede hata görmedi — etkilenen sayılmaz',
  'affected.topoUnknown':           'Topoloji (çağıranlar) okunamadı — yalnız doğrulanabilenler listelendi.',
  'affected.podScoped':             'Pod kapsamlı problem: çağrı kenarları servis düzeyinde, çağrının hangi pod\'a gittiği ayrıştırılamaz.',
  'affected.clustersUnverified':    'Servis birden çok cluster\'da — pod\'un cluster\'ı doğrulanamadı, listelenmedi.',
  'affected.rel.caller':            'çağıran',
  'affected.rel.subject-pod':       'problem pod\'u',
  'affected.rel.evidence-pod':      'pod (kanıt)',
  'affected.rel.cluster':           'cluster',
  'alertChart.belowFloor':          'nokta kuralın en az örnek sayısının altında — değerlendirici orada ölçmedi (boşluk)',
  'alertChart.sourceUnknown':       'kaynak: bilinmiyor — sunucu dizinin kaynağını bildirmedi',

  'sso.trustEmail.label':        'Doğrulanmamış e-postaya güven (önerilmez)',
  'sso.trustEmail.warning':      'email_verified=false olan girişler yalnız izinli alan adlarından kabul edilir. Risk: e-posta çarpışmasıyla hesap ele geçirme — yalnız kullanıcının e-postasını kendisinin belirleyemediği/değiştiremediği kurumsal IdP\'de (self-registration, e-posta düzenleme, sosyal/brokered IdP yok) ve izinli alan adları tanımlıyken kullanın. Admin ve yerel/LDAP hesaplar bu yolla bağlanmaz (aşağıdaki LDAP hesabı bağlama seçeneği açık değilse). Tercih edilen düzeltme IdP tarafında (Keycloak "Trust Email").',
  'sso.trustEmail.needsDomains': 'Önce izinli alan adlarını girin — liste boşken doğrulanmamış e-postaya güvenilemez.',
  // v0.10.1403 — doğrulanmamış e-postalı SSO girişini mevcut LDAP hesabına kullanıcı adıyla bağla.
  'sso.ldapLink.label':          'Mevcut LDAP hesaplarını kullanıcı adıyla SSO\'ya bağla',
  'sso.ldapLink.note':           'Yalnız LDAP girişinden SSO\'ya geçiş için. Doğrulanmamış e-postalı giriş mevcut LDAP hesabını ancak şu durumda açar: imzalı id_token\'daki kullanıcı adı claim\'i Coremetry\'nin o kullanıcının son LDAP girişinde kaydettiği LDAP kullanıcı adına, e-posta hesabın e-postasına eşit ve o LDAP kullanıcı adına sahip tam bir aktif hesap var. Hesap bundan sonra SSO hesabı olur (audit\'lenir). Her kullanıcının önceden en az bir LDAP girişi olmalı. Yerel/parola hesapları asla bağlanmaz. "Doğrulanmamış e-postaya güven" gerekir.',
  'sso.ldapLink.warning':        'Yalnız kullanıcı IdP\'de kullanıcı adını ve e-postasını değiştiremiyorsa kullanın (Keycloak LDAP federasyonu salt-okunur, "Edit username" kapalı, self-registration ve brokered/sosyal IdP yok) — ikisini birden belirleyebilen o hesabı açar.',
  'sso.ldapLinkAdmin.label':     'Admin hesaplarını da bu yolla bağla (önerilmez)',
  'sso.ldapLinkAdmin.warning':   'Admin hesabına IdP\'de bu kullanıcı adını ve e-postayı denetleyen kişi erişir; denetim her admin girişinde yinelenir. Adminlerin SSO\'ya geçmesi gerekmiyorsa kapalı bırakın.',

  'sso.usernameFallback.label':   'E-posta yoksa kullanıcı adıyla eşleştir (AD/LDAP)',
  'sso.usernameFallback.on':      'Açık: id_token ve UserInfo e-posta taşımıyorsa doğrulanmış id_token\'daki kullanıcı adı claim\'i AD/LDAP\'ta (tam eşleşme) aranır ve dizindeki e-posta kullanılır; LDAP yoksa (ya da dizinde e-posta yoksa) kullanıcı LDAP kullanıcı adıyla mevcut Coremetry kullanıcısına eşlenir. E-postasız yeni kullanıcı açılmaz. İzinli alan adları yine uygulanır.',
  'sso.usernameFallback.off':     'Kapalı (varsayılan): id_token ve UserInfo e-posta taşımıyorsa giriş reddedilir (email_missing). UserInfo her zaman denenir. Tercih edilen düzeltme IdP\'de: Keycloak LDAP federasyonunda mail → email eşleyicisi ve email client scope\'u Default + "Add to ID token".',
  'sso.usernameFallback.risk':    'Yalnız kullanıcı bu claim\'i IdP\'de değiştiremiyorsa kullanın (LDAP federasyonu salt-okunur, kullanıcı adı düzenleme kapalı). Brokered (dış/sosyal) IdP, self-registration ya da düzenlenebilir kullanıcı adı varsa GÜVENSİZ — kullanıcı adını seçebilen o hesabı açar. Admin hesapları aşağıda ayrıca izin verilmedikçe bu yolla açılmaz.',
  'sso.usernameAdmin.label':      'Kullanıcı adı eşleştirmesiyle admin hesaplarını da aç (önerilmez)',
  'sso.usernameAdmin.warning':    'Admin hesabına IdP\'de bu kullanıcı adını denetleyen kişi erişir. Kapalı bırakın; adminler parola/LDAP ile girebilir.',
  'sso.usernameClaim.label':      'Kullanıcı adı claim\'i',
  'sso.usernameClaim.hint':       'Yalnız bu claim okunur (varsayılan preferred_username = AD sAMAccountName). Harf, rakam ve _ - . : ; profil/e-posta claim\'leri seçilemez.',

  // v0.10.1334 — uygulama içi rehberli tur (features/tour), R1.
  'tour.chrome.dialog':     'Rehberli tur',
  'tour.chrome.stepOf':     'Adım {n} / {total}',
  'tour.chrome.progress':   'Tur ilerlemesi',
  'tour.chrome.back':       'Geri',
  'tour.chrome.next':       'İleri',
  'tour.chrome.finish':     'Bitir',
  'tour.chrome.skip':       'Turu atla',
  'tour.chrome.restart':    'Turu yeniden başlat',
  'tour.chrome.loading':    'Sayfanın bu bölümü aranıyor…',
  'tour.note.timeout':      'Veri henüz yok — sayfanın bu bölümü görünmedi. Yine de devam edebilirsin.',
  'tour.note.not_yet':      'Sonraki sürümde geliyor.',
  'tour.note.no_services':  'Henüz servis raporlamıyor — OTLP verisi gelince tur anlamlı olur.',
  'tour.welcome.title':     'Hoş geldin — iki dakikalık tur',
  'tour.welcome.body':      'Bu kurulumun şu an, son 5 dakikada gördükleri. Sırada Servisler listesi var — incelemelerin çoğu oradan başlar.',
  'tour.welcome.unavailable': 'Canlı sayılar okunamadı — tur yine de çalışır.',
  'tour.welcome.tile.services': 'Servis',
  'tour.welcome.tile.spans':    'Span · 5 dk',
  'tour.welcome.tile.spansTop': 'Span · 5 dk · ilk 50',
  'tour.welcome.tile.busiest':  'En yoğun servis',
  'tour.welcome.tile.envs':     'Ortam',
  'tour.services.title':    'Servisler: başlangıç noktan',
  'tour.services.body':     'Her satır OTLP span gönderen bir servis. Adın önündeki nokta sağlığıdır: hata oranı %1\'i aşınca (ya da açık bir uyarı/bilgi problemi varsa) amber, %5\'i aşınca (ya da açık kritik problem varsa) kırmızı. Spans, Error rate ve P99 seçili zaman aralığını kapsar; önceki pencereyle kıyas için "Δ prior"u aç. Servisi açmak için satıra tıkla.',
  // v0.10.1336 — rehberli tur R2: adım 3–5 (operations / traces / cosre) + alt adımlar.
  'tour.operations.title':  'Operasyonlar: zaman nereye gidiyor',
  'tour.operations.header': 'Bu, en yoğun servisin sayfası. Adın üstündeki kırıntı aynı zaman penceresiyle Servisler\'e döner, ☆ servisi Cmd-K hızlı erişimine sabitler; bu düğmeler servisin backtrace, log, endpoint, problem, anomali ve trace\'lerine atlar — servis ve pencere de yanında gelir.',
  'tour.operations.tab':    'Operations sekmesi bu servisin her operasyonunu (span adı) listeler. Sekme URL\'de durur (?tab=operations); paylaşılan bağlantı doğrudan burada açılır.',
  'tour.operations.impact': 'Satırlar Impact = ortalama süre × çağrı sayısı ile sıralanır: kullanıcılarına toplamda en çok zaman kaybettiren operasyon en üstte — yalnız en yavaşı değil. Raw span adlarını geldiği gibi gösterir; Normalized (sağ üstte) kimlik taşıyan adları GET /users/:id gibi şekillerde toplar.',
  'tour.operations.body':   'Her satır bir operasyon: trendi, çağrı sayısı, hata oranı ve gecikme yüzdelikleri. Satıra tıklayınca o operasyonun trace\'leri açılır — sıradaki adım tam oraya gidiyor.',
  'tour.traces.title':      'Trace\'ler: yavaş ve hatalı istekler',
  'tour.traces.filters':    'Bu servisin trace\'lerini status_code = error hızlı çipi uygulanmış olarak açtık. Etkin süzgeçler bu çubukta (tıkla: düzenle, ✕: kaldır); alttaki "Hızlı:" satırı sık kullanılanları — hatalar, server span\'ler, POST — tek tıkla ekler.',
  'tour.traces.minms':      'Min ms = 500 hızlı istekleri gizler; geriye yalnız yavaş ve hatalı trace\'ler kalır. Soldaki servis seçici listeyi daraltır; bir alanı değiştirince Search\'e bas (ya da Enter).',
  'tour.traces.body':       'Her satır bir trace — servisler boyunca tek bir istek. Satıra tıklayınca şelalesi açılır: her span, süresi ve öznitelikleri.',
  'tour.cosre.title':       'CoSRE: düz dille sor',
  'tour.cosre.fab':         'Bu düğme CoSRE\'yi açar — telemetrini okuyan asistan. Bulunduğun sayfayı bilir: bir servis ya da trace üstündeyken o bağlamdan başlar. Kırmızı rozet açık kritik problemleri sayar.',
  'tour.cosre.body':        'Bu servis için bir soru taslağı hazırladık — hiçbir şey gönderilmedi. Bitir\'e bas, istersen düzenle, sonra Enter ile gönder. Esc önce sohbet çekmecesini kapatır; ikinci Esc turu atlar.',
  'tour.cosre.prompt':      'Son 1 saatte {svc} hatalı mı?',
  'tour.cosre.promptFleet': 'Şu an önce neye bakmalıyım?',
  'tour.note.copilot_disabled': 'CoSRE bu kurulumda kapalı — admin Settings › CoSRE\'den açar.',
  // v0.10.1336 END
  // v0.10.1434 — Ayarlar › CoSRE › Araç setleri (AiToolsetsPanel): 74 araç kaynağa göre gruplu
  'aiToolsets.tab':          'Kaynak araç setleri',
  'aiToolsets.title':        'AI kaynak araç setleri',
  'aiToolsets.intro':        '{n} CoSRE / MCP aracı okudukları veri kaynağına göre gruplu. Kapalı bir setin araçları uygulama içi sohbete artık sunulmaz; dış MCP çağrıları adlandırılmış bir gerekçeyle reddedilir. Çekirdek telemetri (services, traces, problems) ve meta kapatılamaz. Sağlık yapılandırmadan türetilir — sorgu yok; log için önbellekli bir canlılık yoklaması var.',
  'aiToolsets.col.toolset':  'Araç seti',
  'aiToolsets.col.source':   'Kaynak',
  'aiToolsets.col.tools':    'Araçlar',
  'aiToolsets.col.health':   'Sağlık',
  'aiToolsets.col.status':   'Durum',
  'aiToolsets.on':           'Açık',
  'aiToolsets.off':          'Kapalı',
  'aiToolsets.required':     'zorunlu',
  'aiToolsets.enable':       'Aç',
  'aiToolsets.disable':      'Kapat',
  'aiToolsets.readOnly':     'Salt okunur: araç setlerini yalnız admin açıp kapatabilir.',
  'aiToolsets.lastChange':   'Son değişiklik: {by} · {at}',
  'aiToolsets.saved':        'Araç seti {name}: {state}',
  'aiToolsets.saveFailed':   'Araç setleri kaydedilemedi: {err}',
  'aiToolsets.loadFailed':   'Araç setleri okunamadı',
  'aiToolsets.empty':        'Sunucu araç seti bildirmedi',
  'aiToolsets.confirmTitle': '{name} araç seti kapatılsın mı?',
  'aiToolsets.confirmBody':  '{n} aracı CoSRE sohbetine artık sunulmayacak ve dış MCP çağrıları reddedilecek. İstediğin zaman yeniden açabilirsin.',
  'aiToolsets.health.ok':             'sağlıklı',
  'aiToolsets.health.partial':        'kısmi',
  'aiToolsets.health.not_configured': 'yapılandırılmamış',
  'aiToolsets.health.unreachable':    'erişilemiyor',
  'aiToolsets.health.unauthorized':   'yetki reddi',
  'aiToolsets.health.timeout':        'zaman aşımı',
  'aiToolsets.health.error':          'hata',
  'aiToolsets.src.services':    'ClickHouse servis / operasyon / topoloji / SLO özetleri',
  'aiToolsets.src.traces':      'ClickHouse span\'leri / trace özetleri',
  'aiToolsets.src.problems':    'Problemler, anomaliler, kök neden hipotezleri, exception grupları, vaka hafızası',
  'aiToolsets.src.changes':     'Deploy olayları, servis sürümleri, workload rollout\'ları',
  'aiToolsets.src.metrics':     'VictoriaMetrics / ClickHouse metrik noktaları / Thanos',
  'aiToolsets.src.logs':        'Log deposu (Elasticsearch ya da ClickHouse)',
  'aiToolsets.src.k8s':         'Varlık kataloğu (namespace, workload, pod)',
  'aiToolsets.src.oracle':      'Oracle hata logu kopyası',
  'aiToolsets.src.devops-code': 'Azure DevOps kod araması ve depoları',
  'aiToolsets.src.devops-wiki': 'Azure DevOps wiki',
  'aiToolsets.src.knowledge':   'Runbook\'lar, doküman RAG, statik ürün rehberi',
  'aiToolsets.src.meta':        'Sohbet durumu ve saf yardımcılar',
  // v0.10.1434 END
  // v0.10.1458 — Ayarlar › CoSRE › sağlayıcı sekmesi (AiGeneralQuestionsPanel): konu dışı sorular anahtarı
  'aiGeneralQ.title':      'Genel sorular',
  'aiGeneralQ.label':      'Genel sorulara cevap ver (telemetri dışı sorular modelle cevaplanır; kapalıyken kibar sınır + yol tarifi)',
  'aiGeneralQ.hint':       'Telemetriyle ilgisiz sorular ("Türkiye\'nin başkenti neresidir" gibi) modelden kısa bir genel bilgi cevabı alır; cevap Coremetry telemetrisinden gelmediği notuyla işaretlenir, altında her zamanki CoSRE önerileri durur. Araç, telemetri ya da doküman kullanılmaz. Zararlı istekler her zaman reddedilir.',
  'aiSourceDisc.label':    'Otomatik kaynak keşfi (yüklenen tablolar, wiki, telemetri ve Exceptions birlikte aranır; kanıtı en güçlü kaynak cevaplar)',
  'aiSourceDisc.hint':     'CoSRE cevaplamadan önce tüm kaynakları paralel ve modelsiz yoklar. Kanıtı en güçlü kaynak cevaplar; kanıtı olan diğer kaynaklar cevabın altında çip olur (/excel, /wiki, /telemetri, /exceptions). İki kaynak eşit güçlüyse hangisinden cevaplanacağını sorar. Kapalıyken eski sabit sıra.',
  'aiSourceDisc.saved':    'Kaynak keşfi: {state}',
  'aiGeneralQ.readOnly':   'Salt-okunur: bu ayarı yalnız admin değiştirebilir.',
  'aiGeneralQ.lastChange': 'Son değişiklik: {by} · {at}',
  'aiGeneralQ.saved':      'Genel sorular: {state}',
  'aiGeneralQ.on':         'açık',
  'aiGeneralQ.off':        'kapalı',
  'aiGeneralQ.saveFailed': 'Kaydedilemedi: {err}',
  'aiGeneralQ.loadFailed': 'Ayar okunamadı: {err}',
  // v0.10.1458 END
  // v0.10.1443 — sohbetten dashboard (components/ai/AddToDashboard.tsx, PinToDashboardModal)
  'dash.addFromChat.one':          "Dashboard'a ekle",
  'dash.addFromChat.all':          'Hepsini dashboard yap',
  'dash.addFromChat.title':        "Dashboard'a ekle",
  'dash.addFromChat.submit':       'Ekle',
  'dash.addFromChat.viewerReason': "Dashboard'a eklemek editör ya da admin rolü ister.",
  'dash.addFromChat.added':        '{n} panel eklendi',
  'dash.addFromChat.panels':       '{n} panel',
  'dash.addFromChat.hint':         "Ekle'ye basmadan hiçbir şey kaydedilmez. Panoda zaten olan panel atlanır; panolar paylaşılır.",
  'dash.addFromChat.skip.duplicate': 'panoda zaten var',
  'dash.addFromChat.skip.invalid':   'sorgu reddedildi',
  'dash.addFromChat.skip.cap':       'pano panel tavanı doldu',
  'dash.addFromChat.skip.size':      'pano boyut tavanı doldu',
  // v0.10.1443 END
  // v0.10.1491 — operasyon kartı (R3 trace yarısı)
  'endpoint.opcard.title':      'Kod ve çağrılar',
  'endpoint.opcard.sub':        'örneklem · {n} trace',
  'endpoint.opcard.slowest':    'en yavaş',
  'endpoint.opcard.random':     'rastgele',
  'endpoint.opcard.colTarget':  'Hedef',
  'endpoint.opcard.colOp':      'Operasyon',
  'endpoint.opcard.colProtocol': 'Protokol',
  'endpoint.opcard.colCalls':   'Çağrı',
  'endpoint.opcard.colErr':     'Hata %',
  'endpoint.opcard.colSamples': 'Örnek trace',
  'endpoint.opcard.error':      'Çağrı ağacı sorgusu başarısız — üstteki bölümler hâlâ geçerli.',
  'endpoint.opcard.noSample':   'Bu pencerede bu operasyonun trace\'i yok — pencereyi genişletin ya da env / cluster süzgecini kaldırın.',
  'endpoint.opcard.empty':      'Bu pencerede alt çağrı görülmedi — operasyon yaprak ya da çocuklar enstrümante değil.',
  'endpoint.opcard.sampleNote': 'Çağrı, hata % ve P95 örneklenen trace\'ler üzerinden, pencerenin tamamı değil. Değerler (SQL metni, URL, gövde) hiç okunmaz.',
  'endpoint.opcard.others':     '+{n} satır gösterilmedi (satır tavanı 25).',
  'endpoint.opcard.truncated':  'Bazı örnek trace\'ler tam yürünemeyecek kadar büyüktü.',
  'endpoint.opcard.clamped':    'Bu bölüm için pencere son 24 saate kırpıldı.',
  'endpoint.opcard.window':     'Pencerenin tamamı (tüm trace\'ler, servis→servis çağrı)',
  'endpoint.opcard.opCode':     'Operasyon kodu (span özniteliği)',
  'endpoint.opcard.version':    'Çalışan sürüm (örneklenen giriş span\'lerinin çoğunluğu)',
  'endpoint.opcard.code.title':  'Kod',
  'endpoint.opcard.code.hint':   'Giriş metodu, istek / cevap alanları ve çağrı yerleri depodan (Azure DevOps) — açınca okunur; kod gövdesi tarayıcıya gelmez.',
  'endpoint.opcard.code.entry':  'Giriş metodu',
  'endpoint.opcard.code.request': 'İstek alanları',
  'endpoint.opcard.code.response': 'Cevap alanları',
  'endpoint.opcard.code.noFields': 'Uygulama tipi yok (ya da depoda bulunamadı).',
  'endpoint.opcard.code.colLoc':  'Konum',
  'endpoint.opcard.code.colKind': 'Tür',
  'endpoint.opcard.code.colCallee': 'Çağrı',
  'endpoint.opcard.code.colTarget': 'Koddaki hedef',
  'endpoint.opcard.code.colMatch': 'Eşleşen çağrı (trace)',
  'endpoint.opcard.code.unmatched': 'eşleşme yok',
  'endpoint.opcard.code.unmatchedHint': 'Hiçbir trace satırı tek başına eşlenemedi — çağrı bu yolda koşmuyor olabilir ya da birden çok satır uyuyor.',
  'endpoint.opcard.code.focusRow': 'Bu satırı çağrı ağacında göster',
  'endpoint.opcard.code.noCallSites': 'Giriş metodunda ve çağırdıklarında HTTP / gRPC / DB / mesajlaşma çağrı yeri bulunamadı.',
  'endpoint.opcard.code.error':  'Kod yarısı yüklenemedi — üstteki çağrı ağacı hâlâ geçerli.',
  'endpoint.opcard.code.searchOff': 'Giriş metodunu bulmak için Code Search gerekli (Ayarlar → Kod entegrasyonu).',
  'endpoint.opcard.code.noEntry': 'Giriş metodu depoda bulunamadı.',
  'endpoint.opcard.usages.title': 'Bu metodu kim kullanıyor? — {m}',
  'endpoint.opcard.usages.possible': 'olası',
  'endpoint.opcard.usages.possibleHint': 'Code Search ref: ADA göre eşler, tipe değil — aynı adlı başka bir metot da eşleşir.',
  'endpoint.opcard.usages.caller': 'trace çağıranı',
  'endpoint.opcard.usages.callerHint': 'Bu depo, trace\'lerde bu operasyonu çağıran bir servisin.',
  'endpoint.opcard.usages.more': '+{n} tane daha gösterilmedi (tavan 10).',
  'endpoint.opcard.usages.none': 'Adla başka bir kullanım bulunamadı.',
  'endpoint.opcard.usages.failed': 'Kullanım araması koşmadı.',
  'endpoint.opcard.code.matchNote': 'Eşleşmeler sezgisel: aynı tür + koddaki literal rota / topic / Feign istemci adı ya da türünün tek çağrısı. Yalnız alan adı ve tipi — değer yok.',
  // v0.10.1514 — trace başlığı (A): ölçü kümesi + kimlik satırı.
  'trace.hdr.summary':     'Trace özeti',
  'trace.hdr.identity':    'İş kimliği',
  'trace.hdr.traceStatus': 'Trace kimliği ve durumu',
  'trace.hdr.traceId':     'Trace ID',
  'trace.hdr.measures':    'Trace ölçüleri',
  'trace.hdr.duration':    'SÜRE',
  'trace.hdr.critical':    'KRİTİK YOL',
  'trace.hdr.spans':       'SPAN',
  'trace.hdr.start':       'BAŞLANGIÇ',
  'trace.hdr.serviceOne':  '{n} servis',
  'trace.hdr.serviceMany': '{n} servis',
  'endpoint.opcard.chip':       'Operasyon kartı',
  'endpoint.opcard.chipHint':   'Bu operasyonun çağrı ağacını (kod ve çağrılar) endpoint sayfasında aç',
  'endpoint.opcard.chipInnerHint': 'Trace {gw} üzerinden giriyor; kart, arkasında isteği işleyen ilk servis olan {svc} için açılır',
  'endpoint.opcard.gatewayChip': 'gateway: {op}',
  'endpoint.opcard.gatewayHint': 'Bunun yerine gateway\'in ({gw}) operasyon kartını aç',
  // v0.10.1491 END
  // v0.10.1511 — /traces header column filters (pages/traces/ColumnFilter.tsx)
  'traces.colFilter.filterBy': 'Filtrele:',
  'traces.colFilter.filtered': 'Filtreli',
  'traces.colFilter.clear': 'Temizle',
  'traces.colFilter.errorsOnly': 'Yalnız hatalı span içeren trace\'ler',
  'traces.colFilter.min': 'En az',
  'traces.colFilter.max': 'En çok',
  'traces.colFilter.minOverMax': 'En az, en çoktan büyük',
  'traces.colFilter.apply': 'Uygula',
  'traces.colFilter.search': 'Değer ara…',
  'traces.colFilter.error': 'Değerler yüklenemedi',
  'traces.colFilter.noMatch': 'Bu pencerede aramaya uyan değer yok',
  'traces.colFilter.empty': 'Bu pencere ve filtrelerde değer yok',
  'traces.colFilter.include': 'Dahil et',
  'traces.colFilter.includeTip': 'Yalnız bu değeri taşıyan trace\'ler (=)',
  'traces.colFilter.exclude': 'Hariç tut',
  'traces.colFilter.excludeTip': 'Herhangi bir span\'inde bu değer olan trace\'leri hariç tut (NOT HAS)',
  'traces.colFilter.topN': 'Trace sayısına göre ilk {n} — diğerleri için ara',
  'traces.colFilter.counts': 'Bu pencere ve filtrelerdeki trace sayıları',
  'traces.colFilter.orBlocked': 'Kolon filtreleri AND çipi ekler; kullanmak için filtre kurucusunu AND\'e çevirin.',
};

const CATALOGS: Record<Lang, Catalog> = { en: EN, tr: TR };

// User-level language override. Stored in localStorage so it
// survives reloads without a server round trip and so unauthed
// pages (the public-trace viewer, the login screen) can also
// honour the picked language. When unset, the branding-level
// language wins — operators who never touch the picker get the
// org default they always had.
const USER_LANG_KEY = 'coremetry.lang';
const USER_LANG_EVENT = 'coremetry:lang-change';

function readUserLang(): Lang | null {
  if (typeof window === 'undefined') return null;
  const v = getRaw(USER_LANG_KEY);
  if (v === 'tr' || v === 'en') return v;
  return null;
}

// useUserLang subscribes to localStorage + the same-tab
// USER_LANG_EVENT. The storage listener fires on OTHER tabs;
// the custom event fires within the current tab where the
// picker was clicked, so both paths flow back to every
// useT consumer immediately.
export function useUserLang(): Lang | null {
  const [lang, setLang] = useState<Lang | null>(() => readUserLang());
  useEffect(() => {
    const handler = () => setLang(readUserLang());
    window.addEventListener(USER_LANG_EVENT, handler);
    window.addEventListener('storage', handler);
    return () => {
      window.removeEventListener(USER_LANG_EVENT, handler);
      window.removeEventListener('storage', handler);
    };
  }, []);
  return lang;
}

// setUserLang persists the choice + broadcasts the same-tab
// event so every useT consumer re-renders without a manual
// reload. Passing null clears the override (falls back to
// branding).
export function setUserLang(lang: Lang | null): void {
  if (typeof window === 'undefined') return;
  if (lang) setRaw(USER_LANG_KEY, lang);
  else removeRaw(USER_LANG_KEY);
  window.dispatchEvent(new Event(USER_LANG_EVENT));
}

// lastResolvedLang — v0.10.948: useLang'in en son çözdüğü etkin dil (marka
// varsayılanı dahil); hiçbir bileşen henüz çizilmediyse null.
let lastResolvedLang: Lang | null = null;

// useLang resolves the EFFECTIVE language: user-picked
// (localStorage) → branding default → English. Exported for
// components that need locale-aware formatting beyond catalog
// lookups (e.g. TimeRangePicker's month/weekday tables).
export function useLang(): Lang {
  const brand = useBranding();
  const userLang = useUserLang();
  const lang = userLang ?? (brand.language === 'tr' ? 'tr' : 'en');
  // v0.10.948 — hook DIŞI metin üreticileri (aiSubjectTitle) için son
  // çözülen dil. İdempotent önbellek: aynı girdi
  // aynı değeri yazar, render saflığını bozmaz.
  lastResolvedLang = lang;
  return lang;
}

// currentLang — v0.10.948: hook kullanamayan saf yardımcıların etkin dili.
// Öncelik useT ile aynı: kullanıcı seçimi (localStorage) → son çözülen
// (marka varsayılanı) → İngilizce.
export function currentLang(): Lang {
  return readUserLang() ?? lastResolvedLang ?? 'en';
}

// useT returns a translator scoped to the effective language.
// Priority: user-picked (localStorage) → branding default →
// English fallback. Hook so any of those layers changing
// (admin saves new branding → invalidateBranding fires; user
// clicks the picker → USER_LANG_EVENT fires) flows through
// every consumer without a manual reload.
export function useT(): (key: string) => string {
  const cat = CATALOGS[useLang()];
  return (key: string) => cat[key] ?? EN[key] ?? key;
}

// Direct catalog access for non-component code paths (e.g. an
// imperative toast). Mirrors useT but reads the cached branding
// synchronously.
export function t(key: string, lang: Lang = 'en'): string {
  return CATALOGS[lang][key] ?? EN[key] ?? key;
}
