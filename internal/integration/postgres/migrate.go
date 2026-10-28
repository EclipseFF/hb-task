package postgres

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrations embed.FS

func (s *Store) newMigrator() (*migrate.Migrate, error) {
	source, err := iofs.New(migrations, "migrations")
	if err != nil {
		return nil, err
	}

	config := s.Pool.Config().ConnConfig
	config.ConnectTimeout = 5 * time.Second
	config.RuntimeParams["statement_timeout"] = "30000"
	db := stdlib.OpenDB(*config)
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{StatementTimeout: 30 * time.Second})
	if err != nil {
		_ = source.Close()
		_ = db.Close()
		return nil, err
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return nil, err
	}

	m.LockTimeout = 10 * time.Second
	return m, nil
}

func (s *Store) Migrate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m, err := s.newMigrator()
	if err != nil {
		return fmt.Errorf("initialize migrations: %w", err)
	}

	defer m.Close()
	stop := context.AfterFunc(ctx, func() { m.GracefulStop <- true })
	defer stop()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}

	return ctx.Err()
}
