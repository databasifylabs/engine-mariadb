package mariadb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/dtos"
	"github.com/databasifylabs/engine-sdk/manifest"
	"github.com/databasifylabs/engine-sdk/requests"
)

const (
	clientPort      = 25060
	sstPort         = 4444
	galeraPort      = 4567
	istPort         = 4568
	serverUnit      = "mariadb-server"
	watchUnit       = "mariadb-cluster-watch"
	engineUser      = "mysql"
	mariadbdBinary  = "/usr/sbin/mariadbd"
	galeraProvider  = "/usr/lib/galera/libgalera_smm.so"
	myCnf           = "conf/my.cnf"
	manifestFile    = "conf/processes.json"
	newClusterState = "state/new-cluster"
	galeraNameLimit = 32
	stopTimeout     = 10 * time.Minute
	restartPause    = 3 * time.Second
	domainOffset    = 10
)

func clusterNameOf(databaseID string) string {
	name := strings.ReplaceAll(databaseID, "-", "")
	if len(name) <= galeraNameLimit {
		return name
	}
	sum := sha256.Sum256([]byte(databaseID))

	return hex.EncodeToString(sum[:])[:galeraNameLimit]
}

func galera(request requests.NodeConfigure) bool {
	return request.Clustered && !request.ReadOnly
}

func readOnly(request requests.NodeConfigure) bool {
	return request.ReadOnly || galera(request)
}

func serverIDOf(request requests.NodeConfigure) string {
	if !galera(request) {
		return strconv.FormatInt(family.ServerIDOfAddress(request.Ip), 10)
	}

	return strconv.FormatInt(int64(crc32.ChecksumIEEE([]byte(request.DatabaseId))%(1<<31))|1, 10)
}

func gtidDomainOf(request requests.NodeConfigure) string {
	for _, member := range request.Members {
		if member.Ip == request.Ip {
			return strconv.FormatInt(member.Position+domainOffset, 10)
		}
	}

	return strconv.Itoa(domainOffset)
}

func clusterAddressOf(members []dtos.Member) string {
	addresses := []string{}
	for _, member := range members {
		if member.Voting {
			addresses = append(addresses, member.Ip)
		}
	}

	return "gcomm://" + strings.Join(addresses, ",")
}

func myCnfOf(root string, line string, request requests.NodeConfigure) string {
	tls := root + "/tls/"
	lines := []string{
		"[mysqld]",
		"datadir=" + root + "/data",
		"socket=" + socketPath,
		"pid-file=" + root + "/run/mariadbd.pid",
		"port=" + strconv.Itoa(clientPort),
		"bind-address=*",
		"server_id=" + serverIDOf(request),
		"gtid_domain_id=" + gtidDomainOf(request),
		"log_bin=binlog",
		"log_slave_updates=ON",
		"binlog_format=ROW",
		"binlog_expire_logs_seconds=604800",
		"ssl_ca=" + tls + "ca.crt",
		"ssl_cert=" + tls + "server.crt",
		"ssl_key=" + tls + "server.key",
		"tls_version=TLSv1.2,TLSv1.3",
		"plugin_load_add=auth_ed25519",
		"performance_schema=ON",
		"performance_schema_digests_size=10000",
	}
	if parsecOffered(line) {
		lines = append(lines, "plugin_load_add=auth_parsec")
	}
	if galera(request) {
		lines = append(lines,
			"wsrep_on=ON",
			"wsrep_provider="+galeraProvider,
			"wsrep_cluster_name="+clusterNameOf(request.DatabaseId),
			"wsrep_cluster_address="+clusterAddressOf(request.Members),
			"wsrep_node_address="+request.Ip,
			"wsrep_node_name="+request.Ip,
			"wsrep_node_incoming_address="+request.Ip+":"+strconv.Itoa(clientPort),
			"wsrep_sst_method=mariabackup",
			"wsrep_sst_auth=databasify_sst:"+family.ValueOr(request.Secrets.Replication),
			"wsrep_gtid_mode=ON",
			"wsrep_gtid_domain_id=1",
			`wsrep_provider_options="socket.ssl_key=`+tls+`server.key;socket.ssl_cert=`+tls+`server.crt;socket.ssl_ca=`+tls+`ca.crt;gcache.size=1G"`,
			"innodb_autoinc_lock_mode=2",
			"innodb_force_primary_key=ON",
		)
	}
	if readOnly(request) {
		lines = append(lines, "read_only=ON")
	}
	for _, key := range slices.Sorted(maps.Keys(request.Settings)) {
		lines = append(lines, key+"="+request.Settings[key])
	}
	if galera(request) {
		lines = append(lines, "", "[sst]", "ssl-mode=VERIFY_CA")
	}

	return strings.Join(lines, "\n") + "\n"
}

func manifestOf(root string, request requests.NodeConfigure, newCluster bool) ([]byte, error) {
	argv := []string{mariadbdBinary, "--defaults-file=" + root + "/" + myCnf}
	if newCluster {
		argv = append(argv, "--wsrep-new-cluster")
	}
	server := manifest.Process{
		Name:        serverUnit,
		User:        engineUser,
		Argv:        argv,
		Restart:     manifest.RestartAlways,
		RestartSec:  manifest.Duration(restartPause),
		StopSignal:  "TERM",
		StopTimeout: manifest.Duration(stopTimeout),
		Enabled:     true,
	}

	streams, schedules := family.ArchiveManifest(request, archiveUnits, serverUnit)
	processes := append([]manifest.Process{server}, streams...)
	if request.Clustered {
		processes = append(processes, manifest.Process{
			Name: watchUnit, Argv: []string{family.WrapperPath, "engine-task", "cluster-watch"}, After: []string{serverUnit},
			Restart: manifest.RestartAlways, RestartSec: manifest.Duration(restartPause), StopSignal: "TERM", StopTimeout: manifest.Duration(10 * time.Second), Enabled: true,
		})
	}

	return json.MarshalIndent(manifest.Manifest{Processes: processes, Schedules: schedules}, "", "  ")
}
