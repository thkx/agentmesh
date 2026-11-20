package coding

import (
	"strings"
	"testing"
)

func TestParseReviewVerdict(t *testing.T) {
	got, err := parseReviewVerdict("Review result:\n```json\n{\"verdict\":\"approve\",\"findings\":[]}\n```")
	if err != nil || got.Verdict != "approve" { t.Fatalf("verdict=%+v err=%v", got, err) }
	got, err = parseReviewVerdict("{\"verdict\":\"request_changes\",\"findings\":[\"missing auth check\"]}")
	if err != nil || got.Verdict != "request_changes" || len(got.Findings) != 1 { t.Fatalf("verdict=%+v err=%v", got, err) }
	for _, invalid := range []string{"", "looks good", "{\"verdict\":\"maybe\"}", "{broken}"} {
		if _, err := parseReviewVerdict(invalid); err == nil { t.Fatalf("expected invalid verdict for %q", invalid) }
	}
}

func TestReviewVerdictBoundsFindings(t *testing.T) {
	long := strings.Repeat("x", 3000)
	raw := "{\"verdict\":\"approve\",\"findings\":[\"" + long + "\"]}"
	got, err := parseReviewVerdict(raw)
	if err != nil { t.Fatal(err) }
	if len(got.Findings[0]) != 2000 { t.Fatalf("finding length=%d", len(got.Findings[0])) }
}
