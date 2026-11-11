package mariadb

import (
	"strconv"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/settings"
)

const (
	maxUnsigned      = 18446744073709551615
	maxUnsigned32    = 4294967295
	yearSeconds      = 31536000
	daySeconds       = 86400
	stopwordPattern  = `([A-Za-z0-9_$]{1,64}/[A-Za-z0-9_$]{1,64})?`
	timeZonePattern  = `SYSTEM|[+-](0[0-9]|1[0-3]):[0-5][0-9]|[+-]14:00`
	collationPattern = `(utf8mb4|utf8mb3|latin1|ascii)_[a-z0-9_]{1,40}|binary`
	switchPattern    = `([a-z_]{1,40}=(on|off|default)(,[a-z_]{1,40}=(on|off|default))*)?`
)

var (
	onOff    = []string{"ON", "OFF"}
	sqlModes = []string{"REAL_AS_FLOAT", "PIPES_AS_CONCAT", "ANSI_QUOTES", "IGNORE_SPACE", "IGNORE_BAD_TABLE_OPTIONS", "ONLY_FULL_GROUP_BY", "NO_UNSIGNED_SUBTRACTION", "NO_DIR_IN_CREATE", "NO_AUTO_VALUE_ON_ZERO", "NO_BACKSLASH_ESCAPES", "STRICT_TRANS_TABLES", "STRICT_ALL_TABLES", "NO_ZERO_IN_DATE", "NO_ZERO_DATE", "ALLOW_INVALID_DATES", "ERROR_FOR_DIVISION_BY_ZERO", "TRADITIONAL", "NO_AUTO_CREATE_USER", "HIGH_NOT_PRECEDENCE", "NO_ENGINE_SUBSTITUTION", "PAD_CHAR_TO_FULL_LENGTH", "EMPTY_STRING_IS_NULL", "SIMULTANEOUS_ASSIGNMENT", "TIME_ROUND_FRACTIONAL", "ANSI"}
)

var timeVariables = map[string]string{"default_time_zone": "time_zone"}

func catalog() settings.Catalog {
	return settings.Catalog{Tune: tuned, Fields: settings.Fields(
		settings.Integer("max_connections", "Max connections", "Most client connections accepted at once.", 10, 100000, "151").TunedFromServer(),
		settings.Bytes("innodb_buffer_pool_size", "Buffer pool size", "Memory InnoDB uses to cache tables and indexes, such as 4096M or 8G.", 2097152, 9223372036854775807, "128M").TunedFromServer(),
		settings.Bytes("innodb_log_buffer_size", "Log buffer size", "Memory that buffers redo log writes before they reach disk.", 2097152, 2147479552, "16M").Restarting().TunedFromServer(),
		settings.Bytes("innodb_log_file_size", "Redo log size", "Size of the InnoDB redo log, such as 1G.", 4194304, maxUnsigned, "96M").TunedFromServer(),
		settings.Integer("innodb_read_io_threads", "Read I/O threads", "Background threads that read pages from disk.", 1, 64, "4").TunedFromServer(),
		settings.Integer("innodb_write_io_threads", "Write I/O threads", "Background threads that write pages to disk.", 2, 64, "4").TunedFromServer(),
		settings.Integer("innodb_purge_threads", "Purge threads", "Background threads that purge old row versions.", 1, 32, "4").TunedFromServer(),
		settings.Integer("innodb_io_capacity", "I/O capacity", "I/O operations per second InnoDB background tasks may use.", 100, maxUnsigned32, "200").TunedFromServer(),
		settings.Integer("innodb_io_capacity_max", "Max I/O capacity", "Most I/O operations per second InnoDB may use when it falls behind.", 100, maxUnsigned32, "4294967295").TunedFromServer(),
		settings.Integer("back_log", "Connection backlog", "Pending connection requests queued while the server is busy.", 0, 65535, "150").Restarting().TunedFromServer(),
		settings.Integer("table_open_cache", "Open table cache", "Open tables cached for all threads.", 10, 1048576, "2000").TunedFromServer(),
		settings.Integer("innodb_open_files", "InnoDB open files", "Most .ibd files InnoDB keeps open at once.", 0, 9223372036854775807, "0").Restarting().TunedFromServer(),
		settings.Integer("table_open_cache_instances", "Open table cache instances", "Partitions of the open table cache.", 1, 64, "8").Restarting().TunedFromServer(),
		settings.Integer("thread_cache_size", "Thread cache", "Threads kept for reuse after a connection closes.", 0, 16384, "256").TunedFromServer(),
		settings.Integer("open_files_limit", "Open files limit", "File descriptors the server may use.", 0, maxUnsigned32, "65536").Restarting().TunedFromServer(),
		settings.Choice("automatic_sp_privileges", "Automatic routine privileges", "Grant EXECUTE and ALTER ROUTINE to the creator of a stored routine.", onOff, "ON"),
		settings.Integer("connect_timeout", "Connect timeout", "Seconds the server waits for a connect packet before rejecting the handshake.", 2, yearSeconds, "10").In("s"),
		settings.Text("default_time_zone", "Time zone", "UTC offset such as +06:00, or SYSTEM.", timeZonePattern, "SYSTEM").Restarting(),
		settings.Integer("div_precision_increment", "Division precision", "Digits added to the scale of results of the / operator.", 0, 38, "4"),
		settings.Integer("eq_range_index_dive_limit", "Index dive limit", "Equality ranges above this count use index statistics instead of index dives.", 0, maxUnsigned32, "200"),
		settings.Integer("group_concat_max_len", "GROUP_CONCAT length", "Longest result GROUP_CONCAT returns.", 4, 1073741824, "1048576").In("B"),
		settings.Choice("innodb_ft_enable_stopword", "Full-text stopwords", "Use a stopword list when building FULLTEXT indexes.", onOff, "ON"),
		settings.Integer("innodb_ft_max_token_size", "Full-text max word length", "Longest word stored in an InnoDB FULLTEXT index.", 10, 84, "84").Restarting(),
		settings.Integer("innodb_ft_min_token_size", "Full-text min word length", "Shortest word stored in an InnoDB FULLTEXT index.", 1, 16, "3").Restarting(),
		settings.Integer("innodb_ft_num_word_optimize", "Full-text optimize batch", "Words processed by each OPTIMIZE TABLE pass on a FULLTEXT index.", 1000, 10000, "2000"),
		settings.Integer("innodb_ft_result_cache_limit", "Full-text result cache", "Memory one FULLTEXT query may use for its results.", 1000000, maxUnsigned, "2000000000").In("B"),
		settings.Text("innodb_ft_server_stopword_table", "Full-text server stopwords", "Stopword table for all InnoDB tables, as database/table.", stopwordPattern, ""),
		settings.Text("innodb_ft_user_stopword_table", "Full-text user stopwords", "Stopword table for new FULLTEXT indexes, as database/table.", stopwordPattern, ""),
		settings.Integer("innodb_lock_wait_timeout", "Lock wait timeout", "Seconds a transaction waits for a row lock before giving up.", 0, 100000000, "50").In("s"),
		settings.Choice("innodb_optimize_fulltext_only", "Optimize full-text only", "Make OPTIMIZE TABLE only process FULLTEXT index data.", onOff, "OFF"),
		settings.Choice("innodb_print_all_deadlocks", "Log all deadlocks", "Write every deadlock to the error log.", onOff, "OFF"),
		settings.Choice("innodb_rollback_on_timeout", "Roll back on lock timeout", "Roll back the whole transaction instead of the last statement when a lock wait times out.", onOff, "OFF").Restarting(),
		settings.Integer("interactive_timeout", "Interactive idle timeout", "Seconds before an idle interactive session is closed.", 1, yearSeconds, "28800").In("s"),
		settings.Choice("slow_query_log", "Slow query log", "Record queries that take longer than the slow query time.", onOff, "OFF"),
		settings.Decimal("long_query_time", "Slow query time", "Seconds a query must take to be logged as slow.", 0, yearSeconds, "10").In("s"),
		settings.Bytes("max_allowed_packet", "Max packet size", "Largest single query or row.", 1024, 1073741824, "16M"),
		settings.Bytes("max_heap_table_size", "Max memory table size", "Largest user-created MEMORY table.", 16384, maxUnsigned, "16M"),
		settings.Bytes("tmp_table_size", "Temporary table size", "Largest in-memory temporary table before it moves to disk.", 0, maxUnsigned, "16M"),
		settings.Integer("max_seeks_for_key", "Max seeks for key", "Most key seeks the optimizer assumes when looking rows up by key.", 1, maxUnsigned32, "4294967295"),
		settings.Bytes("net_buffer_length", "Network buffer length", "Starting size of the connection and result buffers.", 1024, 1048576, "16384"),
		settings.Integer("net_read_timeout", "Network read timeout", "Seconds to wait for more data from a connection before aborting the read.", 1, yearSeconds, "30").In("s"),
		settings.Integer("net_write_timeout", "Network write timeout", "Seconds to wait for a block to be written to a connection before aborting the write.", 1, yearSeconds, "60").In("s"),
		settings.Bytes("sort_buffer_size", "Sort buffer size", "Memory each session may use for a sort.", 1024, maxUnsigned, "2M"),
		settings.Integer("wait_timeout", "Idle timeout", "Seconds before an idle connection is closed.", 1, yearSeconds, "28800").In("s"),
		settings.Integer("binlog_expire_logs_seconds", "Binary log retention", "Seconds binary logs are kept. At least a day, so point-in-time restore always reaches the last backup.", daySeconds, 8553600, "604800").In("s"),
		settings.Choice("character_set_server", "Character set", "Default character set for new databases and tables.", []string{"utf8mb4", "utf8mb3", "latin1", "ascii", "binary"}, "utf8mb4"),
		settings.Text("collation_server", "Collation", "Default collation for new databases and tables.", collationPattern, "utf8mb4_uca1400_ai_ci"),
		settings.Flags("sql_mode", "SQL mode", "Which SQL rules the server enforces, such as strict inserts.", sqlModes, "STRICT_TRANS_TABLES,ERROR_FOR_DIVISION_BY_ZERO,NO_AUTO_CREATE_USER,NO_ENGINE_SUBSTITUTION"),
		settings.Integer("innodb_flush_neighbors", "Flush neighbors", "Flush neighbouring dirty pages together: 0 off, 1 contiguous, 2 same extent.", 0, 2, "1"),
		settings.Bytes("innodb_online_alter_log_max_size", "Online ALTER log size", "Most temporary log space one online DDL operation may use.", 65536, maxUnsigned, "128M"),
		settings.Decimal("max_statement_time", "Max statement time", "Seconds a query may run before it is stopped. 0 never stops.", 0, yearSeconds, "0").In("s"),
		settings.Integer("optimizer_prune_level", "Optimizer pruning", "Let the optimizer skip unpromising plans: 0 off, 1 heuristic, 2 aggressive.", 0, 2, "2"),
		settings.Integer("optimizer_search_depth", "Optimizer search depth", "How deep the optimizer searches join orders. 0 picks it automatically.", 0, 62, "62"),
		settings.Text("optimizer_switch", "Optimizer switches", "Comma-separated flag=on|off pairs. Empty keeps the server defaults.", switchPattern, ""),
	)}
}

func tuned(resources dtos.Member) map[string]string {
	values := family.InnodbTuning(resources)
	values["innodb_log_file_size"] = strconv.FormatInt(family.LogCapacityMb(resources), 10) + "M"

	return values
}
