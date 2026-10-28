package core

import (
	"strings"
	"testing"
	"time"
)

func TestSubmitInputValidation(t *testing.T) {
	now := time.Date(2026, 1, 10, 23, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, start, end, comment string
		days                      int
		invalid                   bool
	}{
		{"single day", "2026-01-10", "2026-01-10", "", 1, false},
		{"inclusive dates", "2026-01-11", "2026-01-24", "", 14, false},
		{"past", "2026-01-09", "2026-01-10", "", 0, true},
		{"reversed", "2026-01-12", "2026-01-11", "", 0, true},
		{"invalid date", "2026-02-30", "2026-03-02", "", 0, true},
		{"too long", "2026-01-10", "2027-01-10", "", 0, true},
		{"unicode comment", "2026-01-10", "2026-01-10", strings.Repeat("я", 1000), 1, false},
		{"long comment", "2026-01-10", "2026-01-10", strings.Repeat("я", 1001), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			days, err := (SubmitInput{tc.start, tc.end, tc.comment}).Validate(now)
			if (err != nil) != tc.invalid || days != tc.days {
				t.Fatalf("days=%d, err=%v; want days=%d, invalid=%v", days, err, tc.days, tc.invalid)
			}
		})
	}
}

func TestCheckBalance(t *testing.T) {
	for _, tc := range []struct {
		days, balance int
		want          BalanceResult
		invalid       bool
	}{
		{2, 2, BalanceResult{true, false}, false},
		{3, 3, BalanceResult{true, true}, false},
		{15, 28, BalanceResult{true, true}, false},
		{3, 2, BalanceResult{}, false},
		{1, 0, BalanceResult{}, false},
		{0, 10, BalanceResult{}, true},
		{1, -1, BalanceResult{}, true},
	} {
		got, err := CheckBalance(tc.days, tc.balance)
		if got != tc.want || (err != nil) != tc.invalid {
			t.Errorf("CheckBalance(%d,%d) = %+v, %v", tc.days, tc.balance, got, err)
		}
	}
}

func TestReleaseReservation(t *testing.T) {
	for _, tc := range []struct {
		status                              Status
		checked, reserved, release, invalid bool
	}{
		{Approved, true, true, false, false},
		{AutoApproved, true, true, false, false},
		{Rejected, true, true, true, false},
		{Rejected, true, false, false, false},
		{NeedsHRReview, true, true, true, false},
		{Approved, true, false, false, true},
		{Approved, false, true, false, true},
		{Processing, true, true, false, true},
	} {
		got, err := ReleaseReservation(tc.status, tc.checked, tc.reserved)
		if got != tc.release || (err != nil) != tc.invalid {
			t.Errorf("%+v: release=%v, err=%v", tc, got, err)
		}
	}
}
