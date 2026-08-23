package webui

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// loginLimiter tracks failed staff-PIN attempts per client IP and locks an
// IP out for a cooldown period after too many failures in a row. A single
// shared 4-digit PIN with no rate limiting can be brute-forced in seconds;
// this doesn't make it "real" authentication, but it closes the most
// obvious hole cheaply.
type loginLimiter struct {
	mu          sync.Mutex
	failures    map[string]int
	lockedUntil map[string]time.Time
	maxFailures int
	lockout     time.Duration
}

func newLoginLimiter(maxFailures int, lockout time.Duration) *loginLimiter {
	return &loginLimiter{
		failures:    make(map[string]int),
		lockedUntil: make(map[string]time.Time),
		maxFailures: maxFailures,
		lockout:     lockout,
	}
}

// allowed reports whether ip is currently permitted to attempt a login.
func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	until, locked := l.lockedUntil[ip]
	if !locked {
		return true
	}
	if time.Now().After(until) {
		// Lockout expired - give it a fresh start.
		delete(l.lockedUntil, ip)
		delete(l.failures, ip)
		return true
	}
	return false
}

// recordFailure counts one failed attempt from ip, locking it out once it
// crosses the threshold.
func (l *loginLimiter) recordFailure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[ip]++
	if l.failures[ip] >= l.maxFailures {
		l.lockedUntil[ip] = time.Now().Add(l.lockout)
	}
}

// recordSuccess clears any failure count for ip.
func (l *loginLimiter) recordSuccess(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, ip)
	delete(l.lockedUntil, ip)
}

// clientIP extracts the request's remote IP, stripping the port.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
