package reducer

// ddmin is Zeller's classic delta-debugging minimization, adapted to operate on a set
// of intervention IDs instead of a flat byte sequence. The session's Options.Order, if
// set, is applied to the active set at the start of each outer iteration -- this is the
// only thing the dependency-aware variant changes; acceptance criteria are identical
// (ADR-005).
//
// Termination: the loop ends when, at granularity n == len(active), every complement
// (each single-element removal) was tried and rejected -- 1-minimality over valid
// candidates -- or, for a singleton, when the empty set was tried and rejected.
func (s *session) ddmin(all []string) *halt {
	active := append([]string{}, all...)
	n := 2

	for {
		switch len(active) {
		case 0:
			return nil
		case 1:
			// A singleton cannot be split; the only strictly smaller candidate is the
			// empty set (no interventions at all). Probing it is what makes the final
			// "minimal" claim true for single-element results.
			_, h := s.attempt([]string{}) // accepted -> s.confirmed becomes the empty set
			return h
		}
		if s.opts.Order != nil {
			active = s.opts.Order(active)
		}

		chunks := split(active, n)
		reduced := false

		// Reduce to subset: try each chunk alone.
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			ok, h := s.attempt(chunk)
			if h != nil {
				return h
			}
			if ok {
				active, n, reduced = append([]string{}, chunk...), 2, true
				break
			}
		}
		if reduced {
			continue
		}

		// Reduce to complement: remove one chunk. (At n == 2 the complements equal the
		// chunks already tried; the result cache answers them without spending budget.)
		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			complement := without(active, chunk)
			if len(complement) == 0 {
				continue
			}
			ok, h := s.attempt(complement)
			if h != nil {
				return h
			}
			if ok {
				active, n, reduced = complement, max(n-1, 2), true
				break
			}
		}
		if reduced {
			continue
		}

		if n >= len(active) {
			return nil // cannot shrink further at any granularity: 1-minimal
		}
		n = min(n*2, len(active))
	}
}

// sequential tries removing one intervention at a time, left to right, keeping the
// removal permanently whenever the remainder still reproduces the failure (confirmed
// 3/3). It repeats full passes until a pass removes nothing (ADR-005: "с повторным
// проходом после принятого удаления"), so the result is 1-minimal; removal of the last
// element (the empty set) is tried too.
func (s *session) sequential(all []string) *halt {
	active := append([]string{}, all...)
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(active); {
			candidate := make([]string, 0, len(active)-1)
			candidate = append(candidate, active[:i]...)
			candidate = append(candidate, active[i+1:]...)
			ok, h := s.attempt(candidate)
			if h != nil {
				return h
			}
			if ok {
				active = candidate // stays at index i
				changed = true
				continue
			}
			i++
		}
	}
	return nil
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
