package postgres

import (
	"sync"
	"testing"
	"time"
	"uuid"

	"hb-task/internal/core"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestConcurrentReservationsAndRepeatedTasks(t *testing.T) {
	const dsn = "postgres://vacation:vacation@postgres:5432/vacation_test?sslmode=disable"
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &Store{Pool: pool}
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	employeeID, subject := uuid.New().String(), "test-user-"+uuid.New().String()
	if _, err := pool.Exec(ctx, `INSERT INTO employees(id,keycloak_subject,name,balance_days) VALUES($1,$2,'test',10)`, employeeID, subject); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM outbox WHERE request_id IN (SELECT id FROM vacation_requests WHERE employee_id=$1)`, employeeID)
		_, _ = pool.Exec(ctx, `DELETE FROM vacation_requests WHERE employee_id=$1`, employeeID)
		_, _ = pool.Exec(ctx, `DELETE FROM employees WHERE id=$1`, employeeID)
	}()
	input := core.SubmitInput{StartDate: "2030-01-01", EndDate: "2030-01-07"}
	var requests []core.Request
	for _, key := range []string{"one", "two"} {
		r, _, err := store.Create(ctx, subject, input, 7, key)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, r)
	}
	var wg sync.WaitGroup
	results := make(chan string, 2)
	for _, r := range requests {
		wg.Go(func() {
			result, err := store.Reserve(ctx, r.ID)
			if err != nil {
				t.Error(err)
				return
			}
			if result.Sufficient {
				results <- r.ID
			}
		})
	}
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("expected exactly one reservation, got %d", len(results))
	}
	winner := <-results
	for range 3 {
		if _, err := store.Reserve(ctx, winner); err != nil {
			t.Fatal(err)
		}
	}
	e, err := store.Employee(ctx, subject)
	if err != nil || e.BalanceDays != 3 {
		t.Fatalf("balance after retries: %+v, %v", e, err)
	}
	for range 3 {
		if err := store.Finalize(ctx, winner, core.Rejected); err != nil {
			t.Fatal(err)
		}
	}
	e, err = store.Employee(ctx, subject)
	if err != nil || e.BalanceDays != 10 {
		t.Fatalf("balance after repeated rejection: %+v, %v", e, err)
	}
	if err := store.Finalize(ctx, winner, core.Approved); err == nil {
		t.Fatal("contradictory decision accepted")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE request_id=$1 AND kind='decision'`, winner).Scan(&count); err != nil || count != 1 {
		t.Fatalf("decision events=%d, err=%v", count, err)
	}
	r, created, err := (core.Service{Repo: store}).Submit(ctx, subject, input, "one", time.Date(2029, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || created || r.ID != requests[0].ID {
		t.Fatalf("idempotent create: %+v, %v, %v", r, created, err)
	}
}
