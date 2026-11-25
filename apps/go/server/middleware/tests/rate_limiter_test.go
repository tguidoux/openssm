package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tguidoux/openssm/apps/go/server/middleware"
)

func TestGlobalRateLimiterAllows(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw := middleware.GlobalRateLimiter(100, 10)
	wrapped := mw(handler)

	req := httptest.NewRequest("GET", "/", nil)
	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGlobalRateLimiterRejects(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Very low rate: 1 req/sec, burst of 1
	mw := middleware.GlobalRateLimiter(1, 1)
	wrapped := mw(handler)

	// First request should pass (uses the burst token)
	req1 := httptest.NewRequest("GET", "/", nil)
	rr1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr1, req1)
	assert.Equal(t, http.StatusOK, rr1.Code)

	// Second request immediately should be rejected
	req2 := httptest.NewRequest("GET", "/", nil)
	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusTooManyRequests, rr2.Code)
	assert.NotEmpty(t, rr2.Header().Get("Retry-After"))
}

func ssmRequest(action string) *http.Request {
	req := httptest.NewRequest("POST", "/", nil)
	req.Header.Set("X-Amz-Target", "AmazonSSM."+action)
	return req
}

func TestPerActionRateLimiterAllows(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw, cleanup := middleware.PerActionRateLimiter(100, 10)
	defer cleanup()
	wrapped := mw(handler)

	rr := httptest.NewRecorder()
	wrapped.ServeHTTP(rr, ssmRequest("GetParameter"))

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestPerActionRateLimiterRejects(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw, cleanup := middleware.PerActionRateLimiter(1, 1)
	defer cleanup()
	wrapped := mw(handler)

	rr1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr1, ssmRequest("GetParameter"))
	assert.Equal(t, http.StatusOK, rr1.Code)

	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, ssmRequest("GetParameter"))
	assert.Equal(t, http.StatusTooManyRequests, rr2.Code)

	// Separate limiter per action
	rr3 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr3, ssmRequest("PutParameter"))
	assert.Equal(t, http.StatusOK, rr3.Code)

	// Requests without a target header are not limited
	rr4 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr4, httptest.NewRequest("GET", "/agent/v1/poll", nil))
	assert.Equal(t, http.StatusOK, rr4.Code)
}

func TestPerActionRateLimiterRefills(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mw, cleanup := middleware.PerActionRateLimiter(100, 1)
	defer cleanup()
	wrapped := mw(handler)

	rr1 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr1, ssmRequest("GetParameter"))
	assert.Equal(t, http.StatusOK, rr1.Code)

	time.Sleep(20 * time.Millisecond)

	rr2 := httptest.NewRecorder()
	wrapped.ServeHTTP(rr2, ssmRequest("GetParameter"))
	assert.Equal(t, http.StatusOK, rr2.Code)
}

func TestExtractAction(t *testing.T) {
	tests := []struct {
		target   string
		expected string
	}{
		{"AmazonSSM.GetParameter", "GetParameter"},
		{"AmazonSSM.", ""},
		{"GetParameter", ""},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			assert.Equal(t, tt.expected, middleware.ExtractAction(tt.target))
		})
	}
}
