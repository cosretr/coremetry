package anomaly

// exception_pods_signal.go — v0.10.847 (operatör-bildirimli, PROD):
// exception "Explain root cause" cevabı POD YOĞUNLAŞMASINI görmüyordu.
// Arayüzün «Pods · nodes» paneli "1 pod · 799 oluşum" derken kanıt
// zinciri (BuildExceptionExplainInput) modele fingerprint/tip/servis/
// trend/deploy taşıyor, pod-instance dağılımını HİÇ taşımıyordu; model
// de kök nedeni yalnız şema/yetki üzerinden kurabiliyordu.
//
// ── NEDEN PAYDA HER ŞEY ─────────────────────────────────────────────
//
// "1 pod" TEK BAŞINA bir sinyal DEĞİL. Servis tek replikalıysa hiçbir
// şey ifade etmez (zaten başka pod yok). 12 pod'dan yalnız biri hata
// veriyorsa ÇOK güçlü bir sinyaldir: pod'a özgü bir durum vardır —
// bayat pod, farklı config, bozuk node, tek pod'da eski sürüm. Aynı
// numerator iki zıt teşhis demek. Bu yüzden yoğunlaşma iddiası payda
// ÖLÇÜLMEDEN üretilmez; ölçülemediğinde de "dağılmış" diye DEĞİL,
// açıkça "ölçülemedi" diye geçer (üç değerli kind).
//
// ── PAYDA NEREDEN ───────────────────────────────────────────────────
//
// entity_seen_5m (chstore.EntitySeenForService). Üç nedenle:
//
//  1. AYNI KİMLİK UZAYI. MV'nin kendisi spans'tan (cluster,
//     k8s_namespace, k8s_pod) üçlüsüyle türüyor ve WHERE k8s_pod boş
//     değil süzgeci, ExceptionPods'un "pod boşsa NoContext" kuralıyla
//     birebir aynı nüfusu tanımlıyor. İki farklı kimlik uzayında
//     hesaplanan bir oran YALAN olurdu.
//  2. MV-ÖNCE invariant'ı. Payda bir AGREGAT; ham spans üzerinde
//     ikinci bir groupUniq taraması açmak milyar-satır ölçeğinde bug
//     sayılır. MV zaten 5dk kovalarında hazır, sorgu zaman-sınırlı +
//     LIMIT + max_execution_time.
//  3. DOĞRU NÜFUS. "Pencerede span basmış pod" tam olarak numerator'ın
//     çekildiği havuz. Span basmamış bir pod zaten exception üretemez;
//     onu paydaya koymak yoğunlaşmayı yapay olarak güçlendirirdi.
//
// Elenen adaylar: chstore.ServiceInstances metric_points/host_name
// okuyor — BAŞKA bir kimlik (host adı, k8s pod'u değil) ve runtime
// metriği basmayan serviste boş döner, yani "ölçülemedi"yi yanlışlıkla
// tetiklerdi. GetPodInventory cluster geneli, servise daralmıyor.
//
// ── MALİYET ─────────────────────────────────────────────────────────
//
// Explain başına EK İKİ okuma: (a) GetExceptionGroupPods — arayüzdeki
// panelin zaten koştuğu, grubun kendi penceresiyle sınırlı, tavanlı ham
// spans taraması; (b) bir MV okuması. İkisi de kendi zaman aşımıyla
// sarılı ve YUMUŞAK DÜŞER: okuma hatası explain'i düşürmez, yalnız bu
// kanıt "ölçülemedi" olur (v0.10.820 sözleşmesi). Numerator boşsa
// payda sorgusu HİÇ koşmaz.

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/chstore"
)

// Yoğunlaşma kararının üç değeri. Üçüncüsü bilinçli: iki değerli bir
// bayrak, "bakamadık" hâlini zorunlu olarak "yoğunlaşma yok" diye
// okutur ve model sessizce yanlış bir negatif kanıt alır.
const (
	PodConcYogunlasma = "yogunlasma"
	PodConcDagilmis   = "dagilmis"
	PodConcOlculemedi = "olculemedi"
)

// podConcentrationMinShare — en yoğun instance'ın payı için eşik.
//
// Neden 0.80: payda ≥ 2 iken trafiğin (ve dolayısıyla hataların) pod'lar
// arasında kabaca eşit dağılması NORMAL hâldir; 2 replikada 55/45 düz
// gürültüdür. %80, iki replikada 4:1'lik bir çarpıklık demektir ve
// artık "hata trafiği takip ediyor" ile açıklanamaz — pod'a özgü bir
// durum aranmalıdır. Eşiği daha aşağı çekmek (ör. %50) üç replikalı her
// serviste rastgele bir pod'u şüpheli ilan ederdi.
const podConcentrationMinShare = 0.80

// podConcentrationMinOccurrences — oranın anlamlı sayılması için gereken
// en az (pod bağlamı olan) oluşum. 2 oluşumun ikisinin de aynı pod'dan
// gelmesi %100'dür ve hiçbir şey söylemez; küçük örneklemde oran
// tamamen tesadüftür.
const podConcentrationMinOccurrences = 10

// entitySeenServiceRowCap — chstore.EntitySeenForService'in satır tavanı
// (o sorgudaki LIMIT). Payda tam tavanda döndüyse gerçek instance sayısı
// daha yüksek olabilir ve bunu İLAN ederiz. Not: oradaki LIMIT değişirse
// bu karşılaştırma yalnız NOTU düşürür, asla yanlış bir sayı üretmez.
const entitySeenServiceRowCap = 500

// podEvidenceTimeout — iki okumanın her birine ayrı duvar saati. Explain
// yolu LLM'e gidiyor; kanıt toplama aşaması operatörü bekletmemeli.
const podEvidenceTimeout = 8 * time.Second

// Dürüstlük notları. Sinyalin sınırları METİN olarak modele gider:
// ExceptionPods kendi sınırlarını zaten ilan ediyor, kanıt onları
// TAŞIMAK ve renderlayıcı SÖYLEMEK zorunda — yoksa sinyal yalan söyler.
const (
	notePodSchemaMissing     = "k8s kolonları yok (0011 uygulanmamış) — pod dağılımı ÖLÇÜLEMEDİ"
	notePodsUnreadable       = "pod dağılımı okunamadı"
	noteAllNoContext         = "hiçbir oluşum pod bağlamı taşımıyor"
	noteSampled              = "tarama tavanı doldu — sayılar yalnız EN YENİ satırlar üzerinden, oran YAKLAŞIK"
	noteTruncated            = "pod tavanı doldu — gerçek pod sayısı gösterilenden fazla; tavan dışı pod'ların oluşumları orana girmediği için pay bir miktar YÜKSEK çıkar"
	noteNoContextFmt         = "%d oluşum pod bağlamı taşımıyor; oran yalnız bağlamı olan %d oluşum üzerinden"
	noteHostOnly             = "en yoğun kayıt Kubernetes pod'u DEĞİL, host.name yedeğinden geldi"
	noteDenomUnmeasured      = "servisin penceredeki instance sayısı (payda) ölçülemedi"
	noteDenomInconsistentFmt = "payda (%d) gözlenen pod sayısından (%d) küçük — iki okuma çelişiyor"
	noteTooFewFmt            = "yalnız %d oluşum (eşik %d) — oran yoğunlaşma iddiası için yetersiz"
	noteSingleInstance       = "servisin bu pencerede ölçülen TEK instance'ı var — tüm oluşumların aynı yerden gelmesi bir sinyal DEĞİL"
	noteDenomAtCap           = "payda okuma tavanına dayandı — gerçek instance sayısı daha yüksek olabilir"
)

// PodDenominator — paydanın ÖLÇÜM hâli. Measured ayrı bir alan çünkü
// "0 instance" ile "ölçemedik" aynı şey değil: sıfır, sayı gibi
// davranıp sessizce bölme/karşılaştırmaya girerdi.
type PodDenominator struct {
	Instances int
	Measured  bool
	AtCap     bool // okuma satır tavanına dayandı → gerçek sayı ≥ Instances
}

// PodConcentration — kararın kendisi + onu okumak için gereken her
// sınır. Ham 50 satır MODELE DÖKÜLMEZ: özet + en yoğun tek kayıt.
type PodConcentration struct {
	Kind           string
	TopPod         string
	TopNode        string
	TopCluster     string
	TopNamespace   string
	TopHostOnly    bool // Kubernetes pod'u değil, host.name yedeği
	TopOccurrences int64
	Attributed     int64   // pod bağlamı OLAN oluşum (oranın paydası)
	Share          float64 // TopOccurrences / Attributed, 0..1
	PodsWithHits   int     // hata üreten farklı instance sayısı
	Instances      int     // servisin penceredeki instance sayısı; 0 = ölçülemedi
	Notes          []string
}

// podConcentration — SAF karar. Girdi düz veri, çıktı düz veri; tablo
// testli (exception_pods_signal_test.go).
//
// İddia merdiveni — bir "yoğunlaşma" ancak ŞUNLARIN HEPSİ doğruyken
// üretilir: şema var VE pod bağlamı var VE payda ÖLÇÜLDÜ VE payda
// gözlemle tutarlı VE payda > 1 VE örneklem yeterli VE pay eşiğin
// üstünde. Herhangi biri düşerse iddia YOKTUR; kind'in "ölçülemedi" mi
// "dağılmış" mı olduğu, düşenin ÖLÇÜM mü SONUÇ mu olduğuna bakar.
func podConcentration(p chstore.ExceptionPods, d PodDenominator) PodConcentration {
	c := PodConcentration{PodsWithHits: len(p.Rows)}

	// 1) Şema kapısı en önde: k8s kolonları yokken elimizdeki satırlar
	//    zaten boştur, "tek pod" demek uydurma olurdu.
	if p.SchemaMissing {
		c.Kind, c.Notes = PodConcOlculemedi, []string{notePodSchemaMissing}
		return c
	}
	// 2) Hiç pod satırı yok: oluşumlar var ama hiçbiri bağlam taşımıyor
	//    (ya da tarama boş döndü). Her iki hâlde de ÖLÇÜLEMEDİ.
	if len(p.Rows) == 0 {
		c.Kind = PodConcOlculemedi
		if p.NoContext > 0 || p.Total > 0 {
			c.Notes = []string{noteAllNoContext}
		} else {
			c.Notes = []string{notePodsUnreadable}
		}
		return c
	}

	// En yoğun kayıt, girdinin SIRASINA güvenmeden seçilir (saf
	// fonksiyon çağıranın sıralamasını sözleşme saymaz); eşitlikte ad
	// sırası — aynı girdi her koşuda aynı cevabı versin.
	top := p.Rows[0]
	var attributed int64
	for _, r := range p.Rows {
		attributed += r.Occurrences
		if r.Occurrences > top.Occurrences || (r.Occurrences == top.Occurrences && r.Pod < top.Pod) {
			top = r
		}
	}

	// Şekil notları: karar ne olursa olsun modelin BİLMESİ gerekenler.
	var notes []string
	if p.Sampled {
		notes = append(notes, noteSampled)
	}
	if p.Truncated {
		notes = append(notes, noteTruncated)
	}
	if p.NoContext > 0 {
		notes = append(notes, fmt.Sprintf(noteNoContextFmt, p.NoContext, attributed))
	}
	if top.HostOnly {
		notes = append(notes, noteHostOnly)
	}
	if d.Measured && d.AtCap {
		notes = append(notes, noteDenomAtCap)
	}

	// ÖLÇÜLEMEDİ dalları pay/pod SIZDIRMAZ: sızdırsa renderlayıcı
	// "ima etme" sözleşmesini tutamaz — sayıyı gören model kendi
	// oranını kurar.
	unmeasured := func(n string) PodConcentration {
		c.Kind, c.Notes = PodConcOlculemedi, append(notes, n)
		return c
	}
	switch {
	case !d.Measured:
		return unmeasured(noteDenomUnmeasured)
	case d.Instances < len(p.Rows):
		// Payda, hata ÜRETEN pod sayısından küçük olamaz. Olduysa iki
		// okuma farklı gerçekleri anlatıyordur (MV gecikmesi/TTL) —
		// hangisinin doğru olduğunu bilmeden oran üretmeyiz.
		return unmeasured(fmt.Sprintf(noteDenomInconsistentFmt, d.Instances, len(p.Rows)))
	}

	c.TopPod, c.TopNode, c.TopCluster = top.Pod, top.Node, top.Cluster
	c.TopNamespace, c.TopHostOnly, c.TopOccurrences = top.Namespace, top.HostOnly, top.Occurrences
	c.Attributed, c.Instances, c.Notes = attributed, d.Instances, notes
	if attributed > 0 {
		c.Share = float64(top.Occurrences) / float64(attributed)
	}

	switch {
	case d.Instances <= 1:
		// Tek instance: "hepsi aynı pod'dan" tanım gereği doğru ve
		// TAMAMEN bilgisizdir. Örneklem büyüklüğünden ÖNCE bakılır —
		// payda 1 iken oranın kaç oluşumdan hesaplandığı önemsiz.
		c.Kind = PodConcDagilmis
		c.Notes = append(c.Notes, noteSingleInstance)
	case attributed < podConcentrationMinOccurrences:
		c.Kind = PodConcOlculemedi
		c.Notes = append(c.Notes, fmt.Sprintf(noteTooFewFmt, attributed, podConcentrationMinOccurrences))
		c.TopPod, c.TopNode, c.TopCluster, c.TopNamespace = "", "", "", ""
		c.TopOccurrences, c.Share, c.TopHostOnly = 0, 0, false
	case c.Share >= podConcentrationMinShare:
		c.Kind = PodConcYogunlasma
	default:
		c.Kind = PodConcDagilmis
	}
	return c
}

// pctFloorTR — payı yüzde olarak, TÜRKÇE yazımla (%80) ve AŞAĞI
// yuvarlayarak. Yukarı yuvarlama %99.6'yı "%100" yapar ve "%100" bir
// operatör için "istisnasız hepsi" demektir — ölçmediğimiz bir kesinlik.
func pctFloorTR(share float64) string {
	return fmt.Sprintf("%%%d", int(math.Floor(share*100)))
}

// podLabel — en yoğun kaydın okunabilir kimliği. HostOnly satır bir
// Kubernetes pod'u DEĞİL; "pod" demek modeli olmayan bir kubectl
// komutuna iter.
func podLabel(c PodConcentration) string {
	word := "pod"
	if c.TopHostOnly {
		word = "host"
	}
	var parts []string
	if c.TopNode != "" {
		parts = append(parts, "node "+c.TopNode)
	}
	if c.TopNamespace != "" {
		parts = append(parts, "ns "+c.TopNamespace)
	}
	if c.TopCluster != "" {
		parts = append(parts, "cluster "+c.TopCluster)
	}
	out := word + ": " + c.TopPod
	if len(parts) > 0 {
		out += " (" + strings.Join(parts, ", ") + ")"
	}
	return out
}

// instanceCountTR — paydanın yazımı; tavana dayandıysa "en az N".
func instanceCountTR(c PodConcentration, atCap bool) string {
	if atCap {
		return fmt.Sprintf("en az %d", c.Instances)
	}
	return fmt.Sprintf("%d", c.Instances)
}

// renderPodConcentration — prompt bloğu. SAF. Üç kind ÜÇ AYRI cümle
// üretir; "ölçülemedi" cümlesi hiçbir koşulda "dağılmış" gibi okunmaz.
//
// Blok bir GÖZLEM olarak etiketlenir, SONUÇ olarak değil: kök nedeni
// model kendi kurar, bizim işimiz sinyali önüne koymak. Yorumlama
// talimatı prompt'un sistem yarısında (internal/copilot/prompts.go) —
// bu fonksiyon yalnız olguyu yazar.
func renderPodConcentration(c PodConcentration) string {
	if c.Kind == "" {
		return "" // hiç hesaplanmadı → blok basılmaz (prompt bayt-paritesi)
	}
	atCap := false
	for _, n := range c.Notes {
		if n == noteDenomAtCap {
			atCap = true
		}
	}
	var sb strings.Builder
	sb.WriteString("\n\nPod/INSTANCE dağılımı (GÖZLEM, sonuç değil): ")
	switch c.Kind {
	case PodConcYogunlasma:
		fmt.Fprintf(&sb, "oluşumların %s'i (%d/%d) TEK instance üzerinde toplanmış — %s. "+
			"Servisin aynı pencerede ölçülen instance sayısı %s.",
			pctFloorTR(c.Share), c.TopOccurrences, c.Attributed, podLabel(c), instanceCountTR(c, atCap))
	case PodConcDagilmis:
		if c.Instances <= 1 {
			fmt.Fprintf(&sb, "servisin bu pencerede ölçülen instance sayısı 1 — "+
				"oluşumların tek yerden gelmesi tanım gereği, ayırt edici bir bilgi taşımıyor.")
			break
		}
		fmt.Fprintf(&sb, "oluşumlar ölçülen %s instance'ın %d tanesine yayılmış; "+
			"en yoğun olanın payı %s (%d/%d) — %s.",
			instanceCountTR(c, atCap), c.PodsWithHits, pctFloorTR(c.Share),
			c.TopOccurrences, c.Attributed, podLabel(c))
	case PodConcOlculemedi:
		sb.WriteString("ÖLÇÜLEMEDİ. Bu, oluşumların instance'lara dağılmış olduğu ANLAMINA GELMEZ; " +
			"yoğunlaşma konusunda hiçbir yönde çıkarım yapma.")
	}
	if len(c.Notes) > 0 {
		sb.WriteString(" Ölçüm sınırları: " + strings.Join(c.Notes, "; ") + ".")
	}
	return sb.String()
}

// ── okuma seam'i ────────────────────────────────────────────────────

// podEvidenceReader — iki okumanın dar yüzeyi. Arayüz olması test
// içindir: okuma DÜŞTÜĞÜNDE explain'in çalışmaya devam ettiğini ancak
// düşen bir okuma kurarak kanıtlayabiliriz. *chstore.Store karşılar.
type podEvidenceReader interface {
	GetExceptionGroupPods(ctx context.Context, fingerprint string) (chstore.ExceptionPods, error)
	EntitySeenForService(ctx context.Context, service string, clusterValues []string, from, to time.Time) ([]chstore.EntitySeenAgg, error)
}

// podClusterValues — paydanın cluster KAPSAMI, numerator'ın gördüğü
// cluster'lardan türer. Kapsamsız bir payda sorgusu, aynı servis adını
// taşıyan BAŞKA cluster'ların pod'larını da sayar ve yoğunlaşmayı
// uydurur (kapsanmayan alt sorgu sınıfı). Boş dize de geçerli bir
// değerdir: tek-cluster kurulumda spans.cluster boştur ve MV satırı da
// boş taşır — listeden düşürmek o kurulumda paydayı sıfırlardı.
func podClusterValues(rows []chstore.ExceptionPodRow) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 4)
	for _, r := range rows {
		if !seen[r.Cluster] {
			seen[r.Cluster] = true
			out = append(out, r.Cluster)
		}
	}
	sort.Strings(out)
	return out
}

// buildPodConcentration — iki okuma + saf karar. Her okuma yumuşak
// düşer: hata hâlinde kanıt "ölçülemedi" olur, explain KOŞMAYA DEVAM
// EDER (v0.10.820: doğrulama okuması hatası ≠ iş başarısız).
func buildPodConcentration(ctx context.Context, r podEvidenceReader, fingerprint, service string) PodConcentration {
	if r == nil {
		return PodConcentration{Kind: PodConcOlculemedi, Notes: []string{notePodsUnreadable}}
	}
	pctx, cancel := context.WithTimeout(ctx, podEvidenceTimeout)
	pods, err := r.GetExceptionGroupPods(pctx, fingerprint)
	cancel()
	if err != nil {
		return PodConcentration{Kind: PodConcOlculemedi, Notes: []string{notePodsUnreadable}}
	}

	// Payda sorgusu YALNIZ numerator bir şey bulduysa koşar: pod satırı
	// yokken karar zaten ölçülemedi, MV okuması boşa maliyet olurdu.
	var d PodDenominator
	if len(pods.Rows) > 0 && service != "" && !pods.From.IsZero() && !pods.To.IsZero() {
		dctx, dcancel := context.WithTimeout(ctx, podEvidenceTimeout)
		seen, derr := r.EntitySeenForService(dctx, service, podClusterValues(pods.Rows), pods.From, pods.To)
		dcancel()
		// Boş sonuç ÖLÇÜM DEĞİL: entity MV'si yoksa, TTL'i pencerenin
		// gerisindeyse ya da kolon terfisi yapılmamışsa 0 satır döner.
		// Bunu "0 instance" saymak, bölmeyi de karşılaştırmayı da
		// sessizce bozardı.
		if derr == nil && len(seen) > 0 {
			d = PodDenominator{Instances: len(seen), Measured: true, AtCap: len(seen) >= entitySeenServiceRowCap}
		}
	}
	return podConcentration(pods, d)
}
