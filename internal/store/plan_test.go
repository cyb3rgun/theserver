package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

// queryPlan returns the lines of EXPLAIN QUERY PLAN for the query EachEvent
// runs for f.
func queryPlan(t *testing.T, db *sql.DB, f Filter) []string {
	t.Helper()
	query, args := walkQuery(f)
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

// A ranking reads hits and misses through EachEvent, of one session or of
// everything (internal/scoring, D-029). A session is found through
// events_session_kind with both columns; nothing is sorted. The ranking over
// everything scans, which measured faster than an index on kind.
func TestRankingQueryPlans(t *testing.T) {
	s := openTest(t)
	var version string
	if err := s.db.QueryRow("SELECT sqlite_version()").Scan(&version); err != nil {
		t.Fatal(err)
	}
	t.Logf("SQLite %s", version)

	cases := []struct {
		name   string
		filter Filter
		want   string
	}{
		{"session hits", Filter{SessionID: "s-1", Kind: "hit"}, "SEARCH events USING INDEX events_session_kind (session_id=? AND kind=?)"},
		{"session misses", Filter{SessionID: "s-1", Kind: "miss"}, "SEARCH events USING INDEX events_session_kind (session_id=? AND kind=?)"},
		{"all hits", Filter{Kind: "hit"}, "SCAN events"},
		{"all misses", Filter{Kind: "miss"}, "SCAN events"},
	}
	for _, c := range cases {
		plan := queryPlan(t, s.db, c.filter)
		t.Logf("%s: %s", c.name, strings.Join(plan, "; "))
		if len(plan) != 1 || plan[0] != c.want {
			t.Errorf("%s runs as %q, want %q", c.name, plan, c.want)
		}
	}
}

// ListEvents keeps the order of arrival; EachEvent does not sort.
func TestOnlyListEventsSorts(t *testing.T) {
	ordered, _ := pageQuery(Filter{Kind: "hit"}, 10, 0)
	unordered, _ := walkQuery(Filter{Kind: "hit"})
	if !strings.Contains(ordered, "ORDER BY ts_server, device_id, seq_epoch, seq") {
		t.Errorf("the ListEvents query does not sort: %s", ordered)
	}
	if strings.Contains(unordered, "ORDER BY") {
		t.Errorf("the EachEvent query sorts: %s", unordered)
	}
}
