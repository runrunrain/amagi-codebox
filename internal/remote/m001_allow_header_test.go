package remote

import (
	"net/http"
	"sort"
	"strings"
	"testing"

	"amagi-codebox/internal/remote/contract"
)

// TestM001_AllowAggregatesAllMethodsForSharedPath verifies the 405 Allow header
// aggregates every active method for a path, not just the first match (m-001).
// /sessions is shared by GET (idx 2) and POST (idx 4); /sessions/{id} is shared
// by GET (idx 3) and DELETE (idx 7).
func TestM001_AllowAggregatesAllMethodsForSharedPath(t *testing.T) {
	// Build specs that mirror the production session-route activation (adapter
	// wired): indices 2..9 are active.
	specs := []v1RouteSpec{
		{endpointIndex: 2}, // GET /sessions
		{endpointIndex: 3}, // GET /sessions/{id}
		{endpointIndex: 4}, // POST /sessions
		{endpointIndex: 7}, // DELETE /sessions/{id}
	}

	cases := []struct {
		name    string
		path    string
		wantHas []string
	}{
		{"sessions collection", contract.RESTBasePath + "/sessions", []string{http.MethodGet, http.MethodPost, http.MethodOptions}},
		{"sessions item", contract.RESTBasePath + "/sessions/abc-123", []string{http.MethodGet, http.MethodDelete, http.MethodOptions}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allow := allowedMethodsForPath(specs, tc.path)
			parts := strings.Split(allow, ", ")
			got := map[string]bool{}
			for _, p := range parts {
				got[strings.TrimSpace(p)] = true
			}
			for _, m := range tc.wantHas {
				if !got[m] {
					t.Fatalf("Allow=%q missing method %q", allow, m)
				}
			}
			// Verify de-duplication: OPTIONS appears exactly once.
			if strings.Count(allow, http.MethodOptions) != 1 {
				t.Fatalf("Allow=%q must list OPTIONS exactly once", allow)
			}
			// Verify sorted (methods before OPTIONS).
			methodsOnly := parts[:len(parts)-1]
			if !sort.StringsAreSorted(methodsOnly) {
				t.Fatalf("Allow methods must be sorted: %v", methodsOnly)
			}
			// Verify no duplicates among the method tokens.
			seen := map[string]bool{}
			for _, m := range methodsOnly {
				if seen[m] {
					t.Fatalf("Allow=%q has duplicate method %q", allow, m)
				}
				seen[m] = true
			}
			// Negative: a wrong method (PATCH) is NOT advertised.
			if got[http.MethodPatch] {
				t.Fatalf("Allow=%q must not advertise PATCH", allow)
			}
		})
	}
}

// TestM001_AllowSingleMethodPath verifies a path with one method lists exactly
// that method + OPTIONS.
func TestM001_AllowSingleMethodPath(t *testing.T) {
	specs := []v1RouteSpec{{endpointIndex: 1}} // GET /host/summary
	allow := allowedMethodsForPath(specs, contract.RESTBasePath+"/host/summary")
	if allow != "GET, OPTIONS" {
		t.Fatalf("single-method Allow=%q want %q", allow, "GET, OPTIONS")
	}
}
