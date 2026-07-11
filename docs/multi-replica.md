# Multi-replica / high-availability deployments

isrv is a single binary with zero required external services, and that
remains the default: one replica, SQLite, local storage, everything
in-process. Most deployments should stay there.

To be able to scale horizontally across multiple replicas, we need cross-process
state sinchronization mechanisms. This is where cluster mode comes in.

## Cluster mode requirements

Cluster mode deployment needs all of:

1. **Postgres** (`database.type: postgres`) — SQLite is a local file and
   cannot be safely written to by multiple replicas. Concurrent startup is
   safe: golang-migrate's pgx driver serializes schema migrations with its
   own advisory lock.
2. **Shared storage** — `s3` or `gcs`. The `storage.type: local` only works if
   every replica mounts the same volume (e.g. an RWX PVC); We cannot verify whether
   that's the case, so a warning is displayed instead of refusing. Use local mode at
   your own peril.
3. **Redis** (`cluster.redis.address`) — the coordination backend for shared
   rate-limit state. Any single Redis instance works; losing redis connection degrades
   the rate-limit to in-memory (per replica) store (effectively raising limits to N 
   times the original values, where N is the replica count)
4. **A shared `cluster.ipHashSecret`** — HMAC key for hashing client IPs
   before they are used as Redis keys. Every replica must use the *same*
   value.
5. **An explicit `admin.sessionSecret`** — required whenever admin
   credentials are set, so all replicas sign and verify the same session
   cookies.

## Configuration

```yaml
cluster:
  enabled: true
  # shared HMAC key for hashing client IPs before they become Redis keys.
  # Must be identical on every replica.
  ipHashSecret: "generate-a-long-random-string"
  redis:
    # host:port of the Redis server; required when cluster is enabled
    address: "redis.internal:6379"
    password: ""
    db: 0

database:
  type: postgres
  dsn: "postgres://isrv:...@postgres.internal:5432/isrv"

storage:
  type: s3   # or gcs
  bucketName: "my-isrv-bucket"
  region: "eu-central-1"

admin:
  username: "admin"
  password: "change-me"
  # required in cluster mode when admin credentials are set
  sessionSecret: "generate-another-long-random-string"
```

The same settings as environment variables (secrets usually arrive this way):

| Variable | Default | Description |
|----------|---------|-------------|
| `ISRV_CLUSTER_ENABLED` | `false` | Enable multi-replica coordination |
| `ISRV_CLUSTER_IP_HASH_SECRET` | - | Shared HMAC key for hashing client IPs; must be identical on every replica |
| `ISRV_CLUSTER_REDIS_ADDRESS` | - | `host:port` of the Redis server |
| `ISRV_CLUSTER_REDIS_PASSWORD` | - | Redis password, if any |
| `ISRV_CLUSTER_REDIS_DB` | `0` | Redis logical database number |

Plus the non-cluster settings from the recipe: `ISRV_DATABASE_TYPE=postgres`
(with `ISRV_DATABASE_DSN` or the individual connection parameters),
`ISRV_STORAGE_TYPE=s3|gcs`, and `ISRV_ADMIN_SESSION_SECRET`.
