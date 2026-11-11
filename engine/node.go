package mariadb

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/command"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/requests"
	"github.com/databasifylabs/engine-sdk/responses"
)

const (
	initFile      = "conf/init.sql"
	readyTimeout  = 6 * time.Hour
	crashRestarts = 3
)

func (e nodes) Configure(ctx context.Context, request requests.NodeConfigure) (responses.NodeConfigure, error) {
	if request.Secrets.Admin == nil {
		return responses.NodeConfigure{}, command.Invalid("MariaDB needs the admin secret")
	}
	if galera(request) && request.Secrets.Replication == nil {
		return responses.NodeConfigure{}, command.Invalid("MariaDB clusters need the replication secret")
	}
	if request.Archive != nil && request.Secrets.Agent == nil {
		return responses.NodeConfigure{}, command.Invalid("MariaDB backups need the agent secret")
	}
	if err := e.configs().Catalog.Check(request.Settings); err != nil {
		return responses.NodeConfigure{}, err
	}
	for _, dir := range []string{"conf", "data", "run", "state"} {
		if err := e.root.Dir(dir); err != nil {
			return responses.NodeConfigure{}, err
		}
	}
	changed, err := e.writeFiles(request)
	if err != nil {
		return responses.NodeConfigure{}, err
	}
	if e.root.Empty("data") {
		if err := e.initialize(ctx, request); err != nil {
			return responses.NodeConfigure{}, err
		}
		if err := e.markSeed(request); err != nil {
			return responses.NodeConfigure{}, err
		}
		changed = true
	}

	if err := e.supervisor.Do("daemon-reload"); err != nil {
		return responses.NodeConfigure{}, err
	}

	return responses.NodeConfigure{Changed: changed}, e.startHelpers(request)
}

func (e Engine) startHelpers(request requests.NodeConfigure) error {
	helpers := []string{}
	if request.Archive != nil {
		helpers = append(helpers, archiveUnits.Stream)
	}
	if request.Clustered {
		helpers = append(helpers, watchUnit)
	}
	if len(helpers) == 0 {
		return nil
	}

	return e.supervisor.Do("start", helpers...)
}

func (e Engine) writeFiles(request requests.NodeConfigure) (bool, error) {
	state, err := json.Marshal(request)
	if err != nil {
		return false, err
	}
	effective, err := e.configs().Effective(request)
	if err != nil {
		return false, err
	}
	changed := false
	for _, file := range []struct {
		path        string
		content     string
		engineOwned bool
	}{
		{myCnf, myCnfOf(string(e.root), e.line, effective), true},
		{"conf/admin.cnf", family.AdminCnfOf(socketPath, *request.Secrets.Admin), false},
		{family.NodeState, string(state), false},
		{agentCnf, agentCnfOf(string(e.root), family.ValueOr(request.Secrets.Agent)), false},
	} {
		written, err := e.root.Write(file.path, file.content, 0o600, file.engineOwned)
		if err != nil {
			return false, err
		}
		changed = changed || written
	}
	written, err := e.writeManifest()

	return changed || written, err
}

func (e Engine) writeManifest() (bool, error) {
	request, err := family.Saved(e.root)
	if err != nil {
		return false, err
	}
	encoded, err := manifestOf(string(e.root), request, e.root.Exists(newClusterState))
	if err != nil {
		return false, err
	}

	return e.root.Write(manifestFile, string(encoded), 0o600, false)
}

func (e Engine) markSeed(request requests.NodeConfigure) error {
	if !galera(request) || (request.Primary != nil && *request.Primary != request.Ip) {
		return nil
	}

	return e.markNewCluster()
}

func (e Engine) markNewCluster() error {
	if _, err := e.root.Write(newClusterState, "", 0o600, false); err != nil {
		return err
	}
	_, err := e.writeManifest()

	return err
}

func (e Engine) clearNewCluster() error {
	if !e.root.Exists(newClusterState) {
		return nil
	}
	if err := os.Remove(e.root.Path(newClusterState)); err != nil {
		return err
	}
	if _, err := e.writeManifest(); err != nil {
		return err
	}

	return e.supervisor.Do("daemon-reload")
}

func initStatementsOf(password string, request requests.NodeConfigure) string {
	statements := "FLUSH PRIVILEGES;\nALTER USER 'root'@'localhost' IDENTIFIED VIA mysql_native_password USING PASSWORD(" + family.SQLString(password) + ") OR unix_socket;\n"
	if !galera(request) || request.Secrets.Replication == nil {
		return statements
	}

	return statements + "CREATE USER 'databasify_sst'@'localhost' IDENTIFIED BY " + family.SQLString(*request.Secrets.Replication) + ";\n" +
		"GRANT RELOAD, PROCESS, LOCK TABLES, BINLOG MONITOR ON *.* TO 'databasify_sst'@'localhost';\n"
}

func (e Engine) initialize(ctx context.Context, request requests.NodeConfigure) error {
	if _, err := e.root.Write(initFile, initStatementsOf(*request.Secrets.Admin, request), 0o600, true); err != nil {
		return err
	}
	defer func() { _ = os.Remove(e.root.Path(initFile)) }()
	if err := os.Remove(e.root.Path(archiveIDState)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	_, err := e.run(ctx, "mariadb-install-db", "--no-defaults", "--user="+engineUser, "--datadir="+e.root.Path("data"),
		"--skip-test-db", "--auth-root-authentication-method=socket", "--extra-file="+e.root.Path(initFile))

	return err
}

func (e nodes) Start(context.Context) error {
	if err := e.supervisor.Do("start", serverUnit); err != nil {
		return err
	}
	request, err := family.Saved(e.root)
	if err != nil {
		return err
	}

	return e.startHelpers(request)
}

func (e nodes) Ready(ctx context.Context, _ requests.NodeReady, report contract.Report) error {
	report("waiting for MariaDB")
	baseline := e.restarts()
	err := family.Until(ctx, readyTimeout, func() (bool, error) {
		if e.restarts()-baseline > crashRestarts {
			return false, errors.New("MariaDB keeps stopping while it starts, see its log")
		}
		_, _, err := e.db.Value(ctx, "SELECT 1")

		return err == nil, nil
	})
	if err != nil {
		return err
	}
	request, err := family.Saved(e.root)
	if err != nil || !galera(request) {
		return err
	}
	report("syncing")
	if err := e.waitSynced(ctx); err != nil {
		return err
	}
	e.keepWriterRole(ctx, request)

	return e.clearNewCluster()
}

func (e Engine) waitSynced(ctx context.Context) error {
	return family.Until(ctx, readyTimeout, func() (bool, error) {
		state, err := e.state(ctx)

		return err == nil && state.synced(), nil
	})
}

func (e Engine) restarts() int {
	restarts, _ := strconv.Atoi(e.supervisor.Property(serverUnit, "NRestarts"))

	return restarts
}

func (e nodes) Wake(context.Context, requests.NodeWake) error {
	_ = e.supervisor.Kill("KILL", serverUnit)

	return e.supervisor.Do("stop", serverUnit)
}

func (e nodes) Bootstrap(ctx context.Context, request requests.NodeBootstrap) error {
	if request.Secrets.Replication == nil || request.Secrets.Agent == nil {
		return command.Invalid("MariaDB needs the replication and agent secrets")
	}
	saved, err := family.Saved(e.root)
	if err != nil || !saved.Seed() {
		return err
	}
	statements := []struct {
		query string
		args  []any
	}{
		{"CREATE USER IF NOT EXISTS 'databasify_sst'@'localhost' IDENTIFIED BY ?", []any{*request.Secrets.Replication}},
		{"ALTER USER 'databasify_sst'@'localhost' IDENTIFIED BY ?", []any{*request.Secrets.Replication}},
		{"GRANT RELOAD, PROCESS, LOCK TABLES, BINLOG MONITOR ON *.* TO 'databasify_sst'@'localhost'", nil},
		{"CREATE USER IF NOT EXISTS 'databasify_repl'@'%' IDENTIFIED BY ? REQUIRE SSL", []any{*request.Secrets.Replication}},
		{"ALTER USER 'databasify_repl'@'%' IDENTIFIED BY ?", []any{*request.Secrets.Replication}},
		{"GRANT SELECT, SHOW VIEW, TRIGGER, EVENT, RELOAD, LOCK TABLES, REPLICATION SLAVE, BINLOG MONITOR, READ_ONLY ADMIN ON *.* TO 'databasify_repl'@'%'", nil},
		{"GRANT ALL PRIVILEGES ON `databasify`.* TO 'databasify_repl'@'%'", nil},
		{"CREATE USER IF NOT EXISTS 'databasify_agent'@'localhost' IDENTIFIED BY ?", []any{*request.Secrets.Agent}},
		{"ALTER USER 'databasify_agent'@'localhost' IDENTIFIED BY ?", []any{*request.Secrets.Agent}},
		{"GRANT RELOAD, PROCESS, LOCK TABLES, BINLOG MONITOR, REPLICATION SLAVE, SLAVE MONITOR ON *.* TO 'databasify_agent'@'localhost'", nil},
	}
	for _, statement := range statements {
		if err := e.db.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}
	if err := e.recordFirstWriter(ctx); err != nil {
		return err
	}
	return contract.CreateDefaultAccount(ctx, e.Accounts(), request.Account)
}

func (e Engine) recordFirstWriter(ctx context.Context) error {
	saved, err := family.Saved(e.root)
	if err != nil || !galera(saved) {
		return err
	}

	return e.becomeWriterIfRecorded(ctx, saved.Ip)
}

func (e Engine) becomeWriterIfRecorded(ctx context.Context, ip string) error {
	if err := recordWriter(ctx, e.db, ip, "INSERT IGNORE"); err != nil {
		return err
	}
	writer, _, err := e.db.Value(ctx, writerQuery)
	if err != nil || writer != ip {
		return err
	}

	return e.db.Exec(ctx, "SET GLOBAL read_only = OFF")
}

func recordWriter(ctx context.Context, db family.DB, ip string, verb string) error {
	for _, statement := range []string{
		"CREATE DATABASE IF NOT EXISTS databasify",
		"CREATE TABLE IF NOT EXISTS databasify.writer (id TINYINT PRIMARY KEY, ip VARCHAR(45) NOT NULL)",
	} {
		if err := db.Exec(ctx, statement); err != nil {
			return err
		}
	}

	return db.Exec(ctx, verb+" INTO databasify.writer VALUES (1, ?)", ip)
}

func (e nodes) SyncConfig(context.Context) (responses.ConfigSync, error) {
	return responses.ConfigSync{}, nil
}

func (e nodes) ApplyConfig(ctx context.Context) error {
	request, err := family.Saved(e.root)
	if err != nil {
		return err
	}

	effective, err := e.configs().Effective(request)
	if err != nil {
		return err
	}

	return family.Settings{DB: e.db, Root: e.root, Set: "SET GLOBAL", Static: e.configs().Static()}.Apply(ctx, effective.Settings)
}

func (e nodes) InstallTLS(ctx context.Context, request requests.TlsInstall) error {
	return e.root.InstallTLS(ctx, request, false, e.ReloadTLS)
}

func (e nodes) Restart(context.Context) error {
	return e.supervisor.Do("restart", serverUnit)
}

func (e nodes) ReloadTLS(ctx context.Context) error {
	if !e.supervisor.Active(serverUnit) {
		return nil
	}

	return e.db.Exec(ctx, "FLUSH SSL")
}

func (e nodes) Reset(context.Context, requests.NodeReset) error {
	return contract.ErrUnsupported
}
