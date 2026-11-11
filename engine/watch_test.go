package mariadb

import (
	"context"
	"slices"
	"testing"

	"github.com/databasifylabs/engine-sdk/dtos"
)

func watchedNode(t *testing.T, writer string, incoming string, readOnly string) (Engine, *[]string) {
	t.Helper()
	sent := []string{}
	rows := map[string][][]string{
		"@@wsrep_on":               {{"1", readOnly, "Primary", "Synced", "0"}},
		"WSREP_INCOMING_ADDRESSES": {{incoming}},
	}
	if writer != "" {
		rows["databasify.writer"] = [][]string{{writer}}
	}

	return Engine{db: fakeDB{rows: rows, sent: &sent}}, &sent
}

func TestWatchFollowsTheRecordedWriter(t *testing.T) {
	engine, sent := watchedNode(t, "10.0.0.3", "10.0.0.2:25060,10.0.0.3:25060", "0")

	engine.keepWriterRole(context.Background(), galeraRequest())

	if !slices.Equal(*sent, []string{"SET GLOBAL read_only = ON"}) {
		t.Fatalf("sent = %q", *sent)
	}
}

func TestWatchMakesTheRecordedWriterWritable(t *testing.T) {
	engine, sent := watchedNode(t, "10.0.0.2", "10.0.0.2:25060,10.0.0.3:25060", "1")

	engine.keepWriterRole(context.Background(), galeraRequest())

	if !slices.Equal(*sent, []string{"SET GLOBAL read_only = OFF"}) {
		t.Fatalf("sent = %q", *sent)
	}
}

func TestWatchElectsWhenTheWriterLeft(t *testing.T) {
	engine, sent := watchedNode(t, "10.0.0.1", "10.0.0.10:25060,10.0.0.2:25060", "1")

	engine.keepWriterRole(context.Background(), galeraRequest())

	if !slices.Equal(*sent, []string{"REPLACE INTO databasify.writer VALUES (1, ?)", "SET GLOBAL read_only = OFF"}) {
		t.Fatalf("sent = %q", *sent)
	}
}

func TestWatchLeavesTheElectionToTheLowestMember(t *testing.T) {
	engine, sent := watchedNode(t, "", "10.0.0.2:25060,10.0.0.1:25060", "1")

	engine.keepWriterRole(context.Background(), galeraRequest())

	if len(*sent) != 0 {
		t.Fatalf("sent = %q", *sent)
	}
}

func TestReplicaMovesToTheNextSyncedMember(t *testing.T) {
	members := []dtos.Member{{Ip: "10.0.0.1", Voting: true}, {Ip: "10.0.0.2", Voting: true}, {Ip: "10.0.0.3", Voting: true}, {Ip: "10.0.0.4", Voting: false}}
	synced := func(ip string) bool { return ip == "10.0.0.3" }

	if got := nextSource(members, "10.0.0.1", synced); got != "10.0.0.3" {
		t.Fatalf("next = %s", got)
	}
	if got := nextSource(members, "10.0.0.3", func(string) bool { return false }); got != "" {
		t.Fatalf("next = %s", got)
	}
}

func TestWatchKeepsANodeWithoutARecordedWriterReadOnly(t *testing.T) {
	engine, sent := watchedNode(t, "", "10.0.0.2:25060,10.0.0.3:25060", "0")

	engine.keepWriterRole(context.Background(), galeraRequest())

	if !slices.Equal(*sent, []string{"SET GLOBAL read_only = ON"}) {
		t.Fatalf("sent = %q", *sent)
	}
}
