#!/usr/bin/env python3
"""
Empirical Adversarial Stress Test Suite for Live Knowledge Base Hybrid Search
Target Knowledge Base: 企业资料归档 (ID: 6459d8b2-9059-49f9-83a9-ea5d65ec8bca)
Engines: PostgreSQL ParadeDB BM25 + Vector Search (RRF Hybrid Fusion)
"""

import json
import os
import subprocess
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:8080")
KB_ID = os.environ.get(
    "TEST_KB_ID", "6459d8b2-9059-49f9-83a9-ea5d65ec8bca"
)  # 企业资料归档
TEST_EMAIL = os.environ.get("TEST_EMAIL", "qa-test@example.com")
TEST_PASS = os.environ.get("TEST_PASSWORD", "ChangeMe_TestPass123!")


def get_auth_token():
    payload = json.dumps({"email": TEST_EMAIL, "password": TEST_PASS}).encode()
    req = urllib.request.Request(
        f"{BASE_URL}/api/v1/auth/login",
        data=payload,
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        data = json.loads(resp.read().decode())
        assert data.get("success") is True, f"Login failed: {data}"
        return data["token"]


DB_NAME = os.environ.get("DB_NAME", "GENYEE")


def get_pg_stats():
    cmd = [
        "docker",
        "exec",
        "GENYEE-postgres",
        "psql",
        "-U",
        "postgres",
        "-d",
        DB_NAME,
        "-t",
        "-A",
        "-c",
        f"SELECT state, count(*) FROM pg_stat_activity WHERE datname='{DB_NAME}' GROUP BY state;",
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
    cmd = ["docker", "stats", "--no-stream", "--format", "{{.Name}}:{{.MemUsage}}"]
    out = subprocess.check_output(cmd).decode().strip()
    mem = {}
    for line in out.splitlines():
        if ":" in line:
            name, usage = line.split(":", 1)
            mem[name.strip()] = usage.strip().split("/")[0].strip()
    return mem


def execute_search(
    token, query_text, match_count=5, disable_vector=False, disable_kw=False
):
    url = f"{BASE_URL}/api/v1/knowledge-bases/{KB_ID}/hybrid-search"
    body = {
        "query_text": query_text,
        "match_count": match_count,
        "disable_vector_match": disable_vector,
        "disable_keywords_match": disable_kw,
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode(),
        headers={
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json",
        },
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
                "error": None,
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
            "error": err_body,
        }
    except (urllib.error.URLError, TimeoutError, OSError, ValueError) as e:
        elapsed = time.perf_counter() - start
        return {
            "status": 0,
            "elapsed_sec": round(elapsed, 4),
            "success": False,
            "count": 0,
            "chunks": [],
            "error": str(e),
        }


def run_stress_suite():
    print(
        "================================================================================"
    )
    print("  GENYEE Live Hybrid Search Empirical Adversarial Stress Test Suite")
    print(
        "================================================================================"
    )

    token = get_auth_token()
    print("[INIT] Authenticated successfully. Bearer token acquired.")

    pg_stats_before = get_pg_stats()
    mem_before = get_docker_memory()
    print(f"[STATE BEFORE] PostgreSQL Connections: {pg_stats_before}")
    print(
        f"[STATE BEFORE] GENYEE-app Memory: {mem_before.get('GENYEE-app')}, GENYEE-postgres: {mem_before.get('GENYEE-postgres')}"
    )

    # Boundary Test Cases
    test_cases = [
        {
            "name": "TC-HS-01: Baseline Chinese Domain Query (Hybrid)",
            "query": "GENYEE系统架构规范中混合检索与重排序的核心机制",
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-02: Pure BM25 Keyword Search (Vector Disabled)",
            "query": "知识库 文档分块 向量检索",
            "disable_vector": True,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-03: Pure Vector Search (BM25 Disabled)",
            "query": "企业级私有化部署的高可用与多租户安全隔离策略",
            "disable_vector": False,
            "disable_kw": True,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-04: Boundary: Long Chinese Text (>600 characters)",
            "query": (
                "根据企业知识库技术架构手册与运维指南，请详细梳理在私有化容器集群部署环境下，"
                "文档解析服务、向量检索引擎、混合重排序流水线以及智能体沙箱各组件之间的协同调用关系，"
                "重点说明在大规模并发检索场景下的连接池配置、缓存淘汰策略、多模态图文解析吞吐调优以及"
                "针对跨租户权限隔离与接口高可用容灾的运维保障方案。" * 3
            ),
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-05: Boundary: Complex Punctuation & BM25 Syntax Characters",
            "query": '!(草稿) AND "架构规范" OR [手册] +检索 -废弃 ~5 ^2 (AND OR NOT: / \\ * ? < > = % $ # @ !)',
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-06: Boundary: SQL Injection Payload in Search Query",
            "query": "系统架构规范' OR '1'='1' UNION SELECT NULL, NULL, NULL --",
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-07: Boundary: ParadeDB BM25 Lucene/Tantivy Syntax Breakers",
            "query": 'title:("架构规范" AND "手册") AND content:\\* OR id:[0 TO 999999] && || !',
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 0,
        },
        {
            "name": "TC-HS-08: Boundary: Unicode Emojis & Zero-Width Spaces",
            "query": "📊📈GENYEE系统架构规范\u200b\u200c\u200d💰混合检索性能调优🚀💡",
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 1,
        },
        {
            "name": "TC-HS-09: Boundary: Rare Chinese Characters & Heterogeneous Script",
            "query": "架构朏朒朤曚检索氍毹技术规范手册 v1.0-v2.0 SLA",
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 0,
        },
        {
            "name": "TC-HS-10: Boundary: High match_count limit (30 chunks)",
            "query": "系统架构 混合检索 部署指南",
            "match_count": 30,
            "disable_vector": False,
            "disable_kw": False,
            "expect_min": 5,
        },
    ]

    print(
        "\n--------------------------------------------------------------------------------"
    )
    print("  Phase 1: Sequential Boundary Scenarios Execution")
    print(
        "--------------------------------------------------------------------------------"
    )

    results = []
    all_passed = True
    for tc in test_cases:
        res = execute_search(
            token,
            tc["query"],
            match_count=tc.get("match_count", 5),
            disable_vector=tc["disable_vector"],
            disable_kw=tc["disable_kw"],
        )
        passed = (res["status"] == 200) and (res["count"] >= tc["expect_min"])
        if not passed:
            all_passed = False

        top_title = (
            res["chunks"][0].get("knowledge_title", "N/A") if res["chunks"] else "N/A"
        )
        top_score = res["chunks"][0].get("score", 0.0) if res["chunks"] else 0.0
        match_type = (
            res["chunks"][0].get("match_type", "N/A") if res["chunks"] else "N/A"
        )

        print(f"[{'PASS' if passed else 'FAIL'}] {tc['name']}")
        print(
            f"       Status: {res['status']} | Count: {res['count']} | Latency: {res['elapsed_sec']}s"
        )
        print(
            f"       Top Match: {top_title} (score={top_score:.4f}, type={match_type})"
        )
        if res["error"]:
            print(f"       Error: {res['error']}")
        results.append((tc, res, passed))

    print(
        "\n--------------------------------------------------------------------------------"
    )
    print(
        "  Phase 2: Concurrent Multi-Thread Stress Test (15 Concurrent Hybrid Searches)"
    )
    print(
        "--------------------------------------------------------------------------------"
    )
    concurrent_queries = [
        "系统架构与模块划分",
        "向量数据库索引配置",
        "BM25全文检索分词机制",
        "多模态文档OCR解析流程",
        "混合检索重排序阈值",
        "智能体工具调用与沙箱隔离",
        "PostgreSQL连接池参数调优",
        "多租户API密钥权限控制",
        "知识图谱实体关系抽取",
        "大文件分块上传与切片策略",
        "高可用集群部署与容灾备份",
        "私有化内网离线镜像导入",
        "检索召回率与评估指标说明",
        "流式问答SSE会话状态管理",
        "运维监控日志与审计规范",
    ]

    concurrent_start = time.perf_counter()
    concurrent_res = []
    with ThreadPoolExecutor(max_workers=5) as executor:
        futures = {
            executor.submit(execute_search, token, q, 5, False, False): q
            for q in concurrent_queries
        }
        for f in as_completed(futures):
            q = futures[f]
            r = f.result()
            concurrent_res.append((q, r))
    concurrent_elapsed = time.perf_counter() - concurrent_start

    concurrent_success = sum(
        1 for _, r in concurrent_res if r["status"] == 200 and r["count"] > 0
    )
    print(
        f"[CONCURRENT] Executed {len(concurrent_queries)} queries in {concurrent_elapsed:.2f}s"
    )
    print(
        f"[CONCURRENT] Successful 200 OK with Chunks: {concurrent_success}/{len(concurrent_queries)}"
    )

    time.sleep(1)  # Allow connection pool settle

    pg_stats_after = get_pg_stats()
    mem_after = get_docker_memory()
    print(
        "\n--------------------------------------------------------------------------------"
    )
    print("  Phase 3: Resource Stability & Connection Leak Verification")
    print(
        "--------------------------------------------------------------------------------"
    )
    print(f"[STATE AFTER] PostgreSQL Connections: {pg_stats_after}")
    print(
        f"[STATE AFTER] GENYEE-app Memory: {mem_after.get('GENYEE-app')}, GENYEE-postgres: {mem_after.get('GENYEE-postgres')}"
    )

    # Check for active query leaks in postgres
    active_conns = pg_stats_after.get("active", 0)
    no_conn_leak = active_conns <= 1
    print(
        f"[LEAK CHECK] Active PostgreSQL Connections: {active_conns} (expected <= 1, no hanging queries)"
    )

    # Summary
    suite_ok = (
        all_passed and (concurrent_success == len(concurrent_queries)) and no_conn_leak
    )
    print(
        "\n================================================================================"
    )
    print(f"  Stress Test Result: {'ALL PASS' if suite_ok else 'FAIL'}")
    print(
        "================================================================================"
    )

    return suite_ok


if __name__ == "__main__":
    success = run_stress_suite()
    sys.exit(0 if success else 1)
