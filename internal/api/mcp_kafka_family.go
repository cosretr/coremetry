package api

// mcp_kafka_family.go — v0.10.2454: get_service_health raporunun messaging
// bölümü için Kafka istemci metriği ailesi (Deps.KafkaFamily). GET
// /api/services/{name}/kafka-family ile AYNI çözücü (resolveServiceKafkaFamily);
// önbellek raporun kendi 60 sn girdisinde (yoklama yalnız rapor ıskalanınca).

import (
	"context"
	"time"

	"github.com/cilcenk/coremetry/internal/mcptools"
	"github.com/cilcenk/coremetry/internal/sourcestate"
)

func (s *Server) mcpKafkaFamily(ctx context.Context, service string, at time.Time) (mcptools.KafkaFamilyInfo, error) {
	vm, ok := s.metricSource().(vmMetricSource)
	if !ok {
		// CH metrik kaynağında Kafka istemci metriği sorulmaz (uç ile aynı).
		return mcptools.KafkaFamilyInfo{State: kafkaSrcSpans, Reason: "metrik kaynağı VictoriaMetrics değil"}, nil
	}
	res, err := resolveServiceKafkaFamily(ctx, vm, service, at)
	if err != nil {
		return mcptools.KafkaFamilyInfo{State: kafkaSrcUnknown, Reason: string(sourcestate.Classify(err))}, err
	}
	out := mcptools.KafkaFamilyInfo{State: res.State, Produce: res.Produce, Consume: res.Consume}
	if res.Family != nil {
		out.Family = res.Family.ID
	}
	return out, nil
}
