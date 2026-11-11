package mariadb

import (
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/command"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/enums"
	"github.com/databasifylabs/engine-sdk/requests"
	"github.com/databasifylabs/engine-sdk/responses"
)

const (
	grastate         = "data/grastate.dat"
	rejoinTimeout    = 10 * time.Minute
	switchTimeout    = time.Minute
	lastCommitQuery  = `SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_LAST_COMMITTED'`
	clusterSizeQuery = `SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_CLUSTER_SIZE'`
)

var (
	recoveredPosition = regexp.MustCompile(`WSREP: Recovered position: [0-9a-f-]+:(-?\d+)`)
	savedSeqno        = regexp.MustCompile(`(?m)^seqno:\s*(-?\d+)`)
)

type cluster struct{ Engine }

func (e Engine) Cluster() contract.Cluster { return cluster{e} }

func (e Engine) replicationPassword() (string, error) {
	request, err := family.Saved(e.root)
	if err != nil {
		return "", err
	}
	if request.Secrets.Replication == nil {
		return "", command.Invalid("MariaDB clusters need the replication secret")
	}

	return *request.Secrets.Replication, nil
}

func (e cluster) Enable(ctx context.Context, _ requests.ClusterEnable, report contract.Report) error {
	request, err := family.Saved(e.root)
	if err != nil {
		return err
	}
	if request.ReadOnly {
		return e.followWriter(ctx, request, report)
	}
	if !galera(request) {
		return command.Invalid("run node configure with clustered on first")
	}
	seed := request.Seed()
	if state, err := e.state(ctx); err == nil && state.galera && state.synced() {
		if !seed {
			return nil
		}

		return e.becomeWriterIfRecorded(ctx, request.Ip)
	}
	if err := family.RequirePrimaryKeys(ctx, e.db, e.Capabilities().ReservedDatabases); err != nil {
		return err
	}
	if seed {
		report("bootstrapping")
		if err := e.markNewCluster(); err != nil {
			return err
		}
	} else {
		report("joining")
	}
	if err := e.restart(); err != nil {
		return err
	}
	report("syncing")
	if err := e.waitSynced(ctx); err != nil {
		return err
	}
	if err := e.clearNewCluster(); err != nil || !seed {
		return err
	}

	return e.becomeWriterIfRecorded(ctx, request.Ip)
}

func (e Engine) restart() error {
	if err := e.supervisor.Do("daemon-reload"); err != nil {
		return err
	}

	return e.supervisor.Do("restart", serverUnit)
}

func (e cluster) Disable(ctx context.Context) (responses.ClusterDisable, error) {
	request, err := family.Saved(e.root)
	if err != nil {
		return responses.ClusterDisable{}, err
	}
	if request.ReadOnly {
		return responses.ClusterDisable{}, e.stopFollowing(ctx)
	}
	request.Clustered = false
	if _, err := e.writeFiles(request); err != nil {
		return responses.ClusterDisable{}, err
	}
	if err := e.restart(); err != nil {
		return responses.ClusterDisable{}, err
	}

	return responses.ClusterDisable{}, family.Until(ctx, readyTimeout, func() (bool, error) {
		_, _, err := e.db.Value(ctx, "SELECT 1")

		return err == nil, nil
	})
}

func (e cluster) Failover(ctx context.Context, request requests.ClusterFailover) error {
	saved, err := family.Saved(e.root)
	if err != nil {
		return err
	}
	password, err := e.replicationPassword()
	if err != nil {
		return err
	}
	target := e.connect(request.Target, password)
	if err := recordWriter(ctx, target, request.Target, "REPLACE"); err != nil {
		return err
	}
	for _, member := range saved.Members {
		if !member.Voting || member.Ip == request.Target {
			continue
		}
		if err := e.connect(member.Ip, password).Exec(ctx, "SET GLOBAL read_only = ON"); err != nil && !family.IsUnavailable(err) {
			return err
		}
	}
	if err := target.Exec(ctx, "SET GLOBAL read_only = OFF"); err != nil {
		return err
	}

	return family.Until(ctx, switchTimeout, func() (bool, error) {
		readOnly, _, err := target.Value(ctx, "SELECT @@read_only")

		return err == nil && !isReadOnly(readOnly), nil
	})
}

func (e cluster) Quorum(ctx context.Context) (responses.ClusterQuorum, error) {
	saved, err := family.Saved(e.root)
	if err != nil {
		return responses.ClusterQuorum{}, err
	}
	quorum := responses.ClusterQuorum{Total: family.VotersIn(saved.Members)}
	state, err := e.state(ctx)
	if err != nil || state.clusterStatus != "Primary" {
		return quorum, nil
	}
	size, _, _ := e.db.Value(ctx, clusterSizeQuery)
	quorum.Online, _ = strconv.ParseInt(size, 10, 64)
	quorum.Total = max(quorum.Total, quorum.Online)

	return quorum, nil
}

func (e cluster) ForceQuorum(ctx context.Context, request requests.ClusterForceQuorum) (responses.ClusterForceQuorum, error) {
	if !request.OthersDown {
		return responses.ClusterForceQuorum{}, command.Failure{Code: enums.ErrorCodeBusy, Message: "forcing the cluster while other nodes may be alive would split it; confirm the others are down"}
	}
	saved, err := family.Saved(e.root)
	if err != nil {
		return responses.ClusterForceQuorum{}, err
	}
	state, err := e.state(ctx)
	if err == nil && state.clusterStatus == "Primary" {
		return responses.ClusterForceQuorum{}, command.Failure{Code: enums.ErrorCodeBusy, Message: "this node is already in a primary component"}
	}
	if err == nil {
		err = e.db.Exec(ctx, "SET GLOBAL wsrep_provider_options = 'pc.bootstrap=YES'")
	} else {
		err = e.bootstrapFromDisk()
	}
	if err != nil {
		return responses.ClusterForceQuorum{}, err
	}
	if err := e.waitSynced(ctx); err != nil {
		return responses.ClusterForceQuorum{}, err
	}

	return responses.ClusterForceQuorum{Leader: saved.Ip}, e.clearNewCluster()
}

func (e Engine) bootstrapFromDisk() error {
	content := strings.Replace(e.root.Read(grastate), "safe_to_bootstrap: 0", "safe_to_bootstrap: 1", 1)
	if err := os.WriteFile(e.root.Path(grastate), []byte(content), 0o660); err != nil {
		return err
	}
	if err := e.markNewCluster(); err != nil {
		return err
	}
	if err := e.supervisor.Do("daemon-reload"); err != nil {
		return err
	}

	return e.supervisor.Do("start", serverUnit)
}

func (e cluster) Position(ctx context.Context) (responses.ClusterPosition, error) {
	position := e.positionOf(ctx)

	return responses.ClusterPosition{Position: &position}, nil
}

func (e Engine) positionOf(ctx context.Context) int64 {
	if e.supervisor.Active(serverUnit) {
		committed, found, err := e.db.Value(ctx, lastCommitQuery)
		if err != nil || !found {
			return -1
		}
		position, _ := strconv.ParseInt(committed, 10, 64)

		return position
	}
	if match := savedSeqno.FindStringSubmatch(e.root.Read(grastate)); match != nil && match[1] != "-1" {
		position, _ := strconv.ParseInt(match[1], 10, 64)

		return position
	}
	output, _ := e.run(ctx, mariadbdBinary, "--defaults-file="+e.root.Path(myCnf), "--wsrep-recover", "--user="+engineUser)
	match := recoveredPosition.FindStringSubmatch(output)
	if match == nil {
		return -1
	}
	position, _ := strconv.ParseInt(match[1], 10, 64)

	return position
}

func (e cluster) Rejoin(ctx context.Context, request requests.ClusterRejoin, report contract.Report) error {
	if request.ReadOnly {
		report("pointing replication at a synced member")
		return e.repointReplica(ctx)
	}
	report("starting MariaDB")
	if err := e.supervisor.Do("start", serverUnit); err != nil {
		return err
	}
	report("waiting for Galera to sync")

	return family.Until(ctx, rejoinTimeout, func() (bool, error) {
		state, err := e.state(ctx)

		return err == nil && state.synced(), nil
	})
}

func (e cluster) UpdateMembership(_ context.Context, request requests.ClusterMembership) error {
	saved, err := family.Saved(e.root)
	if err != nil {
		return err
	}
	saved.Members = request.Members
	_, err = e.writeFiles(saved)

	return err
}

func (e cluster) Admit(context.Context, requests.MemberAdmit) error { return nil }

func (e cluster) Promote(context.Context, requests.MemberPromote) error { return nil }

func (e cluster) Forget(context.Context, requests.MemberForget) error { return nil }
