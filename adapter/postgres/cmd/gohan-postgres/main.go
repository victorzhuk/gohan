package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/victorzhuk/gohan/adapter/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gohan-postgres:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn := flag.String("dsn", os.Getenv("DATABASE_URL"), "database URL")
	schema := flag.String("schema", postgres.DefaultSchema, "schema the migrations apply to")
	target := flag.Int64("target", 0, "migrate up to this version, 0 for head")
	flag.Parse()
	if *dsn == "" {
		return fmt.Errorf("no -dsn given and DATABASE_URL is empty")
	}
	pool, err := pgxpool.New(context.Background(), *dsn)
	if err != nil {
		return err
	}
	defer pool.Close()
	applied, err := postgres.Migrate(context.Background(), pool, postgres.MigrateOptions{Schema: *schema, TargetVersion: *target})
	if err != nil {
		return err
	}
	if isTTY(os.Stdout) {
		for _, v := range applied {
			fmt.Printf("applied version %d\n", v)
		}
		if len(applied) == 0 {
			fmt.Println("nothing to apply")
		}
		return nil
	}
	out := struct {
		Applied []int64 `json:"applied"`
	}{Applied: applied}
	if out.Applied == nil {
		out.Applied = []int64{}
	}
	return json.NewEncoder(os.Stdout).Encode(out)
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
