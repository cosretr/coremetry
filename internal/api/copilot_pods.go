package api

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
)

// copilot_pods.go — pod/JVM sağlığı bundle'ı (v0.9.376, operatör istegi —
// SRE perspektifi). Servisliyse pod envanteri (OTel host_name kimliği:
// up/CPU/bellek/son görülme) + o servisin JVM heap doluluğu; servissizse
// filo-geneli heap doluluk sıralaması. Restart/faz KSM ister — kanıt
// bunu açıkça söyler, Pods sekmesine yönlendirir. Veri tamamen CH'den;
// Thanos şartı yok.
//
// v0.9.1147 (AI Faz 3.4, D6) — copilot_guided.go'dan BURAYA taşındı ve
// okuma mcptools'un ortak katmanına indi (mcptools.ReadPodHealth), yani
// get_pod_health tool'u ile guided AYNI iki okumadan ve AYNI satır
// sırasından besleniyor. copilot_guided.go'da kopya KALMADI.
//
// İki sapma bilinçli ve ikisi de DÜRÜSTLÜK yönünde:
//
//  1. Servis modundaki heap listesi artık DOLULUK sırasında. Eski hâl
//     store sırasını basıyordu ve o sorgunun ORDER BY'ı YOK
//     (runtime_pods.go) — yani "en dolu pod hangisi" sorusunun cevabı
//     CH'nin keyfine bağlıydı. Filo dalı zaten sıralıyordu.
//  2. Kesilen heap listesi artık kaç pod'un düştüğünü söylüyor. Eski
//     döngü 13. eşleşmede sessizce çıkıyordu (envanter dalı ise "… ve N
//     pod daha" diyordu) — aynı blokta iki farklı dürüstlük seviyesi.

// guidedPodRows / guidedFleetHeapRows — kanıt bloğundaki satır tavanları
// (eski döngülerin 12 ve 10'u). Gösterim kararı, okuma kararı değil.
const (
	guidedPodRows       = 12
	guidedFleetHeapRows = 10
)

func (s *Server) guidedPodHealthBundle(ctx context.Context, emit func(string, any), service string, from, to time.Time) (string, string, error) {
	nHeap := emitGuidedStep(emit, "jvm_heap_usage", "")
	nInst := 0
	heapRows := guidedPodRows
	if service == "" {
		heapRows = guidedFleetHeapRows
	} else {
		nInst = emitGuidedStep(emit, "service_instances", `{"service":`+jsonStr(service)+`}`)
	}
	// Pencere BÖLÜNMÜŞ ve bu ortak katmanda sabit: heap DAİMA canlı 10 dk
	// (chstore.RuntimePodWindow — sustained ortalama, v0.9.1053), envanter
	// ise sohbetin penceresi. Servis modunda heap okuması düşerse envanter
	// tek başına cevap sayılır (HeapUnavailable) — eski davranış.
	data, err := mcptools.ReadPodHealth(ctx, s.mcpDeps(), service, from, to, guidedPodRows, heapRows)
	if err != nil {
		emitGuidedStepResult(emit, nHeap, "jvm_heap_usage", "", err)
		emitGuidedStepResult(emit, nInst, "service_instances", "", err)
		return "", "", err
	}
	ev, src, rerr := renderPodHealthEvidenceTR(data)
	// v0.9.1229 — envanter ve heap TEK ortak katman çağrısından geliyor ve
	// tek bloğa render ediliyor; blok iki çipe de veriliyor (servis modunda
	// ikinci çip var, filo modunda nInst=0 ve kanıt sessizce düşer).
	emitGuidedStepResult(emit, nHeap, "jvm_heap_usage", ev, rerr)
	emitGuidedStepResult(emit, nInst, "service_instances", ev, rerr)
	// v0.10.1250 (operatör: OTel envanteri "0 pod" derken Infrastructure
	// sekmesi pod'ları Thanos'ta buluyordu) — servis modunda Thanos pod
	// envanteri + HTTP metrikleri (VM) grafik kartları da eklenir.
	var th thanosPodsResult
	if service != "" {
		nTh := emitGuidedStep(emit, "thanos_pods", `{"service":`+jsonStr(service)+`}`)
		th = s.guidedThanosPods(ctx, service)
		tev := renderThanosPodsTR(th, service)
		emitGuidedStepResult(emit, nTh, "thanos_pods", tev, nil)
		if tev != "" {
			ev += tev
			src += " + Thanos pod envanteri (kube-state)"
			rerr = nil // Thanos kanıtı tek başına cevap sayılır
		}
		ev += podHTTPChartFences(service, int64(to.Sub(from).Seconds()))
		src += " + HTTP metrikleri (VictoriaMetrics)"
	}
	stateTurnFrom(ctx).noteRows(podHealthRows(data, th)) // v0.10.2473 — "ikincisini aç" (chat_turn_rows.go)
	return ev, src, rerr
}

// podHealthRows — SAF (v0.10.2473): kanıtta İLK listelenen pod'lar, aynı
// sırayla. Servis modu: OTel envanteri; o boşsa Thanos'un sıralı listesi
// (entity.PodID). Filo modu: heap sıralaması (servis / pod).
func podHealthRows(data mcptools.PodHealthData, th thanosPodsResult) []chatRow {
	var in []podRowIn
	switch {
	case data.Service == "":
		for _, h := range data.Heap {
			in = append(in, podRowIn{Service: h.Service, Pod: h.Pod})
		}
	case len(data.Instances) > 0:
		for _, r := range data.Instances {
			in = append(in, podRowIn{Service: data.Service, Pod: r.ID})
		}
	default:
		for _, r := range thanosPodsRanked(th.Rows) {
			in = append(in, podRowIn{Service: data.Service, Pod: r.Pod, ClusterID: th.CIDs[r.Cluster], Namespace: r.Namespace})
		}
	}
	return podRows(in)
}

// renderPodHealthEvidenceTR — SAF (veri → metin). İki mod tek yerde:
// Service boşsa filo heap sıralaması, doluysa envanter + o servisin heap'i.
func renderPodHealthEvidenceTR(data mcptools.PodHealthData) (string, string, error) {
	var b strings.Builder
	if data.Service == "" {
		if data.HeapTotal == 0 {
			b.WriteString("Filoda JVM heap metriği (jvm.memory.used/limit, OTel runtime) yayınlayan pod yok — JVM olmayan servislerde bu normaldir.\n")
		} else {
			fmt.Fprintf(&b, "Filo-geneli JVM heap doluluğu (son 10 dk ortalaması, %d pod), en dolu önce:\n", data.HeapTotal)
			for _, h := range data.Heap {
				fmt.Fprintf(&b, "- %s / %s: heap %%%.0f\n", h.Service, h.Pod, h.HeapPct)
			}
			if data.HeapTruncated {
				fmt.Fprintf(&b, "… ve %d pod daha (hepsi daha az dolu).\n", data.HeapTotal-len(data.Heap))
			}
		}
		b.WriteString(guidedPodRestartNoteTR)
		return b.String(), "filo JVM heap doluluğu (OTel runtime, canlı)", nil
	}

	fmt.Fprintf(&b, "%s pod/instance envanteri (pencere içinde metrik yayınlayanlar): %d pod, %d canlı (2dk tazelik).\n",
		data.Service, data.InstanceTotal, data.UpCount)
	// Sıra ortak katmanda: önce düşenler (SRE ilk onlara bakar), sonra CPU desc.
	for _, r := range data.Instances {
		state := "CANLI"
		if !r.Up {
			state = "SESSİZ (örnek yok — düşmüş ya da drene olmuş olabilir)"
		}
		fmt.Fprintf(&b, "- %s: %s, cpu %%%.0f, bellek %.0fMB\n", r.ID, state, r.CPUPct, r.MemBytes/1e6)
	}
	if data.InstancesTruncated {
		fmt.Fprintf(&b, "… ve %d pod daha.\n", data.InstanceTotal-len(data.Instances))
	}
	if data.InstanceTotal == 0 {
		b.WriteString("Bu pencerede metrik yayınlayan pod yok.\n")
	}
	if !data.HeapUnavailable {
		if len(data.Heap) == 0 {
			b.WriteString("Bu servis JVM heap metriği yayınlamıyor (JVM değilse normal).\n")
		} else {
			b.WriteString("JVM heap doluluğu (son 10 dk ortalaması):\n")
			for _, h := range data.Heap {
				fmt.Fprintf(&b, "- %s: heap %%%.0f (%.0fMB / %.0fMB -Xmx)\n",
					h.Pod, h.HeapPct, h.UsedBytes/1e6, h.LimitBytes/1e6)
			}
			if data.HeapTruncated {
				fmt.Fprintf(&b, "… ve %d pod daha.\n", data.HeapTotal-len(data.Heap))
			}
		}
	}
	b.WriteString(guidedPodRestartNoteTR)
	return b.String(), fmt.Sprintf("%s pod envanteri + JVM heap (OTel runtime, canlı)", data.Service), nil
}

// guidedPodRestartNoteTR — iki modun paylaştığı dürüstlük altbilgisi:
// restart sayısı ve pod fazı OTel runtime metriklerinde YOK (KSM/Thanos
// ister), model bunu bilmezse uydurur.
const guidedPodRestartNoteTR = "Not: restart sayıları ve pod fazı kube-state-metrics/Thanos ister — servis sayfasının Infrastructure sekmesinde (cluster satırına tıklayın).\n"
