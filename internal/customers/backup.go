package customers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// backupVersion is the format of the backup file; bump it if the format changes incompatibly.
const backupVersion = 1

// Backup is a full copy of the customer list: every customer with all their orders, consent and
// opt-out, plus Since (needed so a migration after a restore doesn't duplicate orders).
type Backup struct {
	Version   int        `json:"version"`
	CreatedAt time.Time  `json:"created_at"`
	Since     time.Time  `json:"since"`
	Customers []Customer `json:"customers"`
}

// Backup copies the whole list.
func (s *Store) Backup(ctx context.Context) (Backup, error) {
	all, err := s.All(ctx)
	if err != nil {
		return Backup{}, err
	}
	return Backup{Version: backupVersion, CreatedAt: s.now(), Since: s.since, Customers: all}, nil
}

// WriteBackup writes a backup as JSON.
func WriteBackup(w io.Writer, b Backup) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", " ")
	return enc.Encode(b)
}

// ReadBackup reads a backup written by WriteBackup.
func ReadBackup(r io.Reader) (Backup, error) {
	var b Backup
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return Backup{}, fmt.Errorf("the file isn't a customer backup: %w", err)
	}
	if b.Version != backupVersion {
		return Backup{}, fmt.Errorf("unknown backup version %d", b.Version)
	}
	for _, c := range b.Customers {
		if c.ID == "" {
			return Backup{}, errors.New("the backup has a customer without ID")
		}
	}
	return b, nil
}

// RestoreResult summarizes a restore.
type RestoreResult struct {
	InBackup     int // customers in the file
	Restored     int // customers that were missing and were put back
	AlreadyThere int // customers that exist: left untouched
	Conflicts    int // missing customers whose document now belongs to another customer: not restored
}

// Restore puts back the customers of a backup that are missing from the list. It never overwrites nor
// deletes: customers that exist stay as they are, with whatever happened after the backup. Since moves
// back to the backup's if that's earlier.
func (s *Store) Restore(ctx context.Context, b Backup) (RestoreResult, error) {
	r := RestoreResult{InBackup: len(b.Customers)}
	for _, c := range b.Customers {
		inserted, err := s.repo.insertIfMissing(ctx, c)
		switch {
		case errors.Is(err, errDuplicate):
			r.Conflicts++
		case err != nil:
			return r, err
		case inserted:
			r.Restored++
		default:
			r.AlreadyThere++
		}
	}
	if !b.Since.IsZero() && b.Since.Before(s.since) {
		since, err := s.repo.restoreSince(ctx, b.Since)
		if err != nil {
			return r, err
		}
		s.since = since
	}
	return r, nil
}
