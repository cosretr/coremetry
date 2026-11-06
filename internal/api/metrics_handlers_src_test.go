package api

import "os"

// readMetricHandlerSources — v0.10.1835: the /api/metrics/* read handlers
// moved from api.go to metrics_handlers.go. The source-pin tests that used to
// scan api.go alone read both files as ONE text, so a handler moving back (or
// a new one landing in either file) stays inside every scan. api.go is kept in
// the set on purpose: a direct s.store.<metric method> call re-added there
// must still fail TestMetricHandlersGoThroughTheSourceSeam.
func readMetricHandlerSources() ([]byte, error) {
	a, err := os.ReadFile("api.go")
	if err != nil {
		return nil, err
	}
	m, err := os.ReadFile("metrics_handlers.go")
	if err != nil {
		return nil, err
	}
	return append(append(a, '\n'), m...), nil
}
