package middleware

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// GlobalRateLimiter creates a middleware that limits the total request rate
// across all actions using a single token bucket.
// When the rate is exceeded, it responds with 429 Too Many Requests.
func GlobalRateLimiter(rps float64, burst int) Middleware {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// PerActionRateLimiter creates a middleware that limits the request rate
// per SSM API action. The action is extracted from the X-Amz-Target header
// (AmazonSSM.<Action>). Each action gets its own token bucket, mirroring
// AWS's per-API throttling.
// When the rate is exceeded, it responds with 429 Too Many Requests.
// A background goroutine periodically removes idle limiters to prevent
// unbounded memory growth. The goroutine is stopped when the returned
// cleanup function is called.
func PerActionRateLimiter(rps float64, burst int) (Middleware, func()) {
	var (
		mu       sync.Mutex
		limiters = make(map[string]*rate.Limiter)
		stop     = make(chan struct{})
	)

	// Clean up idle limiters every 5 minutes to prevent unbounded growth.
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				mu.Lock()
				// Remove limiters that have fully replenished their burst budget,
				// meaning they haven't been used recently.
				for name, l := range limiters {
					if l.Tokens() >= float64(burst) {
						delete(limiters, name)
					}
				}
				mu.Unlock()
			case <-stop:
				return
			}
		}
	}()

	getLimiter := func(action string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		l, exists := limiters[action]
		if !exists {
			l = rate.NewLimiter(rate.Limit(rps), burst)
			limiters[action] = l
		}
		return l
	}

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action := ExtractAction(r.Header.Get("X-Amz-Target"))
			if action == "" {
				// Not an SSM API call (e.g. agent or data channel endpoints)
				next.ServeHTTP(w, r)
				return
			}

			limiter := getLimiter(action)
			if !limiter.Allow() {
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}

	cleanup := func() { close(stop) }
	return mw, cleanup
}

// ExtractAction parses the action name from an X-Amz-Target header value
// such as "AmazonSSM.GetParameter". Returns empty string when the header
// is missing or malformed.
func ExtractAction(target string) string {
	idx := strings.LastIndex(target, ".")
	if idx < 0 || idx == len(target)-1 {
		return ""
	}
	return target[idx+1:]
}
