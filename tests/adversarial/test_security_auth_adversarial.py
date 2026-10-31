#!/usr/bin/env python3
"""
GENYEE Adversarial Security, Authentication & Brand Leakage Challenge Suite
Executed by Challenger 1 (Adversarial Security, Auth & Brand Leakage Challenger)

Coverage:
1. Adversarial Authentication & Boundary Testing
   - Invalid credentials (wrong password, malformed email, malformed JSON, SQLi, XSS, buffer stress)
   - Tampered / forged JWTs on protected endpoints (/api/v1/auth/me, /api/v1/sessions)
   - JWT audience validation in middleware (aud="fake" vs aud="genyee" vs aud="weknora")
2. Adversarial Brand Residue Scan
   - Inspection of running frontend container assets (index.html, JS bundles, CSS, logos)
   - Inspection of running API response payloads, headers, error messages
3. Negative Export Testing
   - GET /api/v1/memory/export without auth, with invalid tokens, with unauthorized/malformed tenants
"""

import sys
import os
import json
import time
import base64
import hmac
import hashlib
import subprocess
import requests

BASE_URL = os.environ.get("BASE_URL", "http://127.0.0.1:80")
VALID_EMAIL = "zhanshun.bao@gamil.com"
VALID_PASSWORD = "mkRI3z$L5w%J#D&VxqeM"
VALID_TENANT_ID = 10000

test_results = {
    "total": 0,
    "passed": 0,
    "failed": 0,
    "cases": []
}

def record_result(name, category, passed, details, status_code=None, response_sample=None):
    test_results["total"] += 1
    if passed:
        test_results["passed"] += 1
        status_str = "[PASS]"
    else:
        test_results["failed"] += 1
        status_str = "[FAIL]"
    
    test_results["cases"].append({
        "name": name,
        "category": category,
        "passed": passed,
        "status_code": status_code,
        "details": details,
        "response_sample": str(response_sample)[:200] if response_sample else None
    })
    print(f"{status_str} [{category}] {name} (Status: {status_code}) - {details}")

def b64url(data: bytes) -> str:
    return base64.urlsafe_b64encode(data).decode('utf-8').rstrip('=')

def make_jwt(header_dict, payload_dict, secret_key=None):
    h = b64url(json.dumps(header_dict, separators=(',', ':')).encode('utf-8'))
    p = b64url(json.dumps(payload_dict, separators=(',', ':')).encode('utf-8'))
    if secret_key is not None:
        signing_input = f"{h}.{p}".encode('utf-8')
        sig = hmac.new(secret_key.encode('utf-8'), signing_input, hashlib.sha256).digest()
        s = b64url(sig)
    else:
        s = ""
    return f"{h}.{p}.{s}"

# ==========================================
# 1. Adversarial Authentication & Boundary Testing
# ==========================================

def test_auth_login_boundaries():
    print("\n--- 1.1 POST /api/v1/auth/login Boundary & Adversarial Tests ---")
    login_url = f"{BASE_URL}/api/v1/auth/login"

    # Baseline: Valid login
    try:
        r = requests.post(login_url, json={"email": VALID_EMAIL, "password": VALID_PASSWORD}, timeout=5)
        passed = (r.status_code == 200 and r.json().get("success") is True and "token" in r.json())
        record_result("Baseline Valid Login", "Auth-Boundary", passed, "Returns HTTP 200 with JWT token", r.status_code, r.text)
        valid_token = r.json().get("token")
    except Exception as e:
        record_result("Baseline Valid Login", "Auth-Boundary", False, f"Exception: {e}")
        valid_token = None

    # Case 1: Wrong password with valid user
    r = requests.post(login_url, json={"email": VALID_EMAIL, "password": "wrong_password_1234"}, timeout=5)
    passed = (r.status_code == 401 and r.headers.get("content-type", "").startswith("application/json") and r.json().get("success") is False)
    record_result("Wrong Password", "Auth-Boundary", passed, "Rejects with HTTP 401 and structured JSON", r.status_code, r.text)

    # Case 2: Non-existent email
    r = requests.post(login_url, json={"email": "nonexistent_user_9999@example.com", "password": "anyPassword123!"}, timeout=5)
    passed = (r.status_code == 401 and r.json().get("success") is False)
    record_result("Non-existent User", "Auth-Boundary", passed, "Rejects with HTTP 401 and structured JSON", r.status_code, r.text)

    # Case 3: Malformed email syntax
    r = requests.post(login_url, json={"email": "not-an-email-at-all", "password": "validLengthPass123"}, timeout=5)
    passed = (r.status_code in (400, 422) and "error" in r.json())
    record_result("Malformed Email Syntax", "Auth-Boundary", passed, "Rejects with HTTP 400 validation error", r.status_code, r.text)

    # Case 4: Short password failing validation tag
    r = requests.post(login_url, json={"email": VALID_EMAIL, "password": "123"}, timeout=5)
    passed = (r.status_code in (400, 422) and "error" in r.json())
    record_result("Short Password (< 6 chars)", "Auth-Boundary", passed, "Rejects with HTTP 400 validation error", r.status_code, r.text)

    # Case 5: Empty JSON object
    r = requests.post(login_url, json={}, timeout=5)
    passed = (r.status_code in (400, 422) and "error" in r.json())
    record_result("Empty JSON Payload", "Auth-Boundary", passed, "Rejects with HTTP 400 validation error", r.status_code, r.text)

    # Case 6: Malformed JSON syntax (raw string)
    r = requests.post(login_url, data='{"email": "broken_json', headers={"Content-Type": "application/json"}, timeout=5)
    passed = (r.status_code == 400 and "error" in r.json())
    record_result("Malformed JSON Syntax", "Auth-Boundary", passed, "Rejects with HTTP 400 JSON parse error", r.status_code, r.text)

    # Case 7: SQL Injection attempt in credentials
    sqli_payload = {"email": "' OR '1'='1' -- @test.com", "password": "' OR '1'='1' --"}
    r = requests.post(login_url, json=sqli_payload, timeout=5)
    passed = (r.status_code in (400, 401) and r.status_code != 500)
    record_result("SQL Injection Attack Payload", "Auth-Boundary", passed, f"Safely handled without 500 crash (code {r.status_code})", r.status_code, r.text)

    # Case 8: XSS Injection attempt in credentials
    xss_payload = {"email": "<script>alert('xss')</script>@domain.com", "password": "<script>alert(1)</script>"}
    r = requests.post(login_url, json=xss_payload, timeout=5)
    passed = (r.status_code in (400, 401) and r.status_code != 500)
    record_result("XSS Attack Payload", "Auth-Boundary", passed, f"Safely handled without 500 crash (code {r.status_code})", r.status_code, r.text)

    # Case 9: Large buffer overflow stress payload (64KB string)
    oversized_payload = {"email": "test@" + "a" * 1000 + ".com", "password": "B" * 65536}
    r = requests.post(login_url, json=oversized_payload, timeout=5)
    passed = (r.status_code in (400, 401, 413) and r.status_code != 500)
    record_result("Oversized 64KB Payload Stress", "Auth-Boundary", passed, f"Handled cleanly without 500 crash (code {r.status_code})", r.status_code, r.text)

    return valid_token

def test_protected_endpoints_tampered_jwt(valid_token):
    print("\n--- 1.2 Protected Endpoints Tampered & Forged JWT Testing ---")
    target_endpoints = [
        ("GET", f"{BASE_URL}/api/v1/auth/me"),
        ("GET", f"{BASE_URL}/api/v1/sessions"),
    ]

    for method, url in target_endpoints:
        endpoint_name = url.split(BASE_URL)[1]

        # Case 1: Missing Authorization header
        r = requests.request(method, url, timeout=5)
        passed = (r.status_code == 401 and "Unauthorized" in r.text)
        record_result(f"Missing Auth Header ({endpoint_name})", "Auth-JWT", passed, "Rejects with HTTP 401 Unauthorized", r.status_code, r.text)

        # Case 2: Malformed Bearer header (empty token)
        r = requests.request(method, url, headers={"Authorization": "Bearer "}, timeout=5)
        passed = (r.status_code == 401)
        record_result(f"Empty Bearer Token ({endpoint_name})", "Auth-JWT", passed, "Rejects with HTTP 401 Unauthorized", r.status_code, r.text)

        # Case 3: Completely bogus token
        r = requests.request(method, url, headers={"Authorization": "Bearer totally.invalid.junktoken"}, timeout=5)
        passed = (r.status_code == 401)
        record_result(f"Garbage JWT ({endpoint_name})", "Auth-JWT", passed, "Rejects with HTTP 401 Unauthorized", r.status_code, r.text)

        # Case 4: Alg "none" attack
        alg_none_token = make_jwt({"alg": "none", "typ": "JWT"}, {"user_id": "8a18ad5e-eb1b-411f-9e6b-5490991c7d8c", "tenant_id": 10000, "exp": int(time.time()) + 3600})
        r = requests.request(method, url, headers={"Authorization": f"Bearer {alg_none_token}"}, timeout=5)
        passed = (r.status_code == 401)
        record_result(f"Alg None Attack ({endpoint_name})", "Auth-JWT", passed, "Rejects unsigned alg=none JWT with HTTP 401", r.status_code, r.text)

        # Case 5: Forged JWT signed with attacker secret key
        forged_attacker_token = make_jwt(
            {"alg": "HS256", "typ": "JWT"},
            {"user_id": "8a18ad5e-eb1b-411f-9e6b-5490991c7d8c", "email": VALID_EMAIL, "tenant_id": 10000, "exp": int(time.time()) + 3600},
            secret_key="attacker_secret_key_1234567890"
        )
        r = requests.request(method, url, headers={"Authorization": f"Bearer {forged_attacker_token}"}, timeout=5)
        passed = (r.status_code == 401)
        record_result(f"Attacker Secret Signed JWT ({endpoint_name})", "Auth-JWT", passed, "Rejects forged signature with HTTP 401", r.status_code, r.text)

        # Case 6: Tampered valid token (signature bit-flip)
        if valid_token and "." in valid_token:
            parts = valid_token.split(".")
            # tamper the signature part
            last_char = 'a' if parts[2][-1] != 'a' else 'b'
            tampered_token = f"{parts[0]}.{parts[1]}.{parts[2][:-1]}{last_char}"
            r = requests.request(method, url, headers={"Authorization": f"Bearer {tampered_token}"}, timeout=5)
            passed = (r.status_code == 401)
            record_result(f"Bit-flipped Signature Tampering ({endpoint_name})", "Auth-JWT", passed, "Rejects tampered signature with HTTP 401", r.status_code, r.text)

        # Case 7: Expired JWT
        expired_token = make_jwt(
            {"alg": "HS256", "typ": "JWT"},
            {"user_id": "8a18ad5e-eb1b-411f-9e6b-5490991c7d8c", "email": VALID_EMAIL, "tenant_id": 10000, "exp": int(time.time()) - 3600},
            secret_key="some_secret"
        )
        r = requests.request(method, url, headers={"Authorization": f"Bearer {expired_token}"}, timeout=5)
        passed = (r.status_code == 401)
        record_result(f"Expired JWT ({endpoint_name})", "Auth-JWT", passed, "Rejects expired JWT with HTTP 401", r.status_code, r.text)

def test_jwt_audience_validation_in_middleware():
    print("\n--- 1.3 External JWT Audience Validation in Middleware ---")
    cmd = ["go", "test", "-v", "./internal/middleware", "-run", "TestAudienceValidation_AdversarialChallenge"]
    p = subprocess.run(cmd, cwd="/home/cursor/GENYEE", capture_output=True, text=True)
    out = p.stdout + p.stderr
    
    genyee_pass = "Accepted_audience:_genyee" in out and "PASS" in out
    weknora_pass = "Accepted_audience:_weknora" in out and "PASS" in out
    fake_rejected = "Rejected_audience:_fake" in out and "token has invalid audience" in out
    empty_rejected = "Rejected_audience:_empty_string" in out and "aud claim is required" in out
    malicious_rejected = "Rejected_audience:_malicious_attacker" in out and "token has invalid audience" in out
    overall_pass = (p.returncode == 0) and genyee_pass and weknora_pass and fake_rejected and empty_rejected and malicious_rejected

    record_result("Middleware External JWT aud=genyee Accepted", "Auth-Audience", genyee_pass, "Audience 'genyee' successfully validated & accepted")
    record_result("Middleware External JWT aud=weknora Accepted (Compat)", "Auth-Audience", weknora_pass, "Audience 'weknora' accepted for backward compatibility")
    record_result("Middleware External JWT aud=fake Rejected", "Auth-Audience", fake_rejected, "Audience 'fake' strictly rejected with invalid audience error")
    record_result("Middleware External JWT aud='' (Empty) Rejected", "Auth-Audience", empty_rejected, "Empty audience strictly rejected")
    record_result("Middleware External JWT Malicious aud Rejected", "Auth-Audience", malicious_rejected, "Arbitrary attacker audience strictly rejected")
    record_result("Overall Middleware Audience Go Test Suite", "Auth-Audience", overall_pass, f"Suite exit code {p.returncode}")

# ==========================================
# 2. Adversarial Brand Residue Scan
# ==========================================

def test_brand_residue_scan(valid_token):
    print("\n--- 2. Adversarial Brand Residue Scan ---")
    
    # 2.1 Front-end running container scan
    # Check for "Tencent WeKnora" (case-insensitive) in running frontend
    cmd = ["docker", "exec", "GENYEE-frontend", "grep", "-rioE", "Tencent.{0,20}WeKnora|WeKnora.{0,20}Tencent", "/usr/share/nginx/html"]
    p = subprocess.run(cmd, capture_output=True, text=True)
    # grep returns exit code 1 when no matches found
    zero_vendor_combo = (p.returncode == 1 and len(p.stdout.strip()) == 0)
    record_result("Zero 'Tencent WeKnora' Combos in Frontend Bundle", "Brand-Compliance", zero_vendor_combo, f"Found matches count: {len(p.stdout.strip().splitlines()) if p.stdout.strip() else 0}")

    # Check index.html title
    cmd = ["docker", "exec", "GENYEE-frontend", "cat", "/usr/share/nginx/html/index.html"]
    p = subprocess.run(cmd, capture_output=True, text=True)
    html_content = p.stdout
    title_correct = "<title>GENYEE</title>" in html_content
    record_result("Frontend HTML Title is 'GENYEE'", "Brand-Compliance", title_correct, "index.html has <title>GENYEE</title>")
    
    # Check favicon & logo assets
    logo_correct = 'href="/assets/favicon-BSc4y12p.ico"' in html_content
    record_result("Frontend Favicon is properly referenced", "Brand-Compliance", logo_correct, "Favicon referenced correctly")

    # Verify weknora.png does not exist in nginx html
    cmd = ["docker", "exec", "GENYEE-frontend", "find", "/usr/share/nginx/html", "-name", "*weknora*.png"]
    p = subprocess.run(cmd, capture_output=True, text=True)
    zero_weknora_png = (len(p.stdout.strip()) == 0)
    record_result("Zero Legacy 'weknora.png' in Frontend Assets", "Brand-Compliance", zero_weknora_png, f"Matches: {p.stdout.strip() or 'None'}")

    # 2.2 Live API Output & Error Message Residue Scan
    # Test GET / response
    r = requests.get(f"{BASE_URL}/", timeout=5)
    r_title_ok = "<title>GENYEE</title>" in r.text
    record_result("HTTP GET / Live Title is GENYEE", "Brand-Compliance", r_title_ok, "Live root HTML title verified")

    # Test error messages for brand leakage
    r_bad_auth = requests.post(f"{BASE_URL}/api/v1/auth/login", json={"email": "bad@bad.com", "password": "wrong"}, timeout=5)
    clean_error_msg = ("tencent" not in r_bad_auth.text.lower()) and ("weknora" not in r_bad_auth.text.lower())
    record_result("Auth Error Message Free of Vendor Residue", "Brand-Compliance", clean_error_msg, "Login error message is generic and clean", r_bad_auth.status_code, r_bad_auth.text)

    # Test session and me response
    if valid_token:
        headers = {"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": str(VALID_TENANT_ID)}
        r_me = requests.get(f"{BASE_URL}/api/v1/auth/me", headers=headers, timeout=5)
        clean_me = ("tencent weknora" not in r_me.text.lower())
        record_result("GET /api/v1/auth/me Payload Free of Vendor Brand", "Brand-Compliance", clean_me, "User profile JSON is clean", r_me.status_code)

        r_sessions = requests.get(f"{BASE_URL}/api/v1/sessions", headers=headers, timeout=5)
        clean_sessions = ("tencent weknora" not in r_sessions.text.lower())
        record_result("GET /api/v1/sessions Payload Free of Vendor Brand", "Brand-Compliance", clean_sessions, "Sessions list JSON is clean", r_sessions.status_code)

# ==========================================
# 3. Negative Export Testing
# ==========================================

def test_negative_export(valid_token):
    print("\n--- 3. Negative Export Testing (/api/v1/memory/export) ---")
    export_url = f"{BASE_URL}/api/v1/memory/export"

    # Case 1: Unauthenticated request (no Authorization header)
    r = requests.get(export_url, timeout=5)
    passed = (r.status_code == 401 and "Unauthorized" in r.text)
    record_result("Memory Export Unauthenticated Rejection", "Negative-Export", passed, "Rejects with HTTP 401 Unauthorized", r.status_code, r.text)

    # Case 2: Bogus / forged token
    r = requests.get(export_url, headers={"Authorization": "Bearer forged.bad.token"}, timeout=5)
    passed = (r.status_code == 401 and "Unauthorized" in r.text)
    record_result("Memory Export Forged Token Rejection", "Negative-Export", passed, "Rejects with HTTP 401 Unauthorized", r.status_code, r.text)

    if not valid_token:
        print("[WARN] Skipping authenticated negative tenant tests because valid_token is unavailable")
        return

    # Case 3: Unauthorized tenant ID (e.g. 99999)
    r = requests.get(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": "99999"}, timeout=5)
    passed = (r.status_code == 403 and "Forbidden" in r.text)
    record_result("Memory Export Unauthorized Tenant (99999)", "Negative-Export", passed, "Rejects with HTTP 403 Forbidden", r.status_code, r.text)

    # Case 4: Malformed non-numeric tenant ID ("invalid-tenant")
    r = requests.get(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": "invalid-tenant"}, timeout=5)
    passed = (r.status_code == 400 and "Invalid X-Tenant-ID header" in r.text)
    record_result("Memory Export Malformed Tenant Header ('invalid-tenant')", "Negative-Export", passed, "Rejects with HTTP 400 Bad Request", r.status_code, r.text)

    # Case 5: Negative tenant ID ("-1")
    r = requests.get(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": "-1"}, timeout=5)
    passed = (r.status_code == 400 and "Invalid X-Tenant-ID header" in r.text)
    record_result("Memory Export Negative Tenant Header ('-1')", "Negative-Export", passed, "Rejects with HTTP 400 Bad Request", r.status_code, r.text)

    # Case 6: Zero tenant ID ("0")
    r = requests.get(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": "0"}, timeout=5)
    passed = (r.status_code == 400 and "Invalid X-Tenant-ID header" in r.text)
    record_result("Memory Export Zero Tenant Header ('0')", "Negative-Export", passed, "Rejects with HTTP 400 Bad Request", r.status_code, r.text)

    # Case 7: Invalid HTTP Method (POST /api/v1/memory/export)
    r = requests.post(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": str(VALID_TENANT_ID)}, timeout=5)
    passed = (r.status_code in (404, 405))
    record_result("Memory Export Invalid Method (POST)", "Negative-Export", passed, f"Rejects with HTTP {r.status_code}", r.status_code)

    # Case 8: Positive Control (Valid Auth & Valid Tenant)
    r = requests.get(export_url, headers={"Authorization": f"Bearer {valid_token}", "X-Tenant-Id": str(VALID_TENANT_ID)}, timeout=5)
    disposition = r.headers.get("Content-Disposition", "")
    passed = (r.status_code == 200 and 'filename="genyee-memories.json"' in disposition and r.json().get("success") is True)
    record_result("Memory Export Positive Control (Valid)", "Negative-Export", passed, "Returns HTTP 200 with genyee-memories.json attachment header", r.status_code, disposition)

def main():
    print("=" * 70)
    print("  GENYEE CHALLENGER 1 ADVERSARIAL VERIFICATION SUITE")
    print(f"  Target: {BASE_URL}")
    print(f"  Time: {time.strftime('%Y-%m-%d %H:%M:%SZ', time.gmtime())}")
    print("=" * 70)

    token = test_auth_login_boundaries()
    test_protected_endpoints_tampered_jwt(token)
    test_jwt_audience_validation_in_middleware()
    test_brand_residue_scan(token)
    test_negative_export(token)

    print("\n" + "=" * 70)
    print(f"  SUMMARY: Total: {test_results['total']} | Passed: {test_results['passed']} | Failed: {test_results['failed']}")
    print("=" * 70)

    # Write summary json
    summary_path = "/home/cursor/GENYEE/test-reports/challenger_1_summary.json"
    os.makedirs(os.path.dirname(summary_path), exist_ok=True)
    with open(summary_path, "w") as f:
        json.dump(test_results, f, indent=2)
    print(f"Wrote challenge summary to: {summary_path}")

    if test_results["failed"] > 0:
        print("\nVERDICT: REQUEST_CHANGES (Some adversarial tests failed)")
        sys.exit(1)
    else:
        print("\nVERDICT: APPROVE (100% Adversarial Challenges Passed Cleanly)")
        sys.exit(0)

if __name__ == "__main__":
    main()
