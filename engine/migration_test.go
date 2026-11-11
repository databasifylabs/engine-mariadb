package mariadb

import (
	"context"
	"slices"
	"strings"
	"testing"
)

func TestDumpPasswordInDefaultsFile(t *testing.T) {
	source := dumpSource{host: "10.0.0.9", port: 3306, user: "app", password: `p"w\x`, tls: true}

	cnf := sourceCnfOf(source)
	args := dumpArgsOf("/srv/mariadb/conf/source.cnf", []string{"shop"}, true)

	if !strings.Contains(cnf, `password="p\"w\\x"`) || !strings.Contains(cnf, "\nssl\n") {
		t.Fatalf("cnf = %s", cnf)
	}
	if args[0] != "--defaults-extra-file=/srv/mariadb/conf/source.cnf" || slices.ContainsFunc(args, func(arg string) bool { return strings.Contains(arg, "p\"w") }) {
		t.Fatalf("args = %v", args)
	}
	if !slices.Contains(args, "--gtid") || slices.Contains(dumpArgsOf("x", []string{"shop"}, false), "--gtid") {
		t.Fatal("only a MariaDB source is dumped with its GTID position")
	}
}

func TestContinuousSetsGtidSlavePos(t *testing.T) {
	sent := []string{}
	engine := Engine{db: fakeDB{rows: map[string][][]string{}, sent: &sent}}

	if err := engine.followSource(context.Background(), dumpSource{host: "10.0.0.9", port: 3306, user: "app", password: "pw", tls: true}, "0-1-500", []string{"shop", "crm"}); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"STOP SLAVE 'migration'",
		"SET GLOBAL gtid_slave_pos = ?",
		"CHANGE MASTER 'migration' TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_SSL = ?, MASTER_SSL_VERIFY_SERVER_CERT = 0, MASTER_USE_GTID = slave_pos",
		"SET GLOBAL migration.replicate_do_db = ?",
		"START SLAVE 'migration'",
	}
	if !slices.Equal(sent, want) {
		t.Fatalf("sent = %q", sent)
	}
}

func TestCheckWarnsForMysqlSource(t *testing.T) {
	canReplicate, warnings := readinessOf("8.4.13", "1", "ROW", "GRANT REPLICATION SLAVE ON *.*", "11.8.9-MariaDB")
	if canReplicate || len(warnings) == 0 || !strings.Contains(warnings[0], "MySQL") {
		t.Fatalf("can replicate = %v, warnings = %v", canReplicate, warnings)
	}

	canReplicate, warnings = readinessOf("11.4.13-MariaDB-log", "1", "ROW", "GRANT REPLICATION SLAVE ON *.*", "11.8.9-MariaDB")
	if !canReplicate || len(warnings) != 0 {
		t.Fatalf("can replicate = %v, warnings = %v", canReplicate, warnings)
	}

	canReplicate, _ = readinessOf("11.4.13-MariaDB", "0", "ROW", "GRANT ALL PRIVILEGES ON *.*", "11.8.9-MariaDB")
	if canReplicate {
		t.Fatal("a source without a binary log cannot be followed")
	}
}
