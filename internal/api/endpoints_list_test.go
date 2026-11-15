package api

import "testing"

func TestEndpointsColumnFilters(t *testing.T) {
	if endpointsMethodFilter(" post ") != "POST" || endpointsMethodFilter("TRACE") != "" || endpointsMethodFilter("") != "" {
		t.Fatal("method")
	}
	if endpointsStatusFilter("5XX") != "5xx" || endpointsStatusFilter("1xx") != "" {
		t.Fatal("status")
	}
}
