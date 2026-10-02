package action

import "sort"

// SupportedLabels returns every label the registry resolves.
//
// It used to be a hand-written list, and it was wrong in the way hand-written
// lists always are: twenty labels behind by the time anyone measured it, missing
// `place` and the whole portal, stronghold, trading and end-portal surface. A
// test that asserted against it was asserting against a fiction that happened to
// contain mostly true statements.
//
// The registry is the truth and it is already guarded, so the list is derived
// from it. That makes the check impossible to pass by accident and impossible
// to pass wrongly — which is the only acceptable property for a test fixture.
func SupportedLabels() map[string]struct{} {
	out := make(map[string]struct{}, len(ActionHandlers))
	for label := range ActionHandlers {
		out[label] = struct{}{}
	}
	return out
}

// AllLabels is SupportedLabels as a sorted slice, for a prompt or a log that
// wants a stable order rather than a set.
func AllLabels() []string {
	labels := SupportedLabels()
	out := make([]string, 0, len(labels))
	for label := range labels {
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}
