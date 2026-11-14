-- Test application database (orders/payments/inventory). Kept separate from the
-- platform metadata database per ADR-004: experiment data isolated from app state.
-- Restored fresh from this seed before every reduction candidate (docs/07-architecture.md).

CREATE TABLE IF NOT EXISTS orders (
    order_id    TEXT PRIMARY KEY,
    status      TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Deliberately has no unique constraint on (order_id) for "succeeded" rows: the demo
-- payments service does not deduplicate retried charges, which is the real bug that
-- FR05 / the "double-payment" scenario is designed to reveal when the proxy drops a
-- successful response and the caller retries. See src/services/payments/main.go.
CREATE TABLE IF NOT EXISTS payments (
    payment_id  TEXT PRIMARY KEY,
    order_id    TEXT NOT NULL,
    status      TEXT NOT NULL,
    amount_cents BIGINT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS inventory_reservations (
    reservation_id TEXT PRIMARY KEY,
    order_id       TEXT NOT NULL,
    sku            TEXT NOT NULL,
    status         TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_payments_order_id ON payments(order_id);
CREATE INDEX IF NOT EXISTS idx_reservations_order_id ON inventory_reservations(order_id);
