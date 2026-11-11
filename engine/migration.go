package mariadb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/databasifylabs/engine-sdk/version"

	"github.com/go-sql-driver/mysql"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/command"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/requests"
	"github.com/databasifylabs/engine-sdk/responses"
)

const (
	migrationUnit       = "mariadb-migration"
	errNoSuchConnection = 1617
)

type migration struct{ Engine }

func (e Engine) Migration() contract.Migration { return migration{e} }

func (e Engine) migrator() family.Migrator {
	return family.Migrator{DB: e.db, Root: e.root, Supervisor: e.supervisor, Unit: migrationUnit, Reserved: e.Capabilities().ReservedDatabases}
}

func (e migration) Check(ctx context.Context, request requests.MigrationCheck) (responses.MigrationCheck, error) {
	report, version, err := e.migrator().Inventory(ctx, request)
	if err != nil || !report.Reachable {
		return report, err
	}
	source := e.migrator().Source(request.Source)
	logBin, _, _ := source.Value(ctx, "SELECT @@log_bin")
	format, _, _ := source.Value(ctx, "SELECT @@binlog_format")
	grants, _ := source.Rows(ctx, "SHOW GRANTS")
	local, _, _ := e.db.Value(ctx, "SELECT @@version")
	canReplicate, warnings := readinessOf(version, logBin, format, strings.ToUpper(fmt.Sprint(grants)), local)
	report.CanReplicate, report.Warnings = canReplicate, append(report.Warnings, warnings...)

	return report, nil
}

func readinessOf(sourceVersion string, logBin string, format string, granted string, local string) (bool, []string) {
	if !strings.Contains(sourceVersion, "MariaDB") {
		return false, []string{"the source is MySQL, whose GTIDs MariaDB cannot follow, so only a one-shot copy is possible"}
	}
	warnings := []string{}
	if logBin != "1" {
		warnings = append(warnings, "the source has no binary log, so only a one-shot copy is possible")
	}
	if logBin == "1" && format != "ROW" {
		warnings = append(warnings, "the source logs in "+format+" format; ROW is safer for a continuous copy")
	}
	release, _, _ := strings.Cut(sourceVersion, "-")
	if version.NewerThan(release, local) {
		warnings = append(warnings, "the source runs "+release+", newer than this server; copying to an older version is not supported")
	}
	granting := strings.Contains(granted, "REPLICATION SLAVE") || strings.Contains(granted, "ALL PRIVILEGES ON *.*")

	return logBin == "1" && granting, warnings
}

func (e migration) Start(_ context.Context, request requests.MigrationStart) error {
	return e.migrator().Start(request)
}

func dumpSourceOf(source dtos.MigrationSource) dumpSource {
	return dumpSource{host: source.Host, port: source.Port, user: source.Username, password: source.Password, tls: source.Tls}
}

func (e migration) Copy(ctx context.Context) error {
	source, requested, continuous := family.SourceFromEnvironment()
	databases, err := e.migrator().Databases(ctx, source, requested)
	if err != nil {
		return err
	}
	if len(databases) == 0 {
		return command.Invalid("the source has no databases to copy")
	}
	version, _, err := e.migrator().Source(source).Value(ctx, "SELECT @@version")
	if err != nil {
		return err
	}
	fromMariaDB := strings.Contains(version, "MariaDB")
	gtid, err := e.copyFrom(ctx, dumpSourceOf(source), databases, fromMariaDB)
	if err != nil || !continuous {
		return err
	}
	if !fromMariaDB || gtid == "" {
		return errors.New("the source gave no MariaDB GTID position, so it cannot be followed")
	}

	return e.followSource(ctx, dumpSourceOf(source), gtid, databases)
}

func (e Engine) followSource(ctx context.Context, source dumpSource, gtid string, databases []string) error {
	ssl := 0
	if source.tls {
		ssl = 1
	}
	if err := e.stopConnection(ctx, "STOP SLAVE 'migration'"); err != nil {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"SET GLOBAL gtid_slave_pos = ?", []any{gtid}},
		{"CHANGE MASTER 'migration' TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?, MASTER_SSL = ?, MASTER_SSL_VERIFY_SERVER_CERT = 0, MASTER_USE_GTID = slave_pos", []any{source.host, source.port, source.user, source.password, ssl}},
		{"SET GLOBAL migration.replicate_do_db = ?", []any{strings.Join(databases, ",")}},
		{"START SLAVE 'migration'", nil},
	}
	for _, statement := range statements {
		if err := e.db.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}

	return nil
}

func (e Engine) stopConnection(ctx context.Context, statement string) error {
	err := e.db.Exec(ctx, statement)
	var server *mysql.MySQLError
	if err != nil && !(errors.As(err, &server) && server.Number == errNoSuchConnection) {
		return err
	}

	return nil
}

func (e migration) Progress(ctx context.Context) (responses.MigrationProgress, error) {
	return e.migrator().Progress(func() (int64, bool) { return e.connectionLag(ctx) }), nil
}

func (e Engine) connectionLag(ctx context.Context) (int64, bool) {
	connections, err := e.db.Named(ctx, "SHOW ALL SLAVES STATUS")
	if err != nil {
		return 0, false
	}
	for _, connection := range connections {
		if connection["Connection_name"] != "migration" || connection["Slave_IO_Running"] != "Yes" || connection["Slave_SQL_Running"] != "Yes" {
			continue
		}
		lag, err := strconv.ParseInt(connection["Seconds_Behind_Master"], 10, 64)

		return lag, err == nil
	}

	return 0, false
}

func (e migration) Stop(ctx context.Context) error {
	e.migrator().StopCopy()
	for _, statement := range []string{"STOP SLAVE 'migration'", "RESET SLAVE 'migration' ALL"} {
		if err := e.stopConnection(ctx, statement); err != nil {
			return err
		}
	}

	return nil
}
