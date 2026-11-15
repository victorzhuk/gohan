package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/victorzhuk/gohan/adapter/postgres/internal/maintaincmd"
	"github.com/victorzhuk/gohan/adapter/postgres/internal/migratecmd"
)

var verbs = map[string]func(context.Context, []string, io.Writer, io.Writer) error{
	"migrate":  migratecmd.Run,
	"maintain": maintaincmd.Run,
}

func main() {
	run, ok := verbs[verb()]
	if !ok {
		usage(os.Stderr)
		os.Exit(2)
	}
	if err := run(context.Background(), os.Args[2:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "gohan-postgres:", err)
		os.Exit(1)
	}
}

func verb() string {
	if len(os.Args) < 2 {
		return ""
	}
	return os.Args[1]
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: gohan-postgres migrate -dsn <url> [-schema <name>] [-target <version>] [-min-reader <version>]")
	fmt.Fprintln(w, "usage: gohan-postgres maintain -dsn <url> [-schema <name>] [-window <days>] [-retention <duration>] [-reap-batch <n>] [-max-reap-batches <n>]")
}
