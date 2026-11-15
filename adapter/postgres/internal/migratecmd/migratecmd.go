package migratecmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/adapter/postgres"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "database URL")
	schema := fs.String("schema", postgres.DefaultSchema, "schema the migrations apply to")
	target := fs.Int64("target", 0, "migrate up to this version, 0 for head")
	minReader := fs.Int64("min-reader", 0, "oldest gohan reader still serving this schema")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dsn == "" {
		return fmt.Errorf("no -dsn given and DATABASE_URL is empty")
	}
	pool, err := pgxpool.New(ctx, *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := postgres.Migrate(ctx, pool, postgres.MigrateOptions{Schema: *schema, TargetVersion: *target, MinReaderVersion: *minReader})
	if err != nil {
		return err
	}
	if out, ok := stdout.(*os.File); ok && isTTY(out) {
		for _, v := range applied {
			fmt.Fprintf(out, "applied version %d\n", v)
		}
		if len(applied) == 0 {
			fmt.Fprintln(out, "nothing to apply")
		}
		return nil
	}
	out := struct {
		Applied []int64 `json:"applied"`
	}{Applied: applied}
	if out.Applied == nil {
		out.Applied = []int64{}
	}
	return json.NewEncoder(stdout).Encode(out)
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
