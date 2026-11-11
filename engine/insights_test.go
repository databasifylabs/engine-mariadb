package mariadb

import (
	"context"
	"testing"

	"github.com/databasifylabs/engine-sdk/dtos"
)

func TestInsightsFromDigests(t *testing.T) {
	engine := Engine{db: fakeDB{rows: map[string][][]string{
		"events_statements_summary_by_digest": {{"SELECT `c` FROM `sbtest1` WHERE `id` = ?", "1200", "3600000000000", "3000000000"}},
	}}}

	insights, err := engine.Nodes().Insights(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if insights.Reason != nil {
		t.Fatalf("reason = %s", *insights.Reason)
	}
	want := dtos.Statement{Text: "SELECT `c` FROM `sbtest1` WHERE `id` = ?", Calls: 1200, TotalMs: 3600, MeanMs: 3}
	if len(insights.Statements) != 1 || insights.Statements[0] != want {
		t.Fatalf("got %+v", insights.Statements)
	}
}

func TestInsightsExplainMissingPreparedStatements(t *testing.T) {
	engine := Engine{db: fakeDB{rows: map[string][][]string{"statement/com/Execute": {{"273755"}}}}}

	insights, err := engine.Nodes().Insights(context.Background())

	if err != nil || insights.Reason == nil {
		t.Fatalf("reason = %v, err = %v", insights.Reason, err)
	}
}
