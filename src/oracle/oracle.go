// Package oracle implements the "Oracle" component from docs/07-architecture.md:
// pre-registered API/SQL invariant checks that return a specific invariant ID plus
// expected/actual values (FR06). Only invariant IDs known to this package are
// accepted -- there is no way to pass arbitrary SQL in from a scenario file (ADR-004).
package oracle

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"reproservice/internal/schema"
)

type Oracle struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Oracle { return &Oracle{pool: pool} }

// Check returns an infra-error assertion (never a false violation) if the check
// itself cannot run -- FR06 requires infra failures to never be reported as a
// violation of the invariant under test.
func (o *Oracle) Check(ctx context.Context, invariantID, orderID string) (schema.Assertion, error) {
	switch invariantID {
	case "payment_exactly_once":
		return o.paymentExactlyOnce(ctx, orderID)
	default:
		return schema.Assertion{RunID: "", InvariantID: invariantID, InfraError: true},
			fmt.Errorf("oracle: unknown invariant_id %q", invariantID)
	}
}

func (o *Oracle) paymentExactlyOnce(ctx context.Context, orderID string) (schema.Assertion, error) {
	var count int
	err := o.pool.QueryRow(ctx,
		`SELECT count(*) FROM payments WHERE order_id = $1 AND status = 'succeeded'`, orderID,
	).Scan(&count)
	if err != nil {
		return schema.Assertion{InvariantID: "payment_exactly_once", InfraError: true}, fmt.Errorf("oracle: query payments: %w", err)
	}
	return schema.Assertion{
		InvariantID: "payment_exactly_once",
		Expected:    "1",
		Actual:      fmt.Sprintf("%d", count),
		Violated:    count != 1,
	}, nil
}
