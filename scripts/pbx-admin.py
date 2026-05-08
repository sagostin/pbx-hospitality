#!/usr/bin/env python3
"""PBX Hospitality Management TUI"""

import json
import sys
from dataclasses import dataclass
from typing import Optional, Callable

import requests
from textual.app import App, ComposeResult
from textual.binding import Binding
from textual.containers import Container, Horizontal, Vertical
from textual.events import Key
from textual.widgets import (
    Button,
    DataTable,
    Footer,
    Header,
    Input,
    Label,
    Static,
    TextArea,
)


@dataclass
class Config:
    api_url: str
    admin_key: str


def load_config() -> Config:
    import os
    from pathlib import Path

    config_paths = [
        Path.home() / ".pbx-admin.yaml",
        Path.home() / ".pbx-admin.yml",
        Path(".pbx-admin.yaml"),
        Path(".pbx-admin.yml"),
    ]

    for path in config_paths:
        if path.exists():
            import yaml

            with open(path) as f:
                data = yaml.safe_load(f) or {}
            return Config(
                api_url=data.get("api_url", "http://localhost:8080"),
                admin_key=data.get("admin_key", ""),
            )

    api_url = os.environ.get("PBX_API_URL", "http://localhost:8080")
    admin_key = os.environ.get("PBX_ADMIN_KEY", "")
    return Config(api_url=api_url, admin_key=admin_key)


class APIError(Exception):
    def __init__(self, status_code: int, message: str, code: str = ""):
        self.status_code = status_code
        self.message = message
        self.code = code
        super().__init__(f"[{code}] {message}")


class APIClient:
    def __init__(self, config: Config):
        self.config = config
        self.session = requests.Session()
        self.session.headers.update({"X-Admin-Key": config.admin_key})

    def _request(self, method: str, path: str, **kwargs) -> dict:
        url = f"{self.config.api_url}{path}"
        try:
            response = self.session.request(method, url, timeout=10, **kwargs)
        except requests.exceptions.ConnectionError:
            raise APIError(
                0, f"Cannot connect to {self.config.api_url}", "CONNECTION_ERROR"
            )
        except requests.exceptions.Timeout:
            raise APIError(0, "Request timed out", "TIMEOUT")

        if not response.ok:
            try:
                err_data = response.json()
                message = err_data.get("error", response.text)
                code = err_data.get("code", "")
            except Exception:
                message = response.text or f"HTTP {response.status_code}"
                code = ""
            raise APIError(response.status_code, message, code)

        if response.status_code == 204:
            return {}
        return response.json()

    def get(self, path: str) -> dict:
        return self._request("GET", path)

    def list(self, path: str) -> list:
        result = self._request("GET", path)
        if isinstance(result, list):
            return result
        return []

    def post(self, path: str, json: dict = None) -> dict:
        return self._request("POST", path, json=json)

    def put(self, path: str, json: dict = None) -> dict:
        return self._request("PUT", path, json=json)

    def delete(self, path: str) -> dict:
        return self._request("DELETE", path)


class FormField:
    def __init__(
        self, key: str, label: str, field_type: str = "text", options: list = None
    ):
        self.key = key
        self.label = label
        self.type = field_type
        self.options = options or []


class PBXAdminApp(App):
    CSS = """
    Screen {
        background: $surface;
    }
    #sidebar {
        width: 25;
        background: $panel;
        border-right: solid $border;
    }
    #sidebar Button {
        width: 100%;
        margin: 1 0;
    }
    #sidebar Button:hover {
        background: $primary-darken-1;
    }
    #content {
        width: 100%;
    }
    .screen-title {
        height: 3;
        margin: 1 2;
        text-style: bold;
    }
    .error-text {
        color: $error;
        padding: 2 4;
    }
    .info-text {
        color: $text-muted;
        padding: 1 2;
    }
    #form-container {
        padding: 2 4;
    }
    .form-label {
        margin-top: 1;
        color: $text-muted;
    }
    .form-buttons {
        margin-top: 2;
        height: 3;
    }
    DataTable {
        height: 80%;
        margin: 1 2;
    }
    #action-buttons {
        height: 3;
        margin: 1 2;
    }
    """

    BINDINGS = [
        Binding("q", "quit", "Quit"),
        Binding("r", "refresh", "Refresh"),
    ]

    def __init__(self, config: Config):
        super().__init__()
        self.config = config
        self.title = "PBX Hospitality Admin"
        self.client = APIClient(config)
        self.current_view = "tenants"
        self.selected_item = None

    def compose(self) -> ComposeResult:
        yield Header()
        with Horizontal(id="main-layout"):
            with Vertical(id="sidebar"):
                yield Label("Navigation", classes="info-text")
                yield Button("Tenants", id="nav-tenants", variant="primary")
                yield Button("Sites", id="nav-sites")
                yield Button("Bicom Systems", id="nav-bicom")
                yield Button("Room Mappings", id="nav-rooms")
                yield Button("Sessions", id="nav-sessions")
                yield Button("PMS Events", id="nav-events")
                yield Button("PBX Status", id="nav-pbx")
            with Vertical(id="content"):
                yield Static("Loading...", classes="screen-title")
                yield DataTable(id="main-table")
                with Horizontal(id="action-buttons"):
                    yield Button(
                        "Create", id="btn-create", variant="primary", disabled=True
                    )
                    yield Button(
                        "Delete", id="btn-delete", variant="error", disabled=True
                    )
                    yield Button(
                        "Reload All", id="btn-reload", variant="warning", disabled=True
                    )

    def on_mount(self) -> None:
        self.setup_table()
        self.load_view("tenants")

    def setup_table(self) -> None:
        table = self.query_one("#main-table")
        table.clear()
        table.focus()

    def on_button_pressed(self, event) -> None:
        btn_id = event.button.id
        if btn_id.startswith("nav-"):
            view = btn_id[4:]
            self.current_view = view
            self.load_view(view)
        elif btn_id == "btn-create":
            self.handle_create()
        elif btn_id == "btn-delete":
            self.handle_delete()
        elif btn_id == "btn-reload":
            self.handle_reload()
        elif btn_id == "btn-cancel":
            self.cancel_form()
        elif btn_id == "btn-save":
            self.save_form()

    def on_data_table_row_selected(self, event) -> None:
        table = self.query_one("#main-table")
        if table.cursor_row is not None and table.cursor_row < len(self.current_rows):
            self.selected_item = self.current_rows[table.cursor_row]

    def load_view(self, view: str) -> None:
        title = self.query_one(".screen-title")
        table = self.query_one("#main-table")
        table.clear()

        create_btn = self.query_one("#btn-create")
        delete_btn = self.query_one("#btn-delete")
        reload_btn = self.query_one("#btn-reload")

        create_btn.disabled = False
        delete_btn.disabled = True
        reload_btn.disabled = True

        try:
            if view == "tenants":
                title.update("Tenants")
                table.add_columns("ID", "Name", "Site", "Enabled")
                rows = self.client.list("/admin/tenants")
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("id", ""),
                        r.get("name", ""),
                        r.get("site_id") or "",
                        "Yes" if r.get("enabled") else "No",
                    )

            elif view == "sites":
                title.update("Sites")
                table.add_columns("ID", "Name", "Enabled")
                rows = self.client.list("/admin/sites")
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("id", ""),
                        r.get("name", ""),
                        "Yes" if r.get("enabled") else "No",
                    )

            elif view == "bicom":
                title.update("Bicom Systems")
                table.add_columns("ID", "Name", "API URL", "Health", "Enabled")
                rows = self.client.list("/admin/bicom-systems")
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("id", ""),
                        r.get("name", ""),
                        r.get("api_url", "")[:40] + "..."
                        if len(r.get("api_url", "")) > 40
                        else r.get("api_url", ""),
                        r.get("health_status", "unknown"),
                        "Yes" if r.get("enabled") else "No",
                    )

            elif view == "rooms":
                title.update("Room Mappings")
                table.add_columns("Tenant", "Room", "Extension", "Room End", "Ext End")
                rows = self.collect_all_rooms()
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("tenant_id", ""),
                        r.get("room_number", ""),
                        r.get("extension", ""),
                        r.get("room_end") or "",
                        r.get("extension_end") or "",
                    )

            elif view == "sessions":
                title.update("Guest Sessions")
                table.add_columns(
                    "Tenant", "Room", "Extension", "Guest Name", "Check In", "Check Out"
                )
                rows = self.collect_all_sessions()
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("tenant_id", ""),
                        r.get("room_number", ""),
                        r.get("extension", "") or "-",
                        r.get("guest_name", "") or "-",
                        self.format_date(r.get("check_in")),
                        self.format_date(r.get("check_out")),
                    )

            elif view == "events":
                title.update("PMS Events")
                table.add_columns("Tenant", "ID", "Type", "Room", "Processed", "Error")
                rows = self.collect_all_events()
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("tenant_id", ""),
                        str(r.get("id", "")),
                        r.get("event_type", ""),
                        r.get("room_number", "") or "-",
                        "Yes" if r.get("processed") else "No",
                        (r.get("error", "") or "")[:30],
                    )

            elif view == "pbx":
                title.update("PBX Status")
                reload_btn.disabled = False
                table.add_columns("System ID", "State", "Last Seen")
                rows = self.client.list("/admin/pbx/status")
                self.current_rows = rows
                for r in rows:
                    table.add_row(
                        r.get("system_id", ""),
                        r.get("state", ""),
                        r.get("last_seen", "")[:19] if r.get("last_seen") else "-",
                    )

        except APIError as e:
            title.update(f"Error: {e.message}")
            self.current_rows = []

    def collect_all_rooms(self) -> list:
        rows = []
        for tenant in self.client.list("/admin/tenants"):
            rooms = self.client.list(f"/admin/tenants/{tenant['id']}/rooms")
            for r in rooms:
                r["tenant_id"] = tenant["id"]
                r["tenant_name"] = tenant["name"]
            rows.extend(rooms)
        return rows

    def collect_all_sessions(self) -> list:
        rows = []
        for tenant in self.client.list("/admin/tenants"):
            sessions = self.client.list(f"/admin/tenants/{tenant['id']}/sessions")
            for s in sessions:
                s["tenant_id"] = tenant["id"]
            rows.extend(sessions)
        return rows

    def collect_all_events(self) -> list:
        rows = []
        for tenant in self.client.list("/admin/tenants"):
            events = self.client.list(f"/admin/tenants/{tenant['id']}/events")
            for e in events:
                e["tenant_id"] = tenant["id"]
            rows.extend(events)
        return rows

    def format_date(self, dt: str) -> str:
        if not dt:
            return "-"
        if len(dt) > 10:
            return dt[:10]
        return dt

    def handle_create(self) -> None:
        if self.current_view == "tenants":
            self.show_tenant_form()
        elif self.current_view == "sites":
            self.show_site_form()
        elif self.current_view == "bicom":
            self.show_bicom_form()

    def handle_delete(self) -> None:
        if not self.selected_item:
            return
        if self.current_view == "tenants":
            self.delete_tenant(self.selected_item["id"])
        elif self.current_view == "sites":
            self.delete_site(self.selected_item["id"])
        elif self.current_view == "bicom":
            self.delete_bicom(self.selected_item["id"])

    def handle_reload(self) -> None:
        if self.current_view == "pbx":
            try:
                self.client.post("/admin/pbx/reload")
                self.load_view("pbx")
            except APIError as e:
                self.show_error(str(e))

    def show_error(self, msg: str) -> None:
        title = self.query_one(".screen-title")
        title.update(f"Error: {msg}")

    def show_tenant_form(self, data: dict = None) -> None:
        content = self.query_one("#content")
        content.remove_children()
        content.mount(
            Static(
                "Create Tenant" if not data else f"Edit Tenant: {data.get('id', '')}",
                classes="screen-title",
            )
        )

        form = Vertical(id="form-container")
        form.mount(Label("ID (alphanumeric, dashes):", classes="form-label"))
        form.mount(
            Input(
                value=data.get("id", "") if data else "",
                id="f-id",
                disabled=data is not None,
            )
        )
        form.mount(Label("Name:", classes="form-label"))
        form.mount(Input(value=data.get("name", "") if data else "", id="f-name"))
        form.mount(Label("Site ID:", classes="form-label"))
        form.mount(
            Input(value=data.get("site_id", "") or "" if data else "", id="f-site_id")
        )
        form.mount(Label("PMS Protocol:", classes="form-label"))
        pms_select = Select(
            options=[
                ("", ""),
                ("mitel", "mitel"),
                ("fias", "fias"),
                ("tigertms", "tigertms"),
            ],
            id="f-pms_protocol",
            value=data.get("pms_config", {}).get("protocol", "") if data else "",
        )
        form.mount(pms_select)
        form.mount(Label("PBX Type:", classes="form-label"))
        pbx_select = Select(
            options=[("", ""), ("bicom", "bicom"), ("zultys", "zultys")],
            id="f-pbx_type",
            value=data.get("pbx_config", {}).get("type", "") if data else "",
        )
        form.mount(pbx_select)
        form.mount(Label("Enabled:", classes="form-label"))
        form.mount(
            Switch(value=data.get("enabled", True) if data else True, id="f-enabled")
        )
        form.mount(Label("", classes="form-label"))
        form.mount(
            Horizontal(
                Button("Save", id="btn-save", variant="primary"),
                Button("Cancel", id="btn-cancel"),
                classes="form-buttons",
            )
        )
        content.mount(form)
        self.form_mode = ("tenant", data)
        self.form_mode_id = data.get("id") if data else None

    def show_site_form(self, data: dict = None) -> None:
        content = self.query_one("#content")
        content.remove_children()
        content.mount(
            Static(
                "Create Site" if not data else f"Edit Site: {data.get('id', '')}",
                classes="screen-title",
            )
        )

        form = Vertical(id="form-container")
        form.mount(Label("ID:", classes="form-label"))
        form.mount(
            Input(
                value=data.get("id", "") if data else "",
                id="f-id",
                disabled=data is not None,
            )
        )
        form.mount(Label("Name:", classes="form-label"))
        form.mount(Input(value=data.get("name", "") if data else "", id="f-name"))
        if not data:
            form.mount(Label("Auth Code (min 16 chars):", classes="form-label"))
            form.mount(Input(value="", id="f-auth_code"))
        form.mount(Label("Enabled:", classes="form-label"))
        form.mount(
            Switch(value=data.get("enabled", True) if data else True, id="f-enabled")
        )
        form.mount(Label("", classes="form-label"))
        form.mount(
            Horizontal(
                Button("Save", id="btn-save", variant="primary"),
                Button("Cancel", id="btn-cancel"),
                classes="form-buttons",
            )
        )
        content.mount(form)
        self.form_mode = ("site", data)
        self.form_mode_id = data.get("id") if data else None

    def show_bicom_form(self, data: dict = None) -> None:
        content = self.query_one("#content")
        content.remove_children()
        content.mount(
            Static(
                "Create Bicom System"
                if not data
                else f"Edit System: {data.get('id', '')}",
                classes="screen-title",
            )
        )

        form = Vertical(id="form-container")
        form.mount(Label("ID:", classes="form-label"))
        form.mount(
            Input(
                value=data.get("id", "") if data else "",
                id="f-id",
                disabled=data is not None,
            )
        )
        form.mount(Label("Name:", classes="form-label"))
        form.mount(Input(value=data.get("name", "") if data else "", id="f-name"))
        form.mount(Label("API URL:", classes="form-label"))
        form.mount(Input(value=data.get("api_url", "") if data else "", id="f-api_url"))
        form.mount(Label("API Key:", classes="form-label"))
        form.mount(Input(value="" if data else "", id="f-api_key"))  # Never prefill
        form.mount(Label("ARI URL:", classes="form-label"))
        form.mount(Input(value=data.get("ari_url", "") if data else "", id="f-ari_url"))
        form.mount(Label("ARI User:", classes="form-label"))
        form.mount(
            Input(value=data.get("ari_user", "") if data else "", id="f-ari_user")
        )
        form.mount(Label("ARI App Name:", classes="form-label"))
        form.mount(
            Input(
                value=data.get("ari_app_name", "") if data else "", id="f-ari_app_name"
            )
        )
        form.mount(Label("Webhook URL:", classes="form-label"))
        form.mount(
            Input(value=data.get("webhook_url", "") if data else "", id="f-webhook_url")
        )
        form.mount(Label("Enabled:", classes="form-label"))
        form.mount(
            Switch(value=data.get("enabled", True) if data else True, id="f-enabled")
        )
        form.mount(Label("", classes="form-label"))
        form.mount(
            Horizontal(
                Button("Save", id="btn-save", variant="primary"),
                Button("Cancel", id="btn-cancel"),
                classes="form-buttons",
            )
        )
        content.mount(form)
        self.form_mode = ("bicom", data)
        self.form_mode_id = data.get("id") if data else None

    def cancel_form(self) -> None:
        self.load_view(self.current_view)

    def save_form(self) -> None:
        mode, original_data = getattr(self, "form_mode", (None, None))
        if not mode:
            return

        def get_val(key: str, default="") -> str:
            widget = self.query_one(
                f"#{key}", Input | Select | Switch, expect_type=False
            )
            if widget is None:
                return default
            if isinstance(widget, Switch):
                return widget.value
            if isinstance(widget, Select):
                return widget.value or ""
            return widget.value or ""

        try:
            if mode == "tenant":
                payload = {
                    "id": self.query_one("#f-id").value,
                    "name": self.query_one("#f-name").value,
                    "enabled": self.query_one("#f-enabled").value,
                }
                site_id = self.query_one("#f-site_id").value
                if site_id:
                    payload["site_id"] = site_id
                pms = self.query_one("#f-pms_protocol").value
                if pms:
                    payload["pms_config"] = {"protocol": pms}
                pbx = self.query_one("#f-pbx_type").value
                if pbx:
                    payload["pbx_config"] = {"type": pbx}

                if original_data:  # edit mode
                    self.client.put(f"/admin/tenants/{self.form_mode_id}", json=payload)
                else:  # create mode
                    self.client.post("/admin/tenants", json=payload)

            elif mode == "site":
                payload = {
                    "name": self.query_one("#f-name").value,
                    "enabled": self.query_one("#f-enabled").value,
                }
                if not original_data:
                    payload["id"] = self.query_one("#f-id").value
                    payload["auth_code"] = self.query_one("#f-auth_code").value

                if original_data:
                    self.client.put(f"/admin/sites/{self.form_mode_id}", json=payload)
                else:
                    self.client.post("/admin/sites", json=payload)

            elif mode == "bicom":
                payload = {
                    "name": self.query_one("#f-name").value,
                    "api_url": self.query_one("#f-api_url").value,
                    "enabled": self.query_one("#f-enabled").value,
                }
                for key in ["ari_url", "ari_user", "ari_app_name", "webhook_url"]:
                    val = self.query_one(f"#f-{key}").value
                    if val:
                        payload[key] = val
                if not original_data:
                    payload["id"] = self.query_one("#f-id").value
                    payload["api_key"] = self.query_one("#f-api_key").value

                if original_data:
                    self.client.put(
                        f"/admin/bicom-systems/{self.form_mode_id}", json=payload
                    )
                else:
                    self.client.post("/admin/bicom-systems", json=payload)

            self.load_view(self.current_view)
        except APIError as e:
            self.show_error(str(e))

    def delete_tenant(self, tenant_id: str) -> None:
        try:
            self.client.delete(f"/admin/tenants/{tenant_id}")
            self.load_view("tenants")
        except APIError as e:
            self.show_error(str(e))

    def delete_site(self, site_id: str) -> None:
        try:
            self.client.delete(f"/admin/sites/{site_id}")
            self.load_view("sites")
        except APIError as e:
            self.show_error(str(e))

    def delete_bicom(self, system_id: str) -> None:
        try:
            self.client.delete(f"/admin/bicom-systems/{system_id}")
            self.load_view("bicom")
        except APIError as e:
            self.show_error(str(e))

    def action_refresh(self) -> None:
        self.load_view(self.current_view)

    def action_quit(self) -> None:
        self.exit()


def main():
    config = load_config()

    if not config.admin_key:
        print("Error: No admin key configured.")
        print("  Set PBX_ADMIN_KEY env var, or")
        print("  Create ~/.pbx-admin.yaml with 'admin_key: <key>'")
        sys.exit(1)

    app = PBXAdminApp(config)
    app.run()


if __name__ == "__main__":
    main()
