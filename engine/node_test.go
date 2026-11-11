package mariadb

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/databasifylabs/engine-sdk/ctl"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/nodefs"
	"github.com/databasifylabs/engine-sdk/requests"
)

type fakeSupervisor struct{ calls *[]string }

func (f fakeSupervisor) Do(verb string, units ...string) error {
	*f.calls = append(*f.calls, verb+" "+strings.Join(units, " "))

	return nil
}

func (f fakeSupervisor) Run(transient ctl.Transient) error {
	return f.Do("run "+transient.Unit, transient.Argv...)
}

func (f fakeSupervisor) DoNoBlock(verb string, units ...string) error { return f.Do(verb, units...) }

func (f fakeSupervisor) Kill(signal string, units ...string) error {
	return f.Do("kill "+signal, units...)
}

func (fakeSupervisor) Active(string) bool { return true }

func (fakeSupervisor) Property(string, string) string { return "0" }

type sequenceDB struct {
	fakeDB
	states *[][]string
}

func (s sequenceDB) Rows(ctx context.Context, query string, args ...any) ([][]string, error) {
	if !strings.Contains(query, "@@wsrep_on") {
		return s.fakeDB.Rows(ctx, query, args...)
	}
	state := (*s.states)[0]
	if len(*s.states) > 1 {
		*s.states = (*s.states)[1:]
	}

	return [][]string{state}, nil
}

func (s sequenceDB) Value(ctx context.Context, query string, args ...any) (string, bool, error) {
	rows, err := s.Rows(ctx, query, args...)
	if err != nil || len(rows) == 0 {
		return "", false, err
	}

	return rows[0][0], true, nil
}

func secret(value string) *string { return &value }

func engineIn(t *testing.T, states *[][]string) Engine {
	t.Helper()
	calls := []string{}
	engine := Engine{db: sequenceDB{fakeDB: fakeDB{rows: map[string][][]string{"SELECT 1": {{"1"}}}}, states: states}, supervisor: fakeSupervisor{&calls}, root: nodefs.Root(t.TempDir())}
	for _, dir := range []string{"conf", "state"} {
		if err := engine.root.Dir(dir); err != nil {
			t.Fatal(err)
		}
	}

	return engine
}

func galeraRequest() requests.NodeConfigure {
	seed := "10.0.0.1"

	return requests.NodeConfigure{
		Clustered: true, Ip: "10.0.0.2", DatabaseId: "db1", Primary: &seed,
		Members:  []dtos.Member{{Ip: "10.0.0.1", Position: 1, Voting: true}, {Ip: "10.0.0.2", Position: 2, Voting: true}, {Ip: "10.0.0.3", Position: 3, Voting: true}},
		Settings: map[string]string{"max_connections": "300"},
		Secrets:  dtos.Secrets{Admin: secret("root-pw"), Replication: secret("sst-pw")},
	}
}

func TestMyCnfForAGaleraNode(t *testing.T) {
	conf := myCnfOf("/srv/mariadb", "11.8", galeraRequest())

	for _, line := range []string{
		"[mysqld]\n", "port=25060\n", "server_id=" + serverIDOf(galeraRequest()) + "\n", "gtid_domain_id=12\n",
		"log_bin=binlog\n", "log_slave_updates=ON\n", "binlog_format=ROW\n",
		"wsrep_on=ON\n", "wsrep_provider=/usr/lib/galera/libgalera_smm.so\n", "wsrep_cluster_name=db1\n",
		"wsrep_cluster_address=gcomm://10.0.0.1,10.0.0.2,10.0.0.3\n", "wsrep_node_address=10.0.0.2\n",
		"wsrep_sst_method=mariabackup\n", "wsrep_sst_auth=databasify_sst:sst-pw\n", "wsrep_gtid_mode=ON\n", "wsrep_gtid_domain_id=1\n",
		"socket.ssl_ca=/srv/mariadb/tls/ca.crt", "innodb_autoinc_lock_mode=2\n", "innodb_force_primary_key=ON\n", "wsrep_node_incoming_address=10.0.0.2:25060\n", "max_connections=300\n", "[sst]\nssl-mode=VERIFY_CA\n",
	} {
		if !strings.Contains(conf, line) {
			t.Errorf("my.cnf lacks %q:\n%s", line, conf)
		}
	}
	other := galeraRequest()
	other.Ip = "10.0.0.3"
	if serverIDOf(other) != serverIDOf(galeraRequest()) {
		t.Fatal("every Galera node needs the same server_id")
	}
	writer := galeraRequest()
	writer.Primary = &writer.Ip
	if !strings.Contains(conf, "read_only=ON\n") || !strings.Contains(myCnfOf("/srv/mariadb", "11.8", writer), "read_only=ON\n") {
		t.Fatal("every Galera node restarts read-only until the watcher confirms the writer")
	}
}

func TestReadOnlyNodeStaysOutOfGalera(t *testing.T) {
	request := galeraRequest()
	request.ReadOnly = true
	conf := myCnfOf("/srv/mariadb", "11.8", request)

	if strings.Contains(conf, "wsrep_on=ON") || !strings.Contains(conf, "read_only=ON\n") {
		t.Fatalf("my.cnf:\n%s", conf)
	}
	if serverIDOf(request) == serverIDOf(galeraRequest()) {
		t.Fatal("a read-only node needs its own server_id")
	}
}

func TestSeedStartsWithNewClusterOnce(t *testing.T) {
	states := [][]string{{"1", "0", "Primary", "Synced", "0"}}
	engine := engineIn(t, &states)
	request := galeraRequest()
	request.Primary = &request.Ip
	if _, err := engine.writeFiles(request); err != nil {
		t.Fatal(err)
	}
	if err := engine.markSeed(request); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(engine.root.Read(manifestFile), "--wsrep-new-cluster") {
		t.Fatal("the seed must start with --wsrep-new-cluster")
	}

	if err := engine.Nodes().Ready(context.Background(), requests.NodeReady{Key: "r"}, func(string) {}); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(engine.root.Read(manifestFile), "--wsrep-new-cluster") || engine.root.Exists(newClusterState) {
		t.Fatal("--wsrep-new-cluster must be used for one start only")
	}
}

func TestReadyWaitsForSynced(t *testing.T) {
	states := [][]string{{"1", "0", "Primary", "Joined", "0"}, {"1", "0", "Primary", "Synced", "0"}}
	engine := engineIn(t, &states)
	if _, err := engine.writeFiles(galeraRequest()); err != nil {
		t.Fatal(err)
	}
	phases := []string{}

	if err := engine.Nodes().Ready(context.Background(), requests.NodeReady{Key: "r"}, func(phase string) { phases = append(phases, phase) }); err != nil {
		t.Fatal(err)
	}

	if len(states) != 1 || strings.Join(phases, ",") != "waiting for MariaDB,syncing" {
		t.Fatalf("states left = %v, phases = %v", states, phases)
	}
}

func TestTailCaptureKeepsTheDeferredGtid(t *testing.T) {
	var tail tailCapture
	_, _ = tail.Write([]byte(strings.Repeat("INSERT INTO t VALUES (1);\n", 5000)))
	_, _ = tail.Write([]byte("-- SET GLOBAL gtid_slave_pos='1-1862348793-21';\n"))

	if match := dumpedGtid.FindStringSubmatch(string(tail.kept)); match == nil || match[1] != "1-1862348793-21" || len(tail.kept) > tailLimit {
		t.Fatalf("kept %d bytes, match = %v", len(tail.kept), match)
	}
}

func TestClusterNameFitsTheGaleraGroupNameLimit(t *testing.T) {
	uuid := clusterNameOf("01a1c505-2ee1-70d0-8d43-c71167c3a538")
	long := clusterNameOf(strings.Repeat("database", 8))

	if uuid != "01a1c5052ee170d08d43c71167c3a538" || len(long) != 32 || clusterNameOf("db1") != "db1" {
		t.Fatalf("uuid = %q, long = %q", uuid, long)
	}
}

func TestAGaleraNodeCreatesTheSstUserWhenItInitializes(t *testing.T) {
	request := galeraRequest()
	request.Secrets.Replication = secret("sst'pw")
	standalone := galeraRequest()
	standalone.Clustered = false

	statements := initStatementsOf("root", request)

	if !strings.Contains(statements, "CREATE USER 'databasify_sst'@'localhost' IDENTIFIED BY 'sst''pw';\nGRANT RELOAD, PROCESS, LOCK TABLES, BINLOG MONITOR ON *.* TO 'databasify_sst'@'localhost';\n") || strings.Contains(initStatementsOf("root", standalone), "databasify_sst") {
		t.Fatalf("statements = %q", statements)
	}
}

func TestReadyMakesAJoinerThatIsNotTheWriterReadOnlyOnceSynced(t *testing.T) {
	states := [][]string{{"1", "0", "Primary", "Synced", "0"}}
	sent := []string{}
	calls := []string{}
	rows := map[string][][]string{"SELECT 1": {{"1"}}, "databasify.writer": {{"10.0.0.1"}}, "WSREP_INCOMING_ADDRESSES": {{"10.0.0.1:25060,10.0.0.2:25060"}}}
	engine := Engine{db: sequenceDB{fakeDB: fakeDB{rows: rows, sent: &sent}, states: &states}, supervisor: fakeSupervisor{&calls}, root: nodefs.Root(t.TempDir())}
	for _, dir := range []string{"conf", "state"} {
		if err := engine.root.Dir(dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := engine.writeFiles(galeraRequest()); err != nil {
		t.Fatal(err)
	}

	if err := engine.Nodes().Ready(context.Background(), requests.NodeReady{Key: "r"}, func(string) {}); err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(sent, "SET GLOBAL read_only = ON") {
		t.Fatalf("sent = %q", sent)
	}
}
