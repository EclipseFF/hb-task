CREATE TABLE IF NOT EXISTS employees (
    id uuid PRIMARY KEY,
    keycloak_subject text NOT NULL,
    name text NOT NULL,
    balance_days integer NOT NULL CHECK (balance_days >= 0)
);

ALTER TABLE employees ADD COLUMN IF NOT EXISTS keycloak_subject text;
UPDATE employees SET keycloak_subject = id::text WHERE keycloak_subject IS NULL;
ALTER TABLE employees ALTER COLUMN keycloak_subject SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS employees_keycloak_subject_key ON employees(keycloak_subject);

CREATE TABLE IF NOT EXISTS vacation_requests (
    id uuid PRIMARY KEY,
    employee_id uuid NOT NULL REFERENCES employees(id),
    start_date date NOT NULL,
    end_date date NOT NULL CHECK (end_date >= start_date),
    days integer NOT NULL CHECK (days BETWEEN 1 AND 365),
    comment text NOT NULL DEFAULT '',
    status text NOT NULL DEFAULT 'submitted' CHECK (status IN
        ('submitted','processing','pending_manager','auto_approved','approved','rejected','needs_hr_review')),
    idempotency_key varchar(128) NOT NULL,
    process_instance_id text,
    balance_checked boolean NOT NULL DEFAULT false,
    balance_sufficient boolean NOT NULL DEFAULT false,
    reserved boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    decided_at timestamptz,
    UNIQUE (employee_id, idempotency_key)
);

ALTER TABLE vacation_requests ALTER COLUMN id DROP DEFAULT;
CREATE INDEX IF NOT EXISTS requests_employee_created ON vacation_requests(employee_id, created_at DESC);

CREATE TABLE IF NOT EXISTS outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('start_process','decision')),
    request_id uuid NOT NULL REFERENCES vacation_requests(id),
    payload jsonb NOT NULL DEFAULT '{}',
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error text,
    published_at timestamptz,
    UNIQUE (kind, request_id)
);
CREATE INDEX IF NOT EXISTS outbox_pending ON outbox(next_attempt_at, id) WHERE published_at IS NULL;

INSERT INTO employees (id, keycloak_subject, name, balance_days) VALUES
    (gen_random_uuid(), '11111111-1111-4111-8111-111111111111', 'Сотрудник1', 28),
    (gen_random_uuid(), '22222222-2222-4222-8222-222222222222', 'Сотрудник2', 10)
ON CONFLICT (keycloak_subject) DO NOTHING;