package api

import (
	"net/http"
	"strings"
)

const (
	defaultCORSMethods = "GET, POST, OPTIONS"
	defaultCORSHeaders = "Content-Type, Authorization, Accept"
)

type CORSConfig struct {
	AllowedOrigins []string
}

func WrapWithCORS(next http.Handler, config CORSConfig) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}

	allowed := normalizeOrigins(config.AllowedOrigins)
	if len(allowed) == 0 {
		return next
	}

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		origin := strings.TrimSpace(request.Header.Get("Origin"))
		allowOrigin, ok := matchOrigin(origin, allowed)
		if ok {
			headers := writer.Header()
			headers.Set("Access-Control-Allow-Origin", allowOrigin)
			headers.Set("Access-Control-Allow-Methods", defaultCORSMethods)
			headers.Set("Access-Control-Allow-Headers", requestedHeaders(request))
			headers.Set("Access-Control-Max-Age", "600")
			headers.Add("Vary", "Origin")
			headers.Add("Vary", "Access-Control-Request-Method")
			headers.Add("Vary", "Access-Control-Request-Headers")
		}

		if request.Method == http.MethodOptions {
			if origin != "" && !ok {
				writer.WriteHeader(http.StatusForbidden)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(writer, request)
	})
}

func normalizeOrigins(origins []string) []string {
	normalized := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		cleaned := strings.TrimSpace(origin)
		if cleaned == "" {
			continue
		}
		if cleaned == "*" {
			return []string{"*"}
		}
		if _, ok := seen[cleaned]; ok {
			continue
		}
		seen[cleaned] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized
}

func matchOrigin(origin string, allowed []string) (string, bool) {
	if len(allowed) == 0 || strings.TrimSpace(origin) == "" {
		return "", false
	}
	for _, candidate := range allowed {
		if candidate == "*" {
			return "*", true
		}
		if origin == candidate {
			return origin, true
		}
	}
	return "", false
}

func requestedHeaders(request *http.Request) string {
	if request == nil {
		return defaultCORSHeaders
	}
	if requested := strings.TrimSpace(request.Header.Get("Access-Control-Request-Headers")); requested != "" {
		return requested
	}
	return defaultCORSHeaders
}
