package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("idempotency key was used with another request")
)

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

type Status string

const (
	Submitted      Status = "submitted"
	Processing     Status = "processing"
	PendingManager Status = "pending_manager"
	AutoApproved   Status = "auto_approved"
	Approved       Status = "approved"
	Rejected       Status = "rejected"
	NeedsHRReview  Status = "needs_hr_review"
)

func (s Status) Final() bool {
	return s == AutoApproved || s == Approved || s == Rejected || s == NeedsHRReview
}

type Employee struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BalanceDays int    `json:"balance_days"`
}

type Request struct {
	ID         string     `json:"id"`
	EmployeeID string     `json:"employee_id"`
	StartDate  string     `json:"start_date"`
	EndDate    string     `json:"end_date"`
	Days       int        `json:"days"`
	Comment    string     `json:"comment"`
	Status     Status     `json:"status"`
	ProcessID  string     `json:"process_instance_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	DecidedAt  *time.Time `json:"decided_at,omitempty"`
}

type SubmitInput struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	Comment   string `json:"comment"`
}

type DecisionEvent struct {
	EventID    string    `json:"event_id"`
	Type       string    `json:"type"`
	Version    int       `json:"version"`
	RequestID  string    `json:"request_id"`
	EmployeeID string    `json:"employee_id"`
	Days       int       `json:"days"`
	Result     Status    `json:"result"`
	OccurredAt time.Time `json:"occurred_at"`
}

type BalanceResult struct {
	Sufficient   bool
	NeedsManager bool
}

func (in SubmitInput) Validate(now time.Time) (int, error) {
	start, err := time.Parse(time.DateOnly, in.StartDate)
	if err != nil {
		return 0, &ValidationError{"start_date must be YYYY-MM-DD"}
	}
	end, err := time.Parse(time.DateOnly, in.EndDate)
	if err != nil {
		return 0, &ValidationError{"end_date must be YYYY-MM-DD"}
	}
	today := now.UTC().Truncate(24 * time.Hour)
	if start.Before(today) || end.Before(start) {
		return 0, &ValidationError{"start_date must be today or later; end_date must not precede it"}
	}

	days := int(end.Sub(start).Hours()/24) + 1
	if days > 365 {
		return 0, &ValidationError{"vacation cannot exceed 365 calendar days"}
	}
	if utf8.RuneCountInString(in.Comment) > 1000 {
		return 0, &ValidationError{"comment cannot exceed 1000 characters"}
	}
	return days, nil
}

func CheckBalance(days, available int) (BalanceResult, error) {
	if days <= 0 || available < 0 {
		return BalanceResult{}, fmt.Errorf("invalid days or balance")
	}
	ok := days <= available
	return BalanceResult{Sufficient: ok, NeedsManager: ok && days > 2}, nil
}

func ReleaseReservation(result Status, checked, reserved bool) (bool, error) {
	if !result.Final() || !checked {
		return false, fmt.Errorf("cannot finalize unchecked request or unknown result %q", result)
	}

	if (result == Approved || result == AutoApproved || result == NeedsHRReview) && !reserved {
		return false, fmt.Errorf("result %q requires a reservation", result)
	}

	return reserved && (result == Rejected || result == NeedsHRReview), nil
}

type Repository interface {
	Create(context.Context, string, SubmitInput, int, string) (Request, bool, error)
	Employee(context.Context, string) (Employee, error)
	List(context.Context, string, int, int) ([]Request, error)
	Reserve(context.Context, string) (BalanceResult, error)
	Finalize(context.Context, string, Status) error
}

type Service struct{ Repo Repository }

func (s Service) Submit(ctx context.Context, subject string, input SubmitInput, key string, now time.Time) (Request, bool, error) {
	if strings.TrimSpace(key) == "" || len(key) > 128 {
		return Request{}, false, &ValidationError{"Idempotency-Key is required (1–128 bytes)"}
	}

	days, err := input.Validate(now)
	if err != nil {
		return Request{}, false, err
	}
	return s.Repo.Create(ctx, subject, input, days, key)
}
