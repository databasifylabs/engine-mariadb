package mariadb

import (
	"context"
	"errors"
	"os"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/command"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/requests"
)

const (
	replicaState      = "state/replica"
	slaveRunningQuery = `SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'SLAVE_RUNNING'`
)

func (e Engine) followWriter(ctx context.Context, request requests.NodeConfigure, report contract.Report) error {
	if request.Primary == nil {
		return command.Invalid("a read-only node needs the writer to follow")
	}
	password, err := e.replicationPassword()
	if err != nil {
		return err
	}
	position, _, err := e.db.Value(ctx, "SELECT @@gtid_slave_pos")
	if err != nil {
		return err
	}
	if position == "" {
		report("copying")
		if err := family.Retry(ctx, rejoinTimeout, func() error { return e.seedFrom(ctx, *request.Primary, password) }); err != nil {
			return err
		}
	}
	report("following")
	if err := e.follow(ctx, *request.Primary, password); err != nil {
		return err
	}
	_, err = e.root.Write(replicaState, "", 0o600, false)

	return err
}

func (e Engine) seedFrom(ctx context.Context, writer string, password string) error {
	gtid, err := e.copyFrom(ctx, dumpSource{host: writer, port: clientPort, user: "databasify_repl", password: password, tls: true}, nil, true)
	if err != nil {
		return err
	}
	if gtid == "" {
		return errors.New("the copy from the writer carried no GTID position")
	}

	return e.db.Exec(ctx, "SET GLOBAL gtid_slave_pos = ?", gtid)
}

func (e Engine) follow(ctx context.Context, writer string, password string) error {
	if err := e.db.Exec(ctx, "STOP SLAVE"); err != nil {
		return err
	}
	if err := e.db.Exec(ctx, "CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = 'databasify_repl', MASTER_PASSWORD = ?, MASTER_SSL = 1, MASTER_USE_GTID = slave_pos", writer, clientPort, password); err != nil {
		return err
	}
	if err := e.db.Exec(ctx, "START SLAVE"); err != nil {
		return err
	}

	return family.Until(ctx, rejoinTimeout, func() (bool, error) {
		running, _, err := e.db.Value(ctx, slaveRunningQuery)

		return err == nil && running == "ON", nil
	})
}

func (e Engine) repointReplica(ctx context.Context) error {
	request, err := family.Saved(e.root)
	if err != nil {
		return err
	}
	if request.Primary == nil {
		return command.Invalid("a read-only node needs the writer to follow")
	}
	password, err := e.replicationPassword()
	if err != nil {
		return err
	}

	return e.follow(ctx, *request.Primary, password)
}

func (e Engine) stopFollowing(ctx context.Context) error {
	for _, statement := range []string{"STOP SLAVE", "RESET SLAVE ALL", "SET GLOBAL read_only = OFF"} {
		if err := e.db.Exec(ctx, statement); err != nil {
			return err
		}
	}
	if err := os.Remove(e.root.Path(replicaState)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	return nil
}
