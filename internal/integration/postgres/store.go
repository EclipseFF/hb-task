package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"uuid"

	"hb-task/internal/core"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

const columns = `id::text, employee_id::text, start_date::text, end_date::text, days, comment,
    status, COALESCE(process_instance_id,''), created_at, decided_at`

func scanRequest(row pgx.Row) (core.Request, error) {
	var r core.Request
	err := row.Scan(&r.ID, &r.EmployeeID, &r.StartDate, &r.EndDate, &r.Days, &r.Comment,
		&r.Status, &r.ProcessID, &r.CreatedAt, &r.DecidedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, core.ErrNotFound
	}
	return r, err
}

func (s *Store) Create(ctx context.Context, subject string, in core.SubmitInput, days int, key string) (core.Request, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return core.Request{}, false, err
	}
	defer tx.Rollback(ctx)
	var employeeID string
	if err := tx.QueryRow(ctx, `SELECT id::text FROM employees WHERE keycloak_subject=$1`, subject).Scan(&employeeID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return core.Request{}, false, core.ErrNotFound
		}
		return core.Request{}, false, err
	}
	r, err := scanRequest(tx.QueryRow(ctx, `INSERT INTO vacation_requests
		(id,employee_id,start_date,end_date,days,comment,idempotency_key) VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (employee_id,idempotency_key) DO NOTHING RETURNING `+columns,
		uuid.New().String(), employeeID, in.StartDate, in.EndDate, days, in.Comment, key))
	if errors.Is(err, core.ErrNotFound) {
		r, err = scanRequest(tx.QueryRow(ctx, `SELECT `+columns+` FROM vacation_requests WHERE employee_id=$1 AND idempotency_key=$2`, employeeID, key))
		if err == nil && (r.StartDate != in.StartDate || r.EndDate != in.EndDate || r.Comment != in.Comment) {
			err = core.ErrConflict
		}
		return r, false, err
	}
	if err != nil {
		return r, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox(kind,request_id) VALUES ('start_process',$1)`, r.ID); err != nil {
		return r, false, err
	}
	return r, true, tx.Commit(ctx)
}

func (s *Store) Employee(ctx context.Context, subject string) (core.Employee, error) {
	var e core.Employee
	err := s.Pool.QueryRow(ctx, `SELECT id::text,name,balance_days FROM employees WHERE keycloak_subject=$1`, subject).Scan(&e.ID, &e.Name, &e.BalanceDays)
	if errors.Is(err, pgx.ErrNoRows) {
		err = core.ErrNotFound
	}
	return e, err
}

// Empty subject is reserved for the manager endpoint.
func (s *Store) List(ctx context.Context, subject string, limit, offset int) ([]core.Request, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+columns+` FROM vacation_requests
		WHERE ($1='' OR employee_id=(SELECT id FROM employees WHERE keycloak_subject=$1))
        ORDER BY created_at DESC,id DESC LIMIT $2 OFFSET $3`, subject, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]core.Request, 0)
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

// Lock the request first, then its employee. Re-delivery of a Camunda job
// returns the saved result and never reserves days a second time.
func (s *Store) Reserve(ctx context.Context, id string) (core.BalanceResult, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return core.BalanceResult{}, err
	}
	defer tx.Rollback(ctx)
	var employeeID string
	var days int
	var checked, sufficient bool
	err = tx.QueryRow(ctx, `SELECT employee_id::text,days,balance_checked,balance_sufficient
		FROM vacation_requests WHERE id=$1 FOR UPDATE`, id).Scan(&employeeID, &days, &checked, &sufficient)
	if err != nil {
		return core.BalanceResult{}, err
	}
	if checked {
		return core.BalanceResult{Sufficient: sufficient, NeedsManager: sufficient && days > 2}, nil
	}
	var available int
	if err = tx.QueryRow(ctx, `SELECT balance_days FROM employees WHERE id=$1 FOR UPDATE`, employeeID).Scan(&available); err != nil {
		return core.BalanceResult{}, err
	}
	result, err := core.CheckBalance(days, available)
	if err != nil {
		return result, err
	}
	if result.Sufficient {
		if _, err = tx.Exec(ctx, `UPDATE employees SET balance_days=balance_days-$2 WHERE id=$1`, employeeID, days); err != nil {
			return result, err
		}
	}
	status := core.Processing
	if result.NeedsManager {
		status = core.PendingManager
	}
	_, err = tx.Exec(ctx, `UPDATE vacation_requests SET balance_checked=true,balance_sufficient=$2,reserved=$2,status=$3 WHERE id=$1`, id, result.Sufficient, status)
	if err != nil {
		return result, err
	}
	return result, tx.Commit(ctx)
}

func (s *Store) Finalize(ctx context.Context, id string, result core.Status) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var employeeID string
	var days int
	var status core.Status
	var checked, reserved bool
	err = tx.QueryRow(ctx, `SELECT employee_id::text,days,status,balance_checked,reserved
		FROM vacation_requests WHERE id=$1 FOR UPDATE`, id).Scan(&employeeID, &days, &status, &checked, &reserved)
	if err != nil {
		return err
	}
	if status.Final() {
		if status != result {
			return fmt.Errorf("request already finalized as %s, got %s", status, result)
		}
		return nil
	}
	release, err := core.ReleaseReservation(result, checked, reserved)
	if err != nil {
		return err
	}
	if release {
		if _, err = tx.Exec(ctx, `UPDATE employees SET balance_days=balance_days+$2 WHERE id=$1`, employeeID, days); err != nil {
			return err
		}
	}
	now := time.Now().UTC()
	if _, err = tx.Exec(ctx, `UPDATE vacation_requests SET status=$2,reserved=false,decided_at=$3 WHERE id=$1`, id, result, now); err != nil {
		return err
	}
	event := core.DecisionEvent{EventID: id, Type: "vacation.decided", Version: 1, RequestID: id, EmployeeID: employeeID, Days: days, Result: result, OccurredAt: now}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO outbox(kind,request_id,payload) VALUES ('decision',$1,$2)`, id, payload); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type OutboxMessage struct {
	ID              int64
	Kind, RequestID string
	Payload         []byte
}

// One short transaction per message, including I/O. This is deliberately simple
// for a small demo; SKIP LOCKED permits another dispatcher to make progress.
func (s *Store) DispatchOne(ctx context.Context, send func(context.Context, OutboxMessage) (string, error)) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var m OutboxMessage
	var attempts int
	err = tx.QueryRow(ctx, `SELECT id,kind,request_id::text,payload,attempts FROM outbox
		WHERE published_at IS NULL AND next_attempt_at<=now() ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&m.ID, &m.Kind, &m.RequestID, &m.Payload, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	processID, sendErr := send(ctx, m)
	if sendErr != nil {
		delay := min(60, 1<<min(attempts, 6))
		_, err = tx.Exec(ctx, `UPDATE outbox SET attempts=attempts+1,last_error=$2,next_attempt_at=now()+make_interval(secs => $3) WHERE id=$1`, m.ID, sendErr.Error(), delay)
	} else {
		_, err = tx.Exec(ctx, `UPDATE outbox SET published_at=now(),last_error=NULL WHERE id=$1`, m.ID)
		if err == nil && m.Kind == "start_process" {
			_, err = tx.Exec(ctx, `UPDATE vacation_requests SET process_instance_id=$2 WHERE id=$1`, m.RequestID, processID)
		}
	}
	if err != nil {
		return true, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, err
	}
	return true, sendErr
}

func (s *Store) Request(ctx context.Context, id string) (core.Request, error) {
	return scanRequest(s.Pool.QueryRow(ctx, `SELECT `+columns+` FROM vacation_requests WHERE id=$1`, id))
}
