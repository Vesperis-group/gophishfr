#!/usr/bin/env python3
"""Deterministically verify the Ansible bootstrap-secret precedence wiring."""

from pathlib import Path
import re
from typing import Optional


ROOT = Path(__file__).resolve().parent.parent
ROLE = ROOT / "ansible-playbook" / "roles" / "gophish"
VARIABLE = "gophish_initial_admin_password"
SECRET_PATH = (
    "/home/{{ gophish_user }}/gophish_deploy/.initial-admin-password"
)


def require(condition: bool, message: str) -> None:
    if not condition:
        raise AssertionError(message)


def task_block(tasks: str, name: str) -> str:
    match = re.search(
        rf"(?ms)^- name: {re.escape(name)}\n(.*?)(?=^- name: |\Z)", tasks
    )
    require(match is not None, f"missing task: {name}")
    return match.group(1)


def effective_value(default: str, override: Optional[str]) -> str:
    # Role defaults are Ansible's lowest-precedence variables. Inventory, play,
    # and Vault inputs therefore replace this value when configured.
    return default if override is None else override


def main() -> None:
    defaults = (ROLE / "defaults" / "main.yml").read_text(encoding="utf-8")
    role_vars = (ROLE / "vars" / "main.yml").read_text(encoding="utf-8")
    tasks = (ROLE / "tasks" / "main.yml").read_text(encoding="utf-8")
    unit = (ROLE / "templates" / "gophish.service.j2").read_text(encoding="utf-8")

    require(
        re.search(rf"(?m)^{VARIABLE}:\s*\"\"\s*$", defaults) is not None,
        "bootstrap password must have an empty role default",
    )
    require(
        re.search(rf"(?m)^{VARIABLE}:", role_vars) is None,
        "role vars would override inventory, play, and Vault input",
    )

    secret_task = task_block(tasks, "Install initial administrator password input")
    require(f'content: "{{{{ {VARIABLE} }}}}"' in secret_task, "override not copied")
    require(f'dest: "{SECRET_PATH}"' in secret_task, "unexpected secret destination")
    require('owner: "{{ gophish_user }}"' in secret_task, "wrong secret owner")
    require('group: "{{ gophish_user }}"' in secret_task, "wrong secret group")
    require('mode: "0400"' in secret_task, "secret must be owner-readable only")
    require(f"when: {VARIABLE} | length > 0" in secret_task, "missing empty guard")
    require(re.search(r"(?m)^  no_log: true$", secret_task) is not None, "secret task logs")

    unit_task = task_block(tasks, "Ensure GophishFR service file is properly set")
    require(re.search(r"(?m)^  no_log: true$", unit_task) is not None, "unit task logs")
    require('User={{ gophish_user }}' in unit, "service user changed")
    require(f"{{% if {VARIABLE} | length > 0 %}}" in unit, "unit guard missing")
    require(
        f"Environment=\"GOPHISH_INITIAL_ADMIN_PASSWORD_FILE={SECRET_PATH}\"" in unit,
        "unit does not reference the provisioned input file",
    )

    default = ""
    require(
        effective_value(default, None) == "",
        "unset role default must skip provisioning for initialized installs",
    )
    for source in ("inventory", "play", "vault"):
        override = f"synthetic-{source}-override"
        resolved = effective_value(default, override)
        require(resolved == override and len(resolved) > 0, f"{source} override blocked")

    print("Ansible bootstrap precedence, ownership, no-log, and unset-default checks passed")


if __name__ == "__main__":
    main()
