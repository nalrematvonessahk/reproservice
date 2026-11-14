package reducer

import "sort"

// LeafFirstOrder builds the dependency-aware candidate ordering used by
// AlgoDependencyAware: interventions that nothing else depends on (leaves of the
// observed dependency DAG) are tried for removal first. Per ADR-005 this only ever
// changes *order*, never rejects a candidate for having an observed dependency link.
func LeafFirstOrder(dependsOn map[string][]string) func([]string) []string {
	dependents := map[string]int{}
	for _, deps := range dependsOn {
		for _, d := range deps {
			dependents[d]++
		}
	}
	return func(ids []string) []string {
		out := append([]string{}, ids...)
		sort.SliceStable(out, func(i, j int) bool {
			return dependents[out[i]] < dependents[out[j]]
		})
		return out
	}
}
