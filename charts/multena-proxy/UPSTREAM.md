# Provenance of this chart

This chart was taken over from a third-party repository and renamed to
`multena-proxy` so that an independently patched version can be published.

## Source

- Repo: `gepaplexx/gp-helm-chart-development`
- Path: `infra/gp-multena`
- Branch: `develop`
- Commit SHA: `6facc178dd06ece1a7c8acdba05b978148f66b3d`
- Chart version (upstream): `1.7.0`
- App version (upstream): `0.7.0`
- Upstream repo license: AGPL-3.0 (kept unchanged, no license work in this step)
- No chart dependencies, no `Chart.lock`

## Retrieval date

2026-11-17

## Retrieval method

Each of the 15 files was downloaded individually via `curl` from
`https://raw.githubusercontent.com/gepaplexx/gp-helm-chart-development/<SHA>/infra/gp-multena/...`
and the byte size was verified against the GitHub Contents API
(`.../contents/infra/gp-multena?ref=<SHA>`):

| File | Bytes |
|---|---|
| Chart.yaml | 1096 |
| values.yaml | 3248 |
| .helmignore | 349 |
| templates/_helpers.tpl | 2350 |
| templates/configmaps.yaml | 2411 |
| templates/deployment.yaml | 5042 |
| templates/external-secrets.yaml | 687 |
| templates/rbac.yaml | 588 |
| templates/service.yaml | 565 |
| templates/servicemonitor.yaml | 432 |
| templates/system-datasources.yaml | 1547 |
| templates/tls-copy.yaml | 1686 |
| templates/rbac-collector/deployment.yaml | 1910 |
| templates/rbac-collector/service.yaml | 487 |
| templates/rbac-collector/rbac.yaml | 1483 |

All 15 files matched the API metadata byte-for-byte.

## Patch delta (rename)

Before renaming, a `helm template` baseline render was produced with the
chart defaults. The diff against the render after renaming contains only
renamed resource/label values (`gp-multena` → `multena-proxy`), no
structural or functional changes:

- `Chart.yaml`: `name: gp-multena` → `name: multena-proxy` (version/appVersion unchanged)
- `templates/_helpers.tpl`: all helper definitions renamed:
  - `gp-multena.name` → `multena-proxy.name`
  - `gp-multena.fullname` → `multena-proxy.fullname`
  - `gp-multena.chart` → `multena-proxy.chart`
  - `gp-multena.labels` → `multena-proxy.labels`
  - `gp-multena.selectorLabels` → `multena-proxy.selectorLabels`
  - `gp-multena.serviceAccountName` → `multena-proxy.serviceAccountName`
  - `gp-multena.secretName` → `multena-proxy.secretName`
  - `gp-multena-rbac-collector.name` → `multena-proxy-rbac-collector.name`
  - `gp-multena-rbac-collector.selectorLabels` → `multena-proxy-rbac-collector.selectorLabels`
- All templates (`configmaps.yaml`, `deployment.yaml`, `external-secrets.yaml`,
  `rbac.yaml`, `service.yaml`, `servicemonitor.yaml`, `system-datasources.yaml`,
  `tls-copy.yaml`, `rbac-collector/*.yaml`): references to the above helpers
  updated accordingly.
- No templates removed, no values taken over from third-party deployments, no
  license change.

Verified with:

```
helm lint charts/multena-proxy
diff <(helm template charts/multena-proxy)  # before/after renaming
```

The diff contains only value changes to resource names/labels
(`gp-multena*` → `multena-proxy*`), no new/removed resources or
structural changes.

## Deviations from upstream (defaults hardening, this repo only)

Applied on top of the vendored/renamed chart, tracked here so a future
re-sync against upstream knows to re-apply them:

- `values.yaml`: `image.repository` → `ghcr.io/apa-it/multena-proxy`;
  `proxy.alert.certUrl` and `proxy.web.jwksCertUrl` neutralized from the
  upstream demo hostname `sso.play.run.gepardec.com` to `sso.example.com`.
- `values.yaml` / `templates/deployment.yaml`: added `image.digest` to
  allow pinning the proxy image by digest (`@sha256:...`) instead of tag.
- `templates/external-secrets.yaml`: fixed a pre-existing upstream bug
  where the guard condition referenced `.Values.proxy.labelStoreKind`
  (a path that does not exist) instead of `.Values.proxy.web.labelStoreKind`,
  which meant the `ExternalSecret` never rendered even with
  `proxy.db.enabled: true`.

## Sync process (for future upstream updates)

1. Identify the new commit SHA in the upstream repo (`gepaplexx/gp-helm-chart-development`,
   path `infra/gp-multena`, branch `develop`).
2. Re-download all 15 files individually via `curl` and verify byte sizes against
   the GitHub Contents API (do not carry anything over from memory/chat history).
3. Before making any change, produce a `helm template` baseline render of the
   version currently in the repo.
4. Apply the new upstream content, then rename `gp-multena` → `multena-proxy`
   again (Chart.yaml `name`, all helpers in `_helpers.tpl`, all references
   in the templates).
5. `helm lint charts/multena-proxy` must be clean.
6. Produce a diff against your own baseline render and make sure it only
   contains the intended delta (upstream update + rename).
7. Update this file (`UPSTREAM.md`) with the new commit SHA, chart/app version,
   and retrieval date.
8. Submit this step via PR, no self-merging.
