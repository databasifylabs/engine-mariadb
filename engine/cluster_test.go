package mariadb

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/command"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/enums"
	"github.com/databasifylabs/engine-sdk/requests"
)

const recoverLog = `2026-11-09 10:00:00 0 [Note] Starting MariaDB 11.8.9-MariaDB-ubu2404-log source revision as process 1
2026-11-09 10:00:01 0 [Note] WSREP: Recovered position: 4c7d0e4a-bd9c-11f1-8a2b-6a1046119477:1342
`

func savedEngine(t *testing.T, request requests.NodeConfigure) Engine {
	t.Helper()
	states := [][]string{{"1", "0", "Primary", "Synced", "0"}}
	engine := engineIn(t, &states)
	state, _ := json.Marshal(request)
	if _, err := engine.root.Write(family.NodeState, string(state), 0o600, false); err != nil {
		t.Fatal(err)
	}

	return engine
}

func TestPositionFallsBackToWsrepRecover(t *testing.T) {
	engine := savedEngine(t, galeraRequest())
	engine.supervisor = stoppedSupervisor{fakeSupervisor{&[]string{}}}
	if err := engine.root.Dir("data"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.root.Write("data/grastate.dat", "# GALERA saved state\nversion: 2.1\nuuid:    4c7d0e4a-bd9c-11f1-8a2b-6a1046119477\nseqno:   -1\nsafe_to_bootstrap: 0\n", 0o600, false); err != nil {
		t.Fatal(err)
	}
	ran := []string{}
	engine.run = func(_ context.Context, name string, args ...string) (string, error) {
		ran = append(ran, name)
		ran = append(ran, args...)

		return recoverLog, nil
	}

	position, err := engine.Cluster().Position(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if position.Position == nil || *position.Position != 1342 || !slices.Contains(ran, "--wsrep-recover") {
		t.Fatalf("position = %v, ran = %v", position.Position, ran)
	}
}

func TestPositionReadsGrastateWhenSaved(t *testing.T) {
	engine := savedEngine(t, galeraRequest())
	engine.supervisor = stoppedSupervisor{fakeSupervisor{&[]string{}}}
	if err := engine.root.Dir("data"); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.root.Write("data/grastate.dat", "version: 2.1\nuuid:    4c7d0e4a-bd9c-11f1-8a2b-6a1046119477\nseqno:   977\nsafe_to_bootstrap: 1\n", 0o600, false); err != nil {
		t.Fatal(err)
	}

	position, err := engine.Cluster().Position(context.Background())
	if err != nil || position.Position == nil || *position.Position != 977 {
		t.Fatalf("position = %v, err = %v", position.Position, err)
	}
}

func TestForceQuorumRefusesInAPrimaryComponent(t *testing.T) {
	engine := savedEngine(t, galeraRequest())

	_, err := engine.Cluster().ForceQuorum(context.Background(), requests.ClusterForceQuorum{OthersDown: true})

	var failure command.Failure
	if !errors.As(err, &failure) || failure.Code != enums.ErrorCodeBusy {
		t.Fatalf("err = %v", err)
	}
}

func TestFailoverSetsReadOnlyOnTheOthers(t *testing.T) {
	engine := savedEngine(t, galeraRequest())
	sent := map[string][]string{}
	engine.connect = func(host string, _ string) family.DB {
		recorded := []string{}
		sent[host] = recorded

		return recordingDB{host: host, sent: sent}
	}

	if err := engine.Cluster().Failover(context.Background(), requests.ClusterFailover{Target: "10.0.0.3"}); err != nil {
		t.Fatal(err)
	}

	if len(sent["10.0.0.3"]) < 3 || sent["10.0.0.3"][2] != "REPLACE INTO databasify.writer VALUES (1, ?)" {
		t.Fatalf("the writer must be recorded first, got %v", sent["10.0.0.3"])
	}
	for host, want := range map[string]string{"10.0.0.1": "SET GLOBAL read_only = ON", "10.0.0.2": "SET GLOBAL read_only = ON", "10.0.0.3": "SET GLOBAL read_only = OFF"} {
		if !slices.Contains(sent[host], want) {
			t.Fatalf("%s got %v", host, sent[host])
		}
	}
}

func TestFailoverAcceptsTheTextReadOnlyOfNewerReleases(t *testing.T) {
	engine := savedEngine(t, galeraRequest())
	sent := map[string][]string{}
	engine.connect = func(host string, _ string) family.DB {
		return recordingDB{host: host, sent: sent, textReadOnly: true}
	}

	if err := engine.Cluster().Failover(context.Background(), requests.ClusterFailover{Target: "10.0.0.3"}); err != nil {
		t.Fatal(err)
	}
}

type stoppedSupervisor struct{ fakeSupervisor }

func (stoppedSupervisor) Active(string) bool { return false }

type recordingDB struct {
	host         string
	sent         map[string][]string
	textReadOnly bool
}

func (r recordingDB) Exec(_ context.Context, query string, _ ...any) error {
	r.sent[r.host] = append(r.sent[r.host], query)

	return nil
}

func (r recordingDB) Rows(context.Context, string, ...any) ([][]string, error) {
	return [][]string{{"0"}}, nil
}

func (r recordingDB) Value(context.Context, string, ...any) (string, bool, error) {
	return r.readOnly(), true, nil
}

func (r recordingDB) readOnly() string {
	if r.textReadOnly {
		return "OFF"
	}

	return "0"
}

func TestBootstrapOnlyRunsOnTheSeed(t *testing.T) {
	engine := savedEngine(t, galeraRequest())
	sent := []string{}
	engine.db = fakeDB{rows: map[string][][]string{}, sent: &sent}

	err := engine.Nodes().Bootstrap(context.Background(), requests.NodeBootstrap{Secrets: dtos.Secrets{Replication: secret("r"), Agent: secret("a")}})

	if err != nil || len(sent) != 0 {
		t.Fatalf("err = %v, sent = %v", err, sent)
	}
}

func TestEnableOnASyncedNodeDoesNotRestartIt(t *testing.T) {
	seed := galeraRequest()
	seed.Primary = &seed.Ip
	engine := savedEngine(t, seed)
	calls := []string{}
	engine.supervisor = fakeSupervisor{&calls}

	if err := engine.Cluster().Enable(context.Background(), requests.ClusterEnable{Key: "e"}, func(string) {}); err != nil {
		t.Fatal(err)
	}

	if slices.ContainsFunc(calls, func(call string) bool { return strings.HasPrefix(call, "restart") }) || engine.root.Exists(newClusterState) {
		t.Fatalf("a synced node was restarted: %v", calls)
	}
}
