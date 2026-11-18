# multena-mcp (Spike)

Benutzer-authentifizierter MCP-Endpoint (Streamable HTTP) vor `multena-proxy`.
Binary: `cmd/multena-mcp`. Der bestehende Proxy-Code ist unverändert.

**Keine Sicherheitsgrenze.** Die Mandantentrennung bleibt im Proxy. `multena-mcp`
prüft das eingehende Token, tauscht es per Keycloak Token Exchange (RFC 8693) auf
`aud=multena` und ruft den Proxy mit dem getauschten Token auf. Das Benutzer-Token
wird nicht weitergereicht, es gibt keinen Service-Account und kein generisches
HTTP-Tool.

## Endpunkte

| Pfad | Zweck |
| --- | --- |
| `/.well-known/oauth-protected-resource` (auch `…/mcp`) | RFC-9728-Metadaten (`resource`, `authorization_servers`) |
| `/mcp` | MCP Streamable HTTP, Bearer-geschützt |
| `/healthz` | Liveness |

Ohne gültiges Token: `401` mit
`WWW-Authenticate: Bearer resource_metadata="<PUBLIC_URL>/.well-known/oauth-protected-resource"`.

## Tokenprüfung (eingehend, aud=`multena-mcp`)

Fail-closed, Regeln aus dem Token-Contract (t_c33696d0 §3):

- Signatur über JWKS (`MULTENA_MCP_JWKS_URL`), Algorithmen fest `RS256`/`ES256`
- `kid` ist Pflicht
- `iss` == `MULTENA_MCP_ISSUER`
- `aud` enthält `MULTENA_MCP_AUDIENCE` (Default `multena-mcp`)
- `exp` ist Pflicht und nicht abgelaufen
- `sub` nicht leer

## Tool

`list_service_instances` (keine Eingabe). Ruft `GET <PROXY_URL>/api/v1/label/si/values`
mit dem getauschten Token auf und liefert `{"service_instances": [...]}`.

## Konfiguration (Umgebungsvariablen)

| Variable | Pflicht | Bedeutung |
| --- | --- | --- |
| `MULTENA_MCP_PUBLIC_URL` | ja | externe Basis-URL, https |
| `MULTENA_MCP_ISSUER` | ja | Keycloak-Realm-Issuer, https |
| `MULTENA_MCP_JWKS_URL` | ja | Keycloak-JWKS-URL, https |
| `MULTENA_MCP_TOKEN_ENDPOINT` | ja | Keycloak-Token-Endpoint, https |
| `MULTENA_MCP_CLIENT_ID` | ja | Client des MCP-Servers (`multena-mcp`) |
| `MULTENA_MCP_CLIENT_SECRET` | ja | Secret des Clients (nur Token-Endpoint) |
| `MULTENA_MCP_PROXY_URL` | ja | multena-proxy-Basis-URL, https |
| `MULTENA_MCP_AUDIENCE` | nein | erwartete `aud` eingehend (Default `multena-mcp`) |
| `MULTENA_MCP_UPSTREAM_AUDIENCE` | nein | `audience` im Exchange (Default `multena`) |
| `MULTENA_MCP_LISTEN_ADDR` | nein | Default `:8090` |

## Keycloak-Anforderungen (nicht geändert, im Realm zu prüfen)

Diese Punkte sind Voraussetzung, im Spike nicht umgesetzt. Die Werte sind Vorschläge,
keine geprüften Istwerte.

1. **Token Exchange** im Realm aktiv und für den Client `multena-mcp` gegenüber Client
   `multena` freigegeben (Permission). Ohne das schlägt der Exchange fehl und der
   Tool-Aufruf liefert einen Fehler. Es gibt keinen Passthrough-Fallback.
2. **Client `multena-mcp`** (confidential, mit Secret), weil der Exchange
   Client-Authentifizierung verlangt [Annahme, im Realm verifizieren].
3. **Audience-Mapper**: Tokens für MCP-Clients tragen `aud=multena-mcp`.
4. **Client `multena`** als Ziel des Exchange; der Exchange-Mapper entfernt die
   Admin-Gruppe, damit es über den MCP keinen Admin-Bypass gibt.
5. **MCP-Clients (Hermes, VS Code, Claude Desktop)**: öffentlicher Client mit
   PKCE S256 und vorregistrierten Redirect-URIs je Client. DCR ist im Spike nicht
   nötig; empfohlen: nein. Die konkreten Redirect-URIs sind je Client zu ermitteln.

## Bekannte Abweichungen / offen

- 401 bei ungültigem Token liefert `WWW-Authenticate: Bearer resource_metadata=…`
  ohne `error="invalid_token"` (RFC 6750 empfiehlt das). Der SDK-Middleware-Standard
  wird verwendet.
- JWKS-Abruf erfolgt beim Start lazy. Ist Keycloak nicht erreichbar, bleibt der
  Dienst up und lehnt alle Aufrufe mit 401 ab (fail closed).
- Kein Caching des Exchange-Ergebnisses. Jeder Tool-Aufruf tauscht neu.
- Manueller Test gegen multena-dev und Client-Kompatibilität sind nicht erledigt
  (siehe Kartenkommentar).

## Tests

```
go test -race ./cmd/multena-mcp/
```

Abgedeckt: PRM-Metadaten, 401 mit `WWW-Authenticate` für fehlendes Token, fehlende
`aud`, falsche `aud`, falsche/fehlende `iss`, abgelaufene/fehlende `exp`, leeres `sub`,
fehlende/unbekannte `kid`, fremder Schlüssel, `alg=HS256`, `alg=none`. Config-Validierung.
End-to-End mit echtem MCP-Client: Exchange mit dem eingehenden Token als Subject,
Upstream-Aufruf mit dem getauschten Token (nicht dem eingehenden), Fehlerpfad bei
verweigertem Exchange.
