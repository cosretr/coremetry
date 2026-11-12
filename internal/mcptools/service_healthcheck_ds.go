package mcptools

// service_healthcheck_ds.go — v0.10.2043 (servis sağlık kontrolü dilim 3):
// kontrol 11, JBoss / WildFly datasource bağlantı havuzu. Datasource başına
// ayrı satır ("ORDERS_DS (cluster-a)"). Okuma pod adaptöründe
// (ServicePodsOpts.JBossDS): aile adları canlı keşifle bulunur
// (thanos.JBossDSDiscoveryQuery, önbellekli), değerler şimdi + pencere başı.
//
// Kural (spec, eşik sabit):
//
//	doluluk = kullanımda / (kullanımda + kullanılabilir), EN KÖTÜ pod   ≥%80 Dikkat · ≥%95 Sorun
//	kullanılabilir 0 (şimdi)                                            Dikkat; pencere başında da 0 → Sorun
//	bekleyen istek (wait_count) artışı > 0                              Dikkat
//	zaman aşımı / blocking failure artışı > 0                           Sorun
//
// "kullanımda" = in_use_count; yoksa active_count yedek (WildFly'da active
// oluşturulmuş bağlantıdır — boştakileri de sayar, doluluğu fazla gösterebilir;
// satır hangisini kullandığını yazar). Ad bulunamazsa satır "Ölçülemedi —
// datasource metriği bulunamadı" der; değer uydurulmaz.

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/cilcenk/coremetry/internal/thanos"
)

const (
	hcDSWarnPct = 80.0
	hcDSCritPct = 95.0
	hcDSMaxRows = 10 // cluster başına datasource satırı tavanı
)

// DatasourcePool — bir datasource'un pod'lar üstünden özeti.
type DatasourcePool struct {
	Name          string   `json:"name"`
	XA            bool     `json:"xa,omitempty"`
	Pods          int      `json:"pods"`
	UtilPct       *float64 `json:"utilPct,omitempty"` // en kötü pod; nil = hesaplanamadı
	UtilPod       string   `json:"utilPod,omitempty"`
	UtilFromActv  bool     `json:"utilFromActive,omitempty"` // in_use yok, active yedek
	ZeroAvailPods int      `json:"zeroAvailPods,omitempty"`  // şimdi kullanılabilir = 0 olan pod
	ZeroAvailBoth int      `json:"zeroAvailBoth,omitempty"`  // pencere başında da 0
	WaitInc       *float64 `json:"waitInc,omitempty"`        // pencerede artış (pod toplamı); nil = bilinmiyor
	TimedOutInc   *float64 `json:"timedOutInc,omitempty"`
	BlockFailInc  *float64 `json:"blockFailInc,omitempty"`
}

// DatasourcePoolsFrom — SAF: değer sorgusunun şimdiki + pencere başı
// örnekleri → datasource özetleri (ada göre sıralı). prevOK=false → sayaç
// artışları bilinmiyor (nil, sıfır DEĞİL). Sayaç sıfırlanması (pod restart,
// cur < prev) → artış = cur.
func DatasourcePoolsFrom(cur, prev []thanos.Sample, prevOK bool) []DatasourcePool {
	type key struct {
		ds  string
		xa  bool
		pod string
	}
	read := func(ss []thanos.Sample) map[key]map[string]float64 {
		out := map[key]map[string]float64{}
		for _, s := range ss {
			role := thanos.JBossDSRole(s.Labels["__name__"])
			if role == "" {
				continue
			}
			k := key{ds: s.Labels["data_source"], pod: s.Labels["pod"]}
			if k.ds == "" {
				k.ds, k.xa = s.Labels["xa_data_source"], true
			}
			if k.ds == "" {
				continue
			}
			if out[k] == nil {
				out[k] = map[string]float64{}
			}
			out[k][role] += s.Value
		}
		return out
	}
	c, p := read(cur), read(prev)
	type dsKey struct {
		ds string
		xa bool
	}
	pools := map[dsKey]*DatasourcePool{}
	add := func(dst **float64, v float64) {
		if *dst == nil {
			z := 0.0
			*dst = &z
		}
		**dst += v
	}
	keys := make([]key, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].pod < keys[j].pod })
	for _, k := range keys {
		m := c[k]
		dk := dsKey{k.ds, k.xa}
		pl := pools[dk]
		if pl == nil {
			pl = &DatasourcePool{Name: k.ds, XA: k.xa}
			pools[dk] = pl
		}
		pl.Pods++
		inUse, okUse := m[thanos.JBossDSInUse]
		fromActive := false
		if !okUse {
			inUse, okUse = m[thanos.JBossDSActive]
			fromActive = okUse
		}
		avail, okAvail := m[thanos.JBossDSAvailable]
		if okUse && okAvail && inUse+avail > 0 {
			u := math.Round(100*inUse/(inUse+avail)*10) / 10
			if pl.UtilPct == nil || u > *pl.UtilPct {
				pl.UtilPct, pl.UtilPod, pl.UtilFromActv = &u, k.pod, fromActive
			}
		}
		if okAvail && avail == 0 && (okUse && inUse > 0 || !okUse) {
			pl.ZeroAvailPods++
			if pv, ok := p[k][thanos.JBossDSAvailable]; ok && pv == 0 {
				pl.ZeroAvailBoth++
			}
		}
		if !prevOK {
			continue
		}
		for role, dst := range map[string]**float64{thanos.JBossDSWait: &pl.WaitInc, thanos.JBossDSTimedOut: &pl.TimedOutInc, thanos.JBossDSBlockingFailure: &pl.BlockFailInc} {
			cv, ok := m[role]
			if !ok {
				continue
			}
			inc := cv
			if pv, ok := p[k][role]; ok && cv >= pv {
				inc = cv - pv
			} else if !ok {
				// Pencere başında seri yok (yeni pod) — birikimli değerin tamamı
				// pencereye ait sayılmaz; bilinmiyor.
				continue
			}
			add(dst, inc)
		}
	}
	out := make([]DatasourcePool, 0, len(pools))
	for _, pl := range pools {
		out = append(out, *pl)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return !out[i].XA
	})
	return out
}

// hcDatasources — kontrol 11: cluster'ın datasource satırları.
func hcDatasources(service, cluster string, cl ServicePodsCluster) []HealthcheckRow {
	one := func(state, seen string) []HealthcheckRow {
		row := hcBase(service, HCCheckDatasource, cluster)
		row.State, row.Seen = state, seen
		return []HealthcheckRow{row}
	}
	switch {
	case !cl.DSDiscoveryOK:
		seen := "datasource keşfi okunamadı"
		if cl.DSErr != "" {
			seen += ": " + truncateRunes(cl.DSErr, hcErrRunes)
		}
		return one(HCUnknown, seen)
	case len(cl.DSNames) == 0:
		return one(HCUnknown, "datasource metriği bulunamadı (jboss_/wildfly_ datasources_pool ailesi bu pod'larda yok)")
	case cl.DSErr != "":
		return one(HCUnknown, "datasource değerleri okunamadı: "+truncateRunes(cl.DSErr, hcErrRunes))
	case len(cl.DS) == 0:
		return one(HCUnknown, "datasource metriği var ama data_source etiketli seri yok")
	}
	pools := cl.DS
	var rows []HealthcheckRow
	for i, pl := range pools {
		if i >= hcDSMaxRows {
			break
		}
		subject := pl.Name
		if cluster != "" {
			subject += " (" + cluster + ")"
		}
		row := hcBase(service, HCCheckDatasource, subject)
		rows = append(rows, hcDatasourceRow(row, pl))
	}
	if len(pools) > hcDSMaxRows {
		rows[len(rows)-1].Seen += fmt.Sprintf(" · +%d datasource gösterilmedi", len(pools)-hcDSMaxRows)
	}
	return rows
}

func hcDatasourceRow(row HealthcheckRow, pl DatasourcePool) HealthcheckRow {
	state := HCOK
	var parts []string
	short := ""
	if pl.UtilPct != nil {
		u := *pl.UtilPct
		basis := "kullanımda"
		if pl.UtilFromActv {
			basis = "active"
		}
		p := fmt.Sprintf("doluluk %%%.0f (%s/%s+kullanılabilir", u, basis, basis)
		if pl.Pods > 1 {
			p += ", en kötü pod " + pl.UtilPod
		}
		parts = append(parts, p+")")
		switch {
		case u >= hcDSCritPct:
			state, short = HCCrit, pl.Name+" havuzu dolu"
		case u >= hcDSWarnPct:
			state, short = HCWarn, fmt.Sprintf("%s havuzu %%%.0f dolu", pl.Name, u)
		}
	}
	if pl.ZeroAvailPods > 0 {
		if pl.ZeroAvailBoth > 0 {
			state = hcWorse(state, HCCrit)
			parts = append(parts, fmt.Sprintf("%d pod'da kullanılabilir bağlantı 0 (pencere başında da)", pl.ZeroAvailBoth))
		} else {
			state = hcWorse(state, HCWarn)
			parts = append(parts, fmt.Sprintf("%d pod'da kullanılabilir bağlantı 0", pl.ZeroAvailPods))
		}
		if short == "" {
			short = pl.Name + " havuzunda boş bağlantı yok"
		}
	}
	if pl.TimedOutInc != nil && *pl.TimedOutInc > 0 {
		state = hcWorse(state, HCCrit)
		parts = append(parts, fmt.Sprintf("pencerede %.0f zaman aşımı", *pl.TimedOutInc))
		if short == "" {
			short = pl.Name + " bağlantı zaman aşımı"
		}
	}
	if pl.BlockFailInc != nil && *pl.BlockFailInc > 0 {
		state = hcWorse(state, HCCrit)
		parts = append(parts, fmt.Sprintf("pencerede %.0f bloklanma hatası", *pl.BlockFailInc))
		if short == "" {
			short = pl.Name + " bloklanma hatası"
		}
	}
	if pl.WaitInc != nil && *pl.WaitInc > 0 {
		state = hcWorse(state, HCWarn)
		parts = append(parts, fmt.Sprintf("pencerede %.0f bekleyen istek", *pl.WaitInc))
		if short == "" {
			short = pl.Name + " bağlantı bekleniyor"
		}
	}
	measured := pl.UtilPct != nil || pl.ZeroAvailPods > 0 || pl.WaitInc != nil || pl.TimedOutInc != nil || pl.BlockFailInc != nil
	if !measured {
		row.State, row.Seen = HCUnknown, "doluluk hesaplanamadı (in_use/active + available serisi yok) ve sayaç artışı okunamadı"
		return row
	}
	if len(parts) == 0 {
		parts = append(parts, "bekleme / zaman aşımı yok")
	}
	if pl.XA {
		parts = append(parts, "XA")
	}
	row.State, row.Seen = state, strings.Join(parts, "; ")
	if state != HCOK {
		row.Short = short
	}
	return row
}
