package auth

import "github.com/prometheus/client_golang/prometheus"

var authAttempts = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "s3_auth_attempts_total",
	Help: "Total number of authentication attempts by method and status",
}, []string{"method", "status", "s3Endpoint"})

func init() {
	prometheus.MustRegister(authAttempts)
}
