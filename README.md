# txctx

Context-based database transaction management for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/hamidghavidel/txctx.svg)](https://pkg.go.dev/github.com/hamidghavidel/txctx)
[![Go Report Card](https://goreportcard.com/badge/github.com/hamidghavidel/txctx)](https://goreportcard.com/report/github.com/hamidghavidel/txctx)
[![codecov](https://codecov.io/github/hamidghavidel/txctx/graph/badge.svg?token=2LC8B9A7R5)](https://codecov.io/github/hamidghavidel/txctx)

## Overview

`txctx` provides a clean abstraction layer for database transaction management in Go applications. It automatically injects database transactions into context, allowing your business logic to seamlessly switch between transactional and non-transactional operations without coupling to the underlying database implementation.

## Features

- 🔄 **Context-based transaction injection** - Transactions are automatically available through context
- 🎯 **Clean abstraction** - Business logic doesn't depend directly on SQL transaction details
- 🔗 **Nested transaction support** - Create child sessions and nested transactions
- ⚡ **Automatic selection** - Automatically uses transaction or regular DB connection based on context
- 🧪 **Testable** - Easy to mock and test transaction behavior
- 🔧 **Flexible** - Support for custom transaction options

## Installation

```bash
go get github.com/hamidghavidel/txctx
```

## Quick Start

```go
package main

import (
    "context"
    "database/sql"
    "log"
    
    "github.com/hamidghavidel/txctx"
    _ "github.com/lib/pq"
)

func main() {
    db, err := sql.Open("postgres", "your-connection-string")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()
    
    // Create a root session
    session := txctx.SQL(db, nil)
    
    // Execute operations within a transaction
    err = session.Transaction(context.Background(), func(ctx context.Context) error {
        // Your business logic here
        return createUser(ctx, session, "john@example.com")
    })
    
    if err != nil {
        log.Printf("Transaction failed: %v", err)
    }
}

func createUser(ctx context.Context, session txctx.Session, email string) error {
    // QueryPerformer automatically uses the transaction from context
    performer := session.QueryPerformer(ctx)
    
    _, err := performer.ExecContext(ctx, 
        "INSERT INTO users (email, created_at) VALUES ($1, NOW())", 
        email,
    )
    return err
}
```

## Usage Patterns

### Automatic Transaction Management

The most common pattern is to use `Transaction()` which handles commit/rollback automatically:

```go
err := session.Transaction(ctx, func(ctx context.Context) error {
    // All operations within this function run in a transaction
    if err := createUser(ctx, session, user); err != nil {
        return err // Automatic rollback
    }
    
    if err := createProfile(ctx, session, profile); err != nil {
        return err // Automatic rollback
    }
    
    return nil // Automatic commit
})
```

### Manual Transaction Control

For more complex scenarios, you can manually control transactions:

```go
childSession, err := session.Begin(ctx)
if err != nil {
    return err
}

defer func() {
    if r := recover(); r != nil {
        childSession.Rollback()
        panic(r)
    }
}()

// Your operations here
if err := someOperation(childSession.Context(), childSession); err != nil {
    childSession.Rollback()
    return err
}

return childSession.Commit()
```

### Nested Transactions

Create nested sessions for complex workflows:

```go
err := session.Transaction(ctx, func(ctx context.Context) error {
    // Outer transaction
    
    nestedSession, err := session.Begin(ctx)
    if err != nil {
        return err
    }
    defer nestedSession.Rollback() // Safety net
    
    // Nested transaction operations
    if err := nestedOperation(nestedSession.Context(), nestedSession); err != nil {
        return err // Outer transaction will also rollback
    }
    
    return nestedSession.Commit()
})
```

### Service Layer Integration

Perfect for service layer architecture:

```go
type UserService struct {
    session txctx.Session
}

func (s *UserService) CreateUserWithProfile(ctx context.Context, user User, profile Profile) error {
    return s.session.Transaction(ctx, func(ctx context.Context) error {
        userID, err := s.createUser(ctx, user)
        if err != nil {
            return err
        }
        
        profile.UserID = userID
        return s.createProfile(ctx, profile)
    })
}

func (s *UserService) createUser(ctx context.Context, user User) (int64, error) {
    performer := s.session.QueryPerformer(ctx)
    // performer automatically uses transaction if available
    result, err := performer.ExecContext(ctx, 
        "INSERT INTO users (email) VALUES ($1)", user.Email)
    if err != nil {
        return 0, err
    }
    return result.LastInsertId()
}
```

## API Reference

### Session Interface

```go
type Session interface {
    Begin(ctx context.Context) (Session, error)
    Transaction(ctx context.Context, f func(context.Context) error) error
    Rollback() error
    Commit() error
    Context() context.Context
    QueryPerformer(ctx context.Context) Performer
}
```

### Key Methods

- **`Begin(ctx)`** - Creates a new child session with an active transaction
- **`Transaction(ctx, func)`** - Executes function within a transaction (auto commit/rollback)
- **`QueryPerformer(ctx)`** - Returns the appropriate database connection (transaction or regular DB)
- **`Commit()`/`Rollback()`** - Manual transaction control

### Performer Interface

```go
type Performer interface {
    ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
    QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
    QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
    PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}
```

## Testing

txctx makes testing database operations straightforward:

```go
func TestUserService(t *testing.T) {
    db := setupTestDB(t)
    session := txctx.SQL(db, nil)
    
    service := &UserService{session: session}
    
    // Test with automatic rollback
    err := session.Transaction(context.Background(), func(ctx context.Context) error {
        user := User{Email: "test@example.com"}
        profile := Profile{Name: "Test User"}
        
        err := service.CreateUserWithProfile(ctx, user, profile)
        assert.NoError(t, err)
        
        // Verify data was created
        // ...
        
        return errors.New("rollback") // Force rollback for clean test
    })
    
    assert.Error(t, err) // Expected rollback error
    // Database is now clean for next test
}
```

## Transaction Options

You can specify custom transaction options:

```go
opts := &sql.TxOptions{
    Isolation: sql.LevelSerializable,
    ReadOnly:  false,
}

session := txctx.SQL(db, opts)
```

## Best Practices

1. **Always handle errors** from `Begin()`, `Commit()`, and `Rollback()`
2. **Use defer for cleanup** when manually managing transactions
3. **Prefer `Transaction()`** over manual transaction control for simple cases
4. **Keep transaction scope minimal** - don't hold transactions longer than necessary
5. **Use context cancellation** to respect timeouts and cancellation

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.