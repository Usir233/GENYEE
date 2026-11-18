package utils

import (
	"fmt"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
)

// validateNode recursively validates AST nodes
// SECURITY: This function uses a COMPREHENSIVE approach to validate ALL node types.
// Any node type that contains child expressions MUST be handled to prevent bypass attacks.
// The principle is: if we don't know how to validate a node type, we REJECT it.
func (v *sqlValidator) validateNode(node *pg_query.Node, result *SQLValidationResult) error {
	if node == nil {
		return nil
	}

	// Check for subqueries (SubLink)
	if v.checkSubqueries {
		if sl := node.GetSubLink(); sl != nil {
			return fmt.Errorf("subqueries are not allowed")
		}
	}

	// Check for function calls
	if fc := node.GetFuncCall(); fc != nil {
		if err := v.validateFuncCall(fc, result); err != nil {
			return err
		}
	}

	// Check for column references
	if cr := node.GetColumnRef(); cr != nil {
		if err := v.validateColumnRef(cr); err != nil {
			return err
		}
	}

	// Check for type casts
	if tc := node.GetTypeCast(); tc != nil {
		if err := v.validateNode(tc.Arg, result); err != nil {
			return err
		}
		if tc.TypeName != nil {
			typeName := v.getTypeName(tc.TypeName)
			if strings.HasPrefix(strings.ToLower(typeName), "pg_") {
				// The parser qualifies standard scalar types (e.g. INTEGER) with pg_catalog.
				switch typeName {
				case "pg_catalog.int2", "pg_catalog.int4", "pg_catalog.int8",
					"pg_catalog.float4", "pg_catalog.float8", "pg_catalog.numeric",
					"pg_catalog.bool", "pg_catalog.bpchar", "pg_catalog.varchar", "pg_catalog.text",
					"pg_catalog.date", "pg_catalog.time", "pg_catalog.timetz",
					"pg_catalog.timestamp", "pg_catalog.timestamptz", "pg_catalog.interval":
					if len(tc.TypeName.Names) != 2 {
						return fmt.Errorf("casting to system type '%s' is not allowed", typeName)
					}
				default:
					return fmt.Errorf("casting to system type '%s' is not allowed", typeName)
				}
			}
			for _, modifier := range tc.TypeName.Typmods {
				if err := v.validateNode(modifier, result); err != nil {
					return err
				}
			}
		}
	}

	// Recursively check A_Expr (expressions)
	if ae := node.GetAExpr(); ae != nil {
		if err := v.validateNode(ae.Lexpr, result); err != nil {
			return err
		}
		if err := v.validateNode(ae.Rexpr, result); err != nil {
			return err
		}
	}

	// Check BoolExpr (AND, OR, NOT)
	if be := node.GetBoolExpr(); be != nil {
		for _, arg := range be.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Check NullTest
	if nt := node.GetNullTest(); nt != nil {
		if err := v.validateNode(nt.Arg, result); err != nil {
			return err
		}
	}

	// Check CoalesceExpr
	if ce := node.GetCoalesceExpr(); ce != nil {
		for _, arg := range ce.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Check CaseExpr
	if caseExpr := node.GetCaseExpr(); caseExpr != nil {
		if err := v.validateNode(caseExpr.Arg, result); err != nil {
			return err
		}
		for _, when := range caseExpr.Args {
			if err := v.validateNode(when, result); err != nil {
				return err
			}
		}
		if err := v.validateNode(caseExpr.Defresult, result); err != nil {
			return err
		}
	}

	// Check CaseWhen
	if cw := node.GetCaseWhen(); cw != nil {
		if err := v.validateNode(cw.Expr, result); err != nil {
			return err
		}
		if err := v.validateNode(cw.Result, result); err != nil {
			return err
		}
	}

	// Check ResTarget (SELECT list items)
	if rt := node.GetResTarget(); rt != nil {
		if err := v.validateNode(rt.Val, result); err != nil {
			return err
		}
	}

	// Check SortBy (ORDER BY items)
	if sb := node.GetSortBy(); sb != nil {
		if err := v.validateNode(sb.Node, result); err != nil {
			return err
		}
	}

	// Check List
	if list := node.GetList(); list != nil {
		for _, item := range list.Items {
			if err := v.validateNode(item, result); err != nil {
				return err
			}
		}
	}

	// ============================================================
	// SECURITY FIX: Comprehensive handling of ALL expression types
	// that can contain child nodes (potential bypass vectors)
	// ============================================================

	// ArrayExpr (ARRAY[...] expressions)
	// Attack: SELECT ARRAY[pg_read_file('/etc/passwd')] FROM table
	if ae := node.GetAArrayExpr(); ae != nil {
		for _, elem := range ae.Elements {
			if err := v.validateNode(elem, result); err != nil {
				return err
			}
		}
	}

	// RowExpr (ROW(...) expressions)
	// Attack: SELECT ROW(pg_read_file('/etc/passwd')) FROM table
	if re := node.GetRowExpr(); re != nil {
		for _, arg := range re.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// MinMaxExpr (GREATEST/LEAST expressions)
	if mm := node.GetMinMaxExpr(); mm != nil {
		for _, arg := range mm.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// NullIfExpr (NULLIF expressions)
	if ni := node.GetNullIfExpr(); ni != nil {
		for _, arg := range ni.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ScalarArrayOpExpr (IN, ANY, ALL with arrays)
	if sao := node.GetScalarArrayOpExpr(); sao != nil {
		for _, arg := range sao.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ArrayCoerceExpr
	if ace := node.GetArrayCoerceExpr(); ace != nil {
		if err := v.validateNode(ace.Arg, result); err != nil {
			return err
		}
	}

	// CoerceViaIO (type coercion via I/O)
	if cvi := node.GetCoerceViaIo(); cvi != nil {
		if err := v.validateNode(cvi.Arg, result); err != nil {
			return err
		}
	}

	// CollateExpr (COLLATE expressions)
	if ce := node.GetCollateExpr(); ce != nil {
		if err := v.validateNode(ce.Arg, result); err != nil {
			return err
		}
	}

	// SubLink (subqueries) - validate child expressions even if subqueries are allowed
	if sl := node.GetSubLink(); sl != nil {
		if err := v.validateNode(sl.Testexpr, result); err != nil {
			return err
		}
	}

	// OpExpr (operator expressions)
	if oe := node.GetOpExpr(); oe != nil {
		for _, arg := range oe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// DistinctExpr (IS DISTINCT FROM)
	if de := node.GetDistinctExpr(); de != nil {
		for _, arg := range de.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// XmlExpr (XML expressions)
	if xe := node.GetXmlExpr(); xe != nil {
		for _, arg := range xe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range xe.NamedArgs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonConstructorExpr
	if jce := node.GetJsonConstructorExpr(); jce != nil {
		for _, arg := range jce.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// ============================================================
	// Additional expression types that need recursive validation
	// ============================================================

	// FuncExpr (different from FuncCall - internal function representation)
	if fe := node.GetFuncExpr(); fe != nil {
		for _, arg := range fe.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// Aggref (aggregate function reference)
	if ag := node.GetAggref(); ag != nil {
		for _, arg := range ag.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range ag.Aggdirectargs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		if ag.Aggfilter != nil {
			if err := v.validateNode(ag.Aggfilter, result); err != nil {
				return err
			}
		}
	}

	// WindowFunc
	if wf := node.GetWindowFunc(); wf != nil {
		for _, arg := range wf.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		if wf.Aggfilter != nil {
			if err := v.validateNode(wf.Aggfilter, result); err != nil {
				return err
			}
		}
	}

	// SubscriptingRef (array subscripting like arr[1])
	if sr := node.GetSubscriptingRef(); sr != nil {
		for _, idx := range sr.Refupperindexpr {
			if err := v.validateNode(idx, result); err != nil {
				return err
			}
		}
		for _, idx := range sr.Reflowerindexpr {
			if err := v.validateNode(idx, result); err != nil {
				return err
			}
		}
		if err := v.validateNode(sr.Refexpr, result); err != nil {
			return err
		}
		if err := v.validateNode(sr.Refassgnexpr, result); err != nil {
			return err
		}
	}

	// NamedArgExpr (named arguments in function calls)
	if nae := node.GetNamedArgExpr(); nae != nil {
		if err := v.validateNode(nae.Arg, result); err != nil {
			return err
		}
	}

	// FieldSelect (field selection from composite type)
	if fs := node.GetFieldSelect(); fs != nil {
		if err := v.validateNode(fs.Arg, result); err != nil {
			return err
		}
	}

	// FieldStore
	if fs := node.GetFieldStore(); fs != nil {
		if err := v.validateNode(fs.Arg, result); err != nil {
			return err
		}
		for _, newval := range fs.Newvals {
			if err := v.validateNode(newval, result); err != nil {
				return err
			}
		}
	}

	// RelabelType (type relabeling)
	if rt := node.GetRelabelType(); rt != nil {
		if err := v.validateNode(rt.Arg, result); err != nil {
			return err
		}
	}

	// ConvertRowtypeExpr
	if cre := node.GetConvertRowtypeExpr(); cre != nil {
		if err := v.validateNode(cre.Arg, result); err != nil {
			return err
		}
	}

	// RowCompareExpr
	if rce := node.GetRowCompareExpr(); rce != nil {
		for _, arg := range rce.Largs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, arg := range rce.Rargs {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// CoerceToDomain
	if ctd := node.GetCoerceToDomain(); ctd != nil {
		if err := v.validateNode(ctd.Arg, result); err != nil {
			return err
		}
	}

	// BooleanTest (IS TRUE, IS FALSE, etc.)
	if bt := node.GetBooleanTest(); bt != nil {
		if err := v.validateNode(bt.Arg, result); err != nil {
			return err
		}
	}

	// AIndices (array indices)
	if ai := node.GetAIndices(); ai != nil {
		if err := v.validateNode(ai.Lidx, result); err != nil {
			return err
		}
		if err := v.validateNode(ai.Uidx, result); err != nil {
			return err
		}
	}

	// AIndirection (array/field indirection)
	if aind := node.GetAIndirection(); aind != nil {
		if err := v.validateNode(aind.Arg, result); err != nil {
			return err
		}
		for _, ind := range aind.Indirection {
			if err := v.validateNode(ind, result); err != nil {
				return err
			}
		}
	}

	// CollateClause
	if cc := node.GetCollateClause(); cc != nil {
		if err := v.validateNode(cc.Arg, result); err != nil {
			return err
		}
	}

	// GroupingFunc
	if gf := node.GetGroupingFunc(); gf != nil {
		for _, arg := range gf.Args {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonValueExpr
	if jve := node.GetJsonValueExpr(); jve != nil {
		if err := v.validateNode(jve.RawExpr, result); err != nil {
			return err
		}
		if err := v.validateNode(jve.FormattedExpr, result); err != nil {
			return err
		}
	}

	// JsonExpr
	if je := node.GetJsonExpr(); je != nil {
		if err := v.validateNode(je.FormattedExpr, result); err != nil {
			return err
		}
		if err := v.validateNode(je.PathSpec, result); err != nil {
			return err
		}
		for _, arg := range je.PassingValues {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
	}

	// JsonIsPredicate
	if jip := node.GetJsonIsPredicate(); jip != nil {
		if err := v.validateNode(jip.Expr, result); err != nil {
			return err
		}
	}

	// JsonScalarExpr (JSON_SCALAR(...)) - PG17 SQL/JSON node.
	// Attack: SELECT JSON_SCALAR(pg_read_file('/etc/passwd')) FROM table
	if jse := node.GetJsonScalarExpr(); jse != nil {
		if err := v.validateNode(jse.Expr, result); err != nil {
			return err
		}
	}

	// JsonFuncExpr (JSON_VALUE / JSON_QUERY / JSON_EXISTS(...)) - PG17 SQL/JSON node.
	// Attack: SELECT JSON_VALUE(pg_read_file('/etc/passwd'), '$') FROM table
	if jfe := node.GetJsonFuncExpr(); jfe != nil {
		if err := v.validateJSONValueExpr(jfe.ContextItem, result); err != nil {
			return err
		}
		if err := v.validateNode(jfe.Pathspec, result); err != nil {
			return err
		}
		for _, arg := range jfe.Passing {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		if err := v.validateJSONBehavior(jfe.OnEmpty, result); err != nil {
			return err
		}
		if err := v.validateJSONBehavior(jfe.OnError, result); err != nil {
			return err
		}
	}

	// JsonParseExpr (JSON(...)) - PG17 SQL/JSON node.
	if jpe := node.GetJsonParseExpr(); jpe != nil {
		if err := v.validateJSONValueExpr(jpe.Expr, result); err != nil {
			return err
		}
	}

	// JsonSerializeExpr (JSON_SERIALIZE(...)) - PG17 SQL/JSON node.
	if jse := node.GetJsonSerializeExpr(); jse != nil {
		if err := v.validateJSONValueExpr(jse.Expr, result); err != nil {
			return err
		}
	}

	// JsonTable (JSON_TABLE(...)) - PG17 SQL/JSON node.
	if jt := node.GetJsonTable(); jt != nil {
		if err := v.validateJSONValueExpr(jt.ContextItem, result); err != nil {
			return err
		}
		for _, arg := range jt.Passing {
			if err := v.validateNode(arg, result); err != nil {
				return err
			}
		}
		for _, col := range jt.Columns {
			if err := v.validateNode(col, result); err != nil {
				return err
			}
		}
		if err := v.validateJSONBehavior(jt.OnError, result); err != nil {
			return err
		}
	}

	// XmlSerialize
	if xs := node.GetXmlSerialize(); xs != nil {
		if err := v.validateNode(xs.Expr, result); err != nil {
			return err
		}
	}

	// WindowDef
	if wd := node.GetWindowDef(); wd != nil {
		if err := v.validateWindowDef(wd, result); err != nil {
			return err
		}
	}

	// SubPlan - BLOCK: This is an internal representation, should not appear in user queries
	if node.GetSubPlan() != nil {
		return fmt.Errorf("SubPlan nodes are not allowed")
	}

	// AlternativeSubPlan - BLOCK
	if node.GetAlternativeSubPlan() != nil {
		return fmt.Errorf("AlternativeSubPlan nodes are not allowed")
	}

	// ============================================================
	// SECURITY: DEFAULT-DENY.
	// The recursive branches above validate every expression type we know how
	// to inspect. Any node type NOT in the recognized set below is REJECTED,
	// because an unhandled type can smuggle a dangerous FuncCall past the
	// blacklist (e.g. PG17's JSON_SCALAR wrapping pg_read_file). Whenever a new
	// recursive handler is added above, add its Node_* wrapper here as well.
	// ============================================================
	switch node.Node.(type) {
	case
		// Recognized expression types (validated recursively above).
		*pg_query.Node_FuncCall,
		*pg_query.Node_FuncExpr,
		*pg_query.Node_ColumnRef,
		*pg_query.Node_TypeCast,
		*pg_query.Node_AExpr,
		*pg_query.Node_OpExpr,
		*pg_query.Node_BoolExpr,
		*pg_query.Node_NullTest,
		*pg_query.Node_BooleanTest,
		*pg_query.Node_CoalesceExpr,
		*pg_query.Node_CaseExpr,
		*pg_query.Node_CaseWhen,
		*pg_query.Node_ResTarget,
		*pg_query.Node_SortBy,
		*pg_query.Node_List,
		*pg_query.Node_AArrayExpr,
		*pg_query.Node_RowExpr,
		*pg_query.Node_MinMaxExpr,
		*pg_query.Node_NullIfExpr,
		*pg_query.Node_ScalarArrayOpExpr,
		*pg_query.Node_ArrayCoerceExpr,
		*pg_query.Node_CoerceViaIo,
		*pg_query.Node_CollateExpr,
		*pg_query.Node_CollateClause,
		*pg_query.Node_SubLink,
		*pg_query.Node_DistinctExpr,
		*pg_query.Node_XmlExpr,
		*pg_query.Node_XmlSerialize,
		*pg_query.Node_JsonConstructorExpr,
		*pg_query.Node_JsonValueExpr,
		*pg_query.Node_JsonExpr,
		*pg_query.Node_JsonIsPredicate,
		*pg_query.Node_JsonScalarExpr,
		*pg_query.Node_JsonFuncExpr,
		*pg_query.Node_JsonParseExpr,
		*pg_query.Node_JsonSerializeExpr,
		*pg_query.Node_JsonTable,
		*pg_query.Node_Aggref,
		*pg_query.Node_WindowFunc,
		*pg_query.Node_WindowDef,
		*pg_query.Node_GroupingFunc,
		*pg_query.Node_SubscriptingRef,
		*pg_query.Node_NamedArgExpr,
		*pg_query.Node_FieldSelect,
		*pg_query.Node_FieldStore,
		*pg_query.Node_RelabelType,
		*pg_query.Node_ConvertRowtypeExpr,
		*pg_query.Node_RowCompareExpr,
		*pg_query.Node_CoerceToDomain,
		*pg_query.Node_AIndices,
		*pg_query.Node_AIndirection,
		// Recognized safe leaf nodes (no child expressions to smuggle through).
		*pg_query.Node_AConst,
		*pg_query.Node_ParamRef,
		*pg_query.Node_SetToDefault,
		*pg_query.Node_CurrentOfExpr,
		*pg_query.Node_CaseTestExpr,
		*pg_query.Node_SqlvalueFunction,
		*pg_query.Node_AStar,
		*pg_query.Node_Integer,
		*pg_query.Node_Float,
		*pg_query.Node_Boolean,
		*pg_query.Node_String_,
		*pg_query.Node_BitString:
		return nil
	default:
		return fmt.Errorf("unsupported SQL expression type %T is not allowed", node.Node)
	}
}

// validateJSONValueExpr validates a JsonValueExpr, which appears as a concrete
// (non-Node) field on several PG17 SQL/JSON expression nodes. Its RawExpr /
// FormattedExpr children can hold arbitrary expressions (including FuncCalls),
// so they must be recursed into.
func (v *sqlValidator) validateJSONValueExpr(jve *pg_query.JsonValueExpr, result *SQLValidationResult) error {
	if jve == nil {
		return nil
	}
	if err := v.validateNode(jve.RawExpr, result); err != nil {
		return err
	}
	return v.validateNode(jve.FormattedExpr, result)
}

// validateJSONBehavior validates the ON EMPTY / ON ERROR behavior of a PG17
// SQL/JSON function. The DEFAULT branch can carry an arbitrary expression.
func (v *sqlValidator) validateJSONBehavior(jb *pg_query.JsonBehavior, result *SQLValidationResult) error {
	if jb == nil {
		return nil
	}
	return v.validateNode(jb.Expr, result)
}

func (v *sqlValidator) validateWindowDef(wd *pg_query.WindowDef, result *SQLValidationResult) error {
	if wd == nil {
		return nil
	}
	for _, part := range wd.PartitionClause {
		if err := v.validateNode(part, result); err != nil {
			return err
		}
	}
	for _, order := range wd.OrderClause {
		if err := v.validateNode(order, result); err != nil {
			return err
		}
	}
	if err := v.validateNode(wd.StartOffset, result); err != nil {
		return err
	}
	return v.validateNode(wd.EndOffset, result)
}

// validateFuncCall validates a function call
func (v *sqlValidator) validateFuncCall(fc *pg_query.FuncCall, result *SQLValidationResult) error {
	// Get function name
	funcName := ""
	for _, namePart := range fc.Funcname {
		if s := namePart.GetString_(); s != nil {
			funcName = strings.ToLower(s.Sval)
		}
	}

	// Check for schema-qualified function calls
	if v.checkSchemaAccess && len(fc.Funcname) > 1 {
		schemaName := ""
		if s := fc.Funcname[0].GetString_(); s != nil {
			schemaName = strings.ToLower(s.Sval)
		}
		if schemaName != "" && schemaName != "pg_catalog" {
			return fmt.Errorf("schema-qualified function calls are not allowed: %s", schemaName)
		}
	}

	// Block dangerous function prefixes
	if v.checkDangerousFuncs {
		dangerousPrefixes := []string{
			"pg_",     // All pg_* functions (pg_read_file, pg_reload_conf, pg_stat_*, etc.)
			"lo_",     // Large object functions (lo_import, lo_export, lo_from_bytea, lo_put, etc.)
			"dblink",  // Database link functions
			"file_",   // File functions
			"copy_",   // Copy functions
			"binary_", // Binary functions
		}
		for _, prefix := range dangerousPrefixes {
			if strings.HasPrefix(funcName, prefix) {
				return fmt.Errorf("function '%s' is not allowed (dangerous prefix)", funcName)
			}
		}

		// Block specific dangerous functions - comprehensive list for RCE prevention
		dangerousFunctions := map[string]bool{
			// Configuration and settings
			"current_setting": true,
			"set_config":      true,

			// XML/XPath functions (XXE risks)
			"query_to_xml":       true,
			"xpath":              true,
			"xmlparse":           true,
			"xmlroot":            true,
			"xmlelement":         true,
			"xmlforest":          true,
			"xmlconcat":          true,
			"xmlagg":             true,
			"xmlpi":              true,
			"xmlcomment":         true,
			"xmlexists":          true,
			"xml_is_well_formed": true,
			"xpath_exists":       true,
			"table_to_xml":       true,
			"cursor_to_xml":      true,
			"database_to_xml":    true,
			"schema_to_xml":      true,

			// Transaction and system info
			"txid_current":          true,
			"txid_current_snapshot": true,
			"txid_snapshot_xmin":    true,
			"txid_snapshot_xmax":    true,

			// Encoding functions (used in attack payloads)
			"encode": true,
			"decode": true,

			// Extension management
			"create_extension": true,

			// Copy operations
			"copy":          true,
			"copy_to":       true,
			"copy_from":     true,
			"pg_copy_to":    true,
			"pg_dump":       true,
			"pg_dumpall":    true,
			"pg_restore":    true,
			"pg_basebackup": true,

			// Process and system functions
			"pg_terminate_backend": true,
			"pg_cancel_backend":    true,
			"pg_rotate_logfile":    true,

			// Advisory locks (can be abused for DoS)
			"pg_advisory_lock":            true,
			"pg_advisory_unlock":          true,
			"pg_advisory_lock_shared":     true,
			"pg_advisory_unlock_shared":   true,
			"pg_try_advisory_lock":        true,
			"pg_try_advisory_lock_shared": true,

			// Backup and replication
			"pg_start_backup":         true,
			"pg_stop_backup":          true,
			"pg_switch_wal":           true,
			"pg_create_restore_point": true,

			// Foreign data wrappers
			"postgres_fdw_handler": true,
			"file_fdw_handler":     true,

			// Procedural languages (code execution)
			"plpgsql_call_handler":  true,
			"plpython_call_handler": true,
			"plperl_call_handler":   true,

			// System catalog modification
			"pg_catalog":         true,
			"information_schema": true,

			// DuckDB file-access functions (data_analysis tool runs on DuckDB).
			// The user's data is already loaded into a session table, so these
			// arbitrary-path readers are never legitimately needed and would
			// allow reading any file on the app container.
			"read_text":         true,
			"read_blob":         true,
			"read_csv":          true,
			"read_csv_auto":     true,
			"read_parquet":      true,
			"read_json":         true,
			"read_json_auto":    true,
			"read_ndjson":       true,
			"read_ndjson_auto":  true,
			"read_json_objects": true,
			"read_xlsx":         true,
			"sniff_csv":         true,
			"glob":              true,
			"st_read":           true,
			"st_read_meta":      true,
		}
		if dangerousFunctions[funcName] {
			return fmt.Errorf("function '%s' is not allowed", funcName)
		}
	}

	// Check against whitelist if enabled
	if v.checkFunctionNames && !v.allowedFunctions[funcName] {
		return fmt.Errorf("function not allowed: %s", funcName)
	}

	// Validate function arguments, aggregate ORDER BY / FILTER, and OVER window clauses recursively.
	for _, arg := range fc.Args {
		if err := v.validateNode(arg, result); err != nil {
			return err
		}
	}
	for _, ord := range fc.AggOrder {
		if err := v.validateNode(ord, result); err != nil {
			return err
		}
	}
	if fc.AggFilter != nil {
		if err := v.validateNode(fc.AggFilter, result); err != nil {
			return err
		}
	}
	if fc.Over != nil {
		if err := v.validateWindowDef(fc.Over, result); err != nil {
			return err
		}
	}

	return nil
}

// validateColumnRef validates a column reference
func (v *sqlValidator) validateColumnRef(cr *pg_query.ColumnRef) error {
	if !v.checkSystemColumns {
		return nil
	}

	// Check for system column access
	for _, field := range cr.Fields {
		if s := field.GetString_(); s != nil {
			colName := strings.ToLower(s.Sval)
			// Block access to system columns
			systemColumns := []string{"xmin", "xmax", "cmin", "cmax", "ctid", "tableoid"}
			for _, sysCol := range systemColumns {
				if colName == sysCol {
					return fmt.Errorf("access to system column '%s' is not allowed", colName)
				}
			}
			// Block pg_ prefixed identifiers
			if strings.HasPrefix(colName, "pg_") {
				return fmt.Errorf("access to '%s' is not allowed", colName)
			}
		}
	}
	return nil
}

// getTypeName extracts the type name from a TypeName node
func (v *sqlValidator) getTypeName(tn *pg_query.TypeName) string {
	var parts []string
	for _, name := range tn.Names {
		if s := name.GetString_(); s != nil {
			parts = append(parts, s.Sval)
		}
	}
	return strings.Join(parts, ".")
}
