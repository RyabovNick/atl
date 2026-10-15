package cli

import "testing"

func TestKeyFromBranchName(t *testing.T) {
	for branch, want := range map[string]string{
		"feature/PROJ-123-fix-login": "PROJ-123",
		"PROJ-7":                     "PROJ-7",
		"bugfix/ab2-45_thing":        "AB2-45",
		"main":                       "",
	} {
		got, _ := keyFromBranchName(branch)
		if got != want {
			t.Errorf("%s: got %q, want %q", branch, got, want)
		}
	}
}
