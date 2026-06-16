package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"

	"github.com/amoorihesham/eco-api/internal/platform/db"
)

func TestRunInTxCommitsOnSuccess(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock: %v", err)
	}
	defer mock.Close()

	mock.ExpectBegin()
	mock.ExpectCommit()

	if err := db.RunInTx(context.Background(), mock, func(_ pgx.Tx) error { return nil }); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}

func TestRunInTxRollsBackOnError(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("new mock: %v", err)
	}
	defer mock.Close()

	want := errors.New("boom")
	mock.ExpectBegin()
	mock.ExpectRollback()

	if err := db.RunInTx(context.Background(), mock, func(_ pgx.Tx) error { return want }); !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("expectations: %v", err)
	}
}
