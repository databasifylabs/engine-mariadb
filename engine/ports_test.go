package mariadb

import (
	"reflect"
	"testing"

	"github.com/databasifylabs/engine-sdk/dtos"
)

func TestCapabilitiesListTheMariadbPorts(t *testing.T) {
	want := dtos.Ports{Client: 25060, Peers: []int{4444, 4567, 4568}}
	if got := (Engine{line: "11.8"}).Capabilities().Ports; !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %+v", got)
	}
}
