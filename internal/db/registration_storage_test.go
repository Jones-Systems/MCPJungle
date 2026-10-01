package db

import (
	"path/filepath"
	"testing"
)

func TestRegistrationStorageDurablePragmas(t *testing.T) {
	conn, err := NewDBConnection("", filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, err := conn.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	var journal string
	if err = conn.Raw("PRAGMA journal_mode").Scan(&journal).Error; err != nil {
		t.Fatal(err)
	}
	var synchronous int
	if err = conn.Raw("PRAGMA synchronous").Scan(&synchronous).Error; err != nil {
		t.Fatal(err)
	}
	var busy int
	if err = conn.Raw("PRAGMA busy_timeout").Scan(&busy).Error; err != nil {
		t.Fatal(err)
	}
	if journal != "wal" || synchronous != 2 || busy != 5000 {
		t.Fatalf("storage durability journal=%s synchronous=%d busy=%d", journal, synchronous, busy)
	}
}
