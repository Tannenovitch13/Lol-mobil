package lcu

import "testing"

func TestParseLockfile(t *testing.T) {
	c, err := ParseLockfile("LeagueClient:12345:54321:s3cr3t-T0ken:https\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Port != 54321 || c.Password != "s3cr3t-T0ken" || c.Protocol != "https" {
		t.Fatalf("unexpected credentials: %+v", c)
	}
	if c.URL() != "https://127.0.0.1:54321" {
		t.Fatalf("unexpected URL %s", c.URL())
	}
	if _, err := ParseLockfile("garbage"); err == nil {
		t.Fatal("garbage lockfile accepted")
	}
}

func TestParseCommandLine(t *testing.T) {
	cmd := `"C:/Riot Games/League of Legends/LeagueClientUx.exe" "--riotclient-auth-token=abc" "--riotclient-app-port=1111" "--remoting-auth-token=Xy_z-123" "--app-port=62000" "--install-directory=C:\Riot Games\League of Legends" "--app-name=LeagueClient"`
	c, install, ok := ParseCommandLine(cmd)
	if !ok {
		t.Fatal("command line not parsed")
	}
	if c.Port != 62000 || c.Password != "Xy_z-123" {
		t.Fatalf("unexpected credentials: %+v", c)
	}
	if install != `C:\Riot Games\League of Legends` {
		t.Fatalf("unexpected install dir %q", install)
	}
	if _, _, ok := ParseCommandLine("notepad.exe"); ok {
		t.Fatal("unrelated command line accepted")
	}
}

func TestOverrideURL(t *testing.T) {
	c := Credentials{BaseURL: "http://127.0.0.1:29999/"}
	if c.URL() != "http://127.0.0.1:29999" {
		t.Fatalf("unexpected URL %s", c.URL())
	}
}
