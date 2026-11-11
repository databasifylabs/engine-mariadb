package mariadb

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const (
	sourceCnf = "conf/source.cnf"
	tailLimit = 64 << 10
)

var dumpedGtid = regexp.MustCompile(`gtid_slave_pos='([^']*)'`)

type dumpSource struct {
	host     string
	port     int64
	user     string
	password string
	tls      bool
}

func optionValue(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func sourceCnfOf(source dumpSource) string {
	lines := []string{
		"[client]",
		"host=" + source.host,
		"port=" + strconv.FormatInt(source.port, 10),
		"user=" + optionValue(source.user),
		"password=" + optionValue(source.password),
	}
	if source.tls {
		lines = append(lines, "ssl", "skip-ssl-verify-server-cert")
	} else {
		lines = append(lines, "skip-ssl")
	}

	return strings.Join(lines, "\n") + "\n"
}

func dumpArgsOf(cnf string, databases []string, withGtid bool) []string {
	args := []string{"--defaults-extra-file=" + cnf, "--single-transaction", "--routines", "--events", "--triggers"}
	if withGtid {
		args = append(args, "--gtid", "--master-data=2")
	}
	if len(databases) == 0 {
		return append(args, "--all-databases")
	}

	return append(append(args, "--databases"), databases...)
}

type tailCapture struct{ kept []byte }

func (t *tailCapture) Write(data []byte) (int, error) {
	t.kept = append(t.kept, data...)
	if excess := len(t.kept) - tailLimit; excess > 0 {
		t.kept = t.kept[excess:]
	}

	return len(data), nil
}

func (e Engine) copyFrom(ctx context.Context, source dumpSource, databases []string, withGtid bool) (string, error) {
	if _, err := e.root.Write(sourceCnf, sourceCnfOf(source), 0o600, false); err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(e.root.Path(sourceCnf)) }()
	dump := exec.CommandContext(ctx, "mariadb-dump", dumpArgsOf(e.root.Path(sourceCnf), databases, withGtid)...)
	load := exec.CommandContext(ctx, "mariadb", "--defaults-extra-file="+adminCnf)
	output, err := dump.StdoutPipe()
	if err != nil {
		return "", err
	}
	var tail tailCapture
	var dumpErrors, loadErrors bytes.Buffer
	dump.Stderr = &dumpErrors
	load.Stderr = &loadErrors
	load.Stdin = io.TeeReader(output, &tail)
	if err := dump.Start(); err != nil {
		return "", err
	}
	loadErr := load.Run()
	dumpErr := dump.Wait()
	if dumpErr != nil {
		return "", fmt.Errorf("mariadb-dump: %w: %s", dumpErr, strings.TrimSpace(dumpErrors.String()))
	}
	if loadErr != nil {
		return "", fmt.Errorf("mariadb: %w: %s", loadErr, strings.TrimSpace(loadErrors.String()))
	}
	match := dumpedGtid.FindStringSubmatch(string(tail.kept))
	if match == nil {
		return "", nil
	}

	return match[1], nil
}
