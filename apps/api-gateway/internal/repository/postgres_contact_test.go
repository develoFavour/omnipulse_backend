package repository

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// stubContactRow feeds scanContact the same twelve columns contactColumns
// selects, so a NULL in any of them is exercised the way Postgres delivers it.
type stubContactRow struct {
	values []interface{}
}

func (r stubContactRow) Scan(dest ...interface{}) error {
	if len(dest) != len(r.values) {
		return fmt.Errorf("scan target count %d does not match column count %d", len(dest), len(r.values))
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *string:
			if r.values[i] != nil {
				*p = r.values[i].(string)
			}
		case *sql.NullString:
			if s, ok := r.values[i].(string); ok {
				*p = sql.NullString{String: s, Valid: true}
			} else {
				*p = sql.NullString{}
			}
		case *time.Time:
			*p = r.values[i].(time.Time)
		case **time.Time:
			if t, ok := r.values[i].(time.Time); ok {
				*p = &t
			} else {
				*p = nil
			}
		default:
			return fmt.Errorf("unsupported scan target %T at column %d", d, i)
		}
	}
	return nil
}

func TestScanContactHandlesNullableColumns(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	deadline := now.Add(30 * 24 * time.Hour)

	cases := []struct {
		name           string
		lastName       interface{}
		anonymizedAt   interface{}
		deleteAfterAt  interface{}
		wantLast       string
		wantAnonymized bool
	}{
		{
			name:           "erased recipient has no surname",
			lastName:       nil,
			anonymizedAt:   now,
			deleteAfterAt:  deadline,
			wantLast:       "",
			wantAnonymized: true,
		},
		{
			name:           "ordinary recipient keeps surname and has no deadline",
			lastName:       "Opia",
			anonymizedAt:   nil,
			deleteAfterAt:  nil,
			wantLast:       "Opia",
			wantAnonymized: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := stubContactRow{values: []interface{}{
				"contact-1", "tenant-1", "Redacted recipient", tc.lastName,
				"whatsapp", "redacted-contact-1", "manual", "opted_out",
				now, now, tc.anonymizedAt, tc.deleteAfterAt,
			}}

			c, err := scanContact(row)
			if err != nil {
				t.Fatalf("scanContact returned an error: %v", err)
			}
			if c.LastName != tc.wantLast {
				t.Errorf("LastName = %q, want %q", c.LastName, tc.wantLast)
			}
			if got := c.AnonymizedAt != nil; got != tc.wantAnonymized {
				t.Errorf("AnonymizedAt set = %v, want %v", got, tc.wantAnonymized)
			}
			if tc.wantAnonymized {
				if c.DeleteAfterAt == nil || !c.DeleteAfterAt.Equal(deadline) {
					t.Errorf("DeleteAfterAt = %v, want %v", c.DeleteAfterAt, deadline)
				}
			} else if c.DeleteAfterAt != nil {
				t.Errorf("DeleteAfterAt = %v, want nil", c.DeleteAfterAt)
			}
		})
	}
}

func TestScanContactRejectsColumnCountDrift(t *testing.T) {
	row := stubContactRow{values: []interface{}{"contact-1", "tenant-1"}}
	if _, err := scanContact(row); err == nil {
		t.Fatal("expected an error when the selected columns and scan targets disagree")
	}
}
