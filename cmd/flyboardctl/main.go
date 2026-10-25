package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flyboard/internal/auth"
	"flyboard/internal/migrations"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "migrate":
		migrate()
	case "bootstrap-token":
		token()
	case "bootstrap":
		token()
	case "status":
		status()
	case "doctor":
		doctor()
	case "backup":
		backupCommand()
	case "restore":
		restoreCommand()
	case "emergency-whitelist-off":
		emergency()
	case "token":
		nodeToken()
	default:
		usage()
	}
}
func usage() {
	log.Fatal("usage: flyboardctl bootstrap|status|doctor|migrate up|status|backup create FILE|backup verify FILE|restore FILE|emergency-whitelist-off|token rotate NODE_ID")
}
func databaseURL() string {
	if p := os.Getenv("DATABASE_URL_FILE"); p != "" {
		b, e := os.ReadFile(p)
		if e != nil {
			log.Fatal(e)
		}
		return string(b)
	}
	return os.Getenv("DATABASE_URL")
}
func db() *sql.DB {
	d, e := sql.Open("pgx", databaseURL())
	if e != nil {
		log.Fatal(e)
	}
	return d
}
func migrate() {
	d := db()
	defer d.Close()
	sub := "up"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	if sub == "up" {
		if e := migrations.Up(context.Background(), d); e != nil {
			log.Fatal(e)
		}
		fmt.Println("migrations up")
		return
	}
	if sub == "status" {
		v, e := migrations.Status(context.Background(), d)
		if e != nil {
			log.Fatal(e)
		}
		for _, x := range v {
			fmt.Println("applied", x)
		}
		return
	}
	usage()
}
func token() {
	t := auth.Token()
	d := db()
	defer d.Close()
	h := sha256.Sum256([]byte(t))
	if _, e := d.Exec("insert into bootstrap_tokens(digest) values($1)", h[:]); e != nil {
		log.Fatal(e)
	}
	fmt.Println(t)
	fmt.Fprintln(os.Stderr, "digest", hex.EncodeToString(h[:8]))
}
func status() {
	d := db()
	defer d.Close()
	var admins, users, nodes int
	e := d.QueryRow(`select (select count(*) from admins),(select count(*) from users),(select count(*) from nodes)`).Scan(&admins, &users, &nodes)
	if e != nil {
		log.Fatal(e)
	}
	fmt.Printf("status=ok admins=%d users=%d nodes=%d\n", admins, users, nodes)
}
func doctor() {
	d := db()
	defer d.Close()
	ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
	defer c()
	if e := d.PingContext(ctx); e != nil {
		log.Fatal("postgres: ", e)
	}
	if _, e := migrations.Status(ctx, d); e != nil {
		log.Fatal("schema: ", e)
	}
	fmt.Println("postgres=ok schema=ok secrets=operator-managed")
}

type manifest struct {
	Version       int               `json:"version"`
	Created       string            `json:"created"`
	Files         map[string]string `json:"files"`
	Configuration map[string]string `json:"configuration"`
}

func backupCommand() {
	if len(os.Args) != 4 {
		usage()
	}
	switch os.Args[2] {
	case "create":
		createBackup(os.Args[3])
	case "verify":
		verifyBackup(os.Args[3])
		fmt.Println("backup verified")
	default:
		usage()
	}
}
func createBackup(dest string) {
	tmp, e := os.MkdirTemp("", "flyboard-backup-")
	if e != nil {
		log.Fatal(e)
	}
	defer os.RemoveAll(tmp)
	dump := filepath.Join(tmp, "database.dump")
	run("pg_dump", "--format=custom", "--file", dump, databaseURL())
	b, e := os.ReadFile(dump)
	if e != nil {
		log.Fatal(e)
	}
	m := manifest{Version: 2, Created: time.Now().UTC().Format(time.RFC3339),
		Files:         map[string]string{"database.dump": fmt.Sprintf("%x", sha256.Sum256(b))},
		Configuration: map[string]string{"format": "flyboard-portable", "schema": "postgresql"}}
	mb, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		log.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(tmp, "manifest.json"), mb, 0600); e != nil {
		log.Fatal(e)
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		log.Fatal(e)
	}
	tw := tar.NewWriter(f)
	for _, n := range []string{"manifest.json", "database.dump"} {
		b, e := os.ReadFile(filepath.Join(tmp, n))
		if e != nil {
			log.Fatal(e)
		}
		if e = tw.WriteHeader(&tar.Header{Name: n, Mode: 0600, Size: int64(len(b)), Typeflag: tar.TypeReg}); e != nil {
			log.Fatal(e)
		}
		if _, e = tw.Write(b); e != nil {
			log.Fatal(e)
		}
	}
	if e = tw.Close(); e != nil {
		log.Fatal(e)
	}
	if e = f.Close(); e != nil {
		log.Fatal(e)
	}
	verifyBackup(dest)
	fmt.Println(dest)
}
func extractVerified(file string) (string, func()) {
	tmp, e := os.MkdirTemp("", "flyboard-restore-")
	if e != nil {
		log.Fatal(e)
	}
	cleanup := func() { os.RemoveAll(tmp) }
	f, e := os.Open(file)
	if e != nil {
		cleanup()
		log.Fatal(e)
	}
	defer f.Close()
	tr := tar.NewReader(io.LimitReader(f, 1<<31))
	seen := map[string]bool{}
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			cleanup()
			log.Fatal(e)
		}
		if seen[h.Name] || h.Typeflag != tar.TypeReg || h.Size < 0 || h.Size > 1<<30 || (h.Name != "manifest.json" && h.Name != "database.dump") {
			cleanup()
			log.Fatal("invalid archive")
		}
		seen[h.Name] = true
		o, e := os.OpenFile(filepath.Join(tmp, h.Name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			cleanup()
			log.Fatal(e)
		}
		if _, e = io.Copy(o, io.LimitReader(tr, h.Size)); e != nil {
			o.Close()
			cleanup()
			log.Fatal(e)
		}
		o.Close()
	}
	if !seen["manifest.json"] || !seen["database.dump"] {
		cleanup()
		log.Fatal("incomplete archive")
	}
	var m manifest
	mb, e := os.ReadFile(filepath.Join(tmp, "manifest.json"))
	if e != nil || json.Unmarshal(mb, &m) != nil || m.Version != 2 {
		cleanup()
		log.Fatal("invalid manifest")
	}
	b, e := os.ReadFile(filepath.Join(tmp, "database.dump"))
	if e != nil || fmt.Sprintf("%x", sha256.Sum256(b)) != m.Files["database.dump"] {
		cleanup()
		log.Fatal("checksum mismatch")
	}
	return tmp, cleanup
}
func verifyBackup(file string) { _, cleanup := extractVerified(file); cleanup() }
func restoreCommand() {
	if len(os.Args) != 3 {
		usage()
	}
	if os.Getenv("FLYBOARD_RESTORE_CONFIRM") != "YES" {
		log.Fatal("restore is destructive; set FLYBOARD_RESTORE_CONFIRM=YES")
	}
	tmp, cleanup := extractVerified(os.Args[2])
	defer cleanup()
	pre := os.Args[2] + ".pre-restore-" + time.Now().UTC().Format("20060102T150405Z") + ".tar"
	createBackup(pre)
	run("pg_restore", "--clean", "--if-exists", "--no-owner", "--dbname", databaseURL(), filepath.Join(tmp, "database.dump"))
	fmt.Println("restore complete; pre-restore backup:", pre)
}
func emergency() {
	d := db()
	defer d.Close()
	_, e := d.Exec(`insert into settings(key,value) values('panel_whitelist_enabled','false') on conflict(key) do update set value='false',version=settings.version+1,updated_at=now()`)
	if e != nil {
		log.Fatal(e)
	}
	fmt.Println("panel whitelist disabled")
}
func nodeToken() {
	if len(os.Args) < 4 || os.Args[2] != "rotate" {
		usage()
	}
	t := auth.Token()
	d := db()
	defer d.Close()
	r, e := d.Exec(`update nodes set token_digest=$2,updated_at=now() where id=$1`, os.Args[3], auth.Digest(t))
	n, _ := r.RowsAffected()
	if e != nil || n != 1 {
		log.Fatal("node not found")
	}
	fmt.Println(t)
}
func run(name string, args ...string) {
	c := exec.Command(name, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if e := c.Run(); e != nil {
		log.Fatal(e)
	}
}
