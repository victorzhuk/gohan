package postgres_test

import (
	"context"
	"testing"

	"github.com/victorzhuk/gohan/adapter/postgres"
)

func TestNewRejectsBadDSN(t *testing.T) {
	if _, err := postgres.New(context.Background(), "://not-a-dsn"); err == nil {
		t.Fatal("New with a broken DSN must fail")
	}
}
