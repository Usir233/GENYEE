"""SSRF URL validation for docreader outbound HTTP requests.

Mirrors the core policy in internal/utils/security.go so redirect targets
during Playwright navigation are blocked the same way as Go-side imports.
"""

from __future__ import annotations

import ipaddress
import os
import re
import socket
from functools import lru_cache
from urllib.parse import urlparse

IPAddress = ipaddress.IPv4Address | ipaddress.IPv6Address
IPNetwork = ipaddress.IPv4Network | ipaddress.IPv6Network

RESTRICTED_HOSTNAMES: frozenset[str] = frozenset(
    {
        "localhost",
        "127.0.0.1",
        "::1",
        "0.0.0.0",
        "metadata.google.internal",
        "metadata.tencentyun.com",
        "metadata.aws.internal",
        "host.docker.internal",
        "gateway.docker.internal",
        "kubernetes.docker.internal",
        "kubernetes",
        "kubernetes.default",
        "kubernetes.default.svc",
        "kubernetes.default.svc.cluster.local",
    }
)

RESTRICTED_SUFFIXES: tuple[str, ...] = (
    ".local",
    ".localhost",
    ".internal",
    ".corp",
    ".lan",
    ".home",
    ".localdomain",
    ".svc.cluster.local",
    ".pod.cluster.local",
)

EXTRA_RESTRICTED_CIDRS: tuple[IPNetwork, ...] = tuple(
    ipaddress.ip_network(cidr)
    for cidr in (
        "100.64.0.0/10",
        "198.18.0.0/15",
        "198.51.100.0/24",
        "203.0.113.0/24",
        "192.0.0.0/24",
        "192.0.2.0/24",
        "0.0.0.0/8",
        "240.0.0.0/4",
        "255.255.255.255/32",
        "172.17.0.0/16",
        "172.18.0.0/16",
        "172.19.0.0/16",
        "172.20.0.0/16",
    )
)

BLOCKED_PORTS: frozenset[str] = frozenset(
    {
        "22",
        "23",
        "25",
        "445",
        "3389",
        "5432",
        "3306",
        "6379",
        "27017",
        "9200",
        "2379",
        "2380",
        "8500",
        "4001",
    }
)

_IP_LIKE_PATTERNS = (
    re.compile(r"^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$"),
    re.compile(r"^\d{8,10}$"),
    re.compile(r"^0[0-7]+\."),
    re.compile(r"(?i)^0x[0-9a-f]+\."),
    re.compile(r"(?i)^0x[0-9a-f]{6,8}$"),
    re.compile(r"(?i)^[0-9a-f:]+::[0-9a-f:]*$"),
    re.compile(r"(?i)^[0-9a-f]{1,4}(:[0-9a-f]{1,4}){7}$"),
)


def _normalize_url(raw_url: str) -> str:
    if "://" not in raw_url:
        return f"https://{raw_url}"
    return raw_url


def normalize_hostname(raw_hostname: str | None) -> tuple[str, str]:
    """Canonicalize FQDN trailing root dot and reject empty DNS labels (MED-14).

    Returns ``(normalized_hostname, error_reason)``. ``error_reason`` is empty
    when ``normalized_hostname`` is syntactically safe for policy evaluation.
    """
    host = (raw_hostname or "").strip().lower()
    if not host:
        return "", "URL has no hostname"
    if host.startswith(".") or ".." in host:
        return "", f"invalid hostname with empty DNS label: {host}"
    host = host.rstrip(".")
    if not host:
        return "", "URL has no hostname"
    if any(ch.isspace() or ord(ch) < 33 or ord(ch) == 127 for ch in host):
        return "", "invalid control character in hostname"
    if "%" in host:
        return "", "invalid zone or percent character in hostname"
    if ":" not in host:
        labels = host.split(".")
        if any(not label for label in labels):
            return "", f"invalid hostname with empty DNS label: {host}"
    return host, ""


@lru_cache(maxsize=1)
def _load_whitelist() -> tuple[frozenset[str], tuple[str, ...], tuple[IPNetwork, ...]]:
    entries: list[str] = []
    for env_key in ("SSRF_WHITELIST", "SSRF_WHITELIST_EXTRA"):
        raw = os.environ.get(env_key, "")
        if raw.strip():
            entries.extend(part.strip() for part in raw.split(",") if part.strip())

    exact_hosts: set[str] = set()
    suffix_hosts: list[str] = []
    cidr_nets: list[IPNetwork] = []
    for entry in entries:
        lowered = entry.lower().rstrip(".")
        if not lowered:
            continue
        if lowered.startswith("*."):
            suffix = lowered[1:]
            if suffix and suffix != "." and ".." not in suffix:
                suffix_hosts.append(suffix)
            continue
        if "/" in lowered:
            try:
                cidr_nets.append(ipaddress.ip_network(lowered, strict=False))
            except ValueError:
                continue
            continue
        exact_hosts.add(lowered)
    return frozenset(exact_hosts), tuple(suffix_hosts), tuple(cidr_nets)


def _whitelist_only_enabled() -> bool:
    """Whether SSRF_DNS_WHITELIST_ONLY makes the whitelist the whole egress
    policy: a host outside it is refused before any DNS query (#3378).

    Parsed like the Go side: 1/t/true enable it, 0/f/false disable it, and a
    non-empty value that is neither enables it — silently leaving an egress
    lockdown off because someone wrote "yes" is the one failure mode this
    control must not have.
    """
    raw = os.environ.get("SSRF_DNS_WHITELIST_ONLY", "").strip()
    if not raw:
        return False
    return raw.lower() not in {"0", "f", "false"}


def _is_whitelisted(hostname: str) -> bool:
    lowered, err = normalize_hostname(hostname)
    if err or not lowered:
        return False
    exact_hosts, suffix_hosts, cidr_nets = _load_whitelist()
    if lowered in exact_hosts:
        return True
    for suffix in suffix_hosts:
        if lowered.endswith(suffix) or lowered == suffix.lstrip("."):
            return True
    try:
        ip = ipaddress.ip_address(lowered)
    except ValueError:
        return False
    return any(ip in net for net in cidr_nets)


def _is_ip_like_hostname(hostname: str) -> bool:
    if any(pattern.search(hostname) for pattern in _IP_LIKE_PATTERNS):
        return True
    if ":" not in hostname:
        last_label = hostname.rsplit(".", 1)[-1]
        if last_label.isdigit() or re.fullmatch(r"(?i)0x[0-9a-f]+", last_label):
            return True
    return False


def _is_restricted_ip(ip: IPAddress) -> str | None:
    if ip.is_private:
        return "private IP address"
    if ip.is_loopback:
        return "loopback address"
    if ip.is_link_local:
        return "link-local address"
    if ip.is_multicast:
        return "multicast address"
    if ip.is_unspecified:
        return "unspecified address"
    if isinstance(ip, ipaddress.IPv4Address):
        for net in EXTRA_RESTRICTED_CIDRS:
            if isinstance(net, ipaddress.IPv4Network) and ip in net:
                return f"restricted range {net}"
    if isinstance(ip, ipaddress.IPv6Address):
        embedded_ipv4 = ip.ipv4_mapped
        if embedded_ipv4 is not None:
            reason = _is_restricted_ip(embedded_ipv4)
            if reason:
                return f"IPv4-mapped {reason}"
        if ip.sixtofour is not None:
            reason = _is_restricted_ip(ip.sixtofour)
            if reason:
                return f"6to4-embedded {reason}"
        if ip.teredo is not None:
            server_ip, client_ip = ip.teredo
            for label, embedded_ip in (("server", server_ip), ("client", client_ip)):
                reason = _is_restricted_ip(embedded_ip)
                if reason:
                    return f"Teredo {label} embeds {reason}"
        # NAT64's well-known prefix (64:ff9b::/96, RFC 6052) and the deprecated
        # IPv4-compatible form (::a.b.c.d) also carry a plain IPv4 address, but
        # ipaddress exposes no accessor for either, so is_private and
        # .sixtofour never see the payload. Without these two branches
        # ::169.254.169.254 reads as an ordinary public IPv6 address.
        # Mirrors internal/ipclass on the Go side.
        packed = ip.packed
        # RFC 8215 local-use NAT64 has deployment-specific IPv4 layouts.
        # Reject the entire /48, matching internal/ipclass, on every Python version.
        if packed[:6] == b"\x00\x64\xff\x9b\x00\x01":
            return "local-use NAT64 translation address"
        if packed[0:4] == b"\x00\x64\xff\x9b" and packed[4:12] == bytes(8):
            reason = _is_restricted_ip(ipaddress.IPv4Address(packed[12:16]))
            if reason:
                return f"NAT64-embedded {reason}"
        if packed[0:12] == bytes(12):
            # :: and ::1 already returned above; ::ffff:a.b.c.d is handled by
            # the ipv4_mapped branch.
            reason = _is_restricted_ip(ipaddress.IPv4Address(packed[12:16]))
            if reason:
                return f"IPv4-compatible {reason}"
        # Site-local (fec0::/10)
        if (ip.packed[0] == 0xFE) and (ip.packed[1] & 0xC0) == 0xC0:
            return "site-local IPv6 address"
    return None


def _resolve_host_ips(hostname: str) -> tuple[tuple[IPAddress, ...], str | None]:
    try:
        infos = socket.getaddrinfo(hostname, None, type=socket.SOCK_STREAM)
    except socket.gaierror as exc:
        return (), f"DNS resolution failed for hostname {hostname}: {exc}"
    ips: list[IPAddress] = []
    seen: set[str] = set()
    for info in infos:
        sockaddr = info[4]
        if not sockaddr:
            continue
        ip_str = sockaddr[0]
        if not isinstance(ip_str, str) or ip_str in seen:
            continue
        seen.add(ip_str)
        try:
            ips.append(ipaddress.ip_address(ip_str))
        except ValueError:
            continue
    if not ips:
        return (), f"DNS resolution failed for hostname {hostname}: no addresses"
    return tuple(ips), None


def is_ssrf_safe_url(raw_url: str) -> tuple[bool, str]:
    """Return (safe, reason). reason is empty when safe is True."""
    if not raw_url or not raw_url.strip():
        return False, "URL is empty"

    raw_stripped = raw_url.strip()
    authority_and_path = re.split(r"[?#]", raw_stripped, maxsplit=1)[0]
    if "\\" in authority_and_path:
        return False, "backslash in URL authority or path is not allowed"

    normalized = _normalize_url(raw_stripped)
    parsed = urlparse(normalized)
    scheme = (parsed.scheme or "").lower()
    if scheme not in {"http", "https"}:
        return False, f"invalid scheme: {scheme or '(none)'} (only http/https allowed)"

    if parsed.username is not None or parsed.password is not None:
        return False, "embedded URL credentials are not allowed"

    hostname_lower, host_err = normalize_hostname(parsed.hostname)
    if host_err:
        return False, host_err

    try:
        port = parsed.port
    except ValueError as exc:
        return False, f"invalid port: {exc}"
    if port is not None:
        if port <= 0 or port > 65535:
            return False, f"invalid port: {port}"
        if str(port) in BLOCKED_PORTS:
            return False, f"port {port} is blocked for security reasons"

    if _is_whitelisted(hostname_lower):
        return True, ""

    # Whitelist-only mode stops here: everything below resolves the name, and
    # a host outside the whitelist must not reach DNS at all (#3378).
    if _whitelist_only_enabled():
        return (
            False,
            f"host is not in the SSRF whitelist: {hostname_lower} "
            "(SSRF_DNS_WHITELIST_ONLY is on; add it to SSRF_WHITELIST to allow it)",
        )

    if hostname_lower in RESTRICTED_HOSTNAMES:
        return False, f"hostname {hostname_lower} is restricted"

    for suffix in RESTRICTED_SUFFIXES:
        if hostname_lower.endswith(suffix):
            return False, f"hostname suffix {suffix} is restricted"

    try:
        ipaddress.ip_address(hostname_lower)
        return (
            False,
            "direct IP address access is not allowed, use domain name or add to SSRF_WHITELIST",
        )
    except ValueError:
        pass

    if _is_ip_like_hostname(hostname_lower):
        return False, "IP-like hostname format is not allowed"

    resolved_ips, resolve_err = _resolve_host_ips(hostname_lower)
    if resolve_err:
        return False, resolve_err

    for resolved_ip in resolved_ips:
        reason = _is_restricted_ip(resolved_ip)
        if reason:
            return (
                False,
                f"hostname {hostname_lower} resolves to restricted IP {resolved_ip}: {reason}",
            )

    return True, ""


def reset_ssrf_whitelist_cache_for_test() -> None:
    """Clear cached whitelist entries (for unit tests only)."""
    _load_whitelist.cache_clear()
