package mariadb

import (
	"bytes"
	"context"
	"net"
	"slices"
	"strings"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/requests"
)

const (
	watchInterval      = 10 * time.Second
	disconnectedChecks = 3
	repointTimeout     = time.Minute
	writerQuery        = `SELECT ip FROM databasify.writer WHERE id = 1`
	incomingQuery      = `SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_INCOMING_ADDRESSES'`
	localStateQuery    = `SELECT VARIABLE_VALUE FROM information_schema.GLOBAL_STATUS WHERE VARIABLE_NAME = 'WSREP_LOCAL_STATE_COMMENT'`
)

func (e cluster) Watch(ctx context.Context) error {
	missed := 0
	for ctx.Err() == nil {
		if request, err := family.Saved(e.root); err == nil {
			if galera(request) {
				e.keepWriterRole(ctx, request)
			}
			if request.ReadOnly && e.root.Exists(replicaState) {
				missed = e.keepFollowing(ctx, request, missed)
			}
		}
		family.Sleep(ctx, watchInterval)
	}

	return nil
}

func (e Engine) keepWriterRole(ctx context.Context, request requests.NodeConfigure) {
	state, err := e.state(ctx)
	if err != nil || !state.synced() {
		return
	}
	writer, recorded, err := e.db.Value(ctx, writerQuery)
	if err != nil {
		return
	}
	if !recorded {
		e.holdReadOnly(ctx, state, true)

		return
	}
	incoming, _, _ := e.db.Value(ctx, incomingQuery)
	live := addressesIn(incoming)
	if !slices.Contains(live, writer) {
		if lowest(live) != request.Ip {
			return
		}
		if err := e.db.Exec(ctx, "REPLACE INTO databasify.writer VALUES (1, ?)", request.Ip); err != nil {
			return
		}
		writer = request.Ip
	}
	e.holdReadOnly(ctx, state, writer != request.Ip)
}

func (e Engine) holdReadOnly(ctx context.Context, state nodeState, readOnly bool) {
	if readOnly == state.readOnly {
		return
	}
	setting := "OFF"
	if readOnly {
		setting = "ON"
	}
	_ = e.db.Exec(ctx, "SET GLOBAL read_only = "+setting)
}

func addressesIn(incoming string) []string {
	addresses := []string{}
	for _, address := range strings.Split(incoming, ",") {
		host, _, err := net.SplitHostPort(strings.TrimSpace(address))
		if err == nil && net.ParseIP(host) != nil {
			addresses = append(addresses, host)
		}
	}

	return addresses
}

func lowest(addresses []string) string {
	chosen := ""
	for _, address := range addresses {
		if chosen == "" || bytes.Compare(net.ParseIP(address).To16(), net.ParseIP(chosen).To16()) < 0 {
			chosen = address
		}
	}

	return chosen
}

func (e Engine) keepFollowing(ctx context.Context, request requests.NodeConfigure, missed int) int {
	running, _, err := e.db.Value(ctx, slaveRunningQuery)
	if err != nil || running == "ON" {
		return 0
	}
	if missed+1 < disconnectedChecks {
		return missed + 1
	}
	password, err := e.replicationPassword()
	if err != nil {
		return 0
	}
	source := nextSource(request.Members, e.currentSource(ctx), func(ip string) bool {
		state, _, err := e.connect(ip, password).Value(ctx, localStateQuery)

		return err == nil && state == "Synced"
	})
	if source == "" {
		return 0
	}
	repoint, cancel := context.WithTimeout(ctx, repointTimeout)
	defer cancel()
	_ = e.follow(repoint, source, password)

	return 0
}

func (e Engine) currentSource(ctx context.Context) string {
	connections, err := e.db.Named(ctx, "SHOW ALL SLAVES STATUS")
	if err != nil {
		return ""
	}
	for _, connection := range connections {
		if connection["Connection_name"] == "" {
			return connection["Master_Host"]
		}
	}

	return ""
}

func nextSource(members []dtos.Member, current string, synced func(ip string) bool) string {
	for _, member := range members {
		if member.Voting && member.Ip != current && synced(member.Ip) {
			return member.Ip
		}
	}

	return ""
}
