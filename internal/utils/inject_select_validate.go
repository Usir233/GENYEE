package utils

import (
	"fmt"
	"regexp"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Compiled once. checkSQLInjectionRisks runs on every validated WHERE clause,
// so these patterns are hoisted to package scope instead of being recompiled
// per call.
var (
	reSQLWhitespace = regexp.MustCompile(`\s+`)

	sqlAlwaysTruePatterns = []struct {
		pattern     *regexp.Regexp
		description string
	}{
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(1\s*=\s*1|'1'\s*=\s*'1'|"1"\s*=\s*"1")(\s|\)|$|and|or)`),
			description: "Always-true condition '1=1' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(0\s*=\s*0|'0'\s*=\s*'0'|"0"\s*=\s*"0")(\s|\)|$|and|or)`),
			description: "Always-true condition '0=0' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(true)(\s|\)|$|and|or)`),
			description: "Always-true condition 'true'",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()('\s*'\s*=\s*'\s*'|"\s*"\s*=\s*"\s*")(\s|\)|$|and|or)`),
			description: "Always-true condition with empty strings",
		},
	}

	sqlAlwaysFalsePatterns = []struct {
		pattern     *regexp.Regexp
		description string
	}{
		{
			pattern: regexp.MustCompile(
				`(^|\s|\()(1\s*=\s*0|0\s*=\s*1|'1'\s*=\s*'0'|"1"\s*=\s*"0")(\s|\)|$|and|or)`,
			),
			description: "Always-false condition '1=0' or similar",
		},
		{
			pattern:     regexp.MustCompile(`(^|\s|\()(false)(\s|\)|$|and|or)`),
			description: "Always-false condition 'false'",
		},
	}

	reSQLOrAlwaysTrue = regexp.MustCompile(`or\s+(1\s*=\s*1|'1'\s*=\s*'1'|true)`)
)

// checkSQLInjectionRisks checks for common SQL injection patterns in WHERE clause
func checkSQLInjectionRisks(whereClause string) []SQLValidationError {
	errors := make([]SQLValidationError, 0)

	if whereClause == "" {
		return errors
	}

	// Normalize the WHERE clause for checking
	normalizedWhere := strings.ToLower(strings.TrimSpace(whereClause))
	normalizedWhere = reSQLWhitespace.ReplaceAllString(normalizedWhere, " ")

	// Pattern 1: Always true conditions like "1=1", "'1'='1'", "true", etc.
	for _, pt := range sqlAlwaysTruePatterns {
		if pt.pattern.MatchString(normalizedWhere) {
			errors = append(errors, SQLValidationError{
				Type:    "sql_injection_risk",
				Message: "Potential SQL injection risk detected",
				Details: fmt.Sprintf("%s found in WHERE clause: %s", pt.description, whereClause),
			})
		}
	}

	// Pattern 2: Always false conditions that might be used for testing
	for _, pt := range sqlAlwaysFalsePatterns {
		if pt.pattern.MatchString(normalizedWhere) {
			errors = append(errors, SQLValidationError{
				Type:    "sql_injection_risk",
				Message: "Suspicious SQL pattern detected",
				Details: fmt.Sprintf("%s found in WHERE clause: %s", pt.description, whereClause),
			})
		}
	}

	// Pattern 3: OR with always-true condition (common injection pattern)
	if reSQLOrAlwaysTrue.MatchString(normalizedWhere) {
		errors = append(errors, SQLValidationError{
			Type:    "sql_injection_risk",
			Message: "High-risk SQL injection pattern detected",
			Details: fmt.Sprintf("OR with always-true condition found in WHERE clause: %s", whereClause),
		})
	}

	return errors
}

// getMapKeys returns the keys of a map as a slice
func getMapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// validateInput performs basic input validation
func (v *sqlValidator) validateInput(sql string) error {
	// Check for null bytes
	if strings.Contains(sql, "\x00") {
		return fmt.Errorf("invalid character in SQL query")
	}

	// Check length limits
	if len(sql) < v.minLength {
		return fmt.Errorf("SQL query too short (min %d characters)", v.minLength)
	}
	if len(sql) > v.maxLength {
		return fmt.Errorf("SQL query too long (max %d characters)", v.maxLength)
	}

	return nil
}

// validateSelectStmt validates a SELECT statement with configured options
func (v *sqlValidator) validateSelectStmt(stmt *pg_query.SelectStmt, result *SQLValidationResult) error {
	tablesInQuery := make(map[string]string) // table name -> alias

	// Check for UNION/INTERSECT/EXCEPT (compound queries)
	if stmt.Op != pg_query.SetOperation_SETOP_NONE {
		return fmt.Errorf("compound queries (UNION/INTERSECT/EXCEPT) are not allowed")
	}

	// Check for WITH clause (CTEs)
	if v.checkCTEs && stmt.WithClause != nil {
		return fmt.Errorf("WITH clause (CTEs) is not allowed")
	}

	// Check for INTO clause (SELECT INTO)
	if stmt.IntoClause != nil {
		return fmt.Errorf("SELECT INTO is not allowed")
	}

	// Check for LOCKING clause (FOR UPDATE, etc.)
	if len(stmt.LockingClause) > 0 {
		return fmt.Errorf("locking clauses (FOR UPDATE, etc.) are not allowed")
	}

	// Validate FROM clause
	for _, fromItem := range stmt.FromClause {
		if err := v.validateFromItem(fromItem, tablesInQuery, result); err != nil {
			return err
		}
	}

	// Validate target list (SELECT columns)
	for _, target := range stmt.TargetList {
		if err := v.validateNode(target, result); err != nil {
			return err
		}
	}

	// Validate WHERE clause
	if stmt.WhereClause != nil {
		if err := v.validateNode(stmt.WhereClause, result); err != nil {
			return err
		}
	}

	// Validate GROUP BY clause
	for _, groupBy := range stmt.GroupClause {
		if err := v.validateNode(groupBy, result); err != nil {
			return err
		}
	}

	// Validate HAVING clause
	if stmt.HavingClause != nil {
		if err := v.validateNode(stmt.HavingClause, result); err != nil {
			return err
		}
	}

	// Validate ORDER BY clause
	for _, sortBy := range stmt.SortClause {
		if err := v.validateNode(sortBy, result); err != nil {
			return err
		}
	}

	// Ensure at least one valid table is referenced
	if len(tablesInQuery) == 0 {
		return fmt.Errorf("no valid table found in query")
	}

	if v.checkTableColumns && len(v.safeTableColumns) > 0 {
		if err := v.validateSelectColumnAllowlist(stmt, nil, nil); err != nil {
			return err
		}
	}

	return nil
}

// validateSelectColumnAllowlist enforces v.safeTableColumns across all clauses of a SELECT
// statement (including nested subqueries, JOIN ON/USING, NATURAL JOIN, wildcards, and
// composite whole-row or field-indirection expressions).
func (v *sqlValidator) validateSelectColumnAllowlist(
	stmt *pg_query.SelectStmt,
	outerAliases map[string]string,
	outerTables []string,
) error {
	if stmt == nil {
		return nil
	}

	localAliases := make(map[string]string)
	localTables := make([]string, 0)

	var collectFrom func(*pg_query.Node) ([]string, error)
	collectFrom = func(node *pg_query.Node) ([]string, error) {
		if node == nil {
			return nil, nil
		}
		if rv := node.GetRangeVar(); rv != nil {
			tableName := strings.ToLower(rv.Relname)
			localTables = append(localTables, tableName)
			alias := tableName
			if rv.Alias != nil && rv.Alias.Aliasname != "" {
				alias = strings.ToLower(rv.Alias.Aliasname)
			}
			localAliases[alias] = tableName
			return []string{tableName}, nil
		}
		if je := node.GetJoinExpr(); je != nil {
			leftTables, err := collectFrom(je.Larg)
			if err != nil {
				return nil, err
			}
			rightTables, err := collectFrom(je.Rarg)
			if err != nil {
				return nil, err
			}
			joined := append(append([]string(nil), leftTables...), rightTables...)
			hasRestricted := false
			for _, t := range joined {
				if _, ok := v.safeTableColumns[t]; ok {
					hasRestricted = true
					break
				}
			}
			if je.IsNatural && hasRestricted {
				return nil, fmt.Errorf("NATURAL JOIN is not allowed on restricted tables")
			}
			for _, u := range je.UsingClause {
				s := u.GetString_()
				if s == nil {
					continue
				}
				col := strings.ToLower(s.Sval)
				for _, t := range joined {
					if allowedCols, restricted := v.safeTableColumns[t]; restricted && !allowedCols[col] {
						return nil, fmt.Errorf("column '%s' in USING clause is not allowed on table '%s'", col, t)
					}
				}
			}
			return joined, nil
		}
		if rss := node.GetRangeSubselect(); rss != nil && rss.Subquery != nil {
			if err := v.validateSelectColumnAllowlist(rss.Subquery.GetSelectStmt(), nil, nil); err != nil {
				return nil, err
			}
			if rss.Alias != nil && rss.Alias.Aliasname != "" {
				// Derived subquery already validated against safeTableColumns;
				// mark alias as unrestricted derived table.
				alias := strings.ToLower(rss.Alias.Aliasname)
				localAliases[alias] = "__subquery__"
				localTables = append(localTables, "__subquery__")
			}
		}
		return nil, nil
	}

	for _, fromItem := range stmt.FromClause {
		if _, err := collectFrom(fromItem); err != nil {
			return err
		}
	}

	scopeAliases := make(map[string]string, len(outerAliases)+len(localAliases))
	for k, val := range outerAliases {
		scopeAliases[k] = val
	}
	for k, val := range localAliases {
		scopeAliases[k] = val
	}

	combinedTables := append(append([]string(nil), localTables...), outerTables...)
	hasRestrictedTable := false
	for _, t := range combinedTables {
		if _, ok := v.safeTableColumns[t]; ok {
			hasRestrictedTable = true
			break
		}
	}

	outputAliases := make(map[string]bool)
	for _, target := range stmt.TargetList {
		if rt := target.GetResTarget(); rt != nil && rt.Name != "" {
			outputAliases[strings.ToLower(rt.Name)] = true
		}
	}

	var walkScopeExpr func(protoreflect.Message) error
	walkScopeExpr = func(msg protoreflect.Message) error {
		if subStmt, ok := msg.Interface().(*pg_query.SelectStmt); ok && subStmt != stmt {
			return v.validateSelectColumnAllowlist(subStmt, scopeAliases, combinedTables)
		}

		var childErr error
		msg.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
			if field.Message() == nil {
				return true
			}
			if field.IsList() {
				list := value.List()
				for i := 0; i < list.Len(); i++ {
					if childErr = walkScopeExpr(list.Get(i).Message()); childErr != nil {
						return false
					}
				}
			} else {
				childErr = walkScopeExpr(value.Message())
			}
			return childErr == nil
		})
		if childErr != nil {
			return childErr
		}

		switch n := msg.Interface().(type) {
		case *pg_query.A_Indirection:
			if hasRestrictedTable {
				for _, ind := range n.Indirection {
					if ind.GetAStar() != nil || ind.GetString_() != nil {
						return fmt.Errorf("composite field indirection is not allowed on restricted tables")
					}
				}
			}
		case *pg_query.FieldSelect:
			if hasRestrictedTable {
				return fmt.Errorf("composite field selection is not allowed on restricted tables")
			}
		case *pg_query.ColumnRef:
			return v.validateColumnRefAgainstAllowlist(n, scopeAliases, combinedTables, hasRestrictedTable)
		}
		return nil
	}

	checkNodeList := func(nodes []*pg_query.Node) error {
		for _, n := range nodes {
			if n != nil {
				if err := walkScopeExpr(n.ProtoReflect()); err != nil {
					return err
				}
			}
		}
		return nil
	}

	var checkJoinQuals func(*pg_query.Node) error
	checkJoinQuals = func(node *pg_query.Node) error {
		if node == nil {
			return nil
		}
		if je := node.GetJoinExpr(); je != nil {
			if err := checkJoinQuals(je.Larg); err != nil {
				return err
			}
			if err := checkJoinQuals(je.Rarg); err != nil {
				return err
			}
			if je.Quals != nil {
				if err := walkScopeExpr(je.Quals.ProtoReflect()); err != nil {
					return err
				}
			}
		}
		return nil
	}

	for _, fromItem := range stmt.FromClause {
		if err := checkJoinQuals(fromItem); err != nil {
			return err
		}
	}
	if err := checkNodeList(stmt.DistinctClause); err != nil {
		return err
	}
	if err := checkNodeList(stmt.TargetList); err != nil {
		return err
	}
	if stmt.WhereClause != nil {
		if err := walkScopeExpr(stmt.WhereClause.ProtoReflect()); err != nil {
			return err
		}
	}
	if err := checkNodeList(stmt.GroupClause); err != nil {
		return err
	}
	if stmt.HavingClause != nil {
		if err := walkScopeExpr(stmt.HavingClause.ProtoReflect()); err != nil {
			return err
		}
	}
	if err := checkNodeList(stmt.WindowClause); err != nil {
		return err
	}
	for _, sortItem := range stmt.SortClause {
		if sb := sortItem.GetSortBy(); sb != nil && sb.Node != nil {
			if cr := sb.Node.GetColumnRef(); cr != nil && len(cr.Fields) == 1 {
				if s := cr.Fields[0].GetString_(); s != nil {
					col := strings.ToLower(s.Sval)
					if outputAliases[col] && !defaultDisallowedSensitiveColumns[col] {
						continue
					}
				}
			}
		}
		if sortItem != nil {
			if err := walkScopeExpr(sortItem.ProtoReflect()); err != nil {
				return err
			}
		}
	}
	if stmt.LimitOffset != nil {
		if err := walkScopeExpr(stmt.LimitOffset.ProtoReflect()); err != nil {
			return err
		}
	}
	if stmt.LimitCount != nil {
		if err := walkScopeExpr(stmt.LimitCount.ProtoReflect()); err != nil {
			return err
		}
	}

	return nil
}

func (v *sqlValidator) validateColumnRefAgainstAllowlist(
	cr *pg_query.ColumnRef,
	scopeAliases map[string]string,
	combinedTables []string,
	hasRestrictedTable bool,
) error {
	if cr == nil || !hasRestrictedTable {
		return nil
	}

	for i, field := range cr.Fields {
		if field.GetAStar() != nil {
			if i == 0 {
				return fmt.Errorf(
					"wildcard '*' is not allowed on restricted tables; specify allowed columns explicitly",
				)
			}
			qual := ""
			if prev := cr.Fields[i-1].GetString_(); prev != nil {
				qual = strings.ToLower(prev.Sval)
			}
			tableName, ok := scopeAliases[qual]
			if !ok {
				tableName = qual
			}
			if _, restricted := v.safeTableColumns[tableName]; restricted || tableName == "" {
				return fmt.Errorf("wildcard '%s.*' is not allowed on restricted table '%s'", qual, tableName)
			}
			return nil
		}
	}

	parts := make([]string, 0, len(cr.Fields))
	for _, field := range cr.Fields {
		s := field.GetString_()
		if s == nil {
			return fmt.Errorf("unsupported column reference expression on restricted table")
		}
		parts = append(parts, strings.ToLower(s.Sval))
	}
	if len(parts) == 0 {
		return fmt.Errorf("empty column reference")
	}
	if len(parts) > 3 || (len(parts) == 3 && parts[0] != "public") {
		return fmt.Errorf("invalid qualified column reference '%s'", strings.Join(parts, "."))
	}

	if len(parts) >= 2 {
		qual := parts[len(parts)-2]
		col := parts[len(parts)-1]
		tableName, ok := scopeAliases[qual]
		if !ok {
			return fmt.Errorf("unknown table or alias '%s' in column reference '%s.%s'", qual, qual, col)
		}
		if allowedCols, restricted := v.safeTableColumns[tableName]; restricted {
			if !allowedCols[col] {
				return fmt.Errorf("access to column '%s' on table '%s' is not allowed", col, tableName)
			}
		}
		return nil
	}

	col := parts[0]
	if defaultDisallowedSensitiveColumns[col] {
		return fmt.Errorf("access to sensitive column '%s' is not allowed", col)
	}

	allowedInAnyRestricted := false
	hasUnrestrictedTable := false
	for _, t := range combinedTables {
		if allowedCols, restricted := v.safeTableColumns[t]; restricted {
			if allowedCols[col] {
				allowedInAnyRestricted = true
			}
		} else {
			hasUnrestrictedTable = true
		}
	}

	if aliasTable, isAlias := scopeAliases[col]; isAlias {
		if allowedCols, restricted := v.safeTableColumns[aliasTable]; restricted && !allowedCols[col] {
			return fmt.Errorf("whole-row reference to restricted table alias '%s' is not allowed", col)
		}
	}

	if !allowedInAnyRestricted && !hasUnrestrictedTable {
		return fmt.Errorf("access to column '%s' is not allowed", col)
	}
	return nil
}

// validateFromItem validates a FROM clause item
func (v *sqlValidator) validateFromItem(
	node *pg_query.Node,
	tables map[string]string,
	result *SQLValidationResult,
) error {
	if node == nil {
		return nil
	}

	// Handle RangeVar (simple table reference)
	if rv := node.GetRangeVar(); rv != nil {
		tableName := strings.ToLower(rv.Relname)

		// Check for schema qualification
		if v.checkSchemaAccess && rv.Schemaname != "" {
			schemaName := strings.ToLower(rv.Schemaname)
			if schemaName != "public" {
				return fmt.Errorf("access to schema '%s' is not allowed", rv.Schemaname)
			}
		}

		// Get alias
		alias := tableName
		if rv.Alias != nil && rv.Alias.Aliasname != "" {
			alias = strings.ToLower(rv.Alias.Aliasname)
		}
		tables[tableName] = alias
		return nil
	}

	// Handle JoinExpr (JOIN)
	if je := node.GetJoinExpr(); je != nil {
		if err := v.validateFromItem(je.Larg, tables, result); err != nil {
			return err
		}
		if err := v.validateFromItem(je.Rarg, tables, result); err != nil {
			return err
		}
		if je.Quals != nil {
			if err := v.validateNode(je.Quals, result); err != nil {
				return err
			}
		}
		return nil
	}

	// Handle RangeSubselect (subquery in FROM)
	if rss := node.GetRangeSubselect(); rss != nil {
		if v.checkSubqueries {
			return fmt.Errorf("subqueries in FROM clause are not allowed")
		}
		// SECURITY: Even when subqueries are permitted, recurse into the
		// subquery so dangerous constructs hidden inside it are still
		// validated. Without this, a FROM subquery like
		//   (SELECT * FROM read_text('/etc/passwd'))
		// smuggles a RangeFunction past the check below.
		return v.validateSubquery(rss.Subquery, tables, result)
	}

	// Handle RangeFunction (function in FROM)
	if node.GetRangeFunction() != nil {
		return fmt.Errorf("functions in FROM clause are not allowed")
	}

	return nil
}

// validateSubquery validates a SELECT statement nested in a FROM subquery.
// It reuses the FROM-item and expression validators so RangeFunction and
// dangerous function checks apply recursively to arbitrarily nested subqueries.
func (v *sqlValidator) validateSubquery(
	node *pg_query.Node,
	tables map[string]string,
	result *SQLValidationResult,
) error {
	if node == nil {
		return nil
	}
	sub := node.GetSelectStmt()
	if sub == nil {
		return nil
	}
	for _, fromItem := range sub.FromClause {
		if err := v.validateFromItem(fromItem, tables, result); err != nil {
			return err
		}
	}
	for _, target := range sub.TargetList {
		if err := v.validateNode(target, result); err != nil {
			return err
		}
	}
	if sub.WhereClause != nil {
		if err := v.validateNode(sub.WhereClause, result); err != nil {
			return err
		}
	}
	for _, groupBy := range sub.GroupClause {
		if err := v.validateNode(groupBy, result); err != nil {
			return err
		}
	}
	if sub.HavingClause != nil {
		if err := v.validateNode(sub.HavingClause, result); err != nil {
			return err
		}
	}
	return nil
}
