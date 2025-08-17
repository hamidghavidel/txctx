package txctx

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQL(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	opts := &sql.TxOptions{
		Isolation: sql.LevelReadCommitted,
		ReadOnly:  false,
	}

	session := SQL(db, opts)

	assert.NotNil(t, session.db)
	assert.Nil(t, session.tx)
	assert.Equal(t, context.Background(), session.Context())
	assert.Equal(t, opts, session.txOptions)
}

func TestSQLSession_Begin(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()

	childSession, err := session.Begin(ctx)
	require.NoError(t, err)

	sqlSession := childSession.(SQLSession)
	assert.NotNil(t, sqlSession.tx)
	assert.NotEqual(t, ctx, sqlSession.Context()) // Context should be different

	// Verify transaction is in context
	tx := sqlSession.Context().Value(txKey{})
	assert.NotNil(t, tx)
	assert.IsType(t, (*sql.Tx)(nil), tx)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_Begin_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	expectedErr := errors.New("begin transaction failed")
	mock.ExpectBegin().WillReturnError(expectedErr)

	childSession, err := session.Begin(ctx)
	assert.Error(t, err)
	assert.Nil(t, childSession)
	assert.Equal(t, expectedErr, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_Transaction_Success(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		// Verify transaction is available in context
		tx := ctx.Value(txKey{})
		assert.NotNil(t, tx)
		assert.IsType(t, (*sql.Tx)(nil), tx)

		// Simulate some database operation
		performer := session.QueryPerformer(ctx)
		_, err := performer.ExecContext(ctx, "INSERT INTO users (email) VALUES (?)", "test@example.com")
		return err
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_Transaction_Rollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	expectedErr := errors.New("business logic error")

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		performer := session.QueryPerformer(ctx)
		_, err := performer.ExecContext(ctx, "INSERT INTO users (email) VALUES (?)", "test@example.com")
		if err != nil {
			return err
		}

		// Simulate business logic error
		return expectedErr
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_Transaction_BeginError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	expectedErr := errors.New("begin failed")
	mock.ExpectBegin().WillReturnError(expectedErr)

	err = session.Transaction(ctx, func(ctx context.Context) error {
		return nil
	})

	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_QueryPerformer_WithoutTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))

	performer := session.QueryPerformer(ctx)
	assert.Equal(t, db, performer)

	rows, err := performer.QueryContext(ctx, "SELECT 1")
	assert.NoError(t, err)
	assert.NotNil(t, rows)
	rows.Close()

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_QueryPerformer_WithTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT 1").WillReturnRows(sqlmock.NewRows([]string{"result"}).AddRow(1))
	mock.ExpectCommit()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		performer := session.QueryPerformer(ctx)

		// Should be the transaction, not the db
		tx := ctx.Value(txKey{})
		assert.Equal(t, tx, performer)
		assert.NotEqual(t, db, performer)

		rows, err := performer.QueryContext(ctx, "SELECT 1")
		if err != nil {
			return err
		}
		rows.Close()
		return nil
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_ManualCommit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	childSession, err := session.Begin(ctx)
	require.NoError(t, err)

	performer := childSession.QueryPerformer(childSession.Context())
	_, err = performer.ExecContext(childSession.Context(), "INSERT INTO users (email) VALUES (?)", "test@example.com")
	require.NoError(t, err)

	err = childSession.Commit()
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_ManualRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectRollback()

	childSession, err := session.Begin(ctx)
	require.NoError(t, err)

	performer := childSession.QueryPerformer(childSession.Context())
	_, err = performer.ExecContext(childSession.Context(), "INSERT INTO users (email) VALUES (?)", "test@example.com")
	require.NoError(t, err)

	err = childSession.Rollback()
	assert.NoError(t, err)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_NestedTransactions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	// Outer transaction
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))

	// Inner transaction
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO profiles").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit() // Inner commit

	mock.ExpectCommit() // Outer commit

	err = session.Transaction(ctx, func(outerCtx context.Context) error {
		// Outer transaction operation
		performer := session.QueryPerformer(outerCtx)
		_, err := performer.ExecContext(outerCtx, "INSERT INTO users (email) VALUES (?)", "test@example.com")
		if err != nil {
			return err
		}

		// Start nested transaction
		return session.Transaction(outerCtx, func(innerCtx context.Context) error {
			performer := session.QueryPerformer(innerCtx)
			_, err := performer.ExecContext(innerCtx, "INSERT INTO profiles (user_id) VALUES (?)", 1)
			return err
		})
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSQLSession_RollbackWithoutTransaction(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)

	// Should not panic or error when no transaction is active
	err = session.Rollback()
	assert.NoError(t, err)
}

func TestSQLSession_CommitWithoutTransaction(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)

	// Should not panic or error when no transaction is active
	err = session.Commit()
	assert.NoError(t, err)
}

func TestSQLSession_Failed(t *testing.T) {
	t.Run("healthy connection", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		defer db.Close()

		session := SQL(db, nil)

		mock.ExpectPing()

		failed := session.Failed()
		assert.False(t, failed)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("failed connection", func(t *testing.T) {
		db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
		require.NoError(t, err)
		defer db.Close()

		session := SQL(db, nil)

		mock.ExpectPing().WillReturnError(errors.New("connection failed"))

		failed := session.Failed()
		assert.True(t, failed)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// Integration test example - demonstrates real usage patterns
func TestSQLSession_IntegrationExample(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)

	// Mock a complete user creation workflow
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").
		WithArgs("john@example.com").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectQuery("SELECT id FROM users WHERE email = ?").
		WithArgs("john@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectExec("INSERT INTO profiles").
		WithArgs(1, "John Doe").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	// Simulate a service that creates user and profile in one transaction
	err = session.Transaction(context.Background(), func(ctx context.Context) error {
		performer := session.QueryPerformer(ctx)

		// Create user
		_, err := performer.ExecContext(ctx, "INSERT INTO users (email) VALUES (?)", "john@example.com")
		if err != nil {
			return err
		}

		// Get user ID
		var userID int64
		row := performer.QueryRowContext(ctx, "SELECT id FROM users WHERE email = ?", "john@example.com")
		if err := row.Scan(&userID); err != nil {
			return err
		}

		// Create profile
		_, err = performer.ExecContext(ctx, "INSERT INTO profiles (user_id, name) VALUES (?, ?)", userID, "John Doe")
		return err
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Benchmark tests
func BenchmarkSQLSession_Transaction(b *testing.B) {
	db, mock, err := sqlmock.New()
	require.NoError(b, err)
	defer db.Close()

	session := SQL(db, nil)

	for i := 0; i < b.N; i++ {
		mock.ExpectBegin()
		mock.ExpectExec("SELECT 1").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err = session.Transaction(context.Background(), func(ctx context.Context) error {
			performer := session.QueryPerformer(ctx)
			_, err := performer.ExecContext(ctx, "SELECT 1")
			return err
		})
		require.NoError(b, err)
	}
}

func BenchmarkSQLSession_QueryPerformer(b *testing.B) {
	db, _, err := sqlmock.New()
	require.NoError(b, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		performer := session.QueryPerformer(ctx)
		_ = performer
	}
}

// Example test showing how to test services using txctx
func TestUserService_Example(t *testing.T) {
	// Example service struct that would use txctx
	type UserService struct {
		session Session
	}

	type User struct {
		Email string
		Name  string
	}

	createUserWithProfile := func(service *UserService, ctx context.Context, user User) error {
		return service.session.Transaction(ctx, func(ctx context.Context) error {
			performer := service.session.QueryPerformer(ctx)

			// Create user
			result, err := performer.ExecContext(ctx, "INSERT INTO users (email) VALUES (?)", user.Email)
			if err != nil {
				return err
			}

			userID, err := result.LastInsertId()
			if err != nil {
				return err
			}

			// Create profile
			_, err = performer.ExecContext(ctx, "INSERT INTO profiles (user_id, name) VALUES (?, ?)", userID, user.Name)
			return err
		})
	}

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	service := &UserService{session: session}

	user := User{
		Email: "test@example.com",
		Name:  "Test User",
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").
		WithArgs(user.Email).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO profiles").
		WithArgs(1, user.Name).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = createUserWithProfile(service, context.Background(), user)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Test transaction options are properly passed
func TestSQLSession_TransactionOptions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	opts := &sql.TxOptions{
		Isolation: sql.LevelSerializable,
		ReadOnly:  true,
	}

	session := SQL(db, opts)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectCommit()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		return nil
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Test context cancellation during transaction
func TestSQLSession_ContextCancellation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)

	ctx, cancel := context.WithCancel(context.Background())

	mock.ExpectBegin()
	mock.ExpectRollback()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		// Cancel context during transaction
		cancel()
		return ctx.Err()
	})

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Test multiple sequential operations in same transaction
func TestSQLSession_MultipleOperations(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO users").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE users SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectCommit()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		performer := session.QueryPerformer(ctx)

		// Multiple operations using same performer/transaction
		_, err := performer.ExecContext(ctx, "INSERT INTO users (email) VALUES (?)", "test@example.com")
		if err != nil {
			return err
		}

		_, err = performer.ExecContext(ctx, "UPDATE users SET verified = true WHERE email = ?", "test@example.com")
		if err != nil {
			return err
		}

		rows, err := performer.QueryContext(ctx, "SELECT COUNT(*) FROM users WHERE email = ?", "test@example.com")
		if err != nil {
			return err
		}
		defer rows.Close()

		return nil
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Test prepared statements work correctly
func TestSQLSession_PreparedStatements(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	session := SQL(db, nil)
	ctx := context.Background()

	mock.ExpectBegin()
	mock.ExpectPrepare("INSERT INTO users").ExpectExec().WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err = session.Transaction(ctx, func(ctx context.Context) error {
		performer := session.QueryPerformer(ctx)

		stmt, err := performer.PrepareContext(ctx, "INSERT INTO users (email) VALUES (?)")
		if err != nil {
			return err
		}
		defer stmt.Close()

		_, err = stmt.ExecContext(ctx, "test@example.com")
		return err
	})

	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
