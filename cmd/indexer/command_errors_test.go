package main

import (
	"strings"
	"testing"
)

func TestOpenDB_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	db, err := openDB()
	if err == nil {
		db.Close()
		t.Fatal("openDB() error = nil, want error for empty DATABASE_URL")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("err = %v, want DATABASE_URL message", err)
	}
}

func TestOpenDB_UnreachableDatabaseFailsPing(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@127.0.0.1:1/db?sslmode=disable&connect_timeout=1")
	db, err := openDB()
	if err == nil {
		db.Close()
		t.Fatal("openDB() error = nil, want ping error")
	}
	if !strings.Contains(err.Error(), "pinging database") {
		t.Fatalf("err = %v, want wrapped ping error", err)
	}
}

func TestReorgCmd_RejectsNonNumericLedger(t *testing.T) {
	err := reorgCmd.RunE(reorgCmd, []string{"not-a-number"})
	if err == nil {
		t.Fatal("RunE() error = nil, want invalid ledger error")
	}
	if !strings.Contains(err.Error(), "invalid ledger number") {
		t.Fatalf("err = %v, want invalid ledger number", err)
	}
}

func TestReorgCmd_FailsWithoutDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := reorgCmd.RunE(reorgCmd, []string{"12345"})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("err = %v, want DATABASE_URL error", err)
	}
}

func TestRepairCmd_FailsWithoutDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	err := repairCmd.RunE(repairCmd, nil)
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL is required") {
		t.Fatalf("err = %v, want DATABASE_URL error", err)
	}
}
