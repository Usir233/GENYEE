package tools

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type scriptedRowsBehavior struct {
	columns    []string
	rows       [][]driver.Value
	repeatRows int
	iterErr    error
	iterAfter  int
	closeErr   error
}

type scriptedSQLDriver struct {
	mu       sync.Mutex
	sessions map[string]*scriptedRowsBehavior
}

var (
	scriptedDriverOnce sync.Once
	scriptedDriverInst = &scriptedSQLDriver{
		sessions: make(map[string]*scriptedRowsBehavior),
	}
	scriptedDSNSeq atomic.Uint64
)

func openScriptedSQLDB(t *testing.T, behavior *scriptedRowsBehavior) *sql.DB {
	t.Helper()
	scriptedDriverOnce.Do(func() {
		sql.Register("genyee_scripted_rows", scriptedDriverInst)
	})
	dsn := fmt.Sprintf("dsn-%d", scriptedDSNSeq.Add(1))
	scriptedDriverInst.mu.Lock()
	scriptedDriverInst.sessions[dsn] = behavior
	scriptedDriverInst.mu.Unlock()
	t.Cleanup(func() {
		scriptedDriverInst.mu.Lock()
		delete(scriptedDriverInst.sessions, dsn)
		scriptedDriverInst.mu.Unlock()
	})
	db, err := sql.Open("genyee_scripted_rows", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func (d *scriptedSQLDriver) Open(name string) (driver.Conn, error) {
	d.mu.Lock()
	b := d.sessions[name]
	d.mu.Unlock()
	if b == nil {
		return nil, errors.New("unknown scripted dsn")
	}
	return &scriptedConn{behavior: b}, nil
}

type scriptedConn struct {
	behavior *scriptedRowsBehavior
}

func (c *scriptedConn) Prepare(query string) (driver.Stmt, error) {
	return &scriptedStmt{behavior: c.behavior, query: query}, nil
}

func (c *scriptedConn) Close() error              { return nil }
func (c *scriptedConn) Begin() (driver.Tx, error) { return nil, errors.New("tx not supported") }

func (c *scriptedConn) ExecContext(
	_ context.Context, _ string, _ []driver.NamedValue,
) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (c *scriptedConn) QueryContext(
	_ context.Context, _ string, _ []driver.NamedValue,
) (driver.Rows, error) {
	return &scriptedRows{behavior: c.behavior}, nil
}

type scriptedStmt struct {
	behavior *scriptedRowsBehavior
	query    string
}

func (s *scriptedStmt) Close() error  { return nil }
func (s *scriptedStmt) NumInput() int { return -1 }

func (s *scriptedStmt) Exec(_ []driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (s *scriptedStmt) Query(_ []driver.Value) (driver.Rows, error) {
	return &scriptedRows{behavior: s.behavior}, nil
}

type scriptedRows struct {
	behavior *scriptedRowsBehavior
	idx      int
}

func (r *scriptedRows) Columns() []string { return r.behavior.columns }

func (r *scriptedRows) Close() error { return r.behavior.closeErr }

func (r *scriptedRows) Next(dest []driver.Value) error {
	if r.behavior.iterErr != nil && r.idx >= r.behavior.iterAfter {
		return r.behavior.iterErr
	}
	if r.behavior.repeatRows > 0 && len(r.behavior.rows) > 0 {
		if r.idx >= r.behavior.repeatRows {
			return io.EOF
		}
		copy(dest, r.behavior.rows[r.idx%len(r.behavior.rows)])
		r.idx++
		return nil
	}
	if r.idx >= len(r.behavior.rows) {
		return io.EOF
	}
	copy(dest, r.behavior.rows[r.idx])
	r.idx++
	return nil
}

func openScriptedGormDB(t *testing.T, behavior *scriptedRowsBehavior) *gorm.DB {
	t.Helper()
	sqlDB := openScriptedSQLDB(t, behavior)
	gdb, err := gorm.Open(
		postgres.New(postgres.Config{Conn: sqlDB, PreferSimpleProtocol: true}),
		&gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)},
	)
	require.NoError(t, err)
	return gdb
}

func TestDatabaseQueryToolFailsOnMidCursorAndCloseErrors(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(42))
	targets := types.SearchTargets{{
		KnowledgeBaseID: "kb-1",
		TenantID:        42,
		Type:            types.SearchTargetTypeKnowledgeBase,
	}}
	args := json.RawMessage(`{"sql":"SELECT id, title FROM knowledges"}`)

	t.Run("mid_cursor_rows_err_fails_closed", func(t *testing.T) {
		iterBoom := errors.New("cursor network reset after row 1")
		gdb := openScriptedGormDB(t, &scriptedRowsBehavior{
			columns:   []string{"id", "title"},
			rows:      [][]driver.Value{{"doc-1", "First"}, {"doc-2", "Second"}},
			iterErr:   iterBoom,
			iterAfter: 1,
		})
		tool := NewDatabaseQueryTool(gdb, targets)
		res, err := tool.Execute(ctx, args)
		require.ErrorIs(t, err, iterBoom)
		require.NotNil(t, res)
		require.False(t, res.Success)
		require.Contains(t, res.Error, "Error iterating rows")
	})

	t.Run("rows_close_err_on_early_break_fails_closed", func(t *testing.T) {
		closeBoom := errors.New("wire trailer checksum failure on close")
		gdb := openScriptedGormDB(t, &scriptedRowsBehavior{
			columns:    []string{"id", "title"},
			rows:       [][]driver.Value{{"doc-1", "First"}},
			repeatRows: maxDatabaseQueryResultRows + 1,
			closeErr:   closeBoom,
		})
		tool := NewDatabaseQueryTool(gdb, targets)
		res, err := tool.Execute(ctx, args)
		require.ErrorIs(t, err, closeBoom)
		require.NotNil(t, res)
		require.False(t, res.Success)
		require.Contains(t, res.Error, "Error closing rows")
	})
}

func TestDataAnalysisExecuteSingleQueryFailsOnMidCursorAndCloseErrors(t *testing.T) {
	ctx := context.Background()

	t.Run("mid_cursor_rows_err", func(t *testing.T) {
		iterBoom := errors.New("duckdb cursor interrupted")
		sqlDB := openScriptedSQLDB(t, &scriptedRowsBehavior{
			columns:   []string{"col1"},
			rows:      [][]driver.Value{{"v1"}, {"v2"}},
			iterErr:   iterBoom,
			iterAfter: 1,
		})
		tool := &DataAnalysisTool{db: sqlDB, sessionID: "s-iter"}
		rows, err := tool.executeSingleQuery(ctx, "SELECT col1 FROM dataset", "k_1_abc")
		require.ErrorIs(t, err, iterBoom)
		require.Nil(t, rows)
	})

	t.Run("rows_close_err_on_early_break", func(t *testing.T) {
		closeBoom := errors.New("duckdb cursor close failure")
		sqlDB := openScriptedSQLDB(t, &scriptedRowsBehavior{
			columns:    []string{"col1"},
			rows:       [][]driver.Value{{"v1"}},
			repeatRows: maxDataAnalysisResultRows + 1,
			closeErr:   closeBoom,
		})
		tool := &DataAnalysisTool{db: sqlDB, sessionID: "s-close"}
		rows, err := tool.executeSingleQuery(ctx, "SELECT col1 FROM dataset", "k_1_abc")
		require.ErrorIs(t, err, closeBoom)
		require.Nil(t, rows)
	})
}

type trackingReadCloser struct {
	io.Reader
	closed atomic.Bool
}

func (r *trackingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestDataAnalysisMaterializeEnforcesSizeCeilingAndCleansUp(t *testing.T) {
	origMax := maxDataAnalysisMaterializeBytes
	maxDataAnalysisMaterializeBytes = 1024
	t.Cleanup(func() { maxDataAnalysisMaterializeBytes = origMax })

	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)

	rc := &trackingReadCloser{Reader: bytes.NewReader(bytes.Repeat([]byte("x"), 2048))}
	fs := &fakeFileService{
		readers: map[string]func() (io.ReadCloser, error){
			"tenants/1/huge.csv": func() (io.ReadCloser, error) {
				return rc, nil
			},
		},
	}
	tool := &DataAnalysisTool{fileService: fs, sessionID: "sess-cap"}
	k := &types.Knowledge{ID: "k-huge", FileType: "csv", FilePath: "tenants/1/huge.csv"}

	path, cleanup, err := tool.materializeKnowledgeFile(context.Background(), k)
	defer cleanup()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds maximum")
	require.Empty(t, path)
	require.True(t, rc.closed.Load(), "source reader must be closed on oversize abort")

	matches, globErr := filepath.Glob(filepath.Join(os.TempDir(), "genyee-data-analysis-*"))
	require.NoError(t, globErr)
	require.Empty(t, matches, "oversized temp file must be removed on failure")
}
