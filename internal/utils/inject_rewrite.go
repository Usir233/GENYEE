package utils

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ValidateSQL validates a SQL statement with the given options
func ValidateSQL(sql string, opts ...SQLValidationOption) (*SQLParseResult, *SQLValidationResult) {
	// Initialize validator with defaults
	validator := &sqlValidator{
		allowedTables:       make(map[string]bool),
		allowedFunctions:    make(map[string]bool),
		tablesWithTenantID:  make(map[string]bool),
		tablesWithDeletedAt: make(map[string]bool),
		minLength:           6,
		maxLength:           4096,
	}

	// Apply options
	for _, opt := range opts {
		opt(validator)
	}

	// Initialize validation result
	validationResult := &SQLValidationResult{
		Valid:  true,
		Errors: make([]SQLValidationError, 0),
	}

	// Phase 1: Basic input validation
	if validator.checkInputValidation {
		if err := validator.validateInput(sql); err != nil {
			validationResult.Valid = false
			validationResult.Errors = append(validationResult.Errors, SQLValidationError{
				Type:    "input_validation_error",
				Message: "Input validation failed",
				Details: err.Error(),
			})
			return nil, validationResult
		}
	}

	// Phase 2: Parse SQL using PostgreSQL's official parser
	parseResult, err := pg_query.Parse(sql)
	if err != nil {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "parse_error",
			Message: "Failed to parse SQL",
			Details: fmt.Sprintf("SQL parse error: %v", err),
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  err.Error(),
		}, validationResult
	}

	// Phase 3: Validate statement count
	if len(parseResult.Stmts) == 0 {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "empty_query",
			Message: "Empty query",
			Details: "No statements found in SQL",
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  "empty query",
		}, validationResult
	}

	if validator.checkSingleStatement && len(parseResult.Stmts) > 1 {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "multiple_statements",
			Message: "Multiple statements are not allowed",
			Details: fmt.Sprintf("Found %d statements, only 1 is allowed", len(parseResult.Stmts)),
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			ParseError:  "multiple statements",
		}, validationResult
	}

	stmt := parseResult.Stmts[0].Stmt

	// Phase 4: Ensure it's a SELECT statement
	selectStmt := stmt.GetSelectStmt()
	if validator.checkSelectOnly && selectStmt == nil {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type:    "not_select_statement",
			Message: "Only SELECT queries are allowed",
			Details: "Statement is not a SELECT query",
		})
		return &SQLParseResult{
			OriginalSQL: sql,
			IsSelect:    false,
			ParseError:  "not a SELECT statement",
		}, validationResult
	}

	allTables := make(map[string]bool)
	// Inspect all AST fields, not just selected expression lists. This covers
	// CTEs, set-operation branches, scalar subqueries and window/limit clauses.
	if err := walkSQLMessages(parseResult.ProtoReflect(), func(message protoreflect.Message) error {
		switch node := message.Interface().(type) {
		case *pg_query.Alias:
			// A relation's positional column aliases can rename a credential
			// column to an allowed name, or retarget tenant/deletion predicates.
			// Security checks use canonical columns; ordinary table aliases and
			// SELECT column AS label remain supported.
			if len(node.Colnames) > 0 && (validator.checkTableColumns || validator.enableTenantInjection ||
				validator.enableSoftDeleteInjection || validator.enableHiddenKBFilter ||
				validator.enableChunkEnabledFilter || validator.enableSearchScopeFilter) {
				return fmt.Errorf(
					"relation column aliases are not supported by SQL security filters; use SELECT column AS label",
				)
			}
		case *pg_query.SelectStmt:
			if node.WithClause != nil {
				return fmt.Errorf("WITH clause (CTEs) is not supported")
			}
			if node.Op != pg_query.SetOperation_SETOP_NONE {
				return fmt.Errorf("compound queries are not supported")
			}
			if node.IntoClause != nil || len(node.LockingClause) > 0 {
				return fmt.Errorf("SELECT must be read-only")
			}
		case *pg_query.RangeVar:
			allTables[node.Relname] = true
			if validator.checkTableNames &&
				(node.Catalogname != "" || (node.Schemaname != "" && node.Schemaname != "public")) {
				return fmt.Errorf("qualified table access is not allowed")
			}
		case *pg_query.RangeFunction:
			return fmt.Errorf("functions in FROM clause are not allowed")
		case *pg_query.RangeTableSample:
			// The relation validators and security rewriter do not support
			// sampled inputs. Never execute one without its row-level filters.
			return fmt.Errorf("TABLESAMPLE is not supported by SQL security filters")
		case *pg_query.FuncCall:
			return validator.validateFuncCall(node, validationResult)
		case *pg_query.SubLink:
			if validator.checkSubqueries {
				return fmt.Errorf("subqueries are not allowed")
			}
		case *pg_query.RangeSubselect:
			if validator.checkSubqueries {
				return fmt.Errorf("subqueries are not allowed")
			}
		}
		return nil
	}); err != nil {
		validationResult.Valid = false
		validationResult.Errors = append(validationResult.Errors, SQLValidationError{
			Type: "statement_validation_error", Message: "Statement validation failed", Details: err.Error(),
		})
	}

	// Build parse result
	result := &SQLParseResult{
		OriginalSQL:  sql,
		IsSelect:     selectStmt != nil,
		TableNames:   make([]string, 0),
		SelectFields: make([]string, 0),
		WhereFields:  make([]string, 0),
	}

	if selectStmt != nil {
		// Extract SELECT fields
		result.SelectFields = extractSelectFieldsFromPgQuery(selectStmt)

		// Extract table names from FROM clause
		result.TableNames = extractTableNamesFromPgQuery(selectStmt)

		// Extract WHERE clause fields and text
		whereFields, whereClause := extractWhereFromPgQuery(selectStmt, sql)
		result.WhereFields = whereFields
		result.WhereClause = whereClause

		// Phase 5: Validate the SELECT statement with deep inspection
		if err := validator.validateSelectStmt(selectStmt, validationResult); err != nil {
			validationResult.Valid = false
			validationResult.Errors = append(validationResult.Errors, SQLValidationError{
				Type:    "statement_validation_error",
				Message: "Statement validation failed",
				Details: err.Error(),
			})
		}

		// Phase 6: Validate table names
		if validator.checkTableNames {
			for table := range allTables {
				if !validator.allowedTables[table] {
					validationResult.Valid = false
					validationResult.Errors = append(validationResult.Errors, SQLValidationError{
						Type:    "table_not_allowed",
						Message: fmt.Sprintf("Table '%s' is not in the allowed list", table),
						Details: fmt.Sprintf("Allowed tables: %v", getMapKeys(validator.allowedTables)),
					})
				}
			}
		}

		// Phase 7: Check for SQL injection risks (legacy check)
		if validator.checkInjectionRisk {
			injectionErrors := checkSQLInjectionRisks(result.WhereClause)
			if len(injectionErrors) > 0 {
				validationResult.Valid = false
				validationResult.Errors = append(validationResult.Errors, injectionErrors...)
			}
		}
	}

	return result, validationResult
}

// ValidateAndSecureSQL validates SQL and returns a secured version with tenant isolation
// This is a convenience function that combines validation and SQL rewriting
func ValidateAndSecureSQL(sql string, opts ...SQLValidationOption) (string, *SQLValidationResult, error) {
	// Default-enforce safe table column allowlist on restricted tables unless overridden.
	effectiveOpts := make([]SQLValidationOption, 0, len(opts)+1)
	effectiveOpts = append(effectiveOpts, WithSafeTableColumns(DefaultSafeTableColumns()))
	effectiveOpts = append(effectiveOpts, opts...)

	// Parse and validate
	_, validationResult := ValidateSQL(sql, effectiveOpts...)

	// If validation failed, return error
	if !validationResult.Valid {
		errMsg := "SQL validation failed"
		if len(validationResult.Errors) > 0 {
			errMsg = validationResult.Errors[0].Message
		}
		return "", validationResult, fmt.Errorf("%s", errMsg)
	}

	// Find validator config to check if tenant injection is enabled
	validator := &sqlValidator{
		tablesWithTenantID:  make(map[string]bool),
		tablesWithDeletedAt: make(map[string]bool),
	}
	for _, opt := range opts {
		opt(validator)
	}

	// If no SQL rewriting is enabled, return original SQL
	if !validator.enableTenantInjection && !validator.enableSoftDeleteInjection && !validator.enableHiddenKBFilter &&
		!validator.enableChunkEnabledFilter && !validator.enableSearchScopeFilter {
		return sql, validationResult, nil
	}

	// Parse again to get normalized SQL
	result, err := pg_query.Parse(sql)
	if err != nil {
		return "", validationResult, fmt.Errorf("failed to parse SQL: %v", err)
	}

	// Rewrite every original SELECT scope, including repeated references to the
	// same table. Never search SQL text for keywords: identifiers and literals
	// may contain WHERE/ORDER BY, and aliases are case sensitive when quoted.
	err = walkSQLMessages(result.ProtoReflect(), func(message protoreflect.Message) error {
		stmt, ok := message.Interface().(*pg_query.SelectStmt)
		if !ok {
			return nil
		}
		// An outer join operates on already-authorized input relations. Putting
		// their filters in this SELECT's WHERE would remove NULL-extended rows.
		prefilterInputs := false
		unaliasedTables := make(map[string]bool)
		var hasOuterJoin func(*pg_query.Node) bool
		hasOuterJoin = func(node *pg_query.Node) bool {
			if rv := node.GetRangeVar(); rv != nil && rv.Alias == nil {
				unaliasedTables[rv.Relname] = true
			}
			if join := node.GetJoinExpr(); join != nil {
				left, right := hasOuterJoin(join.Larg), hasOuterJoin(join.Rarg)
				return join.Jointype != pg_query.JoinType_JOIN_INNER || left || right
			}
			return false
		}
		for _, from := range stmt.FromClause {
			outer := hasOuterJoin(from)
			prefilterInputs = prefilterInputs || outer
		}
		if prefilterInputs {
			rewriteFilteredTableQualifiers(stmt, unaliasedTables)
		}
		var injectFrom func(*pg_query.Node) error
		injectFrom = func(node *pg_query.Node) error {
			if rv := node.GetRangeVar(); rv != nil {
				alias := rv.Relname
				if rv.Alias != nil {
					alias = rv.Alias.Aliasname
				}
				tables := map[string]string{strings.ToLower(rv.Relname): quoteSQLIdentifier(alias)}
				fragment := "SELECT 1"
				fragment = validator.injectTenantConditions(fragment, tables)
				fragment = validator.injectSoftDeleteConditions(fragment, tables)
				fragment = validator.injectHiddenKBFilter(fragment, tables)
				fragment = validator.injectChunkEnabledFilter(fragment, tables)
				fragment = validator.injectSearchScopeConditions(fragment, tables)
				filters, err := pg_query.Parse(fragment)
				if err != nil {
					return fmt.Errorf("failed to build SQL security filter: %w", err)
				}
				filtered := filters.Stmts[0].Stmt.GetSelectStmt()
				if prefilterInputs && filtered.WhereClause != nil {
					filtered.FromClause = []*pg_query.Node{{Node: node.Node}}
					// The original projection was already column-validated. This
					// internal star only preserves the filtered input's columns.
					filtered.TargetList[0].GetResTarget().Val = &pg_query.Node{Node: &pg_query.Node_ColumnRef{
						ColumnRef: &pg_query.ColumnRef{
							Fields: []*pg_query.Node{{Node: &pg_query.Node_AStar{AStar: &pg_query.A_Star{}}}},
						},
					}}
					node.Node = &pg_query.Node_RangeSubselect{RangeSubselect: &pg_query.RangeSubselect{
						Subquery: &pg_query.Node{Node: &pg_query.Node_SelectStmt{SelectStmt: filtered}},
						Alias:    &pg_query.Alias{Aliasname: alias},
					}}
				} else {
					addSQLPredicate(stmt, filtered.WhereClause)
				}
			}
			if join := node.GetJoinExpr(); join != nil {
				if err := injectFrom(join.Larg); err != nil {
					return err
				}
				return injectFrom(join.Rarg)
			}
			return nil
		}
		for _, from := range stmt.FromClause {
			if err := injectFrom(from); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", validationResult, err
	}
	securedSQL, err := pg_query.Deparse(result)
	if err != nil {
		return "", validationResult, fmt.Errorf("failed to deparse secured SQL: %w", err)
	}
	return securedSQL, validationResult, nil
}

// rewriteFilteredTableQualifiers keeps public.table.column references bound to
// the same table after it becomes a filtered input named table. Nested SELECTs
// have their own scopes and are handled by their own rewrite pass.
func rewriteFilteredTableQualifiers(stmt *pg_query.SelectStmt, tables map[string]bool) {
	var walk func(protoreflect.Message)
	walk = func(message protoreflect.Message) {
		if nested, ok := message.Interface().(*pg_query.SelectStmt); ok && nested != stmt {
			return
		}
		if column, ok := message.Interface().(*pg_query.ColumnRef); ok && len(column.Fields) == 3 {
			schema, table := column.Fields[0].GetString_(), column.Fields[1].GetString_()
			if schema != nil && schema.Sval == "public" && table != nil && tables[table.Sval] {
				column.Fields = column.Fields[1:]
			}
		}
		message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			if field.Message() == nil {
				return true
			}
			if field.IsList() {
				list := value.List()
				for i := 0; i < list.Len(); i++ {
					walk(list.Get(i).Message())
				}
			} else {
				walk(value.Message())
			}
			return true
		})
	}
	walk(stmt.ProtoReflect())
}

// walkSQLMessages visits children before their parent so rewriting a SELECT
// does not revisit trusted predicates added by the server.
func walkSQLMessages(message protoreflect.Message, visit func(protoreflect.Message) error) error {
	var walkErr error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Message() == nil {
			return true
		}
		if field.IsList() {
			list := value.List()
			for i := 0; i < list.Len(); i++ {
				if walkErr = walkSQLMessages(list.Get(i).Message(), visit); walkErr != nil {
					return false
				}
			}
		} else {
			walkErr = walkSQLMessages(value.Message(), visit)
		}
		return walkErr == nil
	})
	if walkErr != nil {
		return walkErr
	}
	return visit(message)
}

func quoteSQLIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func addSQLPredicate(stmt *pg_query.SelectStmt, predicate *pg_query.Node) {
	if predicate == nil {
		return
	}
	if stmt.WhereClause == nil {
		stmt.WhereClause = predicate
		return
	}
	stmt.WhereClause = &pg_query.Node{Node: &pg_query.Node_BoolExpr{BoolExpr: &pg_query.BoolExpr{
		Boolop: pg_query.BoolExprType_AND_EXPR, Args: []*pg_query.Node{predicate, stmt.WhereClause},
	}}}
}

// InjectAndConditions combines parsed expressions, preserving literal and alias
// boundaries. An invalid statement or filter returns no executable SQL.
func InjectAndConditions(sql, filter string) string {
	if strings.TrimSpace(filter) == "" {
		return sql
	}
	parsed, err := pg_query.Parse(sql)
	if err != nil || len(parsed.Stmts) != 1 {
		return ""
	}
	stmt := parsed.Stmts[0].Stmt.GetSelectStmt()
	if stmt == nil || stmt.Op != pg_query.SetOperation_SETOP_NONE {
		return ""
	}
	filters, err := pg_query.Parse("SELECT 1 WHERE " + filter)
	if err != nil || len(filters.Stmts) != 1 {
		return ""
	}
	predicate := filters.Stmts[0].Stmt.GetSelectStmt()
	if predicate == nil || predicate.WhereClause == nil {
		return ""
	}
	addSQLPredicate(stmt, predicate.WhereClause)
	out, err := pg_query.Deparse(parsed)
	if err != nil {
		return ""
	}
	return out
}

// injectTenantConditions adds tenant_id filtering to the query
func (v *sqlValidator) injectTenantConditions(sql string, tablesInQuery map[string]string) string {
	if !v.enableTenantInjection {
		return sql
	}

	// Build tenant conditions
	var conditions []string
	for tableName, alias := range tablesInQuery {
		if v.tablesWithTenantID[tableName] {
			// Structured scopes bind each owner to its own KB and document/tag
			// restrictions below. A global caller filter would reject shared KBs.
			if v.enableSearchScopeFilter && len(v.searchScopes) > 0 &&
				(tableName == "knowledge_bases" || tableName == "knowledges" || tableName == "chunks") {
				continue
			}
			if tableName == "tenants" {
				conditions = append(conditions, fmt.Sprintf("%s.id = %d", alias, v.tenantID))
			} else {
				conditions = append(conditions, fmt.Sprintf("%s.tenant_id = %d", alias, v.tenantID))
			}
		}
	}

	if len(conditions) == 0 {
		return sql
	}

	tenantFilter := strings.Join(conditions, " AND ")
	return InjectAndConditions(sql, tenantFilter)
}

// injectSoftDeleteConditions adds deleted_at IS NULL filtering to the query.
func (v *sqlValidator) injectSoftDeleteConditions(sql string, tablesInQuery map[string]string) string {
	if !v.enableSoftDeleteInjection {
		return sql
	}

	var conditions []string
	for tableName, alias := range tablesInQuery {
		if v.tablesWithDeletedAt[tableName] {
			conditions = append(conditions, fmt.Sprintf("%s.deleted_at IS NULL", alias))
		}
	}

	if len(conditions) == 0 {
		return sql
	}

	return InjectAndConditions(sql, strings.Join(conditions, " AND "))
}

// injectHiddenKBFilter adds is_temporary = false filtering for the knowledge_bases table,
// hiding internal/system-managed KBs (e.g., __chat_history__) from query results.
func (v *sqlValidator) injectHiddenKBFilter(sql string, tablesInQuery map[string]string) string {
	if !v.enableHiddenKBFilter {
		return sql
	}
	alias, ok := tablesInQuery["knowledge_bases"]
	if !ok {
		return sql
	}
	return InjectAndConditions(sql, fmt.Sprintf("%s.is_temporary = false", alias))
}

func (v *sqlValidator) injectChunkEnabledFilter(sql string, tablesInQuery map[string]string) string {
	if !v.enableChunkEnabledFilter {
		return sql
	}
	alias, ok := tablesInQuery["chunks"]
	if !ok {
		return sql
	}
	return InjectAndConditions(sql, fmt.Sprintf("%s.is_enabled = true", alias))
}

// injectSearchScopeConditions restricts queries to the allowed knowledge bases
// and (optionally) specific knowledge documents.
func (v *sqlValidator) injectSearchScopeConditions(sql string, tablesInQuery map[string]string) string {
	if !v.enableSearchScopeFilter || len(v.searchScopeKBIDs) == 0 {
		if len(v.searchScopes) == 0 {
			return sql
		}
	}

	if len(v.searchScopes) > 0 {
		return v.injectStructuredSearchScopeConditions(sql, tablesInQuery)
	}

	quotedKBIDs := quoteStringSlice(v.searchScopeKBIDs)
	kbList := strings.Join(quotedKBIDs, ", ")

	var conditions []string

	if alias, ok := tablesInQuery["knowledge_bases"]; ok {
		conditions = append(conditions, fmt.Sprintf("%s.id IN (%s)", alias, kbList))
	}

	if alias, ok := tablesInQuery["knowledges"]; ok {
		conditions = append(conditions, fmt.Sprintf("%s.knowledge_base_id IN (%s)", alias, kbList))
		if len(v.searchScopeKnowledgeIDs) > 0 {
			quotedKIDs := quoteStringSlice(v.searchScopeKnowledgeIDs)
			conditions = append(conditions, fmt.Sprintf("%s.id IN (%s)", alias, strings.Join(quotedKIDs, ", ")))
		}
	}

	if alias, ok := tablesInQuery["chunks"]; ok {
		conditions = append(conditions, fmt.Sprintf("%s.knowledge_base_id IN (%s)", alias, kbList))
		if len(v.searchScopeKnowledgeIDs) > 0 {
			quotedKIDs := quoteStringSlice(v.searchScopeKnowledgeIDs)
			conditions = append(
				conditions, fmt.Sprintf("%s.knowledge_id IN (%s)", alias, strings.Join(quotedKIDs, ", ")),
			)
		}
	}

	if len(conditions) == 0 {
		return sql
	}

	return InjectAndConditions(sql, strings.Join(conditions, " AND "))
}

func (v *sqlValidator) injectStructuredSearchScopeConditions(sql string, tablesInQuery map[string]string) string {
	var conditions []string

	if alias, ok := tablesInQuery["knowledge_bases"]; ok {
		if cond := v.buildKnowledgeBaseScopeCondition(alias, v.searchScopes); cond != "" {
			conditions = append(conditions, cond)
		}
	}
	if alias, ok := tablesInQuery["knowledges"]; ok {
		if cond := v.buildKnowledgeScopeCondition(alias, v.searchScopes); cond != "" {
			conditions = append(conditions, cond)
		}
	}
	if alias, ok := tablesInQuery["chunks"]; ok {
		if cond := v.buildChunkScopeCondition(alias, v.searchScopes); cond != "" {
			conditions = append(conditions, cond)
		}
	}

	if len(conditions) == 0 {
		return sql
	}
	return InjectAndConditions(sql, strings.Join(conditions, " AND "))
}

func (v *sqlValidator) buildKnowledgeBaseScopeCondition(alias string, scopes []SearchScope) string {
	clauses := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if scope.KnowledgeBaseID == "" {
			continue
		}
		clause := fmt.Sprintf("%s.id = %s", alias, quoteString(scope.KnowledgeBaseID))
		if tenant := v.scopeTenantCondition(alias, scope); tenant != "" {
			clause = "(" + clause + " AND " + tenant + ")"
		}
		clauses = append(clauses, clause)
	}
	if len(clauses) == 0 {
		return "false"
	}
	return joinOrClauses(clauses)
}

func unquoteSQLIdentifier(ident string) string {
	trimmed := strings.TrimSpace(ident)
	if len(trimmed) >= 2 && trimmed[0] == '"' && trimmed[len(trimmed)-1] == '"' {
		return strings.ReplaceAll(trimmed[1:len(trimmed)-1], `""`, `"`)
	}
	return trimmed
}

func tagRelationSubqueryAlias(outerAlias string) string {
	outer := unquoteSQLIdentifier(outerAlias)
	subAlias := "__genyee_ktr"
	for strings.EqualFold(outer, subAlias) {
		subAlias += "_"
	}
	return subAlias
}

// buildScopeClause renders one scope. A scope's document whitelist and tag
// filter are ANDed: each is an independent narrowing of the same KB, so
// applying only one of them would admit rows the other excludes. Scopes are
// ORed against each other by joinOrClauses because they are alternatives.
func (v *sqlValidator) buildScopeClause(
	alias, knowledgeIDColumn string,
	includeChunkTagID bool,
	scope SearchScope,
) string {
	if scope.KnowledgeBaseID == "" {
		return ""
	}
	conditions := []string{
		fmt.Sprintf("%s.knowledge_base_id = %s", alias, quoteString(scope.KnowledgeBaseID)),
	}
	if len(scope.KnowledgeIDs) > 0 {
		conditions = append(conditions, fmt.Sprintf(
			"%s.%s IN (%s)",
			alias, knowledgeIDColumn, strings.Join(quoteStringSlice(scope.KnowledgeIDs), ", "),
		))
	}
	if len(scope.TagIDs) > 0 {
		quotedTagIDs := strings.Join(quoteStringSlice(scope.TagIDs), ", ")
		subAlias := tagRelationSubqueryAlias(alias)
		tagRelClause := fmt.Sprintf(
			"EXISTS (SELECT 1 FROM knowledge_tag_relations %s WHERE %s.knowledge_id = %s.%s AND %s.tag_id IN (%s))",
			subAlias, subAlias, alias, knowledgeIDColumn, subAlias, quotedTagIDs,
		)
		if includeChunkTagID {
			conditions = append(conditions, fmt.Sprintf(
				"(%s.tag_id IN (%s) OR %s)",
				alias, quotedTagIDs, tagRelClause,
			))
		} else {
			conditions = append(conditions, tagRelClause)
		}
	}
	if tenant := v.scopeTenantCondition(alias, scope); tenant != "" {
		conditions = append(conditions, tenant)
	}
	if len(conditions) == 1 {
		return conditions[0]
	}
	return "(" + strings.Join(conditions, " AND ") + ")"
}

func (v *sqlValidator) buildKnowledgeScopeCondition(alias string, scopes []SearchScope) string {
	clauses := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if clause := v.buildScopeClause(alias, "id", false, scope); clause != "" {
			clauses = append(clauses, clause)
		}
	}
	if len(clauses) == 0 {
		return "false"
	}
	return joinOrClauses(clauses)
}

func (v *sqlValidator) buildChunkScopeCondition(alias string, scopes []SearchScope) string {
	clauses := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if clause := v.buildScopeClause(alias, "knowledge_id", true, scope); clause != "" {
			clauses = append(clauses, clause)
		}
	}
	if len(clauses) == 0 {
		return "false"
	}
	return joinOrClauses(clauses)
}

// scopeTenantCondition keeps ownership in the same OR branch as the KB and
// document/tag restrictions. Missing ownership retains caller-only isolation.
func (v *sqlValidator) scopeTenantCondition(alias string, scope SearchScope) string {
	tenantID := scope.TenantID
	if tenantID == 0 {
		if !v.enableTenantInjection {
			return ""
		}
		tenantID = v.tenantID
	}
	return fmt.Sprintf("%s.tenant_id = %d", alias, tenantID)
}

func joinOrClauses(clauses []string) string {
	if len(clauses) == 0 {
		return ""
	}
	if len(clauses) == 1 {
		return clauses[0]
	}
	return "(" + strings.Join(clauses, " OR ") + ")"
}

func quoteString(s string) string {
	return quoteStringSlice([]string{s})[0]
}

func quoteStringSlice(ss []string) []string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		escaped := strings.ReplaceAll(s, "'", "''")
		quoted[i] = fmt.Sprintf("'%s'", escaped)
	}
	return quoted
}
