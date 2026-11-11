package mariadb

import (
	"context"
	"os"

	"github.com/databasifylabs/engine-sdk/version"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/layout"
	"github.com/databasifylabs/engine-sdk/nodefs"
	"github.com/databasifylabs/engine-sdk/requests"
	"github.com/databasifylabs/engine-sdk/units"
)

const (
	root       = "/srv/mariadb"
	socketPath = root + "/run/mariadbd.sock"
	adminCnf   = root + "/conf/admin.cnf"

	parsecSince = "11.6"
)

type Engine struct {
	db         family.NamedDB
	supervisor family.Supervisor
	run        family.Runner
	connect    func(host string, password string) family.DB
	line       string
	root       nodefs.Root
}

func New() Engine {
	return Engine{
		db:         family.SocketDB{Socket: socketPath, PasswordCnf: adminCnf},
		supervisor: units.Control{Socket: layout.FromEnv().Socket()},
		run:        family.ExecRunner,
		connect:    replicationUserOn,
		line:       os.Getenv("DATABASIFY_LINE"),
		root:       root,
	}
}

func (Engine) Name() string { return "mariadb" }

func parsecOffered(line string) bool {
	return !version.NewerThan(parsecSince, line)
}

func (e Engine) Capabilities() contract.Capabilities {
	encryption := "mysql_native_password"
	encryptions := []string{encryption, "ed25519"}
	if parsecOffered(e.line) {
		encryptions = append(encryptions, "parsec")
	}

	return contract.Capabilities{
		PasswordEncryptions:       encryptions,
		DefaultPasswordEncryption: &encryption,
		ReservedUsers:             []string{"root", "mysql", "mariadb.sys", "databasify_sst", "databasify_repl", "databasify_agent"},
		ReservedDatabases:         []string{"mysql", "sys", "performance_schema", "information_schema", "databasify"},
		Ports:                     dtos.Ports{Client: clientPort, Peers: []int{sstPort, galeraPort, istPort}},
	}
}

func (Engine) HostProfile() contract.HostProfile { return family.HostProfile() }

type nodes struct{ Engine }

func (e Engine) Nodes() contract.Nodes { return nodes{e} }

func (e Engine) Accounts() contract.Accounts {
	return family.Accounts{DB: e.db, Capabilities: e.Capabilities(), Identified: identifiedWith}
}

func identifiedWith(encryption string) string {
	if encryption == "ed25519" || encryption == "parsec" {
		return "IDENTIFIED VIA " + encryption + " USING PASSWORD(?)"
	}

	return "IDENTIFIED BY ?"
}

func replicationUserOn(host string, password string) family.DB {
	return family.RemoteDB{Host: host, Port: clientPort, User: "databasify_repl", Password: password, Tls: true}
}

func (Engine) Pools() contract.Pools { return contract.UnsupportedPools{} }

func (e Engine) Config() contract.Config { return e.configs() }

func (e Engine) configs() family.Configs {
	return family.Configs{
		DB: e.db, Root: e.root, Catalog: catalog(), Variables: timeVariables, Apply: e.Nodes().ApplyConfig,
		Configure: func(ctx context.Context, request requests.NodeConfigure) error {
			_, err := e.Nodes().Configure(ctx, request)

			return err
		},
	}
}
