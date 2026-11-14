package reducer

import "context"

// reproduces reports whether an outcome counts as the original failure for reduction
// purposes. Every other outcome (PASS, OTHER_FAILURE, TIMEOUT, INAPPLICABLE) means the
// candidate did not reproduce the bug we are shrinking around.
func reproduces(o Outcome) bool { return o == OutcomeOriginalFailure }

// ddmin is Zeller's classic delta-debugging minimization, adapted to operate on a set
// of intervention IDs instead of a flat byte sequence. `order`, if non-nil, is applied
// to the active set at the start of each outer iteration -- this is the only thing the
// dependency-aware variant changes; acceptance criteria are identical (ADR-005).
func ddmin(ctx context.Context, exec Executor, all []string, t *budgetTracker, order func([]string) []string) (*Result, error) {
	active := append([]string{}, all...)
	n := 2

	for len(active) >= 1 {
		if len(active) == 1 {
			break // a singleton can't be split into a strictly smaller reproducing chunk
		}
		if order != nil {
			active = order(active)
		}
		if reason := t.exhausted(); reason != "" {
			return finish(ctx, exec, t, active, reason, order != nil)
		}

		chunks := split(active, n)
		reducedInThisPass := false

		// Try each chunk alone.
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			if reason := t.exhausted(); reason != "" {
				return finish(ctx, exec, t, active, reason, order != nil)
			}
			out, err := t.run(ctx, exec, chunk)
			if err != nil {
				return nil, err
			}
			if reproduces(out) {
				active = chunk
				n = 2
				reducedInThisPass = true
				break
			}
		}
		if reducedInThisPass {
			continue
		}

		// Try each complement (active minus one chunk).
		for i, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			complement := without(active, chunk)
			if len(complement) == 0 {
				continue
			}
			if reason := t.exhausted(); reason != "" {
				return finish(ctx, exec, t, active, reason, order != nil)
			}
			out, err := t.run(ctx, exec, complement)
			if err != nil {
				return nil, err
			}
			if reproduces(out) {
				active = complement
				n = max(n-1, 2)
				reducedInThisPass = true
				_ = i
				break
			}
		}
		if reducedInThisPass {
			continue
		}

		if n >= len(active) {
			break // cannot shrink further at any granularity: 1-minimal (within accepted conditions)
		}
		n = min(n*2, len(active))
	}

	return finish(ctx, exec, t, active, StopMinimal, order != nil)
}

// sequentialRemoval tries removing one intervention at a time, left to right, keeping
// the removal permanently whenever the remaining set still reproduces the failure.
// This is the plain baseline distinct from ddmin's binary-split strategy.
func sequentialRemoval(ctx context.Context, exec Executor, all []string, t *budgetTracker) (*Result, error) {
	active := append([]string{}, all...)
	for i := 0; i < len(active); {
		if reason := t.exhausted(); reason != "" {
			return finish(ctx, exec, t, active, reason, false)
		}
		candidate := make([]string, 0, len(active)-1)
		candidate = append(candidate, active[:i]...)
		candidate = append(candidate, active[i+1:]...)
		if len(candidate) == 0 {
			i++
			continue
		}
		out, err := t.run(ctx, exec, candidate)
		if err != nil {
			return nil, err
		}
		if reproduces(out) {
			active = candidate // removal confirmed-redundant for now; stays at index i
			continue
		}
		i++
	}
	return finish(ctx, exec, t, active, StopMinimal, false)
}

func finish(ctx context.Context, exec Executor, t *budgetTracker, active []string, reason StopReason, dependencyAware bool) (*Result, error) {
	algo := string(AlgoDDMin)
	if dependencyAware {
		algo = string(AlgoDependencyAware)
	}
	if reason == StopMinimal {
		ok, unstable, err := confirm(ctx, exec, t, active)
		if err != nil {
			return &Result{Accepted: active, RunsUsed: t.runsUsed, StopReason: StopBudgetRuns, Algorithm: algo}, nil
		}
		if !ok {
			return &Result{Accepted: active, RunsUsed: t.runsUsed, StopReason: StopUnstable, Unstable: unstable, Algorithm: algo}, nil
		}
	}
	return &Result{Accepted: active, RunsUsed: t.runsUsed, StopReason: reason, Algorithm: algo}, nil
}

func split(s []string, n int) [][]string {
	if n > len(s) {
		n = len(s)
	}
	if n <= 0 {
		return [][]string{s}
	}
	chunks := make([][]string, n)
	base := len(s) / n
	rem := len(s) % n
	idx := 0
	for i := 0; i < n; i++ {
		size := base
		if i < rem {
			size++
		}
		chunks[i] = s[idx : idx+size]
		idx += size
	}
	return chunks
}

func without(s, remove []string) []string {
	skip := map[string]bool{}
	for _, r := range remove {
		skip[r] = true
	}
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !skip[v] {
			out = append(out, v)
		}
	}
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
