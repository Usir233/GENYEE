#!/usr/bin/env python3
"""
Empirical Adversarial Stress Test Suite for Live Knowledge Base Hybrid Search
Target Knowledge Base: 历年资料 (ID: 6459d8b2-9059-49f9-83a9-ea5d65ec8bca)
Engines: PostgreSQL ParadeDB BM25 + Vector Search (RRF Hybrid Fusion)
"""

import json
import time
import subprocess
import urllib.request
import urllib.error
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE_URL = "http://127.0.0.1:8080"
KB_ID = "6459d8b2-9059-49f9-83a9-ea5d65ec8bca"  # 历年资料
TEST_EMAIL = "zhanshun.bao@gamil.com"
TEST_PASS = "mkRI3z$L5w%J#D&VxqeM"

def get_auth_token():
    payload = json.dumps({"email": TEST_EMAIL, "password": TEST_PASS}).encode()
    req = urllib.request.Request(
        f"{BASE_URL}/api/v1/auth/login",
        data=payload,
        headers={"Content-Type": "application/json"}
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.loads(resp.read().decode())
        assert data.get("success") is True, f"Login failed: {data}"
        return data["token"]

def get_pg_stats():
    cmd = [
        "docker", "exec", "GENYEE-postgres",
        "psql", "-U", "postgres", "-d", "WeKnora", "-t", "-A", "-c",
        "SELECT state, count(*) FROM pg_stat_activity WHERE datname='WeKnora' GROUP BY state;"
    ]
    out = subprocess.check_output(cmd).decode().strip()
    stats = {}
    for line in out.splitlines():
        if line.strip():
            parts = line.split("|")
            state = parts[0] if parts[0] else "background"
            stats[state] = int(parts[1])
    return stats

def get_docker_memory():
    cmd = [
        "docker", "stats", "--no-stream", "--format",
        "{{.Name}}:{{.MemUsage}}"
    ]
    out = subprocess.check_output(cmd).decode().strip()
    mem = {}
    for line in out.splitlines():
        if ":" in line:
            name, usage = line.split(":", 1)
            mem[name.strip()] = usage.strip().split("/")[0].strip()
    return mem

def execute_search(token, query_text, match_count=5, disable_vector=False, disable_kw=False):
    url = f"{BASE_URL}/api/v1/knowledge-bases/{KB_ID}/hybrid-search"
    body = {
        "query_text": query_text,
        "match_count": match_count,
        "disable_vector_match": disable_vector,
        "disable_keywords_match": disable_kw
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        }
    )
    start = time.perf_counter()
    try:
        with urllib.request.urlopen(req, timeout=15) as resp:
            elapsed = time.perf_counter() - start
            data = json.loads(resp.read().decode())
            return {
                "status": resp.status,
                "elapsed_sec": round(elapsed, 4),
                "success": data.get("success", False),
                "count": len(data.get("data", [])),
                "chunks": data.get("data", []),
                "error": None
            }
    except urllib.error.HTTPError as e:
        elapsed = time.perf_counter() - start
        err_body = e.read().decode()
        return {
            "status": e.code,
            "elapsed_sec": round(elapsed, 4),
            "success": False,
            "count": 0,
            "chunks": [],
            "error": err_body
        }
    except Exception as e:
        elapsed = time.perf_counter() - start
        return {
            "status": 0,
            "elapsed_sec": round(elapsed, 4),
            "success": False,
            "count": 0,
            "chunks": [],
            "error": str(e)
        }

def run_stress_suite():
    print("================================================================================")
    print("  GENYEE Live Hybrid Search Empirical Adversarial Stress Test Suite")
    print("================================================================================")
    
    token = get_auth_token()
    print("[INIT] Authenticated successfully. Bearer token acquired.")
    
    pg_stats_before = get_pg_stats()
    mem_before = get_docker_memory()
    print(f"[STATE BEFORE] PostgreSQL Connections: {pg_stats_before}")
    print(f"[STATE BEFORE] GENYEE-app Memory: {mem_before.get('GENYEE-app')}, GENYEE-postgres: {mem_before.get('GENYEE-postgres')}")

    # Boundary Test Cases
    test_cases = [
        {
            "name": "TC-HS-01: Baseline Chinese Domain Query (Hybrid)",
            "query": "2025年6月月度经营分析报告核心财务指标与毛利率变动",
            "disable_vector": False, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-02: Pure BM25 Keyword Search (Vector Disabled)",
            "query": "经营分析报告 营业收入 净利润",
            "disable_vector": True, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-03: Pure Vector Search (BM25 Disabled)",
            "query": "公司上一年度各季度的经营状况与盈利水平趋势",
            "disable_vector": False, "disable_kw": True, "expect_min": 1
        },
        {
            "name": "TC-HS-04: Boundary: Long Chinese Text (>600 characters)",
            "query": (
                "根据公司历年发布的月度与季度经营分析报告，请详细梳理自2024年1月至2025年6月期间，"
                "各个业务部门的主营业务收入、营业利润、各项成本费用的变动趋势以及在不同供应链周期下的"
                "毛利表现，重点分析报告中涉及的原材料采购价格波动、渠道拓展策略、团队扩张人效比以及"
                "针对下一季度的经营风险应对方案和管理层总结。" * 3
            ),
            "disable_vector": False, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-05: Boundary: Complex Punctuation & BM25 Syntax Characters",
            "query": '!(月度) AND "经营分析" OR [报告] +指标 -亏损 ~5 ^2 (AND OR NOT: / \\ * ? < > = % $ # @ !)',
            "disable_vector": False, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-06: Boundary: SQL Injection Payload in Search Query",
            "query": "月度经营分析' OR '1'='1' UNION SELECT NULL, NULL, NULL --",
            "disable_vector": False, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-07: Boundary: ParadeDB BM25 Lucene/Tantivy Syntax Breakers",
            "query": 'title:("经营分析" AND "报告") AND content:\\* OR id:[0 TO 999999] && || !',
            "disable_vector": False, "disable_kw": False, "expect_min": 0
        },
        {
            "name": "TC-HS-08: Boundary: Unicode Emojis & Zero-Width Spaces",
            "query": "📊📈2025年经营分析报告\u200B\u200C\u200D💰年度营收增长🚀💡",
            "disable_vector": False, "disable_kw": False, "expect_min": 1
        },
        {
            "name": "TC-HS-09: Boundary: Rare Chinese Characters & Heterogeneous Script",
            "query": "财务朏朒朤曚运营氍毹营收分析报告 2024-2025 FY Q1-Q4",
            "disable_vector": False, "disable_kw": False, "expect_min": 0
        },
        {
            "name": "TC-HS-10: Boundary: High match_count limit (30 chunks)",
            "query": "经营分析 财务报告 总结",
            "match_count": 30,
            "disable_vector": False, "disable_kw": False, "expect_min": 5
        }
    ]

    print("\n--------------------------------------------------------------------------------")
    print("  Phase 1: Sequential Boundary Scenarios Execution")
    print("--------------------------------------------------------------------------------")
    
    results = []
    all_passed = True
    for tc in test_cases:
        res = execute_search(
            token,
            tc["query"],
            match_count=tc.get("match_count", 5),
            disable_vector=tc["disable_vector"],
            disable_kw=tc["disable_kw"]
        )
        passed = (res["status"] == 200) and (res["count"] >= tc["expect_min"])
        if not passed:
            all_passed = False
        
        top_title = res["chunks"][0].get("knowledge_title", "N/A") if res["chunks"] else "N/A"
        top_score = res["chunks"][0].get("score", 0.0) if res["chunks"] else 0.0
        match_type = res["chunks"][0].get("match_type", "N/A") if res["chunks"] else "N/A"

        print(f"[{'PASS' if passed else 'FAIL'}] {tc['name']}")
        print(f"       Status: {res['status']} | Count: {res['count']} | Latency: {res['elapsed_sec']}s")
        print(f"       Top Match: {top_title} (score={top_score:.4f}, type={match_type})")
        if res["error"]:
            print(f"       Error: {res['error']}")
        results.append((tc, res, passed))

    print("\n--------------------------------------------------------------------------------")
    print("  Phase 2: Concurrent Multi-Thread Stress Test (15 Concurrent Hybrid Searches)")
    print("--------------------------------------------------------------------------------")
    concurrent_queries = [
        "2025年6月经营分析报告",
        "2024年10月度总结",
        "2025年1月财务分析",
        "原材料采购与供应链成本",
        "季度主营业务毛利率",
        "月度经营分析 与管理层决策",
        "现金流量表与资产负债",
        "销售渠道拓展与人效",
        "财务预算与年度决算对比",
        "2024年第四季度营收汇总",
        "2025年上半年业绩简报",
        "经营风险应对及策略规划",
        "核心指标月度变动明细",
        "毛利分析与费用控制",
        "年度总结与展望"
    ]
    
    concurrent_start = time.perf_counter()
    concurrent_res = []
    with ThreadPoolExecutor(max_workers=5) as executor:
        futures = {executor.submit(execute_search, token, q, 5, False, False): q for q in concurrent_queries}
        for f in as_completed(futures):
            q = futures[f]
            r = f.result()
            concurrent_res.append((q, r))
    concurrent_elapsed = time.perf_counter() - concurrent_start
    
    concurrent_success = sum(1 for _, r in concurrent_res if r["status"] == 200 and r["count"] > 0)
    print(f"[CONCURRENT] Executed {len(concurrent_queries)} queries in {concurrent_elapsed:.2f}s")
    print(f"[CONCURRENT] Successful 200 OK with Chunks: {concurrent_success}/{len(concurrent_queries)}")
    
    time.sleep(1) # Allow connection pool settle
    
    pg_stats_after = get_pg_stats()
    mem_after = get_docker_memory()
    print("\n--------------------------------------------------------------------------------")
    print("  Phase 3: Resource Stability & Connection Leak Verification")
    print("--------------------------------------------------------------------------------")
    print(f"[STATE AFTER] PostgreSQL Connections: {pg_stats_after}")
    print(f"[STATE AFTER] GENYEE-app Memory: {mem_after.get('GENYEE-app')}, GENYEE-postgres: {mem_after.get('GENYEE-postgres')}")

    # Check for active query leaks in postgres
    active_conns = pg_stats_after.get("active", 0)
    print(f"[LEAK CHECK] Active PostgreSQL Connections: {active_conns} (expected <= 1, no hanging queries)")
    
    # Summary
    print("\n================================================================================")
    print(f"  Stress Test Result: {'ALL PASS' if (all_passed and concurrent_success == len(concurrent_queries)) else 'FAIL'}")
    print("================================================================================")
    
    return all_passed and (concurrent_success == len(concurrent_queries))

if __name__ == "__main__":
    success = run_stress_suite()
    exit(0 if success else 1)
