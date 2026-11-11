package mariadb

import (
	"testing"

	"github.com/databasifylabs/engine-sdk/dtos"
)

func TestMariadbTuningFollowsTheNodeSize(t *testing.T) {
	values := tuned(dtos.Member{Cpus: 2, MemoryMb: 4096, DiskGb: 50})

	want := map[string]string{"innodb_buffer_pool_size": "2560M", "innodb_log_file_size": "640M", "max_connections": "256"}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("%s = %s, want %s", key, values[key], value)
		}
	}
}

func TestOnlyMariadbLimitsAreEnforced(t *testing.T) {
	if err := catalog().Check(map[string]string{"max_statement_time": "2.5", "innodb_read_io_threads": "64", "sql_mode": "EMPTY_STRING_IS_NULL"}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"max_connections": "9", "innodb_force_primary_key": "OFF", "innodb_redo_log_capacity": "1G", "binlog_expire_logs_seconds": "0"} {
		if catalog().Check(map[string]string{key: value}) == nil {
			t.Errorf("%s=%s was accepted", key, value)
		}
	}
}
