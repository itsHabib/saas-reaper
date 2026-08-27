# MongoDB container-backed proof

The product demo compiles and checks generated MongoDB services but does not
boot a MongoDB container. To prove the pack's live store and API semantics,
run the black-box harnesses against a disposable single-node replica set.
The replica set is required: the store publishes a flag definition and its
audit entry in one multi-document transaction, and MongoDB only supports
transactions on a replica set.

From the repository root:

```sh
# 1. Start a disposable single-node replica set (any free host port works).
docker run -d --name reaper-mongo-proof -p 19120:27017 \
  mongo:8.0 mongod --replSet rs0 --bind_ip_all
docker exec reaper-mongo-proof mongosh --quiet \
  --eval 'rs.initiate({_id: "rs0", members: [{_id: 0, host: "127.0.0.1:27017"}]})'

# 2. Generate a MongoDB service.
go run ./cmd/reaper generate \
  --recipe recipes/go-mongodb-docker.yaml --out /tmp/go-mongodb
make -C /tmp/go-mongodb setup check

# 3. Run both harnesses. Each run needs an empty database, so give every run
#    its own database name. directConnection skips replica-set discovery of
#    the container-internal host name.
DATABASE_URL='mongodb://127.0.0.1:19120/reaper_invariants?directConnection=true' \
  bash scripts/invariants.sh /tmp/go-mongodb go 19110
DATABASE_URL='mongodb://127.0.0.1:19120/reaper_conformance?directConnection=true' \
  bash scripts/conformance.sh /tmp/go-mongodb go 19111

# 4. Tear the container down.
docker rm -f reaper-mongo-proof
```

The same two harnesses pass unchanged for the TypeScript and Python MongoDB
packs; substitute the language and a fresh database name per run. The
invariants harness proves stale-revision rejection, exactly one winner under
concurrent creation, token separation in both directions, one newest-first
audit row per successful publish, and definition-plus-audit survival across a
service restart on the same `DATABASE_URL`.
