// legacyUrls — v0.10.1498 (/logs Kibana-Discover yeniden tasarımı, 4 dilim).
//
// Sözleşme: yeniden tasarım HİÇBİR URL parametresini yeniden adlandırmaz ya
// da eklemez. Dışarıda yaşayan her paylaşılan link / kayıtlı görünüm / derin
// link (anomali "Logları aç", CoSRE çipleri, trace "≡ Logs", doküman kalıcı
// linki) dört dilimden sonra da BAYT BAYT aynı dilimi açmalı. Altın liste
// donduruldu: logsUrlSig, readLogsParams ve writeLogsParams çıktısı ve görünüm
// parametrelerinin (panel / breakdown / asc / doc) ayrıştırması. Bir dilim bu
// dosyayı değiştirmek zorunda kalıyorsa URL sözleşmesini bozuyordur.
import { describe, it, expect } from 'vitest';
import {
  logsUrlSig, readLogsParams, writeLogsParams, readLogsUrlState, parseLogsPanel, parseDocParam,
  logsUrlHasFilterState,
} from '@/lib/logsUrl';
import { parseBreakdown } from '@/components/LogsHistogram';

const GOLDEN = [
  '/logs',
  '/logs?range=1h',
  '/logs?service=orders-api&q=level%3Aerror&range=custom%3A1759494780000-1759497240000',
  '/logs?search=legacy-alias&cluster=kxzone',
  '/logs?traceId=0af7651916cd43dd8448eb211c80319c&spanId=b7ad6b7169203331',
  '/logs?hasTrace=1&severity=17',
  '/logs?pattern=Disk%20full&q=boom&panel=patterns',
  '/logs?panel=templates&service=orders-api',
  '/logs?team=orders-team&severity=13',
  '/logs?filters=%5B%5B%22k8s.namespace%22%2C%22shop-prod%22%2C0%2C0%5D%2C%5B%22http.status_code%22%2C%22%22%2C1%2C0%2C1%5D%2C%5B%22level%22%2C%22%22%2C0%2C1%2C0%2C%5B%22warn%22%2C%22error%22%5D%5D%5D',
  '/logs?cols=timestamp%2Cseverity%2Cbody%2Ck8s.pod.name',
  '/logs?breakdown=cluster&asc=1',
  '/logs?breakdown=namespace&env=prod',
  '/logs?doc=1759494780000000000.12345&docsvc=orders-api&env=prod',
  '/logs?severity=garbage&panel=bogus&breakdown=bogus',
] as const;

const sp = (u: string) => new URLSearchParams(u.split('?')[1] ?? '');

describe('/logs eski URL altın listesi — dört dilim boyunca bayt bayt', () => {
  it('sig + okuma + yazma + görünüm parametreleri donmuş', () => {
    const rows = GOLDEN.map(u => {
      const p = sp(u);
      const st = readLogsUrlState(p);
      return {
        url: u,
        sig: st.sig,
        written: writeLogsParams(new URLSearchParams(), st.filter, st.filtersRaw, st.colsRaw).toString(),
        panel: parseLogsPanel(p.get('panel')),
        breakdown: parseBreakdown(p.get('breakdown')),
        asc: p.get('asc') === '1',
        doc: parseDocParam(p.get('doc')),
        hasFilter: logsUrlHasFilterState(p),
      };
    });
    expect(rows).toMatchInlineSnapshot(`
      [
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": false,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs",
          "written": "",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": false,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs?range=1h",
          "written": "",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["orders-api","","level:error",0,"","",false,"","","",""]",
          "url": "/logs?service=orders-api&q=level%3Aerror&range=custom%3A1759494780000-1759497240000",
          "written": "service=orders-api&q=level%3Aerror",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","kxzone","legacy-alias",0,"","",false,"","","",""]",
          "url": "/logs?search=legacy-alias&cluster=kxzone",
          "written": "cluster=kxzone&q=legacy-alias",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",0,"0af7651916cd43dd8448eb211c80319c","b7ad6b7169203331",false,"","","",""]",
          "url": "/logs?traceId=0af7651916cd43dd8448eb211c80319c&spanId=b7ad6b7169203331",
          "written": "traceId=0af7651916cd43dd8448eb211c80319c&spanId=b7ad6b7169203331",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",17,"","",true,"","","",""]",
          "url": "/logs?hasTrace=1&severity=17",
          "written": "severity=17&hasTrace=1",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": "patterns",
          "sig": "["","","boom",0,"","",false,"","","Disk full",""]",
          "url": "/logs?pattern=Disk%20full&q=boom&panel=patterns",
          "written": "q=boom&pattern=Disk+full",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": "templates",
          "sig": "["orders-api","","",0,"","",false,"","","",""]",
          "url": "/logs?panel=templates&service=orders-api",
          "written": "service=orders-api",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",13,"","",false,"","","","orders-team"]",
          "url": "/logs?team=orders-team&severity=13",
          "written": "severity=13&team=orders-team",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",0,"","",false,"[[\\"k8s.namespace\\",\\"shop-prod\\",0,0],[\\"http.status_code\\",\\"\\",1,0,1],[\\"level\\",\\"\\",0,1,0,[\\"warn\\",\\"error\\"]]]","","",""]",
          "url": "/logs?filters=%5B%5B%22k8s.namespace%22%2C%22shop-prod%22%2C0%2C0%5D%2C%5B%22http.status_code%22%2C%22%22%2C1%2C0%2C1%5D%2C%5B%22level%22%2C%22%22%2C0%2C1%2C0%2C%5B%22warn%22%2C%22error%22%5D%5D%5D",
          "written": "filters=%5B%5B%22k8s.namespace%22%2C%22shop-prod%22%2C0%2C0%5D%2C%5B%22http.status_code%22%2C%22%22%2C1%2C0%2C1%5D%2C%5B%22level%22%2C%22%22%2C0%2C1%2C0%2C%5B%22warn%22%2C%22error%22%5D%5D%5D",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": false,
          "panel": null,
          "sig": "["","","",0,"","",false,"","timestamp,severity,body,k8s.pod.name","",""]",
          "url": "/logs?cols=timestamp%2Cseverity%2Cbody%2Ck8s.pod.name",
          "written": "cols=timestamp%2Cseverity%2Cbody%2Ck8s.pod.name",
        },
        {
          "asc": true,
          "breakdown": "cluster",
          "doc": null,
          "hasFilter": false,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs?breakdown=cluster&asc=1",
          "written": "",
        },
        {
          "asc": false,
          "breakdown": "namespace",
          "doc": null,
          "hasFilter": false,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs?breakdown=namespace&env=prod",
          "written": "",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": {
            "id": 12345,
            "ts": 1759494780000000000,
          },
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs?doc=1759494780000000000.12345&docsvc=orders-api&env=prod",
          "written": "",
        },
        {
          "asc": false,
          "breakdown": "severity",
          "doc": null,
          "hasFilter": true,
          "panel": null,
          "sig": "["","","",0,"","",false,"","","",""]",
          "url": "/logs?severity=garbage&panel=bogus&breakdown=bogus",
          "written": "",
        },
      ]
    `);
  });

  it('yazma yabancı parametreleri korur (range, env, panel, breakdown, asc, doc)', () => {
    for (const u of GOLDEN) {
      const p = sp(u);
      const st = readLogsUrlState(p);
      const out = writeLogsParams(p, st.filter, st.filtersRaw, st.colsRaw);
      for (const k of ['range', 'env', 'panel', 'breakdown', 'asc', 'doc', 'docsvc']) {
        expect(out.get(k), `${u} → ${k}`).toBe(p.get(k));
      }
      // round-trip: yazılanı yeniden okumak aynı süzgeç + aynı sig
      expect(readLogsParams(out)).toEqual(st.filter);
      expect(logsUrlSig(readLogsParams(out), out.get('filters') ?? '', out.get('cols') ?? '')).toBe(st.sig);
    }
  });
});

// v0.10.1817 (Log Search Faz 1 dilim 1.1 — sözleşme çivisi). KQL / Lucene
// yazımları URL katmanında VERBATIM taşınır: `q` ne yazıldıysa sig'e ve
// yazılan URL'ye bayt bayt aynı girer. Dilim 1.3 (KQL → kanonik Lucene)
// çeviriyi SUNUCUDA yapar; bu liste URL'nin o çeviriden etkilenmediğini
// çiviler. Yeni parametreler (ör. `lang=lucene`) yalnız EKLENİR — buradaki
// hiçbir satırın çıktısı değişmez.
const GOLDEN_QUERIES = [
  'level: error',
  'service.name:(orders-api or payments-mobile)',
  'latency_ms >= 500',
  'not level:debug and message:"connection refused"',
  'k8s.pod.name:branch-teller-* AND NOT (level:info OR level:debug)',
  'trace.id:*',
  '(level:error OR',
  'body:/time.?out/ AND foo~2 AND bar^3',
] as const;

describe('/logs q verbatim — KQL/Lucene yazımları URL katmanında dönüşmez (v0.10.1817)', () => {
  it('her yazım sig + yazılan URL + geri okumada aynı', () => {
    const rows = GOLDEN_QUERIES.map(q => {
      const p = new URLSearchParams({ q, service: 'orders-api' });
      const st = readLogsUrlState(p);
      const written = writeLogsParams(new URLSearchParams(), st.filter, st.filtersRaw, st.colsRaw);
      expect(readLogsParams(written).search, q).toBe(q);
      return { q, sig: st.sig, written: written.toString() };
    });
    expect(rows).toMatchInlineSnapshot(`
      [
        {
          "q": "level: error",
          "sig": "["orders-api","","level: error",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=level%3A+error",
        },
        {
          "q": "service.name:(orders-api or payments-mobile)",
          "sig": "["orders-api","","service.name:(orders-api or payments-mobile)",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=service.name%3A%28orders-api+or+payments-mobile%29",
        },
        {
          "q": "latency_ms >= 500",
          "sig": "["orders-api","","latency_ms >= 500",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=latency_ms+%3E%3D+500",
        },
        {
          "q": "not level:debug and message:"connection refused"",
          "sig": "["orders-api","","not level:debug and message:\\"connection refused\\"",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=not+level%3Adebug+and+message%3A%22connection+refused%22",
        },
        {
          "q": "k8s.pod.name:branch-teller-* AND NOT (level:info OR level:debug)",
          "sig": "["orders-api","","k8s.pod.name:branch-teller-* AND NOT (level:info OR level:debug)",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=k8s.pod.name%3Abranch-teller-*+AND+NOT+%28level%3Ainfo+OR+level%3Adebug%29",
        },
        {
          "q": "trace.id:*",
          "sig": "["orders-api","","trace.id:*",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=trace.id%3A*",
        },
        {
          "q": "(level:error OR",
          "sig": "["orders-api","","(level:error OR",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=%28level%3Aerror+OR",
        },
        {
          "q": "body:/time.?out/ AND foo~2 AND bar^3",
          "sig": "["orders-api","","body:/time.?out/ AND foo~2 AND bar^3",0,"","",false,"","","",""]",
          "written": "service=orders-api&q=body%3A%2Ftime.%3Fout%2F+AND+foo%7E2+AND+bar%5E3",
        },
      ]
    `);
  });
});
