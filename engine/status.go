package mariadb

import (
	"context"
	"strings"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/enums"
	"github.com/databasifylabs/engine-sdk/responses"
)

const nodeStateQuery = `SELECT @@wsrep_on, @@read_only,
	COALESCE((SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_CLUSTER_STATUS'), ''),
	COALESCE((SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_LOCAL_STATE_COMMENT'), ''),
	COALESCE((SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'SLAVES_RUNNING'), '0')`

type nodeState struct {
	galera, readOnly          bool
	clusterStatus, localState string
	replicating               bool
}

func (e nodes) Status(ctx context.Context) (responses.NodeStatus, error) {
	version, _, err := e.db.Value(ctx, "SELECT @@version")
	if family.IsUnavailable(err) {
		return responses.NodeStatus{Role: enums.NodeRoleOffline}, nil
	}
	if err != nil {
		return responses.NodeStatus{}, err
	}
	state, err := e.state(ctx)
	if err != nil {
		return responses.NodeStatus{}, err
	}
	release, _, _ := strings.Cut(version, "-")

	return responses.NodeStatus{Role: roleOf(state), Version: &release}, nil
}

func (e Engine) state(ctx context.Context) (nodeState, error) {
	rows, err := e.db.Rows(ctx, nodeStateQuery)
	if err != nil || len(rows) == 0 {
		return nodeState{}, err
	}
	row := rows[0]

	return nodeState{galera: row[0] == "1", readOnly: isReadOnly(row[1]), clusterStatus: row[2], localState: row[3], replicating: row[4] != "0"}, nil
}

func isReadOnly(value string) bool {
	return value != "0" && !strings.EqualFold(value, "OFF")
}

func (s nodeState) synced() bool {
	return s.clusterStatus == "Primary" && s.localState == "Synced"
}

func roleOf(state nodeState) enums.NodeRole {
	if state.galera && !state.synced() {
		return enums.NodeRoleUnknown
	}
	if !state.readOnly {
		return enums.NodeRolePrimary
	}
	if state.galera || state.replicating {
		return enums.NodeRoleReplica
	}

	return enums.NodeRoleUnknown
}

func (e nodes) Metrics(ctx context.Context) (responses.NodeMetrics, error) {
	return family.Metrics(ctx, e.db)
}
