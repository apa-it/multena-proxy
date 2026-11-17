# Herkunft dieses Charts

Dieses Chart wurde aus einem fremden Repository übernommen und für `multena-proxy`
umbenannt, damit eine eigene gepatchte Version veröffentlicht werden kann.

## Quelle

- Repo: `gepaplexx/gp-helm-chart-development`
- Pfad: `infra/gp-multena`
- Branch: `develop`
- Commit-SHA: `6facc178dd06ece1a7c8acdba05b978148f66b3d`
- Chart-Version (Upstream): `1.7.0`
- App-Version (Upstream): `0.7.0`
- Lizenz des Upstream-Repos: AGPL-3.0 (bleibt unverändert, keine Lizenzarbeit in diesem Schritt)
- Keine Chart-Dependencies, kein `Chart.lock`

## Abrufdatum

2026-11-17

## Abruf-Methode

Jede der 15 Dateien einzeln per `curl` von
`https://raw.githubusercontent.com/gepaplexx/gp-helm-chart-development/<SHA>/infra/gp-multena/...`
geladen und die Byte-Größe gegen die GitHub-Contents-API (`.../contents/infra/gp-multena?ref=<SHA>`)
verifiziert:

| Datei | Bytes |
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

Alle 15 Dateien stimmten byte-genau mit den API-Metadaten überein.

## Patch-Delta (Umbenennung)

Vor der Umbenennung wurde ein `helm template`-Baseline-Render mit den Chart-Defaults
erstellt. Der Diff gegen den Render nach der Umbenennung enthält ausschließlich
umbenannte Ressourcen-/Label-Werte (`gp-multena` → `multena-proxy`), keine
strukturellen oder funktionalen Änderungen:

- `Chart.yaml`: `name: gp-multena` → `name: multena-proxy` (Version/AppVersion unverändert)
- `templates/_helpers.tpl`: alle Helper-Definitionen umbenannt:
  - `gp-multena.name` → `multena-proxy.name`
  - `gp-multena.fullname` → `multena-proxy.fullname`
  - `gp-multena.chart` → `multena-proxy.chart`
  - `gp-multena.labels` → `multena-proxy.labels`
  - `gp-multena.selectorLabels` → `multena-proxy.selectorLabels`
  - `gp-multena.serviceAccountName` → `multena-proxy.serviceAccountName`
  - `gp-multena.secretName` → `multena-proxy.secretName`
  - `gp-multena-rbac-collector.name` → `multena-proxy-rbac-collector.name`
  - `gp-multena-rbac-collector.selectorLabels` → `multena-proxy-rbac-collector.selectorLabels`
- Alle Templates (`configmaps.yaml`, `deployment.yaml`, `external-secrets.yaml`,
  `rbac.yaml`, `service.yaml`, `servicemonitor.yaml`, `system-datasources.yaml`,
  `tls-copy.yaml`, `rbac-collector/*.yaml`): Referenzen auf die obigen Helper
  entsprechend angepasst.
- Keine Templates gelöscht, keine Werte aus fremden Deployments übernommen, keine
  Lizenzänderung.

Verifiziert mit:

```
helm lint charts/multena-proxy
diff <(helm template charts/multena-proxy)  # vor/nach Umbenennung
```

Der Diff enthält ausschließlich Wert-Änderungen bei Ressourcennamen/Labels
(`gp-multena*` → `multena-proxy*`), keine neuen/entfernten Ressourcen oder
Strukturänderungen.

## Sync-Prozess (bei künftigen Upstream-Updates)

1. Neuen Commit-SHA im Upstream-Repo (`gepaplexx/gp-helm-chart-development`,
   Pfad `infra/gp-multena`, Branch `develop`) identifizieren.
2. Alle 15 Dateien erneut einzeln per `curl` laden und Byte-Größen gegen die
   GitHub-Contents-API verifizieren (nichts aus Gedächtnis/Chatverlauf übernehmen).
3. Vor jeder Änderung einen `helm template`-Baseline-Render der aktuell im Repo
   liegenden Version erstellen.
4. Neue Upstream-Inhalte einspielen, danach erneut `gp-multena` → `multena-proxy`
   umbenennen (Chart.yaml `name`, alle Helper in `_helpers.tpl`, alle Referenzen
   in den Templates).
5. `helm lint charts/multena-proxy` muss sauber sein.
6. Diff gegen den eigenen Baseline-Render erstellen und sicherstellen, dass nur
   gewollte Delta-Änderungen (Upstream-Update + Umbenennung) enthalten sind.
7. Diese Datei (`UPSTREAM.md`) mit neuem Commit-SHA, Chart-/App-Version und
   Abrufdatum aktualisieren.
8. Diesen Schritt per PR einreichen, kein eigenständiges Mergen.
