package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Usir233/GENYEE/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func outerJoinToolFixture(t *testing.T) (*DatabaseQueryTool, context.Context) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "joins.sqlite")), &gorm.Config{})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, db.Exec(`
 CREATE TABLE knowledge_bases(id TEXT,name TEXT,tenant_id INTEGER,is_temporary BOOLEAN,deleted_at TEXT);
 CREATE TABLE knowledges(id TEXT,title TEXT,tenant_id INTEGER,knowledge_base_id TEXT,deleted_at TEXT);
 CREATE TABLE chunks(id TEXT,tenant_id INTEGER,knowledge_base_id TEXT,knowledge_id TEXT,is_enabled BOOLEAN,deleted_at TEXT);
 INSERT INTO knowledge_bases VALUES
 ('empty','empty',1,false,NULL),('filled','filled',1,false,NULL),('deleted-only','deleted-only',1,false,NULL),
 ('restricted','restricted',1,false,NULL),('shared','shared',2,false,NULL),('hidden','hidden',1,true,NULL),
 ('deleted-kb','deleted-kb',1,false,'2026-01-01'),('outside','outside',1,false,NULL),('wrong-owner','wrong-owner',2,false,NULL);
 INSERT INTO knowledges VALUES
 ('visible-doc','visible',1,'filled',NULL),('no-enabled-doc','no enabled chunks',1,'filled',NULL),
 ('deleted-doc','deleted',1,'filled','2026-01-01'),('wrong-tenant-doc','wrong tenant',9,'filled',NULL),
 ('deleted-only-doc','deleted only',1,'deleted-only','2026-01-01'),
 ('allowed-doc','allowed',1,'restricted',NULL),('denied-doc','denied',1,'restricted',NULL),
 ('shared-doc','shared',2,'shared',NULL),('outside-doc','outside',1,'outside',NULL),('wrong-owner-doc','wrong owner',2,'wrong-owner',NULL);
 INSERT INTO chunks VALUES
 ('visible-chunk',1,'filled','visible-doc',true,NULL),('disabled-chunk',1,'filled','visible-doc',false,NULL),
 ('deleted-chunk',1,'filled','visible-doc',true,'2026-01-01'),('wrong-tenant-chunk',9,'filled','visible-doc',true,NULL),
 ('disabled-only',1,'filled','no-enabled-doc',false,NULL),('deleted-only',1,'filled','no-enabled-doc',true,'2026-01-01'),
 ('shared-chunk',2,'shared','shared-doc',true,NULL),('denied-chunk',1,'restricted','denied-doc',true,NULL),
 ('outside-chunk',1,'outside','outside-doc',true,NULL),('wrong-owner-chunk',2,'wrong-owner','wrong-owner-doc',true,NULL);
 `).Error)
	targets := types.SearchTargets{}
	for _, id := range []string{"empty", "filled", "deleted-only", "hidden", "deleted-kb", "wrong-owner"} {
		targets = append(targets, &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: id, TenantID: 1})
	}
	targets = append(targets, &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "shared", TenantID: 2},
		&types.SearchTarget{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "restricted", KnowledgeIDs: []string{"allowed-doc"}, TenantID: 1})
	return NewDatabaseQueryTool(db, targets), context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
}

func executeOuterJoinQuery(t *testing.T, tool *DatabaseQueryTool, ctx context.Context, sql string) []map[string]interface{} {
	t.Helper()
	input, err := json.Marshal(DatabaseQueryInput{SQL: sql})
	require.NoError(t, err)
	result, err := tool.Execute(ctx, input)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	rows, ok := result.Data["rows"].([]map[string]interface{})
	require.True(t, ok)
	return rows
}

func TestDatabaseQueryRelationExclusions(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	for _, tc := range []struct {
		table string
		want  []string
	}{
		{"knowledge_bases", []string{"deleted-only", "empty", "filled", "restricted", "shared"}},
		{"knowledges", []string{"allowed-doc", "no-enabled-doc", "shared-doc", "visible-doc"}},
		{"chunks", []string{"shared-chunk", "visible-chunk"}},
	} {
		t.Run(tc.table, func(t *testing.T) {
			rows := executeOuterJoinQuery(t, tool, ctx, "SELECT id FROM "+tc.table)
			var got []string
			for _, row := range rows {
				got = append(got, row["id"].(string))
			}
			sort.Strings(got)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDatabaseQueryOuterJoinDocumentCounts(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	for _, from := range []string{
		"knowledge_bases kb LEFT JOIN knowledges k ON kb.id = k.knowledge_base_id",
		"knowledges k RIGHT JOIN knowledge_bases kb ON kb.id = k.knowledge_base_id",
	} {
		t.Run(from, func(t *testing.T) {
			// Exact aggregate shape from DatabaseQueryTool's documented example.
			rows := executeOuterJoinQuery(t, tool, ctx, "SELECT kb.name AS kb_name, COUNT(k.id) AS doc_count FROM "+from+" GROUP BY kb.id, kb.name")
			got := map[string]int64{}
			for _, row := range rows {
				got[row["kb_name"].(string)] = row["doc_count"].(int64)
			}
			require.Equal(t, map[string]int64{"empty": 0, "deleted-only": 0, "filled": 2, "restricted": 1, "shared": 1}, got)
		})
	}
}

func TestDatabaseQueryOuterJoinChunkCounts(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	for _, from := range []string{
		"knowledges k LEFT JOIN chunks c ON k.id = c.knowledge_id",
		"chunks c RIGHT JOIN knowledges k ON k.id = c.knowledge_id",
	} {
		t.Run(from, func(t *testing.T) {
			rows := executeOuterJoinQuery(t, tool, ctx, "SELECT k.id AS doc_id, COUNT(c.id) AS chunk_count FROM "+from+" GROUP BY k.id")
			got := map[string]int64{}
			for _, row := range rows {
				got[row["doc_id"].(string)] = row["chunk_count"].(int64)
			}
			require.Equal(t, map[string]int64{"allowed-doc": 0, "no-enabled-doc": 0, "visible-doc": 1, "shared-doc": 1}, got)
		})
	}
}

func TestDatabaseQueryFullJoinPreservesBothNullableSides(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	rows := executeOuterJoinQuery(t, tool, ctx, `SELECT kb.id AS kb_id, k.id AS doc_id FROM knowledge_bases kb
 FULL JOIN knowledges k ON kb.id = k.knowledge_base_id AND k.id <> 'allowed-doc'`)
	var got []string
	for _, row := range rows {
		got = append(got, fmt.Sprintf("%v/%v", row["kb_id"], row["doc_id"]))
	}
	sort.Strings(got)
	want := []string{"<nil>/allowed-doc", "deleted-only/<nil>", "empty/<nil>", "filled/no-enabled-doc", "filled/visible-doc", "restricted/<nil>", "shared/shared-doc"}
	require.Equal(t, want, got)
}

func TestDatabaseQueryOuterJoinAliasCompatibility(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	require.NoError(t, tool.db.Exec(`ATTACH DATABASE ':memory:' AS public;
 CREATE TABLE public.knowledge_bases AS SELECT * FROM knowledge_bases;
 CREATE TABLE public.knowledges AS SELECT * FROM knowledges;`).Error)
	for _, query := range []string{
		`SELECT knowledge_bases.name AS kb_name, COUNT(knowledges.id) AS doc_count FROM knowledge_bases LEFT JOIN knowledges ON knowledge_bases.id = knowledges.knowledge_base_id GROUP BY knowledge_bases.id, knowledge_bases.name`,
		`SELECT "ORDER BY".name AS kb_name, COUNT("WHERE".id) AS doc_count FROM knowledge_bases "ORDER BY" LEFT JOIN knowledges "WHERE" ON "ORDER BY".id = "WHERE".knowledge_base_id GROUP BY "ORDER BY".id, "ORDER BY".name`,
		`SELECT public.knowledge_bases.name AS kb_name, COUNT(public.knowledges.id) AS doc_count FROM public.knowledge_bases LEFT JOIN public.knowledges ON public.knowledge_bases.id = public.knowledges.knowledge_base_id GROUP BY public.knowledge_bases.id, public.knowledge_bases.name`,
	} {
		t.Run(query, func(t *testing.T) {
			rows := executeOuterJoinQuery(t, tool, ctx, query)
			got := map[string]int64{}
			for _, row := range rows {
				got[row["kb_name"].(string)] = row["doc_count"].(int64)
			}
			require.Equal(t, map[string]int64{"empty": 0, "deleted-only": 0, "filled": 2, "restricted": 1, "shared": 1}, got)
		})
	}
}

func TestDatabaseQueryOuterJoinPreservesUserPredicate(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	for _, tc := range []struct {
		where string
		want  []string
	}{
		{"k.id IS NULL", []string{"deleted-only", "empty"}},
		{"k.id = 'visible-doc'", []string{"filled"}},
	} {
		t.Run(tc.where, func(t *testing.T) {
			rows := executeOuterJoinQuery(t, tool, ctx, "SELECT kb.id FROM knowledge_bases kb LEFT JOIN knowledges k ON kb.id=k.knowledge_base_id WHERE "+tc.where+" ORDER BY kb.id")
			var got []string
			for _, row := range rows {
				got = append(got, row["id"].(string))
			}
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDatabaseQueryNestedOuterJoinAndSelfJoin(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	rows := executeOuterJoinQuery(t, tool, ctx, `SELECT kb.id, COUNT(c.id) AS chunk_count FROM knowledge_bases kb
 LEFT JOIN (knowledges k LEFT JOIN chunks c ON k.id = c.knowledge_id) ON kb.id=k.knowledge_base_id GROUP BY kb.id`)
	got := map[string]int64{}
	for _, row := range rows {
		got[row["id"].(string)] = row["chunk_count"].(int64)
	}
	require.Equal(t, map[string]int64{"empty": 0, "deleted-only": 0, "filled": 1, "restricted": 0, "shared": 1}, got)
	rows = executeOuterJoinQuery(t, tool, ctx, `SELECT a.id, COUNT(b.id) AS peers FROM chunks a LEFT JOIN chunks b ON a.knowledge_id=b.knowledge_id AND a.id <> b.id GROUP BY a.id`)
	got = map[string]int64{}
	for _, row := range rows {
		got[row["id"].(string)] = row["peers"].(int64)
	}
	require.Equal(t, map[string]int64{"visible-chunk": 0, "shared-chunk": 0}, got)
}

func TestDatabaseQueryFullJoinExcludesDisabledAndDeletedUnmatchedRows(t *testing.T) {
	tool, ctx := outerJoinToolFixture(t)
	rows := executeOuterJoinQuery(t, tool, ctx, `SELECT k.id AS doc_id, c.id AS chunk_id FROM knowledges k
 FULL JOIN chunks c ON k.id=c.knowledge_id AND c.id <> 'visible-chunk'`)
	var got []string
	for _, row := range rows {
		got = append(got, fmt.Sprintf("%v/%v", row["doc_id"], row["chunk_id"]))
	}
	sort.Strings(got)
	require.Equal(t, []string{"<nil>/visible-chunk", "allowed-doc/<nil>", "no-enabled-doc/<nil>", "shared-doc/shared-chunk", "visible-doc/<nil>"}, got)
}
