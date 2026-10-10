package repository

import (
	"strings"
	"testing"

	"github.com/wso2-open-operations/cs-tools/entity-service/internal/domain"
)

// escapeLikeWildcards is what stops a caller's own % or _ acting as a pattern.
// Without it a search for "%" matches every engineer while looking like it had
// filtered to one — the failure is invisible, which is why it is tested here
// rather than left to the query.
func TestEscapeLikeWildcards(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"ayesha", "ayesha"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{`back\slash`, `back\\slash`},
		{"%_%", `\%\_\%`},
		{"", ""},
	} {
		if got := escapeLikeWildcards(tc.in); got != tc.want {
			t.Errorf("escapeLikeWildcards(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The search has to reach the name, the address AND the two name parts: `name`
// is empty on some synced rows, and the display value falls back to
// first + last, so matching `name` alone would make those engineers
// unsearchable while still listing them.
func TestSearchConditionCoversNameEmailAndTheNameParts(t *testing.T) {
	b := &argBuilder{}
	conds := userConditions(&domain.UserSearchFilters{Search: "ayesha"}, b)

	var clause string
	for _, c := range conds {
		if strings.Contains(c, "ILIKE") {
			clause = c
		}
	}
	if clause == "" {
		t.Fatal("no ILIKE condition was produced for a non-empty search")
	}
	for _, col := range []string{"u.name", "u.email", "u.first_name", "u.last_name"} {
		if !strings.Contains(clause, col) {
			t.Errorf("search does not cover %s: %s", col, clause)
		}
	}
	if !strings.Contains(clause, `ESCAPE '\'`) {
		t.Errorf("the ESCAPE clause is missing, so the escaping above does nothing: %s", clause)
	}
	// One bound argument, reused — not five copies of the term.
	if n := len(b.list()); n != 1 {
		t.Errorf("bound %d arguments for one search term, want 1", n)
	}
	if got := b.list()[0]; got != "%ayesha%" {
		t.Errorf("bound %q, want %q", got, "%ayesha%")
	}
}

// An absent or blank search must add no condition at all — that is the default
// the owner picker opens with, and it has to stay the plain first page.
func TestBlankSearchAddsNoCondition(t *testing.T) {
	for _, term := range []string{"", "   ", "\t"} {
		b := &argBuilder{}
		conds := userConditions(&domain.UserSearchFilters{Search: term}, b)
		for _, c := range conds {
			if strings.Contains(c, "ILIKE") {
				t.Errorf("search %q produced a filter: %s", term, c)
			}
		}
		if len(b.list()) != 0 {
			t.Errorf("search %q bound %d arguments, want 0", term, len(b.list()))
		}
	}
}
