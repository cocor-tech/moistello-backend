package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/moistello/backend/config"
)

type ValidationResult struct {
	Valid       bool     `json:"valid"`
	Environment string   `json:"environment,omitempty"`
	Errors      []string `json:"errors,omitempty"`
	Message     string   `json:"message"`
}

func main() {
	configPath := flag.String("config", "", "Path to config directory or file")
	envFile := flag.String("env-file", "", "Optional .env file to load before validation")
	jsonOutput := flag.Bool("json", false, "Output machine-readable JSON")
	flag.Parse()

	if *envFile != "" {
		if err := loadEnvFile(*envFile); err != nil {
			reportResult(*jsonOutput, false, "", []string{fmt.Sprintf("failed to load env file %s: %v", *envFile, err)})
			os.Exit(1)
		}
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		reportResult(*jsonOutput, false, "", []string{err.Error()})
		os.Exit(1)
	}

	if err := cfg.ValidateOffline(); err != nil {
		reportResult(*jsonOutput, false, cfg.Environment, []string{err.Error()})
		os.Exit(1)
	}

	reportResult(*jsonOutput, true, cfg.Environment, nil)
	os.Exit(0)
}

func reportResult(isJSON bool, valid bool, env string, errors []string) {
	if isJSON {
		res := ValidationResult{
			Valid:       valid,
			Environment: env,
			Errors:      errors,
		}
		if valid {
			res.Message = "configuration is valid (offline preflight passed)"
		} else {
			res.Message = "configuration validation failed"
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
		return
	}

	if valid {
		fmt.Printf("✅ Configuration is valid (environment=%s, offline preflight passed)\n", env)
	} else {
		fmt.Fprintln(os.Stderr, "❌ Configuration validation failed:")
		for _, err := range errors {
			fmt.Fprintf(os.Stderr, "   - %s\n", err)
		}
	}
}

func loadEnvFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := splitLines(string(data))
	for _, line := range lines {
		trimmed := trimSpace(line)
		if trimmed == "" || trimmed[0] == '#' {
			continue
		}
		if len(trimmed) > 7 && trimmed[:7] == "export " {
			trimmed = trimmed[7:]
		}
		eqIdx := -1
		for i := 0; i < len(trimmed); i++ {
			if trimmed[i] == '=' {
				eqIdx = i
				break
			}
		}
		if eqIdx <= 0 {
			continue
		}
		key := trimSpace(trimmed[:eqIdx])
		val := trimSpace(trimmed[eqIdx+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return nil
}

func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}

func splitLines(s string) []string {
	var lines []string
	curr := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, curr)
			curr = ""
		} else if s[i] != '\r' {
			curr += string(s[i])
		}
	}
	if curr != "" {
		lines = append(lines, curr)
	}
	return lines
}
