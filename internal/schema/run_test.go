package schema

import "testing"

func TestCanTransition_HappyPath(t *testing.T) {
	path := []RunState{StateValidating, StateResetting, StateReady, StateRunning, StateAsserting, StateCompleted}
	for i := 0; i < len(path)-1; i++ {
		if !CanTransition(path[i], path[i+1]) {
			t.Fatalf("expected %s -> %s to be legal", path[i], path[i+1])
		}
	}
}

func TestCanTransition_AbortFromAnyWorkingState(t *testing.T) {
	for _, s := range []RunState{StateValidating, StateResetting, StateReady, StateRunning, StateAsserting} {
		if !CanTransition(s, StateCancelled) {
			t.Fatalf("expected %s -> CANCELLED to be legal", s)
		}
		if !CanTransition(s, StateInfraError) {
			t.Fatalf("expected %s -> INFRA_ERROR to be legal", s)
		}
	}
}

func TestCanTransition_TerminalIsFinal(t *testing.T) {
	for _, s := range []RunState{StateCompleted, StateCancelled, StateInfraError} {
		if CanTransition(s, StateRunning) {
			t.Fatalf("terminal state %s must not transition further", s)
		}
	}
}

func TestCanTransition_NoSkippingStates(t *testing.T) {
	if CanTransition(StateValidating, StateRunning) {
		t.Fatal("must not be able to skip RESETTING/READY")
	}
}
