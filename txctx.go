package txctx

import (
	"context"
	"database/sql"
	"time"
)

type Performer interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// Session aims at facilitating business transactions while abstracting the underlying mechanism,
// be it a database transaction or another transaction mechanism. This allows services to execute
// multiple business use-cases and easily rollback changes in case of error, without creating a
// dependency on the database layer.
//
// Sessions should be constituted of a root session created with a "New"-type constructor and allow
// the creation of child sessions with `Begin()` and `Transaction()`. Nested transactions should be supported
// as well.
type Session interface {
	// Begin returns a new session with the given context and a started transaction.
	// Using the returned session should have no side effect on the parent session.
	// The underlying transaction mechanism is injected as a value into the new session's context.
	Begin(ctx context.Context) (Session, error)

	// Transaction executes a transaction. If the given function returns an error, the transaction
	// is rolled back. Otherwise, it is automatically committed before `Transaction()` returns.
	// The underlying transaction mechanism is injected into the context as a value.
	Transaction(ctx context.Context, f func(context.Context) error) error

	// Rollback the changes in the transaction. This action is final.
	Rollback() error

	// Commit the changes in the transaction. This action is final.
	Commit() error

	// Context returns the session's context. If it's the root session, `context.Background()` is returned.
	// If it's a child session started with `Begin()`, then the context will contain the associated
	// transaction mechanism as a value.
	Context() context.Context

	// QueryPerformer returns the underlying query performer.
	QueryPerformer(ctx context.Context) Performer
}

type txKey struct{}

// SQLSession is a session implementation using *sql.DB and *sql.Tx.
type SQLSession struct {
	db        *sql.DB
	tx        *sql.Tx
	ctx       context.Context
	txOptions *sql.TxOptions
}

// SQL creates a new root session for *sql.DB.
// The transaction options are optional.
func SQL(db *sql.DB, opt *sql.TxOptions) SQLSession {
	return SQLSession{
		db:        db,
		txOptions: opt,
		ctx:       context.Background(),
	}
}

// Begin returns a new session with the given context and a started DB transaction.
// The returned session has manual controls. Make sure a call to `Rollback()` or `Commit()`
// is executed before the session is expired (eligible for garbage collection).
// The SQL transaction associated with this session is injected as a value into the new session's context.
func (s SQLSession) Begin(ctx context.Context) (Session, error) {
	tx, err := s.db.BeginTx(ctx, s.txOptions)
	if err != nil {
		return nil, err
	}
	return SQLSession{
		db:        s.db,
		tx:        tx,
		txOptions: s.txOptions,
		ctx:       context.WithValue(ctx, txKey{}, tx),
	}, nil
}

// Rollback the changes in the transaction. This action is final.
func (s SQLSession) Rollback() error {
	if s.tx != nil {
		return s.tx.Rollback()
	}
	return nil
}

// Commit the changes in the transaction. This action is final.
func (s SQLSession) Commit() error {
	if s.tx != nil {
		return s.tx.Commit()
	}
	return nil
}

// Context returns the session's context. If it's the root session, `context.Background()`
// is returned. If it's a child session started with `Begin()`, then the context will contain
// the associated SQL transaction.
func (s SQLSession) Context() context.Context {
	return s.ctx
}

// Transaction executes a transaction. If the given function returns an error, the transaction
// is rolled back. Otherwise, it is automatically committed before `Transaction()` returns.
//
// The SQL transaction associated with this session is injected into the context as a value.
func (s SQLSession) Transaction(ctx context.Context, f func(context.Context) error) error {
	tx, err := s.db.BeginTx(ctx, s.txOptions)
	if err != nil {
		return err
	}
	c := context.WithValue(ctx, txKey{}, tx)
	err = f(c)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// QueryPerformer retrieves the SQL transaction from the context or SQL db.
func (s SQLSession) QueryPerformer(ctx context.Context) Performer {
	tx := ctx.Value(txKey{})
	if tx == nil {
		return s.db
	}
	return tx.(*sql.Tx)
}

func (s SQLSession) Failed() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := s.db.PingContext(ctx)
	return err != nil
}
