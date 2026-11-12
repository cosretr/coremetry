package api

// dashboard_templates.go — v0.10.1997 (Dashboards Faz 3 dilim 3.3; audit
// §5.5 "Şablon galerisi" + "VM şablonları", mockup görünüm 4).
//
//	GET  /api/dashboards/templates        → {templates:[{id,name,…,missing}], backend}
//	POST /api/dashboards/from-template {id} → yeni pano (custom-* kopya)  [editor+]
//
// (from-chat ile aynı biçim: POST /api/dashboards/templates/{id}
// /api/dashboards/{id}/panels kalıbıyla çakışırdı.)
//
// Dört MetricsQL şablonu (Service RED, JVM, Kafka, Kubernetes). Metrikler
// prod'da YALNIZ VictoriaMetrics'te; sorgular VM'in Prometheus adlarıyla
// yazılır (kafkaMetrics.ts ve http_server_request_duration_seconds_*
// aileleri ürünün başka yüzeylerinde zaten bu adlarla soruluyor).
//
// Ön kontrol: her şablonun "gereken metrikleri" metricSource.MetricExists
// ile denetlenir (VM'de aile adı: _bucket/_count/_total ekleri dahil) ve
// eksikler galeride yazılır — şablon yine kurulabilir, operatör eksik
// paneli bilerek alır. Anahtar backend adını taşır (metricsrc deneme
// paramı dahil, v0.5.187 sınıfı); 5 dk önbellek, şablon kümesi derlemede
// sabit.
//
// NEDEN boot tohumu DEĞİL: hazır 10 pano (dashboard_presets.go) her kurulumda
// listeye girer; MetricsQL şablonları yalnız VM'li kurulumda anlamlı. Galeri
// istenince kopya yaratır, listeyi kirletmez. NEDEN istemci kopyası DEĞİL:
// kopya sunucuda kurulur → tek audit satırı (dashboard.create_from_template),
// şablon gövdesi tarayıcıya liste için taşınmaz.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/cilcenk/coremetry/internal/auth"
	"github.com/cilcenk/coremetry/internal/chstore"
)

func init() { registerRoutesExtra("dashboard-templates", (*Server).registerDashboardTemplateRoutes) }

func (s *Server) registerDashboardTemplateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/dashboards/templates", s.listDashboardTemplates)
	mux.HandleFunc("POST /api/dashboards/from-template", auth.RequireAnyRole(editorRoles, s.createDashboardFromTemplate))
}

// ── Şablon tanımları ────────────────────────────────────────────────────────

type tplPanel struct {
	ID     string         `json:"id"`
	Type   string         `json:"type"`
	Title  string         `json:"title"`
	Desc   string         `json:"description,omitempty"`
	Width  int            `json:"width"`
	Height string         `json:"height,omitempty"`
	Config map[string]any `json:"config"`
}

type tplVariable struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	Type  string `json:"type"`
}

type dashTemplate struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Required    []string // metrik aile adları (ön kontrol)
	Variables   []tplVariable
	Panels      []tplPanel
}

func tplPromql(id, title, desc string, width int, query, unit, viz string) tplPanel {
	cfg := map[string]any{"query": query, "source": "vm"}
	if unit != "" {
		cfg["unit"] = unit
	}
	if viz != "" {
		cfg["viz"] = viz
	}
	return tplPanel{ID: id, Type: "promql", Title: title, Desc: desc, Width: width, Config: cfg}
}

var serviceVar = []tplVariable{{Name: "service", Label: "service", Type: "service"}}

// dashboardTemplates — SAF, sabit küme (dashboard_templates_test.go her
// şablonun gereken metriklerini sorgularda geçtiğini doğrular).
func dashboardTemplates() []dashTemplate {
	const httpDur = "http_server_request_duration_seconds"
	return []dashTemplate{
		{
			ID: "vm-service-red", Name: "Service RED",
			Description: "Servis başına istek hızı, hata oranı ve süre (OTel HTTP sunucu metrikleri). Değişken: service.",
			Tags:        []string{"red", "servis"},
			Required:    []string{httpDur},
			Variables:   serviceVar,
			Panels: []tplPanel{
				tplPromql("rate", "İstek hızı", "Saniyedeki istek sayısı", 2,
					`sum(rate(`+httpDur+`_count{service_name="${service}"}[5m]))`, "reqps", ""),
				tplPromql("err", "Hata oranı (5xx)", "5xx yanıtların payı", 1,
					`100 * sum(rate(`+httpDur+`_count{service_name="${service}",http_response_status_code=~"5.."}[5m])) / sum(rate(`+httpDur+`_count{service_name="${service}"}[5m]))`, "%", ""),
				tplPromql("p95", "p95 süre", "İstek süresinin 95. yüzdeliği", 1,
					`1000 * histogram_quantile(0.95, sum by (le) (rate(`+httpDur+`_bucket{service_name="${service}"}[5m])))`, "ms", ""),
				tplPromql("route-rate", "İstek hızı: route'a göre", "", 2,
					`sum by (http_route) (rate(`+httpDur+`_count{service_name="${service}"}[5m]))`, "reqps", ""),
				tplPromql("route-p95", "p95 süre: route'a göre", "", 2,
					`1000 * histogram_quantile(0.95, sum by (le, http_route) (rate(`+httpDur+`_bucket{service_name="${service}"}[5m])))`, "ms", ""),
				tplPromql("status", "Durum koduna göre", "", 4,
					`sum by (http_response_status_code) (rate(`+httpDur+`_count{service_name="${service}"}[5m]))`, "reqps", "stacked-bar"),
			},
		},
		{
			ID: "vm-jvm", Name: "JVM",
			Description: "Bellek, çöp toplama ve iş parçacığı (OTel Java çalışma zamanı metrikleri). Değişken: service.",
			Tags:        []string{"jvm"},
			Required:    []string{"jvm_memory_used_bytes", "jvm_gc_duration_seconds", "jvm_thread_count"},
			Variables:   serviceVar,
			Panels: []tplPanel{
				tplPromql("heap", "Heap kullanımı", "", 2,
					`sum by (jvm_memory_pool_name) (jvm_memory_used_bytes{service_name="${service}",jvm_memory_type="heap"})`, "bytes", "stacked-area"),
				tplPromql("nonheap", "Non-heap kullanımı", "", 2,
					`sum by (jvm_memory_pool_name) (jvm_memory_used_bytes{service_name="${service}",jvm_memory_type="non_heap"})`, "bytes", "stacked-area"),
				tplPromql("gc-time", "GC süresi payı", "Çöp toplamada geçen zamanın payı", 2,
					`100 * sum(rate(jvm_gc_duration_seconds_sum{service_name="${service}"}[5m]))`, "%", ""),
				tplPromql("gc-count", "GC sayısı", "", 2,
					`sum by (jvm_gc_name) (rate(jvm_gc_duration_seconds_count{service_name="${service}"}[5m]))`, "", "bar"),
				tplPromql("threads", "İş parçacığı", "", 2,
					`sum by (service_name) (jvm_thread_count{service_name="${service}"})`, "", ""),
				tplPromql("cpu", "JVM CPU", "", 2,
					`100 * avg by (service_name) (jvm_cpu_recent_utilization_ratio{service_name="${service}"})`, "%", ""),
			},
		},
		{
			ID: "vm-kafka", Name: "Kafka",
			Description: "Tüketici gecikmesi, tüketim ve üretim hızı, yeniden dengeleme (Kafka istemci metrikleri). Değişken: service.",
			Tags:        []string{"kafka"},
			Required:    []string{"kafka_consumer_records_lag_max", "kafka_consumer_records_consumed_rate", "kafka_producer_record_send_rate"},
			Variables:   serviceVar,
			Panels: []tplPanel{
				tplPromql("lag", "Tüketici gecikmesi (en çok)", "Bölüm başına en büyük kayıt gecikmesi", 2,
					`max by (client_id) (kafka_consumer_records_lag_max{service_name="${service}"})`, "", ""),
				tplPromql("consumed", "Tüketim hızı", "", 2,
					`sum by (client_id) (kafka_consumer_records_consumed_rate{service_name="${service}"})`, "", ""),
				tplPromql("sent", "Üretim hızı", "", 2,
					`sum by (client_id) (kafka_producer_record_send_rate{service_name="${service}"})`, "", ""),
				tplPromql("errors", "Üretim hatası", "", 2,
					`sum by (client_id) (kafka_producer_record_error_rate{service_name="${service}"})`, "", ""),
				tplPromql("fetch", "Fetch gecikmesi (en çok)", "", 2,
					`max by (client_id) (kafka_consumer_fetch_latency_max{service_name="${service}"})`, "ms", ""),
				tplPromql("rebalance", "Yeniden dengeleme / sa", "", 2,
					`sum by (client_id) (kafka_consumer_rebalance_rate_per_hour{service_name="${service}"})`, "", "bar"),
			},
		},
		{
			ID: "vm-kubernetes", Name: "Kubernetes",
			Description: "Düğüm, pod ve kapsayıcı kaynakları (kube-state-metrics + cAdvisor).",
			Tags:        []string{"k8s"},
			Required:    []string{"kube_pod_status_phase", "kube_pod_container_status_restarts_total", "container_cpu_usage_seconds_total", "container_memory_working_set_bytes"},
			Panels: []tplPanel{
				tplPromql("phase", "Pod durumu", "Faza göre pod sayısı", 2,
					`sum by (phase) (kube_pod_status_phase == 1)`, "", "stacked-bar"),
				tplPromql("restarts", "Yeniden başlatma (1 sa)", "Son 1 saatte en çok yeniden başlayan kapsayıcılar", 2,
					`topk(10, sum by (namespace, pod) (increase(kube_pod_container_status_restarts_total[1h])))`, "", "bar"),
				tplPromql("cpu", "CPU: namespace'e göre", "", 2,
					`sum by (namespace) (rate(container_cpu_usage_seconds_total{container!=""}[5m]))`, "", "stacked-area"),
				tplPromql("mem", "Bellek: namespace'e göre", "", 2,
					`sum by (namespace) (container_memory_working_set_bytes{container!=""})`, "bytes", "stacked-area"),
				tplPromql("notready", "Hazır olmayan düğüm", "", 2,
					`sum(kube_node_status_condition{condition="Ready",status!="true"})`, "", ""),
				tplPromql("pending", "Bekleyen pod", "", 2,
					`sum by (namespace) (kube_pod_status_phase{phase="Pending"})`, "", ""),
			},
		},
	}
}

func findDashboardTemplate(id string) (dashTemplate, bool) {
	for _, t := range dashboardTemplates() {
		if t.ID == id {
			return t, true
		}
	}
	return dashTemplate{}, false
}

// ── Liste + ön kontrol ──────────────────────────────────────────────────────

type dashTemplateSummary struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	PanelCount  int      `json:"panelCount"`
	Required    []string `json:"required"`
	// Missing: ön kontrolde bulunamayan metrikler; nil = kontrol yapılamadı
	// (Checked=false), boş = hepsi var.
	Missing []string `json:"missing"`
	Checked bool     `json:"checked"`
}

func dashboardTemplatesKey(backend string) string {
	return "dash-templates:v1:src=" + backend
}

func (s *Server) listDashboardTemplates(w http.ResponseWriter, r *http.Request) {
	src, err := s.metricSourceFor(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.serveCached(w, r, dashboardTemplatesKey(src.Name()), 5*time.Minute, func(ctx context.Context) (any, error) {
		tpls := dashboardTemplates()
		exists := precheckMetrics(ctx, src, tpls)
		out := make([]dashTemplateSummary, 0, len(tpls))
		for _, t := range tpls {
			out = append(out, summarizeTemplate(t, exists))
		}
		return map[string]any{"templates": out, "backend": src.Name()}, nil
	})
}

// summarizeTemplate — SAF: exists[name] yoksa o metrik denetlenemedi.
func summarizeTemplate(t dashTemplate, exists map[string]bool) dashTemplateSummary {
	sum := dashTemplateSummary{
		ID: t.ID, Name: t.Name, Description: t.Description, Tags: t.Tags,
		PanelCount: len(t.Panels), Required: t.Required, Missing: []string{}, Checked: true,
	}
	for _, m := range t.Required {
		ok, seen := exists[m]
		if !seen {
			sum.Checked = false
			continue
		}
		if !ok {
			sum.Missing = append(sum.Missing, m)
		}
	}
	return sum
}

// precheckMetrics — gereken metriklerin tekil kümesini en çok 4 eşzamanlı
// istekle denetler. Hata veren ad haritaya girmez ("denetlenemedi").
func precheckMetrics(ctx context.Context, src metricSource, tpls []dashTemplate) map[string]bool {
	seen := map[string]bool{}
	var names []string
	for _, t := range tpls {
		for _, m := range t.Required {
			if !seen[m] {
				seen[m] = true
				names = append(names, m)
			}
		}
	}
	out := make(map[string]bool, len(names))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, n := range names {
		wg.Add(1)
		sem <- struct{}{}
		go func(n string) {
			defer wg.Done()
			defer func() { <-sem }()
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			ok, err := src.MetricExists(cctx, n)
			if err != nil {
				return
			}
			mu.Lock()
			out[n] = ok
			mu.Unlock()
		}(n)
	}
	wg.Wait()
	return out
}

// ── Şablondan oluştur ───────────────────────────────────────────────────────

// templateDashboard — SAF: şablon → kaydedilecek pano (kimliksiz).
func templateDashboard(t dashTemplate) (chstore.Dashboard, error) {
	panels, err := json.Marshal(t.Panels)
	if err != nil {
		return chstore.Dashboard{}, err
	}
	vars := t.Variables
	if vars == nil {
		vars = []tplVariable{}
	}
	variables, err := json.Marshal(vars)
	if err != nil {
		return chstore.Dashboard{}, err
	}
	tags, err := json.Marshal(append([]string{"şablon"}, t.Tags...))
	if err != nil {
		return chstore.Dashboard{}, err
	}
	return chstore.Dashboard{
		Name: t.Name, Description: t.Description,
		Panels: panels, Variables: variables, Tags: tags,
	}, nil
}

func (s *Server) createDashboardFromTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		writeJSONError(w, http.StatusBadRequest, "geçersiz JSON: "+err.Error())
		return
	}
	t, ok := findDashboardTemplate(in.ID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "şablon bulunamadı")
		return
	}
	d, err := templateDashboard(t)
	if err != nil {
		writeErr(w, err)
		return
	}
	// custom- önekli kimlik: boot tohumu yalnız preset- satırlarına dokunur
	// (dashboard_presets.go); şablondan kurulan pano operatörün panosudur.
	d.ID = "custom-" + newID(8)
	if err := s.store.UpsertDashboard(r.Context(), d); err != nil {
		writeErr(w, err)
		return
	}
	details, _ := json.Marshal(map[string]any{"name": d.Name, "template": t.ID})
	s.audit(r, "dashboard.create_from_template", "dashboard", d.ID, string(details))
	writeJSON(w, d)
}
