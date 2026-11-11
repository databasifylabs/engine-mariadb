package mariadb

import (
	"context"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/responses"
)

const (
	preparedExecutesQuery = `SELECT COUNT_STAR FROM performance_schema.events_statements_summary_global_by_event_name WHERE EVENT_NAME = 'statement/com/Execute'`
	preparedNotDigested   = "MariaDB does not record statements run as server-side prepared statements, so those are missing here"
)

func (e nodes) Insights(ctx context.Context) (responses.QueryInsights, error) {
	insights, err := family.Insights(ctx, e.db, e.Capabilities().ReservedDatabases)
	if err != nil {
		return insights, err
	}
	if executes, _, _ := e.db.Value(ctx, preparedExecutesQuery); executes != "" && executes != "0" {
		reason := preparedNotDigested
		insights.Reason = &reason
	}

	return insights, nil
}
