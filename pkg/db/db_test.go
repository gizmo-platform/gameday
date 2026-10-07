package db

import (
	"context"
	"os"
	"testing"
)

type testWidget struct {
	ID    uint   `gorm:"primaryKey"`
	Name  string `gorm:"uniqueIndex"`
	Count int
}

func TestNewPostgresRequiresDSN(t *testing.T) {
	_, err := New(WithDriver(DriverPostgres))
	if err == nil {
		t.Fatal("expected an error for the postgres driver without a DSN")
	}
}

func TestNewUnknownDriver(t *testing.T) {
	_, err := New(WithDriver("bogus"))
	if err == nil {
		t.Fatal("expected an error for an unknown driver")
	}
}

// TestPostgresIntegration exercises the Postgres driver end to end
// (open, migrate, upsert round-trip). It is gated on
// GAMEDAY_TEST_PG_DSN so the default test run stays dependency-free.
func TestPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("GAMEDAY_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("GAMEDAY_TEST_PG_DSN not set")
	}

	d, err := New(WithDriver(DriverPostgres), WithDSN(dsn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if d.Dialector.Name() != "postgres" {
		t.Fatalf("dialector = %q, want postgres", d.Dialector.Name())
	}
	t.Cleanup(func() {
		d.Raw().Migrator().DropTable(&testWidget{})
	})

	if err := d.Migrate(&testWidget{}); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ctx := context.Background()
	w := &testWidget{Name: "widget", Count: 1}
	if err := InsertOrUpdate(ctx, d.Raw(), w); err != nil {
		t.Fatalf("InsertOrUpdate: %v", err)
	}
	if w.ID == 0 {
		t.Fatal("expected a primary key to be assigned")
	}

	// Upsert on the same primary key updates in place, no duplicate row.
	w.Count = 2
	if err := InsertOrUpdate(ctx, d.Raw(), w); err != nil {
		t.Fatalf("InsertOrUpdate (update): %v", err)
	}
	var count int64
	if err := d.Raw().Model(&testWidget{}).Count(&count).Error; err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count = %d, want 1", count)
	}
	var got testWidget
	if err := d.Raw().First(&got, w.ID).Error; err != nil {
		t.Fatalf("First: %v", err)
	}
	if got.Count != 2 {
		t.Fatalf("count = %d, want 2", got.Count)
	}
}
