package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Environment names. CORS policy is chosen from these three buckets; anything
// unrecognised is treated as development so an unknown value never silently
// disables the production policy.
const (
	EnvironmentDevelopment = "development"
	EnvironmentStaging     = "staging"
	EnvironmentProduction  = "production"
)

// corsOriginsEnvVars are consulted, in order, for an explicit allowed-origins
// override. MOISTELLO_CORS_ALLOWED_ORIGINS is the canonical name; the shorter
// ALLOWED_ORIGINS is kept because it is what .env.example has always
// documented. Precedence follows the rest of this package: the environment
// beats the config file, which beats the per-environment default.
var corsOriginsEnvVars = []string{
	"MOISTELLO_CORS_ALLOWED_ORIGINS",
	"ALLOWED_ORIGINS",
}

// NormalizeEnvironment maps the aliases that appear in deployment configs onto
// the three environments the CORS policy distinguishes.
func NormalizeEnvironment(environment string) string {
	switch strings.ToLower(strings.TrimSpace(environment)) {
	case EnvironmentProduction, "prod":
		return EnvironmentProduction
	case EnvironmentStaging, "stage", "stg":
		return EnvironmentStaging
	default:
		return EnvironmentDevelopment
	}
}

// DefaultCORSOrigins returns the allowed origins for an environment when the
// operator has not configured any. The lists are deliberately narrow and
// environment specific (#348): staging and production never inherit a
// localhost origin, so a deployment that forgets to set ALLOWED_ORIGINS serves
// its real frontend no origins at all rather than dev-only ones.
func DefaultCORSOrigins(environment string) []string {
	switch NormalizeEnvironment(environment) {
	case EnvironmentProduction:
		return []string{"https://app.moistello.io", "https://www.moistello.io"}
	case EnvironmentStaging:
		return []string{"https://staging.moistello.io"}
	default:
		return []string{"http://localhost:1110", "http://127.0.0.1:1110"}
	}
}

// SplitOrigins parses a comma-separated origin list, trimming whitespace,
// dropping empty entries and removing duplicates while preserving order.
func SplitOrigins(raw string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, part := range strings.Split(raw, ",") {
		origin := strings.TrimSpace(part)
		if origin == "" {
			continue
		}
		if _, dup := seen[origin]; dup {
			continue
		}
		seen[origin] = struct{}{}
		out = append(out, origin)
	}
	return out
}

func originsFromEnv() []string {
	for _, name := range corsOriginsEnvVars {
		raw, ok := os.LookupEnv(name)
		if !ok {
			continue
		}
		if origins := SplitOrigins(raw); len(origins) > 0 {
			return origins
		}
	}
	return nil
}

// ResolveCORSAllowedOrigins picks the allowed origins for the running process.
// An explicit environment override wins over the config file, which wins over
// the per-environment default.
func ResolveCORSAllowedOrigins(configured []string, environment string) []string {
	if origins := originsFromEnv(); len(origins) > 0 {
		return origins
	}
	if origins := SplitOrigins(strings.Join(configured, ",")); len(origins) > 0 {
		return origins
	}
	return DefaultCORSOrigins(environment)
}

// validateCORS rejects origin lists that would silently break the browser
// handshake. Wildcard origins are refused outside development, and a wildcard
// combined with credentials is refused everywhere because browsers reject
// credentialed requests against Access-Control-Allow-Origin: *. Loopback
// origins are refused outside development for the same reason the database URL
// is: it is always a misconfiguration, never an intent.
//
// Every problem is returned rather than panicked, so config.Load can report a
// CORS misconfiguration in the same single-pass error list as every other
// invalid setting (see AGENTS.md: config.Load must never panic).
func validateCORS(environment string, c CORSConfig) []error {
	env := NormalizeEnvironment(environment)
	var errs []error

	if len(c.AllowedOrigins) == 0 {
		errs = append(errs, fmt.Errorf("config: cors.allowed_origins resolved to an empty list for environment %q", environment))
		return errs
	}

	for _, origin := range c.AllowedOrigins {
		if origin == "*" {
			if c.AllowCredentials {
				errs = append(errs, fmt.Errorf("config: cors.allowed_origins \"*\" cannot be combined with cors.allow_credentials; browsers reject credentialed requests against a wildcard origin"))
			}
			if env != EnvironmentDevelopment {
				errs = append(errs, fmt.Errorf("config: cors.allowed_origins \"*\" is not allowed in %s; list the exact frontend origins", env))
			}
			continue
		}

		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			errs = append(errs, fmt.Errorf("config: cors.allowed_origins entry %q is not an absolute http(s) origin", origin))
			continue
		}
		if env != EnvironmentDevelopment && isLoopbackHost(u.Hostname()) {
			errs = append(errs, fmt.Errorf("config: cors.allowed_origins entry %q is a loopback address but environment is %s; set the real frontend origin via ALLOWED_ORIGINS", origin, env))
		}
	}
	return errs
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
