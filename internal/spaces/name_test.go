package spaces

import "testing"

func TestSanitizeAgentName(t *testing.T) {
	cases := []struct {
		raw, fallback, want string
	}{
		{"Fix Login", "claude", "fix-login"},
		{"UPPER_CASE", "claude", "upper_case"},
		{"123", "claude", "a123"},
		{"", "claude", "claude"},
		{"!!!", "claude", "claude"},
		{"a/b", "claude", "a-b"},
		{"--foo--", "claude", "foo"},
		{".hidden", "claude", "hidden"},
	}
	for _, tc := range cases {
		if got := SanitizeAgentName(tc.raw, tc.fallback); got != tc.want {
			t.Fatalf("SanitizeAgentName(%q, %q)=%q want %q", tc.raw, tc.fallback, got, tc.want)
		}
	}
	long := SanitizeAgentName(string(make([]byte, 40)), "claude")
	if !ValidAgentName(long) {
		t.Fatalf("long name invalid: %q", long)
	}
	fortyA := make([]byte, 40)
	for i := range fortyA {
		fortyA[i] = 'a'
	}
	got := SanitizeAgentName(string(fortyA), "claude")
	if got != string(fortyA[:32]) {
		t.Fatalf("truncate: %q (%d)", got, len(got))
	}
}

func TestValidAgentName(t *testing.T) {
	if !ValidAgentName("fix-login") || !ValidAgentName("a") || !ValidAgentName("a1_b-2") {
		t.Fatal("expected valid names")
	}
	if ValidAgentName("") || ValidAgentName("1abc") || ValidAgentName("Fix") || ValidAgentName("has space") {
		t.Fatal("expected invalid names")
	}
}

func TestUniqueAgentName(t *testing.T) {
	if got := UniqueAgentName("reviewer", nil); got != "reviewer" {
		t.Fatalf("free: %s", got)
	}
	if got := UniqueAgentName("reviewer", []string{"reviewer"}); got != "reviewer-2" {
		t.Fatalf("taken: %s", got)
	}
	if got := UniqueAgentName("reviewer", []string{"reviewer", "reviewer-2"}); got != "reviewer-3" {
		t.Fatalf("taken twice: %s", got)
	}
	long := string(make([]byte, 32))
	for i := range long {
		long = long[:i] + "a" + long[i+1:]
	}
	got := UniqueAgentName(long, []string{long})
	if !ValidAgentName(got) || got == long {
		t.Fatalf("long taken: %q", got)
	}
}
