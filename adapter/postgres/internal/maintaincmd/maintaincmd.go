// Package maintaincmd implements the gohan-postgres maintain verb: one
// pass over the rolling partition window and retention.
package maintaincmd

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
	fs := flag.NewFlagSet("maintain", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dsn := fs.String("dsn", os.Getenv("DATABASE_URL"), "database URL")
	schema := fs.String("schema", postgres.DefaultSchema, "schema the tables live in")
	window := fs.Int("window", postgres.DefaultPartitionWindowDays, "rolling partition window in days")
	retention := fs.Duration("retention", 0, "drop partitions and reap runs older than this, 0 disables")
	reapBatch := fs.Int("reap-batch", 100, "runs deleted per reap batch")
	maxReapBatches := fs.Int("max-reap-batches", 10, "maximum reap batches per run")
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
	report, err := postgres.Maintain(ctx, pool, postgres.MaintainOptions{
		Schema:         *schema,
		WindowDays:     *window,
		Retention:      *retention,
		ReapBatchSize:  *reapBatch,
		MaxReapBatches: *maxReapBatches,
	})
	if err != nil {
		return err
	}
	if out, ok := stdout.(*os.File); ok && isTTY(out) {
		for _, name := range report.Created {
			fmt.Fprintf(out, "created partition %s\n", name)
		}
		for _, name := range report.Dropped {
			fmt.Fprintf(out, "dropped partition %s\n", name)
		}
		if report.Reaped > 0 {
			fmt.Fprintf(out, "reaped %d runs\n", report.Reaped)
		}
		if len(report.Created) == 0 && len(report.Dropped) == 0 && report.Reaped == 0 {
			fmt.Fprintln(out, "nothing to do")
		}
		return nil
	}
	out := struct {
		Created []string `json:"created"`
		Dropped []string `json:"dropped"`
		Reaped  int      `json:"reaped"`
	}{Created: report.Created, Dropped: report.Dropped, Reaped: report.Reaped}
	if out.Created == nil {
		out.Created = []string{}
	}
	if out.Dropped == nil {
		out.Dropped = []string{}
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
