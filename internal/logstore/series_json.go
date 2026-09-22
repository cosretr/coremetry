package logstore

import "encoding/json"

// series_json.go — v0.10.844: chstore.SpanMetricSeries'in ikizi (bkz.
// chstore/series_json.go). ES yolunda parseBuckets boş kovada nil dönebilir
// (elasticsearch.go), FE `ExploreSeries.points` üstünde guard'sız
// reduce/map çağırır (LogsHistogram, Logs, ExploreViz). nil → [].
func (s LogSeries) MarshalJSON() ([]byte, error) {
	type plain LogSeries
	p := plain(s)
	if p.Points == nil {
		p.Points = []LogPoint{}
	}
	return json.Marshal(p)
}
