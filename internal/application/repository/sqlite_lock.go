package repository

import "gorm.io/gorm"

// acquireSQLiteWriteLock upgrades a DEFERRED SQLite transaction to an IMMEDIATE
// writer lock via a no-op UpdateColumn before check-then-write invariant reads,
// closing the read-to-write upgrade race where clause.Locking is a no-op.
func acquireSQLiteWriteLock(tx *gorm.DB, model any, column string, query string, args ...any) error {
	if tx == nil || tx.Name() != "sqlite" {
		return nil
	}
	return tx.Model(model).Where(query, args...).UpdateColumn(column, gorm.Expr(column)).Error
}
