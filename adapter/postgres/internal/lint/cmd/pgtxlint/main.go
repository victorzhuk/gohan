package main

import (
	"fmt"
	"os"

	"github.com/victorzhuk/gohan/adapter/postgres/internal/lint"
)

const usage = "usage: pgtxlint <dir> [<dir>...] (append /... for a recursive walk)"

func main() {
	os.Exit(run())
}

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	findings, err := lint.Check(os.Args[1:]...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	for _, f := range findings {
		fmt.Printf("%s:%d:%d: pgx.Tx %q held across .%s\n", f.Pos.Filename, f.Pos.Line, f.Pos.Column, f.Binding, f.Boundary)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}
