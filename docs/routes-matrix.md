# Routen-/Methodenmatrix (routes.go)

Stand: dieser Branch, nach Rebase auf `main` (Commit 3d3b224 + lokale Änderungen).
Quelle: `routes.go:56-121` (`WithLoki`, `WithThanos`).

Beide Routertabellen werden über `gorilla/mux.HandleFunc`, also nicht
methodenspezifisch, registriert — `handler()` nimmt jede HTTP-Methode an.
`enforceRequest` (enforce.go:20) grenzt danach auf GET/POST ein; alles
andere (PUT/DELETE/PATCH/HEAD/OPTIONS/…) wird mit `"invalid method"` als
403 abgelehnt (siehe `TestEnforceRequest_DispatchesByMethod`).

Enforcer greift immer (LogQLEnforcer bzw. PromQLEnforcer), außer
`validateLabels` meldet `skip=true` (Admin-Bypass oder Labelstore-Skip,
z.B. ConfigMap `#cluster-wide`).

## Loki-Routen (`WithLoki`, Prefix `/loki`, Enforcer: LogQLEnforcer)

| Pfad | MatchWord | GET | POST | Enforcer greift |
|---|---|---|---|---|
| `/api/v1/query` | `query` | ✓ (enforceGet) | ✓ (enforcePost) | ja |
| `/api/v1/query_range` | `query` | ✓ | ✓ | ja |
| `/api/v1/series` | `match[]` | ✓ | ✓ | ja |
| `/api/v1/tail` | `query` | ✓ (Streaming, kein Server-Timeout — s. Scope) | ✓ | ja |
| `/api/v1/index/stats` | `query` | ✓ | ✓ | ja |
| `/api/v1/format_query` | `query` | ✓ | ✓ | ja |
| `/api/v1/labels` | `query` | ✓ | ✓ | ja — **Achtung:** Loki nutzt hier `query`, nicht `match[]` (anders als bei Thanos, s.u.) |
| `/api/v1/label/{label}/values` | `query` | ✓ | ✓ | ja — wie `/labels`, `query` statt `match[]` |
| `/api/v1/query_exemplars` | `query` | ✓ | ✓ | ja |
| `/api/v1/status/buildinfo` | `query` | ✓ | ✓ | ja, auch **ohne** Query-Parameter: leerer `query`-Wert wird vom Enforcer zu `{tenant_label="..."}` synthetisiert (kein Bypass) |

## Thanos-Routen (`WithThanos`, Prefix `""`, Enforcer: PromQLEnforcer)

| Pfad | MatchWord | GET | POST | Enforcer greift |
|---|---|---|---|---|
| `/api/v1/query` | `query` | ✓ | ✓ | ja |
| `/api/v1/query_range` | `query` | ✓ | ✓ | ja |
| `/api/v1/series` | `match[]` | ✓ | ✓ | ja |
| `/api/v1/tail` | `query` | ✓ (kein Loki-Streaming-Endpoint bei Thanos i.d.R. ungenutzt, aber registriert) | ✓ | ja |
| `/api/v1/index/stats` | `query` | ✓ | ✓ | ja |
| `/api/v1/format_query` | `query` | ✓ | ✓ | ja |
| `/api/v1/labels` | `match[]` | ✓ | ✓ | ja — **Achtung:** hier `match[]`, nicht `query` (Unterschied zu Loki, s.o.) |
| `/api/v1/label/{label}/values` | `match[]` | ✓ | ✓ | ja — wie `/labels` |
| `/api/v1/query_exemplars` | `query` | ✓ | ✓ | ja |
| `/api/v1/status/buildinfo` | `query` | ✓ | ✓ | ja, auch ohne Query-Parameter (wie bei Loki) |
| `/api/v1/metadata` | `query` | ✓ | ✓ | ja, auch ohne Query-Parameter — Thanos-exklusiv, kein Loki-Pendant |

## Weitere Endpunkte (`WithHealthz`)

| Pfad | Auth/Enforcement |
|---|---|
| `/healthz` | keiner — liefert 200/500 je nach `a.healthy` |
| `/debug/pprof/` | keiner (pprof-Standardhandler) |
| `/metrics` | keiner (Prometheus-Handler) |

## Admin-Bypass / Skip-Pfad

Wenn `validateLabels` `skip=true` liefert (Admin-Gruppe + `admin.bypass=true`,
oder Labelstore meldet Cluster-Wide-Zugriff), überspringt `handler()` den
kompletten Enforcer-Aufruf (`enforceRequest`) und leitet die Anfrage
unverändert — inkl. eines potenziell "fremden" Tenant-Labels — an den
Upstream weiter (`streamUp`). Siehe
`TestHandler_AdminBypass_SkipsEnforcementAndStreams`.

## Bekannte, bewusst zurückgestellte Lücken

- `WithTLSConfig` (config.go:145) ist nicht unit-testbar, ohne das
  hardcodierte Dateisystem-Layout (`/etc/ssl/ca/ca-certificates.crt`) zu
  mocken; aus Scope-Gründen hier nicht refactored. Architektur-Issue, kein
  Test-Issue.
- `main`/`StartServer` sind laut Auftrag explizit von der Coverage-Zielgröße
  ausgenommen (kein Coverage-Gaming für Prozess-Einstiegspunkte).
