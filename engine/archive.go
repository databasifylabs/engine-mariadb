package mariadb

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	family "github.com/databasifylabs/engine-mysql-family"
	"github.com/databasifylabs/engine-sdk/contract"
	"github.com/databasifylabs/engine-sdk/enums"
	"github.com/databasifylabs/engine-sdk/requests"
	"github.com/databasifylabs/engine-sdk/responses"
)

const (
	agentCnf       = "conf/agent.cnf"
	backupTemp     = "backup/tmp"
	backupMeta     = "backup/meta"
	restoreDir     = "restore"
	archiveIDState = "state/archive-id"
	baseSuffix     = ".mbstream"
	backupInfo     = "mariadb_backup_info"
	backupBinary   = "/usr/bin/mariadb-backup"
	mbstreamBinary = "/usr/bin/mbstream"
	binlogBinary   = "/usr/bin/mariadb-binlog"
	clientBinary   = "/usr/bin/mariadb"
)

var archiveUnits = family.ArchiveUnits{Stream: "mariadb-binlog-stream", Ship: "mariadb-binlog-ship", Backup: "mariadb-backup"}

type archive struct{ Engine }

func (e Engine) Archive() contract.Archive { return archive{e} }

func (e Engine) archiver() family.Archiver {
	return family.Archiver{Root: e.root, DB: e.db, Supervisor: e.supervisor, Units: archiveUnits, BaseSuffix: baseSuffix}
}

func agentCnfOf(root string, password string) string {
	return "[client]\nuser=databasify_agent\npassword=" + password + "\nsocket=" + socketPath + "\n\n[mariadb-backup]\ndatadir=" + root + "/data\n"
}

func backupArgsOf(root string, galeraNode bool) []string {
	args := []string{"--defaults-file=" + root + "/" + agentCnf, "--backup", "--stream=mbstream", "--target-dir=" + root + "/" + backupTemp, "--extra-lsndir=" + root + "/" + backupMeta}
	if galeraNode {
		args = append(args, "--galera-info")
	}

	return args
}

func (e Engine) isWriter(ctx context.Context) bool {
	state, err := e.state(ctx)

	return err == nil && roleOf(state) == enums.NodeRolePrimary
}

func (a archive) Backup(ctx context.Context) error {
	settings, err := family.Storage(a.root)
	if err != nil || !a.isWriter(ctx) {
		return err
	}
	request, err := family.Saved(a.root)
	if err != nil {
		return err
	}
	for _, dir := range []string{backupTemp, backupMeta} {
		_ = os.RemoveAll(a.root.Path(dir))
		if err := os.MkdirAll(a.root.Path(dir), 0o700); err != nil {
			return err
		}
	}
	backup := exec.CommandContext(ctx, backupBinary, backupArgsOf(string(a.root), galera(request))...)

	return a.archiver().Upload(ctx, settings, backup, a.root.Path(backupMeta+"/"+backupInfo))
}

func (a archive) Stream(ctx context.Context) error {
	return family.StreamWhile(ctx, a.isWriter, a.streamOnce)
}

func (a archive) streamOnce(ctx context.Context) error {
	if err := os.MkdirAll(a.root.Path(family.BinlogDir), 0o700); err != nil {
		return err
	}
	first, err := a.archiver().FirstBinlog(ctx, a.db)
	if err != nil {
		return err
	}
	request, err := family.Saved(a.root)
	if err != nil {
		return err
	}
	copierID := family.ServerIDOfAddress(request.Ip) ^ 0x80000000
	output, err := a.run(ctx, binlogBinary, "--defaults-extra-file="+a.root.Path(agentCnf), "--read-from-remote-server", "--raw", "--stop-never",
		"--stop-never-slave-server-id="+strconv.FormatInt(copierID, 10), "--result-file="+a.root.Path(family.BinlogDir)+"/", first)
	if err != nil {
		return fmt.Errorf("%w: %s", err, family.LastLines(output, 3))
	}

	return nil
}

func (a archive) Ship(ctx context.Context) error {
	settings, err := family.Storage(a.root)
	if err != nil {
		return err
	}
	source, err := a.archiveID()
	if err != nil {
		return err
	}

	return a.archiver().Ship(ctx, settings, source)
}

func (e Engine) archiveID() (string, error) {
	if id := e.root.Read(archiveIDState); id != "" {
		return id, nil
	}
	random := make([]byte, 8)
	_, _ = rand.Read(random)
	id := hex.EncodeToString(random)
	_, err := e.root.Write(archiveIDState, id, 0o600, false)

	return id, err
}

func (a archive) Status(ctx context.Context, request requests.ArchiveStatus) (responses.ArchiveStatus, error) {
	return a.archiver().Status(ctx, request)
}

func (a archive) Restore(ctx context.Context, request requests.RestoreStart, report contract.Report) error {
	report("downloading")
	info, err := a.archiver().Download(ctx, request.Storage, request.Target, restoreDir, func(dir string) *exec.Cmd {
		return exec.CommandContext(ctx, mbstreamBinary, "-x", "-C", dir)
	})
	if err != nil {
		return err
	}
	start, err := family.BackupGtids(info)
	if err != nil {
		return err
	}
	report("preparing")
	if _, err := a.run(ctx, backupBinary, "--prepare", "--target-dir="+a.root.Path(restoreDir+"/data")); err != nil {
		return err
	}
	report("starting")
	if err := a.replaceData(ctx); err != nil {
		return err
	}
	report("replaying")
	if err := a.replay(ctx, start, request.Target); err != nil {
		return err
	}

	return os.RemoveAll(a.root.Path(restoreDir))
}

func (a archive) replaceData(ctx context.Context) error {
	if err := a.supervisor.Do("stop", serverUnit); err != nil {
		return err
	}
	if err := os.RemoveAll(a.root.Path("data")); err != nil {
		return err
	}
	if err := a.root.Dir("data"); err != nil {
		return err
	}
	if _, err := a.run(ctx, backupBinary, "--move-back", "--datadir="+a.root.Path("data"), "--target-dir="+a.root.Path(restoreDir+"/data")); err != nil {
		return err
	}
	if _, err := a.run(ctx, "chown", "-R", "64999:64999", a.root.Path("data")); err != nil {
		return err
	}
	if err := os.Remove(a.root.Path(archiveIDState)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := a.supervisor.Do("start", serverUnit); err != nil {
		return err
	}

	return family.Until(ctx, readyTimeout, func() (bool, error) {
		_, _, err := a.db.Value(ctx, "SELECT 1")

		return err == nil, nil
	})
}

func replayArgsOf(start string, target time.Time, files []string) []string {
	return append([]string{"--skip-gtid-strict-mode", "--start-position=" + start, "--stop-datetime=" + target.UTC().Format(family.ReplayLayout)}, files...)
}

func (a archive) replay(ctx context.Context, start string, target time.Time) error {
	for _, files := range a.sourcesOldestFirst() {
		decode := exec.CommandContext(ctx, binlogBinary, replayArgsOf(start, target, files)...)
		decode.Env = append(os.Environ(), "TZ=UTC")
		apply := exec.CommandContext(ctx, clientBinary, "--defaults-extra-file="+adminCnf)
		if err := family.PipeInto(decode, func(stream io.Reader) error {
			apply.Stdin = stream
			output, err := apply.CombinedOutput()
			if err != nil {
				return fmt.Errorf("replaying binary logs: %w: %s", err, family.LastLines(string(output), 3))
			}

			return nil
		}); err != nil {
			return err
		}
		replayed, _, err := a.db.Value(ctx, "SELECT @@gtid_binlog_pos")
		if err != nil {
			return err
		}
		start = laterPositions(start, replayed)
	}

	return nil
}

func (a archive) sourcesOldestFirst() [][]string {
	sources, _ := os.ReadDir(a.root.Path(restoreDir + "/binlogs"))
	grouped := [][]string{}
	for _, source := range sources {
		files, _ := filepath.Glob(a.root.Path(restoreDir + "/binlogs/" + source.Name() + "/*"))
		if len(files) > 0 {
			slices.Sort(files)
			grouped = append(grouped, files)
		}
	}
	slices.SortFunc(grouped, func(a, b []string) int { return cmp.Compare(modifiedAt(a[0]), modifiedAt(b[0])) })

	return grouped
}

func modifiedAt(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	return info.ModTime().UnixNano()
}

func laterPositions(current string, replayed string) string {
	byDomain := map[string]string{}
	order := []string{}
	for _, position := range strings.Split(current+","+replayed, ",") {
		parts := strings.Split(strings.TrimSpace(position), "-")
		if len(parts) != 3 {
			continue
		}
		known, seen := byDomain[parts[0]]
		if !seen {
			order = append(order, parts[0])
		}
		if !seen || sequenceOf(position) > sequenceOf(known) {
			byDomain[parts[0]] = strings.TrimSpace(position)
		}
	}
	merged := make([]string, 0, len(order))
	for _, domain := range order {
		merged = append(merged, byDomain[domain])
	}

	return strings.Join(merged, ",")
}

func sequenceOf(position string) int64 {
	parts := strings.Split(position, "-")
	sequence, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)

	return sequence
}
