# CH-Api-GateWay

API Gateway de la plateforme **CustHome**. Point d'entrée HTTP unique qui route les requêtes vers les microservices backend, avec authentification déléguée, limitation de trafic, CORS et observabilité intégrés.

Écrit en Go (bibliothèque standard, dépendances minimales : `yaml.v3`, `golang.org/x/time`, `google/uuid`).

## Sommaire

- [Fonctionnalités](#fonctionnalités)
- [Démarrage rapide](#démarrage-rapide)
- [Configuration](#configuration)
- [Déploiement en production](#déploiement-en-production)
- [Architecture](#architecture)
- [Authentification](#authentification)
- [Codes de réponse du gateway](#codes-de-réponse-du-gateway)
- [Observabilité](#observabilité)
- [Tests](#tests)

## Fonctionnalités

| Fonctionnalité | Description | US |
|---|---|---|
| Routage par préfixe | Reverse proxy vers les backends selon `path_prefix`, avec `strip_prefix` optionnel | US-01 |
| Authentification déléguée | Validation du token Bearer auprès du microservice d'auth (Rust), injection de `X-User-Id` / `X-User-Role` | US-05 |
| Rate limiting | Token Bucket par IP client (`golang.org/x/time/rate`), `/health` exempté | US-08 |
| Timeout backend | 504 si le backend ne répond pas dans le délai configuré | US-09 |
| Logs JSON structurés | `log/slog`, une ligne par requête, verbosité configurable | US-11 |
| Limite de taille de corps | 413 au-delà de `max_body_bytes` | US-12 |
| Proxies de confiance | `X-Forwarded-For` honoré uniquement derrière les proxies listés | US-13 |
| CORS | Origines / méthodes / en-têtes autorisés, cache du preflight | US-15 |
| Correlation ID | Propagation ou génération de `X-Correlation-ID` (UUID) | — |
| Arrêt propre | SIGINT/SIGTERM → drain des connexions (10 s de grâce) | — |

## Démarrage rapide

### Prérequis

- Go ≥ 1.26

### Compilation et lancement

```sh
go build -o bin/gateway ./cmd/gateway
./bin/gateway -config config.yaml
```

Ou directement :

```sh
go run ./cmd/gateway -config config.yaml
```

| Flag | Défaut | Description |
|---|---|---|
| `-config` | `config.yaml` | Chemin du fichier de configuration de routage |

Le gateway écoute alors sur le port configuré (8080 par défaut dans `config.yaml`) :

```sh
curl http://localhost:8080/health
# {"status":"ok"}
```

## Configuration

La configuration est **statique** : lue une seule fois au démarrage, validée strictement (les champs YAML inconnus sont rejetés). Toute erreur de validation empêche le démarrage avec un message explicite.

### Exemple complet

```yaml
environment: development    # development | production (défaut : development)
server:
  port: 8080
  timeout_seconds: 5        # délai max d'une réponse backend (504 au-delà)
  max_body_bytes: 10485760  # taille max du corps de requête (413 au-delà)
  log_level: "INFO"         # DEBUG | INFO | WARN | ERROR
  rate_limit:
    enabled: true
    requests_per_second: 10
    burst: 20
    trusted_proxies: []     # IP ou CIDR ; vide = RemoteAddr fait foi
  cors:
    allowed_origins:
      - "http://localhost:3000"
    allowed_methods: ["GET", "POST", "PUT", "DELETE", "OPTIONS"]
    allowed_headers: ["Authorization", "Content-Type", "X-Correlation-ID"]
    max_age_seconds: 600    # cache du preflight par les navigateurs

auth_service_url: "http://localhost:8081/validate"
auth_service_timeout_ms: 100

routes:
  - path_prefix: "/api/auth"
    destination_url: "http://localhost:8081"
    strip_prefix: true      # /api/auth/login → /login côté backend
    require_auth: false     # routes publiques (login, register…)
  - path_prefix: "/api/users"
    destination_url: "http://localhost:8082"
    strip_prefix: false
    require_auth: true
```

### Référence des champs

#### `server`

| Champ | Type | Défaut | Contraintes |
|---|---|---|---|
| `port` | int | — (requis) | 1–65535 |
| `timeout_seconds` | int | `5` | ≥ 1 |
| `max_body_bytes` | int64 | `10485760` (10 Mio) | ≥ 1 |
| `log_level` | string | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` (insensible à la casse) |

#### `server.rate_limit`

| Champ | Type | Défaut | Contraintes |
|---|---|---|---|
| `enabled` | bool | `false` | — |
| `requests_per_second` | float | — | > 0 si `enabled` |
| `burst` | int | — | ≥ 1 si `enabled` |
| `trusted_proxies` | []string | `[]` | chaque entrée : IP ou CIDR valide |

#### `server.cors`

| Champ | Type | Défaut | Notes |
|---|---|---|---|
| `allowed_origins` | []string | `[]` | `"*"` autorise toutes les origines ; **rejeté au démarrage hors `environment: development`** (voir [Déploiement en production](#déploiement-en-production)) |
| `allowed_methods` | []string | `[]` | comparaison insensible à la casse |
| `allowed_headers` | []string | `[]` | renvoyés tels quels dans le preflight |
| `max_age_seconds` | int | `0` | ≥ 0 ; omis si 0 |

#### Racine

| Champ | Type | Défaut | Notes |
|---|---|---|---|
| `environment` | string | `development` | `development` ou `production` (insensible à la casse) ; pilote le durcissement CORS |
| `auth_service_url` | string | `""` | URL http(s) ; requis si une route a `require_auth: true` |
| `auth_service_timeout_ms` | int | `100` | ≥ 1 |
| `routes` | []route | — | au moins une route requise |

#### `routes[]`

| Champ | Type | Notes |
|---|---|---|
| `path_prefix` | string | doit commencer par `/` ; unique parmi les routes |
| `destination_url` | string | URL http(s) avec hôte |
| `strip_prefix` | bool | retire `path_prefix` avant de proxifier |
| `require_auth` | bool | exige un token Bearer valide |

## Déploiement en production

### Durcissement CORS

Le wildcard CORS `allowed_origins: ["*"]` autorise n'importe quelle origine à appeler l'API : il est **interdit hors développement**.

La validation est **fail-safe** : le wildcard n'est toléré **que** lorsque l'environnement vaut explicitement `development` (le défaut, pour ne pas gêner le dev local). Dès que l'environnement est autre chose (`production`, `staging`, etc.), un wildcard dans `allowed_origins` **fait échouer le démarrage** avec un message explicite — quelle que soit la façon dont l'environnement a été posé.

En développement, si un wildcard est actif, le gateway émet un **`WARN` au démarrage** pour rappeler de poser `environment: production` avant tout déploiement. Ce log ne doit jamais apparaître en environnement déployé.

### Poser l'environnement de production

Deux moyens, dans l'ordre de priorité :

| Moyen | Où | Priorité |
|---|---|---|
| Clé `environment: production` | en tête de `config.yaml` | base |
| Variable `GATEWAY_ENV=production` | environnement du process (compose, manifest k8s, systemd…) | surcharge `config.yaml` |

Recommandation : poser `environment: production` directement dans le `config.yaml` du déploiement de production. Le garde-fou CORS ne dépend alors d'**aucune** variable d'environnement qu'on pourrait oublier. `GATEWAY_ENV` reste disponible pour surcharger ponctuellement (ex. promouvoir une image identique d'un env à l'autre sans toucher au fichier).

Exemple Docker Compose :

```yaml
services:
  gateway:
    image: ch-api-gateway
    environment:
      - GATEWAY_ENV=production
```

Le démarrage échoue (`log.Fatalf`) si la configuration de production embarque un wildcard CORS : corriger `allowed_origins` avec des origines explicites (ou via `CORS_ALLOWED_ORIGINS`) résout le blocage.

### Variables d'environnement reconnues

| Variable | Effet |
|---|---|
| `GATEWAY_ENV` | Surcharge `environment` (ex. `production`) |
| `PORT` | Surcharge `server.port` |
| `AUTH_SERVICE_URL` | Surcharge `auth_service_url` |
| `AUTH_FRONT_URL` | Surcharge `auth_front_url` |
| `CORS_ALLOWED_ORIGINS` | Surcharge `server.cors.allowed_origins` (liste séparée par des virgules) |

## Architecture

```
cmd/gateway/          Point d'entrée : flags, chargement config, logger, signaux
internal/
  app/                Assemblage de la chaîne de middlewares (BuildHandler)
  config/             Chargement, défauts et validation stricte du YAML
  server/             http.Server (timeouts) + arrêt propre (Run)
  health/             GET /health → {"status":"ok"}
  proxy/              ReverseProxy par route, routeur par préfixe, timeout backend
  middleware/         Auth, rate limit, CORS, logs, correlation ID, client IP,
                      max body, strip des en-têtes non fiables
```

### Chaîne de traitement d'une requête

Ordre d'exécution (du plus externe au plus interne, voir `internal/app/app.go`) :

```
Requête entrante
  │
  ├─ CorrelationIDMiddleware    réutilise ou génère X-Correlation-ID
  ├─ IPExtractor.Middleware     résout l'IP client (trusted_proxies / XFF)
  ├─ LoggingMiddleware          log JSON : méthode, path, status, durée, IP…
  ├─ RateLimiter.Middleware     429 si quota dépassé (par IP, /health exempté)
  ├─ MaxBodyBytesMiddleware     413 si corps > max_body_bytes
  ├─ StripUntrustedHeaders      supprime X-User-Id / X-User-Role entrants
  ├─ CORSMiddleware             preflight OPTIONS + en-têtes Allow-Origin
  │
  ├─ GET /health ───────────────► réponse locale {"status":"ok"}
  │
  └─ Routeur (par path_prefix)
       ├─ AuthMiddleware        si require_auth : validation du Bearer token
       ├─ TimeoutMiddleware     contexte limité à timeout_seconds (504)
       └─ ReverseProxy          strip_prefix éventuel, X-Forwarded-*, proxy
                                 │
                                 ▼
                          Backend (destination_url)
```

Le routage utilise `http.ServeMux` : chaque route est enregistrée sur `path_prefix` et `path_prefix/` ; le préfixe le plus long gagne ; tout chemin non routé renvoie **404**.

### Arrêt propre

`SIGINT` / `SIGTERM` déclenchent `http.Server.Shutdown` avec **10 s** de grâce, puis l'exécution des hooks d'arrêt (arrêt de la goroutine de nettoyage du rate limiter).

## Authentification

Pour les routes `require_auth: true` (`internal/middleware/auth.go`) :

1. Les en-têtes entrants `X-User-Id` / `X-User-Role` sont **systématiquement supprimés** (anti-usurpation, défense en profondeur — fait aussi globalement par `StripUntrustedHeadersMiddleware`).
2. Le token est extrait de `Authorization: Bearer <token>` → **401** s'il est absent ou mal formé.
3. Le gateway appelle `GET {auth_service_url}` avec le token (timeout `auth_service_timeout_ms`, `X-Correlation-ID` propagé).
4. Selon la réponse du service d'auth :

| Réponse du service d'auth | Réponse du gateway |
|---|---|
| `200` + JSON `{"user_id": "...", "role": "..."}` | requête proxifiée avec `X-User-Id` (+ `X-User-Role` si présent) |
| `200` mais JSON invalide ou `user_id` vide | `500 Internal Server Error` |
| `401` / `403` | code répliqué tel quel |
| autre code, timeout, erreur réseau | `503 Service Unavailable` |

Le corps de la réponse d'auth est limité à 64 Kio. Le client HTTP réutilise les connexions (pool keep-alive) pour tenir le budget de 100 ms.

## Codes de réponse du gateway

Codes générés par le gateway lui-même (hors réponses des backends) :

| Code | Cause |
|---|---|
| `401 Unauthorized` | Token Bearer absent ou mal formé ; ou rejet par le service d'auth |
| `403 Forbidden` | Rejet par le service d'auth |
| `404 Not Found` | Aucune route ne correspond au chemin |
| `413 Request Entity Too Large` | Corps de requête > `max_body_bytes` |
| `429 Too Many Requests` | Quota du Token Bucket dépassé pour cette IP |
| `500 Internal Server Error` | Réponse du service d'auth inexploitable |
| `502 Bad Gateway` | Backend injoignable ou erreur de proxy |
| `503 Service Unavailable` | Service d'auth injoignable ou en erreur |
| `504 Gateway Timeout` | Backend sans réponse après `timeout_seconds` |

## Observabilité

### Logs

Logs JSON structurés sur stdout (`log/slog`). Une ligne par requête :

```json
{"time":"...","level":"INFO","msg":"HTTP Request","method":"GET","path":"/api/users/42","status":200,"duration":12000000,"bytes":135,"ip":"203.0.113.7","correlation_id":"6f1c..."}
```

### Correlation ID

- L'en-tête `X-Correlation-ID` entrant est réutilisé s'il est valide (≤ 128 caractères, alphanumériques + `.`, `_`, `-`), sinon un UUID v4 est généré.
- Il est renvoyé dans la réponse, propagé au service d'auth et aux backends, et présent dans chaque log.

### Détermination de l'IP client

- Sans `trusted_proxies` : l'adresse TCP (`RemoteAddr`) fait foi — `X-Forwarded-For` est ignoré (anti-spoofing).
- Avec `trusted_proxies` : si la connexion vient d'un proxy de confiance, la chaîne `X-Forwarded-For` est parcourue de droite à gauche et la première IP non fiable est retenue.

Cette IP sert au rate limiting et aux logs.

### Health check

`GET /health` → `200` `{"status":"ok"}`. Répond localement (sans toucher les backends), exempté de rate limiting — adapté aux probes Kubernetes / load balancers.

## Tests

```sh
go test ./...
```

Chaque paquet a son fichier `_test.go` (config, proxy, middlewares, serveur, health, app).
