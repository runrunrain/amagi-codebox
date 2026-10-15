package v2rayn

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v2rayN data #1.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`CREATE TABLE SubItem (Id TEXT, Remarks TEXT, Url TEXT)`,
		`INSERT INTO SubItem VALUES ('group','Local subscription','https://example.com/?token=private-feed-token')`,
		`CREATE TABLE ProfileItem (IndexId TEXT, ConfigType INTEGER, Address TEXT, Port INTEGER, Password TEXT, Id TEXT, Subid TEXT, Remarks TEXT, Network TEXT, StreamSecurity TEXT, ProtoExtra TEXT, TransportExtra TEXT)`,
		`INSERT INTO ProfileItem VALUES ('1',5,'node.example',443,'c066503f-5bd3-4c00-92ef-56083b3422f3','obsolete','group','Modern VLESS','raw','','{"VlessEncryption":"none"}','{}')`,
		`INSERT INTO ProfileItem VALUES ('2',7,'hy.example',443,'private-node-password','','group','Modern HY2','','tls','{"SalamanderPass":"obfs-password"}','{}')`,
		`INSERT INTO ProfileItem VALUES ('3',5,'bad.example',443,'uuid','','group','Unsupported','xhttp','','{}','{}')`,
	} {
		if _, err = db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
func TestReadLocalDatabaseReadOnly(t *testing.T) {
	path := fixture(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	groups, err := Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].Total != 3 || len(groups[0].Nodes) != 2 || len(groups[0].Warnings) != 1 {
		t.Fatal("wrong group or node counts")
	}
	if groups[0].Nodes[0].Outbound["uuid"] != "c066503f-5bd3-4c00-92ef-56083b3422f3" {
		t.Fatal("legacy field overrode modern credentials")
	}
	if groups[0].Nodes[1].Outbound["obfs"].(map[string]any)["password"] != "obfs-password" {
		t.Fatal("Hysteria2 obfs was lost")
	}
	db, err := openReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("DELETE FROM SubItem"); err == nil {
		t.Fatal("read-only connection allowed a write")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("source database changed")
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err = Read(context.Background(), missing); err == nil {
		t.Fatal("missing DB accepted")
	}
	if _, err = os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("missing DB was created")
	}
}
func TestModernAndLegacyFields(t *testing.T) {
	for _, tc := range []struct {
		row      map[string]any
		protocol string
	}{
		{map[string]any{"ConfigType": 1, "Id": "legacy-uuid", "Security": "auto", "AlterId": 0, "Network": "tcp"}, "vmess"},
		{map[string]any{"ConfigType": 3, "Id": "legacy-password", "Security": "aes-128-gcm"}, "shadowsocks"},
		{map[string]any{"ConfigType": 4, "Password": "pass", "Username": "user"}, "socks"},
		{map[string]any{"ConfigType": 8, "Password": "pass", "Username": "uuid", "ProtoExtra": `{"CongestionControl":"bbr"}`}, "tuic"},
		{map[string]any{"ConfigType": 10, "Password": "pass", "Username": "user", "StreamSecurity": "tls"}, "http"},
	} {
		tc.row["Address"] = "node.example"
		tc.row["Port"] = 443
		n, err := convert(tc.row)
		if err != nil || n.Type != tc.protocol {
			t.Fatalf("protocol %s failed: %v", tc.protocol, err)
		}
	}
	for _, row := range []map[string]any{
		{"ConfigType": 101},
		{"ConfigType": 5, "ProtoExtra": "not JSON"},
		{"ConfigType": 5, "ProtoExtra": `{"VlessEncryption":"unsupported"}`},
		{"ConfigType": 5, "CertSha": "pinned-certificate"},
		{"ConfigType": 5, "PreSocksPort": 1080},
	} {
		row["Address"] = "node.example"
		row["Port"] = 443
		if _, err := convert(row); err == nil {
			t.Fatal("incompatible profile silently downgraded")
		}
	}
}
func TestLocalV2rayNCompatibility(t *testing.T) {
	path := os.Getenv("V2RAYN_TEST_DATABASE")
	if path == "" {
		t.Skip("set V2RAYN_TEST_DATABASE for a read-only local compatibility check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := Read(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	outbounds := []map[string]any{}
	skipped := 0
	for _, g := range groups {
		skipped += g.Total - len(g.Nodes)
		for _, n := range g.Nodes {
			o := map[string]any{}
			for k, v := range n.Outbound {
				o[k] = v
			}
			o["tag"] = fmt.Sprintf("local-node-%d", len(outbounds))
			outbounds = append(outbounds, o)
		}
	}
	if len(outbounds) == 0 {
		t.Fatal("no compatible local nodes")
	}
	t.Logf("groups=%d nodes=%d skipped=%d", len(groups), len(outbounds), skipped)
	if core := os.Getenv("SING_BOX_TEST_BINARY"); core != "" {
		config, err := json.Marshal(map[string]any{"outbounds": outbounds, "dns": map[string]any{"servers": []map[string]any{{"type": "local", "tag": "local"}}}, "route": map[string]any{"default_domain_resolver": "local"}})
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "private-config.json")
		if err = os.WriteFile(file, config, 0600); err != nil {
			t.Fatal(err)
		}
		checkCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if output, checkErr := exec.CommandContext(checkCtx, core, "check", "-c", file).CombinedOutput(); checkErr != nil {
			diagnostic := string(output)
			var redact func(any)
			redact = func(v any) {
				switch x := v.(type) {
				case map[string]any:
					for k, c := range x {
						if k == "password" || k == "uuid" || k == "username" || k == "server" || k == "public_key" || k == "short_id" {
							if s, ok := c.(string); ok && s != "" {
								diagnostic = strings.ReplaceAll(diagnostic, s, "[redacted]")
							}
						}
						redact(c)
					}
				case []any:
					for _, c := range x {
						redact(c)
					}
				}
			}
			for _, o := range outbounds {
				redact(o)
			}
			t.Fatalf("imported local nodes failed sing-box validation: %s", diagnostic)
		}
	}
}
