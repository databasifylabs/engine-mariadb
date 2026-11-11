package mariadb

import (
	"context"
	"strings"
	"testing"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/enums"
)

type fakeDB struct {
	rows  map[string][][]string
	named map[string][]map[string]string
	err   error
	sent  *[]string
}

func (f fakeDB) Exec(_ context.Context, query string, _ ...any) error {
	if f.sent != nil {
		*f.sent = append(*f.sent, query)
	}

	return f.err
}

func (f fakeDB) Rows(_ context.Context, query string, _ ...any) ([][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	for fragment, rows := range f.rows {
		if strings.Contains(query, fragment) {
			return rows, nil
		}
	}

	return [][]string{}, nil
}

func (f fakeDB) Named(_ context.Context, query string, _ ...any) ([]map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}

	return f.named[query], nil
}

func (f fakeDB) Value(ctx context.Context, query string, args ...any) (string, bool, error) {
	rows, err := f.Rows(ctx, query, args...)
	if err != nil || len(rows) == 0 {
		return "", false, err
	}

	return rows[0][0], true, nil
}

func roleWith(t *testing.T, state []string) enums.NodeRole {
	t.Helper()
	db := fakeDB{rows: map[string][][]string{"@@version": {{"11.8.9-MariaDB-ubu2404-log"}}, "@@wsrep_on": {state}}}
	status, err := Engine{db: db}.Nodes().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Version == nil || *status.Version != "11.8.9" {
		t.Fatalf("version = %v", status.Version)
	}

	return status.Role
}

func TestStatusOfASyncedWriter(t *testing.T) {
	if role := roleWith(t, []string{"1", "0", "Primary", "Synced", "0"}); role != enums.NodeRolePrimary {
		t.Fatalf("role = %s", role)
	}
}

func TestStatusOfAReadOnlyGaleraNode(t *testing.T) {
	if role := roleWith(t, []string{"1", "1", "Primary", "Synced", "0"}); role != enums.NodeRoleReplica {
		t.Fatalf("role = %s", role)
	}
	if role := roleWith(t, []string{"1", "0", "Primary", "Joined", "0"}); role != enums.NodeRoleUnknown {
		t.Fatalf("joining role = %s", role)
	}
	if role := roleWith(t, []string{"1", "0", "non-Primary", "Synced", "0"}); role != enums.NodeRoleUnknown {
		t.Fatalf("non-primary role = %s", role)
	}
}

func TestStatusOutsideGalera(t *testing.T) {
	if role := roleWith(t, []string{"0", "0", "", "", "0"}); role != enums.NodeRolePrimary {
		t.Fatalf("standalone role = %s", role)
	}
	if role := roleWith(t, []string{"0", "1", "", "", "1"}); role != enums.NodeRoleReplica {
		t.Fatalf("replica role = %s", role)
	}
	if role := roleWith(t, []string{"0", "1", "", "", "0"}); role != enums.NodeRoleUnknown {
		t.Fatalf("stopped replica role = %s", role)
	}
}

func TestStatusOfflineOnRefused(t *testing.T) {
	refused := family.SocketDB{Socket: t.TempDir() + "/missing.sock"}
	status, err := Engine{db: refused}.Nodes().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != enums.NodeRoleOffline {
		t.Fatalf("role = %s", status.Role)
	}
}
