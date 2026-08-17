package classify

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		kind string
	}{
		{
			name: "working skipped",
			in:   Input{Status: "working"},
			kind: KindSkip,
		},
		{
			name: "blocked is always a decision",
			in:   Input{Status: "blocked", Excerpt: "some chrome"},
			kind: KindNeedsDecision,
		},
		{
			name: "approval question as idle",
			in: Input{
				Status:     "idle",
				PrevStatus: "working",
				Excerpt:    "Allow Herdr to edit README.md?\n[y/n]",
			},
			kind: KindNeedsDecision,
		},
		{
			name: "idle done after work",
			in: Input{
				Status:     "idle",
				PrevStatus: "working",
				Excerpt:    "All tests passed.\n3 files changed.",
			},
			kind: KindSettled,
		},
		{
			name: "done without question",
			in:   Input{Status: "done", Excerpt: "Review complete."},
			kind: KindSettled,
		},
		{
			name: "done with question still pages",
			in:   Input{Status: "done", Excerpt: "Should I open the PR?"},
			kind: KindNeedsDecision,
		},
		{
			name: "unrecognized question showing idle after work",
			in: Input{
				Status:     "idle",
				PrevStatus: "working",
				Excerpt:    "Which branch should I use:",
			},
			kind: KindNeedsDecision,
		},
		{
			name: "unknown without clues",
			in:   Input{Status: "unknown", Excerpt: "garbled"},
			kind: KindUnknownIdle,
		},
		{
			name: "first idle no previous is settled",
			in:   Input{Status: "idle", Excerpt: "Ready."},
			kind: KindSettled,
		},
		{
			name: "first-seen conversation with a question mark is not a decision",
			in: Input{
				Status: "idle",
				Title:  "Herding agents",
				Excerpt: "The current herdr skill is not enough for this.\n" +
					"What should the CLI look like?\n" +
					"A thin watcher.",
			},
			kind: KindSettled,
		},
		{
			name: "model footer always-approve is not a decision",
			in: Input{
				Status:  "idle",
				Excerpt: "Tip: Use @! for hidden files.\n❯\nGrok 4.6 (high) · always-approve\n[stable]",
			},
			kind: KindSettled,
		},
		{
			name: "older approval talk in the scrollback is ignored",
			in: Input{
				Status: "idle",
				Excerpt: "Allow the plugin to run?\n[y/n]\n" +
					"Done.\n" +
					"Wrote the design document.\n" +
					"Waiting is not reasoning.\n" +
					"A dedicated CLI would be useful.\n" +
					"Next step is the watch loop.\n" +
					"Implemented queue and classifier.\n" +
					"Tests are green.\n" +
					"Ready for review.",
			},
			kind: KindSettled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.in)
			if got.Kind != tt.kind {
				t.Fatalf("got %s (%s), want %s", got.Kind, got.Reason, tt.kind)
			}
		})
	}
}

func TestTruncateExcerpt(t *testing.T) {
	if TruncateExcerpt("  hi  ", 10) != "hi" {
		t.Fatal("trim")
	}
	long := ""
	for i := 0; i < 100; i++ {
		long += "x"
	}
	got := TruncateExcerpt(long, 10)
	if got != "xxxxxxxxxx" {
		t.Fatalf("got %q", got)
	}
}
