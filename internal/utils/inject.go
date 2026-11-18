// Package utils provides shared security, SQL validation, cryptographic, HTTP,
// and task-identifier helpers.
package utils

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// This file provides comprehensive SQL validation and security features

/*
Example Usage:

1. Basic SQL parsing:
   result := ParseSQL("SELECT * FROM users WHERE age > 18")
   fmt.Printf("Tables: %v\n", result.TableNames)
   fmt.Printf("WHERE fields: %v\n", result.WhereFields)

2. Simple validation with table whitelist:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE age > 18",
       WithAllowedTables("users", "orders"),
   )
   if !validation.Valid {
       for _, err := range validation.Errors {
           fmt.Printf("Error: %s - %s\n", err.Type, err.Message)
       }
   }

3. Check for SQL injection risks:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE id = 1 OR 1=1",
       WithInjectionRiskCheck(),
   )
   if !validation.Valid {
       fmt.Println("SQL injection risk detected!")
   }

4. Comprehensive security validation:
   parseResult, validation := ValidateSQL(
       "SELECT * FROM users WHERE age > 18",
       WithInputValidation(6, 4096),
       WithSelectOnly(),
       WithSingleStatement(),
       WithAllowedTables("users", "orders"),
       WithDefaultSafeFunctions(),
       WithNoSubqueries(),
       WithNoCTEs(),
       WithNoSystemColumns(),
   )

5. Use security defaults (recommended for production):
   parseResult, validation := ValidateSQL(
       "SELECT * FROM knowledge_bases WHERE name LIKE '%test%'",
       WithSecurityDefaults(tenantID),
   )

6. Validate and secure SQL with tenant isolation:
   securedSQL, validation, err := ValidateAndSecureSQL(
       "SELECT * FROM knowledge_bases",
       WithSecurityDefaults(tenantID),
   )
   // securedSQL will have tenant_id automatically injected:
   // "SELECT * FROM knowledge_bases WHERE knowledge_bases.tenant_id = 123"

7. Custom validation options:
   parseResult, validation := ValidateSQL(
       "SELECT COUNT(*), AVG(score) FROM sessions",
       WithAllowedTables("sessions", "messages"),
       WithAllowedFunctions("count", "avg", "sum"),
       WithTenantIsolation(tenantID, "sessions"),
   )
*/

// SQLParseResult represents the parsed components of a SELECT SQL statement
type SQLParseResult struct {
	IsSelect     bool     `json:"is_select"`             // Whether the SQL is a SELECT statement
	TableNames   []string `json:"table_names"`           // List of table names in FROM clause
	SelectFields []string `json:"select_fields"`         // List of fields in SELECT clause
	WhereFields  []string `json:"where_fields"`          // List of fields in WHERE clause
	WhereClause  string   `json:"where_clause"`          // Complete WHERE clause text
	OriginalSQL  string   `json:"original_sql"`          // Original SQL statement
	ParseError   string   `json:"parse_error,omitempty"` // Error message if parsing failed
}

// SQLValidationError represents a validation error
type SQLValidationError struct {
	Type    string `json:"type"`    // Error type: "table_not_allowed", "sql_injection_risk", etc.
	Message string `json:"message"` // Error message
	Details string `json:"details"` // Additional details
}

// SQLValidationResult represents the result of SQL validation
type SQLValidationResult struct {
	Valid  bool                 `json:"valid"`  // Whether the SQL passed validation
	Errors []SQLValidationError `json:"errors"` // List of validation errors
}

// SQLValidationOption is a function that configures SQL validation
type SQLValidationOption func(*sqlValidator)

// sqlValidator holds validation configuration
type sqlValidator struct {
	// Basic validation
	checkInputValidation bool
	minLength            int
	maxLength            int

	// Statement type validation
	checkSelectOnly      bool
	checkSingleStatement bool

	// Table validation
	allowedTables     map[string]bool
	checkTableNames   bool
	safeTableColumns  map[string]map[string]bool
	checkTableColumns bool

	// Function validation
	allowedFunctions   map[string]bool
	checkFunctionNames bool

	// Security checks
	checkInjectionRisk  bool
	checkSubqueries     bool
	checkCTEs           bool
	checkSystemColumns  bool
	checkSchemaAccess   bool
	checkDangerousFuncs bool

	// Tenant isolation
	enableTenantInjection bool
	tenantID              uint64
	tablesWithTenantID    map[string]bool

	// Soft delete filtering
	enableSoftDeleteInjection bool
	tablesWithDeletedAt       map[string]bool

	// Hidden knowledge base filtering (is_temporary = false)
	enableHiddenKBFilter bool

	// Enabled chunk filtering (is_enabled = true)
	enableChunkEnabledFilter bool

	// Search scope filtering (restrict to specific KBs and knowledges)
	enableSearchScopeFilter bool
	searchScopeKBIDs        []string
	searchScopeKnowledgeIDs []string
	searchScopes            []SearchScope
}

// SearchScope describes one allowed knowledge scope for SQL query injection.
// Empty KnowledgeIDs and TagIDs means the whole KB is in scope.
type SearchScope struct {
	// TenantID is the authorized owner of this KB. Zero uses the caller tenant
	// when tenant isolation is enabled. Scopes must be authorized by the caller.
	TenantID        uint64
	KnowledgeBaseID string
	KnowledgeIDs    []string
	TagIDs          []string
}

// ParseSQL parses a SQL statement using pg_query_go and extracts table names, select fields, and where fields
// This uses the PostgreSQL parser for accurate SQL parsing
func ParseSQL(sql string) *SQLParseResult {
	result := &SQLParseResult{
		OriginalSQL:  sql,
		TableNames:   make([]string, 0),
		SelectFields: make([]string, 0),
		WhereFields:  make([]string, 0),
	}

	// Parse the SQL using pg_query_go
	parseResult, err := pg_query.Parse(sql)
	if err != nil {
		result.IsSelect = false
		result.ParseError = fmt.Sprintf("Failed to parse SQL: %v", err)
		return result
	}

	// Check if it's a SELECT statement
	if len(parseResult.Stmts) == 0 {
		result.IsSelect = false
		result.ParseError = "No statements found in SQL"
		return result
	}

	// Get the first statement
	stmt := parseResult.Stmts[0]
	if stmt.Stmt == nil {
		result.IsSelect = false
		result.ParseError = "Invalid statement"
		return result
	}

	// Check if it's a SELECT statement
	selectStmt := stmt.Stmt.GetSelectStmt()
	if selectStmt == nil {
		result.IsSelect = false
		result.ParseError = "Not a SELECT statement"
		return result
	}
	result.IsSelect = true

	// Extract SELECT fields
	result.SelectFields = extractSelectFieldsFromPgQuery(selectStmt)

	// Extract table names from FROM clause
	result.TableNames = extractTableNamesFromPgQuery(selectStmt)

	// Extract WHERE clause fields and text
	whereFields, whereClause := extractWhereFromPgQuery(selectStmt, sql)
	result.WhereFields = whereFields
	result.WhereClause = whereClause

	return result
}

// extractSelectFieldsFromPgQuery extracts field names from SELECT clause using pg_query parse tree
func extractSelectFieldsFromPgQuery(selectStmt *pg_query.SelectStmt) []string {
	fields := make([]string, 0)
	fieldMap := make(map[string]bool) // Avoid duplicates

	if selectStmt.TargetList == nil {
		return fields
	}

	for _, target := range selectStmt.TargetList {
		resTarget := target.GetResTarget()
		if resTarget == nil {
			continue
		}

		// Extract column names from the target
		colNames := extractColumnNamesFromNode(resTarget.Val)
		for _, colName := range colNames {
			if colName != "" && !fieldMap[colName] {
				fieldMap[colName] = true
				fields = append(fields, colName)
			}
		}
	}

	return fields
}

// extractTableNamesFromPgQuery extracts table names from FROM clause using pg_query parse tree
func extractTableNamesFromPgQuery(selectStmt *pg_query.SelectStmt) []string {
	tables := make([]string, 0)
	tableMap := make(map[string]bool) // Avoid duplicates

	if selectStmt.FromClause == nil {
		return tables
	}

	for _, fromItem := range selectStmt.FromClause {
		tableNames := extractTableNamesFromNode(fromItem)
		for _, tableName := range tableNames {
			if tableName != "" && !tableMap[tableName] {
				tableMap[tableName] = true
				tables = append(tables, tableName)
			}
		}
	}

	return tables
}

// extractWhereFromPgQuery extracts WHERE clause fields and text using pg_query parse tree
func extractWhereFromPgQuery(selectStmt *pg_query.SelectStmt, originalSQL string) ([]string, string) {
	fields := make([]string, 0)
	fieldMap := make(map[string]bool) // Avoid duplicates
	whereClause := ""

	if selectStmt.WhereClause == nil {
		return fields, whereClause
	}

	// Extract WHERE clause text from original SQL
	whereClause = extractWhereClauseText(originalSQL)

	// Extract column names from WHERE clause
	colNames := extractColumnNamesFromNode(selectStmt.WhereClause)
	for _, colName := range colNames {
		if colName != "" && !fieldMap[colName] {
			fieldMap[colName] = true
			fields = append(fields, colName)
		}
	}

	return fields, whereClause
}

// extractColumnNamesFromNode recursively extracts column names from a parse tree node
func extractColumnNamesFromNode(node *pg_query.Node) []string {
	if node == nil {
		return nil
	}

	colNames := make([]string, 0)

	// Handle ColumnRef (column reference)
	if colRef := node.GetColumnRef(); colRef != nil {
		if len(colRef.Fields) > 0 {
			// Extract only the actual column name (the last part of table.column or schema.table.column)
			lastField := colRef.Fields[len(colRef.Fields)-1]
			if strNode := lastField.GetString_(); strNode != nil {
				if strNode.Sval != "*" { // Skip wildcard
					colNames = append(colNames, strNode.Sval)
				}
			}
		}
		return colNames
	}

	// Handle A_Expr (expression with operators)
	if aExpr := node.GetAExpr(); aExpr != nil {
		colNames = append(colNames, extractColumnNamesFromNode(aExpr.Lexpr)...)
		colNames = append(colNames, extractColumnNamesFromNode(aExpr.Rexpr)...)
		return colNames
	}

	// Handle BoolExpr (AND, OR, NOT)
	if boolExpr := node.GetBoolExpr(); boolExpr != nil {
		if boolExpr.Args != nil {
			for _, arg := range boolExpr.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		return colNames
	}

	// Handle FuncCall (function calls)
	if funcCall := node.GetFuncCall(); funcCall != nil {
		if funcCall.Args != nil {
			for _, arg := range funcCall.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		return colNames
	}

	// Handle ResTarget (result target in SELECT)
	if resTarget := node.GetResTarget(); resTarget != nil {
		colNames = append(colNames, extractColumnNamesFromNode(resTarget.Val)...)
		return colNames
	}

	// Handle SubLink (subquery)
	if subLink := node.GetSubLink(); subLink != nil {
		colNames = append(colNames, extractColumnNamesFromNode(subLink.Testexpr)...)
		return colNames
	}

	// Handle NullTest (IS NULL, IS NOT NULL)
	if nullTest := node.GetNullTest(); nullTest != nil {
		colNames = append(colNames, extractColumnNamesFromNode(nullTest.Arg)...)
		return colNames
	}

	// Handle CaseExpr (CASE WHEN)
	if caseExpr := node.GetCaseExpr(); caseExpr != nil {
		colNames = append(colNames, extractColumnNamesFromNode(caseExpr.Arg)...)
		if caseExpr.Args != nil {
			for _, arg := range caseExpr.Args {
				colNames = append(colNames, extractColumnNamesFromNode(arg)...)
			}
		}
		colNames = append(colNames, extractColumnNamesFromNode(caseExpr.Defresult)...)
		return colNames
	}

	// Handle CaseWhen (WHEN clause in CASE)
	if caseWhen := node.GetCaseWhen(); caseWhen != nil {
		colNames = append(colNames, extractColumnNamesFromNode(caseWhen.Expr)...)
		colNames = append(colNames, extractColumnNamesFromNode(caseWhen.Result)...)
		return colNames
	}

	return colNames
}

// extractTableNamesFromNode recursively extracts table names from a parse tree node
func extractTableNamesFromNode(node *pg_query.Node) []string {
	if node == nil {
		return nil
	}

	tableNames := make([]string, 0)

	// Handle RangeVar (table reference)
	if rangeVar := node.GetRangeVar(); rangeVar != nil {
		if rangeVar.Relname != "" {
			tableNames = append(tableNames, rangeVar.Relname)
		}
		return tableNames
	}

	// Handle JoinExpr (JOIN)
	if joinExpr := node.GetJoinExpr(); joinExpr != nil {
		tableNames = append(tableNames, extractTableNamesFromNode(joinExpr.Larg)...)
		tableNames = append(tableNames, extractTableNamesFromNode(joinExpr.Rarg)...)
		return tableNames
	}

	// Handle RangeSubselect (subquery in FROM)
	if rangeSubselect := node.GetRangeSubselect(); rangeSubselect != nil {
		// We could recursively parse the subquery here if needed
		return tableNames
	}

	return tableNames
}

// extractWhereClauseText extracts the WHERE clause text from the original SQL
func extractWhereClauseText(sql string) string {
	lowerSQL := strings.ToLower(sql)
	wherePos := strings.Index(lowerSQL, "where")
	if wherePos == -1 {
		return ""
	}

	// Find the end of WHERE clause
	whereClauseEnd := len(sql)
	for _, keyword := range []string{"group by", "order by", "limit", "having", "union", "intersect", "except"} {
		if pos := strings.Index(lowerSQL[wherePos:], keyword); pos != -1 {
			actualPos := wherePos + pos
			if actualPos < whereClauseEnd {
				whereClauseEnd = actualPos
			}
		}
	}

	// Extract WHERE clause (skip "WHERE" keyword)
	whereClause := strings.TrimSpace(sql[wherePos+5 : whereClauseEnd])
	return whereClause
}

// WithAllowedTables creates a validation option that checks if table names are in the allowed list
func WithAllowedTables(tables ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkTableNames = true
		v.allowedTables = make(map[string]bool)
		for _, table := range tables {
			v.allowedTables[strings.ToLower(table)] = true
		}
	}
}

// WithInjectionRiskCheck creates a validation option that checks for SQL injection risks
func WithInjectionRiskCheck() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkInjectionRisk = true
	}
}

// WithInputValidation enables basic input validation (length, null bytes, etc.)
func WithInputValidation(minLen, maxLen int) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkInputValidation = true
		v.minLength = minLen
		v.maxLength = maxLen
	}
}

// WithSelectOnly ensures only SELECT statements are allowed
func WithSelectOnly() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSelectOnly = true
	}
}

// WithSingleStatement ensures only single statement is allowed (no multiple statements)
func WithSingleStatement() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSingleStatement = true
	}
}

// WithAllowedFunctions creates a validation option that checks if functions are in the allowed list
func WithAllowedFunctions(functions ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkFunctionNames = true
		v.allowedFunctions = make(map[string]bool)
		for _, fn := range functions {
			v.allowedFunctions[strings.ToLower(fn)] = true
		}
	}
}

// WithDefaultSafeFunctions enables a default set of safe SQL functions
func WithDefaultSafeFunctions() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkFunctionNames = true
		v.allowedFunctions = map[string]bool{
			// Aggregate functions
			"count":            true,
			"sum":              true,
			"avg":              true,
			"min":              true,
			"max":              true,
			"array_agg":        true,
			"string_agg":       true,
			"bool_and":         true,
			"bool_or":          true,
			"json_agg":         true,
			"jsonb_agg":        true,
			"json_object_agg":  true,
			"jsonb_object_agg": true,
			// Safe scalar functions
			"coalesce":          true,
			"nullif":            true,
			"greatest":          true,
			"least":             true,
			"abs":               true,
			"ceil":              true,
			"floor":             true,
			"round":             true,
			"length":            true,
			"lower":             true,
			"upper":             true,
			"trim":              true,
			"ltrim":             true,
			"rtrim":             true,
			"substring":         true,
			"concat":            true,
			"concat_ws":         true,
			"replace":           true,
			"left":              true,
			"right":             true,
			"now":               true,
			"current_date":      true,
			"current_timestamp": true,
			"date_trunc":        true,
			"extract":           true,
			"to_char":           true,
			"to_date":           true,
			"to_timestamp":      true,
			"date_part":         true,
			"age":               true,
		}
	}
}

// WithNoSubqueries blocks all subqueries
func WithNoSubqueries() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSubqueries = true
	}
}

// WithNoCTEs blocks Common Table Expressions (WITH clause)
func WithNoCTEs() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkCTEs = true
	}
}

// WithNoSystemColumns blocks access to PostgreSQL system columns
func WithNoSystemColumns() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSystemColumns = true
	}
}

// WithNoSchemaAccess blocks schema-qualified access (except public schema)
func WithNoSchemaAccess() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkSchemaAccess = true
	}
}

// WithNoDangerousFunctions blocks dangerous PostgreSQL functions
func WithNoDangerousFunctions() SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkDangerousFuncs = true
	}
}

// WithTenantIsolation enables automatic tenant_id injection for multi-tenant security
func WithTenantIsolation(tenantID uint64, tables ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.enableTenantInjection = true
		v.tenantID = tenantID
		v.tablesWithTenantID = make(map[string]bool)
		if len(tables) == 0 {
			// Default tables with tenant_id
			// SECURITY: All tables with tenant_id column must be listed here
			// to ensure proper tenant isolation and prevent cross-tenant data access
			v.tablesWithTenantID = map[string]bool{
				"knowledge_bases": true,
				"knowledges":      true,
				"chunks":          true,
			}
		} else {
			for _, table := range tables {
				v.tablesWithTenantID[strings.ToLower(table)] = true
			}
		}
	}
}

// WithSoftDeleteFilter enables automatic deleted_at IS NULL injection.
func WithSoftDeleteFilter(tables ...string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.enableSoftDeleteInjection = true
		v.tablesWithDeletedAt = make(map[string]bool)
		if len(tables) == 0 {
			// Default tables with soft-delete support.
			v.tablesWithDeletedAt = map[string]bool{
				"knowledge_bases": true,
				"knowledges":      true,
				"chunks":          true,
			}
		} else {
			for _, table := range tables {
				v.tablesWithDeletedAt[strings.ToLower(table)] = true
			}
		}
	}
}

// WithHiddenKBFilter excludes internal/temporary knowledge bases (is_temporary = true)
// from query results. These are system-managed KBs like __chat_history__ that should
// not be visible to end users.
func WithHiddenKBFilter() SQLValidationOption {
	return func(v *sqlValidator) {
		v.enableHiddenKBFilter = true
	}
}

// WithChunkEnabledFilter excludes disabled chunks from model-visible SQL
// queries. It is intentionally opt-in because administrative queries may need
// to inspect disabled rows.
func WithChunkEnabledFilter() SQLValidationOption {
	return func(v *sqlValidator) {
		v.enableChunkEnabledFilter = true
	}
}

// WithSearchScopeFilter restricts queries to the specified knowledge bases and
// (optionally) specific knowledge documents. For the knowledge_bases table it
// filters by id; for knowledges it filters by knowledge_base_id (and id when
// knowledgeIDs is non-empty); for chunks it filters by knowledge_base_id (and
// knowledge_id when knowledgeIDs is non-empty).
func WithSearchScopeFilter(kbIDs []string, knowledgeIDs []string) SQLValidationOption {
	return func(v *sqlValidator) {
		if len(kbIDs) > 0 {
			v.enableSearchScopeFilter = true
			v.searchScopeKBIDs = kbIDs
			v.searchScopeKnowledgeIDs = knowledgeIDs
		}
	}
}

// WithSearchScopes restricts queries using structured OR scopes. Each scope can
// represent a full KB, specific documents, or a tag-constrained KB. Nonzero
// TenantID values grant access to that owner within that scope, so scopes must
// come from server-side authorization, never directly from untrusted input.
func WithSearchScopes(scopes []SearchScope) SQLValidationOption {
	return func(v *sqlValidator) {
		if len(scopes) == 0 {
			return
		}
		v.enableSearchScopeFilter = true
		v.searchScopes = append([]SearchScope(nil), scopes...)
	}
}

// DefaultSafeTableColumns returns the strict per-table column allowlist for
// database_query / model-visible SQL queries, excluding credential/config JSON
// columns (cos_config, vlm_config, extract_config, storage_provider_config,
// etc.) and internal storage paths (file_path).
func DefaultSafeTableColumns() map[string][]string {
	return map[string][]string{
		"knowledge_bases": {
			"id", "name", "type", "is_temporary", "description", "tenant_id", "creator_id",
			"embedding_model_id", "summary_model_id", "rerank_model_id",
			"created_at", "updated_at", "deleted_at",
		},
		"knowledges": {
			"id", "tenant_id", "knowledge_base_id", "tag_id", "type", "title", "description",
			"source", "channel", "parse_status", "summary_status", "enable_status",
			"embedding_model_id", "file_name", "folder_path", "file_type", "file_size",
			"file_hash", "storage_size", "created_at", "updated_at", "processed_at",
			"error_message", "deleted_at",
		},
		"chunks": {
			"id", "seq_id", "tenant_id", "knowledge_id", "knowledge_base_id", "tag_id",
			"content", "chunk_index", "is_enabled", "flags", "status", "start_at", "end_at",
			"pre_chunk_id", "next_chunk_id", "chunk_type", "parent_chunk_id", "content_hash",
			"index_status", "content_revision", "created_at", "updated_at", "deleted_at",
		},
	}
}

var defaultDisallowedSensitiveColumns = map[string]bool{
	"cos_config":                 true,
	"storage_config":             true,
	"storage_provider_config":    true,
	"storage_backend_id":         true,
	"vlm_config":                 true,
	"extract_config":             true,
	"asr_config":                 true,
	"chunking_config":            true,
	"image_processing_config":    true,
	"faq_config":                 true,
	"question_generation_config": true,
	"auto_tag_config":            true,
	"profile_config":             true,
	"generated_profile":          true,
	"wiki_config":                true,
	"vector_store_id":            true,
	"file_path":                  true,
	"metadata":                   true,
	"meta_data":                  true,
	"custom_metadata":            true,
	"profile":                    true,
	"last_faq_import_result":     true,
	"source_content":             true,
	"image_info":                 true,
	"video_info":                 true,
	"relation_chunks":            true,
	"indirect_relation_chunks":   true,
	"source_locators":            true,
	"context_header":             true,
}

// WithSafeTableColumns enforces a per-table column allowlist across all SQL
// clauses (SELECT, WHERE, GROUP BY, HAVING, ORDER BY, JOIN) and rejects bare
// '*' / 'table.*' / whole-row composite references on restricted tables.
func WithSafeTableColumns(tableColumns map[string][]string) SQLValidationOption {
	return func(v *sqlValidator) {
		v.checkTableColumns = true
		v.safeTableColumns = make(map[string]map[string]bool, len(tableColumns))
		for table, cols := range tableColumns {
			colSet := make(map[string]bool, len(cols))
			for _, col := range cols {
				colSet[strings.ToLower(col)] = true
			}
			v.safeTableColumns[strings.ToLower(table)] = colSet
		}
	}
}

// WithSecurityDefaults applies a comprehensive set of security validations
func WithSecurityDefaults(tenantID uint64) SQLValidationOption {
	return func(v *sqlValidator) {
		// Apply all security checks
		WithInputValidation(6, 4096)(v)
		WithSelectOnly()(v)
		WithSingleStatement()(v)
		WithNoSubqueries()(v)
		WithNoCTEs()(v)
		WithNoSystemColumns()(v)
		WithNoSchemaAccess()(v)
		WithNoDangerousFunctions()(v)
		WithDefaultSafeFunctions()(v)
		WithTenantIsolation(tenantID)(v)
		WithSafeTableColumns(DefaultSafeTableColumns())(v)

		// Default allowed tables
		// SECURITY: Only tables with tenant_id column should be listed here
		// Tables without tenant_id (messages, embeddings) are excluded to prevent
		// cross-tenant data access vulnerabilities (CVE: Broken Access Control)
		WithAllowedTables(
			"knowledge_bases",
			"knowledges",
			"chunks",
		)(v)
	}
}
