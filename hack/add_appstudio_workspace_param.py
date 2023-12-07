#!/usr/bin/env python

"""
Add the appstudio_workspace parameter to Pipelines as Code repository objects.
The value of the parameter is based on the appstudio.redhat.com/workspace_name
label found in the repository's namespace.
"""

from __future__ import annotations

import argparse
import fnmatch
import json
import logging
import shlex
import shutil
import subprocess
from typing import Any, Iterable, NamedTuple

WORKSPACE_LABEL_NAME = "appstudio.redhat.com/workspace_name"
WORKSPACE_PARAM_NAME = "appstudio_workspace"

log = logging.getLogger(__name__)
handler = logging.StreamHandler()
handler.setFormatter(logging.Formatter("%(asctime)s [%(levelname)7s] %(message)s"))
log.addHandler(handler)
log.setLevel(logging.INFO)


def argtype_repo_pattern(arg_value: str) -> str:
    # if the user specified only namespace_pattern, convert to namespace_pattern:*
    if ":" not in arg_value:
        return arg_value + ":*"
    return arg_value


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.epilog = (
        f"To update all repositories in all namespaces: {parser.prog} '*' --no-dry-run"
    )
    parser.add_argument(
        "repository_pattern",
        nargs="+",
        type=argtype_repo_pattern,
        help=(
            "Specify the Repository objects to process. "
            "Accepts patterns in the form namespace_name[:repository_name]. Supports globs. "
            "If repository_name is omitted, it defaults to '*'. "
            "To specify all repositories in all namespaces, use '*' or '*:*'."
        ),
    )
    parser.add_argument(
        "--output-failed",
        type=argparse.FileType(mode="w"),
        help="File where to write the namespace:name of the repositories that failed to update.",
    )
    parser.add_argument("-v", "--verbose", action="store_true", help="Be more verbose.")
    parser.add_argument("--no-dry-run", action="store_true", help="Apply the patches.")
    return parser.parse_args()


def main() -> None:
    args = parse_args()
    if args.verbose:
        log.setLevel(logging.DEBUG)

    repos = get_repository_objects(args.repository_pattern)
    namespace_to_workspace_map = map_namespaces_to_workspaces(
        repo["metadata"]["namespace"] for repo in repos
    )
    patches = generate_params_patches(repos, namespace_to_workspace_map)

    for patch in patches:
        log.info(
            "%s:%s -> %s=%s",
            patch.repo_namespace,
            patch.repo_name,
            WORKSPACE_PARAM_NAME,
            patch.workspace_param_value,
        )
    log.info("%d/%d repos need to be patched", len(patches), len(repos))

    if not args.no_dry_run:
        if patches:
            log.info("to apply the patches, run with --no-dry-run")
        return

    failed_to_update = []
    for patch in patches:
        try:
            apply_patch(patch)
        except subprocess.CalledProcessError:
            log.warning("failed to update %s:%s", patch.repo_namespace, patch.repo_name)
            failed_to_update.append(f"{patch.repo_namespace}:{patch.repo_name}")

    if failed_to_update and args.output_failed is not None:
        print("\n".join(failed_to_update), file=args.output_failed)


def get_repository_objects(repo_patterns: list[str]) -> list[dict[str, Any]]:
    log.info("getting repository objects")
    repos_list = json.loads(
        kubectl("get", "repository", "--all-namespaces", "-o", "json")
    )

    def matches(repo_obj: dict[str, Any], pattern: str) -> bool:
        namespaced_name = ":".join(get_namespace_and_name(repo_obj))
        return fnmatch.fnmatch(namespaced_name, pattern)

    matching_repos = [
        repo_obj
        for repo_obj in repos_list["items"]
        if any(matches(repo_obj, pattern) for pattern in repo_patterns)
    ]
    log.info("found %d repos matching the specified patterns", len(matching_repos))
    return matching_repos


def get_namespace_and_name(repo_obj: dict[str, Any]) -> tuple[str, str]:
    namespace = repo_obj["metadata"]["namespace"]
    name = repo_obj["metadata"]["name"]
    return namespace, name


def map_namespaces_to_workspaces(namespace_names: Iterable[str]) -> dict[str, str]:
    namespace_names = set(namespace_names)
    if not namespace_names:
        return {}

    log.info("getting namespace objects to find the %s labels", WORKSPACE_LABEL_NAME)
    namespaces_list = json.loads(kubectl("get", "namespace", "-o", "json"))
    namespaces = [
        ns
        for ns in namespaces_list["items"]
        if ns["metadata"]["name"] in namespace_names
    ]

    namespace_to_workspace = {}
    for namespace_obj in namespaces:
        namespace_name = namespace_obj["metadata"]["name"]
        workspace_name = (
            namespace_obj["metadata"].get("labels", {}).get(WORKSPACE_LABEL_NAME)
        )
        if workspace_name:
            namespace_to_workspace[namespace_name] = workspace_name
        else:
            log.warning(
                "the '%s' namespace does not have the workspace label", namespace_name
            )

    return namespace_to_workspace


class RepositoryPatch(NamedTuple):
    repo_namespace: str
    repo_name: str
    merge_patch: dict[str, Any]

    @property
    def workspace_param_value(self) -> str:
        return next(
            param["value"]
            for param in self.merge_patch["spec"]["params"]
            if param["name"] == WORKSPACE_PARAM_NAME
        )


def generate_params_patches(
    repos: list[dict[str, Any]], namespace_to_workspace_map: dict[str, str]
) -> list[RepositoryPatch]:
    def generate_patch(repo_obj: dict[str, Any]) -> RepositoryPatch | None:
        repo_namespace, repo_name = get_namespace_and_name(repo_obj)
        workspace = namespace_to_workspace_map.get(repo_namespace)
        if not workspace:
            log.info(
                "%s:%s: will not patch (namespace does not have the workspace label)",
                repo_namespace,
                repo_name,
            )
            return None

        params = repo_obj["spec"].get("params", []).copy()
        correct_workspace_param = {"name": WORKSPACE_PARAM_NAME, "value": workspace}

        for i, param in enumerate(params):
            if param["name"] == WORKSPACE_PARAM_NAME:
                if param == correct_workspace_param:
                    log.info(
                        "%s:%s: will not patch (already has the correct workspace param)",
                        repo_namespace,
                        repo_name,
                    )
                    return None
                else:
                    params[i] = correct_workspace_param
                    break
        else:
            # doesn't have any "appstudio_workspace" param
            params.append(correct_workspace_param)

        return RepositoryPatch(repo_namespace, repo_name, {"spec": {"params": params}})

    patches = list(filter(None, map(generate_patch, repos)))
    return patches


def apply_patch(patch: RepositoryPatch) -> None:
    log.info("patching %s:%s", patch.repo_namespace, patch.repo_name)
    kubectl(
        "-n",
        patch.repo_namespace,
        "patch",
        "repository",
        patch.repo_name,
        "--patch",
        json.dumps(patch.merge_patch),
        "--type=merge",
    )


def kubectl(*cmd: str) -> str:
    executable_path = shutil.which("kubectl") or shutil.which("oc")
    if not executable_path:
        raise ValueError("Could not find either 'kubectl' or 'oc' in PATH")

    cmd = (executable_path, *cmd)
    log.debug("running cmd: %s", shlex.join(cmd))

    proc = subprocess.run(cmd, capture_output=True, text=True)
    try:
        proc.check_returncode()
    except subprocess.CalledProcessError:
        log.error(
            "%s failed:\nSTDOUT:\n%s\nSTDERR:\n%s",
            shlex.join(cmd),
            proc.stdout,
            proc.stderr,
        )
        raise

    return proc.stdout


if __name__ == "__main__":
    main()
