package util

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBlockDebugPprof(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	handler := BlockDebugPprof(next)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "blocks pprof root", path: "/debug/pprof", wantStatus: http.StatusNotFound},
		{name: "blocks pprof heap", path: "/debug/pprof/heap", wantStatus: http.StatusNotFound},
		{name: "blocks pprof prefix variant", path: "/debug/pprofX", wantStatus: http.StatusNotFound},
		{name: "allows healthz", path: "/healthz", wantStatus: http.StatusTeapot},
		{name: "allows metrics", path: "/metrics", wantStatus: http.StatusTeapot},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
