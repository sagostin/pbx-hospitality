#!/usr/bin/env python3
"""
PBX Hospitality Admin CLI

Interactive CLI for managing Sites, Tenants, Bicom Systems, and PBX status
via the /admin/* API. All endpoints require the X-Admin-Key header.

Environment variables:
  PBX_ADMIN_BASE_URL  - API base URL (default: http://localhost:8080)
  PBX_ADMIN_KEY       - Admin API key (prompted if not set)
"""

import getpass
import json
import os
import sys
from typing import Any, Dict, List, Optional

import requests

DEFAULT_BASE_URL = os.getenv("PBX_ADMIN_BASE_URL", "http://localhost:8080")
ADMIN_KEY = os.getenv("PBX_ADMIN_KEY", "")
TIMEOUT = 15


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def base_url() -> str:
    return os.getenv("PBX_ADMIN_BASE_URL", DEFAULT_BASE_URL).rstrip("/")


def admin_key() -> str:
    key = ADMIN_KEY
    if not key:
        key = getpass.getpass("Admin API key: ").strip()
        if not key:
            print("Error: Admin API key required.", file=sys.stderr)
            sys.exit(1)
    return key


def headers() -> Dict[str, str]:
    return {
        "X-Admin-Key": admin_key(),
        "Content-Type": "application/json",
    }


def _handle_error(resp: requests.Response) -> None:
    print(f"❌ Failed ({resp.status_code})")
    try:
        body = resp.json()
        print(json.dumps(body, indent=2))
    except Exception:
        print(resp.text)


def get_json(path: str) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.get(url, headers={"X-Admin-Key": admin_key()}, timeout=TIMEOUT)


def post_json(path: str, payload: dict) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.post(
        url, headers=headers(), data=json.dumps(payload), timeout=TIMEOUT
    )


def put_json(path: str, payload: dict) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.put(
        url, headers=headers(), data=json.dumps(payload), timeout=TIMEOUT
    )


def patch_json(path: str, payload: dict) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.patch(
        url, headers=headers(), data=json.dumps(payload), timeout=TIMEOUT
    )


def delete_json(path: str) -> requests.Response:
    url = f"{base_url()}{path}"
    return requests.delete(url, headers={"X-Admin-Key": admin_key()}, timeout=TIMEOUT)


def _confirm(prompt: str) -> bool:
    return input(f"{prompt} [y/N]: ").strip().lower() == "y"


# ---------------------------------------------------------------------------
# Sites
# ---------------------------------------------------------------------------


def list_sites() -> Optional[List[Dict[str, Any]]]:
    print("\n=== Sites ===")
    try:
        resp = get_json("/admin/sites")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        _handle_error(resp)
        return None

    sites = resp.json()
    if not sites:
        print("No sites found.")
        return []

    print(f"\n{'ID':<24} {'Name':<28} {'Enabled':<8} {'Created':<20}")
    print("-" * 85)
    for s in sites:
        sid = str(s.get("id", ""))[:24]
        name = str(s.get("name", ""))[:28]
        enabled = "✅" if s.get("enabled", True) else "❌"
        created = str(s.get("created_at", ""))[:20]
        print(f"{sid:<24} {name:<28} {enabled:<8} {created:<20}")

    print(f"\nTotal: {len(sites)} sites")
    return sites


def get_site(site_id: str) -> Optional[Dict[str, Any]]:
    try:
        resp = get_json(f"/admin/sites/{site_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 200:
        return resp.json()
    return None


def show_site_details() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    print(f"\n=== Site: {site_id} ===")
    site = get_site(site_id)
    if not site:
        print("Site not found.")
        return

    print(f"  ID:      {site.get('id')}")
    print(f"  Name:    {site.get('name')}")
    print(f"  Enabled: {site.get('enabled', True)}")
    settings = site.get("settings") or {}
    if settings:
        print(f"  Settings: {json.dumps(settings, indent=4)}")
    print(f"  Created: {site.get('created_at')}")
    print(f"  Updated: {site.get('updated_at')}")


def create_site_interactive() -> Optional[str]:
    print("\n=== Create Site ===")
    site_id = input("Site ID (alphanumeric with dashes, e.g. hotel-alpha): ").strip()
    if not site_id:
        print("Site ID required.")
        return None

    name = input("Display Name: ").strip()
    if not name:
        print("Name required.")
        return None

    auth_code = getpass.getpass("Auth Code (min 16 chars): ").strip()
    if len(auth_code) < 16:
        print("Auth code must be at least 16 characters.")
        return None

    enabled = input("Enabled? [Y/n]: ").strip().lower() != "n"

    payload = {"id": site_id, "name": name, "auth_code": auth_code, "enabled": enabled}
    settings_str = input("Settings JSON (optional, e.g. {}): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON for settings; skipping.")

    try:
        resp = post_json("/admin/sites", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 201:
        data = resp.json()
        print(f"✅ Site created: {data.get('id')}")
        return data.get("id")
    else:
        _handle_error(resp)
        return None


def update_site_interactive() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    print(f"\n=== Update Site: {site_id} ===")
    payload: Dict[str, Any] = {}

    name = input("New Name (leave blank to skip): ").strip()
    if name:
        payload["name"] = name

    auth_code = getpass.getpass("New Auth Code (leave blank to skip): ").strip()
    if auth_code:
        if len(auth_code) < 16:
            print("Auth code must be at least 16 characters.")
            return
        payload["auth_code"] = auth_code

    enabled_str = input("Enabled? [y/n/blank to skip]: ").strip()
    if enabled_str:
        payload["enabled"] = enabled_str.lower() == "y"

    settings_str = input("Settings JSON (leave blank to skip): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON; skipping settings.")

    if not payload:
        print("No fields to update.")
        return

    try:
        resp = put_json(f"/admin/sites/{site_id}", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print("✅ Site updated.")
    else:
        _handle_error(resp)


def delete_site_interactive() -> None:
    site_id = input("Site ID to delete: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    if not _confirm(f"Delete site '{site_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/sites/{site_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Site deleted.")
    else:
        _handle_error(resp)


def site_health() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    print(f"\n=== Site Health: {site_id} ===")
    try:
        resp = get_json(f"/admin/sites/{site_id}/health")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    data = resp.json()
    print(f"  Health Status: {data.get('health_status', 'N/A')}")
    systems = data.get("systems") or []
    if systems:
        print(f"\n  Systems ({len(systems)}):")
        for s in systems:
            print(
                f"    - {s.get('name', 'N/A')} ({s.get('id', 'N/A')})"
                f"  [{s.get('health_status', 'unknown')}]"
            )
            print(f"      API URL: {s.get('api_url', 'N/A')}")
    else:
        print("  No systems configured.")


# ---------------------------------------------------------------------------
# Site Bicom Mappings
# ---------------------------------------------------------------------------


def list_site_bicom_mappings() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    print(f"\n=== Bicom Mappings for {site_id} ===")
    try:
        resp = get_json(f"/admin/sites/{site_id}/bicom")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    mappings = resp.json()
    if not mappings:
        print("No mappings found.")
        return

    print(f"\n{'ID':<6} {'Bicom System':<20} {'Priority':<10} {'Failover':<10}")
    print("-" * 50)
    for m in mappings:
        mid = str(m.get("id", ""))[:6]
        bicom = str(m.get("bicom_system_id", ""))[:20]
        priority = str(m.get("priority", 0))[:10]
        failover = "✅" if m.get("failover_enabled", True) else "❌"
        print(f"{mid:<6} {bicom:<20} {priority:<10} {failover:<10}")

    print(f"\nTotal: {len(mappings)} mappings")


def add_site_bicom_mapping() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    bicom_system_id = input("Bicom System ID: ").strip()
    if not bicom_system_id:
        print("Bicom System ID required.")
        return

    priority_str = input("Priority (default 0): ").strip()
    try:
        priority = int(priority_str) if priority_str else 0
    except ValueError:
        priority = 0

    failover = input("Failover enabled? [Y/n]: ").strip().lower() != "n"

    payload = {
        "bicom_system_id": bicom_system_id,
        "priority": priority,
        "failover_enabled": failover,
    }

    try:
        resp = post_json(f"/admin/sites/{site_id}/bicom", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 201:
        print("✅ Mapping added.")
    else:
        _handle_error(resp)


def remove_site_bicom_mapping() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    bicom_system_id = input("Bicom System ID to remove: ").strip()
    if not bicom_system_id:
        print("Bicom System ID required.")
        return

    if not _confirm(f"Remove mapping for '{bicom_system_id}' from site '{site_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/sites/{site_id}/bicom/{bicom_system_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Mapping removed.")
    else:
        _handle_error(resp)


def list_site_bicom_systems() -> None:
    site_id = input("Site ID: ").strip()
    if not site_id:
        print("Site ID required.")
        return

    print(f"\n=== Bicom Systems for {site_id} ===")
    try:
        resp = get_json(f"/admin/sites/{site_id}/bicom-systems")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    systems = resp.json()
    if not systems:
        print("No bicom systems found.")
        return

    print(f"\n{'ID':<20} {'Name':<24} {'Health':<12} {'Enabled':<8}")
    print("-" * 70)
    for s in systems:
        sid = str(s.get("id", ""))[:20]
        name = str(s.get("name", ""))[:24]
        health = str(s.get("health_status", "unknown"))[:12]
        enabled = "✅" if s.get("enabled", True) else "❌"
        print(f"{sid:<20} {name:<24} {health:<12} {enabled:<8}")

    print(f"\nTotal: {len(systems)} systems")


# ---------------------------------------------------------------------------
# Tenants
# ---------------------------------------------------------------------------


def list_tenants() -> Optional[List[Dict[str, Any]]]:
    print("\n=== Tenants ===")
    try:
        resp = get_json("/admin/tenants")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        _handle_error(resp)
        return None

    tenants = resp.json()
    if not tenants:
        print("No tenants found.")
        return []

    print(f"\n{'ID':<24} {'Site ID':<20} {'Name':<24} {'Enabled':<8}")
    print("-" * 80)
    for t in tenants:
        tid = str(t.get("id", ""))[:24]
        site = str(t.get("site_id") or "-")[:20]
        name = str(t.get("name", ""))[:24]
        enabled = "✅" if t.get("enabled", True) else "❌"
        print(f"{tid:<24} {site:<20} {name:<24} {enabled:<8}")

    print(f"\nTotal: {len(tenants)} tenants")
    return tenants


def get_tenant(tenant_id: str) -> Optional[Dict[str, Any]]:
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 200:
        return resp.json()
    return None


def show_tenant_details() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    print(f"\n=== Tenant: {tenant_id} ===")
    tenant = get_tenant(tenant_id)
    if not tenant:
        print("Tenant not found.")
        return

    print(f"  ID:        {tenant.get('id')}")
    print(f"  Site ID:   {tenant.get('site_id') or 'N/A'}")
    print(f"  Name:      {tenant.get('name')}")
    print(f"  Enabled:   {tenant.get('enabled', True)}")
    pms = tenant.get("pms_config") or {}
    if pms:
        print(f"  PMS Config: {json.dumps(pms, indent=4)}")
    pbx = tenant.get("pbx_config") or {}
    if pbx:
        print(f"  PBX Config: {json.dumps(pbx, indent=4)}")
    settings = tenant.get("settings") or {}
    if settings:
        print(f"  Settings:   {json.dumps(settings, indent=4)}")
    print(f"  Created:   {tenant.get('created_at')}")
    print(f"  Updated:   {tenant.get('updated_at')}")


def create_tenant_interactive() -> Optional[str]:
    print("\n=== Create Tenant ===")
    tenant_id = input("Tenant ID (alphanumeric with dashes): ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return None

    name = input("Display Name: ").strip()
    if not name:
        print("Name required.")
        return None

    site_id = input("Site ID (optional): ").strip() or None

    print("\nPMS Protocol:")
    print("  1) mitel")
    print("  2) fias")
    print("  3) tigertms")
    pms_choice = input("Choose [1-3, default=1]: ").strip()
    pms_protocols = {"1": "mitel", "2": "fias", "3": "tigertms"}
    pms_protocol = pms_protocols.get(pms_choice, "mitel")

    print("\nPBX Type:")
    print("  1) bicom")
    print("  2) zultys")
    print("  3) freeswitch")
    pbx_choice = input("Choose [1-3, default=1]: ").strip()
    pbx_types = {"1": "bicom", "2": "zultys", "3": "freeswitch"}
    pbx_type = pbx_types.get(pbx_choice, "bicom")

    enabled = input("Enabled? [Y/n]: ").strip().lower() != "n"

    payload: Dict[str, Any] = {
        "id": tenant_id,
        "name": name,
        "pms_config": {"protocol": pms_protocol},
        "pbx_config": {"type": pbx_type},
        "enabled": enabled,
    }
    if site_id:
        payload["site_id"] = site_id

    settings_str = input("Settings JSON (optional, e.g. {}): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON for settings; skipping.")

    try:
        resp = post_json("/admin/tenants", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 201:
        data = resp.json()
        print(f"✅ Tenant created: {data.get('id')}")
        return data.get("id")
    else:
        _handle_error(resp)
        return None


def update_tenant_interactive() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    print(f"\n=== Update Tenant: {tenant_id} ===")
    payload: Dict[str, Any] = {}

    name = input("New Name (leave blank to skip): ").strip()
    if name:
        payload["name"] = name

    site_id = input("New Site ID (leave blank to skip, 'null' to clear): ").strip()
    if site_id:
        payload["site_id"] = None if site_id.lower() == "null" else site_id

    pms_protocol = input("PMS Protocol (mitel/fias/tigertms, blank to skip): ").strip()
    if pms_protocol:
        payload["pms_config"] = {"protocol": pms_protocol}

    pbx_type = input("PBX Type (bicom/zultys/freeswitch, blank to skip): ").strip()
    if pbx_type:
        payload["pbx_config"] = {"type": pbx_type}

    enabled_str = input("Enabled? [y/n/blank to skip]: ").strip()
    if enabled_str:
        payload["enabled"] = enabled_str.lower() == "y"

    settings_str = input("Settings JSON (leave blank to skip): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON; skipping settings.")

    if not payload:
        print("No fields to update.")
        return

    try:
        resp = put_json(f"/admin/tenants/{tenant_id}", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print("✅ Tenant updated.")
    else:
        _handle_error(resp)


def delete_tenant_interactive() -> None:
    tenant_id = input("Tenant ID to delete: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    if not _confirm(f"Delete tenant '{tenant_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/tenants/{tenant_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Tenant deleted.")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Tenant Rooms
# ---------------------------------------------------------------------------


def list_tenant_rooms() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    print(f"\n=== Rooms for {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/rooms")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    rooms = resp.json()
    if not rooms:
        print("No rooms found.")
        return

    print(f"\n{'ID':<8} {'Room':<10} {'Ext':<10} {'Match':<16} {'Created':<20}")
    print("-" * 70)
    for r in rooms:
        rid = str(r.get("id", ""))[:8]
        room = str(r.get("room_number", ""))[:10]
        ext = str(r.get("extension", ""))[:10]
        match_p = str(r.get("match_pattern") or "-")[:16]
        created = str(r.get("created_at", ""))[:20]
        print(f"{rid:<8} {room:<10} {ext:<10} {match_p:<16} {created:<20}")

    print(f"\nTotal: {len(rooms)} rooms")


def get_tenant_room() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    room = input("Room number: ").strip()
    if not room:
        print("Room number required.")
        return

    print(f"\n=== Room {room} for {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/rooms/{room}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        data = resp.json()
        print(json.dumps(data, indent=2))
    else:
        _handle_error(resp)


def delete_tenant_room() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    room = input("Room number to delete: ").strip()
    if not room:
        print("Room number required.")
        return

    if not _confirm(f"Delete room '{room}' for tenant '{tenant_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/tenants/{tenant_id}/rooms/{room}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Room deleted.")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Tenant Sessions
# ---------------------------------------------------------------------------


def list_tenant_sessions() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    print(f"\n=== Sessions for {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/sessions")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    sessions = resp.json()
    if not sessions:
        print("No sessions found.")
        return

    print(
        f"\n{'ID':<8} {'Room':<10} {'Ext':<10} {'Guest':<20} {'Check-in':<20} {'Check-out':<20}"
    )
    print("-" * 95)
    for s in sessions:
        sid = str(s.get("id", ""))[:8]
        room = str(s.get("room_number", ""))[:10]
        ext = str(s.get("extension", ""))[:10]
        guest = str(s.get("guest_name", ""))[:20]
        checkin = str(s.get("check_in", ""))[:20]
        checkout = str(s.get("check_out") or "active")[:20]
        print(f"{sid:<8} {room:<10} {ext:<10} {guest:<20} {checkin:<20} {checkout:<20}")

    print(f"\nTotal: {len(sessions)} sessions")


def get_tenant_session() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    room = input("Room number: ").strip()
    if not room:
        print("Room number required.")
        return

    print(f"\n=== Session for room {room} in {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/sessions/{room}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print(json.dumps(resp.json(), indent=2))
    else:
        _handle_error(resp)


def delete_tenant_session() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    room = input("Room number to delete session for: ").strip()
    if not room:
        print("Room number required.")
        return

    if not _confirm(f"Delete session for room '{room}' in tenant '{tenant_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/tenants/{tenant_id}/sessions/{room}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Session deleted.")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Tenant Events
# ---------------------------------------------------------------------------


def list_tenant_events() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    processed = input("Filter processed? [true/false/blank for all]: ").strip()
    limit = input("Limit (default 50): ").strip()
    offset = input("Offset (default 0): ").strip()

    params: List[str] = []
    if processed:
        params.append(f"processed={processed}")
    if limit:
        params.append(f"limit={limit}")
    if offset:
        params.append(f"offset={offset}")

    query = "?" + "&".join(params) if params else ""

    print(f"\n=== Events for {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/events{query}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    events = resp.json()
    if not events:
        print("No events found.")
        return

    print(
        f"\n{'ID':<8} {'Type':<16} {'Room':<10} {'Ext':<10} {'Processed':<10} {'Error':<20}"
    )
    print("-" * 80)
    for ev in events:
        eid = str(ev.get("id", ""))[:8]
        etype = str(ev.get("event_type", ""))[:16]
        room = str(ev.get("room_number", ""))[:10]
        ext = str(ev.get("extension", ""))[:10]
        proc = "✅" if ev.get("processed", False) else "❌"
        error = str(ev.get("error") or "-")[:20]
        print(f"{eid:<8} {etype:<16} {room:<10} {ext:<10} {proc:<10} {error:<20}")

    print(f"\nTotal: {len(events)} events")


def delete_tenant_event() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    event_id = input("Event ID to delete: ").strip()
    if not event_id:
        print("Event ID required.")
        return

    if not _confirm(f"Delete event {event_id} for tenant '{tenant_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/tenants/{tenant_id}/events/{event_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Event deleted.")
    else:
        _handle_error(resp)


def retry_tenant_event() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    event_id = input("Event ID to retry: ").strip()
    if not event_id:
        print("Event ID required.")
        return

    try:
        resp = post_json(f"/admin/tenants/{tenant_id}/events/{event_id}/retry", {})
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print("✅ Event retry triggered.")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Tenant Health
# ---------------------------------------------------------------------------


def tenant_health() -> None:
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    print(f"\n=== Tenant Health: {tenant_id} ===")
    try:
        resp = get_json(f"/admin/tenants/{tenant_id}/health")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        data = resp.json()
        print(f"  Name:            {data.get('name', 'N/A')}")
        print(f"  PMS Connected:   {'✅' if data.get('pms_connected') else '❌'}")
        print(f"  PBX Connected:   {'✅' if data.get('pbx_connected') else '❌'}")
        print(f"  Enabled:         {'✅' if data.get('enabled') else '❌'}")
        print(f"  Room Count:      {data.get('room_count', 0)}")
        print(f"  Active Sessions: {data.get('active_sessions', 0)}")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Tenant Import
# ---------------------------------------------------------------------------


def import_tenants_interactive() -> None:
    print("\n=== Import Tenants ===")
    print(
        "Paste JSON array of tenant objects. Press Ctrl+D (or Ctrl+Z on Windows) when done."
    )
    lines = []
    try:
        while True:
            line = input()
            lines.append(line)
    except EOFError:
        pass

    raw = "\n".join(lines).strip()
    if not raw:
        print("No input provided.")
        return

    try:
        tenants = json.loads(raw)
    except json.JSONDecodeError as e:
        print(f"Invalid JSON: {e}")
        return

    if not isinstance(tenants, list):
        print("Expected a JSON array of tenant objects.")
        return

    payload = {"tenants": tenants}
    try:
        resp = post_json("/admin/tenants/import", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        data = resp.json()
        print(
            f"✅ Import complete: {data.get('created', 0)} created, {len(data.get('errors', []))} errors"
        )
        errors = data.get("errors") or []
        if errors:
            print("Errors:")
            for err in errors:
                print(f"  - {err}")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Bicom Systems
# ---------------------------------------------------------------------------


def list_bicom_systems() -> Optional[List[Dict[str, Any]]]:
    print("\n=== Bicom Systems ===")
    try:
        resp = get_json("/admin/bicom-systems")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code != 200:
        _handle_error(resp)
        return None

    systems = resp.json()
    if not systems:
        print("No bicom systems found.")
        return []

    print(f"\n{'ID':<20} {'Name':<24} {'Health':<12} {'Enabled':<8} {'Tenant':<20}")
    print("-" * 90)
    for s in systems:
        sid = str(s.get("id", ""))[:20]
        name = str(s.get("name", ""))[:24]
        health = str(s.get("health_status", "unknown"))[:12]
        enabled = "✅" if s.get("enabled", True) else "❌"
        tenant = str(s.get("tenant_id") or "-")[:20]
        print(f"{sid:<20} {name:<24} {health:<12} {enabled:<8} {tenant:<20}")

    print(f"\nTotal: {len(systems)} systems")
    return systems


def get_bicom_system(system_id: str) -> Optional[Dict[str, Any]]:
    try:
        resp = get_json(f"/admin/bicom-systems/{system_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 200:
        return resp.json()
    return None


def show_bicom_system_details() -> None:
    system_id = input("Bicom System ID: ").strip()
    if not system_id:
        print("Bicom System ID required.")
        return

    print(f"\n=== Bicom System: {system_id} ===")
    system = get_bicom_system(system_id)
    if not system:
        print("Bicom system not found.")
        return

    print(f"  ID:           {system.get('id')}")
    print(f"  Name:         {system.get('name')}")
    print(f"  API URL:      {system.get('api_url')}")
    print(f"  Tenant ID:    {system.get('tenant_id') or 'N/A'}")
    print(f"  ARI URL:      {system.get('ari_url') or 'N/A'}")
    print(f"  ARI User:     {system.get('ari_user') or 'N/A'}")
    print(f"  ARI App:      {system.get('ari_app_name') or 'N/A'}")
    print(f"  Webhook URL:  {system.get('webhook_url') or 'N/A'}")
    print(f"  Health:       {system.get('health_status', 'unknown')}")
    print(f"  Enabled:      {system.get('enabled', True)}")
    settings = system.get("settings") or {}
    if settings:
        print(f"  Settings:     {json.dumps(settings, indent=4)}")
    print(f"  Created:      {system.get('created_at')}")
    print(f"  Updated:      {system.get('updated_at')}")


def create_bicom_system_interactive() -> Optional[str]:
    print("\n=== Create Bicom System ===")
    system_id = input("System ID (alphanumeric with dashes): ").strip()
    if not system_id:
        print("System ID required.")
        return None

    name = input("Display Name: ").strip()
    if not name:
        print("Name required.")
        return None

    api_url = input("API URL: ").strip()
    if not api_url:
        print("API URL required.")
        return None

    api_key = getpass.getpass("API Key: ").strip()
    if not api_key:
        print("API Key required.")
        return None

    tenant_id = input("Tenant ID (optional): ").strip() or None
    ari_url = input("ARI URL (optional): ").strip() or None
    ari_user = input("ARI User (optional): ").strip() or None
    ari_pass = getpass.getpass("ARI Password (optional): ").strip() or None
    ari_app_name = input("ARI App Name (optional): ").strip() or None
    webhook_url = input("Webhook URL (optional): ").strip() or None
    enabled = input("Enabled? [Y/n]: ").strip().lower() != "n"

    payload: Dict[str, Any] = {
        "id": system_id,
        "name": name,
        "api_url": api_url,
        "api_key": api_key,
        "enabled": enabled,
    }
    for key, val in [
        ("tenant_id", tenant_id),
        ("ari_url", ari_url),
        ("ari_user", ari_user),
        ("ari_pass", ari_pass),
        ("ari_app_name", ari_app_name),
        ("webhook_url", webhook_url),
    ]:
        if val is not None:
            payload[key] = val

    settings_str = input("Settings JSON (optional, e.g. {}): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON for settings; skipping.")

    try:
        resp = post_json("/admin/bicom-systems", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return None

    if resp.status_code == 201:
        data = resp.json()
        print(f"✅ Bicom system created: {data.get('id')}")
        return data.get("id")
    else:
        _handle_error(resp)
        return None


def update_bicom_system_interactive() -> None:
    system_id = input("Bicom System ID: ").strip()
    if not system_id:
        print("Bicom System ID required.")
        return

    print(f"\n=== Update Bicom System: {system_id} ===")
    payload: Dict[str, Any] = {}

    name = input("New Name (leave blank to skip): ").strip()
    if name:
        payload["name"] = name

    api_url = input("New API URL (leave blank to skip): ").strip()
    if api_url:
        payload["api_url"] = api_url

    api_key = getpass.getpass("New API Key (leave blank to skip): ").strip()
    if api_key:
        payload["api_key"] = api_key

    tenant_id = input("New Tenant ID (leave blank to skip, 'null' to clear): ").strip()
    if tenant_id:
        payload["tenant_id"] = None if tenant_id.lower() == "null" else tenant_id

    ari_url = input("New ARI URL (leave blank to skip): ").strip()
    if ari_url:
        payload["ari_url"] = ari_url

    ari_user = input("New ARI User (leave blank to skip): ").strip()
    if ari_user:
        payload["ari_user"] = ari_user

    ari_pass = getpass.getpass("New ARI Password (leave blank to skip): ").strip()
    if ari_pass:
        payload["ari_pass"] = ari_pass

    ari_app_name = input("New ARI App Name (leave blank to skip): ").strip()
    if ari_app_name:
        payload["ari_app_name"] = ari_app_name

    webhook_url = input("New Webhook URL (leave blank to skip): ").strip()
    if webhook_url:
        payload["webhook_url"] = webhook_url

    enabled_str = input("Enabled? [y/n/blank to skip]: ").strip()
    if enabled_str:
        payload["enabled"] = enabled_str.lower() == "y"

    settings_str = input("Settings JSON (leave blank to skip): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON; skipping settings.")

    if not payload:
        print("No fields to update.")
        return

    try:
        resp = put_json(f"/admin/bicom-systems/{system_id}", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print("✅ Bicom system updated.")
    else:
        _handle_error(resp)


def delete_bicom_system_interactive() -> None:
    system_id = input("Bicom System ID to delete: ").strip()
    if not system_id:
        print("Bicom System ID required.")
        return

    if not _confirm(f"Delete bicom system '{system_id}'?"):
        print("Cancelled.")
        return

    try:
        resp = delete_json(f"/admin/bicom-systems/{system_id}")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 204:
        print("✅ Bicom system deleted.")
    else:
        _handle_error(resp)


def update_ari_secret_interactive() -> None:
    system_id = input("Bicom System ID: ").strip()
    if not system_id:
        print("Bicom System ID required.")
        return

    ari_pass = getpass.getpass("New ARI Password: ").strip()
    if not ari_pass:
        print("ARI password required.")
        return

    try:
        resp = put_json(
            f"/admin/bicom-systems/{system_id}/ari-secret", {"ari_pass": ari_pass}
        )
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        print("✅ ARI secret updated.")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# PBX Status / Reload
# ---------------------------------------------------------------------------


def list_pbx_status() -> None:
    print("\n=== PBX Status ===")
    try:
        resp = get_json("/admin/pbx/status")
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code != 200:
        _handle_error(resp)
        return

    systems = resp.json()
    if not systems:
        print("No PBX systems found.")
        return

    print(f"\n{'System ID':<24} {'State':<14} {'Last Seen':<20}")
    print("-" * 60)
    for s in systems:
        sid = str(s.get("system_id", ""))[:24]
        state = str(s.get("state", ""))[:14]
        last_seen = str(s.get("last_seen", ""))[:20]
        print(f"{sid:<24} {state:<14} {last_seen:<20}")

    print(f"\nTotal: {len(systems)} systems")


def reload_pbx_system() -> None:
    system_id = input("System ID to reload: ").strip()
    if not system_id:
        print("System ID required.")
        return

    try:
        resp = post_json(f"/admin/pbx/{system_id}/reload", {})
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        data = resp.json()
        print(f"✅ Reloading: {data.get('status')} (scope: {data.get('scope')})")
    else:
        _handle_error(resp)


def reload_all_pbx() -> None:
    try:
        resp = post_json("/admin/pbx/reload", {})
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 200:
        data = resp.json()
        print(f"✅ Reloading: {data.get('status')} (scope: {data.get('scope')})")
    else:
        _handle_error(resp)


# ---------------------------------------------------------------------------
# Quick Create Flows
# ---------------------------------------------------------------------------


def quick_create_site_tenant() -> None:
    print("\n=== Quick Create: Site → Tenant ===")
    site_id = create_site_interactive()
    if not site_id:
        print("Site creation failed or cancelled.")
        return

    if not _confirm("Create a tenant for this site?"):
        print("Done.")
        return

    print(f"\nCreating tenant linked to site '{site_id}'...")
    tenant_id = input("Tenant ID: ").strip()
    if not tenant_id:
        print("Tenant ID required.")
        return

    name = input("Tenant Name: ").strip()
    if not name:
        print("Name required.")
        return

    print("\nPMS Protocol:")
    print("  1) mitel")
    print("  2) fias")
    print("  3) tigertms")
    pms_choice = input("Choose [1-3, default=1]: ").strip()
    pms_protocols = {"1": "mitel", "2": "fias", "3": "tigertms"}
    pms_protocol = pms_protocols.get(pms_choice, "mitel")

    print("\nPBX Type:")
    print("  1) bicom")
    print("  2) zultys")
    print("  3) freeswitch")
    pbx_choice = input("Choose [1-3, default=1]: ").strip()
    pbx_types = {"1": "bicom", "2": "zultys", "3": "freeswitch"}
    pbx_type = pbx_types.get(pbx_choice, "bicom")

    enabled = input("Enabled? [Y/n]: ").strip().lower() != "n"

    payload = {
        "id": tenant_id,
        "name": name,
        "site_id": site_id,
        "pms_config": {"protocol": pms_protocol},
        "pbx_config": {"type": pbx_type},
        "enabled": enabled,
    }

    settings_str = input("Settings JSON (optional): ").strip()
    if settings_str:
        try:
            payload["settings"] = json.loads(settings_str)
        except json.JSONDecodeError:
            print("Invalid JSON; skipping settings.")

    try:
        resp = post_json("/admin/tenants", payload)
    except requests.RequestException as e:
        print(f"Network error: {e}")
        return

    if resp.status_code == 201:
        data = resp.json()
        print(f"✅ Tenant created: {data.get('id')}")
    else:
        _handle_error(resp)


def quick_create_bicom_system() -> None:
    print("\n=== Quick Create: Bicom System ===")
    system_id = create_bicom_system_interactive()
    if not system_id:
        print("Bicom system creation failed or cancelled.")
        return

    # Optionally map to site
    if _confirm("Map this bicom system to a site?"):
        site_id = input("Site ID: ").strip()
        if site_id:
            priority_str = input("Priority (default 0): ").strip()
            try:
                priority = int(priority_str) if priority_str else 0
            except ValueError:
                priority = 0
            failover = input("Failover enabled? [Y/n]: ").strip().lower() != "n"

            payload = {
                "bicom_system_id": system_id,
                "priority": priority,
                "failover_enabled": failover,
            }
            try:
                resp = post_json(f"/admin/sites/{site_id}/bicom", payload)
            except requests.RequestException as e:
                print(f"Network error: {e}")
                return

            if resp.status_code == 201:
                print("✅ Mapped to site.")
            else:
                _handle_error(resp)
        else:
            print("Site ID required; skipping mapping.")


# ---------------------------------------------------------------------------
# Menu
# ---------------------------------------------------------------------------


def menu() -> None:
    last_site: Optional[str] = None
    last_tenant: Optional[str] = None
    last_bicom: Optional[str] = None

    while True:
        print("\n" + "=" * 64)
        print(" PBX Hospitality Admin CLI ".center(64, "="))
        print("=" * 64)
        print(f"Base URL: {base_url()}")
        if last_site:
            print(f"Last site:   {last_site}")
        if last_tenant:
            print(f"Last tenant: {last_tenant}")
        if last_bicom:
            print(f"Last bicom:  {last_bicom}")

        print("\n📍 Sites:")
        print("  1) List sites")
        print("  2) Show site details")
        print("  3) Create site")
        print("  4) Update site")
        print("  5) Delete site")
        print("  6) Site health")
        print("  7) Site bicom mappings")
        print("  8) Bicom systems for site")

        print("\n🏨 Tenants:")
        print("  9) List tenants")
        print("  a) Show tenant details")
        print("  b) Create tenant")
        print("  c) Update tenant")
        print("  d) Delete tenant")
        print("  e) Tenant rooms")
        print("  f) Tenant sessions")
        print("  g) Tenant events")
        print("  h) Tenant health")
        print("  i) Import tenants (bulk)")

        print("\n🖥️  Bicom Systems:")
        print("  j) List bicom systems")
        print("  k) Show bicom system details")
        print("  l) Create bicom system")
        print("  m) Update bicom system")
        print("  n) Delete bicom system")
        print("  o) Update ARI secret")

        print("\n📡 PBX:")
        print("  p) PBX status")
        print("  q) Reload PBX (specific)")
        print("  r) Reload all PBX")

        print("\n⚡ Quick Create:")
        print("  s) Quick create: site → tenant")
        print("  t) Quick create: bicom system")

        print("\n  0) Exit")

        choice = input("\n> ").strip().lower()

        if choice == "1":
            sites = list_sites()
            if sites:
                last_site = str(sites[0].get("id", ""))

        elif choice == "2":
            sid = input("Site ID: ").strip() or last_site
            if sid:
                show_site_details()
                last_site = sid
            else:
                print("Site ID required.")

        elif choice == "3":
            created = create_site_interactive()
            if created:
                last_site = created

        elif choice == "4":
            update_site_interactive()

        elif choice == "5":
            delete_site_interactive()

        elif choice == "6":
            site_health()

        elif choice == "7":
            print("\n  1) List mappings")
            print("  2) Add mapping")
            print("  3) Remove mapping")
            sub = input("  > ").strip()
            if sub == "1":
                list_site_bicom_mappings()
            elif sub == "2":
                add_site_bicom_mapping()
            elif sub == "3":
                remove_site_bicom_mapping()
            else:
                print("Invalid choice.")

        elif choice == "8":
            list_site_bicom_systems()

        elif choice == "9":
            tenants = list_tenants()
            if tenants:
                last_tenant = str(tenants[0].get("id", ""))

        elif choice == "a":
            tid = input("Tenant ID: ").strip() or last_tenant
            if tid:
                show_tenant_details()
                last_tenant = tid
            else:
                print("Tenant ID required.")

        elif choice == "b":
            created = create_tenant_interactive()
            if created:
                last_tenant = created

        elif choice == "c":
            update_tenant_interactive()

        elif choice == "d":
            delete_tenant_interactive()

        elif choice == "e":
            print("\n  1) List rooms")
            print("  2) Get room")
            print("  3) Delete room")
            sub = input("  > ").strip()
            if sub == "1":
                list_tenant_rooms()
            elif sub == "2":
                get_tenant_room()
            elif sub == "3":
                delete_tenant_room()
            else:
                print("Invalid choice.")

        elif choice == "f":
            print("\n  1) List sessions")
            print("  2) Get session")
            print("  3) Delete session")
            sub = input("  > ").strip()
            if sub == "1":
                list_tenant_sessions()
            elif sub == "2":
                get_tenant_session()
            elif sub == "3":
                delete_tenant_session()
            else:
                print("Invalid choice.")

        elif choice == "g":
            print("\n  1) List events")
            print("  2) Delete event")
            print("  3) Retry event")
            sub = input("  > ").strip()
            if sub == "1":
                list_tenant_events()
            elif sub == "2":
                delete_tenant_event()
            elif sub == "3":
                retry_tenant_event()
            else:
                print("Invalid choice.")

        elif choice == "h":
            tenant_health()

        elif choice == "i":
            import_tenants_interactive()

        elif choice == "j":
            systems = list_bicom_systems()
            if systems:
                last_bicom = str(systems[0].get("id", ""))

        elif choice == "k":
            bid = input("Bicom System ID: ").strip() or last_bicom
            if bid:
                show_bicom_system_details()
                last_bicom = bid
            else:
                print("Bicom System ID required.")

        elif choice == "l":
            created = create_bicom_system_interactive()
            if created:
                last_bicom = created

        elif choice == "m":
            update_bicom_system_interactive()

        elif choice == "n":
            delete_bicom_system_interactive()

        elif choice == "o":
            update_ari_secret_interactive()

        elif choice == "p":
            list_pbx_status()

        elif choice == "q":
            reload_pbx_system()

        elif choice == "r":
            reload_all_pbx()

        elif choice == "s":
            quick_create_site_tenant()

        elif choice == "t":
            quick_create_bicom_system()

        elif choice == "0":
            print("Bye!")
            break

        else:
            print("Invalid choice.")


def main() -> None:
    try:
        menu()
    except KeyboardInterrupt:
        print("\nInterrupted. Bye!")


if __name__ == "__main__":
    main()
