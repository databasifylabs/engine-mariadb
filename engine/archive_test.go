package mariadb

import (
	"slices"
	"strings"
	"testing"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
)

func TestBackupStreamsMbstreamIntoRclone(t *testing.T) {
	args := backupArgsOf("/srv/mariadb", true)

	for _, want := range []string{"--defaults-file=/srv/mariadb/conf/agent.cnf", "--backup", "--stream=mbstream", "--galera-info", "--extra-lsndir=/srv/mariadb/backup/meta"} {
		if !slices.Contains(args, want) {
			t.Fatalf("args = %v lack %s", args, want)
		}
	}
	if slices.Contains(backupArgsOf("/srv/mariadb", false), "--galera-info") {
		t.Fatal("a standalone backup has no Galera state")
	}
	if baseSuffix != ".mbstream" {
		t.Fatalf("suffix = %s", baseSuffix)
	}
}

func TestRestoreReadsTheBackupGtid(t *testing.T) {
	gtid, err := family.BackupGtids("tool_name = mariadb-backup\nbinlog_pos = filename 'binlog.000003', position '379', GTID of the last change '1-1862348793-43'\ninnodb_from_lsn = 0\n")

	if err != nil || gtid != "1-1862348793-43" {
		t.Fatalf("gtid = %q, err = %v", gtid, err)
	}
}

func TestReplayStartsAfterTheBackupGtid(t *testing.T) {
	target, _ := time.Parse(time.RFC3339, "2026-11-09T11:30:00Z")

	args := replayArgsOf("1-1-99,11-5-3", target, []string{"binlog.000001", "binlog.000002"})

	if strings.Join(args, " ") != "--start-position=1-1-99,11-5-3 --stop-datetime=2026-11-09 11:30:00 binlog.000001 binlog.000002" {
		t.Fatalf("args = %v", args)
	}
}

func TestLaterPositionsWinPerDomain(t *testing.T) {
	if got := laterPositions("1-1-99,11-5-3", "1-1-120"); got != "1-1-120,11-5-3" {
		t.Fatalf("merged = %s", got)
	}
	if got := laterPositions("1-1-99", ""); got != "1-1-99" {
		t.Fatalf("merged = %s", got)
	}
}
