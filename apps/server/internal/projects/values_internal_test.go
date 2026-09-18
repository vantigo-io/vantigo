package projects

import "testing"

// TestValidTaskStatus pins the task status enum (design §3.1/§4.1) ahead of
// Task 2's create/update validation, the same way suggestion_internal_test.go
// pins lettersFor/customerLetters ahead of their own endpoint tests: cheaper
// to name every case here than only through a task create request.
func TestValidTaskStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		status string
		want   bool
	}{
		{"todo", true},
		{"in-progress", true},
		{"done", true},
		{"", false},
		{"Todo", false}, // exact match only, like a project's status
		{"backlog", false},
	}
	for _, tc := range cases {
		if got := validTaskStatus(tc.status); got != tc.want {
			t.Errorf("validTaskStatus(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}
