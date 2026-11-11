package mariadb

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/databasifylabs/engine-sdk/requests"
)

func TestEd25519UsesPasswordFunction(t *testing.T) {
	cases := map[string]string{
		"ed25519":               "CREATE USER ?@'%' IDENTIFIED VIA ed25519 USING PASSWORD(?) REQUIRE SSL",
		"mysql_native_password": "CREATE USER ?@'%' IDENTIFIED BY ? REQUIRE SSL",
	}
	for encryption, want := range cases {
		sent := []string{}
		accounts := Engine{db: fakeDB{rows: map[string][][]string{}, sent: &sent}}.Accounts()
		if _, err := accounts.CreateUser(context.Background(), requests.UserCreate{Name: "app", Password: "pw", Encryption: &encryption}); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(sent, want) {
			t.Fatalf("%s sent %v", encryption, sent)
		}
	}
}

func TestMariadb118OffersParsec(t *testing.T) {
	newer := Engine{line: "11.8"}.Capabilities()
	older := Engine{line: "11.4"}.Capabilities()

	if !slices.Equal(newer.PasswordEncryptions, []string{"mysql_native_password", "ed25519", "parsec"}) || *newer.DefaultPasswordEncryption != "mysql_native_password" {
		t.Fatalf("11.8 = %+v", newer)
	}
	if !slices.Equal(older.PasswordEncryptions, []string{"mysql_native_password", "ed25519"}) {
		t.Fatalf("11.4 = %+v", older)
	}
	if identifiedWith("parsec") != "IDENTIFIED VIA parsec USING PASSWORD(?)" {
		t.Fatalf("parsec = %s", identifiedWith("parsec"))
	}
	if !strings.Contains(myCnfOf("/srv/mariadb", "11.8", galeraRequest()), "plugin_load_add=auth_parsec\n") || strings.Contains(myCnfOf("/srv/mariadb", "11.4", galeraRequest()), "parsec") {
		t.Fatal("only 11.8 loads the PARSEC plugin")
	}
}
