# Release notes draft - v0.8.0

> Draft for the next real release. Copy this into the GitHub Release body when
> the tag is actually pushed; this file is not itself a publish action.
>
> App version and Helm chart version are versioned together: pushing a
> single `vX.Y.Z` tag publishes both the container image and the chart
> (version `X.Y.Z`, appVersion `vX.Y.Z` - matching the pushed image tag). There is no separate
> `multena-proxy-X.Y.Z` tag.

## Breaking change

The Helm chart used to deploy Multena changed:

- **Before:** `gp-multena` from `https://gepaplexx.github.io/gp-helm-charts/`
  (upstream `gepaplexx/gp-helm-chart-development` repo).
- **Now:** `multena-proxy` from this repository's own chart repo,
  `https://apa-it.github.io/multena-proxy/`.

Existing deployments installed from the old chart must be migrated:

1. Review the new chart's `values.yaml` - release name, values keys and
   defaults are not guaranteed to match the old chart.
2. `helm repo add multena-proxy https://apa-it.github.io/multena-proxy/`
3. `helm repo update`
4. `helm upgrade multena multena-proxy/multena-proxy -n <grafana-namespace> -f <your-values>`
5. Verify the rendered manifests (`helm template ... | diff -` against the
   previous release) before applying to a production namespace.

## Fixes

- LogQL tenant-bypass closed via a negative matcher (merged in #2).

## Chart changes

- New `image.registry` and `collector.image.registry` (default `ghcr.io`) for pull-through mirrors;
  the registry is prepended to `image.repository` / `collector.image.name` unless those already contain a host.
- Hardened chart defaults: configurable `image.repository`, digest pinning,
  neutral SSO placeholder, `ExternalSecret` `labelStoreKind` bugfix (merged
  in #13).
