package customers

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// TestMongoLive runs against a real MongoDB only when MONGODB_TEST_URI is set. It uses a throwaway
// database (skydropx_test) and drops it at the end, so production data is never touched.
func TestMongoLive(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const db = "skydropx_test"
	s, closeFn, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeFn(context.Background()) }()
	defer func() {
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err == nil {
			_ = client.Database(db).Drop(context.Background())
			_ = client.Disconnect(context.Background())
		}
	}()

	if _, err := s.RecordOrder(ctx, Customer{Name: "Prueba", DocumentID: "999999", Phone: "3000000000", Source: "Facebook"},
		Order{Date: time.Now(), Units: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordOrder(ctx, Customer{Name: "Prueba Dos", Phone: "3000000000"}, Order{Date: time.Now()}); err != nil {
		t.Fatal(err)
	}
	c, err := s.GrantConsent(ctx, "999999", "test", "test")
	if err != nil || !c.Contactable() || len(c.Orders) != 2 || c.Name != "Prueba Dos" || c.Source != "facebook" {
		t.Fatalf("%+v %v", c, err)
	}
	if c, err := s.OptOut(ctx, "3000000000"); err != nil || c.Contactable() {
		t.Fatalf("opt-out: %+v %v", c, err)
	}
	all, err := s.All(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("all: %d %v", len(all), err)
	}
}

// TestMongoLiveConcurrent checks uniqueness and atomicity against a real MongoDB (see TestMongoLive).
func TestMongoLiveConcurrent(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	const db = "skydropx_test_concurrent"
	s, closeFn, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeFn(context.Background()) }()
	defer func() {
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err == nil {
			_ = client.Database(db).Drop(context.Background())
			_ = client.Disconnect(context.Background())
		}
	}()
	recordConcurrently(t, s, 20)
}

// TestMongoLiveMigration checks the migration against a real MongoDB (see TestMongoLive).
func TestMongoLiveMigration(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const db = "skydropx_test_migration"
	s, closeFn, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeFn(context.Background()) }()
	defer func() {
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err == nil {
			_ = client.Database(db).Drop(context.Background())
			_ = client.Disconnect(context.Background())
		}
	}()
	if time.Since(s.Since()) > time.Minute {
		t.Fatalf("a new database starts now: %v", s.Since())
	}
	checkMigration(t, s)
}

// openLive opens a throwaway database on the real MongoDB and drops it when the test ends.
func openLive(t *testing.T, uri, db string) *Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, closeFn, err := OpenMongo(ctx, uri, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if client, err := mongo.Connect(options.Client().ApplyURI(uri)); err == nil {
			_ = client.Database(db).Drop(context.Background())
			_ = client.Disconnect(context.Background())
		}
		_ = closeFn(context.Background())
	})
	return s
}

// TestMongoLiveBackupRestore checks backup and restore against a real MongoDB (see TestMongoLive).
func TestMongoLiveBackupRestore(t *testing.T) {
	uri := os.Getenv("MONGODB_TEST_URI")
	if uri == "" {
		t.Skip("MONGODB_TEST_URI not set")
	}
	src, dst := openLive(t, uri, "skydropx_test_backup_src"), openLive(t, uri, "skydropx_test_backup_dst")
	checkBackupRestore(t, src, dst)
	// The restored start survives a restart.
	again := openLive(t, uri, "skydropx_test_backup_dst")
	if !again.Since().Equal(day(1)) {
		t.Fatalf("since after reopening: %v", again.Since())
	}
}
