package customers

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func day(d int) time.Time { return time.Date(2026, 11, d, 10, 0, 0, 0, colombia) }

func TestNormalize(t *testing.T) {
	if NormalizePhone("+57 300-123-4567") != "3001234567" || NormalizePhone("123") != "" {
		t.Error("NormalizePhone")
	}
	if NormalizeDocument("1.234.567.890") != "1234567890" || NormalizeDocument("12") != "" {
		t.Error("NormalizeDocument")
	}
}

func TestRecordOrderMergesTheSameCustomer(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	s.now = func() time.Time { return day(16) }

	// First order: no consent, only phone.
	c, err := s.RecordOrder(ctx, Customer{Name: "Ana", Phone: "300 111 2233", City: "Montería", Source: "bot"},
		Order{Date: day(1), Units: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "tel:3001112233" || c.Contactable() {
		t.Fatalf("first order: %+v", c)
	}
	// Second order with document and consent: same person (matched by phone), data and consent updated.
	c, _ = s.RecordOrder(ctx, Customer{Name: "Ana Pérez", DocumentID: "1.111.111", Phone: "3001112233", City: "Cereté",
		Consent: Consent{Given: true, Date: day(5), Evidence: "SÍ"}, Source: "bot"}, Order{Date: day(5), Units: 2})
	if c.ID != "tel:3001112233" || c.DocumentID != "1111111" || c.Name != "Ana Pérez" || c.City != "Cereté" {
		t.Fatalf("second order should update the same customer: %+v", c)
	}
	if !c.Contactable() || len(c.Orders) != 2 || !c.FirstOrderAt.Equal(day(1)) || !c.LastOrderAt.Equal(day(5)) {
		t.Fatalf("orders/consent: %+v", c)
	}
	// A new order without consent nor phone updates the data it brings, erases nothing and keeps consent.
	c, _ = s.RecordOrder(ctx, Customer{Name: "Ana P.", DocumentID: "1111111", City: "Lorica"}, Order{Date: day(6)})
	if c.Name != "Ana P." || c.City != "Lorica" || c.Phone != "3001112233" || !c.Consent.Given || len(c.Orders) != 3 {
		t.Fatalf("third order: %+v", c)
	}
	// Someone else with another document sharing the phone is a different customer.
	other, _ := s.RecordOrder(ctx, Customer{Name: "Primo", DocumentID: "2222222", Phone: "3001112233"}, Order{Date: day(7)})
	if other.ID == c.ID {
		t.Fatal("a different document must be a different customer")
	}
	if _, err := s.RecordOrder(ctx, Customer{Name: "Sin datos"}, Order{}); err == nil {
		t.Error("a customer without document nor phone must be rejected")
	}
}

func TestConsentAndOptOut(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	_, _ = s.RecordOrder(ctx, Customer{Name: "Luis", Phone: "3105556677"}, Order{Date: day(2)})

	c, err := s.GrantConsent(ctx, "310 555 6677", "Respondió SÍ por WhatsApp", "Tú")
	if err != nil || !c.Contactable() {
		t.Fatalf("consent: %+v %v", c, err)
	}
	c, _ = s.OptOut(ctx, "3105556677")
	if c.Contactable() || !c.Consent.Given || c.OptOutAt == nil {
		t.Fatalf("opt-out keeps the evidence but stops contact: %+v", c)
	}
	// A customer with a document is also found by their phone.
	_, _ = s.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "1111111", Phone: "3001112233"}, Order{Date: day(3)})
	if c, err := s.OptOut(ctx, "300 111 2233"); err != nil || c.Name != "Ana" || c.Contactable() {
		t.Fatalf("by phone: %+v %v", c, err)
	}
	if c, err := s.Find(ctx, "1.111.111"); err != nil || c.Name != "Ana" {
		t.Fatalf("by document: %+v %v", c, err)
	}
	if _, err := s.OptOut(ctx, "3000000000"); err != ErrNotFound {
		t.Errorf("unknown customer: %v", err)
	}
}

func TestSourceSummaryAndCSV(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	_, _ = s.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "111111", Phone: "3001112233", City: "Montería", Source: " Facebook "}, Order{Date: day(1)})
	// Buying again from another campaign keeps where she originally came from.
	_, _ = s.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "111111", Source: "WhatsApp"}, Order{Date: day(3), Units: 2})
	_, _ = s.RecordOrder(ctx, Customer{Name: "Luis", Phone: "3105556677", City: "Sincelejo"}, Order{Date: day(2)})

	all, _ := s.All(ctx)
	st := Summary(all)
	if st.Customers != 2 || st.Repeat != 1 || st.Orders != 3 || st.ByCity["Montería"] != 1 ||
		st.BySource["facebook"] != 1 || st.BySource[""] != 1 {
		t.Fatalf("summary: %+v", st)
	}
	if all[0].Name != "Ana" || all[0].Source != "facebook" {
		t.Fatalf("most recent buyer first, first source kept: %+v", all[0])
	}

	var buf bytes.Buffer
	if err := WriteCSV(&buf, all); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "\xef\xbb\xbfnombre,cedula,telefono") || !strings.Contains(out, "Ana,111111,3001112233,,,Montería,,,no,,,,2,2026-11-01,2026-11-03,facebook") {
		t.Fatalf("csv:\n%s", out)
	}
}

// recordConcurrently records n orders of the same new customer at once, plus consent and opt-out of
// another one, and checks nothing was lost or duplicated.
func recordConcurrently(t *testing.T, s *Store, n int) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.RecordOrder(ctx, Customer{Name: "Luis", Phone: "3105556677"}, Order{Date: day(1)}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "1111111", Phone: "3001112233"}, Order{Date: day(2), Units: 1})
			errs <- err
		}()
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				_, err = s.RecordOrder(ctx, Customer{Phone: "3105556677"}, Order{Date: day(3)})
			} else {
				_, err = s.GrantConsent(ctx, "3105556677", "test", "test")
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	all, err := s.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("the same document must be one customer, got %d customers", len(all))
	}
	ana, _ := s.Find(ctx, "1111111")
	luis, _ := s.Find(ctx, "3105556677")
	if len(ana.Orders) != n || len(luis.Orders) != 1+(n+1)/2 || !luis.Consent.Given {
		t.Fatalf("lost writes: Ana %d orders (want %d), Luis %d orders (want %d), consent %v",
			len(ana.Orders), n, len(luis.Orders), 1+(n+1)/2, luis.Consent.Given)
	}
}

func TestConcurrentWritesMemory(t *testing.T) { recordConcurrently(t, NewMemoryStore(), 20) }

// checkMigration migrates past shipments into a list where the bot already recorded one customer.
func checkMigration(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	s.since = day(10)
	if _, err := s.RecordOrder(ctx, Customer{Name: "Ana Pérez", DocumentID: "111111", Phone: "3001112233", City: "Cereté",
		Source: "facebook", Consent: Consent{Given: true, Evidence: "SÍ"}}, Order{Date: day(12), Units: 1}); err != nil {
		t.Fatal(err)
	}
	history := []Sighting{
		{Customer{Name: "ANA", DocumentID: "111111", City: "Montería", Email: "ana@mail.com"}, Order{Date: day(2)}},
		{Customer{Name: "Ana", Phone: "300 111 2233", City: "Lorica"}, Order{Date: day(5)}},
		{Customer{Name: "Luis viejo", Phone: "3105556677", City: "Sincelejo"}, Order{Date: day(1)}},
		{Customer{Name: "Luis", Phone: "3105556677", City: "Sincelejo"}, Order{Date: day(3)}},
		{Customer{Name: "Nadie"}, Order{Date: day(4)}},
		{Customer{Name: "Pedro", Phone: "3200000000"}, Order{Date: day(11)}}, // the bot already recorded it
	}
	r, err := s.Migrate(ctx, append([]Sighting(nil), history...))
	if err != nil {
		t.Fatal(err)
	}
	if r != (MigrationResult{Shipments: 6, New: 1, OrdersAdded: 4, NoKeys: 1, AfterSince: 1}) {
		t.Fatalf("first migration: %+v", r)
	}
	ana, _ := s.Find(ctx, "111111")
	if ana.Name != "Ana Pérez" || ana.City != "Cereté" || ana.Email != "ana@mail.com" || ana.Source != "facebook" ||
		!ana.Contactable() || len(ana.Orders) != 3 || !ana.FirstOrderAt.Equal(day(2)) || !ana.LastOrderAt.Equal(day(12)) {
		t.Fatalf("the migration only fills empty fields of what the bot recorded: %+v", ana)
	}
	luis, _ := s.Find(ctx, "3105556677")
	if luis.Name != "Luis" || luis.Source != SourceSkydropx || luis.Contactable() || len(luis.Orders) != 2 {
		t.Fatalf("migrated customer: newest data, source skydropx, no consent: %+v", luis)
	}
	if _, err := s.Find(ctx, "3200000000"); err != ErrNotFound {
		t.Fatal("shipments after Since aren't migrated")
	}

	r, err = s.Migrate(ctx, append([]Sighting(nil), history...))
	if err != nil {
		t.Fatal(err)
	}
	if r != (MigrationResult{Shipments: 6, Repeated: 4, NoKeys: 1, AfterSince: 1}) {
		t.Fatalf("running it again must not duplicate: %+v", r)
	}
	if all, _ := s.All(ctx); len(all) != 2 {
		t.Fatalf("customers: %d", len(all))
	}
}

func TestMigrationMemory(t *testing.T) { checkMigration(t, NewMemoryStore()) }

// checkBackupRestore backs up src, then restores it into dst, which lost some customers and changed others.
func checkBackupRestore(t *testing.T, src, dst *Store) {
	t.Helper()
	ctx := context.Background()
	src.since, dst.since = day(1), day(10)
	_, _ = src.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "111111", Phone: "3001112233", Source: "facebook",
		Consent: Consent{Given: true, Evidence: "SÍ"}}, Order{Date: day(2), Units: 2, UnitPrice: 60000, CreatedBy: "V"})
	_, _ = src.RecordOrder(ctx, Customer{Name: "Ana", DocumentID: "111111"}, Order{Date: day(4), Units: 1})
	_, _ = src.RecordOrder(ctx, Customer{Name: "Luis", Phone: "3105556677"}, Order{Date: day(3)})
	_, _ = src.OptOut(ctx, "3105556677")
	_, _ = src.RecordOrder(ctx, Customer{Name: "Marta", DocumentID: "333333"}, Order{Date: day(3)})

	b, err := src.Backup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteBackup(&buf, b); err != nil {
		t.Fatal(err)
	}
	b, err = ReadBackup(&buf)
	if err != nil || len(b.Customers) != 3 || !b.Since.Equal(day(1)) {
		t.Fatalf("round trip: %d customers, since %v, %v", len(b.Customers), b.Since, err)
	}

	// dst: Luis bought again after the backup (must stay as is), and Marta's document now belongs to a
	// customer created another way (conflict). Ana is missing.
	_, _ = dst.RecordOrder(ctx, Customer{Name: "Luis", Phone: "3105556677"}, Order{Date: day(12)})
	_, _ = dst.RecordOrder(ctx, Customer{Name: "Marta", Phone: "3150000000"}, Order{Date: day(11)})
	_, _ = dst.RecordOrder(ctx, Customer{Name: "Marta", DocumentID: "333333", Phone: "3150000000"}, Order{Date: day(12)})

	r, err := dst.Restore(ctx, b)
	if err != nil {
		t.Fatal(err)
	}
	if r != (RestoreResult{InBackup: 3, Restored: 1, AlreadyThere: 1, Conflicts: 1}) {
		t.Fatalf("restore: %+v", r)
	}
	ana, err := dst.Find(ctx, "111111")
	if err != nil || !ana.Contactable() || ana.Source != "facebook" || len(ana.Orders) != 2 || ana.Orders[0].UnitPrice != 60000 {
		t.Fatalf("restored customer must come back complete: %+v %v", ana, err)
	}
	luis, _ := dst.Find(ctx, "3105556677")
	if len(luis.Orders) != 1 || luis.OptOutAt != nil {
		t.Fatalf("an existing customer is never overwritten: %+v", luis)
	}
	if !dst.Since().Equal(day(1)) {
		t.Fatalf("since must move back to the backup's: %v", dst.Since())
	}
	if r, _ := dst.Restore(ctx, b); r.Restored != 0 {
		t.Fatalf("restoring twice changes nothing: %+v", r)
	}
}

func TestBackupRestoreMemory(t *testing.T) { checkBackupRestore(t, NewMemoryStore(), NewMemoryStore()) }

func TestReadBackupRejectsOtherFiles(t *testing.T) {
	for _, in := range []string{"nombre,cedula\nAna,1", `{"version":9,"customers":[]}`, `{"version":1,"customers":[{"name":"x"}]}`} {
		if _, err := ReadBackup(strings.NewReader(in)); err == nil {
			t.Errorf("must reject %q", in)
		}
	}
}

func TestRealSourceReplacesSkydropx(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	_, _ = s.RecordOrder(ctx, Customer{Name: "Ana", Phone: "3001112233", Source: SourceSkydropx}, Order{Date: day(1)})
	c, _ := s.RecordOrder(ctx, Customer{Name: "Ana", Phone: "3001112233", Source: "Facebook"}, Order{Date: day(2)})
	if c.Source != "facebook" {
		t.Fatalf("source = %q, want facebook replacing the skydropx placeholder", c.Source)
	}
	c, _ = s.RecordOrder(ctx, Customer{Name: "Ana", Phone: "3001112233", Source: SourceSkydropx}, Order{Date: day(3)})
	if c.Source != "facebook" {
		t.Fatalf("source = %q, a later skydropx must not replace facebook", c.Source)
	}
}
