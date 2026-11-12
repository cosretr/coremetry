package thanos

// jboss_ds.go — v0.10.2043 (servis sağlık kontrolü dilim 3): JBoss / WildFly
// datasource bağlantı havuzu PromQL kurucuları. SAF.
//
// Metrik adları prod'da DOĞRULANMADI (repo'da yalnız test fikstürü
// jboss_datasources_pool_active_count vardı). Bu yüzden sabit ad listesi
// YOK: önce servis pod'larında aile adlarını CANLI keşfeder
// (JBossDSDiscoveryQuery — tek count by (__name__), jmxDiscoveryQuery
// emsali), değer sorgusu yalnız bulunan adlarla koşar. Kabul edilen aile:
//
//	(jboss|wildfly)_datasources_(pool|jdbc)_<istatistik>[_total]
//
// jboss_ = JMX exporter adlandırması, wildfly_ = WildFly'ın yerleşik
// /metrics'i; _total MicroProfile sayaç son eki. Ad bulunamazsa kontrol
// "Ölçülemedi — datasource metriği bulunamadı" der; değer uydurulmaz.
//
// İstatistik rolleri (WildFly pool istatistikleri): InUseCount (kullanımdaki
// bağlantı; yoksa ActiveCount yedek), AvailableCount (havuzdan hâlâ
// alınabilir), WaitCount / TimedOut / BlockingFailureCount (birikimli sayaç —
// artış, şimdiki ile `offset` pencere başı farkından).

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// JBoss DS istatistik rolleri.
const (
	JBossDSInUse           = "in_use"
	JBossDSActive          = "active"
	JBossDSAvailable       = "available"
	JBossDSWait            = "wait"
	JBossDSTimedOut        = "timed_out"
	JBossDSBlockingFailure = "blocking_failure"
)

const jbossDSFamilyRe = `(jboss|wildfly)_datasources_(pool|jdbc)_.+`

var jbossDSNameRe = regexp.MustCompile(`^(jboss|wildfly)_datasources_(pool|jdbc)_[a-z0-9_]+$`)

// ValidJBossDSMetric — keşiften gelen ad enjeksiyon kalkanı.
func ValidJBossDSMetric(m string) bool { return jbossDSNameRe.MatchString(m) }

// JBossDSRole — SAF: metrik adı → rol ("" = kontrolde kullanılmaz).
func JBossDSRole(name string) string {
	if !ValidJBossDSMetric(name) {
		return ""
	}
	s := strings.TrimSuffix(name, "_total")
	i := strings.Index(s, "_datasources_pool_")
	if i < 0 {
		return ""
	}
	switch s[i+len("_datasources_pool_"):] {
	case "in_use_count":
		return JBossDSInUse
	case "active_count":
		return JBossDSActive
	case "available_count":
		return JBossDSAvailable
	case "wait_count":
		return JBossDSWait
	case "timed_out", "timed_out_count":
		return JBossDSTimedOut
	case "blocking_failure_count":
		return JBossDSBlockingFailure
	}
	return ""
}

// jbossDSSelector — servis pod'ları + namespace'leri + cluster kalkanı.
func jbossDSSelector(nsFilter string, namespaces []string, podRe string) string {
	return fmt.Sprintf(`pod=~"%s",namespace=~"%s"%s`, escapeLabelValue(podRe), exactAlternation(namespaces), nsMatcher(nsFilter))
}

// JBossDSDiscoveryQuery — SAF: servis pod'larında görünen DS aile adları.
func JBossDSDiscoveryQuery(nsFilter string, namespaces []string, podRe string) string {
	return fmt.Sprintf(`count by (__name__) ({__name__=~"%s",%s})`, jbossDSFamilyRe, jbossDSSelector(nsFilter, namespaces, podRe))
}

// JBossDSValuesQuery — SAF: bulunan adların pod × datasource değerleri
// (offset > 0 → pencere başı; sayaç artışı için). XA datasource'u
// xa_data_source etiketinde taşır (jmxGrouping emsali).
func JBossDSValuesQuery(names []string, nsFilter string, namespaces []string, podRe string, offset time.Duration) string {
	off := ""
	if offset >= time.Minute {
		off = " offset " + promDur(offset)
	}
	return fmt.Sprintf(`sum by (__name__, pod, data_source, xa_data_source) ({__name__=~"%s",%s}%s)`,
		exactAlternation(names), jbossDSSelector(nsFilter, namespaces, podRe), off)
}
