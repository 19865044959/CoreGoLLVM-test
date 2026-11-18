package main

import (
	"net/http"
	"net/http/httptest"
)

// pprofPattern asks the global DefaultServeMux which pattern, if any, was
// registered for path by net/http/pprof's init function.
func pprofPattern(path string) string {
	req := httptest.NewRequest("GET", path, nil)
	_, pattern := http.DefaultServeMux.Handler(req)
	return pattern
}

func pprofIndex() (int, string) {
	req := httptest.NewRequest("GET", "/debug/pprof/", nil)
	rec := httptest.NewRecorder()
	h, _ := http.DefaultServeMux.Handler(req)
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}
