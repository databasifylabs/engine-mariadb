package main

import (
	"os"

	mariadb "github.com/databasifylabs/engine-mariadb/engine"
	sdk "github.com/databasifylabs/engine-sdk"
)

func main() {
	os.Exit(sdk.Run(mariadb.New()))
}
