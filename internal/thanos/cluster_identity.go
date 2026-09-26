package thanos

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// cluster_identity.go — REMOTE CLUSTER KAYDI = ENTITY HİYERARŞİSİNİN KÖKÜ
// (v0.10.128, K8s entity katmanı adım 2; design §1.1, keşif engel #2).
//
// Keşif: kayıtta id YOKTU, `Name` join anahtarıydı; ad değişince kimlik
// kırılıyordu; Thanos serisi ↔ kayıt ↔ span cluster değeri için eşleme
// alanı yoktu. Üç karar:
//
//   ID          opak, DEĞİŞMEZ. Kayıt oluşturulurken üretilir; mevcut
//               kayıtlar için boot'ta Name'den türetilir ve bloba BİR KEZ
//               yazılır (BackfillClusterIDs). Name sonradan değişse de id
//               kalır — entity_id'nin birinci bileşeni.
//   Thanos      ThanosLabelName BOŞ = matcher yok = eski davranış (cluster
//   etiketi     başına URL). Dolu ise doQuery her seçiciye
//               <ad>="<değer>" enjekte eder (cluster_matcher.go); değer
//               boşsa Name.
//   Span değeri SpanClusterValue boşsa Name (bugünkü join anahtarı).
//
// Geriye dönük doldurma = türetilmiş varsayılanlar; satır yeniden
// yazılmaz, yalnız boş ID doldurulur.

var promLabelNameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// derivedClusterID — "c-" + fnv64a(Name) ilk 8 hex. Boş ad → boş id.
func derivedClusterID(name string) string {
	if name == "" {
		return ""
	}
	h := fnv.New64a()
	h.Write([]byte(name))
	return fmt.Sprintf("c-%08x", h.Sum64()&0xffffffff)
}

// EffectiveID — kayıtlı ID ya da Name'den türetilmiş id.
func (c ClusterConfig) EffectiveID() string {
	if c.ID != "" {
		return c.ID
	}
	return derivedClusterID(c.Name)
}

// EffectiveThanosLabel — (etiket adı, değeri). Ad boşsa ("", "") =
// enjeksiyon yok. Değer boşsa Name.
func (c ClusterConfig) EffectiveThanosLabel() (string, string) {
	if c.ThanosLabelName == "" {
		return "", ""
	}
	if c.ThanosLabelValue != "" {
		return c.ThanosLabelName, c.ThanosLabelValue
	}
	return c.ThanosLabelName, c.Name
}

// SpanClusterKey — span `cluster` kolonunda bu cluster'ın BİRİNCİL değeri
// (listenin ilki). Eşleşme için SpanClusterKeys / MatchesSpanCluster.
func (c ClusterConfig) SpanClusterKey() string {
	return c.SpanClusterKeys()[0]
}

// SpanClusterKeys — v0.10.139: kaydın TÜM span cluster değerleri
// (SpanClusterValue + SpanClusterValues, tekil, boşlar atılmış); hiçbiri
// yoksa Name. Okuma tarafı haritaları BUNU anahtarlar — atama geriye
// dönük çalışır (tarihsel entity_seen satırları okuma anında bağlanır).
func (c ClusterConfig) SpanClusterKeys() []string {
	out := make([]string, 0, 1+len(c.SpanClusterValues))
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(c.SpanClusterValue)
	for _, v := range c.SpanClusterValues {
		add(v)
	}
	if len(out) == 0 {
		out = append(out, c.Name)
	}
	return out
}

// ExplicitSpanClusterValues — kayda AÇIKÇA yazılmış değerler (Name yedeği
// HARİÇ). Snapshot/Settings formu bunu gösterir: yedeği değer gibi
// göstermek, yeniden kaydetmede eski adı kalıcı değere çevirirdi (inceleme).
func (c ClusterConfig) ExplicitSpanClusterValues() []string {
	out := make([]string, 0, 1+len(c.SpanClusterValues))
	seen := map[string]bool{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(c.SpanClusterValue)
	for _, v := range c.SpanClusterValues {
		add(v)
	}
	return out
}

// MatchesSpanCluster — değer bu kayda mı ait.
func (c ClusterConfig) MatchesSpanCluster(v string) bool {
	for _, k := range c.SpanClusterKeys() {
		if k == v {
			return true
		}
	}
	return false
}

// SpanClusterOwner — değerin bağlı olduğu kayıt (teklik kuralının okuma yüzü).
func SpanClusterOwner(cfg Settings, value string) (ClusterConfig, bool) {
	for _, c := range cfg.Clusters {
		if c.MatchesSpanCluster(value) {
			return c, true
		}
	}
	return ClusterConfig{}, false
}

// ValidThanosLabelName — PUT doğrulaması: boş ya da Prometheus etiket adı.
func ValidThanosLabelName(s string) bool {
	return s == "" || promLabelNameRe.MatchString(s)
}

// BackfillClusterIDs — boş ID'leri türetir. Girdi değişmez; changed=true
// ise çağıran tam-blob yazar (SavePersisted). Adsız kayıt dokunulmaz.
func BackfillClusterIDs(cfg Settings) (Settings, bool) {
	out := Settings{Clusters: make([]ClusterConfig, len(cfg.Clusters))}
	copy(out.Clusters, cfg.Clusters)
	changed := false
	for i := range out.Clusters {
		if out.Clusters[i].ID == "" && out.Clusters[i].Name != "" {
			out.Clusters[i].ID = derivedClusterID(out.Clusters[i].Name)
			changed = true
		}
	}
	return out, changed
}

// ClusterByID — etkin kayıt, id ile.
func (s *Service) ClusterByID(id string) (ClusterConfig, bool) {
	if id == "" {
		return ClusterConfig{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.cfg.Clusters {
		if c.Enabled && c.EffectiveID() == id {
			return c, true
		}
	}
	return ClusterConfig{}, false
}

// ClusterByRef — `?cluster=` id YA DA ad taşıyabilir (eski URL'ler ad
// taşır; yeni yüzeyler id). Önce id, sonra ad.
func (s *Service) ClusterByRef(ref string) (ClusterConfig, bool) {
	if c, ok := s.ClusterByID(ref); ok {
		return c, true
	}
	return s.ClusterByName(ref)
}

// ReconcileClusterSettings — PUT'un kimlik/gizli birleştirmesi (saf).
//
// ID sunucu sahipli:
//
//	(a) gelen ID saklı bir kayda aitse → yeniden adlandırma: o id ve
//	    (gelen token boşsa) o kaydın token'ı korunur;
//	(b) aynı AD saklıysa → saklı id + (boş token yerine) saklı token;
//	(c) yeni kayıt → Name'den türetilmiş id; istemcinin gönderdiği
//	    yabancı id alınmaz.
//
// Etiket adı Prometheus etiket sözdizimine uymuyorsa hata (boş serbest).
func ReconcileClusterSettings(in, cur Settings) (Settings, error) {
	byID := map[string]ClusterConfig{}
	byName := map[string]ClusterConfig{}
	for _, c := range cur.Clusters {
		if id := c.EffectiveID(); id != "" {
			byID[id] = c
		}
		byName[c.Name] = c
	}
	out := Settings{Clusters: make([]ClusterConfig, len(in.Clusters))}
	copy(out.Clusters, in.Clusters)
	for i := range out.Clusters {
		c := &out.Clusters[i]
		if !ValidThanosLabelName(c.ThanosLabelName) {
			return Settings{}, fmt.Errorf("thanosLabelName %q geçersiz (Prometheus etiket adı bekleniyor)", c.ThanosLabelName)
		}
		var prev ClusterConfig
		var found bool
		if c.ID != "" {
			prev, found = byID[c.ID]
		}
		if !found {
			prev, found = byName[c.Name]
		}
		switch {
		case found:
			c.ID = prev.EffectiveID()
			if c.Token == "" {
				c.Token = prev.Token
			}
			carryRolloutFields(c, prev)
			// v0.10.139 — otomatik algılama alanları sunucu sahipli: istemci
			// göndermediyse saklı değer korunur; etiket ELLE değiştirildiyse
			// kaynak manual'a düşer (auto rozeti yalan söylemesin).
			if c.ThanosLabelSource == "" {
				c.ThanosLabelSource, c.ThanosLabelDetectedAt = prev.ThanosLabelSource, prev.ThanosLabelDetectedAt
				// ETKİN çift karşılaştırılır: değer boşken ad değişimi de matcher'ı
				// değiştirir (inceleme) — auto rozeti o durumda da düşer.
				pl, pv := prev.EffectiveThanosLabel()
				cl, cv := c.EffectiveThanosLabel()
				if prev.ThanosLabelSource == "auto" && (cl != pl || cv != pv) {
					c.ThanosLabelSource, c.ThanosLabelDetectedAt = "manual", 0
				}
			}
		default:
			c.ID = derivedClusterID(c.Name)
		}
		// Liste kanonik: yalnız AÇIK değerler (tekil, boşsuz); SpanClusterValue =
		// ilk eleman. Açık değer yoksa Name yedeği örtük kalır (listeye yazılmaz).
		if keys := c.ExplicitSpanClusterValues(); len(keys) > 0 {
			c.SpanClusterValue, c.SpanClusterValues = keys[0], keys
		} else {
			c.SpanClusterValue, c.SpanClusterValues = "", nil
		}
		// v0.10.956 — Rollouts v2 alanları: normalise + biçim doğrulaması
		// (tekillik aşağıda, tüm liste üstünde).
		if err := normalizeRolloutFields(c); err != nil {
			return Settings{}, err
		}
	}
	// v0.10.139 — TEKLİK: bir span cluster değeri ve bir (etiket, değer) çifti
	// aynı anda yalnız BİR kayda. Çakışma reddedilir, bağlı kayıt söylenir.
	if err := checkClusterUniqueness(out); err != nil {
		return Settings{}, err
	}
	return out, nil
}

// v0.10.956 — Rollouts v2 P1.3 alan sınırları (docs/rollouts/v2-audit.md
// §3.2). Blob tüm pod'lara 30 s'de bir yüklenir; sınırlar onu küçük tutar.
const (
	apiServerURLsMax = 16 // bir cluster'ın dest_server yazımları; gerçekte 1-3
	argoSuffixMax    = 63 // Argo uygulama adı bir k8s adı (DNS etiketi ≤ 63)
	pairGroupMax     = 64 // rune
)

// argoSuffixRe — v0.10.956 — suffix bir Argo uygulama adının parçası:
// harf/rakamla başlar ve biter, arada '.', '_' ya da '-'. Büyük harfe izin
// var (tekillik harf duyarsız); boşluk yok, '.' dışında regex meta karakteri
// yok — P3 yine de QuoteMeta ile kaçırır (audit §6), değer bir ad parçası.
var argoSuffixRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// carryRolloutFields — v0.10.956 — eski istemci / karışık sürüm koruması
// (audit §3.2: "bir Save ya da karışık sürüm pod'u alanları siler").
//
// İşaret `apiServerUrls` ANAHTARININ varlığı: alanları bilen istemci
// (ClustersTab, GET → PUT gidiş-dönüşü — Snapshot listeyi hep [] ile basar)
// anahtarı HER ZAMAN gönderir; JSON'da `[]` Go'da nil OLMAYAN boş dilimdir.
//   - liste nil (anahtar yok = eski istemci ya da alan bilmeyen betik):
//     liste saklı değerden taşınır; boş metin alanları da taşınır, dolu
//     metin uygulanır (kısmi betik gövdesi yalnız suffix'i değiştirebilir);
//   - liste nil değil: gövde YETKİLİ — [] ve "" temizler.
//
// Ters yön (eski pod yeni istemcinin gövdesini işlerse) buradan
// düzeltilemez: eski ikili alanları tanımaz, blobu onlarsız yazar. Pencere
// bu sürüme geçişin rolling-upgrade süresi (ya da eski sürüme geri dönüş).
func carryRolloutFields(c *ClusterConfig, prev ClusterConfig) {
	if c.APIServerURLs != nil {
		return
	}
	c.APIServerURLs = prev.APIServerURLs
	if strings.TrimSpace(c.ArgoSuffix) == "" {
		c.ArgoSuffix = prev.ArgoSuffix
	}
	if strings.TrimSpace(c.PairGroup) == "" {
		c.PairGroup = prev.PairGroup
	}
}

// normalizeRolloutFields — v0.10.956 — saf; Reconcile'ın parçası, böylece
// üç blob yazıcısı (PUT, detect, assign-span-cluster) aynı kuraldan geçer.
// Liste kanonikleşir (NormalizeAPIServerURL; boş öğe atılır, tekrar
// tekilleşir). nil-lik KORUNUR: nil kalan liste "anahtar yok" demektir ve
// ikinci bir Reconcile (PUT'un otomatik algılama kancası) aynı sonucu verir;
// açıkça temizlenen liste boş-ama-nil-değil kalır, ikinci geçiş onu saklı
// değerle DOLDURMAZ. Blob'a ikisi de yazılmaz (omitempty).
func normalizeRolloutFields(c *ClusterConfig) error {
	c.ArgoSuffix = strings.TrimSpace(c.ArgoSuffix)
	c.PairGroup = strings.TrimSpace(c.PairGroup)
	if c.ArgoSuffix != "" && (len(c.ArgoSuffix) > argoSuffixMax || !argoSuffixRe.MatchString(c.ArgoSuffix)) {
		return fmt.Errorf("cluster %q argoSuffix %q geçersiz: harf/rakamla başlayıp biten, en çok %d karakter (arada . _ - olabilir)", c.Name, c.ArgoSuffix, argoSuffixMax)
	}
	if utf8.RuneCountInString(c.PairGroup) > pairGroupMax {
		return fmt.Errorf("cluster %q pairGroup en çok %d karakter olabilir", c.Name, pairGroupMax)
	}
	if strings.IndexFunc(c.PairGroup, unicode.IsControl) >= 0 {
		return fmt.Errorf("cluster %q pairGroup kontrol karakteri (satır sonu vb.) içeremez", c.Name)
	}
	if c.APIServerURLs == nil {
		return nil
	}
	out := make([]string, 0, len(c.APIServerURLs))
	seen := map[string]bool{}
	for i, raw := range c.APIServerURLs {
		if strings.TrimSpace(raw) == "" {
			continue // form listesindeki boş satır
		}
		n, err := NormalizeAPIServerURL(raw)
		if err != nil {
			// Ham değer YANKILANMAZ (userinfo parolası olabilir) — konum yeter.
			return fmt.Errorf("cluster %q apiServerUrls #%d: %v", c.Name, i+1, err)
		}
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) > apiServerURLsMax {
		return fmt.Errorf("cluster %q apiServerUrls en çok %d adres taşıyabilir (%d verildi)", c.Name, apiServerURLsMax, len(out))
	}
	c.APIServerURLs = out
	return nil
}

// checkClusterUniqueness — saf; tablo-testli. Hata metni operatöre gider.
func checkClusterUniqueness(cfg Settings) error {
	spanOwner := map[string]string{}
	labelOwner := map[string]string{}
	apiOwner := map[string]string{}    // v0.10.956 — kanonik API server URL → kayıt
	suffixOwner := map[string]string{} // v0.10.956 — küçük harf argoSuffix → kayıt
	for _, c := range cfg.Clusters {
		// v0.10.956 — Rollouts v2 P1.3: bir dest_server tek cluster'a, bir
		// suffix tek cluster'a çözülmeli (audit §3.2, §6 "suffix → cluster").
		// Değerler Reconcile'da zaten normalise; burada yeniden normalise
		// etmek fonksiyonu tek başına çağrıldığında da doğru tutar.
		for _, u := range c.APIServerURLs {
			k := strings.TrimSpace(u)
			if n, err := NormalizeAPIServerURL(u); err == nil {
				k = n
			}
			if k == "" {
				continue
			}
			// v0.10.956 — küme-içi adres (https://kubernetes.default.svc, her
			// yazımıyla: …svc:443, …svc.cluster.local) Remote Cluster'a
			// YAZILMAZ. Argo CD iki hub'da kurulu (operatör, audit §5.6): bu
			// adres "instance'ın KENDİ hub'ı" demek ve eşlemede instance'ın
			// hubClusterId'sine çözülür (argocd hubs[]). Bir kayda bağlamak iki
			// hub'dan birini yanlış seçerdi; hub'ın dış API adresi yazılır.
			if IsInClusterAPIServerURL(k) {
				return fmt.Errorf("apiServerUrls: %q kaydında %s yazılmaz — Argo'nun küme-içi hedefi instance'ın hub'ına çözülür (Ayarlar › Argo CD, hubs); hub'ın dış API server adresini yazın", c.Name, InClusterAPIServerURL)
			}
			if o, dup := apiOwner[k]; dup && o != c.Name {
				return fmt.Errorf("apiServerUrls: %s zaten %q kaydına bağlı; bir API server adresi aynı anda tek kayda bağlanabilir", k, o)
			}
			apiOwner[k] = c.Name
		}
		if s := strings.ToLower(strings.TrimSpace(c.ArgoSuffix)); s != "" {
			if o, dup := suffixOwner[s]; dup && o != c.Name {
				return fmt.Errorf("argoSuffix %q zaten %q kaydında (büyük/küçük harf duyarsız); bir suffix tek cluster'a çözülmeli", c.ArgoSuffix, o)
			}
			suffixOwner[s] = c.Name
		}
		for _, v := range c.SpanClusterKeys() {
			if o, dup := spanOwner[v]; dup && o != c.Name {
				return fmt.Errorf("span cluster değeri %q zaten %q kaydına bağlı; bir değer aynı anda tek kayda bağlanabilir", v, o)
			}
			spanOwner[v] = c.Name
		}
		if l, v := c.EffectiveThanosLabel(); l != "" {
			k := l + "=" + v
			if o, dup := labelOwner[k]; dup && o != c.Name {
				return fmt.Errorf("thanos etiketi %s=%q zaten %q kaydına bağlı", l, v, o)
			}
			labelOwner[k] = c.Name
		}
	}
	return nil
}

// ProbeCluster — rozet: matcher'lı `count(kube_node_info)` kaç seri
// döndürüyor? (Settings "Test label"; 10 s deadline çağıranda.) Etiket
// adı boşsa matcher yok — yine de URL/token'ı sınar.
func (s *Service) ProbeCluster(ctx context.Context, c ClusterConfig) (int, error) {
	res, err := s.doQuery(ctx, c, "/api/v1/query", url.Values{"query": {"count(kube_node_info)"}})
	if err != nil {
		return 0, err
	}
	if len(res) == 0 || len(res[0].Value) < 2 {
		return 0, nil
	}
	var raw string
	if err := json.Unmarshal(res[0].Value[1], &raw); err != nil {
		return 0, fmt.Errorf("probe değeri çözülemedi: %w", err)
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("probe değeri sayı değil: %q", raw)
	}
	return int(f), nil
}

// Sample — anlık sorgu satırı: etiket seti + değer (entity syncer için
// dışa açık şekil; promSeries paket-içi kalır).
type Sample struct {
	Labels map[string]string
	Value  float64
}

// InstantSamples — matcher'lı anlık sorgu (doQuery enjeksiyonu). Görev
// kısıtı: Thanos'a giden her sorgu seçici taşır — expr'in seçicisi
// çağıranın sorumluluğu (entity.SnapshotQueries); cluster matcher burada.
func (s *Service) InstantSamples(ctx context.Context, c ClusterConfig, expr string) ([]Sample, error) {
	res, err := s.doQuery(ctx, c, "/api/v1/query", url.Values{"query": {expr}})
	if err != nil {
		return nil, err
	}
	out := make([]Sample, 0, len(res))
	for _, r := range res {
		v := 0.0
		if len(r.Value) >= 2 {
			var raw string
			if json.Unmarshal(r.Value[1], &raw) == nil {
				v, _ = strconv.ParseFloat(raw, 64)
			}
		}
		out = append(out, Sample{Labels: r.Metric, Value: v})
	}
	return out, nil
}
