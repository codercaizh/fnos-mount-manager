package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFnOSGatewayOriginCompatibility(t *testing.T) {
	cases := []struct {
		name, origin, site, custom, ctype string
		code                              int
	}{
		{"gateway rewrites Host but browser reports same-origin", "https://nas.example.net:5667", "same-origin", "1", "application/json", 200},
		{"gateway without fetch metadata", "https://nas.example.net", "", "1", "application/json", 200},
		{"old client without Origin", "", "", "1", "application/json", 200},
		{"cross-site request", "https://evil.example", "cross-site", "1", "application/json", 403},
		{"same-site not same-origin", "https://other.nas.example.net", "same-site", "1", "application/json", 403},
		{"missing CSRF request header", "https://nas.example.net", "same-origin", "", "application/json", 403},
		{"non JSON content type", "https://nas.example.net", "same-origin", "1", "text/plain", 403},
		{"malformed origin", "null", "same-origin", "1", "application/json", 403},
		{"origin path is not valid", "https://evil.example/route", "same-origin", "1", "application/json", 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://internal-gateway/app/fnos-mount-manager/api/create", strings.NewReader(`{"value":"ok"}`))
			r.Host = "unix-gateway.internal"
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
			}
			if tc.custom != "" {
				r.Header.Set("X-Fnos-Mount-UI", tc.custom)
			}
			if tc.ctype != "" {
				r.Header.Set("Content-Type", tc.ctype)
			}
			w := httptest.NewRecorder()
			var payload struct {
				Value string `json:"value"`
			}
			ok := readJSON(w, r, &payload)
			if tc.code == 200 {
				if !ok || payload.Value != "ok" {
					t.Fatalf("expected accepted JSON, got code %d: %s", w.Code, w.Body.String())
				}
			} else if ok || w.Code != tc.code {
				t.Fatalf("expected %d, got ok=%v code=%d body=%s", tc.code, ok, w.Code, w.Body.String())
			}
		})
	}
}

func TestGatewayOriginCreationReachesValidation(t *testing.T) {
	s := &Store{Records: []*Record{}}
	r := httptest.NewRequest(http.MethodPost, "http://internal-gateway/app/fnos-mount-manager/api/create", strings.NewReader(`{"source":"/etc","parent":"/vol1/1000/share","name":"x","mode":"rw"}`))
	r.Host = "internal-gateway"
	r.Header.Set("Origin", "https://nas.example.net")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Fnos-Mount-UI", "1")
	w := httptest.NewRecorder()
	s.serve(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected path validation to run (400), got %d: %s", w.Code, w.Body.String())
	}
}
