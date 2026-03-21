package governance

import "testing"

func TestParseVoteChoice(t *testing.T) {
	t.Parallel()

	testCases := map[string]VoteChoice{
		"yes":     VoteFor,
		"for":     VoteFor,
		"1":       VoteFor,
		"no":      VoteAgainst,
		"against": VoteAgainst,
		"0":       VoteAgainst,
		"abstain": VoteAbstain,
		"2":       VoteAbstain,
	}

	for input, want := range testCases {
		got, err := ParseVoteChoice(input)
		if err != nil {
			t.Fatalf("ParseVoteChoice(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseVoteChoice(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNormalizeProposalTitle(t *testing.T) {
	t.Parallel()

	if got := NormalizeProposalTitle("", 7); got != "UGP-007" {
		t.Fatalf("NormalizeProposalTitle fallback = %q, want UGP-007", got)
	}
}
