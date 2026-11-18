# Cloudflare workers.dev deployment permissions

Verified on a fresh free Cloudflare account on 2026-10-25 with a 30-day,
account-scoped token granting only **Account → Workers Scripts → Edit**.
No zone, DNS, tunnel, account-write, Workers Routes or separate Durable Objects
permission is required for this deployment path.

Minimum account-scoped token policy for the deployment operations:

| Dashboard label | API permission name | Use |
| --- | --- | --- |
| Account → Workers Scripts → Edit | Workers Scripts Write | Account discovery, subdomain lookup/creation, script + SQLite DO upload, secret, workers.dev route, deletion |

Scope the permission to the selected account, not all accounts. The token link
also pre-fills **Zone → Workers Routes → Edit**, which only the self-hosted
NetBird server's Signal bridge uses (see `docs/netbird-server.md`); the Free
link works without it. Do not request token-creation or permission-edit
capabilities. `Workers Scripts Write` is a broad account-level
permission; do not represent it as limited to Syno Connect Center-created Workers.

| Method | Path after `/client/v4` | Notes |
| --- | --- | --- |
| GET | `/accounts?per_page=50` | Select automatically only when exactly one account is returned; otherwise supply `accountId` |
| GET | `/accounts/{account}/workers/subdomain` | Existing account-wide workers.dev subdomain |
| PUT | `/accounts/{account}/workers/subdomain` | JSON `{"subdomain":"chosen-name"}`; only when absent, never rename an existing one |
| PUT | `/accounts/{account}/workers/scripts/{worker}` | Multipart metadata + JS module; initial `migrations.new_sqlite_classes:["Tunnel"]` |
| PUT | `/accounts/{account}/workers/scripts/{worker}/secrets` | JSON `{name:"RELAY_SECRET",text:secret,type:"secret_text"}` |
| POST | `/accounts/{account}/workers/scripts/{worker}/subdomain` | JSON `{enabled:true,previews_enabled:false}` |
| DELETE | `/accounts/{account}/workers/scripts/{worker}?force=true` | Deletes script with DO binding; delete only owned Workers |

Reproducible acceptance evidence (no credentials or account identifiers are
published): create a 30-day token scoped to one fresh free account with only
Workers Scripts Edit, then connect without `accountId`. `GET /accounts?per_page=50` returned HTTP 200 with exactly one account. The same token
also returned HTTP 200 for `GET /accounts`, `GET /accounts/{account}/workers/subdomain`
and `GET /accounts/{account}/workers/scripts`. Syno Connect Center registered the previously
absent subdomain, uploaded SQLite-backed Workers, enabled their workers.dev
routes and deleted its owned Workers. Header/none/OIDC gateway traffic ran
through the deployed relay. No Account Settings Read, DNS, zone or tunnel
permission was granted. A separately created Worker without Syno Connect Center's ownership
tag survived Off and Disconnect and was then deleted by the test owner.

Syno Connect Center reuses an existing account subdomain. It never renames or deletes it.
Account discovery still refuses zero or several accounts; `apps.workersconnect`
with an explicit `accountId` bypasses discovery and uses the same single permission.
Subdomain-name collision handling was not exercised on the live account.

Sources:

- [Account subdomain GET](https://developers.cloudflare.com/api/resources/workers/subresources/subdomains/methods/get/)
- [Account subdomain PUT](https://developers.cloudflare.com/api/resources/workers/subresources/subdomains/methods/update/)
- [Worker module upload](https://developers.cloudflare.com/api/resources/workers/subresources/scripts/methods/update/)
- [Workers.dev route](https://developers.cloudflare.com/api/resources/workers/subresources/scripts/subresources/subdomain/methods/create/)
- [Secrets](https://developers.cloudflare.com/api/resources/workers/subresources/scripts/subresources/secrets/methods/update/)

- [Account list](https://developers.cloudflare.com/api/resources/accounts/methods/list/)
